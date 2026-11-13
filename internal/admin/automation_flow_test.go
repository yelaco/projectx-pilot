package admin

import (
	"context"
	"encoding/json"
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
	"projectxbot/internal/session"
	"projectxbot/internal/signal"
	"projectxbot/internal/storage"
	"projectxbot/internal/webhook"
)

type flowAPI struct {
	mu       sync.Mutex
	requests []projectx.PlaceRequest
}

func (f *flowAPI) Accounts(context.Context) ([]projectx.Account, error) {
	sim := true
	return []projectx.Account{{ID: 42, Name: "PRAC-test", CanTrade: true, Simulated: &sim, Balance: 50000}}, nil
}
func (f *flowAPI) Contract(context.Context, string) (projectx.Contract, error) {
	return projectx.Contract{ID: "mnq", SymbolID: "F.US.MNQ", TickSize: .25, TickValue: .5, ActiveContract: true}, nil
}
func (f *flowAPI) OpenOrders(context.Context, int64) ([]projectx.Order, error) {
	return []projectx.Order{}, nil
}
func (f *flowAPI) OpenPositions(context.Context, int64) ([]projectx.Position, error) {
	return []projectx.Position{}, nil
}
func (f *flowAPI) Orders(context.Context, int64, time.Time, time.Time) ([]projectx.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return []projectx.Order{}, nil
	}
	r := f.requests[0]
	return []projectx.Order{{ID: 77, AccountID: 42, ContractID: "mnq", Type: r.Type, Side: r.Side, Size: r.Size, Status: 1, CustomTag: r.CustomTag}}, nil
}
func (f *flowAPI) Trades(context.Context, int64, time.Time, time.Time) ([]projectx.Trade, error) {
	return []projectx.Trade{}, nil
}
func (f *flowAPI) Place(_ context.Context, r projectx.PlaceRequest) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r)
	return 77, nil
}
func (f *flowAPI) Cancel(context.Context, int64) error         { return nil }
func (f *flowAPI) CloseContract(context.Context, string) error { return nil }
func (f *flowAPI) count() int                                  { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }

type flowQuotes struct{ now func() time.Time }

func (q flowQuotes) Watch(context.Context, string) {}
func (q flowQuotes) Close()                        {}
func (q flowQuotes) Get(string) (realtime.Quote, error) {
	return realtime.Quote{Price: 20000, At: q.now()}, nil
}
func TestManagerDispatchesApprovedWebhookAndBlocksDisarmedBacklog(t *testing.T) {
	m, password, actor, _, state := automaticFixture(t)
	var stamp atomic.Int64
	base := time.Date(2026, 11, 13, 9, 0, 0, 0, session.Chicago)
	stamp.Store(base.UnixNano())
	clock := func() time.Time { return time.Unix(0, stamp.Load()) }
	m.now = clock
	api := &flowAPI{}
	engine, err := execution.NewWithClock(m.executionStore, func(int64) (execution.API, execution.Quotes, error) { return api, flowQuotes{clock}, nil }, clock)
	if err != nil {
		t.Fatal(err)
	}
	m.executor = engine
	m.remote, err = storage.OpenWithClock(filepath.Join(state, "remote-alerts.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := webhook.New(m.remote, webhook.Options{Enabled: true, MaxAge: 5 * time.Minute, Now: clock, Resolve: func(path string) (ingress.Route, bool) {
		r := m.Ingress().Routes[0]
		return r, path == "/tradingview/swift"
	}})
	if err != nil {
		t.Fatal(err)
	}
	send := func(id string) {
		t.Helper()
		s := signal.Signal{ID: id, Strategy: "swift", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19990, TakeProfit: 20020, Timestamp: clock()}
		b, _ := json.Marshal(map[string]any{"delivery_token": m.Ingress().Routes[0].Token, "signal": s})
		req := httptest.NewRequest("POST", "http://127.0.0.1:8090/tradingview/swift", strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		receiver.ServeHTTP(w, req)
		if w.Code != http.StatusAccepted && w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	send("before-arming")
	if err = m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	blocked, err := m.executionStore.AutomationReceipt("before-arming")
	if err != nil || blocked.State != "blocked" || api.count() != 0 {
		t.Fatal(blocked, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.RunExecution(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	w, _ := session.NormalWindow("2026-11-13")
	w.Verified = true
	a := execution.Authorization{AutomationConfirmed: true, Window: w, Floor: 48000, PracticeConfirmed: true, BracketsConfirmed: true, FloorConfirmed: true, AccountName: "PRAC-test"}
	deadline := time.Now().Add(time.Second)
	for {
		err = m.ArmProfileAutomation(context.Background(), actor, password, "swift", a)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "monitor is not running") || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	stamp.Store(base.Add(time.Second).UnixNano())
	send("execute-once")
	if err = m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := m.executionStore.AutomationReceipt("execute-once")
	if err != nil || result.State != "submitted" || api.count() != 1 {
		t.Fatal(result, err, api.count())
	}
	record, err := m.remote.Get("execute-once")
	if err != nil || record.Status != "reviewed" {
		t.Fatal(record, err)
	}
	send("execute-once")
	if err = m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.count() != 1 {
		t.Fatal("manager replayed duplicated delivery")
	}
	if err = m.PauseProfileAutomation("other"); err == nil {
		t.Fatal("another profile paused this session")
	}
	if err = m.FlattenProfileAutomation(context.Background(), actor, password, "other"); err == nil {
		t.Fatal("another profile flattened this session")
	}
	if !m.ExecutionStatus().Armed {
		t.Fatal("foreign profile controls disarmed active automation")
	}
	if err = m.PauseProfileAutomation("swift"); err != nil {
		t.Fatal(err)
	}
	stamp.Store(base.Add(2 * time.Second).UnixNano())
	send("after-pause")
	if err = m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err = m.executionStore.AutomationReceipt("after-pause")
	if err != nil || result.State != "blocked" || api.count() != 1 {
		t.Fatal(result, err)
	}
}
