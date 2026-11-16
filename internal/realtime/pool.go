package realtime

import (
	"context"
	"errors"
	"sync"
)

type pooled struct {
	feed *Feed
	refs int
}
type Pool struct {
	mu    sync.Mutex
	feeds map[string]*pooled
	token func(context.Context) (string, error)
}

func NewPool(token func(context.Context) (string, error)) *Pool {
	return &Pool{feeds: map[string]*pooled{}, token: token}
}

type Handle struct {
	mu       sync.Mutex
	pool     *Pool
	contract string
	feed     *Feed
}

func (p *Pool) Handle() *Handle { return &Handle{pool: p} }
func (h *Handle) Watch(ctx context.Context, contract string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.contract == contract && h.feed != nil {
		return
	}
	h.release()
	p := h.pool
	p.mu.Lock()
	entry := p.feeds[contract]
	if entry == nil {
		entry = &pooled{feed: New(p.token)}
		p.feeds[contract] = entry
	}
	entry.refs++
	h.contract, h.feed = contract, entry.feed
	p.mu.Unlock()
	h.feed.Watch(ctx, contract)
}
func (h *Handle) release() {
	if h.feed == nil {
		return
	}
	p := h.pool
	p.mu.Lock()
	entry := p.feeds[h.contract]
	entry.refs--
	last := entry.refs == 0
	if last {
		delete(p.feeds, h.contract)
	}
	p.mu.Unlock()
	if last {
		entry.feed.Close()
	}
	h.feed = nil
	h.contract = ""
}
func (h *Handle) Close() { h.mu.Lock(); defer h.mu.Unlock(); h.release() }
func (h *Handle) Get(contract string) (Quote, error) {
	h.mu.Lock()
	f := h.feed
	match := h.contract == contract
	h.mu.Unlock()
	if f == nil || !match {
		return Quote{}, errors.New("shared quote subscription not ready")
	}
	return f.Get(contract)
}
func (h *Handle) Wait(ctx context.Context, contract string) (Quote, error) {
	h.mu.Lock()
	f := h.feed
	match := h.contract == contract
	h.mu.Unlock()
	if f == nil || !match {
		return Quote{}, errors.New("shared quote subscription not ready")
	}
	return f.Wait(ctx, contract)
}

// Inspect reads the shared cache only. It never subscribes, waits or calls REST.
func (h *Handle) Inspect(contract string) QuoteStatus {
	h.mu.Lock()
	feed, match := h.feed, h.contract == contract
	h.mu.Unlock()
	if feed == nil || !match {
		return QuoteStatus{Contract: contract, State: "Shared subscription not ready"}
	}
	return feed.Inspect(contract)
}
