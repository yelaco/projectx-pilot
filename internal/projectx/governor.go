package projectx

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Governor shares a rolling request budget across the application's clients.
// The documented ceiling is 200/60s. Keep 20 spare slots, with 40 of the 180
// slots unavailable to reads so entry/protective mutations cannot be starved.
type Governor struct {
	mu         sync.Mutex
	all, reads []time.Time
	slots      chan struct{}
}

func NewGovernor() *Governor { return &Governor{slots: make(chan struct{}, 8)} }
func (g *Governor) Acquire(ctx context.Context, path string) (func(), error) {
	write := strings.HasPrefix(path, "/api/Order/") && path != "/api/Order/search" && path != "/api/Order/searchOpen" || strings.HasPrefix(path, "/api/Position/close") || strings.HasPrefix(path, "/api/Position/partial")
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		select {
		case g.slots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		now := time.Now()
		g.mu.Lock()
		prune := func(in []time.Time) []time.Time {
			n := 0
			for n < len(in) && !in[n].After(now.Add(-time.Minute)) {
				n++
			}
			return in[n:]
		}
		g.all = prune(g.all)
		g.reads = prune(g.reads)
		wait := time.Duration(0)
		if len(g.all) >= 180 {
			wait = time.Until(g.all[0].Add(time.Minute))
		}
		if !write && len(g.reads) >= 140 {
			other := time.Until(g.reads[0].Add(time.Minute))
			if other > wait {
				wait = other
			}
		}
		if wait <= 0 {
			g.all = append(g.all, now)
			if !write {
				g.reads = append(g.reads, now)
			}
			g.mu.Unlock()
			return func() { <-g.slots }, nil
		}
		g.mu.Unlock()
		<-g.slots
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
}
func (c *Client) SetGovernor(g *Governor) { c.governor = g }
