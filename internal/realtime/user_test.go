package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestUserHubSubscriptionsEventsAndReconnect(t *testing.T) {
	var connections atomic.Int32
	subscribed := make(chan map[int64]bool, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer offline-test-token" {
			t.Error("missing bearer header")
			w.WriteHeader(401)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		_, b, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if !strings.Contains(string(b), `"protocol":"json"`) {
			t.Error("missing handshake")
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte("{}\x1e"))
		methods := map[string]int{}
		accounts := map[int64]bool{}
		for n := 0; n < 7; n++ {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				Target, InvocationID string
				Arguments            []int64
			}
			if json.Unmarshal([]byte(strings.TrimSuffix(string(b), "\x1e")), &msg) != nil {
				t.Error("invalid subscription")
				return
			}
			methods[msg.Target]++
			if msg.Target == "SubscribeOrders" && len(msg.Arguments) == 1 {
				accounts[msg.Arguments[0]] = true
			}
			ack, _ := json.Marshal(map[string]any{"type": 3, "invocationId": msg.InvocationID})
			_ = conn.WriteMessage(websocket.TextMessage, append(ack, 0x1e))
		}
		if methods["SubscribeAccounts"] != 1 || methods["SubscribeOrders"] != 2 || methods["SubscribePositions"] != 2 || methods["SubscribeTrades"] != 2 {
			t.Error("subscriptions not restored", methods)
		}
		subscribed <- accounts
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":1,"target":"GatewayUserOrder","arguments":[{"accountId":42,"id":99}]}`+"\x1e"))
		// Keep the connection open until cancellation/reconnect.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	callbacks := make(chan int64, 16)
	h := NewUserHub(func(context.Context) (string, error) { return "offline-test-token", nil }, func(id int64) { callbacks <- id })
	h.dial = func(ctx context.Context, _ string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		return websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), headers)
	}
	h.SetAccounts([]int64{43, 42, 42})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.Run(ctx) }()
	waitEvent := func(id int64) {
		t.Helper()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for {
			select {
			case v := <-callbacks:
				if v == id {
					return
				}
			case <-timer.C:
				t.Fatal("missing event", id)
			}
		}
	}
	waitEvent(0)
	waitEvent(42)
	if !h.Status().Connected {
		t.Fatal("stream not marked subscribed")
	}
	h.Reconnect()
	waitEvent(0)
	waitEvent(42)
	if connections.Load() < 2 {
		t.Fatal("connection not restored")
	}
	h.SetAccounts([]int64{42, 44})
	waitEvent(0)
	waitEvent(42)
	scopeReady := false
	deadline := time.NewTimer(time.Second)
	for !scopeReady {
		select {
		case accounts := <-subscribed:
			scopeReady = accounts[44] && !accounts[43]
		case <-deadline.C:
			t.Fatal("new account scope was not subscribed")
		}
	}
	deadline.Stop()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop")
	}
}
func TestUserEventScopeAndMalformedPayloads(t *testing.T) {
	for _, target := range []string{"GatewayUserOrder", "GatewayUserPosition", "GatewayUserTrade"} {
		if id := userAccount(target, []json.RawMessage{[]byte(`{"accountId":42}`)}); id != 42 {
			t.Fatal("account routing lost")
		}
	}
	if userAccount("GatewayUserAccount", []json.RawMessage{[]byte(`{"id":43}`)}) != 43 {
		t.Fatal("account update lost")
	}
	if userAccount("Untrusted", []json.RawMessage{[]byte(`{"accountId":42}`)}) != 0 || userAccount("GatewayUserOrder", nil) != 0 {
		t.Fatal("unknown event routed")
	}
}
