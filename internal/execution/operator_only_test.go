package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
)

func TestAutomaticManagementPreservesEstablishedExposure(t *testing.T) {
	for _, name := range []string{"missing stop", "changed stop", "duplicate entry", "entry mismatch", "position mismatch", "extra position", "missing costs", "daily limit", "floor limit", "schedule", "quote outage", "read outage", "flat snapshot"} {
		t.Run(name, func(t *testing.T) {
			e, api, q, store, c, p, a, s, now := fixture(t)
			if err := e.Arm(context.Background(), c, p, a); err != nil {
				t.Fatal(err)
			}
			preview, err := e.PrepareOrder(context.Background(), s, "mnq", 2, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.Submit(context.Background(), preview.Token); err != nil {
				t.Fatal(err)
			}
			i := e.active()
			price := 20000.0
			api.history = []projectx.Order{{ID: 77, AccountID: 42, ContractID: "mnq", CustomTag: i.Tag, Type: 2, Side: 0, Size: 1, FillVolume: 1, Status: 2, FilledPrice: &price}}
			api.positions = []projectx.Position{{ID: 3, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: price}}
			api.orders = protectedOrders(i, 1)
			if err = e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			if i.VerifiedPosition == nil {
				t.Fatal("valid exposure not remembered")
			}
			remembered := *i.VerifiedPosition
			switch name {
			case "missing stop":
				api.orders = api.orders[1:]
			case "changed stop":
				changed := 19995.0
				api.orders[0].StopPrice = &changed
			case "duplicate entry":
				copy := api.history[0]
				copy.ID = 88
				api.history = append(api.history, copy)
			case "entry mismatch":
				api.history[0].Size = 2
			case "position mismatch":
				api.positions[0].Type = 2
			case "extra position":
				copy := api.positions[0]
				copy.ID = 4
				api.positions = append(api.positions, copy)
			case "missing costs":
				i.Policy.PerTradeOnly = false
				api.trades = []projectx.Trade{{ID: 1}}
			case "daily limit":
				i.Policy.PerTradeOnly = false
				i.Policy.MaxDailyLoss = 1
				q.q.Price = 19900
			case "floor limit":
				i.Policy.PerTradeOnly = false
				i.Floor = api.accounts[0].Balance
			case "schedule":
				*now = i.Window.Flatten
			case "quote outage":
				i.Policy.PerTradeOnly = false
				q.err = errors.New("offline")
			case "read outage":
				api.readErr = errors.New("offline")
			case "flat snapshot":
				api.positions = nil
				z := 0.0
				api.trades = []projectx.Trade{{ID: 1, ContractID: "mnq", Size: 1, ProfitAndLoss: &z}}
				i.FlatSince = now.Add(-4 * time.Second)
			}
			_ = e.reconcile(context.Background(), i)
			if api.closes != 0 || api.cancels != 0 || i.EmergencyAttempted {
				t.Fatal("automatic management mutated exposure/protection", api.closes, api.cancels)
			}
			// Fresh API observations can disagree without erasing historical evidence.
			recovered, err := New(store, func(int64) (API, Quotes, error) { return api, q, nil })
			if err != nil {
				t.Fatal(err)
			}
			held := recovered.active()
			if held == nil || held.VerifiedPosition == nil || *held.VerifiedPosition != remembered {
				t.Fatal("restart lost established exposure")
			}
			if recovered.Status().Armed || recovered.Status().RunRequested {
				t.Fatal("restart restored execution authority")
			}
			if name == "missing stop" {
				api.readErr = nil
				if err = e.Flatten(context.Background()); err != nil {
					t.Fatal(err)
				}
				if api.closes != 1 || !i.EmergencyAttempted {
					t.Fatal("explicit operator action failed to flatten")
				}
				_ = e.reconcile(context.Background(), i)
				if api.closes != 1 {
					t.Fatal("emergency replayed")
				}
			}
		})
	}
}

func TestPauseDoesNotCancelEstablishedPartialEntryOrProtection(t *testing.T) {
	e, api, _, _, c, p, a, s, _ := fixture(t)
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
	parent := projectx.Order{ID: 77, AccountID: 42, ContractID: "mnq", CustomTag: i.Tag, Type: 1, Size: 2, FillVolume: 1, Status: 1}
	api.history = []projectx.Order{parent}
	api.orders = append(protectedOrders(i, 1), parent)
	api.positions = []projectx.Position{{ID: 3, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: 20000}}
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	e.Pause()
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if api.closes != 0 || api.cancels != 0 || len(api.orders) != 3 || i.VerifiedPosition == nil {
		t.Fatal("pause changed an established partial fill")
	}
	// The status snapshot must not allow callers to alter retained evidence.
	status := e.Status()
	status.Intents[0].VerifiedPosition.Size = 99
	if e.Status().Intents[0].VerifiedPosition.Size != 1 {
		t.Fatal("status exposed mutable position evidence")
	}
	// Broker closes naturally; fresh closing evidence and repeated flat observations finish the intent.
	api.history[0].FillVolume = 1
	api.history[0].Status = 3
	api.positions = nil
	api.orders = nil
	profit := 10.0
	api.trades = []projectx.Trade{{ID: 1, ContractID: "mnq", Size: 1, ProfitAndLoss: &profit}}
	i.FlatSince = e.now().Add(-4 * time.Second)
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.State != "closed" || api.closes != 0 || api.cancels != 0 {
		t.Fatal("natural broker exit did not reconcile without mutation")
	}
}
