package projectx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestProtectedTradingPayloads(t *testing.T) {
	for _, typ := range []int{1, 2} {
		c := testClient(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/Order/place" {
				t.Fatal(r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["accountId"] != float64(42) || body["type"] != float64(typ) || body["size"] != float64(2) || body["customTag"] != "durable-tag" {
				t.Fatal(body)
			}
			if typ == 2 && body["limitPrice"] != nil || typ == 1 && body["limitPrice"] != float64(20000) {
				t.Fatal("wrong entry price", body)
			}
			if body["stopLossBracket"].(map[string]any)["type"] != float64(4) || body["takeProfitBracket"].(map[string]any)["type"] != float64(1) {
				t.Fatal("unprotected request")
			}
			return response(200, `{"success":true,"errorCode":0,"orderId":77}`), nil
		})
		c.token = "test"
		c.renewAt = time.Now().Add(time.Hour)
		tr, _ := NewTrading(c, 42)
		r := PlaceRequest{AccountID: 42, ContractID: "mnq", Type: typ, Side: 0, Size: 2, CustomTag: "durable-tag", StopLossBracket: Bracket{Ticks: -40, Type: 4}, TakeProfitBracket: Bracket{Ticks: 80, Type: 1}}
		if typ == 1 {
			v := 20000.0
			r.LimitPrice = &v
		}
		if id, err := tr.Place(context.Background(), r); err != nil || id != 77 {
			t.Fatal(id, err)
		}
	}
}
func TestTradingAmbiguityNeverRetries(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		transport  bool
	}{
		{"timeout", "", 0, true}, {"upstream-error", "", 503, false}, {"malformed", "{", 200, false}, {"missing-id", `{"success":true}`, 200, false}, {"pending", `{"success":false,"errorCode":6}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(*http.Request) (*http.Response, error) {
				calls++
				if tc.transport {
					return nil, errors.New("transport timeout")
				}
				return response(tc.status, tc.body), nil
			})
			c.token = "test"
			c.renewAt = time.Now().Add(time.Hour)
			tr, _ := NewTrading(c, 42)
			_, err := tr.Place(context.Background(), PlaceRequest{AccountID: 42, ContractID: "mnq", Type: 2, Side: 0, Size: 1, CustomTag: "tag", StopLossBracket: Bracket{Ticks: -1, Type: 4}, TakeProfitBracket: Bracket{Ticks: 2, Type: 1}})
			if !errors.Is(err, ErrAmbiguous) || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
}
func TestTradingPinAndProtection(t *testing.T) {
	c := testClient(t, func(*http.Request) (*http.Response, error) { t.Fatal("unsafe network request"); return nil, nil })
	tr, _ := NewTrading(c, 42)
	r := PlaceRequest{AccountID: 99, ContractID: "mnq", Type: 2, Size: 1, CustomTag: "tag", StopLossBracket: Bracket{Ticks: -1, Type: 4}, TakeProfitBracket: Bracket{Ticks: 1, Type: 1}}
	if _, err := tr.Place(context.Background(), r); err == nil {
		t.Fatal("wrong account accepted")
	}
	r.AccountID = 42
	r.StopLossBracket = Bracket{}
	if _, err := tr.Place(context.Background(), r); err == nil {
		t.Fatal("unprotected order accepted")
	}
	if err := c.call(context.Background(), "/api/Order/place", "test", r, &struct{}{}); err == nil {
		t.Fatal("read-only client writes")
	}
}

func TestSignedBracketOffsetsSerializedBySide(t *testing.T) {
	for _, tc := range []struct {
		name               string
		side, stop, target int
	}{{"BUY", 0, -40, 80}, {"SELL", 1, 40, -80}} {
		for _, typ := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/type%d", tc.name, typ), func(t *testing.T) {
				calls := 0
				c := testClient(t, func(r *http.Request) (*http.Response, error) {
					calls++
					var body struct {
						Side   int     `json:"side"`
						Stop   Bracket `json:"stopLossBracket"`
						Target Bracket `json:"takeProfitBracket"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body.Side != tc.side || body.Stop.Ticks != tc.stop || body.Target.Ticks != tc.target {
						t.Fatal("wrong signed JSON offsets", body)
					}
					return response(200, `{"success":true,"errorCode":0,"orderId":77}`), nil
				})
				c.token = "MOCK"
				c.renewAt = time.Now().Add(time.Hour)
				tr, _ := NewTrading(c, 42)
				price := 20000.0
				req := PlaceRequest{AccountID: 42, ContractID: "mnq", Type: typ, Side: tc.side, Size: 1, CustomTag: "mock-tag", StopLossBracket: Bracket{Ticks: tc.stop, Type: 4}, TakeProfitBracket: Bracket{Ticks: tc.target, Type: 1}}
				if typ == 1 {
					req.LimitPrice = &price
				}
				if _, err := tr.Place(context.Background(), req); err != nil || calls != 1 {
					t.Fatal(err, calls)
				}
			})
		}
	}
}
func TestInvalidSignedProtectionNeverTransmits(t *testing.T) {
	c := testClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid bracket reached transport")
		return nil, nil
	})
	tr, _ := NewTrading(c, 42)
	for _, tc := range []struct{ side, stop, target int }{{0, 40, 80}, {0, -40, -80}, {1, -40, -80}, {1, 40, 80}, {0, 0, 80}, {1, 40, 0}} {
		_, err := tr.Place(context.Background(), PlaceRequest{AccountID: 42, ContractID: "mnq", Type: 2, Side: tc.side, Size: 1, CustomTag: "mock", StopLossBracket: Bracket{Ticks: tc.stop, Type: 4}, TakeProfitBracket: Bracket{Ticks: tc.target, Type: 1}})
		if err == nil {
			t.Fatal("invalid directional protection accepted", tc)
		}
	}
}
