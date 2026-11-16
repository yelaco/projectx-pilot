// Package realtime implements the documented SignalR JSON market-quote subset
// over Gorilla WebSocket. It never carries order execution traffic.
package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Quote struct {
	Price float64
	At    time.Time
}
type Feed struct {
	mu         sync.Mutex
	generation uint64
	changed    chan struct{}
	state      string
	quote      Quote
	contract   string
	cancel     context.CancelFunc
	done       chan struct{}
	token      func(context.Context) (string, error)
	dial       func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
}

func New(token func(context.Context) (string, error)) *Feed {
	d := *websocket.DefaultDialer
	d.Proxy = nil
	d.HandshakeTimeout = 8 * time.Second
	return &Feed{token: token, dial: d.DialContext, changed: make(chan struct{}), state: "subscription not started"}
}
func (f *Feed) Watch(ctx context.Context, contract string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.contract == contract && f.done != nil {
		select {
		case <-f.done:
		default:
			return
		}
	}
	if f.cancel != nil {
		f.cancel()
	}
	child, cancel := context.WithCancel(ctx)
	f.cancel = cancel
	f.contract = contract
	f.generation++
	generation := f.generation
	f.state = "connecting to ProjectX market data"
	f.quote = Quote{}
	f.notifyLocked()
	f.done = make(chan struct{})
	done := f.done
	go func() { defer close(done); f.run(child, contract, generation) }()
}
func (f *Feed) Close() {
	f.mu.Lock()
	cancel, done := f.cancel, f.done
	f.cancel = nil
	f.contract = ""
	f.generation++
	f.quote = Quote{}
	f.state = "market subscription stopped"
	f.notifyLocked()
	f.done = nil
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
func (f *Feed) Get(contract string) (Quote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getLocked(contract)
}
func (f *Feed) getLocked(contract string) (Quote, error) {
	if f.contract != contract {
		return Quote{}, errors.New("market subscription is not started for the selected contract")
	}
	if f.quote.At.IsZero() {
		return Quote{}, fmt.Errorf("fresh ProjectX market quote unavailable: %s", f.state)
	}
	if time.Since(f.quote.At) > 5*time.Second || f.quote.At.After(time.Now().Add(time.Second)) {
		return Quote{}, errors.New("ProjectX market quote is stale; check market hours, selected expiry and market-data access")
	}
	return f.quote, nil
}

// Wait is event-driven and bounded by the caller. It never substitutes a stale
// quote or starts a trading request. Active-position monitoring uses Get instead.
func (f *Feed) Wait(ctx context.Context, contract string) (Quote, error) {
	for {
		f.mu.Lock()
		q, err := f.getLocked(contract)
		changed := f.changed
		f.mu.Unlock()
		if err == nil {
			return q, nil
		}
		select {
		case <-ctx.Done():
			return Quote{}, fmt.Errorf("%w: %v", ctx.Err(), err)
		case <-changed:
		}
	}
}
func (f *Feed) notifyLocked() {
	if f.changed != nil {
		close(f.changed)
	}
	f.changed = make(chan struct{})
}
func (f *Feed) set(contract string, generation uint64, q Quote, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.contract == contract && f.generation == generation {
		f.quote = q
		f.state = state
		f.notifyLocked()
	}
}
func (f *Feed) phase(contract string, generation uint64, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.contract == contract && f.generation == generation {
		f.state = state
		f.notifyLocked()
	}
}
func (f *Feed) run(ctx context.Context, contract string, generation uint64) {
	delay := time.Second
	for ctx.Err() == nil {
		err := f.connection(ctx, contract, generation)
		if ctx.Err() != nil {
			return
		}
		f.set(contract, generation, Quote{}, err.Error()+"; reconnecting")
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
		}
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
}
func (f *Feed) connection(ctx context.Context, contract string, generation uint64) error {
	f.phase(contract, generation, "authenticating market-data connection")
	tokenCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	token, err := f.token(tokenCtx)
	cancel()
	if err != nil {
		return errors.New("market-data authentication failed; check ProjectX API credentials")
	}
	f.phase(contract, generation, "connecting to ProjectX market data")
	// Bearer header avoids putting credentials in URLs and error messages.
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	conn, resp, err := f.dial(ctx, "wss://rtc.topstepx.com/hubs/market", h)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return fmt.Errorf("market WebSocket connection rejected (HTTP %d); check authentication or market-data access", resp.StatusCode)
		}
		return errors.New("market WebSocket connection failed; check local network access")
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	write := func(v any) error {
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, append(b, 0x1e))
	}
	if write(map[string]any{"protocol": "json", "version": 1}) != nil {
		return errors.New("market SignalR handshake could not be sent")
	}
	f.phase(contract, generation, "waiting for market SignalR handshake")
	decoder := protocol{contract: contract}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	subscribed := false
	lastPing := time.Now()
	for {
		_, b, e := conn.ReadMessage()
		if e != nil {
			return errors.New("market connection lost or timed out")
		}
		quotes, ready, e := decoder.consume(b)
		if e != nil {
			return e
		}
		if ready && !subscribed {
			if write(map[string]any{"type": 1, "invocationId": "quote", "target": "SubscribeContractQuotes", "arguments": []string{contract}}) != nil {
				return errors.New("market quote subscription could not be sent")
			}
			f.phase(contract, generation, "waiting for first market quote; check market hours and selected expiry")
			subscribed = true
		}
		for _, q := range quotes {
			f.set(contract, generation, q, "receiving market quotes")
		}
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		// Respond to server activity with an application-level SignalR ping. Only
		// this reader goroutine writes; no concurrent websocket writers.
		if time.Since(lastPing) >= 10*time.Second {
			if write(map[string]int{"type": 6}) != nil {
				return errors.New("market keepalive failed")
			}
			lastPing = time.Now()
		}
	}
}

type protocol struct {
	contract  string
	buffer    []byte
	handshake bool
}

func (p *protocol) consume(data []byte) ([]Quote, bool, error) {
	p.buffer = append(p.buffer, data...)
	if len(p.buffer) > 1<<20 {
		return nil, false, errors.New("hub frame too large")
	}
	var quotes []Quote
	for {
		i := bytes.IndexByte(p.buffer, 0x1e)
		if i < 0 {
			break
		}
		frame := p.buffer[:i]
		p.buffer = p.buffer[i+1:]
		if !p.handshake {
			var h map[string]json.RawMessage
			if json.Unmarshal(frame, &h) != nil || h["error"] != nil || h["type"] != nil {
				return nil, false, errors.New("hub handshake failed")
			}
			p.handshake = true
			continue
		}
		var m struct {
			Type      int               `json:"type"`
			Target    string            `json:"target"`
			Error     string            `json:"error"`
			Arguments []json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(frame, &m) != nil {
			return nil, false, errors.New("invalid market hub message")
		}
		if m.Type == 3 && m.Error != "" {
			return nil, false, errors.New("market quote subscription rejected; check contract expiry and market-data access")
		}
		if m.Type == 7 || m.Error != "" {
			return nil, false, errors.New("market hub disconnected")
		}
		if m.Type != 1 || m.Target != "GatewayQuote" || len(m.Arguments) != 2 {
			continue
		}
		var contract string
		var q struct {
			LastPrice   float64   `json:"lastPrice"`
			LastUpdated time.Time `json:"lastUpdated"`
			Timestamp   time.Time `json:"timestamp"`
		}
		if json.Unmarshal(m.Arguments[0], &contract) != nil || contract != p.contract || json.Unmarshal(m.Arguments[1], &q) != nil {
			continue
		}
		at := q.LastUpdated
		if at.IsZero() {
			at = q.Timestamp
		}
		if q.LastPrice <= 0 || math.IsNaN(q.LastPrice) || math.IsInf(q.LastPrice, 0) || at.IsZero() || at.After(time.Now().Add(time.Second)) {
			continue
		}
		quotes = append(quotes, Quote{Price: q.LastPrice, At: at})
	}
	return quotes, p.handshake, nil
}

// QuoteStatus is diagnostic cache state, never authorization to place an order.
type QuoteStatus struct {
	Contract string
	State    string
	At       time.Time
	Fresh    bool
}

func (f *Feed) Inspect(contract string) QuoteStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.contract != contract {
		return QuoteStatus{Contract: contract, State: "Subscription not selected"}
	}
	_, err := f.getLocked(contract)
	return QuoteStatus{Contract: contract, State: f.state, At: f.quote.At, Fresh: err == nil}
}
