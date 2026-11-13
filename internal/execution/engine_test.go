package execution

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"projectxbot/internal/projectx"
	"projectxbot/internal/realtime"
	"projectxbot/internal/risk"
	"projectxbot/internal/session"
	"projectxbot/internal/signal"
	"projectxbot/internal/storage"
)

type mockAPI struct {
	accounts        []projectx.Account
	contract        projectx.Contract
	orders          []projectx.Order
	positions       []projectx.Position
	history         []projectx.Order
	trades          []projectx.Trade
	requests        []projectx.PlaceRequest
	placeErr        error
	readErr         error
	cancels, closes int
	onPlace         func()
}

func (m *mockAPI) Accounts(context.Context) ([]projectx.Account, error) { return m.accounts, m.readErr }
func (m *mockAPI) Contract(context.Context, string) (projectx.Contract, error) {
	return m.contract, m.readErr
}
func (m *mockAPI) OpenOrders(context.Context, int64) ([]projectx.Order, error) {
	return m.orders, m.readErr
}
func (m *mockAPI) OpenPositions(context.Context, int64) ([]projectx.Position, error) {
	return m.positions, m.readErr
}
func (m *mockAPI) Orders(context.Context, int64, time.Time, time.Time) ([]projectx.Order, error) {
	return m.history, m.readErr
}
func (m *mockAPI) Trades(context.Context, int64, time.Time, time.Time) ([]projectx.Trade, error) {
	return m.trades, m.readErr
}
func (m *mockAPI) Place(_ context.Context, r projectx.PlaceRequest) (int64, error) {
	if m.onPlace != nil {
		m.onPlace()
	}
	m.requests = append(m.requests, r)
	return 77, m.placeErr
}
func (m *mockAPI) Cancel(_ context.Context, id int64) error {
	m.cancels++
	for n := range m.history {
		if m.history[n].ID == id {
			m.history[n].Status = 3
		}
	}
	var orders []projectx.Order
	for _, o := range m.orders {
		if o.ID != id {
			orders = append(orders, o)
		}
	}
	m.orders = orders
	return nil
}
func (m *mockAPI) CloseContract(context.Context, string) error {
	m.closes++
	m.positions = nil
	return nil
}

type mockQuotes struct {
	q   realtime.Quote
	err error
}

func (q *mockQuotes) Watch(context.Context, string)      {}
func (q *mockQuotes) Get(string) (realtime.Quote, error) { return q.q, q.err }
func (q *mockQuotes) Close()                             {}
func fixture(t *testing.T) (*Engine, *mockAPI, *mockQuotes, *storage.Inbox, Config, risk.Profile, Authorization, signal.Signal, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, session.Chicago)
	simulated := true
	api := &mockAPI{accounts: []projectx.Account{{ID: 42, Name: "PRAC-test", Balance: 50000, CanTrade: true, Simulated: &simulated}}, contract: projectx.Contract{ID: "mnq", SymbolID: "F.US.MNQ", TickSize: .25, TickValue: .5, ActiveContract: true}}
	quote := &mockQuotes{q: realtime.Quote{Price: 20000, At: now}}
	store, err := storage.Open(filepath.Join(t.TempDir(), "execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	e, err := New(store, func(int64) (API, Quotes, error) { return api, quote, nil })
	if err != nil {
		t.Fatal(err)
	}
	e.ctx = context.Background()
	e.now = func() time.Time { return now }
	p := risk.Defaults()
	p.ID = "practice"
	p.Name = "Practice"
	p.AccountIDs = []int64{42}
	c := Config{Enabled: true, AccountID: 42, TradeManagerID: p.ID, RoundTripCosts: 3, SlippageTicks: 2, MaxQuantity: 5, MaxDeviationTicks: 4}
	a := Authorization{Window: session.Window{Start: now.Add(-time.Hour), LastEntry: time.Date(2026, 11, 13, 15, 0, 0, 0, session.Chicago), Flatten: time.Date(2026, 11, 13, 15, 5, 0, 0, session.Chicago), Verified: true}, Floor: 48000, PracticeConfirmed: true, BracketsConfirmed: true, FloorConfirmed: true, AccountName: "PRAC-test"}
	s := signal.Signal{ID: "one", Strategy: "manual", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19990, TakeProfit: 20020, Timestamp: now}
	return e, api, quote, store, c, p, a, s, &now
}
func TestMarketAndLimitExplicitQuantity(t *testing.T) {
	for _, typ := range []int{1, 2} {
		t.Run(map[int]string{1: "limit", 2: "market"}[typ], func(t *testing.T) {
			e, api, _, store, c, p, a, s, _ := fixture(t)
			if err := e.Arm(context.Background(), c, p, a); err != nil {
				t.Fatal(err)
			}
			preview, err := e.PrepareOrder(context.Background(), s, "mnq", typ, 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(api.requests) != 0 || preview.Plan.Quantity != 2 {
				t.Fatal("preview submitted or changed quantity")
			}
			api.onPlace = func() {
				rows, err := store.Executions()
				if err != nil || len(rows) != 1 || rows[0].State != "submitting" {
					t.Fatal("intent not durable before HTTP", rows, err)
				}
			}
			if err = e.Submit(context.Background(), preview.Token); err != nil {
				t.Fatal(err)
			}
			r := api.requests[0]
			if r.AccountID != 42 || r.Type != typ || r.Size != 2 || r.CustomTag == "" || r.StopLossBracket.Ticks != -40 || r.TakeProfitBracket.Ticks != 80 {
				t.Fatal(r)
			}
			if typ == 1 && (r.LimitPrice == nil || *r.LimitPrice != 20000) || typ == 2 && r.LimitPrice != nil {
				t.Fatal("wrong price schema")
			}
			if err = e.Submit(context.Background(), preview.Token); err == nil || len(api.requests) != 1 {
				t.Fatal("duplicate submission")
			}
		})
	}
}
func TestExecutionGates(t *testing.T) {
	for _, name := range []string{"disabled", "unarmed", "missing-simulated", "not-practice", "wrong-name", "no-brackets", "unknown-floor", "unknown-hours", "position", "working-order", "trade-limit", "daily-loss", "stale-quote", "oversized"} {
		t.Run(name, func(t *testing.T) {
			e, api, q, _, c, p, a, s, now := fixture(t)
			switch name {
			case "disabled":
				c.Enabled = false
			case "missing-simulated":
				api.accounts[0].Simulated = nil
			case "not-practice":
				a.AccountName = "50KTC-test"
				api.accounts[0].Name = a.AccountName
			case "wrong-name":
				a.AccountName = "different"
			case "no-brackets":
				a.BracketsConfirmed = false
			case "unknown-floor":
				a.FloorConfirmed = false
			case "unknown-hours":
				a.Window.Verified = false
			case "position":
				api.positions = []projectx.Position{{ID: 1}}
			case "working-order":
				api.orders = []projectx.Order{{ID: 1}}
			}
			err := e.Arm(context.Background(), c, p, a)
			if err == nil {
				switch name {
				case "unarmed":
					e.Pause()
				case "trade-limit":
					for n := 0; n < 6; n++ {
						api.history = append(api.history, projectx.Order{FillVolume: 1})
					}
				case "daily-loss":
					loss := -451.0
					cost := 0.0
					api.trades = []projectx.Trade{{ID: 1, ProfitAndLoss: &loss, Fees: &cost, Commissions: &cost}}
				case "stale-quote":
					q.q.At = now.Add(-time.Minute)
				}
				qty := 1
				if name == "oversized" {
					qty = 100
				}
				_, err = e.PrepareOrder(context.Background(), s, "mnq", 1, qty)
			}
			if err == nil || len(api.requests) != 0 {
				t.Fatal("unsafe gate allowed trade", name)
			}
		})
	}
}
func TestConcurrentSubmitsAndRestartDoNotReplay(t *testing.T) {
	e, api, _, store, c, p, a, s, _ := fixture(t)
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = e.Submit(context.Background(), preview.Token) }()
	}
	wg.Wait()
	if len(api.requests) != 1 {
		t.Fatal("duplicate mutation")
	}
	recovered, err := New(store, func(int64) (API, Quotes, error) { return api, &mockQuotes{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status().Armed || recovered.active() == nil {
		t.Fatal("restart lost intent or arming persisted")
	}
	recovered.ctx = context.Background()
	if err = recovered.Arm(context.Background(), c, p, a); err == nil {
		t.Fatal("armed before recovery")
	}
	if len(api.requests) != 1 {
		t.Fatal("replayed order")
	}
}
func TestAmbiguousTimeoutReconcilesByTagWithoutRetry(t *testing.T) {
	e, api, _, _, c, p, a, s, now := fixture(t)
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	api.placeErr = projectx.ErrAmbiguous
	if err = e.Submit(context.Background(), preview.Token); err == nil {
		t.Fatal("timeout accepted")
	}
	i := e.active()
	api.history = []projectx.Order{{ID: 77, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, Status: 1, CustomTag: i.Tag}}
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.OrderID != 77 || len(api.requests) != 1 || e.Status().Armed {
		t.Fatal("bad ambiguous recovery")
	}
	*now = now.Add(time.Minute)
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if len(api.requests) != 1 {
		t.Fatal("retried placement")
	}
}
func protectedOrders(i *Intent, size int) []projectx.Order {
	parent, stop, target := int64(77), int64(78), int64(79)
	sl, tp := 19990.0, 20020.0
	return []projectx.Order{
		{ID: stop, ParentOrderID: &parent, LinkedOrderID: &target, ContractID: "mnq", Side: 1, Type: 4, Status: 1, Size: size, StopPrice: &sl},
		{ID: target, ParentOrderID: &parent, LinkedOrderID: &stop, ContractID: "mnq", Side: 1, Type: 1, Status: 1, Size: size, LimitPrice: &tp},
	}
}
func TestPartialFillProtectionAndMissingStopFailSafe(t *testing.T) {
	e, api, _, store, c, p, a, s, _ := fixture(t)
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Submit(context.Background(), preview.Token); err != nil {
		t.Fatal(err)
	}
	i := e.active()
	parent := projectx.Order{ID: 77, AccountID: 42, ContractID: "mnq", Type: 1, Size: 2, FillVolume: 1, Status: 1, CustomTag: i.Tag}
	api.history = []projectx.Order{parent}
	api.positions = []projectx.Position{{ID: 3, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: 20000}}
	api.orders = append(protectedOrders(i, 1), parent)
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.State != "partial" {
		t.Fatal(i.State)
	}
	api.orders = api.orders[1:]
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if api.closes != 1 || !i.EmergencyAttempted || e.Status().Armed {
		t.Fatal("missing protection did not flatten")
	}
	if reason, err := store.ExecutionLock("42", i.Session); err != nil || reason == "" {
		t.Fatal("lockout not persisted")
	}
	// A subsequent reconciliation can observe flatness but cannot send close twice.
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if api.closes != 1 {
		t.Fatal("emergency repeated")
	}
}
func TestConnectionLossPausesAndPreservesIntent(t *testing.T) {
	e, api, _, _, c, p, a, s, _ := fixture(t)
	_ = e.Arm(context.Background(), c, p, a)
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Submit(context.Background(), preview.Token); err != nil {
		t.Fatal(err)
	}
	api.readErr = errors.New("offline")
	if err = e.reconcile(context.Background(), e.active()); err == nil {
		t.Fatal("loss ignored")
	}
	if e.active() == nil || len(api.requests) != 1 {
		t.Fatal("intent lost")
	}
}
func TestPreviewExpiryAndRiskChanges(t *testing.T) {
	for _, change := range []string{"expiry", "daily-loss", "quantity"} {
		t.Run(change, func(t *testing.T) {
			e, api, _, _, c, p, a, s, now := fixture(t)
			_ = e.Arm(context.Background(), c, p, a)
			preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 2)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "expiry":
				*now = now.Add(121 * time.Second)
			case "daily-loss":
				loss := -440.0
				fee := 0.0
				api.trades = []projectx.Trade{{ID: 1, ProfitAndLoss: &loss, Fees: &fee, Commissions: &fee}}
			case "quantity":
				e.config.MaxQuantity = 1
			}
			if err = e.Submit(context.Background(), preview.Token); err == nil || len(api.requests) != 0 {
				t.Fatal("stale or invalid plan sent")
			}
		})
	}
}

func TestMixedPriceFillsHaveWeightedProtection(t *testing.T) {
	e, _, _, _, _, _, _, _, _ := fixture(t)
	i := Intent{OrderID: 77, Signal: signal.Signal{Direction: "BUY"}, Plan: risk.SizingPlan{StopTicks: 40, TargetTicks: 80}, Contract: projectx.Contract{TickSize: .25}}
	p := projectx.Position{Size: 2, AveragePrice: 20000.125, Type: 1, ContractID: "mnq"}
	orders := protectedOrders(&i, 1)
	more := protectedOrders(&i, 1)
	parent, sid, tid := int64(77), int64(80), int64(81)
	sl, tp := 19990.25, 20020.25
	more[0].ID = sid
	more[0].ParentOrderID = &parent
	more[0].LinkedOrderID = &tid
	more[0].StopPrice = &sl
	more[1].ID = tid
	more[1].ParentOrderID = &parent
	more[1].LinkedOrderID = &sid
	more[1].LimitPrice = &tp
	orders = append(orders, more...)
	if err := verifyProtection(i, p, orders); err != nil {
		t.Fatal(err)
	}
	wrong := 19989.0
	orders[0].StopPrice = &wrong
	if err := verifyProtection(i, p, orders); err == nil {
		t.Fatal("incorrect protective distance accepted")
	}
	_ = e
}
func TestPauseCancelsWorkingEntryWithoutReplayingPlace(t *testing.T) {
	e, api, _, _, c, p, a, s, _ := fixture(t)
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Submit(context.Background(), preview.Token); err != nil {
		t.Fatal(err)
	}
	i := e.active()
	parent := projectx.Order{ID: 77, CustomTag: i.Tag, AccountID: 42, ContractID: "mnq", Size: 1, Type: 1, Status: 1}
	api.history = []projectx.Order{parent}
	api.orders = []projectx.Order{parent}
	e.Pause()
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if api.cancels != 1 || api.closes != 0 || len(api.requests) != 1 || e.Status().Armed {
		t.Fatal("pause failed")
	}
}

func TestPartialExitRetainsProtectionForRemainingQuantity(t *testing.T) {
	e, api, _, _, c, p, a, s, _ := fixture(t)
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Submit(context.Background(), preview.Token); err != nil {
		t.Fatal(err)
	}
	i := e.active()
	api.history = []projectx.Order{{ID: 77, CustomTag: i.Tag, Type: 1, ContractID: "mnq", AccountID: 42, Size: 2, FillVolume: 2, Status: 2}}
	api.positions = []projectx.Position{{ID: 1, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: 20000}}
	api.orders = protectedOrders(i, 1)
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.State != "protected" || api.closes != 0 {
		t.Fatal("remaining covered exposure flattened", i.State)
	}
}

func TestPausedContinuousAuthorizationCannotRearm(t *testing.T) {
	e, _, _, _, c, p, a, _, _ := fixture(t)
	c.Automated = true
	c.AutomationProfileID = "swift"
	c.AutomationContractID = "mnq"
	a.Continuous = true
	a.AutomationConfirmed = true
	if err := e.Arm(context.Background(), c, p, a); err == nil {
		t.Fatal("controller armed without a running choice")
	}
	if err := e.RequestRun("swift"); err != nil {
		t.Fatal(err)
	}
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	e.Pause()
	if err := e.Arm(context.Background(), c, p, a); err == nil {
		t.Fatal("stale controller authorization undid Pause")
	}
	if s := e.Status(); s.Armed || s.RunRequested {
		t.Fatal("Pause was not authoritative", s)
	}
}

func TestPerTradeOnlyExecutionWithoutManualFloorOrDailyCaps(t *testing.T) {
	e, api, _, _, c, p, a, s, _ := fixture(t)
	p.PerTradeOnly = true
	p.MaxDailyLoss = 1
	p.MaxTradesPerSession = 1
	p.MaxOpenRisk = 1
	p.MaxSignalRisk = 1
	a.Floor = 0
	a.FloorConfirmed = false
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	loss := -1000.0
	fees := 3.0
	api.trades = []projectx.Trade{{ID: 1, ProfitAndLoss: &loss, Fees: &fees}}
	api.history = []projectx.Order{{ID: 90, FillVolume: 1, Status: 2}, {ID: 91, FillVolume: 1, Status: 2}}
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 2, 2)
	if err != nil || preview.Plan.PlannedRisk > p.MaxRiskPerTrade {
		t.Fatal(preview, err)
	}
	if err = e.Submit(context.Background(), preview.Token); err != nil {
		t.Fatal(err)
	}
	i := e.active()
	if !i.Policy.PerTradeOnly || i.Floor != 0 {
		t.Fatal("risk mode was not persisted", i)
	}
	api.history = append(api.history, projectx.Order{ID: i.OrderID, CustomTag: i.Tag, ContractID: "mnq", Type: 2, Size: 2, FillVolume: 2, Status: 2})
	api.positions = []projectx.Position{{ID: 1, AccountID: 42, ContractID: "mnq", Type: 1, Size: 2, AveragePrice: 20000}}
	api.orders = protectedOrders(i, 2)
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.State != "protected" || api.closes != 0 {
		t.Fatal("legacy daily cap incorrectly flattened simplified trade", i.State, api.closes)
	}
	e.Pause()
	if _, err = e.PrepareOrder(context.Background(), s, "mnq", 2, 1); err == nil {
		t.Fatal("pause bypassed")
	}
}

func TestStartAutomationReplacesIdleManualPermissionAndPreview(t *testing.T) {
	e, api, _, _, c, p, a, s, _ := fixture(t)
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.RequestRun("swift"); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(); st.Armed || !st.RunRequested || st.Preview != nil {
		t.Fatal("manual permission/preview survived switch", st)
	}
	// Starting alone has not placed or authorized an order.
	if err = e.Submit(context.Background(), preview.Token); err == nil || len(api.requests) != 0 {
		t.Fatal("old manual preview remained usable")
	}
	c.Automated = true
	c.AutomationProfileID = "swift"
	c.AutomationContractID = "mnq"
	a.AutomationConfirmed = true
	a.Continuous = true
	// Fresh reconciliation must still see unexpected broker exposure.
	api.positions = []projectx.Position{{ID: 1, AccountID: 42, ContractID: "mnq", Size: 1}}
	if err = e.Arm(context.Background(), c, p, a); err == nil || e.Status().Armed {
		t.Fatal("switch skipped remote reconciliation")
	}
	api.positions = nil
	if err = e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(); !st.Armed || !st.Automated || st.AutomationProfileID != "swift" {
		t.Fatal(st)
	}
	if len(api.requests) != 0 {
		t.Fatal("switch itself submitted an order")
	}
}
func TestStartAutomationDoesNotReplaceActiveTradeOrAnotherRunner(t *testing.T) {
	t.Run("active manual trade", func(t *testing.T) {
		e, _, _, _, c, p, a, s, _ := fixture(t)
		if err := e.Arm(context.Background(), c, p, a); err != nil {
			t.Fatal(err)
		}
		preview, err := e.PrepareOrder(context.Background(), s, "mnq", 2, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Submit(context.Background(), preview.Token); err != nil {
			t.Fatal(err)
		}
		if err = e.RequestRun("swift"); err == nil || e.Status().RunRequested {
			t.Fatal("active manual trade was replaced")
		}
	})
	t.Run("other runner waiting for hours", func(t *testing.T) {
		e, _, _, _, _, _, _, _, _ := fixture(t)
		if err := e.RequestRun("first"); err != nil {
			t.Fatal(err)
		}
		if err := e.RequestRun("second"); err == nil || !strings.Contains(err.Error(), "first") {
			t.Fatal("other requested runner replaced", err)
		}
		if st := e.Status(); st.RunProfileID != "first" || !st.RunRequested {
			t.Fatal(st)
		}
	})
}

type startingQuotes struct {
	*mockQuotes
	waits int
}

func (q *startingQuotes) Wait(ctx context.Context, contract string) (realtime.Quote, error) {
	q.waits++
	if _, ok := ctx.Deadline(); !ok {
		return realtime.Quote{}, errors.New("unbounded quote startup wait")
	}
	q.err = nil
	return q.q, nil
}
func TestFirstPracticePreviewWaitsForQuoteWithoutPlacing(t *testing.T) {
	e, api, quotes, _, c, p, a, _, _ := fixture(t)
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	quotes.err = errors.New("stream starting")
	source := &startingQuotes{mockQuotes: quotes}
	e.quotes = source
	s, err := e.TicketSignal(context.Background(), "practice-api-test", "MNQ", "BUY", "mnq", 2, 0, 40, 80)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PrepareOrder(context.Background(), s, "mnq", 2, 1); err != nil {
		t.Fatal(err)
	}
	if source.waits != 1 || len(api.requests) != 0 {
		t.Fatal("preview did not wait or submitted an order")
	}
}

func TestDisabledDeviationAppliesAtPreviewAndSubmission(t *testing.T) {
	for _, typ := range []int{1, 2} {
		t.Run(map[int]string{1: "limit", 2: "market"}[typ], func(t *testing.T) {
			e, api, quotes, _, c, p, a, s, _ := fixture(t)
			quotes.q.Price = 20100
			if err := e.Arm(context.Background(), c, p, a); err != nil {
				t.Fatal(err)
			}
			if _, err := e.PrepareOrder(context.Background(), s, "mnq", typ, 1); err == nil {
				t.Fatal("deviation enabled by default was bypassed")
			}
			c.DisablePriceDeviation = true
			if err := e.Arm(context.Background(), c, p, a); err != nil {
				t.Fatal(err)
			}
			preview, err := e.PrepareOrder(context.Background(), s, "mnq", typ, 1)
			if err != nil {
				t.Fatal(err)
			}
			quotes.q.Price = 20200
			if err = e.Submit(context.Background(), preview.Token); err != nil {
				t.Fatal(err)
			}
			if len(api.requests) != 1 || api.requests[0].Size != 1 || !e.active().Config.DisablePriceDeviation {
				t.Fatal("disabled policy not applied/persisted")
			}
		})
	}
}

func TestSubmissionFailureDiagnosticPersistsWithoutRetryOrSensitiveError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		want    string
	}{
		{"API failure", &projectx.APIError{Endpoint: "PRIVATE_NOT_FOR_LOGS", Status: 200, Code: 2}, "HTTP 200, API code 2"},
		{"bracket mode", &projectx.APIError{Status: 200, Code: 2, AutoOCORequired: true}, "Enable Auto OCO Brackets"},
		{"ambiguous", projectx.ErrAmbiguous, "did not confirm"},
		{"unclassified", errors.New("PRIVATE_NOT_FOR_LOGS"), "without a confirmed result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, api, _, store, c, p, a, s, _ := fixture(t)
			if err := e.Arm(context.Background(), c, p, a); err != nil {
				t.Fatal(err)
			}
			preview, err := e.PrepareOrder(context.Background(), s, "mnq", 2, 1)
			if err != nil {
				t.Fatal(err)
			}
			api.placeErr = tc.failure
			err = e.Submit(context.Background(), preview.Token)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("diagnostic absent or leaked", err)
			}
			intent := e.active()
			if intent == nil || intent.State != "uncertain" || !strings.Contains(intent.SubmissionFailure, tc.want) {
				t.Fatal("diagnostic not retained")
			}
			// Reconciliation changes Message; the original safe cause must remain.
			if err = e.reconcile(context.Background(), intent); err != nil {
				t.Fatal(err)
			}
			rows, err := store.Executions()
			if err != nil || len(rows) != 1 || !strings.Contains(rows[0].Payload, tc.want) || strings.Contains(rows[0].Payload, "PRIVATE") {
				t.Fatal("diagnostic was lost during reconciliation")
			}
			if err = e.Submit(context.Background(), preview.Token); err == nil || len(api.requests) != 1 {
				t.Fatal("uncertain submission retried")
			}
		})
	}
}

func TestProviderReasonIsRuntimeOnlyAndNeverInAuditOrLedger(t *testing.T) {
	e, api, _, store, c, p, a, s, _ := fixture(t)
	var audit []string
	e.audit = func(_, message string) { audit = append(audit, message) }
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	api.placeErr = &projectx.APIError{Endpoint: "/api/Order/place", Status: 200, Code: 2, ProviderReason: "PRIVATE_UI_REASON"}
	err = e.Submit(context.Background(), preview.Token)
	if err == nil || strings.Contains(err.Error(), "PRIVATE_UI_REASON") {
		t.Fatal("error omitted or exposed provider reason")
	}
	st := e.Status()
	if st.SubmissionDetail == nil || st.SubmissionDetail.Reason != "PRIVATE_UI_REASON" || st.SubmissionDetail.IntentID != e.active().ID {
		t.Fatal("UI reason missing or unscoped")
	}
	st.SubmissionDetail.Reason = "tampered"
	if e.Status().SubmissionDetail.Reason != "PRIVATE_UI_REASON" {
		t.Fatal("status exposed mutable state")
	}
	rows, err := store.Executions()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if strings.Contains(row.Payload, "PRIVATE_UI_REASON") {
			t.Fatal("provider reason persisted")
		}
	}
	for _, row := range audit {
		if strings.Contains(row, "PRIVATE_UI_REASON") {
			t.Fatal("provider reason entered audit")
		}
	}
	if len(api.requests) != 1 {
		t.Fatal("placement retry")
	}
}

func TestManualMarketAndLimitBracketSignsKeepPositiveRiskDistances(t *testing.T) {
	for _, direction := range []string{"BUY", "SELL"} {
		for _, typ := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/type%d", direction, typ), func(t *testing.T) {
				e, api, _, _, c, p, a, s, _ := fixture(t)
				wantSide, wantStop, wantTarget := 0, -40, 80
				if direction == "SELL" {
					s.Direction = "SELL"
					s.StopLoss = 20010
					s.TakeProfit = 19980
					wantSide, wantStop, wantTarget = 1, 40, -80
				}
				if err := e.Arm(context.Background(), c, p, a); err != nil {
					t.Fatal(err)
				}
				preview, err := e.PrepareOrder(context.Background(), s, "mnq", typ, 1)
				if err != nil {
					t.Fatal(err)
				}
				if preview.Plan.StopTicks != 40 || preview.Plan.TargetTicks != 80 || preview.Plan.PlannedRisk <= 0 {
					t.Fatal("wire signs corrupted risk plan")
				}
				if err = e.Submit(context.Background(), preview.Token); err != nil {
					t.Fatal(err)
				}
				r := api.requests[0]
				if r.Side != wantSide || r.StopLossBracket.Ticks != wantStop || r.TakeProfitBracket.Ticks != wantTarget {
					t.Fatal("incorrect directional brackets", r)
				}
			})
		}
	}
}

func TestOneWayOCOFromObservedMGCLayout(t *testing.T) {
	parent, stopID, targetID := int64(3762421266), int64(3762421267), int64(3762421269)
	stopPrice, targetPrice := 3877.4, 3889.4
	i := Intent{OrderID: parent, Signal: signal.Signal{Direction: "BUY"}, Plan: risk.SizingPlan{StopTicks: 40, TargetTicks: 80}, Contract: projectx.Contract{TickSize: .1}}
	position := projectx.Position{ContractID: "mgc", Size: 1, Type: 1, AveragePrice: 3881.4}
	makeOrders := func() []projectx.Order {
		return []projectx.Order{
			{ID: stopID, ParentOrderID: &parent, ContractID: "mgc", Side: 1, Type: 4, Status: 1, Size: 1, StopPrice: &stopPrice},
			{ID: targetID, ParentOrderID: &parent, LinkedOrderID: &stopID, ContractID: "mgc", Side: 1, Type: 1, Status: 1, Size: 1, LimitPrice: &targetPrice},
		}
	}
	for _, tc := range []struct {
		name  string
		edit  func([]projectx.Order)
		valid bool
	}{
		{"target to stop", func([]projectx.Order) {}, true},
		{"stop to target", func(o []projectx.Order) { o[0].LinkedOrderID = &targetID; o[1].LinkedOrderID = nil }, true},
		{"reciprocal", func(o []projectx.Order) { o[0].LinkedOrderID = &targetID }, true},
		{"no link", func(o []projectx.Order) { o[1].LinkedOrderID = nil }, false},
		{"conflicting link", func(o []projectx.Order) { x := int64(123); o[0].LinkedOrderID = &x }, false},
		{"wrong parent", func(o []projectx.Order) { x := int64(123); o[0].ParentOrderID = &x }, false},
		{"wrong side", func(o []projectx.Order) { o[0].Side = 0 }, false},
		{"pending protection", func(o []projectx.Order) { o[0].Status = 8 }, false},
		{"wrong quantity", func(o []projectx.Order) { o[1].Size = 2 }, false},
		{"wrong target", func(o []projectx.Order) { x := 3890.0; o[1].LimitPrice = &x }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orders := makeOrders()
			tc.edit(orders)
			err := verifyProtection(i, position, orders)
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
	orders := makeOrders()
	orders = append(orders, orders[0])
	if err := verifyProtection(i, position, orders); err == nil {
		t.Fatal("duplicate protection accepted")
	}
}
func TestOneWayOCOFillDoesNotTriggerEmergencyClose(t *testing.T) {
	e, api, _, store, c, p, a, s, _ := fixture(t)
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	preview, err := e.PrepareOrder(context.Background(), s, "mnq", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Submit(context.Background(), preview.Token); err != nil {
		t.Fatal(err)
	}
	intent := e.active()
	api.history = []projectx.Order{{ID: 77, CustomTag: intent.Tag, ContractID: "mnq", Type: 2, Side: 0, Size: 1, FillVolume: 1, Status: 2}}
	api.positions = []projectx.Position{{ID: 1, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: 20000}}
	api.orders = protectedOrders(intent, 1)
	api.orders[0].LinkedOrderID = nil
	if err = e.reconcile(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if intent.State != "protected" || api.closes != 0 || intent.EmergencyAttempted {
		t.Fatal("valid one-way protection flattened", intent.State)
	}
	// A real missing leg still triggers the fail-safe, and its first cause survives.
	api.orders = api.orders[1:]
	if err = e.reconcile(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if api.closes != 1 || intent.HaltReason == "" || len(intent.HaltOrders) != 1 {
		t.Fatal("missing protection not captured/closed")
	}
	original := intent.HaltReason
	if intent.Message == original {
		t.Fatal("fixture did not advance emergency progress")
	}
	rows, err := store.Executions()
	if err != nil || !strings.Contains(rows[0].Payload, original) {
		t.Fatal("original trigger lost from durable record")
	}
}
