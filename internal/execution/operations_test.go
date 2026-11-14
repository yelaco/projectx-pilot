package execution

import (
	"context"
	"testing"

	"projectxbot/internal/projectx"
)

func TestOperationDayExecutionHierarchy(t *testing.T) {
	for _, mode := range []string{"general", "manager", "other-manager", "submit-recheck", "pending", "filled"} {
		t.Run(mode, func(t *testing.T) {
			e, api, _, _, c, p, a, s, _ := fixture(t)
			dates := []string{"2026-11-13"}
			if mode == "general" {
				if err := e.SetOperationPlan(dates, nil); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "manager" {
				if err := e.SetOperationPlan(nil, map[string][]string{p.ID: dates}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "other-manager" {
				if err := e.SetOperationPlan(nil, map[string][]string{"other": dates}); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.Arm(context.Background(), c, p, a); err != nil {
				t.Fatal(err)
			}
			preview, err := e.PrepareOrder(context.Background(), s, "mnq", 1, 2)
			if mode == "general" || mode == "manager" {
				if err == nil || len(api.requests) != 0 {
					t.Fatal("operation block bypassed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "submit-recheck" {
				if err := e.SetOperationPlan(dates, nil); err != nil {
					t.Fatal(err)
				}
			}
			err = e.Submit(context.Background(), preview.Token)
			if mode == "submit-recheck" {
				if err == nil || len(api.requests) != 0 {
					t.Fatal("submit bypassed operation gate")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "other-manager" {
				return
			}
			i := e.active()
			parent := projectx.Order{ID: i.OrderID, AccountID: 42, ContractID: "mnq", Type: 1, Size: 2, Status: 1, CustomTag: i.Tag}
			api.orders = []projectx.Order{parent}
			if mode == "filled" {
				parent.FillVolume, parent.Status = 2, 2
				api.orders = protectedOrders(i, 2)
				api.positions = []projectx.Position{{ID: 1, AccountID: 42, ContractID: "mnq", Type: 1, Size: 2, AveragePrice: 20000}}
			}
			api.history = []projectx.Order{parent}
			if err := e.SetOperationPlan(nil, map[string][]string{p.ID: dates}); err != nil {
				t.Fatal(err)
			}
			if err := e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "filled" {
				want = 0
			}
			if api.cancels != want || api.closes != 0 {
				t.Fatal("wrong management on blocked day", api.cancels, api.closes)
			}
		})
	}
}
