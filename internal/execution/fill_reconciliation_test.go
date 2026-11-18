package execution

import (
	"context"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

func fillLagFixture(t *testing.T) (*Engine, *mockAPI, *storage.Inbox, *Intent, *time.Time) {
	t.Helper()
	e, api, _, store, c, p, a, s, now := fixture(t)
	c.Automated = true
	c.AutomationProfileID = "swift"
	c.AutomationContractID = "mnq"
	c.MaxQuantity = 1
	a.AutomationConfirmed = true
	if err := e.Arm(context.Background(), c, p, a); err != nil {
		t.Fatal(err)
	}
	if err := e.RequestRun("swift"); err != nil {
		t.Fatal(err)
	}
	if err := e.ExecuteAutomation(context.Background(), "swift", s, "mnq", 2); err != nil {
		t.Fatal(err)
	}
	i := e.active()
	api.history = []projectx.Order{{ID: 77, AccountID: 42, ContractID: "mnq", CustomTag: i.Tag, Type: 2, Size: 1, FillVolume: 0, Status: 1}}
	api.positions = []projectx.Position{{ID: 100, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: 20000}}
	api.orders = protectedOrders(i, 1)
	return e, api, store, i, now
}
func assertNoExtraMutations(t *testing.T, api *mockAPI) {
	t.Helper()
	if api.closes != 0 || api.cancels != 0 || len(api.requests) != 1 {
		t.Fatalf("unexpected broker mutation: close=%d cancel=%d placements=%d", api.closes, api.cancels, len(api.requests))
	}
}
func TestTransientFillHistoryLagDoesNotPauseRunningAutomation(t *testing.T) {
	e, api, store, i, now := fillLagFixture(t)
	e.monitorInterval = func() time.Duration { return 10 * time.Second }
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.State != "reconciling" || i.EntryFillPendingSince.IsZero() || e.protectionCheck == "verified" || i.AttentionRequired || !e.Status().Armed || !e.Status().RunRequested {
		t.Fatalf("lag was treated as a persistent failure: %+v", e.Status())
	}
	if why, err := store.ExecutionLock("42", i.Session); err != nil || why != "" {
		t.Fatalf("premature lock: %q %v", why, err)
	}
	if e.monitorDelayLocked() != 2*time.Second {
		t.Fatal("long monitor interval delayed confirmation")
	}
	if err := e.check(); err == nil {
		t.Fatal("pending reconciliation allowed another entry")
	}
	*now = now.Add(2 * time.Second)
	api.history[0].FillVolume = 1
	api.history[0].Status = 2
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.State != "protected" || !i.EntryFillPendingSince.IsZero() || i.HaltReason != "" || i.ObservedEntryFillVolume != 1 || !e.Status().Armed || !e.Status().RunRequested {
		t.Fatalf("recovered fill lag paused automation: %+v", e.Status())
	}
	if why, _ := store.ExecutionLock("42", i.Session); why != "" {
		t.Fatal("recovery created lock", why)
	}
	assertNoExtraMutations(t, api)
}
func TestPersistentFillDiscrepancyLocksAfterFreshConfirmation(t *testing.T) {
	e, api, store, i, now := fillLagFixture(t)
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.AttentionRequired {
		t.Fatal("rapid wake confirmed discrepancy too soon")
	}
	*now = now.Add(time.Second)
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	why, err := store.ExecutionLock("42", i.Session)
	if err != nil || why != "position exposure differs from observed entry fills" || !i.AttentionRequired || e.Status().Armed || e.Status().RunRequested {
		t.Fatalf("confirmed discrepancy not locked: %q %v", why, err)
	}
	// Catching up later must never automatically erase a retained fault lock.
	api.history[0].FillVolume = 1
	api.history[0].Status = 2
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if retained, _ := store.ExecutionLock("42", i.Session); retained != why || i.HaltReason != why || e.Status().RunRequested {
		t.Fatal("confirmed lock or first trigger erased")
	}
	assertNoExtraMutations(t, api)
}
func TestFillLagNeverDefersKnownExposureOrProtectionFaults(t *testing.T) {
	for _, kind := range []string{"wrong contract", "wrong direction", "excess quantity", "missing stop", "invalid fill quantity"} {
		t.Run(kind, func(t *testing.T) {
			e, api, store, i, _ := fillLagFixture(t)
			switch kind {
			case "wrong contract":
				api.positions[0].ContractID = "other"
			case "wrong direction":
				api.positions[0].Type = 2
			case "excess quantity":
				api.positions[0].Size = 2
			case "missing stop":
				api.orders = api.orders[1:]
			case "invalid fill quantity":
				api.history[0].FillVolume = -1
			}
			if err := e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			if why, _ := store.ExecutionLock("42", i.Session); why == "" || !i.AttentionRequired || e.Status().Armed || !i.EntryFillPendingSince.IsZero() {
				t.Fatalf("known fault was deferred: %s", kind)
			}
			assertNoExtraMutations(t, api)
		})
	}
}
func TestPendingFillEvidenceSurvivesRestartWithoutArming(t *testing.T) {
	e, api, store, i, now := fillLagFixture(t)
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(store, e.factory)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status().Armed || recovered.Status().RunRequested || recovered.active() == nil || recovered.active().EntryFillPendingSince.IsZero() {
		t.Fatal("restart lost reconciliation evidence or restored authority")
	}
	recovered.ctx = context.Background()
	recovered.now = e.now
	if err := recovered.connect(42); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Second)
	if err := recovered.reconcile(context.Background(), recovered.active()); err != nil {
		t.Fatal(err)
	}
	if why, _ := store.ExecutionLock("42", i.Session); why == "" {
		t.Fatal("restart reset confirmation window")
	}
	assertNoExtraMutations(t, api)
}

func TestFillHistoryRegressionPreservesEarlierVerifiedExposure(t *testing.T) {
	e, api, store, i, now := fillLagFixture(t)
	api.history[0].FillVolume = 1
	api.history[0].Status = 2
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	verifiedAt := i.VerifiedAt
	*now = now.Add(time.Second)
	api.history[0].FillVolume = 0
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.VerifiedPosition == nil || !i.VerifiedAt.Equal(verifiedAt) || e.protectionCheck == "verified" || !e.Status().RunRequested {
		t.Fatal("pending reconciliation lost history or represented it as fresh protection")
	}
	*now = now.Add(2 * time.Second)
	api.history[0].FillVolume = 1
	if err := e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if why, _ := store.ExecutionLock("42", i.Session); why != "" || i.State != "protected" || !e.Status().RunRequested {
		t.Fatal("temporary history regression created a persistent pause")
	}
	assertNoExtraMutations(t, api)
}
func TestFillLagDoesNotWeakenRiskLockOrDelayNewProtectionFault(t *testing.T) {
	for _, riskLock := range []bool{false, true} {
		t.Run(map[bool]string{false: "protection changes during pending", true: "existing risk lock"}[riskLock], func(t *testing.T) {
			e, api, store, i, _ := fillLagFixture(t)
			if err := e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			if riskLock {
				if err := e.requireAttentionWithKind(i, "daily loss limit reached", "daily_limit"); err != nil {
					t.Fatal(err)
				}
			} else {
				api.orders = api.orders[1:]
			}
			if err := e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			reason, err := store.ExecutionLock("42", i.Session)
			if err != nil || reason == "" || !i.AttentionRequired || e.Status().RunRequested {
				t.Fatal("fault hidden by reconciliation window", reason, err)
			}
			if riskLock && (reason != "daily loss limit reached" || i.HaltReason != "daily loss limit reached") {
				t.Fatal("risk lock/original trigger overwritten", reason)
			}
			assertNoExtraMutations(t, api)
		})
	}
}
