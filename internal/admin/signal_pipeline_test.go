package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"projectxbot/internal/execution"
	"projectxbot/internal/ingress"
	"projectxbot/internal/session"
	"projectxbot/internal/signal"
	"projectxbot/internal/storage"
	"projectxbot/internal/webhook"
)

// Uses the real generated message, HTTP receiver, SQLite claim, manager and risk/
// execution engine. The only trading capability is an offline mock gateway.
func TestGeneratedTradingViewMessagesRouteAndSizeWithoutManualApproval(t *testing.T) {
	for _, direction := range []string{"BUY", "SELL"} {
		t.Run(direction, func(t *testing.T) {
			m, password, actor, _, state := automaticFixture(t)
			var stamp atomic.Int64
			base := time.Date(2026, 11, 13, 9, 0, 0, 0, session.Chicago)
			stamp.Store(base.UnixNano())
			clock := func() time.Time { return time.Unix(0, stamp.Load()) }
			m.now = clock
			cfg := m.ExecutionConfig()
			cfg.MaxQuantity = 10
			if err := m.SaveProfileExecution(actor, password, "swift", cfg, true); err != nil {
				t.Fatal(err)
			}
			buy := ingress.PriceMapping{Entry: `{{plot("Long Entry")}}`, Stop: `{{plot("Long SL")}}`, Target: `{{plot("Long TP")}}`}
			sell := ingress.PriceMapping{Entry: `{{plot("Short Entry")}}`, Stop: `{{plot("Short SL")}}`, Target: `{{plot("Short TP")}}`}
			if err := m.SaveMappings("swift", "separate", ingress.PriceMapping{}, buy, sell, false); err != nil {
				t.Fatal(err)
			}
			if err := m.SaveIngress(true, false, "https://alerts.example.test"); err != nil {
				t.Fatal(err)
			}
			m.remote, _ = storage.OpenWithClock(filepath.Join(state, "remote-alerts.db"), clock)
			if m.remote == nil {
				t.Fatal("mock inbox unavailable")
			}
			api := &flowAPI{}
			engine, err := execution.NewWithClock(m.executionStore, func(int64) (execution.API, execution.Quotes, error) { return api, flowQuotes{clock}, nil }, clock)
			if err != nil {
				t.Fatal(err)
			}
			m.executor = engine
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); m.RunExecution(ctx) }()
			t.Cleanup(func() { cancel(); <-done })
			window, _ := session.NormalWindow("2026-11-13")
			window.Verified = true
			auth := execution.Authorization{AutomationConfirmed: true, Window: window, Floor: 48000, AccountName: "PRAC-test", PracticeConfirmed: true, BracketsConfirmed: true, FloorConfirmed: true}
			deadline := time.Now().Add(time.Second)
			for {
				err = m.ArmProfileAutomation(context.Background(), actor, password, "swift", auth)
				if err == nil {
					break
				}
				if !strings.Contains(err.Error(), "monitor is not running") || time.Now().After(deadline) {
					t.Fatal(err)
				}
				time.Sleep(time.Millisecond)
			}
			stamp.Store(base.Add(time.Second).UnixNano())
			receiver, err := webhook.New(m.remote, webhook.Options{Enabled: true, MaxAge: 5 * time.Minute, Now: clock, Resolve: func(path string) (ingress.Route, bool) {
				r := m.Ingress().Routes[0]
				return r, path == "/tradingview/swift"
			}})
			if err != nil {
				t.Fatal(err)
			}
			template := m.Templates()[0]
			if !template.Ready || template.URL != "https://alerts.example.test/tradingview/swift" {
				t.Fatal("generated alert setup not ready")
			}
			draft := template.Buy
			if direction == "SELL" {
				draft = template.Sell
			}
			render := func() string {
				return strings.NewReplacer(
					buy.Entry, "20000", buy.Stop, "19990", buy.Target, "20020",
					sell.Entry, "20000", sell.Stop, "20010", sell.Target, "19980",
					"{{time}}", clock().UTC().Truncate(time.Minute).Format(time.RFC3339),
					"{{timenow}}", clock().UTC().Format(time.RFC3339),
				).Replace(draft)
			}
			post := func(body string, want int) {
				t.Helper()
				r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/tradingview/swift", strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				receiver.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("webhook status %d; expected %d", w.Code, want)
				}
			}
			body := render()
			var payload struct {
				Token  string        `json:"delivery_token"`
				Signal signal.Signal `json:"signal"`
			}
			if err = json.Unmarshal([]byte(body), &payload); err != nil {
				t.Fatal("generated message did not resolve to valid JSON", err)
			}
			post(body, 202)
			if api.count() != 0 {
				t.Fatal("receiver itself transmitted an order")
			}
			if err = m.processAutomation(context.Background()); err != nil {
				t.Fatal(err)
			}
			receipt, err := m.executionStore.AutomationReceipt(payload.Signal.ID)
			if err != nil || receipt.State != "submitted" {
				t.Fatal("generated webhook did not submit through local dispatcher", err)
			}
			api.mu.Lock()
			request := api.requests[0]
			api.mu.Unlock()
			// 40 ticks * $0.50 + 2 slippage ticks * $0.50 + $3 costs = $24.
			// $150 / $24 floors to six contracts, below the configured cap of ten.
			stop, target, side := -40, 80, 0
			if direction == "SELL" {
				stop, target, side = 40, -80, 1
			}
			if api.count() != 1 || request.AccountID != 42 || request.ContractID != "mnq" || request.Size != 6 || request.Side != side || request.StopLossBracket.Ticks != stop || request.TakeProfitBracket.Ticks != target {
				t.Fatal("routing, position sizing or bracket direction incorrect")
			}
			post(body, 200)
			if err = m.processAutomation(context.Background()); err != nil {
				t.Fatal(err)
			}
			if api.count() != 1 {
				t.Fatal("redelivery placed a duplicate order")
			}
			bad := payload
			bad.Token = "INVALID_MOCK_TOKEN"
			b, _ := json.Marshal(bad)
			post(string(b), 401)
			bad = payload
			bad.Signal.Symbol = "MGC"
			b, _ = json.Marshal(bad)
			post(string(b), 422)
			bad = payload
			bad.Signal.Timestamp = clock().Add(-10 * time.Minute)
			b, _ = json.Marshal(bad)
			post(string(b), 422)
			m.PauseExecution()
			stamp.Store(base.Add(61 * time.Second).UnixNano())
			next := render()
			post(next, 202)
			if err = m.processAutomation(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal([]byte(next), &payload); err != nil {
				t.Fatal(err)
			}
			blocked, err := m.executionStore.AutomationReceipt(payload.Signal.ID)
			if err != nil || blocked.State != "blocked" || api.count() != 1 {
				t.Fatal("paused alerts were not blocked")
			}
		})
	}
}
