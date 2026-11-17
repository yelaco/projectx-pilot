package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/yelaco/projectx-pilot/internal/config"
)

func TestConfigurePreservesConfigAndRequiresExecutionLease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "mode: read-only\nrefresh_interval: 2m\naccount_id: 42\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	scope := config.RemoteDashboard{Origin: "https://pilot.example.test", TeamDomain: "https://unit.cloudflareaccess.com", Audience: strings.Repeat("a", 64), Emails: []string{"owner@example.test"}}
	raw, _ := json.Marshal(scope)
	lock, err := os.OpenFile(filepath.Join(dir, "execution.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err = configure(path, dir, strings.NewReader(string(raw))); err == nil {
		t.Fatal("wrote config while engine held lease")
	}
	contents, _ := os.ReadFile(path)
	if string(contents) != original {
		t.Fatal("failed configuration changed file")
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err = configure(path, dir, strings.NewReader(string(raw))); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != 42 || got.Mode != "read-only" || got.RefreshInterval.String() != "2m0s" || got.RemoteDashboard.Origin != scope.Origin {
		t.Fatalf("config not preserved: %+v", got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("config permissions changed")
	}
	for _, input := range []string{`{}`, `{"Origin":"https://pilot.example.test","unknown":true}`, strings.Repeat("a", 16385)} {
		if err := configure(path, dir, strings.NewReader(input)); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
}
