package execution

import (
	"context"
	"projectxbot/internal/projectx"
	"projectxbot/internal/storage"
	"strings"
	"testing"
)

func legacyOCOFixture(t *testing.T) (*Engine, *mockAPI, *storage.Inbox, *Intent) {
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
	i.State = "closed"
	i.HaltKind = "fault"
	i.EmergencyAttempted = true
	if err = e.persist(i); err != nil {
		t.Fatal(err)
	}
	if err = store.LockExecution("42", i.Session, legacyOCOLock); err != nil {
		t.Fatal(err)
	}
	e.Pause()
	price := 20000.0
	children := protectedOrders(i, 1)
	children[0].LinkedOrderID = nil
	for n := range children {
		children[n].AccountID = 42
		children[n].Status = 3
	}
	api.history = append([]projectx.Order{{ID: 77, AccountID: 42, CustomTag: i.Tag, ContractID: "mnq", Type: 2, Side: 0, Size: 1, FillVolume: 1, Status: 2, FilledPrice: &price}}, children...)
	return e, api, store, i
}
func TestReviewKnownOCOLockRequiresFreshProofAndNeverArmsOrWritesAPI(t *testing.T) {
	e, api, store, i := legacyOCOFixture(t)
	if err := e.ReviewOneWayOCOLock(context.Background(), 42, i.ID); err != nil {
		t.Fatal(err)
	}
	if why, _ := store.ExecutionLock("42", i.Session); why != "" {
		t.Fatal("lock not cleared")
	}
	if st := e.Status(); st.Armed || st.RunRequested {
		t.Fatal("review armed execution")
	}
	if len(api.requests) != 1 || api.closes != 0 || api.cancels != 0 {
		t.Fatal("review made trading requests")
	}
	if i.State != "closed" || !i.EmergencyAttempted {
		t.Fatal("review altered terminal state/latch")
	}
}
func TestReviewDeniesUnrelatedOrUnverifiedStates(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Engine, *mockAPI, *storage.Inbox, *Intent)
	}{
		{"wrong lock", func(_ *Engine, _ *mockAPI, s *storage.Inbox, i *Intent) {
			if err := s.LockExecution("42", i.Session, "daily loss limit reached"); err != nil {
				t.Fatal(err)
			}
		}},
		{"unresolved", func(_ *Engine, _ *mockAPI, _ *storage.Inbox, i *Intent) { i.State = "uncertain" }},
		{"position", func(_ *Engine, a *mockAPI, _ *storage.Inbox, _ *Intent) {
			a.positions = []projectx.Position{{ID: 1, Size: 1}}
		}},
		{"working order", func(_ *Engine, a *mockAPI, _ *storage.Inbox, _ *Intent) { a.orders = []projectx.Order{{ID: 1}} }},
		{"wrong tag", func(_ *Engine, a *mockAPI, _ *storage.Inbox, _ *Intent) { a.history[0].CustomTag = "other" }},
		{"wrong bracket price", func(_ *Engine, a *mockAPI, _ *storage.Inbox, _ *Intent) {
			price := 19900.0
			a.history[1].StopPrice = &price
		}},
		{"no OCO", func(_ *Engine, a *mockAPI, _ *storage.Inbox, _ *Intent) { a.history[2].LinkedOrderID = nil }},
		{"broker cannot trade", func(_ *Engine, a *mockAPI, _ *storage.Inbox, _ *Intent) { a.accounts[0].CanTrade = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, api, s, i := legacyOCOFixture(t)
			tc.edit(e, api, s, i)
			if err := e.ReviewOneWayOCOLock(context.Background(), 42, i.ID); err == nil {
				t.Fatal("unsafe review accepted")
			}
			if reason, _ := s.ExecutionLock("42", i.Session); reason == "" {
				t.Fatal("failed review cleared lock")
			}
			if len(api.requests) != 1 || api.closes != 0 || api.cancels != 0 {
				t.Fatal("review mutated broker")
			}
		})
	}
}

type changingRecoveryAPI struct {
	*mockAPI
	reads int
}

func (a *changingRecoveryAPI) OpenOrders(context.Context, int64) ([]projectx.Order, error) {
	a.reads++
	if a.reads >= 2 {
		return []projectx.Order{{ID: 999}}, nil
	}
	return nil, nil
}
func TestReviewChecksFlatnessAgainBeforeClearing(t *testing.T) {
	e, api, s, i := legacyOCOFixture(t)
	changing := &changingRecoveryAPI{mockAPI: api}
	e.api = changing
	err := e.ReviewOneWayOCOLock(context.Background(), 42, i.ID)
	if err == nil || !strings.Contains(err.Error(), "not flat") || changing.reads != 2 {
		t.Fatal("second observation skipped", err)
	}
	if reason, _ := s.ExecutionLock("42", i.Session); reason == "" {
		t.Fatal("cleared after exposure changed")
	}
}
func TestReviewCancellationRetainsLock(t *testing.T) {
	e, _, s, i := legacyOCOFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.ReviewOneWayOCOLock(ctx, 42, i.ID); err == nil {
		t.Fatal("cancelled review accepted")
	}
	if reason, _ := s.ExecutionLock("42", i.Session); reason == "" {
		t.Fatal("cancelled review erased lock")
	}
}
