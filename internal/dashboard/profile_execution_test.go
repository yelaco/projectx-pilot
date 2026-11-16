package dashboard

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"projectxbot/internal/admin"
	"projectxbot/internal/app"
	"projectxbot/internal/projectx"
	"projectxbot/internal/risk"
)

func TestProfileExecutionSetupDoesNotRequirePracticePage(t *testing.T) {
	m, password, _ := managementFixture(t)
	if err := m.Save(admin.Update{AccountID: 7, Instruments: []string{"MNQ"}, RefreshSeconds: 60, Username: "offline", APIKey: "not-a-real-api-key"}); err != nil {
		t.Fatal(err)
	}
	s := app.New(continuousProfileReader{}, 0)
	m.Attach(s)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := AdminHandler(s, password, "127.0.0.1:8080", m)
	p := risk.Defaults()
	p.PerTradeOnly = true
	p.ID = "practice"
	p.Name = "Practice"
	p.AccountIDs = []int64{7}
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAutomation(admin.Automation{ID: "swift", Name: "Swift", Strategy: "swift", AccountID: 7, TradeManagerID: "practice", Instruments: []string{"MNQ"}, Source: "tradingview-manual", ExecutionMode: "automatic", ContractID: "mnq-contract", OrderType: "market"}); err != nil {
		t.Fatal(err)
	}
	page := requestAs(h, "local", password, "GET", "/automations/profiles/swift?tab=execution", nil)
	csrf := formCSRF(t, page)
	if strings.Contains(page.Body.String(), `name="floor"`) {
		t.Fatal("simplified setup still requires account floor")
	}
	for _, action := range []string{"/automation-setup", "/automation-start", "/automation-pause", "/automation-flatten"} {
		if !strings.Contains(page.Body.String(), `action="`+action+`"`) {
			t.Fatal("missing profile control", action)
		}
	}
	if strings.Contains(page.Body.String(), `action="/execution/settings"`) || strings.Contains(page.Body.String(), `action="/execution/arm"`) || strings.Contains(page.Body.String(), "Go to Practice execution") {
		t.Fatal("profile still depends on manual page")
	}
	values := url.Values{"csrf": {csrf}, "id": {"swift"}, "account_id": {"7"}, "trade_manager_id": {"practice"}, "costs": {"3"}, "slippage": {"2"}, "quantity": {"1"}, "deviation": {"4"}, "enabled": {"yes"}, "deviation_disabled": {"yes"}, "setup_confirmed": {"yes"}, "current_password": {password}}
	if w := requestAs(h, "local", password, "POST", "/automation-setup", values); w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "tab=execution") {
		t.Fatal("profile save failed", w.Code, w.Body.String())
	}
	if !m.ProfileConfig("swift").DisablePriceDeviation {
		t.Fatal("profile setup lost deviation toggle")
	}
	if !m.Policies().Allowed(7) || !m.ProfileConfig("swift").Enabled || m.ProfileConfig("swift").AutomationProfileID != "swift" || m.ExecutionStatus().Armed {
		t.Fatal("profile save did not enable capability safely")
	}
	values.Set("account_id", "99")
	if w := requestAs(h, "local", password, "POST", "/automation-setup", values); w.Code != 400 || !strings.Contains(w.Body.String(), "changed; reload") {
		t.Fatal("target switch accepted", w.Code)
	}

	if ready, why := m.FleetReadiness("swift"); !ready {
		t.Fatal(why)
	}
	// The complete profile flow reaches session arming, without invoking any
	// manual-page route. No monitor/network is started in this form test.
	values = url.Values{"csrf": {csrf}, "id": {"swift"}}
	if w := requestAs(h, "local", password, "POST", "/automation-start", values); w.Code != 400 || !strings.Contains(w.Body.String(), "runner service is starting") {
		t.Fatal("one-click start did not reach controller", w.Code, w.Body.String())
	}
	for _, action := range []string{"/automation-setup", "/automation-start", "/automation-pause", "/automation-flatten"} {
		values.Set("csrf", "invalid")
		if w := requestAs(h, "local", password, "POST", action, values); w.Code != 403 {
			t.Fatal("profile CSRF bypass", action, w.Code)
		}
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080"+action, strings.NewReader(values.Encode()))
		r.SetBasicAuth("local", password)
		r.Header.Set("Origin", "https://foreign.example")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("profile origin bypass")
		}
	}
	manual := requestAs(h, "local", password, "GET", "/execution", nil)
	if manual.Code != 200 || strings.Contains(manual.Body.String(), `name="arm_mode"`) {
		t.Fatal("manual page still offers automatic arming")
	}
}

type continuousProfileReader struct{ inboxTestReader }

func (continuousProfileReader) Accounts(context.Context) ([]projectx.Account, error) {
	sim := true
	return []projectx.Account{{ID: 7, Name: "PRAC-test", CanTrade: true, Simulated: &sim}}, nil
}

func TestFleetControlsRequireLocalAuthorization(t *testing.T) {
	m, password, _ := managementFixture(t)
	h := AdminHandler(app.New(nil, 0), password, "127.0.0.1:8080", m)
	page := requestAs(h, "local", password, "GET", "/runners", nil)
	csrf := formCSRF(t, page)
	for _, path := range []string{"/fleet/pause", "/fleet/resume", "/fleet/flatten", "/fleet/account-pause", "/fleet/account-flatten", "/fleet/review-oco"} {
		values := url.Values{"csrf": {"invalid"}, "account_id": {"7"}}
		if w := requestAs(h, "local", password, "POST", path, values); w.Code != 403 {
			t.Fatal("CSRF bypass", path, w.Code)
		}
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080"+path, strings.NewReader(values.Encode()))
		r.SetBasicAuth("local", password)
		r.Header.Set("Origin", "https://foreign.example")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("origin bypass", path, w.Code)
		}
	}
	values := url.Values{"csrf": {csrf}}
	if w := requestAs(h, "local", password, "POST", "/fleet/pause", values); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	_, paused := m.RunnerSnapshot()
	if !paused {
		t.Fatal("global pause absent")
	}
	if w := requestAs(h, "local", password, "POST", "/fleet/flatten", values); w.Code != 400 {
		t.Fatal("flatten without password accepted", w.Code)
	}
	if w := requestAs(h, "local", password, "POST", "/fleet/resume", values); w.Code != 303 {
		t.Fatal(w.Code)
	}
	_, paused = m.RunnerSnapshot()
	if paused {
		t.Fatal("global resume failed")
	}
	if m.RuntimeStatus().Armed {
		t.Fatal("resume armed")
	}
}

type multipleProfileReader struct{ continuousProfileReader }

func (multipleProfileReader) Accounts(context.Context) ([]projectx.Account, error) {
	sim := true
	return []projectx.Account{{ID: 7, Name: "Combine seven", CanTrade: true, Simulated: &sim, Balance: 50000}, {ID: 8, Name: "Express eight", CanTrade: true, Simulated: &sim, Balance: 50000}}, nil
}
func TestMultipleAccountSetupFormGrantsWholeVisibleScope(t *testing.T) {
	m, password, _ := managementFixture(t)
	if err := m.Save(admin.Update{AccountID: 7, Instruments: []string{"MNQ"}, RefreshSeconds: 60, Username: "offline", APIKey: "not-a-real-key"}); err != nil {
		t.Fatal(err)
	}
	service := app.New(multipleProfileReader{}, 7)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Attach(service)
	policy := risk.Defaults()
	policy.PerTradeOnly = true
	policy.ID = "multi"
	policy.Name = "Multi"
	policy.Routing = "multiple"
	policy.AccountIDs = []int64{7, 8}
	if err := m.SaveTradeManager(policy); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAutomation(admin.Automation{ID: "multi", Name: "Multi", Strategy: "multi", TradeManagerID: "multi", Instruments: []string{"MNQ"}, Source: "tradingview-manual", ExecutionMode: "automatic", ContractID: "mnq", OrderType: "limit"}); err != nil {
		t.Fatal(err)
	}
	h := AdminHandler(service, password, "127.0.0.1:8080", m)
	page := requestAs(h, "local", password, "GET", "/automations/profiles/multi?tab=execution", nil)
	for _, name := range []string{"Combine seven", "Express eight"} {
		if !strings.Contains(page.Body.String(), name) {
			t.Fatal("target scope not displayed", name)
		}
	}
	if strings.Contains(page.Body.String(), "One execution session can run at a time") {
		t.Fatal("obsolete restriction displayed")
	}
	values := url.Values{"csrf": {formCSRF(t, page)}, "id": {"multi"}, "account_id": {"0"}, "trade_manager_id": {"multi"}, "costs": {"3"}, "slippage": {"2"}, "quantity": {"4"}, "deviation": {"4"}, "enabled": {"yes"}, "setup_confirmed": {"yes"}, "current_password": {password}}
	w := requestAs(h, "local", password, "POST", "/automation-setup", values)
	if w.Code != 303 {
		t.Fatal("multi-account setup form failed", w.Code, w.Body.String())
	}
	if !m.Policies().Allowed(7) || !m.Policies().Allowed(8) || m.ProfileConfig("multi").AccountID != 0 {
		t.Fatal("target set silently reduced")
	}
	if ready, why := m.FleetReadiness("multi"); !ready {
		t.Fatal(why)
	}
	if m.RuntimeStatus().Armed {
		t.Fatal("setup armed accounts")
	}
}
