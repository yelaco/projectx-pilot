package storage

/*
#include <sqlite3.h>
*/
import "C"
import (
	"errors"
	"time"
	"unsafe"
)

// AutomationReceipt permanently consumes a delivery before any trading attempt.
// Its ID matches the signal/intent ID, so a crash cannot authorize a replay.
type AutomationReceipt struct{ ID, ProfileID, AccountID, ContractID, State, Message, UpdatedAt string }

func (s *Inbox) automationSchema() error {
	return s.exec(`CREATE TABLE IF NOT EXISTS automation_receipts(id TEXT PRIMARY KEY,profile TEXT NOT NULL,account TEXT NOT NULL,contract TEXT NOT NULL,state TEXT NOT NULL,message TEXT NOT NULL,updated TEXT NOT NULL);`)
}
func (s *Inbox) ClaimAutomation(r AutomationReceipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("execution store closed")
	}
	if err := s.automationSchema(); err != nil {
		return err
	}
	return s.write("INSERT INTO automation_receipts(id,profile,account,contract,state,message,updated) VALUES(?,?,?,?,'processing','',?)", r.ID, r.ProfileID, r.AccountID, r.ContractID, time.Now().UTC().Format(time.RFC3339Nano))
}
func (s *Inbox) FinishAutomation(id, state, message string) error {
	switch state {
	case "submitted", "uncertain", "blocked", "interrupted":
	default:
		return errors.New("invalid automation result")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("execution store closed")
	}
	if err := s.write("UPDATE automation_receipts SET state=?,message=?,updated=? WHERE id=?", state, message, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Inbox) AutomationReceipts() ([]AutomationReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("execution store closed")
	}
	if err := s.automationSchema(); err != nil {
		return nil, err
	}
	stmt, err := s.prepare("SELECT id,profile,account,contract,state,message,updated FROM automation_receipts ORDER BY rowid DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	var rows []AutomationReceipt
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, errors.New("automation receipts unavailable")
		}
		v := func(i int) string {
			return C.GoStringN((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, C.int(i)))), C.int(C.sqlite3_column_bytes(stmt, C.int(i))))
		}
		rows = append(rows, AutomationReceipt{v(0), v(1), v(2), v(3), v(4), v(5), v(6)})
	}
	return rows, nil
}
func (s *Inbox) AutomationReceipt(id string) (AutomationReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return AutomationReceipt{}, errors.New("execution store closed")
	}
	if err := s.automationSchema(); err != nil {
		return AutomationReceipt{}, err
	}
	stmt, err := s.prepare("SELECT id,profile,account,contract,state,message,updated FROM automation_receipts WHERE id=?", id)
	if err != nil {
		return AutomationReceipt{}, err
	}
	defer C.sqlite3_finalize(stmt)
	rc := C.sqlite3_step(stmt)
	if rc == C.SQLITE_DONE {
		return AutomationReceipt{}, ErrNotFound
	}
	if rc != C.SQLITE_ROW {
		return AutomationReceipt{}, errors.New("automation receipt lookup failed")
	}
	v := func(i int) string {
		return C.GoStringN((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, C.int(i)))), C.int(C.sqlite3_column_bytes(stmt, C.int(i))))
	}
	return AutomationReceipt{v(0), v(1), v(2), v(3), v(4), v(5), v(6)}, nil
}

// PendingSince returns a bounded FIFO batch. Exact Go time comparisons avoid
// rounding/formatting ambiguity and prevent old/foreign-profile rows starving
// fresh eligible alerts. The inbox enforces at most 1000 pending records.
func (s *Inbox) PendingSince(since time.Time, profile ...string) ([]Record, error) {
	if len(profile) > 1 {
		return nil, errors.New("at most one profile filter is supported")
	}
	selected := ""
	if len(profile) == 1 {
		selected = profile[0]
	}
	ids, err := s.pendingIDs(since, selected)
	if err != nil {
		return nil, err
	}
	rows := make([]Record, 0, len(ids))
	for _, id := range ids {
		r, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		if r.Status == "pending_review" {
			rows = append(rows, r)
		}
	}
	return rows, nil
}
func (s *Inbox) pendingIDs(since time.Time, profile string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("signal store closed")
	}
	query := "SELECT id,received FROM signals WHERE status='pending_review' ORDER BY rowid LIMIT 1000"
	var values []string
	if profile != "" {
		query = "SELECT id,received FROM signals WHERE status='pending_review' AND profile=? ORDER BY rowid LIMIT 1000"
		values = []string{profile}
	}
	stmt, err := s.prepare(query, values...)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	var ids []string
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, errors.New("pending signals unavailable")
		}
		value := func(i int) string {
			return C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, C.int(i)))))
		}
		received, err := time.Parse(time.RFC3339Nano, value(1))
		if err != nil {
			return nil, errors.New("invalid persisted receipt timestamp")
		}
		if received.Before(since) {
			continue
		}
		ids = append(ids, value(0))
		if len(ids) == 20 {
			break
		}
	}
	return ids, nil
}
