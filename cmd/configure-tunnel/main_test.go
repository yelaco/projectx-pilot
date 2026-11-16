package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/yelaco/projectx-pilot/internal/ingress"
)

const mockToken = "OFFLINE_MOCK_TOKEN_NOT_A_REAL_CLOUDFLARE_SECRET"

func TestConfigurePreservesProfileRoutesAndPrivateToken(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	conf := filepath.Join(root, "config")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := ingress.Open(filepath.Join(state, "ingress.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Update(func(s *ingress.Settings) error {
		s.QuickTunnel = true
		s.Routes = []ingress.Route{{ID: "swift", Token: "mock-delivery-token", Strategy: "swift", Entry: "{{close}}"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = configure("alerts.example.com", state, conf, strings.NewReader(mockToken)); err != nil {
		t.Fatal(err)
	}
	got, err := ingress.Load(filepath.Join(state, "ingress.json"))
	if err != nil || !got.Enabled || got.QuickTunnel || got.PublicURL != "https://alerts.example.com" || len(got.Routes) != 1 || got.Routes[0].Token != "mock-delivery-token" || got.Routes[0].Entry != "{{close}}" {
		t.Fatal("routing was not preserved")
	}
	tokenFile := filepath.Join(conf, "cloudflared-token")
	b, err := os.ReadFile(tokenFile)
	if err != nil || strings.TrimSpace(string(b)) != mockToken {
		t.Fatal("token file not saved")
	}
	info, _ := os.Stat(tokenFile)
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe secret permissions")
	}
	if err = configure("alerts.example.com", state, conf, strings.NewReader(mockToken)); err != nil {
		t.Fatal("rerun failed", err)
	}
	if _, err = os.Stat(filepath.Join(state, "execution.db")); !os.IsNotExist(err) {
		t.Fatal("setup touched trading state")
	}
}
func TestConfigureValidationAndLease(t *testing.T) {
	for _, host := range []string{"https://alerts.example.com", "evil.example/path", "a.example\nExecStart=bad", "localhost", "-bad.example"} {
		root := t.TempDir()
		err := configure(host, filepath.Join(root, "state"), filepath.Join(root, "config"), strings.NewReader(mockToken))
		if err == nil {
			t.Fatal("invalid hostname accepted")
		}
		entries, _ := os.ReadDir(root)
		if len(entries) != 0 {
			t.Fatal("invalid input mutated state")
		}
	}
	for _, token := range []string{"", "sudo cloudflared service install " + mockToken, strings.Repeat("a", 16385)} {
		root := t.TempDir()
		err := configure("alerts.example.com", root, root, strings.NewReader(token))
		if err == nil || strings.Contains(err.Error(), token) && token != "" {
			t.Fatal("invalid token accepted or exposed")
		}
	}
	root := t.TempDir()
	lock, err := os.OpenFile(filepath.Join(root, "execution.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err = configure("alerts.example.com", root, root, strings.NewReader(mockToken)); err == nil {
		t.Fatal("overwrote running bot configuration")
	}
}
func TestCheckAndCorruptRoutingNeverWriteToken(t *testing.T) {
	root := t.TempDir()
	if err := configurePlan("alerts.example.com", filepath.Join(root, "state"), filepath.Join(root, "config"), strings.NewReader(mockToken), true); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("preflight mutated state")
	}
	if err := os.WriteFile(filepath.Join(root, "ingress.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := configure("alerts.example.com", root, root, strings.NewReader(mockToken)); err == nil {
		t.Fatal("corrupt routing replaced")
	}
	if _, err := os.Stat(filepath.Join(root, "cloudflared-token")); !os.IsNotExist(err) {
		t.Fatal("token changed despite invalid routing")
	}
}
