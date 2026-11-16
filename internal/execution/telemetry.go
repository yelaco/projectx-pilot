package execution

import (
	"context"
	"time"
)

type Timing struct {
	DeliveryID                                                                           string
	ReceivedAt, DispatchedAt, ChecksStartedAt, ChecksFinishedAt, SubmittedAt, AcceptedAt time.Time
}

func (t Timing) AcceptanceMS() float64 {
	if t.AcceptedAt.IsZero() || t.ReceivedAt.IsZero() {
		return 0
	}
	return float64(t.AcceptedAt.Sub(t.ReceivedAt)) / float64(time.Millisecond)
}

type traceKey struct{}

func WithTiming(ctx context.Context, t *Timing) context.Context {
	return context.WithValue(ctx, traceKey{}, t)
}
func timing(ctx context.Context) *Timing { t, _ := ctx.Value(traceKey{}).(*Timing); return t }

// Observe publishes immutable snapshots without waiting on HTTP work under the
// engine mutex. The callback must be nonblocking and must not call the engine.
func (e *Engine) Observe(f func(Status)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.observer = f
	e.publishLocked()
}
func (e *Engine) publishLocked() {
	if e.observer != nil {
		e.observer(e.snapshotLocked())
	}
}
func (e *Engine) WaitReady(ctx context.Context) error {
	select {
	case <-e.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}
func (e *Engine) SetMonitorInterval(f func() time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.monitorInterval = f
}
