package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type UserStatus struct {
	State     string
	Connected bool
	Accounts  int
	LastEvent time.Time
}
type UserHub struct {
	mu      sync.Mutex
	ids     []int64
	status  UserStatus
	token   func(context.Context) (string, error)
	changed func(int64)
	cancel  context.CancelFunc
	wake    chan struct{}
	dial    func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
}

func NewUserHub(token func(context.Context) (string, error), changed func(int64)) *UserHub {
	d := *websocket.DefaultDialer
	d.Proxy = nil
	d.HandshakeTimeout = 8 * time.Second
	return &UserHub{token: token, changed: changed, wake: make(chan struct{}, 1), dial: d.DialContext, status: UserStatus{State: "idle; no account subscriptions"}}
}
func (h *UserHub) SetAccounts(ids []int64) {
	out := []int64{}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id > 0 && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	h.mu.Lock()
	if reflect.DeepEqual(out, h.ids) {
		h.mu.Unlock()
		return
	}
	h.ids = out
	cancel := h.cancel
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (h *UserHub) Reconnect() {
	h.mu.Lock()
	cancel := h.cancel
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (h *UserHub) Status() UserStatus { h.mu.Lock(); defer h.mu.Unlock(); return h.status }
func (h *UserHub) set(state string, connected bool, n int) {
	h.mu.Lock()
	h.status.State = state
	h.status.Connected = connected
	h.status.Accounts = n
	h.mu.Unlock()
}
func (h *UserHub) Run(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		h.mu.Lock()
		ids := append([]int64(nil), h.ids...)
		if len(ids) == 0 {
			h.mu.Unlock()
			h.set("idle; no account subscriptions", false, 0)
			select {
			case <-ctx.Done():
				return
			case <-h.wake:
			}
			continue
		}
		// Publish the cancellation handle with the account snapshot under one lock.
		// Scope changes must never miss a connection being started.
		child, cancel := context.WithCancel(ctx)
		h.cancel = cancel
		h.mu.Unlock()
		err := h.connection(child, ids)
		cancel()
		h.mu.Lock()
		h.cancel = nil
		h.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		h.set(err.Error()+"; reconnecting", false, len(ids))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-h.wake:
			timer.Stop()
			delay = time.Second
		case <-timer.C:
			if delay < 30*time.Second {
				delay *= 2
			}
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}
func (h *UserHub) connection(ctx context.Context, ids []int64) error {
	h.set("authenticating user data", false, len(ids))
	tokenCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	token, err := h.token(tokenCtx)
	cancel()
	if err != nil {
		return errors.New("user-data authentication unavailable")
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	conn, response, err := h.dial(ctx, "wss://rtc.topstepx.com/hubs/user", headers)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return errors.New("user-data WebSocket connection failed")
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	write := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, append(b, 0x1e))
	}
	if write(map[string]any{"protocol": "json", "version": 1}) != nil {
		return errors.New("user-data handshake failed")
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	handshake := false
	pending := map[string]bool{}
	var buffer []byte
	lastPing := time.Now()
	ackDeadline := time.Now().Add(10 * time.Second)
	for {
		_, b, err := conn.ReadMessage()
		if err != nil {
			return errors.New("user-data connection lost")
		}
		buffer = append(buffer, b...)
		if len(buffer) > 1<<20 {
			return errors.New("user-data frame too large")
		}
		for {
			end := bytes.IndexByte(buffer, 0x1e)
			if end < 0 {
				break
			}
			frame := buffer[:end]
			buffer = buffer[end+1:]
			if len(frame) == 0 {
				continue
			}
			if !handshake {
				var ack map[string]any
				if json.Unmarshal(frame, &ack) != nil || len(ack) != 0 {
					return errors.New("user-data handshake rejected")
				}
				handshake = true
				invoke := func(target string, args []any) error {
					id := "u" + strconv.Itoa(len(pending)+1)
					pending[id] = true
					return write(map[string]any{"type": 1, "invocationId": id, "target": target, "arguments": args})
				}
				if invoke("SubscribeAccounts", []any{}) != nil {
					return errors.New("user subscription failed")
				}
				for _, id := range ids {
					for _, method := range []string{"SubscribeOrders", "SubscribePositions", "SubscribeTrades"} {
						if invoke(method, []any{id}) != nil {
							return errors.New("account subscription failed")
						}
					}
				}
				h.set("waiting for user subscription acknowledgements", false, len(ids))
				continue
			}
			var msg struct {
				Type       int               `json:"type"`
				Invocation string            `json:"invocationId"`
				Target     string            `json:"target"`
				Arguments  []json.RawMessage `json:"arguments"`
				Error      string            `json:"error"`
			}
			if json.Unmarshal(frame, &msg) != nil {
				return errors.New("user-data protocol invalid")
			}
			switch msg.Type {
			case 3:
				if msg.Error != "" {
					return errors.New("user-data subscription rejected")
				}
				if pending[msg.Invocation] {
					delete(pending, msg.Invocation)
					if len(pending) == 0 {
						h.set("subscribed; reconnect reconciliation requested", true, len(ids))
						h.changed(0)
					}
				}
			case 1:
				id := userAccount(msg.Target, msg.Arguments)
				if id > 0 {
					h.mu.Lock()
					h.status.LastEvent = time.Now().UTC()
					h.mu.Unlock()
					h.changed(id)
				}
			case 7:
				return errors.New("user-data server closed connection")
			case 6:
			default:
			}
		}
		deadline := time.Now().Add(30 * time.Second)
		if (!handshake || len(pending) > 0) && ackDeadline.Before(deadline) {
			deadline = ackDeadline
		}
		conn.SetReadDeadline(deadline)
		if time.Since(lastPing) > 10*time.Second {
			if write(map[string]int{"type": 6}) != nil {
				return errors.New("user-data keepalive failed")
			}
			lastPing = time.Now()
		}
	}
}
func userAccount(target string, args []json.RawMessage) int64 {
	if target != "GatewayUserAccount" && target != "GatewayUserOrder" && target != "GatewayUserPosition" && target != "GatewayUserTrade" {
		return 0
	}
	if len(args) != 1 {
		return 0
	}
	var v struct {
		ID        int64 `json:"id"`
		AccountID int64 `json:"accountId"`
	}
	if json.Unmarshal(args[0], &v) != nil {
		return 0
	}
	if target == "GatewayUserAccount" {
		return v.ID
	}
	return v.AccountID
}
