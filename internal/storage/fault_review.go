package storage

/*
#include <sqlite3.h>
*/
import "C"
import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/yelaco/projectx-pilot/internal/lockreview"
)

// ResolveFaultLock atomically records acknowledgement fields only and removes
// exactly the observed lock. Existing execution authority/history is immutable.
func (s *Inbox) ResolveFaultLock(before ExecRow, after, reason, snapshot string) error {
	policy := lockreview.Classify(reason)
	hash, err := hex.DecodeString(snapshot)
	if !policy.Reviewable || err != nil || len(hash) != 32 {
		return errors.New("lock has no valid scoped review policy")
	}
	if before.State == "uncertain" || before.State == "submitting" || before.State == "risk-lockout" {
		return errors.New("unresolved/risk executions cannot be acknowledged away")
	}
	var old, next map[string]json.RawMessage
	if json.Unmarshal([]byte(before.Payload), &old) != nil || json.Unmarshal([]byte(after), &next) != nil {
		return errors.New("invalid execution review evidence")
	}
	var guard struct {
		ID, Session, State, HaltReason, LockReason, RiskLockReason string
		AccountID                                                  int64
		EmergencyAttempted                                         bool
	}
	if json.Unmarshal([]byte(before.Payload), &guard) != nil || guard.ID != before.ID || guard.Session != before.Session || guard.State != before.State || guard.AccountID <= 0 || strconv.FormatInt(guard.AccountID, 10) != before.Account || guard.RiskLockReason != "" || lockreview.IsRisk(guard.HaltReason) {
		return errors.New("matching non-risk execution evidence required")
	}
	terminal := before.State == "closed" || before.State == "rejected" || before.State == "cancelled"
	legacyTerminal := policy.TerminalOnly && terminal && guard.LockReason == "" && guard.HaltReason == ""
	if guard.LockReason != reason && guard.HaltReason != reason && !legacyTerminal {
		return errors.New("execution does not identify this exact lock")
	}
	if (policy.TerminalOnly || guard.EmergencyAttempted) && !terminal {
		return errors.New("terminal reconciliation required before review")
	}
	switch before.State {
	case "closed", "rejected", "cancelled", "attention-required", "protected", "partial", "accepted", "protective-failure":
	default:
		return errors.New("unsupported execution state requires reconciliation")
	}
	var active bool
	var at time.Time
	if json.Unmarshal(next["AcknowledgedLockActive"], &active) != nil || !active || json.Unmarshal(next["AcknowledgedLockAt"], &at) != nil || at.IsZero() {
		return errors.New("valid explicit acknowledgement required")
	}
	var ackReason, ackSnapshot string
	if json.Unmarshal(next["AcknowledgedLockReason"], &ackReason) != nil || json.Unmarshal(next["AcknowledgedLockSnapshot"], &ackSnapshot) != nil || ackReason != reason || ackSnapshot != snapshot || len(next["AcknowledgedLockAt"]) == 0 {
		return errors.New("matching explicit acknowledgement required")
	}
	for _, key := range []string{"AcknowledgedLockActive", "AcknowledgedLockReason", "AcknowledgedLockSnapshot", "AcknowledgedLockAt"} {
		delete(old, key)
		delete(next, key)
	}
	if !reflect.DeepEqual(old, next) {
		return errors.New("review cannot alter execution state, history, claims or emergency latches")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("execution store closed")
	}
	if err = s.executionSchema(); err != nil {
		return err
	}
	if err = s.exec(`CREATE TABLE IF NOT EXISTS execution_lock_reviews(id INTEGER PRIMARY KEY,account TEXT NOT NULL,session TEXT NOT NULL,intent TEXT NOT NULL,reason TEXT NOT NULL,snapshot TEXT NOT NULL,at TEXT NOT NULL)`); err != nil {
		return err
	}
	if err = s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer s.exec("ROLLBACK")
	if err = s.write(`DELETE FROM execution_locks WHERE account=? AND session=? AND reason=? AND EXISTS(SELECT 1 FROM execution_intents WHERE id=? AND account=? AND session=? AND state=? AND payload=?) AND NOT EXISTS(SELECT 1 FROM execution_intents WHERE account=? AND id<>? AND state NOT IN ('closed','rejected','cancelled'))`, before.Account, before.Session, reason, before.ID, before.Account, before.Session, before.State, before.Payload, before.Account, before.ID); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) != 1 {
		return errors.New("lock or execution changed, or another execution is unresolved; lock retained")
	}
	if err = s.write(`UPDATE execution_intents SET payload=? WHERE id=? AND account=? AND session=? AND state=? AND payload=?`, after, before.ID, before.Account, before.Session, before.State, before.Payload); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) != 1 {
		return errors.New("execution changed during acknowledgement")
	}
	if err = s.write(`INSERT INTO execution_lock_reviews(account,session,intent,reason,snapshot,at) VALUES(?,?,?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, before.Account, before.Session, before.ID, reason, snapshot); err != nil {
		return err
	}
	return s.exec("COMMIT")
}
