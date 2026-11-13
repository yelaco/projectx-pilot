package projectx

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestExposureReads(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	calls := 0
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["accountId"] != float64(42) || r.Header.Get("Authorization") != "Bearer test" {
			t.Fatal("account or auth missing")
		}
		switch r.URL.Path {
		case "/api/Order/searchOpen":
			return response(200, `{"success":true,"orders":[{"id":7,"accountId":42,"contractId":"MNQ","size":2,"fillVolume":1,"parentOrderId":3,"linkedOrderId":8,"status":1}]}`), nil
		case "/api/Position/searchOpen":
			return response(200, `{"success":true,"positions":[{"id":4,"accountId":42,"contractId":"MNQ","size":1,"type":1,"averagePrice":20000}]}`), nil
		case "/api/Trade/search":
			if body["startTimestamp"] != "2026-11-13T11:00:00Z" || body["endTimestamp"] != "2026-11-13T12:00:00Z" {
				t.Fatal(body)
			}
			return response(200, `{"success":true,"trades":[{"id":5,"accountId":42,"creationTimestamp":"2026-11-13T11:30:00Z","profitAndLoss":null,"fees":0,"commissions":0.5}]}`), nil
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
			return nil, nil
		}
	})
	c.token, c.renewAt = "test", now.AddDate(10, 0, 0)
	orders, err := c.OpenOrders(context.Background(), 42)
	if err != nil || len(orders) != 1 || orders[0].FillVolume != 1 || orders[0].ParentOrderID == nil || *orders[0].LinkedOrderID != 8 {
		t.Fatalf("%+v %v", orders, err)
	}
	positions, err := c.OpenPositions(context.Background(), 42)
	if err != nil || len(positions) != 1 || positions[0].Size != 1 {
		t.Fatal(positions, err)
	}
	trades, err := c.Trades(context.Background(), 42, now.Add(-time.Hour), now)
	if err != nil || len(trades) != 1 || trades[0].ProfitAndLoss != nil || trades[0].Fees == nil || *trades[0].Commissions != .5 {
		t.Fatal(trades, err)
	}
	if calls != 3 {
		t.Fatal("unexpected retries", calls)
	}
}

func TestIncompleteExposureCannotEstablishFlatness(t *testing.T) {
	for _, body := range []string{
		`{"success":true}`, `{"success":true,"orders":null,"positions":null}`,
		`{"success":true,"orders":[{"id":1,"accountId":99,"contractId":"x","size":1}],"positions":[{"id":1,"accountId":99,"contractId":"x","size":1}]}`,
	} {
		c := testClient(t, func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		c.token, c.renewAt = "test", time.Now().Add(time.Hour)
		if _, err := c.OpenOrders(context.Background(), 42); err == nil {
			t.Fatal("accepted incomplete orders", body)
		}
		if _, err := c.OpenPositions(context.Background(), 42); err == nil {
			t.Fatal("accepted incomplete positions", body)
		}
	}
	c := testClient(t, func(*http.Request) (*http.Response, error) {
		return response(200, `{"success":true,"orders":[],"positions":[]}`), nil
	})
	c.token, c.renewAt = "test", time.Now().Add(time.Hour)
	if _, err := c.OpenOrders(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenPositions(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
}

func TestExposureValidationBeforeNetwork(t *testing.T) {
	c := testClient(t, func(*http.Request) (*http.Response, error) { t.Fatal("network used"); return nil, nil })
	if _, err := c.OpenOrders(context.Background(), 0); err == nil {
		t.Fatal("invalid account")
	}
	if _, err := c.OpenPositions(context.Background(), -1); err == nil {
		t.Fatal("invalid account")
	}
	if _, err := c.Trades(context.Background(), 42, time.Now(), time.Time{}); err == nil {
		t.Fatal("invalid interval")
	}
}

func TestSimulatedAccountIsNotInferred(t *testing.T) {
	var accounts []Account
	if err := json.Unmarshal([]byte(`[{"id":1},{"id":2,"simulated":false},{"id":3,"simulated":true}]`), &accounts); err != nil {
		t.Fatal(err)
	}
	if accounts[0].Simulated != nil || accounts[1].Simulated == nil || *accounts[1].Simulated || accounts[2].Simulated == nil || !*accounts[2].Simulated {
		t.Fatal(accounts)
	}
}
