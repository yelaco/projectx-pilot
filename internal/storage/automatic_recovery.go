package storage

/*
#include <sqlite3.h>
*/
import "C"
import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/yelaco/projectx-pilot/internal/lockreview"
)

// ResolveRecoveredExecutionLock removes only an exact resolved technical lock.
// Unlike acknowledgement, it requires closed history and never changes payloads.
func (s *Inbox) ResolveRecoveredExecutionLock(before ExecRow, reason string, first, last time.Time) error {
	var i struct {
		ID, Session, State, HaltReason, LockReason, RiskLockReason string
		AccountID                                                  int64
		EmergencyAttempted                                         bool
		ObservedOrders, ObservedPositions                          []json.RawMessage
		FlatSince, Updated                                         time.Time
	}
	if !lockreview.AutoRecoverable(reason) || json.Unmarshal([]byte(before.Payload), &i) != nil || i.ID != before.ID || i.Session != before.Session || i.State != "closed" || before.State != "closed" || i.AccountID <= 0 || strconv.FormatInt(i.AccountID, 10) != before.Account || i.LockReason != reason || !lockreview.AutoRecoverable(i.HaltReason) || i.RiskLockReason != "" || i.EmergencyAttempted || len(i.ObservedOrders) != 0 || len(i.ObservedPositions) != 0 || i.FlatSince.IsZero() || i.Updated.IsZero() || first.Before(i.Updated) || last.Sub(first) < 3*time.Second {
		return errors.New("resolved non-risk closed execution and fresh flat observations required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("execution store closed")
	}
	if err := s.executionSchema(); err != nil {
		return err
	}
	if err := s.exec(`CREATE TABLE IF NOT EXISTS execution_lock_recoveries(id INTEGER PRIMARY KEY,account TEXT NOT NULL,session TEXT NOT NULL,intent TEXT NOT NULL,reason TEXT NOT NULL,first_observed TEXT NOT NULL,last_observed TEXT NOT NULL,at TEXT NOT NULL)`); err != nil {
		return err
	}
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer s.exec("ROLLBACK")
	if err := s.write(`DELETE FROM execution_locks WHERE account=? AND session=? AND reason=? AND EXISTS(SELECT 1 FROM execution_intents WHERE id=? AND account=? AND session=? AND state=? AND payload=?) AND NOT EXISTS(SELECT 1 FROM execution_intents WHERE account=? AND state NOT IN ('closed','rejected','cancelled')) AND (SELECT id FROM execution_intents WHERE account=? ORDER BY rowid DESC LIMIT 1)=?`, before.Account, before.Session, reason, before.ID, before.Account, before.Session, before.State, before.Payload, before.Account, before.Account, before.ID); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) != 1 {
		return errors.New("lock or execution changed; recovery denied")
	}
	if err := s.write(`INSERT INTO execution_lock_recoveries(account,session,intent,reason,first_observed,last_observed,at) VALUES(?,?,?,?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, before.Account, before.Session, before.ID, reason, first.UTC().Format(time.RFC3339Nano), last.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return s.exec("COMMIT")
}
