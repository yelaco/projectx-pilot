package projectx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func testClient(t *testing.T, f transport) *Client {
	t.Helper()
	c, err := New("user", "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = f
	return c
}
func TestDiscoveryAndRenewal(t *testing.T) {
	var login, renew int
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" {
			t.Error("wrong method")
		}
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/api/Auth/loginKey":
			login++
			if string(b) != `{"userName":"user","apiKey":"secret"}` {
				t.Errorf("wrong login schema: %s", b)
			}
			return response(200, `{"success":true,"errorCode":0,"token":"one"}`), nil
		case "/api/Auth/validate":
			renew++
			if r.Header.Get("Authorization") != "Bearer one" {
				t.Error("missing auth")
			}
			return response(200, `{"success":true,"errorCode":0,"newToken":"two"}`), nil
		case "/api/Account/search":
			if string(b) != `{"onlyActiveAccounts":true}` {
				t.Error("wrong filter")
			}
			return response(200, `{"success":true,"accounts":[{"id":42,"balance":50000,"canTrade":true}]}`), nil
		case "/api/Contract/available":
			if string(b) != `{"live":false}` {
				t.Error("must use sim data")
			}
			return response(200, `{"success":true,"contracts":[{"id":"x","tickSize":0.25,"tickValue":0.5}]}`), nil
		case "/api/Contract/search":
			if !strings.Contains(string(b), `"searchText":"MNQ"`) {
				t.Error("wrong search")
			}
			return response(200, `{"success":true,"contracts":[]}`), nil
		case "/api/Contract/searchById":
			return response(200, `{"success":true,"contract":{"id":"x","tickSize":0.25,"tickValue":0.5}}`), nil
		}
		t.Fatal("unexpected endpoint")
		return nil, nil
	})
	a, err := c.Accounts(context.Background())
	if err != nil || len(a) != 1 || a[0].ID != 42 {
		t.Fatalf("%v %v", a, err)
	}
	c.renewAt = time.Time{}
	contracts, err := c.AvailableContracts(context.Background())
	if err != nil || len(contracts) != 1 {
		t.Fatal(err)
	}
	if _, err = c.SearchContracts(context.Background(), "MNQ"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Contract(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if login != 1 || renew != 1 {
		t.Fatalf("login=%d renew=%d", login, renew)
	}
}
func TestConcurrentAuthentication(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(200, `{"success":true,"token":"t"}`), nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Authenticate(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("authentication storm", calls.Load())
	}
}
func TestFailuresAreRedactedAndNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		status       int
		transportErr bool
	}{
		{"business", `{"success":false,"errorCode":3,"errorMessage":"secret"}`, 200, false},
		{"http", "secret", 401, false}, {"malformed", "secret", 200, false}, {"empty-token", `{"success":true}`, 200, false},
		{"oversized", strings.Repeat("s", maxResponse+1), 200, false}, {"timeout", "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(*http.Request) (*http.Response, error) {
				calls++
				if tc.transportErr {
					return nil, fmt.Errorf("secret timeout")
				}
				return response(tc.status, tc.body), nil
			})
			err := c.Authenticate(context.Background())
			if err == nil || strings.Contains(err.Error(), "secret") || calls != 1 {
				t.Fatalf("unsafe failure: %v calls=%d", err, calls)
			}
		})
	}
}
func TestCancellationAndTradingEndpointBlock(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Authenticate(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.call(context.Background(), "/api/Order/place", "", nil, nil); err == nil {
		t.Fatal("trading allowed")
	}
}
func TestTransportSafety(t *testing.T) {
	c, _ := New("u", "k", time.Second)
	tr := c.http.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("proxy enabled")
	}
	if c.http.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirects enabled")
	}
}
func TestUnauthorizedInvalidatesSession(t *testing.T) {
	count := 0
	c := testClient(t, func(*http.Request) (*http.Response, error) { count++; return response(401, ""), nil })
	c.token = "expired"
	c.renewAt = time.Now().Add(time.Hour)
	_, err := c.Accounts(context.Background())
	if err == nil || c.token != "" || count != 1 {
		t.Fatalf("err=%v count=%d", err, count)
	}
}

func TestExpiredRenewalFallsBackToLogin(t *testing.T) {
	var paths []string
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/Auth/validate" {
			return response(401, ""), nil
		}
		return response(200, `{"success":true,"token":"replacement"}`), nil
	})
	c.token = "old"
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.token != "replacement" || len(paths) != 2 || paths[0] != "/api/Auth/validate" || paths[1] != "/api/Auth/loginKey" {
		t.Fatal(paths)
	}
}

func TestBracketModeDiagnosticUsesOnlyDocumentedAllowlistedMessage(t *testing.T) {
	for _, tc := range []struct {
		message string
		code    int
		want    bool
	}{
		{"Brackets cannot be used with Position Brackets. You must enable Auto OCO Brackets.", 2, true},
		{"PRIVATE_RESPONSE_DETAILS", 2, false},
		{"Brackets cannot be used with Position Brackets. You must enable Auto OCO Brackets.", 7, false},
	} {
		b, _ := json.Marshal(map[string]any{"success": false, "errorCode": tc.code, "errorMessage": tc.message})
		c := testClient(t, func(*http.Request) (*http.Response, error) { return response(200, string(b)), nil })
		err := c.request(context.Background(), "/api/Order/place", "MOCK_TOKEN", struct{}{}, &struct{}{})
		var ae *APIError
		if !errors.As(err, &ae) || ae.AutoOCORequired != tc.want {
			t.Fatal("incorrect bracket classification")
		}
		if strings.Contains(err.Error(), "PRIVATE_RESPONSE") || strings.Contains(ae.SafeMessage(), "PRIVATE_RESPONSE") {
			t.Fatal("provider content leaked")
		}
	}
}

func TestPlacementReasonIsBoundedRedactedAndNotPartOfError(t *testing.T) {
	const detail = "Invalid stop loss ticks (400). PRIVATE_OPERATOR_DETAIL"
	c := testClient(t, func(*http.Request) (*http.Response, error) {
		b, _ := json.Marshal(map[string]any{"success": false, "errorCode": 2, "errorMessage": detail + " key=secret jwt=MOCK_ACCESS_TOKEN user=user\n"})
		return response(200, string(b)), nil
	})
	err := c.request(context.Background(), "/api/Order/place", "MOCK_ACCESS_TOKEN", struct{}{}, &struct{}{})
	var ae *APIError
	if !errors.As(err, &ae) || !strings.Contains(ae.ProviderReason, "Invalid stop loss ticks") {
		t.Fatal("missing UI reason")
	}
	for _, secret := range []string{"secret", "MOCK_ACCESS_TOKEN", "user=user", "\n"} {
		if strings.Contains(ae.ProviderReason, secret) {
			t.Fatal("reason contains secret/control")
		}
	}
	serialized, _ := json.Marshal(ae)
	if strings.Contains(err.Error(), "PRIVATE_OPERATOR_DETAIL") || strings.Contains(ae.SafeMessage(), "PRIVATE_OPERATOR_DETAIL") || strings.Contains(string(serialized), "PRIVATE_OPERATOR_DETAIL") {
		t.Fatal("runtime reason leaked through logging/serialization surface")
	}
	if len([]rune(c.placementReason("/api/Order/place", strings.Repeat("a", 5000), ""))) > 1025 {
		t.Fatal("unbounded reason")
	}
	if c.placementReason("/api/Auth/loginKey", "PRIVATE_AUTH_BODY", "") != "" {
		t.Fatal("captured unrelated response")
	}
}
