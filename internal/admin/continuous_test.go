package admin

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/session"
	"github.com/yelaco/projectx-pilot/internal/signal"
)

type runReader struct{ accountReader }

func (runReader) Accounts(context.Context) ([]projectx.Account, error) {
	sim := true
	return []projectx.Account{{ID: 42, Name: "PRAC-test", CanTrade: true, Simulated: &sim, Balance: 50000}}, nil
}
func continuousFixture(t *testing.T, simple ...bool) (*Manager, *atomic.Int64, *flowAPI) {
	t.Helper()
	m, password, actor, _, _ := automaticFixture(t)
	var stamp atomic.Int64
	stamp.Store(time.Date(2026, 11, 13, 9, 0, 0, 0, session.Chicago).UnixNano())
	clock := func() time.Time { return time.Unix(0, stamp.Load()) }
	m.now = clock
	service := app.New(runReader{}, 0)
	m.Attach(service)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(simple) > 0 && simple[0] {
		p := m.Policies().TradeManagers[0]
		p.PerTradeOnly = true
		if err := m.SaveTradeManager(p); err != nil {
			t.Fatal(err)
		}
	}
	a := execution.Authorization{Floor: 48000, PracticeConfirmed: true, BracketsConfirmed: true, FloorConfirmed: true, Window: session.Window{Verified: true}}
	if len(simple) > 0 && simple[0] {
		a.Floor = 0
		a.FloorConfirmed = false
	}
	if err := m.SaveContinuousSetup(actor, password, "swift", m.ExecutionConfig(), true, a); err != nil {
		t.Fatal(err)
	}
	api := &flowAPI{}
	e, err := execution.NewWithClock(m.executionStore, func(int64) (execution.API, execution.Quotes, error) { return api, flowQuotes{clock}, nil }, clock)
	if err != nil {
		t.Fatal(err)
	}
	m.executor = e
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.RunExecution(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(time.Second)
	for {
		err = m.StartContinuousAutomation(context.Background(), "swift")
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "monitor is not running") || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	return m, &stamp, api
}
func TestContinuousStartCrossesSessionsAndWeekendsUntilPaused(t *testing.T) {
	m, clock, api := continuousFixture(t)
	if s := m.ExecutionStatus(); !s.RunRequested || !s.Armed {
		t.Fatal(s)
	}
	clock.Store(time.Date(2026, 11, 13, 15, 10, 0, 0, session.Chicago).UnixNano())
	if err := m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.ExecutionStatus(); !s.RunRequested || s.Armed || !strings.Contains(s.RunMessage, "market closed") {
		t.Fatal("continuous intent lost at daily close", s)
	}
	clock.Store(time.Date(2026, 11, 15, 17, 1, 0, 0, session.Chicago).UnixNano())
	if err := m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.ExecutionStatus(); !s.RunRequested || !s.Armed || s.ArmedUntil.In(session.Chicago).Format("2006-01-02") != "2026-11-16" {
		t.Fatal("next session did not resume", s)
	}
	// Reaching today's persisted daily limit must wait, not turn Start into a
	// recurring daily approval. The next verified session gets a new lock key.
	if err := m.executionStore.LockExecution("42", "2026-11-16", "session loss or trade limit reached"); err != nil {
		t.Fatal(err)
	}
	if err := m.executor.ExecuteAutomation(context.Background(), "swift", signal.Signal{Timestamp: time.Unix(0, clock.Load())}, "mnq", 2); err == nil {
		t.Fatal("daily lock ignored")
	}
	if err := m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.ExecutionStatus(); !s.RunRequested || s.Armed || !strings.Contains(s.RunMessage, "session risk limit") {
		t.Fatal(s)
	}
	clock.Store(time.Date(2026, 11, 17, 9, 0, 0, 0, session.Chicago).UnixNano())
	if err := m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !m.ExecutionStatus().Armed {
		t.Fatal("new session did not restore readiness")
	}
	if err := m.PauseProfileAutomation("swift"); err != nil {
		t.Fatal(err)
	}
	clock.Store(time.Date(2026, 11, 18, 9, 0, 0, 0, session.Chicago).UnixNano())
	if err := m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.ExecutionStatus(); s.RunRequested || s.Armed {
		t.Fatal("manual pause silently resumed", s)
	}
	if api.count() != 0 {
		t.Fatal("start/session transitions placed an order without a signal")
	}
}
func TestContinuousRunWaitsForUnknownCalendarAndDoesNotRestoreAfterRestart(t *testing.T) {
	m, clock, _ := continuousFixture(t)
	clock.Store(time.Date(2027, 1, 4, 9, 0, 0, 0, session.Chicago).UnixNano())
	if err := m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := m.ExecutionStatus(); !s.RunRequested || s.Armed || !strings.Contains(s.RunMessage, "calendar verification") {
		t.Fatal("unknown calendar allowed entries", s)
	}
	next, err := execution.New(m.executionStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := next.Status(); s.RunRequested || s.Armed {
		t.Fatal("restart restored live authorization", s)
	}
}
func TestContinuousScopeChangeStopsRequestedRun(t *testing.T) {
	m, _, _ := continuousFixture(t)
	// The fixture's setup password is deliberately not read back from disk. A
	// scope change uses the normal policy mutation path, which must stop the run.
	profile := m.Policies().Automations[0]
	profile.Name = "Edited profile"
	if err := m.SaveAutomation(profile); err != nil {
		t.Fatal(err)
	}
	if s := m.ExecutionStatus(); s.RunRequested || s.Armed {
		t.Fatal("configuration edit did not pause continuous mode")
	}
	if err := m.StartContinuousAutomation(context.Background(), "swift"); err == nil {
		t.Fatal("old setup scope reused after edit")
	}
}

func TestContinuousPerTradeOnlyStartsWithoutFloor(t *testing.T) {
	m, _, _ := continuousFixture(t, true)
	if m.ExecutionConfig().RunSetup.Floor != 0 {
		t.Fatal("invented an account floor")
	}
	if s := m.ExecutionStatus(); !s.RunRequested || !s.Armed {
		t.Fatal(s)
	}
	m.PauseExecution()
}

type ticketReader struct{ runReader }

func (ticketReader) AvailableContracts(context.Context) ([]projectx.Contract, error) {
	return []projectx.Contract{{ID: "mnq", Name: "MNQ", SymbolID: "F.US.MNQ", ActiveContract: true, TickSize: .25, TickValue: .5}}, nil
}
func TestPracticeTicketNeedsNoAutomationProfile(t *testing.T) {
	m, _, api := continuousFixture(t, true)
	m.PauseExecution()
	service := app.New(ticketReader{}, 0)
	m.Attach(service)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	window, _ := session.NormalWindow("2026-11-13")
	window.Verified = true
	auth := execution.Authorization{Window: window, AccountName: "PRAC-test", PracticeConfirmed: true, BracketsConfirmed: true}
	m.mu.Lock()
	err := m.armExecutionLocked(context.Background(), auth)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = m.DeleteAutomation("swift"); err != nil {
		t.Fatal(err)
	}
	// Deleting a profile pauses entries; rearm the independent API test session.
	m.mu.Lock()
	err = m.armExecutionLocked(context.Background(), auth)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = m.PreviewTicket(context.Background(), "mnq", "BUY", 2, 1, 40, 80, 0); err != nil {
		t.Fatal(err)
	}
	st := m.ExecutionStatus()
	if st.Preview == nil || st.Preview.Plan.Quantity != 1 || st.Automated || st.RunRequested || api.count() != 0 {
		t.Fatal("preview submitted or enabled remote automatic mode", st)
	}
	if len(m.Policies().Automations) != 0 {
		t.Fatal("test created a hidden automation profile")
	}
}
