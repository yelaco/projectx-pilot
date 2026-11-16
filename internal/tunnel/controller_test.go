package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"github.com/yelaco/projectx-pilot/internal/ingress"
	"github.com/yelaco/projectx-pilot/internal/storage"
	"strings"
	"testing"
	"time"
)

func TestOwnProcessIsStoppedAndURLCleared(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "cloudflared")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'https://unit-test.trycloudflare.com\\n'\nexec /bin/sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	s, err := ingress.Open(filepath.Join(dir, "ingress.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Update(func(s *ingress.Settings) error { s.Enabled = true; s.QuickTunnel = true; return nil })
	audit, err := storage.Open(filepath.Join(dir, "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	controller := New(s, audit)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); controller.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(2 * time.Second)
	for s.Snapshot().PublicURL == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Snapshot().PublicURL != "https://unit-test.trycloudflare.com" {
		t.Fatal("URL not detected", controller.Status())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("owned tunnel did not stop")
	}
	if s.Snapshot().PublicURL != "" {
		t.Fatal("stale public URL retained")
	}
}

func TestDiagnosticsNeverCopyRawProcessDetails(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"ERR lookup private-SECRET-TOKEN: no such host", "DNS"},
		{"ERR connection refused TOKEN=SECRET", "local receiver"},
		{"WRN timeout private-SECRET-TOKEN", "timed out"},
		{"ERR something unknown TOKEN=SECRET", "reported an error"},
	} {
		got := diagnostic(tc.line)
		if got == "" || !strings.Contains(got, tc.want) || strings.Contains(got, "SECRET") {
			t.Fatal("unsafe or missing diagnostic", got)
		}
	}
	if diagnostic("INF normal line SECRET") != "" {
		t.Fatal("raw process line copied")
	}
}
