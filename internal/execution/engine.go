package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yelaco/projectx-pilot/internal/lockreview"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/realtime"
	"github.com/yelaco/projectx-pilot/internal/risk"
	"github.com/yelaco/projectx-pilot/internal/signal"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

type Engine struct {
	lastReconcileAttempt, lastReconcileSuccess time.Time
	reconcileError                             string
	protectionCheck                            string
	protectionCheckedAt                        time.Time

	faultLockReview          *faultLockChallenge
	staleQuoteReview         *staleQuoteChallenge
	ready                    chan struct{}
	observer                 func(Status)
	monitorInterval          func() time.Duration
	newsCalendar             NewsCalendar
	managerOperationDays     map[string][]string
	operationDays            []string
	submissionDetail         *SubmissionDetail
	runWanted                bool
	runStartedAt             time.Time
	recoveryFlatAt           time.Time
	recoveryKey              string
	runRequested             bool
	runProfileID, runMessage string
	armedAt                  time.Time
	apiAccount               int64
	paused                   bool
	audit                    func(string, string)
	lastAudit                map[string]string
	mu                       sync.Mutex
	store                    Store
	factory                  Factory
	api                      API
	quotes                   Quotes
	ctx                      context.Context
	config                   Config
	policy                   risk.Profile
	auth                     Authorization
	armedUntil               time.Time
	preview                  *Preview
	intents                  []Intent
	message                  string
	wake                     chan struct{}
	now                      func() time.Time
}

func New(store Store, factory Factory, audit ...func(string, string)) (*Engine, error) {
	rows, err := store.Executions()
	if err != nil {
		return nil, err
	}
	e := &Engine{ready: make(chan struct{}), store: store, factory: factory, wake: make(chan struct{}, 1), now: time.Now, lastAudit: map[string]string{}}
	if len(audit) > 0 {
		e.audit = audit[0]
	}
	for _, r := range rows {
		var i Intent
		if json.Unmarshal([]byte(r.Payload), &i) != nil || i.ID != r.ID || strconv.FormatInt(i.AccountID, 10) != r.Account || i.State != r.State || i.Session != r.Session {
			return nil, errors.New("invalid execution recovery record")
		}
		if i.VerifiedPosition != nil || len(i.ObservedPositions) > 0 {
			i.ExposureObserved = true
		}
		e.intents = append(e.intents, i)
	}
	return e, nil
}
func terminal(state string) bool {
	return state == "closed" || state == "cancelled" || state == "rejected"
}
func (e *Engine) active() *Intent {
	for n := range e.intents {
		if !terminal(e.intents[n].State) {
			return &e.intents[n]
		}
	}
	return nil
}
func (e *Engine) CanConfigure() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.active() == nil }
func (e *Engine) SetFactory(f Factory) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishLocked()
	if e.active() != nil {
		return errors.New("cannot change credentials while execution requires monitoring")
	}
	e.disarm()
	if e.quotes != nil {
		e.quotes.Close()
	}
	e.api = nil
	e.quotes = nil
	e.factory = f
	return nil
}
func (e *Engine) disarm() { e.armedUntil = time.Time{}; e.preview = nil; e.paused = true }
func (e *Engine) Pause() {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishLocked()
	e.disarm()
	e.runRequested = false
	e.runWanted = false
	e.recoveryFlatAt = time.Time{}
	e.runMessage = "Paused"
	e.paused = true
	select {
	case e.wake <- struct{}{}:
	default:
	}
}
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}
func (e *Engine) snapshotLocked() Status {
	s := Status{MonitorRunning: e.ctx != nil && e.ctx.Err() == nil, LastReconcileAttempt: e.lastReconcileAttempt, LastReconcileSuccess: e.lastReconcileSuccess, ReconcileError: e.reconcileError, ProtectionCheck: e.protectionCheck, ProtectionCheckedAt: e.protectionCheckedAt, Quotes: e.quoteStatusLocked(), RecoveryPending: e.runWanted && !e.runRequested, RunRequested: e.runRequested, RunProfileID: e.runProfileID, RunMessage: e.runMessage, Automated: e.config.Automated, AutomationProfileID: e.config.AutomationProfileID, ArmedAt: e.armedAt, Enabled: e.config.Enabled, Armed: e.now().Before(e.armedUntil), AccountID: e.config.AccountID, ArmedUntil: e.armedUntil, Error: e.message, Intents: append([]Intent(nil), e.intents...)}
	if s.RecoveryPending && !strings.Contains(strings.ToLower(s.RunMessage), "recovery") {
		s.RunMessage = "Waiting for recovery · " + s.RunMessage
	}
	if source, ok := e.quotes.(interface {
		Inspect(string) realtime.QuoteStatus
	}); ok {
		s.quoteSource = source
	}
	if e.submissionDetail != nil {
		detail := *e.submissionDetail
		s.SubmissionDetail = &detail
	}
	for n := range s.Intents {
		s.Intents[n].Policy = s.Intents[n].Policy.Clone()
		if s.Intents[n].VerifiedPosition != nil {
			copy := *s.Intents[n].VerifiedPosition
			s.Intents[n].VerifiedPosition = &copy
		}
	}
	if len(s.Intents) > 30 {
		s.Intents = s.Intents[:30]
	}
	if e.preview != nil {
		p := *e.preview
		s.Preview = &p
	}
	return s
}
func (e *Engine) persist(i *Intent) error {
	i.Updated = e.now().UTC()
	b, err := json.Marshal(i)
	if err != nil {
		return err
	}
	err = e.store.SaveExecution(storage.ExecRow{ID: i.ID, Account: strconv.FormatInt(i.AccountID, 10), Session: i.Session, State: i.State, Payload: string(b)})
	if err != nil {
		e.disarm()
		e.message = "execution persistence failed; manage account in TopstepX"
		i.AttentionRequired = true
		if e.audit != nil {
			e.audit("ERROR", fmt.Sprintf("Account #%d: execution persistence failed; position unchanged; review account locally", i.AccountID))
		}
	}
	if err == nil && e.audit != nil && e.lastAudit[i.ID] != i.State+":"+i.Message {
		level := "INFO"
		if i.AttentionRequired || i.State == "uncertain" || i.State == "protective-failure" {
			level = "ERROR"
		}
		e.audit(level, fmt.Sprintf("Account #%d: Order lifecycle: %s. %s", i.AccountID, i.State, i.Message))
		e.lastAudit[i.ID] = i.State + ":" + i.Message
	}
	e.publishLocked()
	return err
}
func (e *Engine) lock(i *Intent, why string) {
	e.disarm()
	if i.HaltKind != "session_close" && i.HaltKind != "daily_limit" {
		e.runRequested = false
		e.runMessage = why
	}
	e.message = why
	i.Message = why
	i.LockReason = why
	if lockreview.IsRisk(why) {
		i.RiskLockReason = why
	}
	if err := e.store.LockExecution(strconv.FormatInt(i.AccountID, 10), i.Session, why); err != nil {
		e.message = "lockout persistence failed; stop service and manage account in TopstepX"
	} else if actual, err := e.store.ExecutionLock(strconv.FormatInt(i.AccountID, 10), i.Session); err == nil && lockreview.IsRisk(actual) {
		i.RiskLockReason = actual
		i.LockReason = actual
	}
}
func (e *Engine) connect(account int64) error {
	if e.api != nil {
		if e.apiAccount != account {
			return errors.New("execution connection belongs to another account")
		}
		return nil
	}
	if e.factory == nil {
		return errors.New("ProjectX credentials required")
	}
	api, quotes, err := e.factory(account)
	if err != nil {
		return err
	}
	if api == nil || quotes == nil {
		return errors.New("execution API and quote source required")
	}
	e.api, e.quotes = api, quotes
	e.apiAccount = account
	return nil
}
func (e *Engine) Arm(ctx context.Context, c Config, p risk.Profile, a Authorization) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishLocked()
	if a.Continuous && (!e.runRequested || e.runProfileID != c.AutomationProfileID) {
		return errors.New("continuous run was paused before session authorization")
	}
	e.disarm()
	if !c.Automated {
		e.runWanted = false
		e.runRequested = false
	}
	if c.Automated && (!a.AutomationConfirmed || c.AutomationProfileID == "" || c.AutomationContractID == "") {
		return errors.New("automatic execution needs local Start confirmation and a pinned profile/contract")
	}
	if e.ctx == nil || e.ctx.Err() != nil {
		return errors.New("execution monitor is not running")
	}
	if e.active() != nil {
		return errors.New("existing intent must be reconciled before arming")
	}
	if !c.Enabled || c.AccountID <= 0 || p.ID != c.TradeManagerID || p.Routing != "single" || !p.HasAccount(c.AccountID) {
		return errors.New("enable execution and assign a single-account Trade Manager")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if !a.PracticeConfirmed || !a.BracketsConfirmed || !finite(c.RoundTripCosts) || c.RoundTripCosts <= 0 || c.SlippageTicks < 1 || c.MaxQuantity < 1 || c.MaxQuantity > 100 || c.MaxDeviationTicks < 0 || c.MaxDeviationTicks > 100 {
		return errors.New("confirm the selected API account, Auto OCO mode and valid cost/slippage limits")
	}
	if !p.PerTradeOnly && (!a.FloorConfirmed || !finite(a.Floor) || a.Floor <= 0) {
		return errors.New("legacy policy requires a verified fixed account loss floor; save the Trade Manager to switch to per-trade-only")
	}
	// First validate the supplied authorization; narrowing must never turn an
	// invalid/unverified input into a valid authorization.
	if err := a.Window.Validate(e.now()); err != nil {
		return err
	}
	restricted, err := p.TradingSchedule().Restrict(a.Window)
	if err != nil {
		return err
	}
	a.Window = restricted
	if err := a.Window.Validate(e.now()); err != nil {
		return err
	}
	why, err := e.store.ExecutionLock(strconv.FormatInt(c.AccountID, 10), a.Window.ID())
	if err != nil {
		return err
	}
	if why != "" {
		return fmt.Errorf("bot local safety lock for this trading session: %s. This is separate from TopstepX account permission", why)
	}
	if e.quotes != nil {
		e.quotes.Close()
	}
	e.api, e.quotes = nil, nil
	if err := e.connect(c.AccountID); err != nil {
		return err
	}
	account, err := e.account(ctx, c.AccountID)
	if err != nil {
		return err
	}
	if !c.Automated && (account.Name != a.AccountName || !strings.HasPrefix(strings.ToUpper(account.Name), "PRAC")) {
		return errors.New("manual Practice tests require the selected PRAC account; use an automation profile for eligible Combine or Express Funded accounts")
	}
	if !p.PerTradeOnly && account.Balance <= a.Floor {
		return errors.New("balance is at or below the verified loss floor")
	}
	positions, err := e.api.OpenPositions(ctx, c.AccountID)
	if err != nil {
		return err
	}
	orders, err := e.api.OpenOrders(ctx, c.AccountID)
	if err != nil {
		return err
	}
	if len(positions) != 0 || len(orders) != 0 {
		return errors.New("account must be flat with no working orders before arming")
	}
	if c.Automated {
		metadata, err := e.api.Contract(ctx, c.AutomationContractID)
		if err != nil {
			return errors.New("automatic contract metadata unavailable")
		}
		if !metadata.ActiveContract {
			return errors.New("selected contract is not active")
		}
	}
	if p.PerTradeOnly {
		a.Floor = 0
		a.FloorConfirmed = false
	}
	e.config, e.policy, e.auth = c, p.Clone(), a
	e.paused = false
	e.armedUntil = e.now().Add(time.Hour)
	if e.armedUntil.After(a.Window.LastEntry) {
		e.armedUntil = a.Window.LastEntry
	}
	e.armedAt = e.now().UTC()
	if c.Automated {
		e.armedUntil = a.Window.LastEntry
		e.quotes.Watch(e.ctx, c.AutomationContractID)
	}
	e.message = ""
	return nil
}
func (e *Engine) account(ctx context.Context, id int64) (projectx.Account, error) {
	accounts, err := e.api.Accounts(ctx)
	if err != nil {
		return projectx.Account{}, err
	}
	for _, a := range accounts {
		if a.ID == id {
			if !a.CanTrade {
				return a, errors.New("ProjectX reports canTrade=false for the selected account")
			}
			if a.Simulated == nil {
				return a, errors.New("ProjectX did not verify the account simulated status")
			}
			if !*a.Simulated {
				return a, errors.New("Live Funded accounts are not supported by Topstep's ProjectX API")
			}
			if !finite(a.Balance) {
				return a, errors.New("account balance is unavailable")
			}

			return a, nil
		}
	}
	return projectx.Account{}, errors.New("pinned account not found")
}
func (e *Engine) check() error {
	if e.ctx == nil || e.ctx.Err() != nil {
		return errors.New("execution monitor is stopping or unavailable")
	}
	if !e.config.Enabled || !e.now().Before(e.armedUntil) {
		return errors.New("execution is disarmed; arm locally first")
	}
	if err := e.auth.Window.Validate(e.now()); err != nil {
		return err
	}
	why, err := e.store.ExecutionLock(strconv.FormatInt(e.config.AccountID, 10), e.auth.Window.ID())
	if err != nil {
		return err
	}
	if why != "" {
		e.disarm()
		return fmt.Errorf("bot local session lock prevents entries: %s", why)
	}
	if err := e.operationAllowed(e.now(), e.config.TradeManagerID); err != nil {
		return err
	}
	if err := e.newsAllowed(e.now(), e.config.NewsFilter); err != nil {
		return err
	}
	if e.active() != nil {
		return errors.New("an order or unresolved execution already exists")
	}
	return nil
}
func (e *Engine) size(ctx context.Context, s signal.Signal, contract string) (risk.SizingPlan, projectx.Contract, error) {
	var empty risk.SizingPlan
	if err := e.check(); err != nil {
		return empty, projectx.Contract{}, err
	}
	// Independent authoritative reads run concurrently. No order mutation is
	// allowed until all complete and the normal risk/freshness checks pass.
	var c projectx.Contract
	var account projectx.Account
	var positions []projectx.Position
	var orders []projectx.Order
	var accountObservedAt time.Time
	var readErrors [4]error
	var reads sync.WaitGroup
	reads.Add(4)
	go func() { defer reads.Done(); c, readErrors[0] = e.api.Contract(ctx, contract) }()
	go func() {
		defer reads.Done()
		account, readErrors[1] = e.account(ctx, e.config.AccountID)
		accountObservedAt = e.now()
	}()
	go func() { defer reads.Done(); positions, readErrors[2] = e.api.OpenPositions(ctx, e.config.AccountID) }()
	go func() { defer reads.Done(); orders, readErrors[3] = e.api.OpenOrders(ctx, e.config.AccountID) }()
	reads.Wait()
	for _, err := range readErrors {
		if err != nil {
			return empty, c, err
		}
	}
	// Automatic alerts use the locally pinned contract; their symbol is a
	// profile routing key, not a broker contract selector.
	if c.ID != contract {
		return empty, c, errors.New("contract metadata does not match selected contract")
	}
	if !e.config.Automated && !strings.HasSuffix(c.SymbolID, "."+s.Symbol) {
		return empty, c, errors.New("contract does not match signal symbol")
	}
	if len(positions) != 0 || len(orders) != 0 {
		return empty, c, errors.New("existing positions or working orders block new entries")
	}
	var history []projectx.Order
	var trades []projectx.Trade
	var err error
	// Per-trade-only policies intentionally do not calculate daily P&L/MLL or
	// trade-count limits. Avoid two irrelevant pre-entry network round trips.
	if !e.policy.PerTradeOnly {
		history, err = e.api.Orders(ctx, e.config.AccountID, e.auth.Window.StartOfSession(), e.now().UTC())
		if err != nil {
			return empty, c, err
		}
		trades, err = e.api.Trades(ctx, e.config.AccountID, e.auth.Window.StartOfSession(), e.now().UTC())
		if err != nil {
			return empty, c, err
		}
	}
	loss := 0.0
	if !e.policy.PerTradeOnly {
		loss, err = sessionLoss(trades)
		if err != nil {
			return empty, c, err
		}
	}
	count := 0
	for _, o := range history {
		if o.FillVolume > 0 && o.ParentOrderID == nil {
			count++
		}
	}
	reserved := 0
	for _, i := range e.intents {
		if i.AccountID == e.config.AccountID && i.Session == e.auth.Window.ID() && i.State != "rejected" {
			reserved++
		}
	}
	if reserved > count {
		count = reserved
	}
	if !e.policy.PerTradeOnly && (loss >= e.policy.MaxDailyLoss || count >= e.policy.MaxTradesPerSession) {
		if err := e.store.LockExecution(strconv.FormatInt(e.config.AccountID, 10), e.auth.Window.ID(), "session loss or trade limit reached"); err != nil {
			return empty, c, err
		}
		e.disarm()
		return empty, c, errors.New("session loss or trade limit reached")
	}
	e.quotes.Watch(e.ctx, contract)
	q, err := e.entryQuote(ctx, contract)
	if err != nil {
		return empty, c, err
	}
	if err := e.check(); err != nil {
		return empty, c, err
	}
	plan, err := risk.Size(e.policy, s, c, risk.SizingInputs{DisablePriceDeviation: e.config.DisablePriceDeviation, Known: true, SessionKnown: true, ObservedAt: accountObservedAt, SessionLoss: loss, LiquidationHeadroom: account.Balance - e.auth.Floor, Trades: count, Positions: len(positions), RoundTripCosts: e.config.RoundTripCosts, SlippageTicks: e.config.SlippageTicks, MaxQuantity: e.config.MaxQuantity, Quote: q.Price, QuoteAt: q.At, MaxDeviationTicks: e.config.MaxDeviationTicks}, e.now(), e.config.maxAge(), 5*time.Second)
	return plan, c, err
}
func sessionLoss(trades []projectx.Trade) (float64, error) {
	loss := 0.0
	seen := map[int64]bool{}
	for _, t := range trades {
		if seen[t.ID] {
			return 0, errors.New("duplicate trade history observations")
		}
		seen[t.ID] = true
		if t.Voided {
			continue
		}
		if t.Fees == nil || t.Commissions == nil || !finite(*t.Fees) || !finite(*t.Commissions) || *t.Fees < 0 || *t.Commissions < 0 {
			return 0, errors.New("authoritative trade fees or commissions unavailable")
		}
		loss += *t.Fees + *t.Commissions
		if t.ProfitAndLoss != nil {
			if !finite(*t.ProfitAndLoss) {
				return 0, errors.New("invalid realized P&L")
			}
			loss += math.Max(0, -*t.ProfitAndLoss)
		}
	}
	if !finite(loss) {
		return 0, errors.New("invalid session loss")
	}
	return loss, nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("secure random unavailable")
	}
	return hex.EncodeToString(b)
}
func (e *Engine) Prepare(ctx context.Context, s signal.Signal, contract string) (Preview, error) {
	return e.PrepareOrder(ctx, s, contract, 1, 0)
}
func (e *Engine) PrepareOrder(ctx context.Context, s signal.Signal, contract string, orderType, quantity int) (Preview, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.preview = nil
	if e.config.Automated {
		return Preview{}, errors.New("pause automatic execution and arm manual mode to use test tickets")
	}
	if (orderType != 1 && orderType != 2) || quantity < 0 || quantity > 100 {
		return Preview{}, errors.New("invalid order type or contract quantity")
	}
	plan, c, err := e.size(ctx, s, contract)
	if err != nil {
		return Preview{}, err
	}
	if quantity > 0 {
		if quantity > plan.Quantity {
			return Preview{}, errors.New("requested contracts exceed risk-compliant quantity")
		}
		plan.Quantity = quantity
		plan.PlannedRisk = float64(quantity) * plan.RiskPerContract
	}
	p := Preview{OrderType: orderType, Token: randomID(), Signal: s, Contract: c, Plan: plan, Expires: e.now().Add(2 * time.Minute)}
	e.preview = &p
	return p, nil
}
func (e *Engine) Submit(ctx context.Context, token string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.preview
	if e.config.Automated {
		return errors.New("manual submissions are unavailable while automatic execution is armed")
	}
	if p == nil || token != p.Token || !e.now().Before(p.Expires) {
		return errors.New("preview expired; request a new preview")
	}
	e.preview = nil
	plan, c, err := e.size(ctx, p.Signal, p.Contract.ID)
	if err != nil {
		return err
	}
	if p.Plan.Quantity > plan.Quantity {
		return errors.New("requested contracts no longer fit risk budget")
	}
	plan.Quantity = p.Plan.Quantity
	plan.PlannedRisk = float64(plan.Quantity) * plan.RiskPerContract
	if plan != p.Plan || c != p.Contract {
		return errors.New("risk or contract inputs changed; request a new preview")
	}
	return e.place(ctx, p, c, plan)
}

// ExecuteAutomation is called only by the local dispatcher after its durable
// delivery claim and approved profile/source/account checks. It is not a webhook.
func (e *Engine) ExecuteAutomation(ctx context.Context, profileID string, s signal.Signal, contract string, orderType int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishLocked()
	if !e.config.Automated || e.config.AutomationProfileID != profileID || e.config.AutomationContractID != contract || (orderType != 1 && orderType != 2) {
		return errors.New("alert does not match the locally armed automation")
	}
	if s.Timestamp.Before(e.armedAt) {
		return errors.New("signal predates this arming; historical alerts cannot execute")
	}
	if t := timing(ctx); t != nil {
		t.ChecksStartedAt = e.now().UTC()
	}
	plan, c, err := e.size(ctx, s, contract)
	if t := timing(ctx); t != nil {
		t.ChecksFinishedAt = e.now().UTC()
	}
	if err != nil {
		return err
	}
	return e.place(ctx, &Preview{OrderType: orderType, Signal: s, Contract: c, Plan: plan}, c, plan)
}
func (e *Engine) place(ctx context.Context, p *Preview, c projectx.Contract, plan risk.SizingPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.check(); err != nil {
		return err
	}
	source := "local-manual"
	profile := ""
	if e.config.Automated {
		source = "tradingview"
		profile = e.config.AutomationProfileID
	}
	i := Intent{Source: source, ProfileID: profile, OrderType: p.OrderType, ID: p.Signal.ID, AccountID: e.config.AccountID, Session: e.auth.Window.ID(), State: "submitting", Tag: "pxbot-" + randomID(), Signal: p.Signal, Contract: c, Plan: plan, Policy: e.policy.Clone(), Config: e.config, Window: e.auth.Window, Floor: e.auth.Floor, Created: e.now().UTC(), Updated: e.now().UTC()}
	if t := timing(ctx); t != nil {
		i.Timing = *t
	}
	b, err := json.Marshal(i)
	if err != nil {
		return err
	}
	if err = e.store.ReserveExecution(storage.ExecRow{ID: i.ID, Account: strconv.FormatInt(i.AccountID, 10), Session: i.Session, State: i.State, Payload: string(b)}); err != nil {
		return errors.New("signal already submitted, account busy or durable reservation failed")
	}
	e.intents = append([]Intent{i}, e.intents...)
	intent := &e.intents[0]
	e.submissionDetail = nil
	side := 0
	if p.Signal.Direction == "SELL" {
		side = 1
	}
	// Once intent is durable, complete the bounded attempt independently of a
	// browser disconnect; startup reconciliation handles process interruption.
	wireCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var limit *float64
	if p.OrderType == 1 {
		price := i.Signal.Entry
		limit = &price
	}
	// Risk/UI retain positive distances; the wire API uses signed price offsets.
	stopOffset, targetOffset := -plan.StopTicks, plan.TargetTicks
	if side == 1 {
		stopOffset, targetOffset = plan.StopTicks, -plan.TargetTicks
	}
	if t := timing(ctx); t != nil {
		t.SubmittedAt = e.now().UTC()
		intent.Timing = *t
	}
	id, err := e.api.Place(wireCtx, projectx.PlaceRequest{AccountID: i.AccountID, ContractID: c.ID, Type: p.OrderType, Side: side, Size: plan.Quantity, LimitPrice: limit, CustomTag: i.Tag, StopLossBracket: projectx.Bracket{Ticks: stopOffset, Type: 4}, TakeProfitBracket: projectx.Bracket{Ticks: targetOffset, Type: 1}})
	if err == nil {
		if t := timing(ctx); t != nil {
			t.AcceptedAt = e.now().UTC()
			intent.Timing = *t
		}
	}
	intent.OrderID = id
	if err != nil {
		intent.SubmissionFailure = safeSubmissionFailure(err)
		var apiError *projectx.APIError
		if errors.As(err, &apiError) && apiError.ProviderReason != "" {
			e.submissionDetail = &SubmissionDetail{IntentID: intent.ID, Reason: apiError.ProviderReason}
		}
		intent.State = "uncertain"
		if errors.Is(err, projectx.ErrAmbiguous) {
			e.lock(intent, "submission did not confirm acceptance; reconciliation required")
		} else {
			intent.Message = "Gateway reported failure; waiting for terminal order reconciliation."
			e.disarm()
		}
		if saveErr := e.persist(intent); saveErr != nil {
			return saveErr
		}
		select {
		case e.wake <- struct{}{}:
		default:
		}
		return fmt.Errorf("%s Submission requires reconciliation; do not resubmit this signal", intent.SubmissionFailure)
	}
	intent.State = "accepted"
	intent.Message = "Gateway accepted the order; fill and protection are not yet verified."
	if err = e.persist(intent); err != nil {
		return err
	}
	select {
	case e.wake <- struct{}{}:
	default:
	}
	return nil
}

// TicketSignal builds a local-only signal from an explicit manual test ticket.
func (e *Engine) TicketSignal(ctx context.Context, strategy, symbol, direction, contract string, orderType int, entry float64, stopTicks, targetTicks int) (signal.Signal, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.check(); err != nil {
		return signal.Signal{}, err
	}
	if stopTicks < 1 || stopTicks > 100000 || targetTicks < 1 || targetTicks > 100000 {
		return signal.Signal{}, errors.New("stop and target must be positive bounded tick distances")
	}
	c, err := e.api.Contract(ctx, contract)
	if err != nil {
		return signal.Signal{}, err
	}
	if orderType == 2 {
		e.quotes.Watch(e.ctx, contract)
		q, err := e.entryQuote(ctx, contract)
		if err != nil {
			return signal.Signal{}, err
		}
		entry = math.Round(q.Price/c.TickSize) * c.TickSize
	}
	sign := 1.0
	if direction == "SELL" {
		sign = -1
	}
	return signal.Signal{ID: "manual-" + randomID(), Strategy: strategy, Symbol: symbol, Direction: direction, Entry: entry, StopLoss: entry - sign*float64(stopTicks)*c.TickSize, TakeProfit: entry + sign*float64(targetTicks)*c.TickSize, Timestamp: e.now().UTC()}, nil
}

// NewWithClock injects time for deterministic risk/session/recovery tests.
func NewWithClock(store Store, factory Factory, clock func() time.Time, audit ...func(string, string)) (*Engine, error) {
	if clock == nil {
		return nil, errors.New("clock required")
	}
	e, err := New(store, factory, audit...)
	if err != nil {
		return nil, err
	}
	e.now = clock
	return e, nil
}

// RequestRun is an explicit, authenticated local Start choice. It is in-memory:
// recovery always starts paused. Session permission is separately reconciled.
func (e *Engine) RequestRun(profile string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishLocked()
	if e.ctx == nil || e.ctx.Err() != nil {
		return errors.New("execution monitor is not running")
	}
	if e.active() != nil {
		return errors.New("reconcile the active execution before starting continuous operation")
	}
	if e.runRequested && e.runProfileID != profile {
		return fmt.Errorf("automation profile %q is running; pause that profile before starting this one", e.runProfileID)
	}
	if e.now().Before(e.armedUntil) && e.config.Automated && e.config.AutomationProfileID != profile {
		return fmt.Errorf("automation profile %q is armed; pause that profile before starting this one", e.config.AutomationProfileID)
	}
	// An explicit Start may replace idle manual permission, never an active intent.
	// Invalidate its preview and permission before the manager reconciles/arms anew.
	if !e.config.Automated {
		e.disarm()
	}

	e.runWanted = true
	e.runStartedAt = e.now().UTC()
	e.recoveryFlatAt = time.Time{}
	e.runRequested = true
	e.runProfileID = profile
	e.runMessage = "Starting; checking account and market hours"
	return nil
}
func (e *Engine) SetRunMessage(message string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if strings.HasPrefix(message, "Running") && !e.runRequested && !e.runWanted {
		return
	}
	defer e.publishLocked()
	e.runMessage = message
}

// entryQuote tolerates stream startup without weakening quote freshness. Mock
// sources may supply Get only; position monitoring deliberately never waits.
func (e *Engine) entryQuote(ctx context.Context, contract string) (realtime.Quote, error) {
	if q, err := e.quotes.Get(contract); err == nil {
		return q, nil
	}
	if source, ok := e.quotes.(interface {
		Wait(context.Context, string) (realtime.Quote, error)
	}); ok {
		waitCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		return source.Wait(waitCtx, contract)
	}
	return e.quotes.Get(contract)
}

// Preserve only fixed descriptions and numeric API codes. Raw server messages,
// request URLs, tokens and order/signal bodies must never enter the audit trail.
func safeSubmissionFailure(err error) string {
	if errors.Is(err, projectx.ErrAmbiguous) {
		return "ProjectX did not confirm the submission result."
	}
	var apiError *projectx.APIError
	if errors.As(err, &apiError) {
		detail := fmt.Sprintf("ProjectX reported a failure (HTTP %d, API code %d).", apiError.Status, apiError.Code)
		if apiError.AutoOCORequired {
			detail += " The selected account uses Position Brackets. Enable Auto OCO Brackets in TopstepX Settings → Risk Settings while the account is flat."
		}
		return detail
	}
	return "Order submission failed without a confirmed result."
}

// quoteStatusLocked only reads existing local caches; it never warms a feed.
func (e *Engine) quoteStatusLocked() realtime.QuoteStatus {
	contract := e.config.AutomationContractID
	if i := e.active(); i != nil {
		contract = i.Contract.ID
	} else if e.preview != nil {
		contract = e.preview.Contract.ID
	}
	if contract == "" {
		return realtime.QuoteStatus{State: "No selected subscription"}
	}
	if inspect, ok := e.quotes.(interface {
		Inspect(string) realtime.QuoteStatus
	}); ok {
		return inspect.Inspect(contract)
	}
	if e.quotes != nil {
		q, err := e.quotes.Get(contract)
		if err == nil {
			return realtime.QuoteStatus{Contract: contract, State: "Cached quote available", At: q.At, Fresh: true}
		}
	}
	return realtime.QuoteStatus{Contract: contract, State: "Cached quote unavailable"}
}
