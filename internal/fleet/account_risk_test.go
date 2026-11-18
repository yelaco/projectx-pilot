package fleet

import "testing"

func TestAccountSpecificRiskSizesCopyAndDistributedSignals(t *testing.T) {
	for _, mode := range []string{"copy", "round_robin"} {
		t.Run(mode, func(t *testing.T) {
			f, store, g, ctx := setup(t)
			p := plan("account-risk", 1, 2)
			p.Policy.MultiAccountMode = mode
			p.Policy.AccountRiskOverrides = map[int64]float64{1: 36, 2: 72}
			if err := f.Start(ctx, p, nil); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return armed(f, 2) })
			if err := f.Dispatch(record(f, p, "first-risk"), p); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.placed[1] == 1 })
			if mode == "round_robin" {
				if err := f.Dispatch(record(f, p, "second-risk"), p); err != nil {
					t.Fatal(err)
				}
			}
			waitFor(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.placed[2] == 1 })
			g.mu.Lock()
			first, second := g.requests[1].Size, g.requests[2].Size
			g.mu.Unlock()
			// Each contract costs $24 planned risk in this fixture. Overrides replace
			// the manager's $150 default, giving floor(36/24)=1 and floor(72/24)=3.
			if first != 1 || second != 3 {
				t.Fatalf("wrong account sizing: %d, %d", first, second)
			}
			if p.Policy.AccountRiskOverrides[1] != 36 || p.Policy.AccountRiskOverrides[2] != 72 || len(p.Policy.AccountIDs) != 2 {
				t.Fatal("scoping mutated manager policy")
			}
			views, _ := f.Snapshot()
			for _, view := range views {
				for _, i := range view.Status.Intents {
					if i.Policy.MaxRiskPerTrade != p.Policy.MaxRiskForAccount(view.AccountID) {
						t.Fatal("intent did not capture account-specific risk")
					}
				}
			}
			jobs, err := store.TargetJobs()
			if err != nil {
				t.Fatal(err)
			}
			if len(jobs) != 2 {
				t.Fatal("wrong target allocation", jobs)
			}
		})
	}
}

func TestAccountRiskBelowOneContractBlocksWithoutFallbackOrUndo(t *testing.T) {
	for _, mode := range []string{"copy", "round_robin"} {
		t.Run(mode, func(t *testing.T) {
			f, store, g, ctx := setup(t)
			p := plan("small-budget", 1, 2)
			p.Policy.MultiAccountMode = mode
			p.Policy.AccountRiskOverrides = map[int64]float64{1: 10, 2: 72}
			if err := f.Start(ctx, p, nil); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return armed(f, 2) })
			if err := f.Dispatch(record(f, p, "small-budget-signal"), p); err != nil {
				t.Fatal(err)
			}
			count := 1
			if mode == "copy" {
				count = 2
			}
			waitFor(t, func() bool {
				jobs, _ := store.TargetJobs()
				if len(jobs) != count {
					return false
				}
				for _, j := range jobs {
					if j.State == "claimed" {
						return false
					}
				}
				return true
			})
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.placed[1] != 0 || g.closed[1] != 0 || g.closed[2] != 0 {
				t.Fatal("small override caused an oversized entry or undo")
			}
			if mode == "copy" && g.placed[2] != 1 {
				t.Fatal("blocked copy target affected independent accepted target")
			}
			if mode == "round_robin" && g.placed[2] != 0 {
				t.Fatal("risk rejection reassigned the selected signal")
			}
		})
	}
}
