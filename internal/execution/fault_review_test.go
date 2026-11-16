package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

func activeFaultFixture(t *testing.T) (*Engine, *mockAPI, *storage.Inbox, *Intent) {
	t.Helper()
	e, api, _, store, c, p, a, s, _ := fixture(t)
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
	api.history = []projectx.Order{{ID: 77, AccountID: 42, ContractID: "mnq", CustomTag: i.Tag, Type: 2, Size: 1, FillVolume: 1, Status: 2, FilledPrice: &price}}
	api.positions = []projectx.Position{{ID: 100, AccountID: 42, ContractID: "mnq", Type: 1, Size: 1, AveragePrice: price}}
	api.orders = protectedOrders(i, 1)
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	api.orders = api.orders[1:]
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	return e, api, store, i
}
func TestActiveProtectionFaultCanBeConfirmedWithoutEndingOrMutatingPosition(t *testing.T) {
	e, api, store, i := activeFaultFixture(t)
	err := e.ReviewFaultLock(context.Background(), 42, i.ID, "")
	var prompt *StaleQuoteConfirmationRequired
	if !errors.As(err, &prompt) || prompt.Reason == "" {
		t.Fatal("generic lock did not prompt", err)
	}
	reason := i.LockReason
	if err = e.ReviewFaultLock(context.Background(), 42, i.ID, prompt.Token); err != nil {
		t.Fatal(err)
	}
	if why, _ := store.ExecutionLock("42", i.Session); why != "" {
		t.Fatal("acknowledged fault lock retained", why)
	}
	if terminal(i.State) || e.active() == nil || !i.AcknowledgedLockActive || i.HaltReason != reason || api.closes != 0 || api.cancels != 0 || len(api.requests) != 1 || i.EmergencyAttempted {
		t.Fatal("review changed execution history/authority or broker state")
	}
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if why, _ := store.ExecutionLock("42", i.Session); why != "" {
		t.Fatal("same acknowledged observation immediately relocked", why)
	}
	recovered, err := New(store, e.factory)
	if err != nil {
		t.Fatal(err)
	}
	recovered.now = e.now
	recovered.ctx = context.Background()
	_ = recovered.connect(42)
	if err = recovered.reconcile(context.Background(), recovered.active()); err != nil {
		t.Fatal(err)
	}
	if why, _ := store.ExecutionLock("42", i.Session); why != "" {
		t.Fatal("restart lost exact acknowledgement", why)
	}
	// A changed account snapshot is a fresh fault, not covered by the old acknowledgement.
	changed := 20019.0
	api.orders[0].LimitPrice = &changed
	_ = recovered.reconcile(context.Background(), recovered.active())
	if why, _ := store.ExecutionLock("42", i.Session); why == "" {
		t.Fatal("changed exposure inherited old confirmation")
	}
	if recovered.Status().Armed || recovered.Status().RunRequested || api.closes != 0 || api.cancels != 0 {
		t.Fatal("review/restart restored authority or mutated broker")
	}
}
func TestFaultReviewCannotWaiveRiskOrUnresolvedExecution(t *testing.T) {
	for _, mode := range []string{"daily", "floor", "uncertain", "active emergency", "changed identity", "unknown", "legacy OCO", "first risk"} {
		t.Run(mode, func(t *testing.T) {
			e, api, store, i := activeFaultFixture(t)
			switch mode {
			case "daily":
				_ = store.LockExecution("42", i.Session, "daily loss limit reached")
			case "floor":
				_ = store.LockExecution("42", i.Session, "liquidation buffer reached")
			case "uncertain":
				i.State = "uncertain"
				_ = e.persist(i)
			case "active emergency":
				i.EmergencyAttempted = true
				_ = e.persist(i)
			case "changed identity":
				api.history[0].CustomTag = "different"
			case "unknown":
				i.LockReason = "unknown"
				i.HaltReason = "unknown"
				_ = store.LockExecution("42", i.Session, "unknown")
			case "legacy OCO":
				_ = store.LockExecution("42", i.Session, legacyOCOLock)
			case "first risk":
				i.HaltReason = "daily loss limit reached"
				_ = e.persist(i)
			}
			err := e.ReviewFaultLock(context.Background(), 42, i.ID, "")
			var prompt *StaleQuoteConfirmationRequired
			if err == nil || errors.As(err, &prompt) {
				t.Fatal("unsafe case offered confirmation", mode, err)
			}
			if why, _ := store.ExecutionLock("42", i.Session); why == "" {
				t.Fatal("unsafe case cleared lock")
			}
			if api.closes != 0 || api.cancels != 0 {
				t.Fatal("review mutated broker state")
			}
		})
	}
}
func TestRiskLocksSurviveLaterTechnicalFaults(t *testing.T) {
	e, _, store, i := activeFaultFixture(t)
	_ = e.requireAttentionWithKind(i, "daily loss limit reached", "daily_limit")
	_ = e.requireAttention(i, "stop or target coverage does not match filled exposure")
	if reason, _ := store.ExecutionLock("42", i.Session); reason != "daily loss limit reached" {
		t.Fatal("technical display overwrote durable risk lock", reason)
	}
	if i.RiskLockReason != "daily loss limit reached" {
		t.Fatal("risk evidence not retained")
	}
}

func TestFaultAcknowledgementExpiresWhenConditionRecovers(t *testing.T) {
	e, api, store, i := activeFaultFixture(t)
	err := e.ReviewFaultLock(context.Background(), 42, i.ID, "")
	var prompt *StaleQuoteConfirmationRequired
	if !errors.As(err, &prompt) {
		t.Fatal(err)
	}
	if err = e.ReviewFaultLock(context.Background(), 42, i.ID, prompt.Token); err != nil {
		t.Fatal(err)
	}
	api.orders = protectedOrders(i, 1)
	if err = e.reconcile(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if i.AcknowledgedLockActive {
		t.Fatal("acknowledgement remained active after normal monitoring recovered")
	}
	api.orders = api.orders[1:]
	_ = e.reconcile(context.Background(), i)
	if why, _ := store.ExecutionLock("42", i.Session); why == "" {
		t.Fatal("new incident reused an old acknowledgement")
	}
}
func TestGeneralFaultConfirmationCannotOverrideChangedScope(t *testing.T) {
	for _, mode := range []string{"token", "expiry", "account", "snapshot", "reason", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			e, api, store, i := activeFaultFixture(t)
			err := e.ReviewFaultLock(context.Background(), 42, i.ID, "")
			var prompt *StaleQuoteConfirmationRequired
			if !errors.As(err, &prompt) {
				t.Fatal(err)
			}
			token := prompt.Token
			account := int64(42)
			switch mode {
			case "token":
				token = "forged"
			case "expiry":
				e.faultLockReview.Expires = e.now()
			case "account":
				account = 43
			case "snapshot":
				api.positions[0].Size = 2
			case "reason":
				_ = store.LockExecution("42", i.Session, "session costs or P&L cannot be verified")
			case "uncertain":
				i.State = "uncertain"
				_ = e.persist(i)
			}
			err = e.ReviewFaultLock(context.Background(), account, i.ID, token)
			if err == nil {
				t.Fatal("changed confirmation scope accepted")
			}
			if why, _ := store.ExecutionLock("42", i.Session); why == "" {
				t.Fatal("changed scope cleared lock")
			}
			if i.AcknowledgedLockActive || api.closes != 0 || api.cancels != 0 {
				t.Fatal("invalid confirmation changed execution")
			}
		})
	}
}
func TestRiskLockInsertedDuringReviewWinsAtomically(t *testing.T) {
	e, api, store, i := activeFaultFixture(t)
	err := e.ReviewFaultLock(context.Background(), 42, i.ID, "")
	var prompt *StaleQuoteConfirmationRequired
	if !errors.As(err, &prompt) {
		t.Fatal(err)
	}
	e.api = &changingReviewAPI{API: api, change: func() {
		if err := store.LockExecution("42", i.Session, "daily loss limit reached"); err != nil {
			t.Fatal(err)
		}
	}}
	if err = e.ReviewFaultLock(context.Background(), 42, i.ID, prompt.Token); err == nil {
		t.Fatal("confirmation cleared a raced risk lock")
	}
	if reason, _ := store.ExecutionLock("42", i.Session); reason != "daily loss limit reached" {
		t.Fatal("risk lock lost", reason)
	}
}

func TestLegacyResolvedSubmissionLockWithMissingReasonFieldsHasReviewPath(t *testing.T) {
	e, _, store, i := legacyOCOFixture(t)
	i.HaltReason = ""
	i.LockReason = ""
	if err := e.persist(i); err != nil {
		t.Fatal(err)
	}
	if err := store.LockExecution("42", i.Session, "submission did not confirm acceptance; reconciliation required"); err != nil {
		t.Fatal(err)
	}
	err := e.ReviewFaultLock(context.Background(), 42, i.ID, "")
	var prompt *StaleQuoteConfirmationRequired
	if !errors.As(err, &prompt) {
		t.Fatal("resolved historical submission has no review path", err)
	}
	if err = e.ReviewFaultLock(context.Background(), 42, i.ID, prompt.Token); err != nil {
		t.Fatal(err)
	}
	if reason, _ := store.ExecutionLock("42", i.Session); reason != "" {
		t.Fatal("resolved legacy lock retained", reason)
	}
	if i.State != "closed" || !i.EmergencyAttempted || e.Status().Armed {
		t.Fatal("legacy review altered state or authority")
	}
}
