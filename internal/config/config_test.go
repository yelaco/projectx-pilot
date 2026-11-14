package config

import (
	"os"
	"path/filepath"
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
	for _, s := range []string{"news_filter: unsupported", "mode: invalid", "mode: paper", "dashboard_listen: 0.0.0.0:8080", "dashboard_listen: localhost:8080", "dashboard_listen: 127.0.0.1:0", "request_timeout: 0s", "refresh_interval: 1s", "account_id: -1", "log_level: verbose", "unknown: true", "allowed_instruments: []", "allowed_instruments: [MNQ, MNQ]", "{}\n---\n{}", "mode: ["} {
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
