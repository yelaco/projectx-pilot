package fleet

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/projectx"
)

func TestRunningProfileRecoversTechnicalFaultWithoutReplayingBlockedSignal(t *testing.T) {
	f, store, g, ctx := setup(t)
	var elapsed atomic.Int64
	initialClock := f.now
	f.now = func() time.Time { return initialClock().Add(time.Duration(elapsed.Load())) }
	g.now = f.now
	f.factory = func(account int64) (execution.API, execution.Quotes, error) {
		return accountAPI{g, account}, quotes{f.now}, nil
	}
	p := plan("recovery", 1)
	if err := f.Start(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return armed(f, 1) })
	f.mu.Lock()
	r := f.accounts[1]
	f.mu.Unlock()
	r.engine.SetMonitorInterval(func() time.Duration { return time.Hour })
	if err := f.Dispatch(record(f, p, "entry"), p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.placed[1] == 1 })
	g.mu.Lock()
	g.history[1][0].FillVolume = g.history[1][0].Size
	g.history[1][0].Status = 2
	quantity := g.history[1][0].Size
	g.positions[1] = []projectx.Position{{ID: 99, AccountID: 1, ContractID: "mnq", Type: 1, Size: quantity, AveragePrice: 20000}}
	g.orders[1] = nil
	g.mu.Unlock()
	r.engine.Wake()
	waitFor(t, func() bool { return r.engine.Status().RecoveryPending })
	f.arm(r)
	f.mu.Lock()
	requested := r.requested
	f.mu.Unlock()
	if !requested {
		t.Fatal("controller discarded original Start")
	}
	if err := f.Dispatch(record(f, p, "during-fault"), p); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	g.positions[1] = nil
	profit := 0.0
	g.trades[1] = []projectx.Trade{{ID: 777, ContractID: "mnq", Size: quantity, ProfitAndLoss: &profit}}
	g.mu.Unlock()
	r.engine.Wake()
	waitFor(t, func() bool { s := r.engine.Status(); return len(s.Intents) > 0 && !s.Intents[0].FlatSince.IsZero() })
	elapsed.Add(int64(4 * time.Second))
	r.engine.Wake()
	waitFor(t, func() bool { s := r.engine.Status(); return len(s.Intents) > 0 && s.Intents[0].State == "closed" })
	elapsed.Add(int64(20 * time.Second))
	f.arm(r)
	if r.engine.Status().Armed {
		t.Fatal("only one recovery observation armed account")
	}
	elapsed.Add(int64(20 * time.Second))
	f.arm(r)
	if !r.engine.Status().Armed || !r.engine.Status().RunRequested {
		t.Fatal("profile did not automatically resume", r.engine.Status().RunMessage)
	}
	g.mu.Lock()
	placed, closed := g.placed[1], g.closed[1]
	g.mu.Unlock()
	if placed != 1 || closed != 0 {
		t.Fatal("recovery replayed or closed an order")
	}
	jobs, err := store.TargetJobs()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, j := range jobs {
		if j.DeliveryID == "during-fault" {
			found = true
			if j.State != "blocked" {
				t.Fatal("fault-time alert replayed", j.State)
			}
		}
	}
	if !found {
		t.Fatal("blocked alert lacked durable outcome")
	}
	f.PauseProfile(p.ProfileID)
	elapsed.Add(int64(20 * time.Second))
	f.arm(r)
	if r.engine.Status().Armed || r.engine.Status().RecoveryPending || r.engine.Status().RunRequested {
		t.Fatal("manual Pause was undone")
	}
}
