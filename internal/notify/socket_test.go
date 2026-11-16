package notify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateWakeAndShutdown(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "inbox.sock")
	l, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("socket not private")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	woke := make(chan struct{}, 1)
	go func() {
		defer close(done)
		l.Run(ctx, func() {
			select {
			case woke <- struct{}{}:
			default:
			}
		})
	}()
	Ping(path)
	select {
	case <-woke:
	case <-time.After(time.Second):
		t.Fatal("worker not notified")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("socket did not shut down")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("socket remained after stop")
	}
	// A receiver failure must not block durable HTTP acceptance.
	start := time.Now()
	Ping(path)
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("notification fallback blocked receiver")
	}
}
func TestRefusesNonSocketPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "inbox.sock")
	if err := os.WriteFile(p, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(p); err == nil {
		t.Fatal("overwrote a file")
	}
}
