package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/realtime"
	"github.com/yelaco/projectx-pilot/internal/risk"
	"github.com/yelaco/projectx-pilot/internal/session"
	"github.com/yelaco/projectx-pilot/internal/signal"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

type gateway struct {
	mu              sync.Mutex
	readHook        func(context.Context, int64) error
	orders, history map[int64][]projectx.Order
	positions       map[int64][]projectx.Position
	placed          map[int64]int
	closed          map[int64]int
	requests        map[int64]projectx.PlaceRequest
	blocked         int64
	release         chan struct{}
	now             func() time.Time
	next            int64
}
type accountAPI struct {
	g       *gateway
	account int64
}

func (a accountAPI) Accounts(ctx context.Context) ([]projectx.Account, error) {
	a.g.mu.Lock()
	hook := a.g.readHook
	a.g.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, a.account); err != nil {
			return nil, err
		}
	}
	s := true
	return []projectx.Account{{ID: 1, Name: "Combine", CanTrade: true, Simulated: &s, Balance: 50000}, {ID: 2, Name: "Express Funded", CanTrade: true, Simulated: &s, Balance: 50000}, {ID: 3, Name: "Practice", CanTrade: true, Simulated: &s, Balance: 50000}}, nil
}
func (a accountAPI) Contract(context.Context, string) (projectx.Contract, error) {
	return projectx.Contract{ID: "mnq", SymbolID: "F.US.MNQ", TickSize: .25, TickValue: .5, ActiveContract: true}, nil
}
func (a accountAPI) OpenOrders(context.Context, int64) ([]projectx.Order, error) {
	a.g.mu.Lock()
	defer a.g.mu.Unlock()
	return append([]projectx.Order{}, a.g.orders[a.account]...), nil
}
func (a accountAPI) OpenPositions(context.Context, int64) ([]projectx.Position, error) {
	a.g.mu.Lock()
	defer a.g.mu.Unlock()
	return append([]projectx.Position{}, a.g.positions[a.account]...), nil
}
func (a accountAPI) Orders(context.Context, int64, time.Time, time.Time) ([]projectx.Order, error) {
	a.g.mu.Lock()
	defer a.g.mu.Unlock()
	return append([]projectx.Order{}, a.g.history[a.account]...), nil
}
func (a accountAPI) Trades(context.Context, int64, time.Time, time.Time) ([]projectx.Trade, error) {
	return []projectx.Trade{}, nil
}
func (a accountAPI) Place(ctx context.Context, r projectx.PlaceRequest) (int64, error) {
	g := a.g
	g.mu.Lock()
	g.placed[a.account]++
	g.requests[a.account] = r
	blocked := g.blocked == a.account
	g.next++
	id := g.next
	parent := projectx.Order{ID: id, AccountID: a.account, ContractID: r.ContractID, Type: r.Type, Side: r.Side, Size: r.Size, CustomTag: r.CustomTag, Status: 1}
	g.history[a.account] = append(g.history[a.account], parent)
	g.orders[a.account] = append(g.orders[a.account], parent)
	g.mu.Unlock()
	if blocked {
		select {
		case <-g.release:
		case <-ctx.Done():
		}
		return 0, projectx.ErrAmbiguous
	}
	return id, nil
}
func (a accountAPI) Cancel(_ context.Context, id int64) error {
	g := a.g
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []projectx.Order{}
	for _, o := range g.orders[a.account] {
		if o.ID != id {
			out = append(out, o)
		}
	}
	g.orders[a.account] = out
	for n, o := range g.history[a.account] {
		if o.ID == id {
			g.history[a.account][n].Status = 3
		}
	}
	return nil
}
func (a accountAPI) CloseContract(context.Context, string) error {
	a.g.mu.Lock()
	defer a.g.mu.Unlock()
	a.g.positions[a.account] = nil
	a.g.closed[a.account]++
	return nil
}

type quotes struct{ now func() time.Time }

func (q quotes) Watch(context.Context, string) {}
func (q quotes) Close()                        {}
func (q quotes) Get(string) (realtime.Quote, error) {
	return realtime.Quote{Price: 20000, At: q.now()}, nil
}
func setup(t *testing.T) (*Fleet, *storage.Inbox, *gateway, context.Context) {
	t.Helper()
	start := time.Now()
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, session.Chicago)
	now := func() time.Time { return base.Add(time.Since(start)) }
	store, err := storage.Open(filepath.Join(t.TempDir(), "execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	g := &gateway{orders: map[int64][]projectx.Order{}, history: map[int64][]projectx.Order{}, positions: map[int64][]projectx.Position{}, placed: map[int64]int{}, closed: map[int64]int{}, requests: map[int64]projectx.PlaceRequest{}, now: now, next: 100, release: make(chan struct{})}
	factory := func(account int64) (execution.API, execution.Quotes, error) {
		return accountAPI{g, account}, quotes{now}, nil
	}
	f, err := New(store, factory, nil, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.Run(ctx) }()
	if err := f.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("runners did not shut down")
		}
		store.Close()
	})
	return f, store, g, ctx
}
func plan(id string, accounts ...int64) Plan {
	p := risk.Defaults()
	p.PerTradeOnly = true
	p.ID = "manager-" + id
	p.Name = id
	p.AccountIDs = accounts
	if len(accounts) > 1 {
		p.Routing = "multiple"
	}
	return Plan{ProfileID: id, Strategy: id, ContractID: "mnq", Scope: "scope-" + id, Instruments: []string{"MNQ"}, OrderType: 1, Config: execution.Config{Enabled: true, NewsFilter: "off", SignalMaxAge: 5 * time.Minute, TradeManagerID: p.ID, MaxQuantity: 5, RoundTripCosts: 3, SlippageTicks: 2, MaxDeviationTicks: 4}, Policy: p, ConfirmedAt: time.Now().UTC()}
}
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("condition timed out")
		case <-tick.C:
		}
	}
}
func armed(f *Fleet, n int) bool {
	views, _ := f.Snapshot()
	count := 0
	for _, v := range views {
		if v.Status.Armed {
			count++
		}
	}
	return count == n
}
func record(f *Fleet, p Plan, id string) storage.Record {
	now := f.now().UTC()
	return storage.Record{ProfileID: p.ProfileID, Status: "pending_review", ReceivedAt: now.Format(time.RFC3339Nano), Signal: signal.Signal{ID: id, Strategy: p.Strategy, Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19990, TakeProfit: 20020, Timestamp: now}}
}

// gatedPlacement forces the fast target to finish before the slow target reaches
// the gateway, making the scheduler ordering behind the original failure explicit.
type gatedPlacement struct {
	execution.API
	gate <-chan struct{}
}

func (a gatedPlacement) Place(ctx context.Context, request projectx.PlaceRequest) (int64, error) {
	select {
	case <-a.gate:
		return a.API.Place(ctx, request)
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func TestMultiAccountFanoutIsConcurrentAndIdempotent(t *testing.T) {
	f, store, g, ctx := setup(t)
	// Release before setup's shutdown cleanup, including when an assertion fails.
	var releaseOnce, entryOnce sync.Once
	entryGate := make(chan struct{})
	allowFirstEntry := func() { entryOnce.Do(func() { close(entryGate) }) }
	unblock := func() { releaseOnce.Do(func() { close(g.release) }) }
	t.Cleanup(func() { allowFirstEntry(); unblock() })
	factory := f.factory
	f.SetFactory(func(account int64) (execution.API, execution.Quotes, error) {
		api, quotes, err := factory(account)
		if account == 1 && err == nil {
			api = gatedPlacement{API: api, gate: entryGate}
		}
		return api, quotes, err
	})
	p := plan("swift", 1, 2)
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 2) })
	g.mu.Lock()
	g.blocked = 1
	g.mu.Unlock()
	delivery := record(f, p, "signal-one")
	// Disk and scheduler latency are not a dispatch SLA. Prove dispatch returns
	// while account 1 remains blocked rather than imposing a 100ms wall-clock cap.
	dispatched := make(chan error, 1)
	go func() { dispatched <- f.Dispatch(delivery, p) }()
	select {
	case err := <-dispatched:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dispatch did not return while a gateway submission was blocked")
	}
	waitFor(t, func() bool {
		jobs, err := store.TargetJobs()
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range jobs {
			if j.AccountID == 2 && j.State == "submitted" {
				return true
			}
		}
		return false
	})
	// Deliberately reproduce the old test's faulty assumption: account 2 has
	// completed, but account 1 has not reached the gateway yet.
	g.mu.Lock()
	earlyFirst := g.placed[1]
	g.mu.Unlock()
	if earlyFirst != 0 {
		t.Fatal("test placement gate failed")
	}
	allowFirstEntry()
	waitFor(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.placed[1] > 0 && g.placed[2] > 0
	})
	// Account 1 now remains blocked in submission; account 2 already completed.
	g.mu.Lock()
	first, second := g.placed[1], g.placed[2]
	g.mu.Unlock()
	if first != 1 || second != 1 {
		t.Fatalf("missing target: account 1 attempts=%d, account 2 attempts=%d", first, second)
	}
	if err := f.Dispatch(delivery, p); err != nil {
		t.Fatal(err)
	}
	unblock()
	waitFor(t, func() bool {
		jobs, _ := store.TargetJobs()
		if len(jobs) != 2 {
			return false
		}
		for _, j := range jobs {
			if j.State == "claimed" {
				return false
			}
		}
		return true
	})
	jobs, _ := store.TargetJobs()
	for _, j := range jobs {
		if j.AccountID == 1 && j.State != "uncertain" {
			t.Fatal("ambiguous target was not isolated", j.State)
		}
		if j.AccountID == 2 && (j.State != "submitted" || j.AcceptedAt.IsZero() || j.AcceptanceMS() < 0) {
			t.Fatal("acceptance trace missing")
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.placed[1] != 1 || g.placed[2] != 1 {
		t.Fatal("duplicate submission")
	}
}
func TestIndependentProfilesAndOwnership(t *testing.T) {
	f, _, g, ctx := setup(t)
	a, b := plan("mnq", 1), plan("gold", 2)
	if err := f.Start(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 2) })
	if err := f.Start(ctx, plan("conflict", 1, 3), nil); err == nil {
		t.Fatal("overlapping account ownership allowed")
	}
	views, _ := f.Snapshot()
	for _, v := range views {
		if v.AccountID == 3 && v.ProfileID != "" {
			t.Fatal("partial start reserved an unowned target")
		}
	}
	f.PauseProfile(a.ProfileID)
	if err := f.Dispatch(record(f, a, "paused-alert"), a); err != nil {
		t.Fatal(err)
	}
	if err := f.Dispatch(record(f, b, "live-alert"), b); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.placed[2] == 1 })
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.placed[1] != 0 {
		t.Fatal("pause leaked or old alert executed")
	}
}
func TestGlobalPausePersistenceAndNoBacklogReplay(t *testing.T) {
	f, store, g, ctx := setup(t)
	p := plan("swift", 1)
	// Alerts received before Start are permanently consumed as blocked.
	early := record(f, p, "before-start")
	if err := f.Dispatch(early, p); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 1) })
	if err := f.Dispatch(early, p); err != nil {
		t.Fatal(err)
	}
	if err := f.PauseAll(true); err != nil {
		t.Fatal(err)
	}
	if paused, err := store.FleetPaused(); err != nil || !paused {
		t.Fatal("global pause not durable")
	}
	if err := f.Start(ctx, p, nil); err == nil {
		t.Fatal("global pause bypassed")
	}
	if err := f.Resume(); err != nil {
		t.Fatal(err)
	}
	if armed(f, 1) {
		t.Fatal("resume armed a profile")
	}
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 1) })
	if err := f.Dispatch(early, p); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.placed[1] != 0 {
		t.Fatal("pre-arming alert replayed")
	}
}
func TestRecoveryConsumesClaimAndPreservesAccountOwnership(t *testing.T) {
	f, store, g, ctx := setup(t)
	p := plan("swift", 1)
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 1) })
	delivery := record(f, p, "restart-test")
	if err := f.Dispatch(delivery, p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { jobs, _ := store.TargetJobs(); return len(jobs) == 1 && jobs[0].State == "submitted" })
	claimed := storage.TargetJob{ID: "interrupted-job", DeliveryID: "interrupted", ProfileID: "swift", AccountID: 2, ContractID: "mnq", ClaimedAt: f.now()}
	if err := store.ClaimTargets([]storage.TargetJob{claimed}); err != nil {
		t.Fatal(err)
	}
	// Constructing recovery performs no API mutations or restoration of arming.
	next, err := New(store, f.factory, nil, f.now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.recoverClaims(); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.DeliveryTargets("interrupted")
	if len(jobs) != 1 || jobs[0].State != "interrupted" {
		t.Fatal("claim was replayable")
	}
	views, _ := next.Snapshot()
	for _, v := range views {
		if v.Status.Armed || v.Status.RunRequested {
			t.Fatal("recovery armed")
		}
		if v.AccountID == 1 && v.ProfileID != "swift" {
			t.Fatal("lost unresolved ownership")
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.placed[1] != 1 {
		t.Fatal("recovery transmitted")
	}
	if !errors.Is(store.ClaimTargets([]storage.TargetJob{claimed}), storage.ErrDuplicate) {
		t.Fatal("interrupted target could be reclaimed")
	}
}

func TestConcurrentStartOwnsAccountAtomically(t *testing.T) {
	f, _, _, ctx := setup(t)
	results := make(chan error, 2)
	go func() { results <- f.Start(ctx, plan("a", 1), nil) }()
	go func() { results <- f.Start(ctx, plan("b", 1), nil) }()
	successes := 0
	for n := 0; n < 2; n++ {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("overlapping concurrent starts", successes)
	}
}

func TestTargetFailureAndManualConflictAreIsolated(t *testing.T) {
	f, store, g, ctx := setup(t)
	p := plan("swift", 1, 2)
	if err := f.Start(ctx, p, func(id int64) bool { return id == 2 }); err == nil {
		t.Fatal("manual ownership ignored")
	}
	views, _ := f.Snapshot()
	for _, v := range views {
		if v.ProfileID != "" {
			t.Fatal("partial ownership on rejected Start")
		}
	}
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 2) })
	f.PauseAccount(1)
	delivery := record(f, p, "partial")
	if err := f.Dispatch(delivery, p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		rows, _ := store.DeliveryTargets(delivery.Signal.ID)
		return len(rows) == 2 && rows[0].State == "blocked" && rows[1].State == "submitted"
	})
	g.mu.Lock()
	if g.placed[1] != 0 || g.placed[2] != 1 {
		t.Fatal("target isolation failed")
	}
	g.mu.Unlock()
	// A different routing set can never expand a previously claimed delivery.
	changed := plan("swift", 1, 2, 3)
	if err := f.Dispatch(delivery, changed); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.DeliveryTargets(delivery.Signal.ID)
	if len(rows) != 2 {
		t.Fatal("frozen targets expanded")
	}
	// Pausing a working account does not transfer its ownership.
	f.PauseProfile(p.ProfileID)
	if err := f.Start(ctx, plan("other", 2), nil); err == nil {
		t.Fatal("working account ownership stolen")
	}
}

func TestConfigurationReadDoesNotConsumeRunningClaims(t *testing.T) {
	f, store, _, _ := setup(t)
	job := storage.TargetJob{ID: "pending", DeliveryID: "pending", ProfileID: "p", AccountID: 1}
	if err := store.ClaimTargets([]storage.TargetJob{job}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(store, f.factory, nil, f.now, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := store.UnfinishedTargets()
	if err != nil || len(rows) != 1 {
		t.Fatal("constructor consumed a live process's claim", rows, err)
	}
}

func TestProtectiveFailureDoesNotFlattenAnotherAccount(t *testing.T) {
	f, store, g, ctx := setup(t)
	p := plan("swift", 1, 2)
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 2) })
	delivery := record(f, p, "protection-isolation")
	if err := f.Dispatch(delivery, p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		rows, _ := store.DeliveryTargets(delivery.Signal.ID)
		return len(rows) == 2 && rows[0].State == "submitted" && rows[1].State == "submitted"
	})
	// Account 1 fills without any protective children. Account 2 stays working.
	g.mu.Lock()
	parent := g.history[1][0]
	parent.FillVolume = parent.Size
	parent.Status = 2
	g.history[1][0] = parent
	g.positions[1] = []projectx.Position{{AccountID: 1, ContractID: "mnq", Size: parent.Size, Type: 1, AveragePrice: 20000}}
	g.orders[1] = nil
	g.mu.Unlock()
	f.WakeAccount(1)
	waitFor(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.closed[1] == 1 })
	g.mu.Lock()
	otherClosed, otherOrders := g.closed[2], len(g.orders[2])
	g.mu.Unlock()
	if otherClosed != 0 || otherOrders != 1 {
		t.Fatal("another account was flattened or cancelled")
	}
	w, _, err := p.Policy.TradingSchedule().Window(f.now())
	if err != nil {
		t.Fatal(err)
	}
	reason, err := store.ExecutionLock("1", w.ID())
	if err != nil || reason == "" {
		t.Fatal("failed account not locked", reason, err)
	}
	reason, err = store.ExecutionLock("2", w.ID())
	if err != nil || reason != "" {
		t.Fatal("another account locked", reason, err)
	}
}

func TestPauseCancelsPreflightBeforeDurableSubmission(t *testing.T) {
	f, store, g, ctx := setup(t)
	p := plan("swift", 1)
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 1) })
	entered := make(chan struct{}, 1)
	g.mu.Lock()
	g.readHook = func(ctx context.Context, _ int64) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	g.mu.Unlock()
	delivery := record(f, p, "cancel-preflight")
	if err := f.Dispatch(delivery, p); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("preflight never started")
	}
	paused := make(chan struct{})
	go func() { f.PauseProfile(p.ProfileID); close(paused) }()
	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("Pause did not cancel preflight")
	}
	waitFor(t, func() bool {
		rows, _ := store.DeliveryTargets(delivery.Signal.ID)
		return len(rows) == 1 && rows[0].State == "blocked"
	})
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.placed[1] != 0 {
		t.Fatal("paused preflight submitted")
	}
	rows, err := store.Executions()
	if err != nil || len(rows) != 0 {
		t.Fatal("paused preflight reserved an intent", rows, err)
	}
}

func TestCorruptRecoveryAccountFailsClosed(t *testing.T) {
	f, store, _, _ := setup(t)
	intent := execution.Intent{ID: "bad", AccountID: 2, Source: "tradingview", ProfileID: "p", Session: "session", State: "accepted"}
	payload, _ := json.Marshal(intent)
	if err := store.ReserveExecution(storage.ExecRow{ID: "bad", Account: "1", Session: "session", State: "accepted", Payload: string(payload)}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(store, f.factory, nil, f.now, nil); err == nil {
		t.Fatal("mismatched account recovery silently omitted")
	}
}

type contractMetadataAPI struct {
	execution.API
	metadata projectx.Contract
}

func (a contractMetadataAPI) Contract(context.Context, string) (projectx.Contract, error) {
	return a.metadata, nil
}

func TestManagersSelectWindowsIndependentlyOfInstrumentNames(t *testing.T) {
	f, store, g, ctx := setup(t)
	factory := f.factory
	f.SetFactory(func(account int64) (execution.API, execution.Quotes, error) {
		api, q, err := factory(account)
		metadata := projectx.Contract{ID: "es", SymbolID: "F.US.ES", TickSize: .25, TickValue: 12.5, ActiveContract: true}
		if account == 2 {
			metadata = projectx.Contract{ID: "cl", SymbolID: "F.US.CL", TickSize: .01, TickValue: 10, ActiveContract: true}
		}
		return contractMetadataAPI{API: api, metadata: metadata}, q, err
	})
	equity := plan("equity", 1)
	equity.ContractID = "es"
	equity.Instruments = []string{"ES"}
	equity.Policy.Schedule = &session.Schedule{Start: "08:30", LastEntry: "13:00", Flatten: "13:05"}
	energy := plan("energy", 2)
	energy.ContractID = "cl"
	energy.Instruments = []string{"CL"}
	energy.Policy.Schedule = &session.Schedule{Start: "10:00", LastEntry: "14:00", Flatten: "14:05"}
	if err := f.Start(ctx, equity, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(ctx, energy, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		views, _ := f.Snapshot()
		ready, waiting := false, false
		for _, v := range views {
			if v.AccountID == 1 {
				ready = v.Status.Armed
			}
			if v.AccountID == 2 {
				waiting = v.Status.RunRequested && !v.Status.Armed
			}
		}
		return ready && waiting
	})
	delivery := record(f, equity, "es-manager-schedule")
	delivery.Signal.Symbol = "ES"
	delivery.Signal.StopLoss = 19999
	delivery.Signal.TakeProfit = 20002
	if err := f.Dispatch(delivery, equity); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		rows, _ := store.DeliveryTargets(delivery.Signal.ID)
		return len(rows) == 1 && rows[0].State == "submitted"
	})
	rows, err := store.Executions()
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	var intent execution.Intent
	if err := json.Unmarshal([]byte(rows[0].Payload), &intent); err != nil {
		t.Fatal(err)
	}
	if intent.Window.Flatten.In(session.Chicago).Format("15:04") != "13:05" || intent.Policy.TradingSchedule().Start != "08:30" {
		t.Fatal("manager schedule not captured in execution", intent.Window)
	}
	g.mu.Lock()
	first, second := g.placed[1], g.placed[2]
	g.mu.Unlock()
	if first != 1 || second != 0 {
		t.Fatal("schedule isolation failed", first, second)
	}
}
