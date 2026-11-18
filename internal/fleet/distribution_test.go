package fleet

import (
	"context"
	"errors"
	"testing"
)

func TestDistributionUsesOneReadyAccountAndNeverQueuesNoReadySignals(t *testing.T) {
	f, store, g, ctx := setup(t)
	p := plan("distributed", 1, 2)
	p.Policy.MultiAccountMode = "round_robin"
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 2) })
	for _, delivery := range []string{"first", "second"} {
		if err := f.Dispatch(record(f, p, delivery), p); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool {
			g.mu.Lock()
			defer g.mu.Unlock()
			if delivery == "first" {
				return g.placed[1] == 1
			}
			return g.placed[2] == 1
		})
	}
	if err := f.Dispatch(record(f, p, "none-ready"), p); err != nil {
		t.Fatal(err)
	}
	if err := f.Dispatch(record(f, p, "first"), p); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.TargetJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatal("distribution created more than one outcome per signal", len(jobs))
	}
	found := false
	for _, j := range jobs {
		if j.DeliveryID == "none-ready" {
			found = true
			if j.AccountID != 0 || j.State != "blocked" {
				t.Fatal("unavailable signal was queued", j)
			}
		}
	}
	if !found {
		t.Fatal("missing no-ready outcome")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.placed[1] != 1 || g.placed[2] != 1 {
		t.Fatal("signal copied or replayed")
	}
}
func TestDistributionSkipsPausedAccountAndDoesNotFallbackAfterBrokerReadFailure(t *testing.T) {
	for _, paused := range []bool{true, false} {
		t.Run(map[bool]string{true: "skip paused", false: "no fallback after selection"}[paused], func(t *testing.T) {
			f, store, g, ctx := setup(t)
			p := plan("distributed", 1, 2)
			p.Policy.MultiAccountMode = "round_robin"
			if err := f.Start(ctx, p, nil); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return armed(f, 2) })
			want := int64(1)
			if paused {
				f.PauseAccount(1)
				want = 2
			} else {
				g.mu.Lock()
				g.readHook = func(_ context.Context, account int64) error {
					if account == 1 {
						return errors.New("test account read unavailable")
					}
					return nil
				}
				g.mu.Unlock()
			}
			if err := f.Dispatch(record(f, p, "one-signal"), p); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { jobs, _ := store.TargetJobs(); return len(jobs) == 1 && jobs[0].State != "claimed" })
			jobs, err := store.TargetJobs()
			if err != nil || len(jobs) != 1 || jobs[0].AccountID != want {
				t.Fatal("wrong selected target", jobs, err)
			}
			if !paused && jobs[0].State != "blocked" {
				t.Fatal("read failure not blocked", jobs[0])
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			if paused && (g.placed[1] != 0 || g.placed[2] != 1) {
				t.Fatal("paused account was selected")
			}
			if !paused && (g.placed[1] != 0 || g.placed[2] != 0) {
				t.Fatal("read failure fell back or placed")
			}
		})
	}
}
