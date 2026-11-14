package automation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"projectxbot/internal/execution"
	"projectxbot/internal/ingress"
	"projectxbot/internal/projectx"
	"projectxbot/internal/realtime"
	"projectxbot/internal/risk"
	"projectxbot/internal/session"
	"projectxbot/internal/signal"
	"projectxbot/internal/storage"
	"projectxbot/internal/webhook"
)

// mockGateway records actual typed write requests produced by the real engine;
// no credentials, listening sockets or ProjectX network calls are involved.
type mockGateway struct {
	missingStop bool
	mu          sync.Mutex
	requests    []projectx.PlaceRequest
	history     []projectx.Order
	orders      []projectx.Order
	positions   []projectx.Position
	ambiguous   bool
	now         func() time.Time
}

func (g *mockGateway) Accounts(context.Context) ([]projectx.Account, error) {
	sim := true
	return []projectx.Account{{ID: 42, Name: "PRAC-offline-test", Balance: 50000, CanTrade: true, Simulated: &sim}}, nil
}
func (g *mockGateway) Contract(context.Context, string) (projectx.Contract, error) {
	return projectx.Contract{ID: "mnq", SymbolID: "F.US.MNQ", TickSize: .25, TickValue: .5, ActiveContract: true}, nil
}
func (g *mockGateway) Orders(context.Context, int64, time.Time, time.Time) ([]projectx.Order, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]projectx.Order{}, g.history...), nil
}
func (g *mockGateway) OpenOrders(context.Context, int64) ([]projectx.Order, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]projectx.Order{}, g.orders...), nil
}
func (g *mockGateway) OpenPositions(context.Context, int64) ([]projectx.Position, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]projectx.Position{}, g.positions...), nil
}
func (g *mockGateway) Trades(context.Context, int64, time.Time, time.Time) ([]projectx.Trade, error) {
	return []projectx.Trade{}, nil
}
func (g *mockGateway) Cancel(context.Context, int64) error { return nil }
func (g *mockGateway) CloseContract(context.Context, string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.positions = nil
	g.orders = nil
	return nil
}
func (g *mockGateway) Place(_ context.Context, r projectx.PlaceRequest) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests = append(g.requests, r)
	parent, sid, tid := int64(77), int64(78), int64(79)
	price, positionType := 20000.0, 1
	if r.Side == 1 {
		positionType = 2
	}
	stop, target := price+float64(r.StopLossBracket.Ticks)*.25, price+float64(r.TakeProfitBracket.Ticks)*.25
	g.history = []projectx.Order{{ID: parent, AccountID: 42, ContractID: "mnq", Type: r.Type, Side: r.Side, Size: r.Size, FillVolume: r.Size, Status: 2, CustomTag: r.CustomTag, FilledPrice: &price}}
	g.positions = []projectx.Position{{ID: 1, AccountID: 42, ContractID: "mnq", Type: positionType, Size: r.Size, AveragePrice: price}}
	g.orders = []projectx.Order{{ID: sid, AccountID: 42, ContractID: "mnq", Type: 4, Side: 1 - r.Side, Size: r.Size, Status: 1, ParentOrderID: &parent, StopPrice: &stop}, {ID: tid, AccountID: 42, ContractID: "mnq", Type: 1, Side: 1 - r.Side, Size: r.Size, Status: 1, ParentOrderID: &parent, LinkedOrderID: &sid, LimitPrice: &target}}
	if g.missingStop {
		g.orders = g.orders[1:]
	}
	if g.ambiguous {
		return 0, projectx.ErrAmbiguous
	}
	return parent, nil
}
func (g *mockGateway) count() int { g.mu.Lock(); defer g.mu.Unlock(); return len(g.requests) }

type quotes struct{ now func() time.Time }

func (q quotes) Watch(context.Context, string) {}
func (q quotes) Get(string) (realtime.Quote, error) {
	return realtime.Quote{Price: 20000, At: q.now()}, nil
}
func (q quotes) Close() {}

type fixture struct {
	engine        *execution.Engine
	gateway       *mockGateway
	store, remote *storage.Inbox
	dispatcher    Dispatcher
	binding       Binding
	receiver      http.Handler
	clock         *atomic.Int64
	config        execution.Config
	policy        risk.Profile
	auth          execution.Authorization
	now           func() time.Time
	factory       execution.Factory
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{clock: &atomic.Int64{}}
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, session.Chicago)
	f.clock.Store(base.UnixNano())
	f.now = func() time.Time { return time.Unix(0, f.clock.Load()) }
	var err error
	f.store, err = storage.OpenWithClock(filepath.Join(t.TempDir(), "execution.db"), f.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.store.Close() })
	f.remote, err = storage.OpenWithClock(filepath.Join(t.TempDir(), "remote.db"), f.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.remote.Close() })
	f.gateway = &mockGateway{now: f.now}
	f.factory = func(int64) (execution.API, execution.Quotes, error) { return f.gateway, quotes{f.now}, nil }
	f.engine, err = execution.NewWithClock(f.store, f.factory, f.now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.engine.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	f.policy = risk.Defaults()
	f.policy.ID = "practice"
	f.policy.Name = "Practice"
	f.policy.AccountIDs = []int64{42}
	f.config = execution.Config{Enabled: true, AccountID: 42, TradeManagerID: "practice", RoundTripCosts: 3, SlippageTicks: 2, MaxQuantity: 5, MaxDeviationTicks: 4, Automated: true, AutomationProfileID: "swift", AutomationContractID: "mnq"}
	window, _ := session.NormalWindow("2026-11-13")
	window.Verified = true
	f.auth = execution.Authorization{Window: window, Floor: 48000, AccountName: "PRAC-offline-test", PracticeConfirmed: true, BracketsConfirmed: true, FloorConfirmed: true, AutomationConfirmed: true}
	deadline := time.Now().Add(time.Second)
	for {
		err = f.engine.Arm(context.Background(), f.config, f.policy, f.auth)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "monitor is not running") || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	f.clock.Store(base.Add(time.Second).UnixNano())
	f.binding = Binding{ProfileID: "swift", ContractID: "mnq", Strategy: "swift", AccountID: 42, Instruments: []string{"MNQ"}, OrderType: 2, Approved: true}
	f.dispatcher = Dispatcher{Engine: f.engine, Store: f.store, Now: f.now}
	f.receiver, err = webhook.New(f.remote, webhook.Options{Enabled: true, MaxAge: 5 * time.Minute, Now: f.now, Resolve: func(path string) (ingress.Route, bool) {
		return ingress.Route{ID: "swift", Token: strings.Repeat("x", 64), Strategy: "swift", Symbols: []string{"MNQ"}}, path == "/tradingview/swift"
	}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) alert(t *testing.T, id, direction string) storage.Record {
	t.Helper()
	stop, target := 19990.0, 20020.0
	if direction == "SELL" {
		stop, target = 20010, 19980
	}
	s := signal.Signal{ID: id, Strategy: "swift", Symbol: "MNQ", Direction: direction, Entry: 20000, StopLoss: stop, TakeProfit: target, Timestamp: f.now()}
	body, _ := json.Marshal(struct {
		Token  string        `json:"delivery_token"`
		Signal signal.Signal `json:"signal"`
	}{strings.Repeat("x", 64), s})
	req := httptest.NewRequest("POST", "http://127.0.0.1:8090/tradingview/swift", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.receiver.ServeHTTP(w, req)
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	r, err := f.remote.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestWebhookToRealEngineToProtectedGatewayOrder(t *testing.T) {
	for _, direction := range []string{"BUY", "SELL"} {
		t.Run(direction, func(t *testing.T) {
			f := setup(t)
			r := f.alert(t, "alert-one", direction)
			if f.gateway.count() != 0 {
				t.Fatal("receiver transmitted before local dispatch")
			}
			if err := f.dispatcher.Process(context.Background(), f.binding, r); err != nil {
				t.Fatal(err)
			}
			if f.gateway.count() != 1 {
				t.Fatal("order not placed")
			}
			f.gateway.mu.Lock()
			req := f.gateway.requests[0]
			f.gateway.mu.Unlock()
			stopOffset, targetOffset := -40, 80
			if direction == "SELL" {
				stopOffset, targetOffset = 40, -80
			}
			if req.AccountID != 42 || req.Size != 5 || req.Type != 2 || req.CustomTag == "" || req.StopLossBracket.Ticks != stopOffset || req.TakeProfitBracket.Ticks != targetOffset {
				t.Fatal(req)
			}
			if direction == "SELL" && req.Side != 1 || direction == "BUY" && req.Side != 0 {
				t.Fatal("wrong side")
			}
			receipt, err := f.store.AutomationReceipt(r.Signal.ID)
			if err != nil || receipt.State != "submitted" {
				t.Fatal(receipt, err)
			}
			// Real monitor reads the simulated fill/children and verifies protection.
			deadline := time.Now().Add(time.Second)
			for {
				status := f.engine.Status()
				if len(status.Intents) == 1 && status.Intents[0].State == "protected" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("fill not protected", status)
				}
				time.Sleep(time.Millisecond)
			}
			// Redelivery and a resumed dispatcher must never place the same alert twice.
			_ = f.alert(t, "alert-one", direction)
			if err = f.dispatcher.Process(context.Background(), f.binding, r); err != nil {
				t.Fatal(err)
			}
			if f.gateway.count() != 1 {
				t.Fatal("duplicate order")
			}
		})
	}
}
func TestAutomaticGatesConsumeOrRejectWithoutTrading(t *testing.T) {
	for _, name := range []string{"unapproved", "paused", "wrong-profile", "wrong-account", "before-arm", "expired", "wrong-strategy", "wrong-symbol", "price-deviation"} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			r := f.alert(t, "gated", "BUY")
			b := f.binding
			switch name {
			case "unapproved":
				b.Approved = false
			case "paused":
				f.engine.Pause()
			case "wrong-profile":
				r.ProfileID = "other"
			case "wrong-account":
				b.AccountID = 99
			case "before-arm":
				r.ReceivedAt = f.engine.Status().ArmedAt.Add(-time.Second).Format(time.RFC3339Nano)
			case "expired":
				r.Signal.Timestamp = f.now().Add(-10 * time.Minute)
			case "wrong-strategy":
				r.Signal.Strategy = "other"
			case "wrong-symbol":
				r.Signal.Symbol = "MGC"
			case "price-deviation":
				r.Signal.Entry = 20100
				r.Signal.StopLoss = 20090
				r.Signal.TakeProfit = 20120
			}
			err := f.dispatcher.Process(context.Background(), b, r)
			receipt, lookupErr := f.store.AutomationReceipt(r.Signal.ID)
			if err == nil && (lookupErr != nil || receipt.State != "blocked") {
				t.Fatal("gate not enforced", receipt, err)
			}
			if f.gateway.count() != 0 {
				t.Fatal("gated alert traded")
			}
		})
	}
}
func TestAmbiguousAutoPlacementAndRestartNeverReplay(t *testing.T) {
	f := setup(t)
	f.gateway.ambiguous = true
	if err := f.engine.RequestRun("swift"); err != nil {
		t.Fatal(err)
	}
	r := f.alert(t, "timeout", "BUY")
	if err := f.dispatcher.Process(context.Background(), f.binding, r); err != nil {
		t.Fatal(err)
	}
	receipt, err := f.store.AutomationReceipt(r.Signal.ID)
	if err != nil || receipt.State != "uncertain" {
		t.Fatal(receipt, err)
	}
	recovered, err := execution.NewWithClock(f.store, f.factory, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if f.engine.Status().RunRequested {
		t.Fatal("ambiguous write left continuous run enabled")
	}
	if recovered.Status().Armed {
		t.Fatal("arming survived restart")
	}
	resumed := Dispatcher{Engine: recovered, Store: f.store, Now: f.now}
	if err = resumed.Process(context.Background(), f.binding, r); err != nil {
		t.Fatal(err)
	}
	if f.gateway.count() != 1 {
		t.Fatal("ambiguous order replayed")
	}
}
func TestCrashAfterClaimDoesNotReplayAlert(t *testing.T) {
	f := setup(t)
	r := f.alert(t, "claimed", "BUY")
	if err := f.store.ClaimAutomation(storage.AutomationReceipt{ID: r.Signal.ID, ProfileID: "swift", AccountID: "42", ContractID: "mnq"}); err != nil {
		t.Fatal(err)
	}
	if err := f.dispatcher.Process(context.Background(), f.binding, r); err != nil {
		t.Fatal(err)
	}
	receipt, err := f.store.AutomationReceipt(r.Signal.ID)
	if err != nil || receipt.State != "interrupted" || f.gateway.count() != 0 {
		t.Fatal(receipt, err)
	}
	if !errors.Is(f.store.ClaimAutomation(receipt), storage.ErrDuplicate) {
		t.Fatal("claim not durable")
	}
}

func TestMissingProtectionStopsContinuousRun(t *testing.T) {
	f := setup(t)
	f.gateway.missingStop = true
	if err := f.engine.RequestRun("swift"); err != nil {
		t.Fatal(err)
	}
	r := f.alert(t, "missing-stop", "BUY")
	if err := f.dispatcher.Process(context.Background(), f.binding, r); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		s := f.engine.Status()
		if !s.RunRequested && len(s.Intents) > 0 && s.Intents[0].EmergencyAttempted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("missing protection did not stop continuous run", s)
		}
		time.Sleep(time.Millisecond)
	}
}
