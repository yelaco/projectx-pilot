package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/fleet"
	"github.com/yelaco/projectx-pilot/internal/ingress"
	"github.com/yelaco/projectx-pilot/internal/notify"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/session"
	"github.com/yelaco/projectx-pilot/internal/signal"
	"github.com/yelaco/projectx-pilot/internal/storage"
	"github.com/yelaco/projectx-pilot/internal/webhook"
)

type multiFlow struct {
	*flowAPI
	account int64
}

func (f multiFlow) Accounts(context.Context) ([]projectx.Account, error) {
	sim := true
	return []projectx.Account{{ID: 42, Name: "Combine", CanTrade: true, Simulated: &sim, Balance: 50000}, {ID: 43, Name: "Express Funded", CanTrade: true, Simulated: &sim, Balance: 50000}}, nil
}
func (f multiFlow) Orders(context.Context, int64, time.Time, time.Time) ([]projectx.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []projectx.Order{}
	for _, r := range f.requests {
		if r.AccountID == f.account {
			out = append(out, projectx.Order{ID: 77, AccountID: f.account, ContractID: "mnq", Type: r.Type, Side: r.Side, Size: r.Size, Status: 1, CustomTag: r.CustomTag})
		}
	}
	return out, nil
}
func (f multiFlow) OpenOrders(ctx context.Context, id int64) ([]projectx.Order, error) {
	return f.Orders(ctx, id, time.Time{}, time.Time{})
}
func (f multiFlow) AvailableContracts(context.Context) ([]projectx.Contract, error) {
	return []projectx.Contract{{ID: "mnq", Name: "MNQ", SymbolID: "F.US.MNQ", TickSize: .25, TickValue: .5, ActiveContract: true}}, nil
}
func TestUnixWakeToMultiAccountAcceptance(t *testing.T) {
	m, password, actor, _, state := automaticFixture(t)
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, session.Chicago)
	start := time.Now()
	clock := func() time.Time { return base.Add(time.Since(start)) }
	m.now = clock
	policy := m.policies.TradeManagers[0]
	policy.PerTradeOnly = true
	policy.Routing = "multiple"
	policy.AccountIDs = []int64{42, 43}
	if err := m.SaveTradeManager(policy); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateAutomationExecution("swift", "automatic", "mnq", "limit", policy.ID); err != nil {
		t.Fatal(err)
	}
	api := &flowAPI{}
	factory := func(id int64) (execution.API, execution.Quotes, error) {
		return multiFlow{api, id}, flowQuotes{clock}, nil
	}
	var err error
	m.fleet, err = fleet.New(m.executionStore, factory, nil, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := app.New(multiFlow{api, 42}, 42)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Attach(service)
	cfg := execution.Config{Enabled: true, TradeManagerID: policy.ID, RoundTripCosts: 3, SlippageTicks: 2, MaxQuantity: 3, MaxDeviationTicks: 4}
	auth := execution.Authorization{PracticeConfirmed: true, BracketsConfirmed: true, Window: session.Window{Verified: true}}
	if err := m.SaveProfileSetup(actor, password, "swift", cfg, true, auth); err != nil {
		t.Fatal(err)
	}
	m.remote, err = storage.OpenWithClock(filepath.Join(state, "remote-alerts.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := notify.Listen(filepath.Join(state, "inbox.sock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	executionDone, dispatchDone, socketDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var wakes atomic.Int32
	go func() { defer close(executionDone); m.RunExecution(ctx) }()
	go func() { defer close(dispatchDone); m.RunAutomation(ctx) }()
	go func() { defer close(socketDone); listener.Run(ctx, func() { wakes.Add(1); m.WakeAutomation() }) }()
	t.Cleanup(func() { cancel(); listener.Close(); <-socketDone; <-dispatchDone; <-executionDone })
	if err := m.WaitRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.StartProfile(ctx, "swift"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		views, _ := m.RunnerSnapshot()
		armed := 0
		for _, v := range views {
			if v.Status.Armed {
				armed++
			}
		}
		if armed == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runners not armed", views)
		}
		time.Sleep(time.Millisecond)
	}
	receiver, err := webhook.New(m.remote, webhook.Options{Enabled: true, MaxAge: 5 * time.Minute, Now: clock, AfterStore: func() { notify.Ping(filepath.Join(state, "inbox.sock")) }, Resolve: func(path string) (ingress.Route, bool) {
		r := m.Ingress().Routes[0]
		return r, path == "/tradingview/swift"
	}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"delivery_token": m.Ingress().Routes[0].Token, "signal": signal.Signal{ID: "fanout-socket", Strategy: "swift", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19990, TakeProfit: 20020, Timestamp: clock()}})
	arrival := time.Now()
	request := httptest.NewRequest("POST", "http://127.0.0.1:8090/tradingview/swift", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	receiver.ServeHTTP(response, request)
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	deadline = time.Now().Add(time.Second)
	for {
		jobs, _ := m.TargetResults()
		submitted := 0
		for _, j := range jobs {
			if j.State == "submitted" {
				submitted++
				if j.AcceptanceMS() < 0 || j.ReceivedAt.IsZero() || j.SubmitAt.IsZero() {
					t.Fatal("missing timing")
				}
			}
		}
		if submitted == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket-to-gateway dispatch exceeded one second", jobs)
		}
		time.Sleep(time.Millisecond)
	}
	if wakes.Load() == 0 {
		t.Fatal("socket did not notify the worker")
	}
	if api.count() != 2 {
		t.Fatal("did not execute complete target set")
	}
	// New profile scope is independent of old Connection settings, even while
	// existing account intents need monitoring. Saving it must not pause the
	// unrelated running profile or require a connection-settings change.
	m.mu.Lock()
	m.c.AllowedInstruments = []string{"MNQ"}
	m.mu.Unlock()
	draft := Automation{ID: "gold", Name: "Gold", Strategy: "gold", AccountID: 43, Instruments: []string{"MGC"}, Source: "tradingview-manual"}
	if err := m.SaveAutomation(draft); err != nil {
		t.Fatal("profile edit blocked by monitored account/global instrument list", err)
	}
	views, _ := m.RunnerSnapshot()
	for _, view := range views {
		if view.ProfileID != "swift" || !view.Status.RunRequested || len(view.Status.Intents) == 0 {
			t.Fatal("unrelated account monitoring was interrupted", view.AccountID)
		}
	}
	results, err := m.AutomationResults()
	if err != nil || len(results) != 1 || results[0].State != "submitted" {
		t.Fatal("aggregate result incorrect", results, err)
	}
	t.Logf("Offline webhook-to-two-account acceptance: %s (mocked gateway; not live provider latency)", time.Since(arrival))
}

func TestIndependentProfileSetupPersistsWithoutOverwriting(t *testing.T) {
	m, password, actor, path, state := automaticFixture(t)
	p := m.Policies().TradeManagers[0]
	p.PerTradeOnly = true
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	second := p
	second.ID = "second"
	second.Name = "Second"
	second.AccountIDs = []int64{43}
	if err := m.SaveTradeManager(second); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAutomation(Automation{ID: "second", Name: "Second", Strategy: "second", TradeManagerID: "second", AccountID: 43, Instruments: []string{"MNQ"}, Source: "tradingview-manual", ExecutionMode: "automatic", ContractID: "mnq", OrderType: "limit"}); err != nil {
		t.Fatal(err)
	}
	service := app.New(multiFlow{&flowAPI{}, 42}, 42)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Attach(service)
	auth := execution.Authorization{PracticeConfirmed: true, BracketsConfirmed: true, Window: session.Window{Verified: true}}
	first := execution.Config{Enabled: true, AccountID: 42, TradeManagerID: p.ID, RoundTripCosts: 3, SlippageTicks: 2, MaxQuantity: 2, MaxDeviationTicks: 4}
	if err := m.SaveProfileSetup(actor, password, "swift", first, true, auth); err != nil {
		t.Fatal(err)
	}
	other := first
	other.AccountID = 43
	other.TradeManagerID = "second"
	other.MaxQuantity = 5
	other.RoundTripCosts = 7
	other.DisablePriceDeviation = true
	if err := m.SaveProfileSetup(actor, password, "second", other, true, auth); err != nil {
		t.Fatal(err)
	}
	if m.ProfileConfig("swift").MaxQuantity != 2 || m.ProfileConfig("swift").RoundTripCosts != 3 || m.ProfileConfig("second").MaxQuantity != 5 {
		t.Fatal("profile setup overwritten")
	}
	config := m.Settings().Config
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	next, _, err := Open(config, path, state)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	for _, id := range []string{"swift", "second"} {
		if ready, why := next.FleetReadiness(id); !ready {
			t.Fatal(id, why)
		}
	}
	if next.ProfileConfig("swift").MaxQuantity != 2 || next.ProfileConfig("second").MaxQuantity != 5 || !next.ProfileConfig("second").DisablePriceDeviation {
		t.Fatal("profile setup not durable")
	}
	if next.RuntimeStatus().Armed || next.RuntimeStatus().RunRequested {
		t.Fatal("configuration restored arming")
	}
	// Changing one manager's schedule invalidates only its profile's setup.
	changed := next.Policies().TradeManagers[0]
	changed.Schedule = &session.Schedule{Start: "08:30", LastEntry: "13:00", Flatten: "13:05"}
	if err := next.SaveTradeManager(changed); err != nil {
		t.Fatal(err)
	}
	if ready, why := next.FleetReadiness("swift"); ready || !strings.Contains(why, "scope changed") {
		t.Fatal("schedule edit did not require scoped confirmation", ready, why)
	}
	if ready, why := next.FleetReadiness("second"); !ready {
		t.Fatal("unrelated manager setup invalidated", why)
	}
}
