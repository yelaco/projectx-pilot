package execution

import (
	"context"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
)

func TestLimitExpiresThirtyMinutesAfterSignal(t *testing.T) {
	for _, mode := range []string{"unfilled", "partial", "filled", "restart"} {
		t.Run(mode, func(t *testing.T) {
			e, api, q, store, c, p, a, s, now := fixture(t)
			s.Timestamp = now.Add(-2 * time.Minute) // expiry is not measured from submission
			if err := e.Arm(context.Background(), c, p, a); err != nil {
				t.Fatal(err)
			}
			preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 2)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.Submit(context.Background(), preview.Token); err != nil {
				t.Fatal(err)
			}
			i := e.active()
			parent := projectx.Order{ID: i.OrderID, AccountID: 42, ContractID: "mnq", Type: 1, Size: 2, Status: 1, CustomTag: i.Tag}
			if mode == "partial" || mode == "filled" {
				parent.FillVolume = 1
				if mode == "filled" {
					parent.FillVolume = 2
					parent.Status = 2
				}
				api.positions = []projectx.Position{{ID: 1, AccountID: 42, ContractID: "mnq", Type: 1, Size: parent.FillVolume, AveragePrice: 20000}}
				api.orders = protectedOrders(i, parent.FillVolume)
			}
			if mode != "filled" {
				api.orders = append(api.orders, parent)
			}
			api.history = []projectx.Order{parent}
			if mode == "restart" {
				e, err = New(store, func(int64) (API, Quotes, error) { return api, q, nil })
				if err != nil {
					t.Fatal(err)
				}
				e.now = func() time.Time { return *now }
				if err = e.connect(42); err != nil {
					t.Fatal(err)
				}
				i = e.active()
			}
			*now = s.Timestamp.Add(30*time.Minute - time.Second)
			q.q.At = *now
			if err = e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			if api.cancels != 0 || api.closes != 0 {
				t.Fatal("expired before thirty minutes")
			}
			*now = s.Timestamp.Add(30 * time.Minute)
			q.q.At = *now
			if err = e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "filled" {
				want = 0
			}
			if api.cancels != want || api.closes != 0 {
				t.Fatal("incorrect expiry mutation", api.cancels, api.closes)
			}
			if mode == "partial" && (len(api.positions) != 1 || len(api.orders) != 2) {
				t.Fatal("expiry removed filled exposure or brackets")
			}
			if err = e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			if api.cancels != want {
				t.Fatal("cancel replayed")
			}
			if mode == "unfilled" || mode == "restart" {
				*now = now.Add(4 * time.Second)
				if err = e.reconcile(context.Background(), i); err != nil {
					t.Fatal(err)
				}
				if i.State != "cancelled" {
					t.Fatal(i.State)
				}
			}
		})
	}
}

func TestNewSignalDoesNotReplaceWorkingEntry(t *testing.T) {
	e, api, _, _, c, p, a, s, _ := fixture(t)
	c.Automated, c.AutomationProfileID, c.AutomationContractID = true, "swift", "mnq"
	a.AutomationConfirmed = true
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	if err := e.ExecuteAutomation(context.Background(), "swift", s, "mnq", 1); err != nil {
		t.Fatal(err)
	}
	next := s
	next.ID = "second-entry"
	next.Timestamp = next.Timestamp.Add(time.Second)
	if err := e.ExecuteAutomation(context.Background(), "swift", next, "mnq", 1); err == nil {
		t.Fatal("replaced working entry")
	}
	if len(api.requests) != 1 || api.cancels != 0 || api.closes != 0 {
		t.Fatal("new signal mutated original setup")
	}
}
