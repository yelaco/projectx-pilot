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

	"projectxbot/internal/admin"
	"projectxbot/internal/app"
	"projectxbot/internal/config"
)

func TestFocusedPagesAndProfileSecretIsolation(t *testing.T) {
	t.Setenv("PROJECTX_USERNAME", "")
	t.Setenv("PROJECTX_API_KEY", "")
	t.Setenv("BOT_DASHBOARD_TOKEN", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	manager, password, err := admin.Open(c, path, filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for _, id := range []string{"first", "new"} {
		if err := manager.SaveAutomation(admin.Automation{ID: id, Name: "Profile " + id, Strategy: "strategy-" + id, AccountID: 7, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}); err != nil {
			t.Fatal(err)
		}
	}
	firstToken := manager.Ingress().Routes[0].Token
	otherToken := manager.Ingress().Routes[1].Token
	h := AdminHandler(app.New(nil, 0), password, "127.0.0.1:8080", manager)
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		r.SetBasicAuth("local", password)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	cases := []struct{ path, want string }{
		{"/", "System overview"}, {"/automations", "Automation profiles"}, {"/automations/new", "Create an automation profile"},
		{"/accounts", "Execution permissions"}, {"/contracts", "Available contracts"}, {"/deliveries", "TradingView deliveries"},
		{"/signals", "Signal inbox"}, {"/activity", "Operational events"}, {"/settings", "Save settings"}, {"/settings/webhooks", "Apply webhook settings"},
		{"/automations/profiles/first", "Profile configuration"}, {"/automations/profiles/new", "Profile new"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			w := get(tc.path)
			if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) || !strings.Contains(w.Body.String(), "</html>") {
				t.Fatalf("page %s: %d %s", tc.path, w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), firstToken) || strings.Contains(w.Body.String(), otherToken) {
				t.Fatal("profile secret escaped its settings/messages page")
			}
		})
	}
	home := get("/").Body.String()
	if strings.Contains(home, `action="/automation"`) || strings.Contains(home, `action="/settings"`) || strings.Contains(home, `action="/signal-import"`) {
		t.Fatal("overview still contains unrelated configuration forms")
	}
	for _, tab := range []string{"webhook", "messages"} {
		w := get("/automations/profiles/first?tab=" + tab)
		if w.Code != 200 || !strings.Contains(w.Body.String(), firstToken) || strings.Contains(w.Body.String(), otherToken) {
			t.Fatal("profile secret scoping failed", tab, w.Code)
		}
		if tab == "webhook" && !strings.Contains(w.Body.String(), `type="password"`) {
			t.Fatal("token not concealed by default")
		}
	}
	if notice := get("/automations/profiles/first?notice=automation"); !strings.Contains(notice.Body.String(), "Automation profiles updated.") {
		t.Fatal("profile save feedback missing")
	}
	if w := get("/automations/profiles/missing"); w.Code != 404 {
		t.Fatal("unknown profile accepted", w.Code)
	}
	// The one-click preset uses the same local authorization and CSRF checks.
	presetPage := get("/settings/webhooks")
	if !strings.Contains(presetPage.Body.String(), "Use Cloudflare preset") || !strings.Contains(presetPage.Body.String(), "connection-log-rows") || !strings.Contains(presetPage.Body.String(), "Pause log") {
		t.Fatal("preset or running log missing")
	}
	match := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(presetPage.Body.String())
	if len(match) != 2 {
		t.Fatal("missing preset CSRF")
	}
	values := url.Values{"preset": {"cloudflare-quick"}, "csrf": {match[1]}}
	presetRequest := httptest.NewRequest("POST", "http://127.0.0.1:8080/ingress", strings.NewReader(values.Encode()))
	presetRequest.SetBasicAuth("local", password)
	presetRequest.Header.Set("Origin", "http://127.0.0.1:8080")
	presetRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	presetResponse := httptest.NewRecorder()
	h.ServeHTTP(presetResponse, presetRequest)
	if presetResponse.Code != 303 || presetResponse.Header().Get("Location") != "/settings/webhooks?notice=ingress" || !manager.Ingress().Enabled || !manager.Ingress().QuickTunnel {
		t.Fatal("preset did not apply", presetResponse.Code)
	}
	if len(manager.Policies().Accounts) != 0 || manager.Settings().Config.Mode != "read-only" {
		t.Fatal("preset altered execution permissions")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/activity", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("new page is unauthenticated")
	}
}
func TestPostRedirectsStayWithTheirWorkflow(t *testing.T) {
	for _, tc := range []struct{ action, id, want string }{
		{"/settings", "", "/settings?notice=saved"}, {"/ingress", "", "/settings/webhooks?notice=saved"},
		{"/automation", "swift", "/automations/profiles/swift?notice=saved"}, {"/mapping", "swift", "/automations/profiles/swift?tab=webhook&notice=saved"},
		{"/delivery-import", "one", "/deliveries?notice=saved"}, {"/signal-review", "one", "/signals?notice=saved"}, {"/automation-delete", "swift", "/automations?notice=saved"},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080"+tc.action, strings.NewReader("id="+tc.id))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		redirectAction(w, r, "saved")
		if w.Code != 303 || w.Header().Get("Location") != tc.want {
			t.Fatalf("%s got %s", tc.action, w.Header().Get("Location"))
		}
	}
}
