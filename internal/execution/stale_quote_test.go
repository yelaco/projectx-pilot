package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/yelaco/projectx-pilot/internal/projectx"
)

func TestProtectedExposureDoesNotFlattenOnQuoteLoss(t *testing.T) {
	for _, perTrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "per-trade"}[perTrade], func(t *testing.T) {
			e, api, q, store, c, p, a, s, _ := fixture(t)
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
			i.Policy.PerTradeOnly = perTrade
			price := 20000.0
			api.history = []projectx.Order{{ID: 77, AccountID: 42, ContractID: "mnq", CustomTag: i.Tag, Type: 2, Size: 1, FillVolume: 1, Status: 2, FilledPrice: &price}}
			api.positions = []projectx.Position{{ID: 3, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: price}}
			api.orders = protectedOrders(i, 1)
			q.err = errors.New("feed unavailable")
			if err = e.reconcile(context.Background(), i); err != nil {
				t.Fatal(err)
			}
			if i.EmergencyAttempted || api.closes != 0 || api.cancels != 0 || i.State != "protected" {
				t.Fatal("quote loss mutated protected exposure")
			}
			if reason, err := store.ExecutionLock("42", i.Session); err != nil || reason != "" {
				t.Fatal("quote loss created lock", reason, err)
			}
			if !perTrade && (e.Status().Armed || e.Status().RunRequested) {
				t.Fatal("legacy risk monitoring loss did not pause")
			}
			// Protection failures remain actionable even without market data.
			api.orders = api.orders[1:]
			_ = e.reconcile(context.Background(), i)
			if i.EmergencyAttempted || api.closes != 0 || !i.AttentionRequired {
				t.Fatal("missing protection must report attention without closing")
			}
		})
	}
}

func TestReviewStaleQuoteLockScopedAndReadOnly(t *testing.T) {
	e, api, store, i := legacyOCOFixture(t)
	i.HaltReason = staleQuoteLock
	i.HaltOrders = protectedOrders(i, 1)
	if err := e.persist(i); err != nil {
		t.Fatal(err)
	}
	if err := store.LockExecution("42", i.Session, staleQuoteLock); err != nil {
		t.Fatal(err)
	}
	if err := e.ReviewStaleQuoteLock(context.Background(), 42, i.ID); err != nil {
		t.Fatal(err)
	}
	if reason, _ := store.ExecutionLock("42", i.Session); reason != "" {
		t.Fatal("lock retained", reason)
	}
	if e.Status().Armed || e.Status().RunRequested || api.closes != 0 || api.cancels != 0 || len(api.requests) != 1 || !i.EmergencyAttempted {
		t.Fatal("review changed execution authority or sent mutations")
	}
}

func TestStaleQuoteReviewNeverClearsDailyLock(t *testing.T) {
	e, _, store, i := legacyOCOFixture(t)
	i.HaltReason = staleQuoteLock
	if err := store.LockExecution("42", i.Session, "daily loss limit reached"); err != nil {
		t.Fatal(err)
	}
	if err := e.ReviewStaleQuoteLock(context.Background(), 42, i.ID); err == nil {
		t.Fatal("daily lock review accepted")
	}
	if reason, _ := store.ExecutionLock("42", i.Session); reason != "daily loss limit reached" {
		t.Fatal("daily lock changed")
	}
}

func TestStaleQuoteReviewRejectsUnverifiedEvidence(t *testing.T) {
	for _, name := range []string{"wrong trigger", "unresolved", "exposed", "working orders", "no protection evidence", "wrong history", "invalid tick"} {
		t.Run(name, func(t *testing.T) {
			e, api, store, i := legacyOCOFixture(t)
			i.HaltReason = staleQuoteLock
			i.HaltOrders = protectedOrders(i, 1)
			if err := store.LockExecution("42", i.Session, staleQuoteLock); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "wrong trigger":
				i.HaltReason = "daily loss limit reached"
			case "unresolved":
				i.State = "uncertain"
			case "exposed":
				api.positions = []projectx.Position{{ID: 1, Size: 1}}
			case "working orders":
				api.orders = []projectx.Order{{ID: 1}}
			case "no protection evidence":
				i.HaltOrders = nil
			case "wrong history":
				api.history[0].CustomTag = "other"
			case "invalid tick":
				i.Contract.TickSize = 0
			}
			if err := e.ReviewStaleQuoteLock(context.Background(), 42, i.ID); err == nil {
				t.Fatal("unsafe review accepted")
			}
			if reason, _ := store.ExecutionLock("42", i.Session); reason != staleQuoteLock {
				t.Fatal("failed review changed lock")
			}
			if api.closes != 0 || api.cancels != 0 || len(api.requests) != 1 {
				t.Fatal("review mutated broker state")
			}
		})
	}
}
