package execution

import (
	"context"
	"testing"
	"time"

	"projectxbot/internal/projectx"
	"projectxbot/internal/signal"
)

func TestTradeScopedExit(t *testing.T) {
	for _, mode := range []string{"pending", "filled", "partial", "wrong-trade", "wrong-direction", "wrong-profile", "unrelated-order", "unrelated-position", "paused", "stale", "restart", "ambiguous-cancel", "ambiguous-close", "fill-during-cancel"} {
		t.Run(mode, func(t *testing.T) {
			e, api, _, store, c, p, a, s, now := fixture(t)
			c.Automated, c.AutomationProfileID, c.AutomationContractID = true, "swift", "mnq"
			a.AutomationConfirmed = true
			if err := e.Arm(context.Background(), c, p, a); err != nil {
				t.Fatal(err)
			}
			if err := e.ExecuteAutomation(context.Background(), "swift", s, "mnq", 1); err != nil {
				t.Fatal(err)
			}
			i := e.active()
			parent := projectx.Order{ID: i.OrderID, AccountID: 42, CustomTag: i.Tag, ContractID: "mnq", Side: 0, Type: 1, Size: i.Plan.Quantity, Status: 1}
			if mode == "pending" || mode == "ambiguous-cancel" || mode == "fill-during-cancel" {
				api.orders = []projectx.Order{parent}
			} else {
				parent.FillVolume, parent.Status = parent.Size, 2
				if mode == "partial" {
					parent.FillVolume, parent.Status = 1, 1
				}
				api.positions = []projectx.Position{{ID: 1, AccountID: 42, ContractID: "mnq", Type: 1, Size: parent.FillVolume, AveragePrice: 20000}}
				api.orders = protectedOrders(i, parent.FillVolume)
				if mode == "partial" {
					api.orders = append(api.orders, parent)
				}
			}
			api.history = []projectx.Order{parent}
			x := signal.Signal{ID: "exit-one", Action: "tp", TradeID: s.ID, Strategy: s.Strategy, Symbol: s.Symbol, Direction: s.Direction, Timestamp: *now}
			switch mode {
			case "ambiguous-cancel":
				api.cancelErr = projectx.ErrAmbiguous
			case "ambiguous-close":
				api.closeErr = projectx.ErrAmbiguous
			case "fill-during-cancel":
				api.onCancel = func() {
					api.history[0].FillVolume = 1
					api.positions = []projectx.Position{{ID: 1, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: 20000}}
				}
			case "wrong-trade":
				x.TradeID = "other"
			case "wrong-direction":
				x.Direction = "SELL"
			case "wrong-profile":
				i.ProfileID = "other"
			case "unrelated-order":
				api.orders = append(api.orders, projectx.Order{ID: 999, ContractID: "mgc"})
			case "unrelated-position":
				api.positions[0].ContractID = "mgc"
			case "paused":
				e.Pause()
			case "stale":
				x.Timestamp = now.Add(-time.Hour)
			case "restart":
				var err error
				e, err = New(store, func(int64) (API, Quotes, error) { return api, &mockQuotes{}, nil })
				if err != nil {
					t.Fatal(err)
				}
			}
			err := e.ExitAutomation(context.Background(), "swift", x, "mnq")
			if mode == "ambiguous-cancel" || mode == "ambiguous-close" {
				if err == nil || !i.EmergencyAttempted || e.Status().Armed {
					t.Fatal("uncertain mutation did not latch and disarm", err)
				}
				beforeCancel, beforeClose := api.cancels, api.closes
				recovered, err := New(store, func(int64) (API, Quotes, error) { return api, &mockQuotes{}, nil })
				if err != nil {
					t.Fatal(err)
				}
				recovered.now = e.now
				if err := recovered.connect(42); err != nil {
					t.Fatal(err)
				}
				_ = recovered.reconcile(context.Background(), recovered.active())
				if api.cancels != beforeCancel || api.closes != beforeClose {
					t.Fatal("restart replayed mutation")
				}
				return
			}
			valid := mode == "pending" || mode == "filled" || mode == "partial" || mode == "fill-during-cancel"
			if !valid {
				if err == nil || api.cancels != 0 || api.closes != 0 {
					t.Fatalf("unsafe exit: %v cancel=%d close=%d", err, api.cancels, api.closes)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "pending" && api.closes != 0 || mode != "pending" && api.closes != 1 {
				t.Fatal("incorrect close count", api.closes)
			}
			if len(api.orders) != 0 || len(api.positions) != 0 {
				t.Fatal("not flat")
			}
			cancels, closes := api.cancels, api.closes
			if err := e.ExitAutomation(context.Background(), "swift", x, "mnq"); err != nil {
				t.Fatal(err)
			}
			if cancels != api.cancels || closes != api.closes {
				t.Fatal("exit replayed")
			}
			if err := e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			*now = now.Add(4 * time.Second)
			if err := e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			if i.State != "closed" || i.Message != "Signal exit verified flat." {
				t.Fatal(i.State, i.Message)
			}
			// A late exit for this closed trade cannot affect a newer active intent.
			newer := *i
			newer.ID, newer.State = "newer-entry", "accepted"
			e.intents = append(e.intents, newer)
			if err := e.ExitAutomation(context.Background(), "swift", x, "mnq"); err != nil {
				t.Fatal(err)
			}
			if cancels != api.cancels || closes != api.closes {
				t.Fatal("late exit touched newer trade")
			}
			if !e.Status().Armed {
				t.Fatal("successful exit disarmed continuous execution")
			}
		})
	}
}
