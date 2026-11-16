package storage

/*
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"
import (
	"encoding/hex"
	"errors"
	"fmt"
	"unsafe"
)

// ExecRow contains an immutable identity and engine-owned JSON state.
type ExecRow struct{ ID, Account, Session, State, Payload string }

func (s *Inbox) executionSchema() error {
	return s.exec(`CREATE TABLE IF NOT EXISTS execution_intents(id TEXT PRIMARY KEY,account TEXT NOT NULL,session TEXT NOT NULL,state TEXT NOT NULL,payload TEXT NOT NULL); CREATE UNIQUE INDEX IF NOT EXISTS one_active_execution ON execution_intents(account) WHERE state NOT IN ('closed','rejected','cancelled'); DROP INDEX IF EXISTS one_active_bot_execution; CREATE TABLE IF NOT EXISTS execution_locks(account TEXT NOT NULL,session TEXT NOT NULL,reason TEXT NOT NULL,PRIMARY KEY(account,session));`)
}
func (s *Inbox) ReserveExecution(r ExecRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("execution store closed")
	}
	if err := s.executionSchema(); err != nil {
		return err
	}
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer s.exec("ROLLBACK")
	if err := s.write("INSERT INTO execution_intents(id,account,session,state,payload) VALUES(?,?,?,?,?)", r.ID, r.Account, r.Session, r.State, r.Payload); err != nil {
		return err
	}
	return s.exec("COMMIT")
}
func (s *Inbox) SaveExecution(r ExecRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("execution store closed")
	}
	if err := s.write("UPDATE execution_intents SET state=?,payload=? WHERE id=? AND account=? AND session=?", r.State, r.Payload, r.ID, r.Account, r.Session); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) != 1 {
		return errors.New("execution record missing")
	}
	return nil
}
func (s *Inbox) Executions() ([]ExecRow, error) { return s.executionRows("") }
func (s *Inbox) executionRows(account string) ([]ExecRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("execution store closed")
	}
	if err := s.executionSchema(); err != nil {
		return nil, err
	}
	query := "SELECT id,account,session,state,payload FROM execution_intents"
	if account != "" {
		query += " WHERE account=?"
	}
	stmt, err := s.prepare(query + " ORDER BY rowid DESC")
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	if account != "" {
		value := C.CString(account)
		C.sqlite3_bind_text(stmt, 1, value, -1, (*[0]byte)(C.SQLITE_TRANSIENT))
		C.free(unsafe.Pointer(value))
	}
	var rows []ExecRow
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, errors.New("execution read failed")
		}
		value := func(i int) string {
			return C.GoStringN((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, C.int(i)))), C.int(C.sqlite3_column_bytes(stmt, C.int(i))))
		}
		rows = append(rows, ExecRow{value(0), value(1), value(2), value(3), value(4)})
	}
	return rows, nil
}
func (s *Inbox) LockExecution(account, session, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("execution store closed")
	}
	if err := s.executionSchema(); err != nil {
		return err
	}
	return s.write(`INSERT INTO execution_locks(account,session,reason) VALUES(?,?,?) ON CONFLICT(account,session) DO UPDATE SET reason=CASE WHEN execution_locks.reason IN ('daily loss limit reached','session loss or trade limit reached','liquidation buffer reached') THEN execution_locks.reason ELSE excluded.reason END`, account, session, reason)
}
func (s *Inbox) ExecutionLock(account, session string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return "", errors.New("execution store closed")
	}
	if err := s.executionSchema(); err != nil {
		return "", err
	}
	stmt, err := s.prepare("SELECT reason FROM execution_locks WHERE account=? AND session=?", account, session)
	if err != nil {
		return "", err
	}
	defer C.sqlite3_finalize(stmt)
	rc := C.sqlite3_step(stmt)
	if rc == C.SQLITE_DONE {
		return "", nil
	}
	if rc != C.SQLITE_ROW {
		return "", errors.New("execution lock lookup failed")
	}
	return C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, 0)))), nil
}

// ResolveOneWayOCOLock atomically records a scoped repair and removes only the
// legacy checker lock. No execution row, signal claim or other lock is changed.
func (s *Inbox) ResolveOneWayOCOLock(account, session, intent, expected string) error {
	return s.resolveReviewedLock(account, session, intent, expected, "protective orders missing, pending or inconsistent")
}

// ResolveStaleQuoteLock atomically audits and removes only the exact reviewed
// stale-quote lock, retaining execution history, claims and other account locks.
func (s *Inbox) ResolveStaleQuoteLock(account, session, intent, expected string) error {
	return s.resolveReviewedLock(account, session, intent, expected, "market data stale while managing exposure")
}

// ResolveConfirmedStaleQuoteLock records the acknowledged current exposure without
// changing any execution, broker order, placement claim or emergency latch.
func (s *Inbox) ResolveConfirmedStaleQuoteLock(account, session, intent, expected, snapshot string, positions, orders int) error {
	hash, err := hex.DecodeString(snapshot)
	if err != nil || len(hash) != 32 || positions < 0 || orders < 0 || positions+orders == 0 {
		return errors.New("valid confirmed account snapshot required")
	}
	note := fmt.Sprintf("stale-quote historical verification; operator confirmed %d positions and %d working orders; two matching fresh observations; snapshot %s", positions, orders, snapshot)
	return s.resolveReviewedLock(account, session, intent, expected, "market data stale while managing exposure", note)
}
func (s *Inbox) resolveReviewedLock(account, session, intent, expected, allowed string, auditNote ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("execution store closed")
	}
	if expected != allowed {
		return errors.New("lock reason does not match this scoped review")
	}
	if err := s.executionSchema(); err != nil {
		return err
	}
	if err := s.exec(`CREATE TABLE IF NOT EXISTS execution_recoveries(intent TEXT PRIMARY KEY,account TEXT NOT NULL,session TEXT NOT NULL,reason TEXT NOT NULL,at TEXT NOT NULL)`); err != nil {
		return err
	}
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer s.exec("ROLLBACK")
	if err := s.write(`DELETE FROM execution_locks WHERE account=? AND session=? AND reason=? AND EXISTS(SELECT 1 FROM execution_intents WHERE id=? AND account=? AND session=? AND state='closed') AND NOT EXISTS(SELECT 1 FROM execution_intents WHERE account=? AND state NOT IN ('closed','rejected','cancelled'))`, account, session, expected, intent, account, session, account); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) != 1 {
		return errors.New("lock changed, execution unresolved, or scoped closed intent missing; no lock was cleared")
	}
	note := expected + ": historical verification and two fresh flat observations"
	if len(auditNote) == 1 {
		note = auditNote[0]
	}
	if err := s.write(`INSERT INTO execution_recoveries(intent,account,session,reason,at) VALUES(?,?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, intent, account, session, note); err != nil {
		return err
	}
	return s.exec("COMMIT")
}
