package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"projectxbot/internal/config"
)

func setup(t *testing.T) (*Manager, string, string, string) {
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
	state := filepath.Join(dir, "state")
	m, token, err := Open(c, p, state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m, token, p, state
}
func TestOnboardingAndRestart(t *testing.T) {
	m, token, p, state := setup(t)
	if len(token) != 64 || m.Settings().HasCredentials {
		t.Fatal("invalid initial setup")
	}
	if api, err := m.Reader(); err != nil || api != nil {
		t.Fatal("unconfigured reader")
	}
	if err := m.Save(Update{AccountID: 42, Instruments: []string{"MNQ", "MGC"}, RefreshSeconds: 90, Username: "trader", APIKey: "secret"}); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(p)
	if err != nil || c.AccountID != 42 {
		t.Fatal(c, err)
	}
	for _, file := range []string{p, filepath.Join(state, "credentials.json"), filepath.Join(state, "admin-password")} {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("permissions %s %v", file, err)
		}
	}
	next, nextToken, err := Open(c, p, state)
	if err != nil || nextToken != token || !next.Settings().HasCredentials || next.Settings().Username != "trader" {
		t.Fatal("restart lost settings", err)
	}
	if err = next.Save(Update{AccountID: 42, Instruments: []string{"MNQ"}, RefreshSeconds: 60, Username: "trader"}); err != nil {
		t.Fatal(err)
	}
	if next.credentials.APIKey != "secret" {
		t.Fatal("blank key overwrote existing secret")
	}
	if err = next.Save(Update{Instruments: []string{"MNQ"}, RefreshSeconds: 60, Username: "trader", ClearCredentials: true}); err != nil {
		t.Fatal(err)
	}
	if next.Settings().HasCredentials {
		t.Fatal("credentials not removed")
	}
}
func TestValidationLeavesConfigUnchanged(t *testing.T) {
	m, _, p, _ := setup(t)
	before, _ := os.ReadFile(p)
	for _, u := range []Update{{RefreshSeconds: 1, Instruments: []string{"MNQ"}}, {RefreshSeconds: 60, Instruments: []string{"MNQ"}, AccountID: -1}, {RefreshSeconds: 60, Instruments: []string{"MNQ"}, Username: "x"}} {
		if m.Save(u) == nil {
			t.Fatal("invalid settings accepted")
		}
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("invalid settings persisted")
	}
}
func TestEnvironmentCredentialsCannotBeChanged(t *testing.T) {
	m, _, p, state := setup(t)
	t.Setenv("PROJECTX_USERNAME", "env-user")
	t.Setenv("PROJECTX_API_KEY", "env-secret")
	c := m.Settings().Config
	m, _, err := Open(c, p, state)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Settings().EnvironmentCredentials {
		t.Fatal("environment not recognized")
	}
	err = m.Save(Update{RefreshSeconds: 60, Instruments: []string{"MNQ"}, Username: "env-user", APIKey: "replacement"})
	if err == nil || strings.Contains(err.Error(), "env-secret") {
		t.Fatal("environment mutation allowed or secret leaked")
	}
	if err = m.Save(Update{RefreshSeconds: 60, Instruments: []string{"MNQ"}, Username: "env-user"}); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionProcessLease(t *testing.T) {
	m, _, path, state := setup(t)
	if err := m.AcquireExecutionLease(); err != nil {
		t.Fatal(err)
	}
	next, _, err := Open(m.Settings().Config, path, state)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err = next.AcquireExecutionLease(); err == nil {
		t.Fatal("duplicate process owns execution state")
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	if err = next.AcquireExecutionLease(); err != nil {
		t.Fatal("lease not released", err)
	}
}
