package storage

import (
	"errors"
	"fmt"
	"github.com/yelaco/projectx-pilot/internal/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutionReservationAndLockoutSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "executions.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := ExecRow{ID: "signal", Account: "42", Session: "2026-11-13", State: "submitting", Payload: "intent"}
	if err = s.ReserveExecution(r); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveExecution(r); err == nil {
		t.Fatal("duplicate intent")
	}
	other := r
	other.ID = "other"
	if err = s.ReserveExecution(other); err == nil {
		t.Fatal("overlapping account reservation")
	}
	if err = s.LockExecution("42", r.Session, "daily loss"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.Executions()
	if err != nil || len(rows) != 1 || rows[0].State != "submitting" {
		t.Fatal(rows, err)
	}
	if reason, err := s.ExecutionLock("42", r.Session); err != nil || reason != "daily loss" {
		t.Fatal(reason, err)
	}
	if reason, err := s.ExecutionLock("42", "2026-11-16"); err != nil || reason != "" {
		t.Fatal("new session inherited lock", reason, err)
	}
	r.State = "closed"
	if err = s.SaveExecution(r); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveExecution(other); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentReservation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "exec.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var successes atomic.Int32
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.ReserveExecution(ExecRow{ID: "same", Account: "42", Session: "session", State: "submitting", Payload: "intent"}) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal(successes.Load())
	}
}

func TestAutomationClaimsAndFIFODoNotReplayOrStarve(t *testing.T) {
	base := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	now := base
	s, err := OpenWithClock(filepath.Join(t.TempDir(), "alerts.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for n := 0; n < 30; n++ {
		if err = s.Insert(Record{Signal: signal.Signal{ID: fmt.Sprintf("foreign-%d", n)}, ProfileID: "other"}); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 30; n++ {
		if err = s.Insert(Record{Signal: signal.Signal{ID: fmt.Sprintf("old-%d", n)}, ProfileID: "swift"}); err != nil {
			t.Fatal(err)
		}
	}
	now = base.Add(time.Second)
	if err = s.Insert(Record{Signal: signal.Signal{ID: "fresh"}, ProfileID: "swift"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.PendingSince(base.Add(time.Nanosecond), "swift")
	if err != nil || len(rows) != 1 || rows[0].Signal.ID != "fresh" {
		t.Fatal("foreign/old alerts starved fresh alert", rows, err)
	}
	receipt := AutomationReceipt{ID: "fresh", ProfileID: "swift", AccountID: "42", ContractID: "mnq"}
	if err = s.ClaimAutomation(receipt); err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimAutomation(receipt); !errors.Is(err, ErrDuplicate) {
		t.Fatal("claim replay allowed")
	}
	if err = s.FinishAutomation("fresh", "submitted", "acknowledged"); err != nil {
		t.Fatal(err)
	}
	got, err := s.AutomationReceipt("fresh")
	if err != nil || got.State != "submitted" {
		t.Fatal(got, err)
	}
}

func TestOCOReviewClearsOnlyMatchingClosedFaultAndKeepsClaims(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const reason = "protective orders missing, pending or inconsistent"
	row := ExecRow{ID: "old-intent", Account: "42", Session: "2026-11-13", State: "closed", Payload: "original-history"}
	if err = s.ReserveExecution(row); err != nil {
		t.Fatal(err)
	}
	if err = s.LockExecution("42", row.Session, reason); err != nil {
		t.Fatal(err)
	}
	if err = s.LockExecution("43", row.Session, "daily loss limit reached"); err != nil {
		t.Fatal(err)
	}
	if err = s.ResolveOneWayOCOLock("43", row.Session, row.ID, reason); err == nil {
		t.Fatal("wrong account unlocked")
	}
	if err = s.ResolveOneWayOCOLock("42", row.Session, row.ID, "daily loss limit reached"); err == nil {
		t.Fatal("loss lock can be reviewed")
	}
	active := ExecRow{ID: "active", Account: "42", Session: row.Session, State: "uncertain", Payload: "unresolved"}
	if err = s.ReserveExecution(active); err != nil {
		t.Fatal(err)
	}
	if err = s.ResolveOneWayOCOLock("42", row.Session, row.ID, reason); err == nil {
		t.Fatal("unlocked over an unresolved intent on the same account")
	}
	if got, _ := s.ExecutionLock("42", row.Session); got != reason {
		t.Fatal("failed review erased lock")
	}
	active.State = "rejected"
	if err = s.SaveExecution(active); err != nil {
		t.Fatal(err)
	}
	if err = s.ResolveOneWayOCOLock("42", row.Session, row.ID, reason); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ExecutionLock("42", row.Session); got != "" {
		t.Fatal("review did not clear exact lock")
	}
	if got, _ := s.ExecutionLock("43", row.Session); got != "daily loss limit reached" {
		t.Fatal("another account's loss lock changed")
	}
	if err = s.ReserveExecution(row); err == nil {
		t.Fatal("review erased idempotency claim")
	}
	rows, err := s.Executions()
	if err != nil || len(rows) != 2 || rows[1].Payload != "original-history" {
		t.Fatal("execution history changed")
	}
	if err = s.ResolveOneWayOCOLock("42", row.Session, row.ID, reason); err == nil {
		t.Fatal("review replayed")
	}
}

func TestStaleQuoteReviewClearsOnlyMatchingClosedFaultAndKeepsClaims(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const reason = "market data stale while managing exposure"
	row := ExecRow{ID: "old-intent", Account: "42", Session: "2026-11-13", State: "closed", Payload: "original-history"}
	if err = s.ReserveExecution(row); err != nil {
		t.Fatal(err)
	}
	if err = s.LockExecution("42", row.Session, reason); err != nil {
		t.Fatal(err)
	}
	if err = s.LockExecution("43", row.Session, "daily loss limit reached"); err != nil {
		t.Fatal(err)
	}
	if err = s.ResolveStaleQuoteLock("43", row.Session, row.ID, reason); err == nil {
		t.Fatal("wrong account unlocked")
	}
	if err = s.ResolveStaleQuoteLock("42", row.Session, row.ID, "daily loss limit reached"); err == nil {
		t.Fatal("loss lock can be reviewed")
	}
	active := ExecRow{ID: "active", Account: "42", Session: row.Session, State: "uncertain", Payload: "unresolved"}
	if err = s.ReserveExecution(active); err != nil {
		t.Fatal(err)
	}
	if err = s.ResolveStaleQuoteLock("42", row.Session, row.ID, reason); err == nil {
		t.Fatal("unlocked over an unresolved intent on the same account")
	}
	if got, _ := s.ExecutionLock("42", row.Session); got != reason {
		t.Fatal("failed review erased lock")
	}
	active.State = "rejected"
	if err = s.SaveExecution(active); err != nil {
		t.Fatal(err)
	}
	if err = s.ResolveStaleQuoteLock("42", row.Session, row.ID, reason); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ExecutionLock("42", row.Session); got != "" {
		t.Fatal("review did not clear exact lock")
	}
	if got, _ := s.ExecutionLock("43", row.Session); got != "daily loss limit reached" {
		t.Fatal("another account's loss lock changed")
	}
	if err = s.ReserveExecution(row); err == nil {
		t.Fatal("review erased idempotency claim")
	}
	rows, err := s.Executions()
	if err != nil || len(rows) != 2 || rows[1].Payload != "original-history" {
		t.Fatal("execution history changed")
	}
	if err = s.ResolveStaleQuoteLock("42", row.Session, row.ID, reason); err == nil {
		t.Fatal("review replayed")
	}
}

func TestConfirmedStaleQuoteReviewIsScopedAndPreservesExecution(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	row := ExecRow{ID: "reviewed-closed", Account: "42", Session: "2026-11-13", State: "closed", Payload: "unchanged emergency latch and history"}
	if err = s.ReserveExecution(row); err != nil {
		t.Fatal(err)
	}
	const reason = "market data stale while managing exposure"
	if err = s.LockExecution("42", row.Session, reason); err != nil {
		t.Fatal(err)
	}
	snapshot := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err = s.ResolveConfirmedStaleQuoteLock("42", row.Session, row.ID, "daily loss limit reached", snapshot, 1, 2); err == nil {
		t.Fatal("confirmed daily lock override accepted")
	}
	if err = s.ResolveConfirmedStaleQuoteLock("42", row.Session, row.ID, reason, "invalid", 1, 2); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
	active := ExecRow{ID: "uncertain", Account: "42", Session: row.Session, State: "uncertain", Payload: "unresolved"}
	if err = s.ReserveExecution(active); err != nil {
		t.Fatal(err)
	}
	if err = s.ResolveConfirmedStaleQuoteLock("42", row.Session, row.ID, reason, snapshot, 1, 2); err == nil {
		t.Fatal("confirmation bypassed unresolved local intent")
	}
	active.State = "rejected"
	if err = s.SaveExecution(active); err != nil {
		t.Fatal(err)
	}
	if err = s.ResolveConfirmedStaleQuoteLock("42", row.Session, row.ID, reason, snapshot, 1, 2); err != nil {
		t.Fatal(err)
	}
	if lock, _ := s.ExecutionLock("42", row.Session); lock != "" {
		t.Fatal("confirmed exact lock retained")
	}
	rows, err := s.Executions()
	if err != nil || len(rows) != 2 {
		t.Fatal("execution history changed", err)
	}
	found := false
	for _, got := range rows {
		if got.ID == row.ID {
			found = true
			if got.Payload != row.Payload || got.State != "closed" {
				t.Fatal("review rewrote execution")
			}
		}
	}
	if !found {
		t.Fatal("closed execution removed")
	}
	if err = s.ResolveConfirmedStaleQuoteLock("42", row.Session, row.ID, reason, snapshot, 1, 2); err == nil {
		t.Fatal("confirmation replayed")
	}
}
