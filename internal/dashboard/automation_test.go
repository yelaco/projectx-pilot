package dashboard

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"projectxbot/internal/admin"
	"projectxbot/internal/risk"
)

func TestAutomaticRoutingTabDefaultsAndApprovalSecurity(t *testing.T) {
	m, password, h := managementFixture(t)
	p := risk.Defaults()
	p.ID = "practice"
	p.Name = "Practice"
	p.AccountIDs = []int64{42}
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAutomation(admin.Automation{ID: "swift", Name: "Swift", Strategy: "swift", AccountID: 42, TradeManagerID: "practice", Instruments: []string{"MNQ"}, Source: "tradingview-manual"}); err != nil {
		t.Fatal(err)
	}
	path := "/automations/profiles/swift?tab=execution"
	page := requestAs(h, "local", password, "GET", path, nil)
	csrf := formCSRF(t, page)
	if !strings.Contains(page.Body.String(), "Signal routing") || strings.Contains(page.Body.String(), "approval_reference") || !strings.Contains(page.Body.String(), `value="review" selected`) {
		t.Fatal("automatic settings missing or default changed")
	}
	values := url.Values{"csrf": {csrf}, "id": {"swift"}, "execution_mode": {"automatic"}, "contract_id": {"mnq"}, "order_type": {"market"}}
	if w := requestAs(h, "local", password, "POST", "/automation-execution", values); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	if m.Policies().Automations[0].ExecutionMode != "automatic" || m.ExecutionStatus().Armed {
		t.Fatal("routing did not save or automatically armed")
	}
	if ready, _ := m.AutomationReadiness(); ready {
		t.Fatal("routing granted approval")
	}

}
func TestEasySessionPickerRetainsCalendarConfirmation(t *testing.T) {
	_, password, h := managementFixture(t)
	page := requestAs(h, "local", password, "GET", "/execution", nil)
	if page.Code != 200 {
		t.Fatal(page.Code, page.Body.String())
	}
	for _, text := range []string{`name="session_date" type="date"`, `name="start" type="datetime-local"`, `Normal session · automatic Chicago times`, `no retyping needed`} {
		if !strings.Contains(page.Body.String(), text) {
			t.Fatal("missing simplified control", text)
		}
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1/execution/arm", nil)
	req.PostForm = url.Values{"session_mode": {"normal"}, "session_date": {"2026-11-13"}}
	w, err := executionWindow(req)
	if err != nil || w.Verified {
		t.Fatal("preset auto-confirmed holidays", w, err)
	}
	req.PostForm.Set("hours_confirmed", "yes")
	w, err = executionWindow(req)
	if err != nil || !w.Verified || w.Flatten.UTC().Format(time.RFC3339) != "2026-11-13T21:05:00Z" {
		t.Fatal(w, err)
	}
	req.PostForm = url.Values{"session_mode": {"custom"}, "start": {"2026-11-13T08:00"}, "last_entry": {"2026-11-13T10:00"}, "flatten": {"2026-11-13T10:05"}, "hours_confirmed": {"yes"}}
	w, err = executionWindow(req)
	if err != nil || w.Start.UTC().Hour() != 14 {
		t.Fatal("custom picker interpreted desktop timezone", w, err)
	}
}

func TestAutomaticRoutingFailurePreservesFieldsAndCanAssignManager(t *testing.T) {
	m, password, h := managementFixture(t)
	p := risk.Defaults()
	p.ID = "practice"
	p.Name = "Practice"
	p.AccountIDs = []int64{42}
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAutomation(admin.Automation{ID: "swift", Name: "Swift", Strategy: "swift", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}); err != nil {
		t.Fatal(err)
	}
	csrf := formCSRF(t, requestAs(h, "local", password, "GET", "/automations/profiles/swift?tab=execution", nil))
	values := url.Values{"csrf": {csrf}, "id": {"swift"}, "execution_mode": {"automatic"}, "contract_id": {"mnq-contract"}, "order_type": {"limit"}, "trade_manager_id": {""}}
	w := requestAs(h, "local", password, "POST", "/automation-execution", values)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "assign a single-account Trade Manager") || !strings.Contains(w.Body.String(), `value="automatic" selected`) || !strings.Contains(w.Body.String(), `value="limit" selected`) {
		t.Fatal("lost routing selection or missing diagnostic", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `value="mnq-contract" selected`) {
		t.Fatal("contract selection lost after failure")
	}
	values.Set("trade_manager_id", "practice")
	if w = requestAs(h, "local", password, "POST", "/automation-execution", values); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	if m.Policies().Automations[0].TradeManagerID != "practice" || m.ExecutionStatus().Armed {
		t.Fatal("assignment not saved safely")
	}
}
