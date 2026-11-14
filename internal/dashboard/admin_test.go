package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"projectxbot/internal/projectx"
	"projectxbot/internal/signal"
	"projectxbot/internal/storage"
	"regexp"
	"strings"
	"testing"
	"time"

	"projectxbot/internal/admin"
	"projectxbot/internal/app"
	"projectxbot/internal/config"
)

func TestAdminSettingsSecurityAndPersistence(t *testing.T) {
	t.Setenv("PROJECTX_USERNAME", "")
	t.Setenv("PROJECTX_API_KEY", "")
	t.Setenv("BOT_DASHBOARD_TOKEN", "")
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	_ = os.WriteFile(p, []byte("{}"), 0600)
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	m, token, err := admin.Open(c, p, filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	s := app.New(nil, 0)
	m.Attach(s)
	h := AdminHandler(s, token, "127.0.0.1:8080", m)
	get := httptest.NewRequest("GET", "http://127.0.0.1:8080/", nil)
	get.SetBasicAuth("local", token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, get)
	if w.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("referrer policy suppresses browser origin")
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), "System overview") || !strings.Contains(w.Body.String(), "</html>") {
		t.Fatalf("template render failed: %d %s", w.Code, w.Body.String())
	}
	pageGet := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		req.SetBasicAuth("local", token)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		return response
	}
	match := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(w.Body.String())
	if len(match) != 2 {
		t.Fatal("missing CSRF token")
	}
	// Simulate a service restart after the browser loaded its form.
	h = AdminHandler(s, token, "127.0.0.1:8080", m)
	values := url.Values{"csrf": {match[1]}, "account_id": {"7"}, "instruments": {"mnq, mgc"}, "refresh_seconds": {"90"}, "news_filter": {"off"}, "username": {"local-user"}, "api_key": {"secret-key"}}
	for _, tc := range []struct {
		name, origin, csrf string
		auth               bool
		want               int
	}{{"no auth", "http://127.0.0.1:8080", match[1], false, 401}, {"cross origin", "http://evil.test", match[1], true, 403}, {"no origin", "", match[1], true, 403}, {"bad csrf", "http://127.0.0.1:8080", "bad", true, 403}, {"valid", "http://127.0.0.1:8080", match[1], true, 303}} {
		t.Run(tc.name, func(t *testing.T) {
			values.Set("csrf", tc.csrf)
			r := httptest.NewRequest("POST", "http://127.0.0.1:8080/settings", strings.NewReader(values.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", tc.origin)
			if tc.auth {
				r.SetBasicAuth("local", token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "secret-key") {
				t.Fatal("secret echoed")
			}
		})
	}
	saved, err := config.Load(p)
	if err != nil || saved.NewsFilter != "off" || saved.AccountID != 7 || len(saved.AllowedInstruments) != 2 {
		t.Fatal("settings not saved", err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, get)
	if strings.Contains(w.Body.String(), "secret-key") {
		t.Fatal("secret rendered")
	}
	for _, tokenCase := range []struct {
		csrf string
		want int
	}{{match[1], 303}, {"invalid", 403}} {
		values.Set("csrf", tokenCase.csrf)
		request := httptest.NewRequest("POST", "http://127.0.0.1:8080/settings", strings.NewReader(values.Encode()))
		request.SetBasicAuth("local", token)
		request.Header.Set("Origin", "null")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != tokenCase.want {
			t.Fatalf("opaque browser origin: got %d want %d", w.Code, tokenCase.want)
		}
	}
	// Exercise profile creation/edit rendering through the actual authenticated HTTP handler.
	profile := url.Values{"csrf": {match[1]}, "id": {"mnq-test"}, "name": {"Morning <test>"}, "strategy": {"breakout"}, "account_id": {"7"}, "instruments": {"MNQ"}, "source": {"tradingview-manual"}}
	request := httptest.NewRequest("POST", "http://127.0.0.1:8080/automation", strings.NewReader(profile.Encode()))
	request.SetBasicAuth("local", token)
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 303 {
		t.Fatalf("profile save: %d %s", w.Code, w.Body.String())
	}
	w = pageGet("/automations/profiles/mnq-test")
	if !strings.Contains(w.Body.String(), "Morning &lt;test&gt;") || !strings.Contains(w.Body.String(), "</html>") {
		t.Fatal("existing profile not rendered or escaped")
	}
	s.Configure(inboxTestReader{}, 0, time.Minute)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	alert := signal.Signal{ID: "web-alert-001", Strategy: "breakout", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19995, TakeProfit: 20010, Timestamp: time.Now().UTC()}
	alertJSON, _ := json.Marshal(alert)
	importValues := url.Values{"csrf": {match[1]}, "profile_id": {"mnq-test"}, "contract_id": {"mnq-contract"}, "payload": {string(alertJSON)}}
	// Seed historical records through the internal store interface; public JSON
	// import is retired, but stored review history remains readable.
	if err := m.ImportSignal("mnq-test", "mnq-contract", string(alertJSON)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{410} {
		request = httptest.NewRequest("POST", "http://127.0.0.1:8080/signal-import", strings.NewReader(importValues.Encode()))
		request.SetBasicAuth("local", token)
		request.Header.Set("Origin", "http://127.0.0.1:8080")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != want {
			t.Fatalf("signal import got %d want %d: %s", w.Code, want, w.Body.String())
		}
	}
	w = pageGet("/signals")
	if !strings.Contains(w.Body.String(), "web-alert-001") || !strings.Contains(w.Body.String(), "pending_review") || !strings.Contains(w.Body.String(), "</html>") {
		t.Fatal("signal row did not render")
	}
	review := url.Values{"csrf": {match[1]}, "id": {"web-alert-001"}, "status": {"reviewed"}}
	request = httptest.NewRequest("POST", "http://127.0.0.1:8080/signal-review", strings.NewReader(review.Encode()))
	request.SetBasicAuth("local", token)
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 303 {
		t.Fatalf("review got %d %s", w.Code, w.Body.String())
	}
	rows, err := m.Signals()
	if err != nil || len(rows) != 1 || rows[0].Status != "reviewed" {
		t.Fatal("review not persisted", err)
	}
	// Monitoring is authenticated and never returns delivery capabilities.
	routeToken := m.Ingress().Routes[0].Token
	monitorRequest := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/monitor", nil)
	monitorRequest.SetBasicAuth("local", token)
	monitorResponse := httptest.NewRecorder()
	h.ServeHTTP(monitorResponse, monitorRequest)
	if monitorResponse.Code != 200 || strings.Contains(monitorResponse.Body.String(), routeToken) || strings.Contains(monitorResponse.Body.String(), "secret-key") {
		t.Fatal("monitor endpoint leaked credentials or failed")
	}
	anonymous := httptest.NewRecorder()
	h.ServeHTTP(anonymous, httptest.NewRequest("GET", "http://127.0.0.1:8080/api/monitor", nil))
	if anonymous.Code != 401 {
		t.Fatal("monitor is public")
	}
	// Incoming webhook rows render unassigned until a separate local POST imports them.
	remote, err := storage.Open(filepath.Join(dir, "state", "remote-alerts.db"))
	if err != nil {
		t.Fatal(err)
	}
	alert.ID = "incoming-web-001"
	if err = remote.Insert(storage.Record{Signal: alert}); err != nil {
		t.Fatal(err)
	}
	if err = remote.Close(); err != nil {
		t.Fatal(err)
	}
	w = pageGet("/deliveries")
	if !strings.Contains(w.Body.String(), "incoming-web-001") || !strings.Contains(w.Body.String(), "Import for local review") || !strings.Contains(w.Body.String(), "</html>") {
		t.Fatal("unassigned delivery did not render")
	}
	assignment := url.Values{"csrf": {match[1]}, "id": {"incoming-web-001"}, "profile_id": {"mnq-test"}, "contract_id": {"mnq-contract"}}
	request = httptest.NewRequest("POST", "http://127.0.0.1:8080/delivery-import", strings.NewReader(assignment.Encode()))
	request.SetBasicAuth("local", token)
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 303 {
		t.Fatalf("delivery assignment: %d %s", w.Code, w.Body.String())
	}
	for _, route := range []string{"/automation", "/automation-delete", "/account-permission", "/refresh", "/signal-import", "/signal-review", "/delivery-import", "/delivery-reject", "/ingress", "/mapping", "/admin-access"} {
		request = httptest.NewRequest("POST", "http://127.0.0.1:8080"+route, strings.NewReader("csrf=invalid"))
		request.SetBasicAuth("local", token)
		request.Header.Set("Origin", "http://127.0.0.1:8080")
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != 403 {
			t.Fatalf("route %s missing CSRF check: %d", route, w.Code)
		}
	}
	// Rotating the password invalidates old forms even with current authentication.
	rotated := strings.Repeat("z", 64)
	actor := m.Access().List()[0]
	if err := m.ChangeAccess(actor.ID, token, "update", actor.ID, "local", rotated); err != nil {
		t.Fatal(err)
	}
	rotatedHandler := AdminHandler(s, rotated, "127.0.0.1:8080", m)
	oldForm := url.Values{"csrf": {match[1]}}
	rotationRequest := httptest.NewRequest("POST", "http://127.0.0.1:8080/refresh", strings.NewReader(oldForm.Encode()))
	rotationRequest.SetBasicAuth("local", rotated)
	rotationRequest.Header.Set("Origin", "http://127.0.0.1:8080")
	rotationRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rotationResponse := httptest.NewRecorder()
	rotatedHandler.ServeHTTP(rotationResponse, rotationRequest)
	if rotationResponse.Code != 403 || !strings.Contains(rotationResponse.Body.String(), "Nothing was saved") {
		t.Fatal("old form accepted after password rotation or missing recovery UI")
	}
	// Oversized forms must fail before any configuration change.
	values.Set("csrf", match[1])
	values.Set("api_key", strings.Repeat("k", 9000))
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/settings", strings.NewReader(values.Encode()))
	r.SetBasicAuth("local", rotated)
	r.Header.Set("Origin", "http://127.0.0.1:8080")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("oversized form accepted", w.Code)
	}
}

type inboxTestReader struct{}

func (inboxTestReader) Accounts(context.Context) ([]projectx.Account, error) {
	return []projectx.Account{{ID: 7, CanTrade: true}}, nil
}
func (inboxTestReader) AvailableContracts(context.Context) ([]projectx.Contract, error) {
	return []projectx.Contract{{ID: "mnq-contract", SymbolID: "F.US.MNQ", TickSize: .25, TickValue: .5, ActiveContract: true}}, nil
}
