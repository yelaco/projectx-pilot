package dashboard

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/admin"
	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/config"
)

func managementFixture(t *testing.T) (*admin.Manager, string, http.Handler) {
	t.Helper()
	t.Setenv("PROJECTX_USERNAME", "")
	t.Setenv("PROJECTX_API_KEY", "")
	t.Setenv("BOT_DASHBOARD_TOKEN", "")
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	m, password, err := admin.OpenWithClock(c, p, filepath.Join(dir, "state"), func() time.Time { return time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m, password, AdminHandler(app.New(nil, 0), password, "127.0.0.1:8080", m)
}
func requestAs(h http.Handler, user, password, method, path string, values url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(values.Encode()))
	r.SetBasicAuth(user, password)
	if method == "POST" {
		r.Header.Set("Origin", "http://127.0.0.1:8080")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func formCSRF(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != 200 {
		t.Fatal("form unavailable", w.Code, w.Body.String())
	}
	m := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(w.Body.String())
	if len(m) != 2 {
		t.Fatal("missing CSRF")
	}
	return m[1]
}
func TestAdministratorManagementOverHTTP(t *testing.T) {
	m, initial, h := managementFixture(t)
	csrf := formCSRF(t, requestAs(h, "local", initial, "GET", "/settings/access", nil))
	values := url.Values{"csrf": {csrf}, "action": {"add"}, "username": {"second"}, "current_password": {"wrong"}, "new_password": {"second-secret-password"}, "confirm_password": {"second-secret-password"}}
	if w := requestAs(h, "local", initial, "POST", "/admin-access", values); w.Code != 400 {
		t.Fatal("current password not required", w.Code)
	}
	values.Set("current_password", initial)
	if w := requestAs(h, "local", initial, "POST", "/admin-access", values); w.Code != 303 {
		t.Fatal("could not add admin", w.Code, w.Body.String())
	}
	secondPage := requestAs(h, "second", "second-secret-password", "GET", "/settings/access", nil)
	secondCSRF := formCSRF(t, secondPage)
	for _, secret := range []string{initial, "second-secret-password"} {
		if strings.Contains(secondPage.Body.String(), secret) {
			t.Fatal("password echoed")
		}
	}
	var secondID string
	for _, u := range m.Access().List() {
		if u.Username == "second" {
			secondID = u.ID
		}
	}
	update := url.Values{"csrf": {secondCSRF}, "action": {"update"}, "target_id": {secondID}, "username": {"owner-two"}, "current_password": {"second-secret-password"}, "new_password": {"replacement-secret-password"}, "confirm_password": {"replacement-secret-password"}}
	if w := requestAs(h, "second", "second-secret-password", "POST", "/admin-access", update); w.Code != 303 {
		t.Fatal("update failed", w.Code, w.Body.String())
	}
	if w := requestAs(h, "second", "second-secret-password", "GET", "/", nil); w.Code != 401 {
		t.Fatal("old login still valid")
	}
	if w := requestAs(h, "owner-two", "replacement-secret-password", "GET", "/", nil); w.Code != 200 {
		t.Fatal("new login rejected", w.Code)
	}
	if w := requestAs(h, "owner-two", "replacement-secret-password", "POST", "/refresh", url.Values{"csrf": {secondCSRF}}); w.Code != 403 {
		t.Fatal("old form token still valid", w.Code)
	}
	remove := url.Values{"csrf": {csrf}, "action": {"delete"}, "target_id": {secondID}, "current_password": {initial}}
	if w := requestAs(h, "local", initial, "POST", "/admin-access", remove); w.Code != 303 {
		t.Fatal("revoke failed", w.Code)
	}
	if w := requestAs(h, "owner-two", "replacement-secret-password", "GET", "/", nil); w.Code != 401 {
		t.Fatal("revoked admin still valid")
	}
	remove.Set("target_id", m.Access().List()[0].ID)
	if w := requestAs(h, "local", initial, "POST", "/admin-access", remove); w.Code != 400 {
		t.Fatal("last admin removed", w.Code)
	}
}
func TestTradePoliciesAndDerivedWebhookURLsOverHTTP(t *testing.T) {
	m, password, h := managementFixture(t)
	csrf := formCSRF(t, requestAs(h, "local", password, "GET", "/trade-managers/new", nil))
	fields := url.Values{"csrf": {csrf}, "id": {"practice"}, "name": {"Practice limits"}, "routing": {"single"}, "account_ids_csv": {"42"}, "max_risk_per_trade": {"150"}}
	if w := requestAs(h, "local", password, "POST", "/trade-manager", fields); w.Code != 303 {
		t.Fatal("save policy failed", w.Code, w.Body.String())
	}
	if p := m.Policies().TradeManagers[0]; !p.PerTradeOnly || p.MaxRiskPerTrade != 150 {
		t.Fatal("simple policy not persisted", p)
	}
	if w := requestAs(h, "local", password, "GET", "/trade-managers/profiles/practice", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Practice limits") {
		t.Fatal("policy editor failed", w.Code)
	}
	a := url.Values{"csrf": {csrf}, "id": {"swift"}, "name": {"Swift practice"}, "strategy": {"swift"}, "instruments": {"MNQ"}, "source": {"tradingview-manual"}, "trade_manager_id": {"practice"}}
	if w := requestAs(h, "local", password, "POST", "/automation", a); w.Code != 303 {
		t.Fatal("manager assignment failed", w.Code, w.Body.String())
	}
	if m.Policies().Automations[0].AccountID != 42 {
		t.Fatal("wrong single-account routing")
	}
	if err := m.SaveIngress(true, false, "https://one.example"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/automations", "/automations/profiles/swift", "/automations/profiles/swift?tab=webhook", "/automations/profiles/swift?tab=messages"} {
		w := requestAs(h, "local", password, "GET", path, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `https://one.example/tradingview/swift`) {
			t.Fatalf("derived URL missing on %s", path)
		}
	}
	for _, path := range []string{"/", "/settings/webhooks", "/api/monitor"} {
		w := requestAs(h, "local", password, "GET", path, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "https://one.example") || !strings.Contains(w.Body.String(), "Permanent hostname") {
			t.Fatal("saved hostname missing", path, w.Code)
		}
	}
	monitor := requestAs(h, "local", password, "GET", "/api/monitor", nil)
	if !strings.Contains(monitor.Body.String(), `"WebhookURLs":{"swift":"https://one.example/tradingview/swift"}`) {
		t.Fatal("monitor missing canonical endpoints")
	}
	_ = m.SaveIngress(true, false, "https://two.example")
	w := requestAs(h, "local", password, "GET", "/automations/profiles/swift", nil)
	if !strings.Contains(w.Body.String(), "https://two.example/tradingview/swift") || strings.Contains(w.Body.String(), "https://one.example") {
		t.Fatal("URL did not follow tunnel host")
	}
	fields.Set("routing", "multiple")
	fields.Set("account_ids_csv", "42,43")
	if w := requestAs(h, "local", password, "POST", "/trade-manager", fields); w.Code != 303 {
		t.Fatal("multi policy save failed", w.Code, w.Body.String())
	}
	w = requestAs(h, "local", password, "GET", "/automations/profiles/swift", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Account #0") {
		t.Fatal("multi policy rendered as fake single account", w.Code)
	}
	if w := requestAs(h, "local", password, "POST", "/trade-manager-delete", url.Values{"csrf": {csrf}, "id": {"practice"}}); w.Code != 400 {
		t.Fatal("referenced manager deleted", w.Code)
	}
}
