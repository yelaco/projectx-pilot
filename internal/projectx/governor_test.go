package projectx

import (
	"context"
	"testing"
	"time"
)

func TestReadBudgetReservesMutationCapacity(t *testing.T) {
	g := NewGovernor()
	now := time.Now()
	for n := 0; n < 140; n++ {
		g.all = append(g.all, now)
		g.reads = append(g.reads, now)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if release, err := g.Acquire(ctx, "/api/Order/search"); err == nil {
		release()
		t.Fatal("read budget exceeded")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	release, err := g.Acquire(ctx2, "/api/Position/closeContract")
	if err != nil {
		t.Fatal("reads starved emergency mutation", err)
	}
	release()
}
func TestGovernorWindowAndCancellation(t *testing.T) {
	g := NewGovernor()
	for n := 0; n < 180; n++ {
		g.all = append(g.all, time.Now().Add(-61*time.Second))
	}
	release, err := g.Acquire(context.Background(), "/api/Order/place")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(g.all) != 1 {
		t.Fatal("expired requests retained")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Acquire(ctx, "/api/Order/place"); err == nil {
		t.Fatal("ignored cancellation")
	}
}
