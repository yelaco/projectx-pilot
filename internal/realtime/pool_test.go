package realtime

import (
	"context"
	"errors"
	"testing"
)

func TestPoolSharesAndReleasesOnlyLastSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // No network is needed to verify ownership.
	p := NewPool(func(context.Context) (string, error) { return "", errors.New("offline test") })
	a, b := p.Handle(), p.Handle()
	a.Watch(ctx, "mnq")
	b.Watch(ctx, "mnq")
	if a.feed != b.feed || len(p.feeds) != 1 || p.feeds["mnq"].refs != 2 {
		t.Fatal("duplicate quote stream")
	}
	a.Close()
	if p.feeds["mnq"] == nil || p.feeds["mnq"].refs != 1 {
		t.Fatal("closed another subscriber")
	}
	b.Close()
	if len(p.feeds) != 0 {
		t.Fatal("subscription leaked")
	}
	a.Close()
	b.Close()
}
