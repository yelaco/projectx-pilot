package fleet

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/projectx"
)

func rolloverSetup(t *testing.T) (*Fleet, *gateway, Plan, *runner, *atomic.Int64) {
	t.Helper()
	f, _, g, ctx := setup(t)
	var offset atomic.Int64
	clock := f.now
	f.now = func() time.Time { return clock().Add(time.Duration(offset.Load())) }
	g.now = f.now
	f.factory = func(account int64) (execution.API, execution.Quotes, error) {
		return accountAPI{g, account}, quotes{f.now}, nil
	}
	old := projectx.Contract{ID: "old", SymbolID: "F.US.MNQ", ActiveContract: true, TickSize: .25, TickValue: .5}
	next := old
	next.ID = "next"
	g.contracts = map[string]projectx.Contract{"old": old, "next": next}
	p := plan("rollover", 1)
	p.ContractID = "old"
	p.ContractMode = "active"
	p.ContractSymbolID = "F.US.MNQ"
	f.UpdateContractDiscovery([]projectx.Contract{old}, f.now(), true, time.Minute)
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 1) })
	f.mu.Lock()
	r := f.accounts[1]
	f.mu.Unlock()
	r.engine.SetMonitorInterval(func() time.Duration { return time.Hour })
	return f, g, p, r, &offset
}
func TestAutomaticRolloverRetainsStartAndCapturedOpenPosition(t *testing.T) {
	f, g, p, r, offset := rolloverSetup(t)
	if err := f.Dispatch(record(f, p, "old-expiry"), p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.placed[1] == 1 })
	g.mu.Lock()
	parent := &g.history[1][0]
	parent.FillVolume = parent.Size
	parent.Status = 2
	quantity, id := parent.Size, parent.ID
	g.positions[1] = []projectx.Position{{ID: 500, AccountID: 1, ContractID: "old", Type: 1, Size: quantity, AveragePrice: 20000}}
	stopID, targetID := id+1, id+2
	stop, target := 19990.0, 20020.0
	g.orders[1] = []projectx.Order{{ID: stopID, ContractID: "old", ParentOrderID: &id, LinkedOrderID: &targetID, Side: 1, Type: 4, Status: 1, Size: quantity, StopPrice: &stop}, {ID: targetID, ContractID: "old", ParentOrderID: &id, LinkedOrderID: &stopID, Side: 1, Type: 1, Status: 1, Size: quantity, LimitPrice: &target}}
	next := g.contracts["next"]
	g.mu.Unlock()
	r.engine.Wake()
	waitFor(t, func() bool { s := r.engine.Status(); return len(s.Intents) > 0 && s.Intents[0].State == "protected" })
	f.UpdateContractDiscovery([]projectx.Contract{next}, f.now(), true, time.Minute)
	f.arm(r)
	if s := r.engine.Status(); s.AutomationContractID != "old" || !s.RunRequested {
		t.Fatal("rollover changed an occupied account", s)
	}
	g.mu.Lock()
	if len(g.orders[1]) != 2 || g.closed[1] != 0 {
		t.Fatal("rollover mutated protection")
	}
	g.positions[1] = nil
	g.orders[1] = nil
	profit := 0.0
	g.trades[1] = []projectx.Trade{{ID: 900, ContractID: "old", Size: quantity, ProfitAndLoss: &profit}}
	g.mu.Unlock()
	r.engine.Wake()
	waitFor(t, func() bool { s := r.engine.Status(); return !s.Intents[0].FlatSince.IsZero() })
	offset.Add(int64(4 * time.Second))
	r.engine.Wake()
	waitFor(t, func() bool { s := r.engine.Status(); return s.Intents[0].State == "closed" })
	offset.Add(int64(20 * time.Second))
	f.arm(r)
	s := r.engine.Status()
	if !s.Armed || !s.RunRequested || s.AutomationContractID != "next" {
		t.Fatal("rollover required another Start", s.RunMessage, s.AutomationContractID)
	}
	if err := f.Dispatch(record(f, p, "new-expiry"), p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.placed[1] == 2 })
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.requests[1].ContractID != "next" || g.history[1][0].ContractID != "old" || g.closed[1] != 0 {
		t.Fatal("rollover lost execution identity or changed exposure")
	}
}
func TestAutomaticRolloverWaitsForUnambiguousFreshDiscoveryWithoutLosingStart(t *testing.T) {
	f, g, p, r, offset := rolloverSetup(t)
	g.mu.Lock()
	next := g.contracts["next"]
	other := next
	other.ID = "ambiguous"
	g.mu.Unlock()
	for _, snapshot := range [][]projectx.Contract{{next, other}, {{ID: "wrong", SymbolID: "F.US.MGC", ActiveContract: true, TickSize: .1, TickValue: 1}}} {
		f.UpdateContractDiscovery(snapshot, f.now(), true, time.Minute)
		f.arm(r)
		if s := r.engine.Status(); s.Armed || !s.RunRequested {
			t.Fatal("ambiguous discovery did not safely retain Start")
		}
	}
	f.UpdateContractDiscovery([]projectx.Contract{next}, f.now().Add(-2*time.Minute), true, time.Minute)
	f.arm(r)
	if r.engine.Status().Armed {
		t.Fatal("stale discovery armed a replacement")
	}
	offset.Add(int64(20 * time.Second))
	f.UpdateContractDiscovery([]projectx.Contract{next}, f.now(), true, time.Minute)
	f.arm(r)
	if s := r.engine.Status(); !s.Armed || !s.RunRequested || s.AutomationContractID != "next" {
		t.Fatal("fresh discovery did not recover without Start", s)
	}
	f.PauseProfile(p.ProfileID)
	offset.Add(int64(20 * time.Second))
	f.UpdateContractDiscovery([]projectx.Contract{next}, f.now(), true, time.Minute)
	f.arm(r)
	if r.engine.Status().Armed || r.engine.Status().RunRequested {
		t.Fatal("rollover undid manual Pause")
	}
}

func TestAutomaticRolloverFreezesOneExpiryBeforeCopyFanout(t *testing.T) {
	f, store, g, ctx := setup(t)
	old := projectx.Contract{ID: "old", SymbolID: "F.US.MNQ", TickSize: .25, TickValue: .5}
	next := old
	next.ID = "next"
	next.ActiveContract = true
	g.contracts = map[string]projectx.Contract{"old": old, "next": next}
	p := plan("copy-roll", 1, 2)
	p.ContractID = "old"
	p.ContractMode = "active"
	p.ContractSymbolID = "F.US.MNQ"
	f.UpdateContractDiscovery([]projectx.Contract{next}, f.now(), true, time.Minute)
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 2) })
	if err := f.Dispatch(record(f, p, "same-expiry"), p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.placed[1] == 1 && g.placed[2] == 1 })
	jobs, err := store.TargetJobs()
	if err != nil || len(jobs) != 2 {
		t.Fatal(jobs, err)
	}
	for _, job := range jobs {
		if job.ContractID != "next" {
			t.Fatal("copy target did not freeze resolved expiry", job)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.requests[1].ContractID != "next" || g.requests[2].ContractID != "next" || g.closed[1] != 0 || g.closed[2] != 0 {
		t.Fatal("copy target selected a different expiry or rolled exposure")
	}
}
