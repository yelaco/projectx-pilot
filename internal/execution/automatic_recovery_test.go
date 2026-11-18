package execution

import (
	"context"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

func resolvedRecoveryFixture(t *testing.T) (*Engine, *mockAPI, *storage.Inbox, *Intent, *time.Time) {
	t.Helper()
	e, api, store, i, now := fillLagFixture(t)
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Second)
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if !e.Status().RecoveryPending {
		t.Fatal("technical fault discarded Start")
	}
	api.history[0].FillVolume = 1
	api.history[0].Status = 2
	api.positions = nil
	api.orders = nil
	profit := 0.0
	cost := 0.0
	api.trades = []projectx.Trade{{ID: 99, ContractID: "mnq", Size: 1, ProfitAndLoss: &profit, Fees: &cost, Commissions: &cost}}
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(3 * time.Second)
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.State != "closed" {
		t.Fatal("fixture did not reconcile closure", i.State)
	}
	e.quotes.(*mockQuotes).q.At = *now
	return e, api, store, i, now
}
func TestAutomaticRecoveryRetainsStartButRequiresFlatConfirmationAndFreshArm(t *testing.T) {
	e, api, store, i, now := resolvedRecoveryFixture(t)
	reason := i.HaltReason
	ready, err := e.ResumeAfterRecovery(context.Background(), "swift", i.Session)
	if err != nil || ready || !e.Status().RecoveryPending || e.Status().Armed {
		t.Fatal("first observation restored authority", ready, err)
	}
	*now = now.Add(3 * time.Second)
	e.quotes.(*mockQuotes).q.At = *now
	ready, err = e.ResumeAfterRecovery(context.Background(), "swift", i.Session)
	if err != nil || !ready || !e.Status().RunRequested || e.Status().Armed {
		t.Fatal("resolved fault failed to restore only running intent", ready, err)
	}
	if why, _ := store.ExecutionLock("42", i.Session); why != "" || i.HaltReason != reason || i.LockReason != reason {
		t.Fatal("recovery lost historical trigger or retained current lock")
	}
	authorization := e.auth
	authorization.Continuous = true
	if err := e.Arm(context.Background(), e.config, e.policy, authorization); err != nil {
		t.Fatal(err)
	}
	if !e.Status().Armed {
		t.Fatal("fresh arming failed")
	}
	assertNoExtraMutations(t, api)
}
func TestAutomaticRecoveryCannotOverrideOperatorOrAuthorityChanges(t *testing.T) {
	for _, mode := range []string{"manual pause", "restart", "wrong profile", "cancelled context", "old execution", "risk lock", "ambiguous original", "emergency latch", "active exposure"} {
		t.Run(mode, func(t *testing.T) {
			e, api, store, i, now := resolvedRecoveryFixture(t)
			if ready, err := e.ResumeAfterRecovery(context.Background(), "swift", i.Session); err != nil || ready {
				t.Fatal(ready, err)
			}
			*now = now.Add(3 * time.Second)
			e.quotes.(*mockQuotes).q.At = *now
			profile := "swift"
			ctx := context.Background()
			switch mode {
			case "manual pause":
				e.Pause()
			case "restart":
				recovered, err := New(store, e.factory)
				if err != nil {
					t.Fatal(err)
				}
				recovered.ctx = ctx
				recovered.now = e.now
				e = recovered
			case "wrong profile":
				profile = "other"
			case "cancelled context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "old execution":
				i.Created = e.runStartedAt.Add(-time.Second)
				if err := e.persist(i); err != nil {
					t.Fatal(err)
				}
			case "risk lock":
				if err := store.LockExecution("42", i.Session, "daily loss limit reached"); err != nil {
					t.Fatal(err)
				}
			case "ambiguous original":
				i.HaltReason = "entry cancellation uncertain"
				if err := e.persist(i); err != nil {
					t.Fatal(err)
				}
			case "emergency latch":
				i.EmergencyAttempted = true
				if err := e.persist(i); err != nil {
					t.Fatal(err)
				}
			case "active exposure":
				api.positions = []projectx.Position{{ID: 200, ContractID: "mnq", Size: 1, Type: 1}}
			}
			ready, err := e.ResumeAfterRecovery(ctx, profile, i.Session)
			if err == nil || ready || e.Status().Armed {
				t.Fatal("unsafe auto recovery", mode, ready, err)
			}
			if why, _ := store.ExecutionLock("42", i.Session); why == "" {
				t.Fatal("unsafe recovery deleted lock", mode)
			}
			assertNoExtraMutations(t, api)
		})
	}
}
func TestAutomaticRecoveryReadFailureResetsObservationPair(t *testing.T) {
	e, api, store, i, now := resolvedRecoveryFixture(t)
	if ready, err := e.ResumeAfterRecovery(context.Background(), "swift", i.Session); err != nil || ready {
		t.Fatal(ready, err)
	}
	*now = now.Add(3 * time.Second)
	e.quotes.(*mockQuotes).q.At = *now
	api.readErr = context.DeadlineExceeded
	if ready, err := e.ResumeAfterRecovery(context.Background(), "swift", i.Session); err == nil || ready {
		t.Fatal("read failure accepted")
	}
	api.readErr = nil
	if ready, err := e.ResumeAfterRecovery(context.Background(), "swift", i.Session); err != nil || ready {
		t.Fatal("failed read did not reset observation pair", ready, err)
	}
	if why, _ := store.ExecutionLock("42", i.Session); why == "" {
		t.Fatal("first fresh observation released lock")
	}
	assertNoExtraMutations(t, api)
}

func TestAutomaticRecoveryRequiresRecoveredLegacyAccounting(t *testing.T) {
	e, api, store, i, _ := resolvedRecoveryFixture(t)
	api.trades[0].Fees = nil
	if ready, err := e.ResumeAfterRecovery(context.Background(), "swift", i.Session); err == nil || ready {
		t.Fatal("missing accounting was treated as recovered")
	}
	if why, _ := store.ExecutionLock("42", i.Session); why == "" {
		t.Fatal("unverified accounting allowed technical lock release")
	}
	assertNoExtraMutations(t, api)
}
