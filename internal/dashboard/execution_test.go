package dashboard

import (
	"context"
	"net/http/httptest"
	"net/url"
	"projectxbot/internal/app"

	"projectxbot/internal/execution"
	"projectxbot/internal/risk"
	"strings"
	"testing"
)

func TestExecutionPageAndSecurity(t *testing.T) {
	m, password, h := managementFixture(t)
	page := requestAs(h, "local", password, "GET", "/execution", nil)
	csrf := formCSRF(t, page)
	for _, text := range []string{"Market Order · preview", "Limit Order · preview", "name=\"quantity\"", "name=\"stop_ticks\"", "name=\"target_ticks\"", "Emergency flatten"} {
		if !strings.Contains(page.Body.String(), text) {
			t.Fatalf("missing %s", text)
		}
	}
	if strings.Contains(page.Body.String(), password) {
		t.Fatal("password leaked")
	}
	if m.ExecutionStatus().Armed || m.ExecutionConfig().Enabled {
		t.Fatal("execution enabled by default")
	}
	for _, action := range []string{"/execution/review-oco-lock", "/execution/settings", "/execution/arm", "/execution/ticket", "/execution/submit", "/execution/flatten", "/execution/pause"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080"+action, strings.NewReader(url.Values{"csrf": {csrf}}.Encode()))
		r.SetBasicAuth("local", password)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://foreign.example")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("foreign origin accepted", action, w.Code)
		}
		if w = requestAs(h, "local", password, "POST", action, url.Values{"csrf": {"invalid"}}); w.Code != 403 {
			t.Fatal("CSRF bypass", action, w.Code)
		}
		if w = requestAs(h, "local", password, "GET", action, nil); w.Code != 404 {
			t.Fatal("GET performed action", action, w.Code)
		}
	}
	values := url.Values{"csrf": {csrf}, "current_password": {"wrong"}, "account_id": {"42"}, "costs": {"3"}, "slippage": {"2"}, "quantity": {"1"}, "deviation": {"4"}, "enabled": {"yes"}, "trade_manager_id": {"practice"}}
	if w := requestAs(h, "local", password, "POST", "/execution/settings", values); w.Code != 400 || !strings.Contains(w.Body.String(), "current administrator password required") {
		t.Fatal("reauthentication missing", w.Code, w.Body.String())
	}
	values = url.Values{"csrf": {csrf}, "order_type": {"market"}, "quantity": {"1"}, "stop_ticks": {"40"}, "target_ticks": {"80"}, "profile_id": {"manual"}, "contract_id": {"mnq"}, "direction": {"SELL"}}
	w := requestAs(h, "local", password, "POST", "/execution/ticket", values)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "enable and arm local execution first") {
		t.Fatal("disabled execution did not reject", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `value="SELL" selected`) {
		t.Fatal("manual ticket fields lost on error")
	}
}

func TestPracticeExecutionFloorUsesSavedTradeManagerPolicy(t *testing.T) {
	m, password, h := managementFixture(t)
	p := risk.Defaults()
	p.ID = "practice"
	p.Name = "Practice"
	p.AccountIDs = []int64{42}
	p.PerTradeOnly = true
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	actor := m.Access().List()[0].ID
	c := execution.Config{AccountID: 42, TradeManagerID: p.ID, RoundTripCosts: 3, SlippageTicks: 2, MaxQuantity: 1, MaxDeviationTicks: 4}
	if err := m.SaveExecution(actor, password, c); err != nil {
		t.Fatal(err)
	}
	page := requestAs(h, "local", password, "GET", "/execution", nil)
	if page.Code != 200 || strings.Contains(page.Body.String(), `name="floor"`) || strings.Contains(page.Body.String(), `name="floor_confirmed"`) || !strings.Contains(page.Body.String(), "Per-trade risk only") {
		t.Fatal("simplified manual page still shows legacy floor requirements")
	}
	csrf := formCSRF(t, page)
	// The rendered no-floor form must parse and reach server authorization.
	// This offline fixture has not granted account permission or started a monitor.
	fields := url.Values{"csrf": {csrf}, "current_password": {password}, "session_mode": {"normal"}, "session_date": {"2026-11-13"}, "hours_confirmed": {"yes"}, "practice_confirmed": {"yes"}, "brackets_confirmed": {"yes"}}
	result := requestAs(h, "local", password, "POST", "/execution/arm", fields)
	if result.Code != 400 || strings.Contains(result.Body.String(), "invalid verified loss floor") || strings.Contains(result.Body.String(), `name="floor"`) {
		t.Fatal("no-floor form failed parsing or error rerender restored field", result.Code)
	}
	// Backward compatibility: unchanged legacy policies still show their actual requirement.
	p.PerTradeOnly = false
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	legacy := requestAs(h, "local", password, "GET", "/execution", nil)
	if legacy.Code != 200 || !strings.Contains(legacy.Body.String(), `name="floor"`) || !strings.Contains(legacy.Body.String(), `name="floor_confirmed"`) {
		t.Fatal("legacy policy floor requirement hidden")
	}
	if m.ExecutionStatus().Armed {
		t.Fatal("rendering a page armed execution")
	}
}

func TestPracticeAPITestHasNoAutomationSelector(t *testing.T) {
	m, password, _ := managementFixture(t)
	service := app.New(inboxTestReader{}, 0)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := AdminHandler(service, password, "127.0.0.1:8080", m)
	page := requestAs(h, "local", password, "GET", "/execution", nil)
	if !strings.Contains(page.Body.String(), `value="mnq-contract"`) {
		t.Fatal("no-profile API test hid allowed contracts")
	}
	if page.Code != 200 || strings.Contains(page.Body.String(), `name="profile_id"`) || !strings.Contains(page.Body.String(), "No automation profile or webhook is required") {
		t.Fatal("API test still depends on profiles")
	}
	create := requestAs(h, "local", password, "GET", "/automations/new", nil)
	csrf := formCSRF(t, create)
	bad := requestAs(h, "local", password, "POST", "/automation", url.Values{"csrf": {csrf}, "source": {"local-manual"}})
	if bad.Code != 400 || !strings.Contains(bad.Body.String(), "webhook signals only") {
		t.Fatal("non-webhook profile accepted", bad.Code)
	}
	review := requestAs(h, "local", password, "GET", "/signals", nil)
	if strings.Contains(review.Body.String(), `action="/signal-import"`) {
		t.Fatal("manual signal import still exposed")
	}

	if strings.Contains(create.Body.String(), `value="local-manual"`) || strings.Contains(create.Body.String(), `value="local-strategy"`) || !strings.Contains(create.Body.String(), `name="source" value="tradingview-manual"`) {
		t.Fatal("non-webhook source choices remain")
	}
}

func TestDeviationToggleSavedThroughProtectedExecutionSettings(t *testing.T) {
	m, password, h := managementFixture(t)
	p := risk.Defaults()
	p.ID = "practice"
	p.Name = "Practice"
	p.AccountIDs = []int64{42}
	p.PerTradeOnly = true
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	page := requestAs(h, "local", password, "GET", "/execution", nil)
	if !strings.Contains(page.Body.String(), `name="deviation_disabled"`) {
		t.Fatal("missing deviation toggle")
	}
	csrf := formCSRF(t, page)
	fields := url.Values{"csrf": {csrf}, "account_id": {"42"}, "trade_manager_id": {"practice"}, "costs": {"3"}, "slippage": {"2"}, "quantity": {"1"}, "deviation": {"4"}, "deviation_disabled": {"yes"}, "current_password": {password}}
	if w := requestAs(h, "local", password, "POST", "/execution/settings", fields); w.Code != 303 {
		t.Fatal("save failed", w.Code)
	}
	if !m.ExecutionConfig().DisablePriceDeviation || m.ExecutionStatus().Armed {
		t.Fatal("setting not saved or save armed execution")
	}
	fields.Del("deviation_disabled")
	if w := requestAs(h, "local", password, "POST", "/execution/settings", fields); w.Code != 303 || m.ExecutionConfig().DisablePriceDeviation {
		t.Fatal("could not re-enable deviation")
	}
}

func TestOCOLockReviewRequiresExplicitConfirmationAndReauthentication(t *testing.T) {
	_, password, h := managementFixture(t)
	csrf := formCSRF(t, requestAs(h, "local", password, "GET", "/execution", nil))
	fields := url.Values{"csrf": {csrf}, "intent_id": {"old-order"}, "current_password": {password}}
	w := requestAs(h, "local", password, "POST", "/execution/review-oco-lock", fields)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "confirm review") {
		t.Fatal("review confirmation missing", w.Code)
	}
	fields.Set("review_confirmed", "yes")
	fields.Set("current_password", "wrong")
	w = requestAs(h, "local", password, "POST", "/execution/review-oco-lock", fields)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "current administrator password required") {
		t.Fatal("review bypassed reauthentication", w.Code)
	}
}
