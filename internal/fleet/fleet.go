// Package fleet coordinates account-isolated execution. A delivery's entire
// routing set is durably claimed before any target is dispatched.
package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/lockreview"
	"github.com/yelaco/projectx-pilot/internal/risk"
	"github.com/yelaco/projectx-pilot/internal/session"
	"github.com/yelaco/projectx-pilot/internal/signal"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

type Plan struct {
	CalendarRevision                       string
	OrderType                              int
	ProfileID, Strategy, ContractID, Scope string
	Instruments                            []string
	Config                                 execution.Config
	Policy                                 risk.Profile
	Floor                                  float64
	ConfirmedAt                            time.Time
}

func (p Plan) Targets() []int64 { return append([]int64(nil), p.Policy.AccountIDs...) }

type View struct {
	LockSession string
	LockReason  string
	LockPolicy  lockreview.Info
	AccountID   int64
	ProfileID   string
	OwnerSince  time.Time
	Busy        bool
	Status      execution.Status
}
type task struct {
	job    storage.TargetJob
	signal signal.Signal
	plan   Plan
}
type runner struct {
	lastWake   atomic.Int64
	id         int64
	engine     *execution.Engine
	owner      string
	since      time.Time
	plan       Plan
	requested  bool
	permission context.Context
	revoke     context.CancelFunc
	queue      chan task
	wake       chan struct{}
	busy       atomic.Bool
	active     atomic.Bool
	status     atomic.Value
	retry      time.Time
}
type Fleet struct {
	ready        chan struct{}
	readyErr     error
	stopping     bool
	mu           sync.Mutex
	store        *storage.Inbox
	factory      execution.Factory
	news         execution.NewsCalendar
	now          func() time.Time
	audit        func(string, string)
	accounts     map[int64]*runner
	ctx          context.Context
	wg           sync.WaitGroup
	paused       bool
	active       atomic.Int64
	dates        []string
	managerDates map[string][]string
}

func New(store *storage.Inbox, factory execution.Factory, news execution.NewsCalendar, now func() time.Time, audit func(string, string)) (*Fleet, error) {
	if now == nil {
		now = time.Now
	}
	f := &Fleet{ready: make(chan struct{}), store: store, factory: factory, news: news, now: now, audit: audit, accounts: map[int64]*runner{}}
	var err error
	f.paused, err = store.FleetPaused()
	if err != nil {
		return nil, err
	}
	rows, err := store.Executions()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var i execution.Intent
		if json.Unmarshal([]byte(row.Payload), &i) != nil || i.ID != row.ID || strconv.FormatInt(i.AccountID, 10) != row.Account || i.State != row.State || i.Session != row.Session {
			return nil, errors.New("invalid fleet recovery record")
		}
		if i.Source != "tradingview" {
			continue
		}
		if i.AccountID <= 0 || i.ProfileID == "" {
			return nil, errors.New("invalid automatic recovery identity")
		}
		r, err := f.ensure(i.AccountID)
		if err != nil {
			return nil, err
		}
		if !terminal(i.State) {
			r.owner = i.ProfileID
			r.since = i.Created
		}
	}
	return f, nil
}

// recoverClaims runs only after the caller owns the process execution lease.
func (f *Fleet) recoverClaims() error {
	rows, err := f.store.Executions()
	if err != nil {
		return err
	}
	intents := map[string]bool{}
	for _, row := range rows {
		intents[row.ID] = true
	}
	jobs, err := f.store.UnfinishedTargets()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		j.State = "interrupted"
		j.Message = "Restart interrupted dispatch; job will never replay. Inspect account execution recovery."
		if intents[j.ID] {
			j.State = "uncertain"
			j.Message = "Durable order intent exists; reconciliation owns recovery. No replay."
		}
		j.FinishedAt = f.now().UTC()
		if err := f.store.SaveTarget(j); err != nil {
			return err
		}
	}
	return nil
}
func terminal(s string) bool { return s == "closed" || s == "rejected" || s == "cancelled" }
func active(s execution.Status) bool {
	for _, i := range s.Intents {
		if !terminal(i.State) {
			return true
		}
	}
	return false
}
func (f *Fleet) ensure(id int64) (*runner, error) {
	if r := f.accounts[id]; r != nil {
		return r, nil
	}
	e, err := execution.NewWithClock(storage.ScopedExecution{Inbox: f.store, Account: strconv.FormatInt(id, 10)}, f.factory, f.now, func(level, message string) {
		if f.audit != nil {
			f.audit(level, fmt.Sprintf("Account #%d: %s", id, message))
		}
	})
	if err != nil {
		return nil, err
	}
	r := &runner{id: id, engine: e, queue: make(chan task, 1), wake: make(chan struct{}, 1)}
	e.Observe(func(s execution.Status) {
		r.status.Store(s)
		on := active(s)
		before := r.active.Swap(on)
		if on && !before {
			f.active.Add(1)
		} else if !on && before {
			f.active.Add(-1)
		}
	})
	e.SetNewsCalendar(f.news)
	if err := e.SetOperationPlan(f.dates, f.managerDates); err != nil {
		return nil, err
	}
	e.SetMonitorInterval(func() time.Duration {
		d := time.Duration(f.active.Load()) * 3 * time.Second
		if d < 5*time.Second {
			d = 5 * time.Second
		}
		return d
	})
	f.accounts[id] = r
	if f.ctx != nil {
		f.launch(r)
	}
	return r, nil
}
func (f *Fleet) launch(r *runner) {
	f.wg.Add(2)
	go func() { defer f.wg.Done(); r.engine.Run(f.ctx) }()
	go func() { defer f.wg.Done(); f.worker(r) }()
}
func (f *Fleet) Run(ctx context.Context) {
	f.mu.Lock()
	f.readyErr = f.recoverClaims()
	if f.readyErr != nil {
		close(f.ready)
		f.mu.Unlock()
		return
	}
	f.ctx = ctx
	close(f.ready)
	for _, r := range f.accounts {
		f.launch(r)
	}
	f.mu.Unlock()
	<-ctx.Done()
	f.mu.Lock()
	f.stopping = true
	f.mu.Unlock()
	f.wg.Wait()
}
func (f *Fleet) Ready() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ctx != nil && !f.stopping && f.ctx.Err() == nil
}
func (f *Fleet) Snapshot() ([]View, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []View{}
	lockSession := session.CurrentTradingDate(f.now())
	for _, r := range f.accounts {
		s, _ := r.status.Load().(execution.Status)
		s.Quotes = s.ReadQuoteStatus()
		reason, err := f.store.ExecutionLock(strconv.FormatInt(r.id, 10), lockSession)
		if err != nil {
			reason = "Local lock status unavailable"
		}
		out = append(out, View{LockSession: lockSession, LockReason: reason, LockPolicy: lockreview.Classify(reason), AccountID: r.id, ProfileID: r.owner, OwnerSince: r.since, Busy: r.busy.Load(), Status: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out, f.paused
}
func (f *Fleet) Start(ctx context.Context, p Plan, manualBusy func(int64) bool) error {
	p.Policy = p.Policy.Clone()
	p.Instruments = append([]string(nil), p.Instruments...)
	if !p.Config.Enabled || p.ProfileID == "" || p.ContractID == "" || p.ConfirmedAt.IsZero() {
		return errors.New("save this profile's trading setup first")
	}
	if err := p.Policy.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	if f.ctx == nil || f.stopping || f.ctx.Err() != nil {
		f.mu.Unlock()
		return errors.New("account runner service is starting; retry Start")
	}
	if f.paused {
		f.mu.Unlock()
		return errors.New("global pause is active; resume entries globally before Start")
	}
	for _, r := range f.accounts {
		s, _ := r.status.Load().(execution.Status)
		if r.owner == p.ProfileID && (active(s) || r.busy.Load()) {
			f.mu.Unlock()
			return errors.New("resolve this profile’s existing account executions before Start")
		}
	}
	targets := []*runner{}
	for _, id := range p.Targets() {
		r, err := f.ensure(id)
		if err != nil {
			f.mu.Unlock()
			return err
		}
		s, _ := r.status.Load().(execution.Status)
		if manualBusy != nil && manualBusy(id) {
			f.mu.Unlock()
			return fmt.Errorf("account #%d has an armed manual test or unresolved manual intent", id)
		}
		if r.owner != "" && r.owner != p.ProfileID && (r.requested || active(s) || r.busy.Load()) {
			f.mu.Unlock()
			return fmt.Errorf("account #%d belongs to profile %q; pause and resolve its execution before switching", id, r.owner)
		}
		if active(s) || r.busy.Load() {
			f.mu.Unlock()
			return fmt.Errorf("account #%d has an active or uncertain execution; reconcile before Start", id)
		}
		targets = append(targets, r)
	}
	started := f.now().UTC()
	for _, r := range targets {
		if r.revoke != nil {
			r.revoke()
		}
		r.permission, r.revoke = context.WithCancel(f.ctx)
		r.owner = p.ProfileID
		r.plan = p
		r.since = started
		r.requested = true
		r.retry = time.Time{}
	}
	f.mu.Unlock()
	// Wakes each independent account immediately. Arming itself places no orders.
	for _, r := range targets {
		if err := r.engine.WaitReady(ctx); err != nil {
			f.PauseProfile(p.ProfileID)
			return err
		}
		if err := r.engine.RequestRun(p.ProfileID); err != nil {
			f.PauseProfile(p.ProfileID)
			return err
		}
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
	return nil
}
func (f *Fleet) PauseProfile(id string) {
	f.mu.Lock()
	targets := []*runner{}
	for _, r := range f.accounts {
		if r.owner == id {
			r.requested = false
			if r.revoke != nil {
				r.revoke()
			}
			targets = append(targets, r)
		}
	}
	f.mu.Unlock()
	for _, r := range targets {
		r.engine.Pause()
	}
}
func (f *Fleet) PauseAccount(id int64) {
	f.mu.Lock()
	r := f.accounts[id]
	if r != nil {
		r.requested = false
		if r.revoke != nil {
			r.revoke()
		}
	}
	f.mu.Unlock()
	if r != nil {
		r.engine.Pause()
	}
}
func (f *Fleet) PauseAll(persist bool) error {
	f.mu.Lock()
	var err error
	if persist {
		f.paused = true
		err = f.store.SetFleetPaused(true)
	}
	targets := []*runner{}
	for _, r := range f.accounts {
		r.requested = false
		if r.revoke != nil {
			r.revoke()
		}
		targets = append(targets, r)
	}
	f.mu.Unlock()
	for _, r := range targets {
		r.engine.Pause()
	}
	return err
}
func (f *Fleet) Resume() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.store.SetFleetPaused(false); err != nil {
		return err
	}
	f.paused = false
	return nil
}
func (f *Fleet) SetOperationPlan(dates []string, managers map[string][]string) error {
	f.mu.Lock()
	f.dates = append([]string(nil), dates...)
	f.managerDates = map[string][]string{}
	for k, v := range managers {
		f.managerDates[k] = append([]string(nil), v...)
	}
	rs := []*runner{}
	for _, r := range f.accounts {
		rs = append(rs, r)
	}
	f.mu.Unlock()
	for _, r := range rs {
		if err := r.engine.SetOperationPlan(dates, managers); err != nil {
			return err
		}
	}
	return nil
}
func (f *Fleet) SetFactory(factory execution.Factory) {
	f.mu.Lock()
	f.factory = factory
	rs := []*runner{}
	for _, r := range f.accounts {
		rs = append(rs, r)
	}
	f.mu.Unlock()
	for _, r := range rs {
		if r.engine.CanConfigure() {
			_ = r.engine.SetFactory(factory)
		}
	}
}
func (f *Fleet) WakeAccount(id int64) {
	f.mu.Lock()
	rs := []*runner{}
	for _, r := range f.accounts {
		if id == 0 || id == r.id {
			rs = append(rs, r)
		}
	}
	f.mu.Unlock()
	for _, r := range rs {
		now := time.Now().UnixNano()
		last := r.lastWake.Load()
		if id == 0 || now-last >= int64(time.Second) {
			r.lastWake.Store(now)
			r.engine.Wake()
		}
	}
}
func (f *Fleet) Flatten(ctx context.Context, profile string, account int64) map[int64]string {
	f.mu.Lock()
	rs := []*runner{}
	for _, r := range f.accounts {
		if (profile == "" || r.owner == profile) && (account == 0 || r.id == account) {
			r.requested = false
			if r.revoke != nil {
				r.revoke()
			}
			rs = append(rs, r)
		}
	}
	f.mu.Unlock()
	out := map[int64]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range rs {
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.engine.Pause()
			s, _ := r.status.Load().(execution.Status)
			message := "Already flat; no managed execution"
			if active(s) {
				if err := r.engine.Flatten(ctx); err != nil {
					message = err.Error()
				} else {
					message = "Flatten requested; awaiting account flatness verification"
				}
			}
			mu.Lock()
			out[r.id] = message
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}
func (f *Fleet) worker(r *runner) {
	if err := r.engine.WaitReady(f.ctx); err != nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-r.wake:
		case <-timer.C:
		case job := <-r.queue:
			f.execute(r, job)
		}
		f.arm(r)
		timer.Reset(5 * time.Second)
	}
}
func (f *Fleet) arm(r *runner) {
	f.mu.Lock()
	s, _ := r.status.Load().(execution.Status)
	if !r.requested || f.paused {
		if !active(s) && !r.busy.Load() {
			r.owner = ""
		}
		f.mu.Unlock()
		return
	}
	p := r.plan
	permission := r.permission
	started := r.since
	if !s.RunRequested && !s.RecoveryPending {
		if s.RunProfileID == r.owner {
			r.requested = false
			if r.revoke != nil {
				r.revoke()
			}
		}
		f.mu.Unlock()
		return
	}
	if s.Armed && f.now().Before(s.ArmedUntil) {
		f.mu.Unlock()
		return
	}
	if active(s) || r.busy.Load() || f.now().Before(r.retry) {
		f.mu.Unlock()
		return
	}
	retryDelay := 15 * time.Second
	if s.RecoveryPending {
		running := 0
		for _, account := range f.accounts {
			if account.requested {
				running++
			}
		}
		if scaled := time.Duration(running) * 3 * time.Second; scaled > retryDelay {
			retryDelay = scaled
		}
	}
	r.retry = f.now().Add(retryDelay)
	f.mu.Unlock()
	w, open, err := p.Policy.TradingSchedule().Window(f.now())
	if err != nil {
		r.engine.SetRunMessage("Running · waiting: " + err.Error())
		return
	}
	if !open {
		r.engine.SetRunMessage("Running · market closed; waiting for the next verified session")
		return
	}
	if !s.RunRequested && s.RecoveryPending {
		check, cancel := context.WithTimeout(permission, 15*time.Second)
		recovered, recoveryErr := r.engine.ResumeAfterRecovery(check, p.ProfileID, w.ID())
		cancel()
		if recoveryErr != nil {
			r.engine.SetRunMessage("Running · waiting for recovery: " + recoveryErr.Error())
			return
		}
		if !recovered {
			r.engine.SetRunMessage("Running · waiting for a second fresh flat/no-order observation")
			return
		}
	}
	locked, err := f.store.ExecutionLock(strconv.FormatInt(r.id, 10), w.ID())
	if err != nil {
		r.engine.Pause()
		return
	}
	if locked != "" {
		if locked == "session loss or trade limit reached" || locked == "daily loss limit reached" {
			r.engine.SetRunMessage("Running · session risk limit; waiting for a verified next session")
		} else {
			r.engine.Pause()
			r.engine.SetRunMessage("Paused · account safety lock requires review: " + locked)
			f.mu.Lock()
			r.requested = false
			if r.revoke != nil {
				r.revoke()
			}
			f.mu.Unlock()
		}
		return
	}
	c := p.Config
	c.AccountID = r.id
	c.Automated = true
	c.AutomationProfileID = p.ProfileID
	c.AutomationContractID = p.ContractID
	policy, policyErr := p.Policy.ForAccount(r.id)
	if policyErr != nil {
		r.engine.SetRunMessage("Running · account risk configuration invalid")
		return
	}
	a := execution.Authorization{Continuous: true, AutomationConfirmed: true, Window: w, Floor: p.Floor, PracticeConfirmed: true, BracketsConfirmed: true, FloorConfirmed: true}
	ctx, cancel := context.WithTimeout(permission, 15*time.Second)
	err = r.engine.Arm(ctx, c, policy, a)
	cancel()
	// Pause or configuration changes may race the fresh network checks in Arm.
	// A late Arm must never restore a permission that the operator just revoked.
	f.mu.Lock()
	current := r.requested && !f.paused && r.owner == p.ProfileID && r.plan.Scope == p.Scope && r.plan.ConfirmedAt.Equal(p.ConfirmedAt) && r.since.Equal(started)
	f.mu.Unlock()
	if !current {
		r.engine.Pause()
		return
	}
	if err != nil {
		r.engine.SetRunMessage("Running · account readiness: " + err.Error())
		return
	}
	r.engine.SetRunMessage("Running · accepting eligible signals")
}
func jobID(profile, delivery string, account int64) string {
	sum := sha256.Sum256([]byte(profile + "\x00" + delivery + "\x00" + strconv.FormatInt(account, 10)))
	return "fanout-" + hex.EncodeToString(sum[:])
}

// Dispatch never waits for a network request. Claim and bounded scheduling are
// serialized; all account HTTP work is independent of this global mutex.
func (f *Fleet) Dispatch(record storage.Record, p Plan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := p.Policy.Validate(); err != nil {
		return err
	}
	received, err := time.Parse(time.RFC3339Nano, record.ReceivedAt)
	if err != nil {
		return errors.New("invalid inbox receipt timestamp")
	}
	policy, _ := json.Marshal(p)
	jobs := make([]storage.TargetJob, 0, len(p.Policy.AccountIDs))
	tasks := map[int64]*runner{}
	for _, account := range p.Targets() {
		j := storage.TargetJob{ID: jobID(p.ProfileID, record.Signal.ID, account), DeliveryID: record.Signal.ID, ProfileID: p.ProfileID, ContractID: p.ContractID, AccountID: account, Policy: string(policy), ReceivedAt: received, ClaimedAt: f.now().UTC()}
		j.RoutingMode = p.Policy.DistributionMode()
		jobs = append(jobs, j)
		tasks[account] = f.accounts[account]
	}
	if p.Policy.DistributionMode() == "round_robin" {
		eligible := map[int64]bool{}
		for _, job := range jobs {
			r := tasks[job.AccountID]
			eligible[job.AccountID] = f.dispatchBlockReason(r, record, p, received) == "" && r != nil && !r.busy.Load()
		}
		selected, claimErr := f.store.ClaimRoundRobin(p.Policy.ID, jobs, eligible)
		if errors.Is(claimErr, storage.ErrDuplicate) {
			return nil
		}
		if claimErr != nil {
			return claimErr
		}
		if selected.AccountID == 0 {
			return nil
		} // Durable blocked outcome; never queued/replayed.
		jobs = []storage.TargetJob{selected}
	} else if err := f.store.ClaimTargets(jobs); err != nil {
		if errors.Is(err, storage.ErrDuplicate) {
			return nil
		}
		return err
	}
	for _, j := range jobs {
		r := tasks[j.AccountID]
		reason := f.dispatchBlockReason(r, record, p, received)
		if reason != "" {
			j.State = "blocked"
			j.Message = reason
			j.FinishedAt = f.now().UTC()
			if err := f.store.SaveTarget(j); err != nil {
				return err
			}
			continue
		}
		if !r.busy.CompareAndSwap(false, true) {
			j.State = "blocked"
			j.Message = "Account runner is busy; alert will not replay"
			j.FinishedAt = f.now().UTC()
			if err := f.store.SaveTarget(j); err != nil {
				return err
			}
			continue
		}
		select {
		case r.queue <- task{job: j, signal: record.Signal, plan: p}:
		default:
			r.busy.Store(false)
			j.State = "blocked"
			j.Message = "Account queue full; no delayed replay"
			j.FinishedAt = f.now().UTC()
			if err := f.store.SaveTarget(j); err != nil {
				return err
			}
		}
	}
	return nil
}

// Caller holds f.mu. Snapshot readiness only selects a target; authoritative
// broker/risk checks still run immediately before that target can submit.
func (f *Fleet) dispatchBlockReason(r *runner, record storage.Record, p Plan, received time.Time) string {
	reason := ""
	age := p.Config.SignalMaxAge
	if age <= 0 {
		age = 5 * time.Minute
	}
	if err := record.Signal.ValidateSchema(f.now(), age); err != nil {
		reason = err.Error()
	}
	allowed := false
	for _, sym := range p.Instruments {
		if sym == record.Signal.Symbol {
			allowed = true
		}
	}
	if record.Status != "pending_review" || record.AccountID != "" || record.ContractID != "" {
		reason = "Delivery is not an unassigned webhook alert"
	}
	if record.ProfileID != p.ProfileID || record.Signal.Strategy != p.Strategy || !allowed {
		reason = "Signal does not match the frozen profile strategy/instrument"
	}
	if f.paused {
		reason = "Global entry pause was active when this delivery was claimed"
	}
	if r == nil || !r.requested || r.owner != p.ProfileID || r.plan.Scope != p.Scope {
		reason = "Profile/account was not started with this exact scope; no delayed replay"
	} else {
		s, _ := r.status.Load().(execution.Status)
		if !s.MonitorRunning || !s.RunRequested || !s.Armed || !f.now().Before(s.ArmedUntil) || received.Before(r.since) || received.Before(s.ArmedAt) || record.Signal.Timestamp.Before(r.since) || received.After(f.now().Add(time.Second)) {
			reason = "Delivery arrived before arming or outside an eligible session; no delayed replay"
		}
		if active(s) {
			reason = "Account already has a working or unresolved execution"
		}
	}
	return reason
}

func (f *Fleet) execute(r *runner, t task) {
	defer r.busy.Store(false)
	j := t.job
	j.DispatchedAt = f.now().UTC()
	trace := &execution.Timing{DeliveryID: j.DeliveryID, ReceivedAt: j.ReceivedAt, DispatchedAt: j.DispatchedAt}
	f.mu.Lock()
	permission := r.permission
	valid := r.requested && !f.paused && r.owner == j.ProfileID && r.plan.Scope == t.plan.Scope && r.plan.ConfirmedAt.Equal(t.plan.ConfirmedAt) && !j.ReceivedAt.Before(r.since)
	f.mu.Unlock()
	if !valid {
		j.State = "blocked"
		j.Message = "Execution paused or scope changed before dispatch"
	} else {
		s := t.signal
		s.ID = j.ID
		typ := t.plan.OrderType
		ctx, cancel := context.WithTimeout(permission, 20*time.Second)
		err := r.engine.ExecuteAutomation(execution.WithTiming(ctx, trace), j.ProfileID, s, j.ContractID, typ)
		cancel()
		j.State = "submitted"
		j.Message = "ProjectX accepted this account's order; follow fill/protection monitoring"
		if err != nil {
			j.State = "blocked"
			j.Message = err.Error()
			s, _ := r.status.Load().(execution.Status)
			for _, i := range s.Intents {
				if i.ID == j.ID {
					j.State = "uncertain"
					j.Message = "Durable intent exists; reconciliation required. Never retry this target."
				}
			}
		}
	}
	j.ChecksAt = trace.ChecksStartedAt
	j.SubmitAt = trace.SubmittedAt
	j.AcceptedAt = trace.AcceptedAt
	j.FinishedAt = f.now().UTC()
	if err := f.store.SaveTarget(j); err != nil {
		r.engine.Pause()
		f.mu.Lock()
		r.requested = false
		if r.revoke != nil {
			r.revoke()
		}
		f.mu.Unlock()
		if f.audit != nil {
			f.audit("ERROR", "Target outcome persistence failed; account paused")
		}
	}
}

func (f *Fleet) WaitReady(ctx context.Context) error {
	select {
	case <-f.ready:
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.readyErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *Fleet) ReviewOCOLock(ctx context.Context, account int64, intent string) error {
	f.mu.Lock()
	r := f.accounts[account]
	f.mu.Unlock()
	if r == nil {
		return errors.New("automatic account runner not found")
	}
	return r.engine.ReviewOneWayOCOLock(ctx, account, intent)
}
func (f *Fleet) ReviewStaleQuoteLock(ctx context.Context, account int64, intent string, confirmation ...string) error {
	f.mu.Lock()
	r := f.accounts[account]
	f.mu.Unlock()
	if r == nil {
		return errors.New("automatic account runner not found")
	}
	return r.engine.ReviewStaleQuoteLock(ctx, account, intent, confirmation...)
}

func (f *Fleet) ReviewFaultLock(ctx context.Context, account int64, intent, confirmation string) error {
	f.mu.Lock()
	r := f.accounts[account]
	f.mu.Unlock()
	if r == nil {
		return errors.New("automatic account runner not found")
	}
	return r.engine.ReviewFaultLock(ctx, account, intent, confirmation)
}
