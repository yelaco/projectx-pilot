package dashboard

import (
	"net/url"
	"strings"
	"testing"
)

func TestDiscordSettingsRequireLocalCSRFAndPasswordAndNeverRenderSecret(t *testing.T) {
	m, password, h := managementFixture(t)
	csrf := formCSRF(t, requestAs(h, "local", password, "GET", "/settings", nil))
	fields := url.Values{"csrf": {csrf}, "enabled": {"yes"}, "discord_webhook": {"https://discord.com/api/webhooks/123/offline-test-token"}, "current_password": {"wrong"}}
	if w := requestAs(h, "local", password, "POST", "/attention-settings", fields); w.Code != 400 || !strings.Contains(w.Body.String(), "current administrator password required") {
		t.Fatal("reauth bypass", w.Code)
	}
	fields.Set("current_password", password)
	fields.Set("csrf", "invalid")
	if w := requestAs(h, "local", password, "POST", "/attention-settings", fields); w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code)
	}
	fields.Set("csrf", csrf)
	if w := requestAs(h, "local", password, "POST", "/attention-settings", fields); w.Code != 303 {
		t.Fatal("save failed", w.Code, w.Body.String())
	}
	if !m.AttentionStatus().Enabled {
		t.Fatal("notifications not enabled")
	}
	page := requestAs(h, "local", password, "GET", "/settings", nil)
	if strings.Contains(page.Body.String(), "offline-test-token") {
		t.Fatal("webhook credential rendered")
	}
	if !strings.Contains(page.Body.String(), "Webhook saved") {
		t.Fatal("configured status missing")
	}
}

func TestDailyReportSettingsAreSeparateAndRequireReauthentication(t *testing.T) {
	m, password, h := managementFixture(t)
	csrf := formCSRF(t, requestAs(h, "local", password, "GET", "/settings", nil))
	fields := url.Values{"csrf": {csrf}, "enabled": {"yes"}, "discord_webhook": {"https://discord.com/api/webhooks/123/offline-daily-token"}, "current_password": {"wrong"}}
	if w := requestAs(h, "local", password, "POST", "/trading-report-settings", fields); w.Code != 400 {
		t.Fatal("daily setting bypassed reauth", w.Code)
	}
	fields.Set("current_password", password)
	if w := requestAs(h, "local", password, "POST", "/trading-report-settings", fields); w.Code != 303 {
		t.Fatal("daily report save failed", w.Code, w.Body.String())
	}
	if !m.TradingReportStatus().Enabled || m.AttentionStatus().Enabled || m.AttentionStatus().Configured {
		t.Fatal("daily configuration changed system reports")
	}
	page := requestAs(h, "local", password, "GET", "/settings", nil)
	if strings.Contains(page.Body.String(), "offline-daily-token") || !strings.Contains(page.Body.String(), "4:15 PM America/Chicago") {
		t.Fatal("daily webhook leaked or schedule missing")
	}
}
