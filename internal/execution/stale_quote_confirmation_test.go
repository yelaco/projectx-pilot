package execution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

func staleReviewFixture(t *testing.T) (*Engine, *mockAPI, *storage.Inbox, *Intent) {
	t.Helper()
	e, api, store, i := legacyOCOFixture(t)
	i.HaltReason = staleQuoteLock
	i.HaltOrders = protectedOrders(i, 1)
	if err := e.persist(i); err != nil {
		t.Fatal(err)
	}
	if err := store.LockExecution("42", i.Session, staleQuoteLock); err != nil {
		t.Fatal(err)
	}
	return e, api, store, i
}
func promptFor(t *testing.T, e *Engine, i *Intent) *StaleQuoteConfirmationRequired {
	t.Helper()
	err := e.ReviewStaleQuoteLock(context.Background(), 42, i.ID)
	var prompt *StaleQuoteConfirmationRequired
	if !errors.As(err, &prompt) || prompt.Token == "" {
		t.Fatal("expected scoped exposure confirmation", err)
	}
	return prompt
}
func TestStaleQuoteOpenExposureRequiresConfirmationAndLeavesAccountUntouched(t *testing.T) {
	for _, mode := range []string{"position", "orders"} {
		t.Run(mode, func(t *testing.T) {
			e, api, store, i := staleReviewFixture(t)
			if mode == "position" {
				api.positions = []projectx.Position{{ID: 100, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: 20000}}
			} else {
				api.orders = protectedOrders(i, 1)
			}
			before := reviewSnapshot(api.positions, api.orders)
			prompt := promptFor(t, e, i)
			if reason, _ := store.ExecutionLock("42", i.Session); reason != staleQuoteLock {
				t.Fatal("prompt cleared lock")
			}
			if err := e.ReviewStaleQuoteLock(context.Background(), 42, i.ID, prompt.Token); err != nil {
				t.Fatal(err)
			}
			if reason, _ := store.ExecutionLock("42", i.Session); reason != "" {
				t.Fatal("confirmed lock not cleared")
			}
			if reviewSnapshot(api.positions, api.orders) != before || api.closes != 0 || api.cancels != 0 || len(api.requests) != 1 {
				t.Fatal("review changed broker state")
			}
			if e.Status().Armed || e.Status().RunRequested || !i.EmergencyAttempted || i.State != "closed" {
				t.Fatal("review changed authority/history")
			}
			if err := e.ReviewStaleQuoteLock(context.Background(), 42, i.ID, prompt.Token); err == nil {
				t.Fatal("confirmation replay accepted")
			}
			// Clearing this historical lock does not grant entry permission with exposure.
			if err := e.Arm(context.Background(), e.config, e.policy, e.auth); err == nil || !strings.Contains(err.Error(), "flat with no working orders") {
				t.Fatal("open account armed after lock review", err)
			}
		})
	}
}
func TestStaleQuoteConfirmationCannotOverrideChangedOrExpiredScope(t *testing.T) {
	for _, mode := range []string{"forged", "expired", "changed exposure", "new daily lock", "unresolved", "wrong account", "flat now", "restart"} {
		t.Run(mode, func(t *testing.T) {
			e, api, store, i := staleReviewFixture(t)
			api.positions = []projectx.Position{{ID: 100, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: 20000}}
			prompt := promptFor(t, e, i)
			token := prompt.Token
			account := int64(42)
			switch mode {
			case "forged":
				token = "forged"
			case "expired":
				e.staleQuoteReview.Expires = e.now().Add(-time.Second)
			case "changed exposure":
				api.positions[0].Size = 2
			case "new daily lock":
				if err := store.LockExecution("42", i.Session, "daily loss limit reached"); err != nil {
					t.Fatal(err)
				}
			case "unresolved":
				i.State = "uncertain"
			case "wrong account":
				account = 43
			case "flat now":
				api.positions = nil
			case "restart":
				e.staleQuoteReview = nil
			}
			if err := e.ReviewStaleQuoteLock(context.Background(), account, i.ID, token); err == nil {
				t.Fatal("changed/expired scope accepted")
			}
			expected := staleQuoteLock
			if mode == "new daily lock" {
				expected = "daily loss limit reached"
			}
			if reason, _ := store.ExecutionLock("42", i.Session); reason != expected {
				t.Fatal("lock changed without valid confirmation", reason)
			}
			if api.closes != 0 || api.cancels != 0 || len(api.requests) != 1 {
				t.Fatal("invalid confirmation mutated account")
			}
		})
	}
}

type changingReviewAPI struct {
	API
	reads  int
	change func()
}

func (a *changingReviewAPI) OpenPositions(ctx context.Context, account int64) ([]projectx.Position, error) {
	a.reads++
	if a.reads == 2 {
		a.change()
	}
	return a.API.OpenPositions(ctx, account)
}
func TestStaleQuoteConfirmationRechecksExposureBeforeAtomicClear(t *testing.T) {
	e, api, store, i := staleReviewFixture(t)
	api.positions = []projectx.Position{{ID: 100, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: 20000}}
	prompt := promptFor(t, e, i)
	e.api = &changingReviewAPI{API: api, change: func() { api.positions[0].Size = 2 }}
	err := e.ReviewStaleQuoteLock(context.Background(), 42, i.ID, prompt.Token)
	var next *StaleQuoteConfirmationRequired
	if !errors.As(err, &next) || next.Token == prompt.Token || next.Positions[0].Size != 2 {
		t.Fatal("changed exposure did not require renewed confirmation", err)
	}
	if reason, _ := store.ExecutionLock("42", i.Session); reason != staleQuoteLock {
		t.Fatal("lock cleared over changing exposure")
	}
	if api.closes != 0 || api.cancels != 0 {
		t.Fatal("review sent mutations")
	}
}
