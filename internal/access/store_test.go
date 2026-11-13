package access

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialLifecycleAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admins.json")
	initial := strings.Repeat("b", 64)
	s, err := Open(path, initial)
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := s.Verify("local", initial)
	if !ok {
		t.Fatal("bootstrap login failed")
	}
	if err = s.Change(owner.ID, "wrong", "add", "", "second", "second-long-password"); err == nil {
		t.Fatal("missing current password check")
	}
	if err = s.Change(owner.ID, initial, "add", "", "second", "second-long-password"); err != nil {
		t.Fatal(err)
	}
	second, ok := s.Verify("SECOND", "second-long-password")
	if !ok {
		t.Fatal("added login failed")
	}
	if err = s.Change(owner.ID, initial, "update", owner.ID, "owner", "replacement-password"); err != nil {
		t.Fatal(err)
	}
	if _, ok = s.Verify("local", initial); ok {
		t.Fatal("old cached login remained valid")
	}
	updated, ok := s.Verify("owner", "replacement-password")
	if !ok || updated.Secret == owner.Secret {
		t.Fatal("login update failed or CSRF not rotated")
	}
	if err = s.Change(updated.ID, "replacement-password", "delete", second.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok = s.Verify("second", "second-long-password"); ok {
		t.Fatal("deleted login still works")
	}
	if err = s.Change(updated.ID, "replacement-password", "delete", updated.ID, "", ""); err == nil {
		t.Fatal("last administrator deleted")
	}
	reloaded, err := Open(path, initial)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok = reloaded.Verify("owner", "replacement-password"); !ok {
		t.Fatal("restart lost credentials")
	}
	if _, ok = reloaded.Verify("local", initial); ok {
		t.Fatal("bootstrap restored on restart")
	}
	data, _ := os.ReadFile(path)
	for _, secret := range []string{initial, "replacement-password", "second-long-password"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("plaintext password persisted")
		}
	}
	if reloaded.BootstrapActive() {
		t.Fatal("bootstrap still active")
	}
}
func TestInvalidCredentialChanges(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "admins.json"), strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	owner := s.List()[0]
	for _, tc := range []struct{ name, password string }{{"local", "long-password-new"}, {"bad:name", "long-password-new"}, {"valid", "short"}, {"valid", strings.Repeat("a", 73)}} {
		if err = s.Change(owner.ID, strings.Repeat("a", 64), "add", "", tc.name, tc.password); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
}
