package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadText(t *testing.T, text string) (Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}
func TestDefaults(t *testing.T) {
	c, err := loadText(t, "{}\n")
	if err != nil || c.NewsFilter != "us_fomc" || c.Mode != "read-only" || c.RequestTimeout != 10*time.Second {
		t.Fatalf("%+v %v", c, err)
	}
}
func TestUnsafeConfig(t *testing.T) {
	for _, s := range []string{"news_filter: unsupported", "mode: invalid", "mode: paper", "dashboard_listen: 0.0.0.0:8080", "dashboard_listen: localhost:8080", "dashboard_listen: 127.0.0.1:0", "request_timeout: 0s", "refresh_interval: 1s", "account_id: -1", "log_level: verbose", "unknown: true", "{}\n---\n{}", "mode: ["} {
		t.Run(s, func(t *testing.T) {
			if _, err := loadText(t, s); err == nil {
				t.Fatal("accepted unsafe config")
			}
		})
	}
}
func TestSecrets(t *testing.T) {
	t.Setenv("PROJECTX_USERNAME", "u")
	t.Setenv("PROJECTX_API_KEY", "k")
	t.Setenv("BOT_DASHBOARD_TOKEN", "short")
	if _, err := ReadSecrets(); err == nil {
		t.Fatal("weak token accepted")
	}
	t.Setenv("BOT_DASHBOARD_TOKEN", "01234567890123456789012345678901")
	if _, err := ReadSecrets(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidOperationPlans(t *testing.T) {
	for _, text := range []string{
		"no_trade_dates: [2026-02-30]",
		"no_trade_dates: ['2026-12-07', '2026-12-07']",
		"trade_manager_no_trade_dates: {INVALID: ['2026-12-07']}",
		"trade_manager_no_trade_dates: {first: [bad]}",
	} {
		if _, err := loadText(t, text); err == nil {
			t.Fatal("invalid plan accepted", text)
		}
	}
}

func TestRetiredGlobalInstrumentsAreIgnoredAndRemovedOnSave(t *testing.T) {
	for _, legacy := range []string{"[]", "[MNQ]", "[MNQ, MNQ]"} {
		t.Run(legacy, func(t *testing.T) {
			c, err := loadText(t, "allowed_instruments: "+legacy+"\n")
			if err != nil || len(c.AllowedInstruments) != 0 {
				t.Fatal("old global scope still required/enforced", c, err)
			}
			path := filepath.Join(t.TempDir(), "saved.yaml")
			c.AllowedInstruments = []string{"MNQ"} // Direct old callers are retired too.
			if err := Save(path, c); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(b), "allowed_instruments") {
				t.Fatal("retired setting persisted", err)
			}
		})
	}
}

func TestRemoteDashboardRequiresCompleteExactAccessScope(t *testing.T) {
	valid := RemoteDashboard{Origin: "https://pilot.example.test", TeamDomain: "https://unit.cloudflareaccess.com", Audience: strings.Repeat("a", 64), Emails: []string{"owner@example.test"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*RemoteDashboard){
		func(c *RemoteDashboard) { c.Origin = "http://pilot.example.test" },
		func(c *RemoteDashboard) { c.Origin = "https://pilot.example.test/" },
		func(c *RemoteDashboard) { c.Origin = "https://pilot.example.test:443" },
		func(c *RemoteDashboard) { c.TeamDomain = "https://evil.example" },
		func(c *RemoteDashboard) { c.Audience = "" },
		func(c *RemoteDashboard) { c.Emails = nil },
		func(c *RemoteDashboard) { c.Emails = []string{"*@example.test"} },
		func(c *RemoteDashboard) { c.Origin = "" },
	} {
		c := valid
		change(&c)
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted unsafe remote configuration: %+v", c)
		}
	}
	if err := (RemoteDashboard{}).Validate(); err != nil {
		t.Fatal(err)
	}
}
