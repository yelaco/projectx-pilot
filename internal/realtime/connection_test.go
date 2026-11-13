package realtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestFeedWaitsForFirstQuoteAfterSubscription(t *testing.T) {
	subscribed := make(chan struct{})
	release := make(chan struct{})
	serverError := make(chan string, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer OFFLINE_MOCK_JWT" {
			serverError <- "missing authorization header"
			w.WriteHeader(401)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		if err = conn.WriteMessage(websocket.TextMessage, []byte("{}\x1e")); err != nil {
			return
		}
		_, b, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if !strings.Contains(string(b), "SubscribeContractQuotes") || !strings.Contains(string(b), "mnq") {
			serverError <- "missing quote subscription"
			return
		}
		close(subscribed)
		<-release
		payload := fmt.Sprintf("{\"type\":1,\"target\":\"GatewayQuote\",\"arguments\":[\"mnq\",{\"lastPrice\":20000,\"lastUpdated\":\"%s\"}]}\x1e", time.Now().UTC().Format(time.RFC3339Nano))
		if err = conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			return
		}
		_, _, _ = conn.ReadMessage() // hold open until Feed.Close
	}))
	defer server.Close()
	feed := New(func(context.Context) (string, error) { return "OFFLINE_MOCK_JWT", nil })
	feed.dial = func(ctx context.Context, _ string, h http.Header) (*websocket.Conn, *http.Response, error) {
		return websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), h)
	}
	feed.Watch(context.Background(), "mnq")
	defer feed.Close()
	select {
	case <-subscribed:
	case msg := <-serverError:
		t.Fatal(msg)
	case <-time.After(time.Second):
		t.Fatal("subscription not established")
	}
	if _, err := feed.Get("mnq"); err == nil {
		t.Fatal("quote fabricated before delivery")
	}
	ready := make(chan Quote, 1)
	failure := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		q, err := feed.Wait(ctx, "mnq")
		if err != nil {
			failure <- err
			return
		}
		ready <- q
	}()
	close(release)
	select {
	case q := <-ready:
		if q.Price != 20000 {
			t.Fatal("wrong quote")
		}
	case err := <-failure:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("first quote not delivered")
	}
	feed.Close()
	if _, err := feed.Get("mnq"); err == nil {
		t.Fatal("closed feed retained quote permission")
	}
}
func TestFeedWaitBoundsAndOldGenerationCannotPublish(t *testing.T) {
	feed := New(nil)
	feed.contract = "mnq"
	feed.generation = 2
	feed.state = "connecting"
	feed.set("mnq", 1, Quote{Price: 123, At: time.Now()}, "receiving")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := feed.Wait(ctx, "mnq"); err == nil {
		t.Fatal("old connection supplied quote")
	}
	feed.set("mnq", 2, Quote{Price: 123, At: time.Now().Add(-time.Minute)}, "receiving")
	if _, err := feed.Get("mnq"); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatal("stale data accepted", err)
	}
}
func TestSubscriptionErrorsAreSpecificAndRedacted(t *testing.T) {
	p := protocol{contract: "mnq"}
	_, _, err := p.consume([]byte("{}\x1e{\"type\":3,\"invocationId\":\"quote\",\"error\":\"PRIVATE_PROVIDER_DETAILS\"}\x1e"))
	if err == nil || !strings.Contains(err.Error(), "subscription rejected") || strings.Contains(err.Error(), "PRIVATE_PROVIDER") {
		t.Fatal("subscription error missing or leaked")
	}
}
func TestWebSocketAuthorizationFailureIsSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "PRIVATE_PROVIDER_DETAILS", 401) }))
	defer server.Close()
	feed := New(func(context.Context) (string, error) { return "OFFLINE_MOCK_JWT", nil })
	feed.contract = "mnq"
	feed.generation = 1
	feed.dial = func(ctx context.Context, _ string, h http.Header) (*websocket.Conn, *http.Response, error) {
		return websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), h)
	}
	err := feed.connection(context.Background(), "mnq", 1)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "PRIVATE_PROVIDER") || strings.Contains(err.Error(), "MOCK_JWT") {
		t.Fatal("unsafe or missing authentication diagnosis")
	}
}
