package dashboard

import (
	"net/url"
	"strings"
	"testing"
)

func TestPublicSettingsRequireCSRFPasswordAndStaySeparate(t *testing.T) {
	m, password, h := managementFixture(t)
	csrf := formCSRF(t, requestAs(h, "local", password, "GET", "/settings", nil))
	fields := url.Values{"csrf": {csrf}, "current_password": {"wrong"}}
	if w := requestAs(h, "local", password, "POST", "/public-status-settings", fields); w.Code != 400 || !strings.Contains(w.Body.String(), "current administrator password required") {
		t.Fatal("publication password bypass", w.Code)
	}
	fields.Set("current_password", password)
	fields.Set("csrf", "bad")
	if w := requestAs(h, "local", password, "POST", "/public-status-settings", fields); w.Code != 403 {
		t.Fatal("publication CSRF bypass", w.Code)
	}
	fields.Set("csrf", csrf)
	if w := requestAs(h, "local", password, "POST", "/public-status-settings", fields); w.Code != 303 {
		t.Fatal("save disabled publication", w.Code, w.Body.String())
	}
	s, _ := m.PublicSettings()
	if s.Enabled || m.ExecutionStatus().Armed {
		t.Fatal("disabled publication enabled authority")
	}
	page := requestAs(h, "local", password, "GET", "/settings", nil)
	if !strings.Contains(page.Body.String(), "Public trading status") {
		t.Fatal("missing settings panel")
	}
}
