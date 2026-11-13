// Package storage provides a narrow SQLite inbox using the system SQLite library.
package storage

/*
#cgo pkg-config: sqlite3
#include <stdlib.h>
#include <sqlite3.h>
static int bind_text_copy(sqlite3_stmt *stmt, int i, const char *v, int n) {
 return sqlite3_bind_text(stmt,i,v,n,SQLITE_TRANSIENT);
}
*/
import "C"

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
	"unsafe"

	"projectxbot/internal/signal"
)

var ErrDuplicate = errors.New("signal ID already exists; duplicate ignored")
var ErrTransition = errors.New("signal is not awaiting review, or does not exist")

type Record struct {
	Signal                                               signal.Signal
	ProfileID, ContractID, AccountID, Status, ReceivedAt string
}
type Inbox struct {
	now func() time.Time
	mu  sync.Mutex
	db  *C.sqlite3
}

func Open(path string) (*Inbox, error) {
	// The enclosing private state directory must already exist.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	s := &Inbox{now: time.Now}
	if C.sqlite3_open_v2(p, &s.db, C.SQLITE_OPEN_READWRITE|C.SQLITE_OPEN_FULLMUTEX, nil) != C.SQLITE_OK {
		if s.db != nil {
			C.sqlite3_close(s.db)
		}
		return nil, errors.New("could not open signal database")
	}
	C.sqlite3_busy_timeout(s.db, 500)
	if err = s.exec(`PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; CREATE TABLE IF NOT EXISTS activity(id INTEGER PRIMARY KEY, at TEXT NOT NULL, level TEXT NOT NULL, component TEXT NOT NULL, message TEXT NOT NULL); CREATE TABLE IF NOT EXISTS signals(id TEXT PRIMARY KEY, payload TEXT NOT NULL, profile TEXT NOT NULL, contract TEXT NOT NULL, account TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending_review','reviewed','rejected')), received TEXT NOT NULL); CREATE INDEX IF NOT EXISTS signals_by_profile_status ON signals(profile,status); CREATE TABLE IF NOT EXISTS signal_events(id INTEGER PRIMARY KEY, signal_id TEXT NOT NULL, event TEXT NOT NULL, at TEXT NOT NULL);`); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *Inbox) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	if C.sqlite3_close(s.db) != C.SQLITE_OK {
		return errors.New("could not close signal database")
	}
	s.db = nil
	return nil
}
func (s *Inbox) exec(sql string) error {
	p := C.CString(sql)
	defer C.free(unsafe.Pointer(p))
	if C.sqlite3_exec(s.db, p, nil, nil, nil) != C.SQLITE_OK {
		return errors.New("signal database operation failed")
	}
	return nil
}
func (s *Inbox) prepare(sql string, values ...string) (*C.sqlite3_stmt, error) {
	p := C.CString(sql)
	defer C.free(unsafe.Pointer(p))
	var stmt *C.sqlite3_stmt
	if C.sqlite3_prepare_v2(s.db, p, -1, &stmt, nil) != C.SQLITE_OK {
		return nil, errors.New("signal database query failed")
	}
	for i, v := range values {
		value := C.CString(v)
		rc := C.bind_text_copy(stmt, C.int(i+1), value, C.int(len(v)))
		C.free(unsafe.Pointer(value))
		if rc != C.SQLITE_OK {
			C.sqlite3_finalize(stmt)
			return nil, errors.New("signal database parameter failed")
		}
	}
	return stmt, nil
}
func (s *Inbox) write(sql string, values ...string) error {
	stmt, err := s.prepare(sql, values...)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	rc := C.sqlite3_step(stmt)
	if rc == C.SQLITE_CONSTRAINT {
		return ErrDuplicate
	}
	if rc != C.SQLITE_DONE {
		return errors.New("signal database write failed")
	}
	return nil
}
func (s *Inbox) Insert(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("signal database is closed")
	}
	b, err := json.Marshal(r.Signal)
	if err != nil {
		return errors.New("signal encoding failed")
	}
	if err = s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer s.exec("ROLLBACK")
	stmt, err := s.prepare("SELECT COUNT(*) FROM signals WHERE status='pending_review'")
	if err != nil {
		return err
	}
	rc := C.sqlite3_step(stmt)
	count := int(C.sqlite3_column_int(stmt, 0))
	C.sqlite3_finalize(stmt)
	if rc != C.SQLITE_ROW {
		return errors.New("could not check signal inbox capacity")
	}
	if count >= 1000 {
		return errors.New("signal inbox is full; review or reject pending signals first")
	}
	at := s.now().UTC().Format(time.RFC3339Nano)
	if err = s.write("INSERT INTO signals VALUES(?,?,?,?,?,'pending_review',?)", r.Signal.ID, string(b), r.ProfileID, r.ContractID, r.AccountID, at); err != nil {
		return err
	}
	if err = s.write("INSERT INTO signal_events(signal_id,event,at) VALUES(?,'imported',?)", r.Signal.ID, at); err != nil {
		return err
	}
	return s.exec("COMMIT")
}
func (s *Inbox) Review(id, status string) error {
	if status != "reviewed" && status != "rejected" {
		return ErrTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("signal database is closed")
	}
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer s.exec("ROLLBACK")
	if err := s.write("UPDATE signals SET status=? WHERE id=? AND status='pending_review'", status, id); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) != 1 {
		return ErrTransition
	}
	if err := s.write("INSERT INTO signal_events(signal_id,event,at) VALUES(?,?,?)", id, status, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return s.exec("COMMIT")
}
func (s *Inbox) Recent() ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("signal database is closed")
	}
	stmt, err := s.prepare("SELECT payload,profile,contract,account,status,received FROM signals ORDER BY (status='pending_review') DESC,rowid DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	var out []Record
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, errors.New("could not read signal inbox")
		}
		value := func(i int) string {
			return C.GoStringN((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, C.int(i)))), C.int(C.sqlite3_column_bytes(stmt, C.int(i))))
		}
		var r Record
		if json.Unmarshal([]byte(value(0)), &r.Signal) != nil {
			return nil, errors.New("invalid persisted signal")
		}
		r.ProfileID = value(1)
		r.ContractID = value(2)
		r.AccountID = value(3)
		r.Status = value(4)
		r.ReceivedAt = value(5)
		out = append(out, r)
	}
	return out, nil
}

var ErrNotFound = errors.New("signal not found")

func (s *Inbox) Get(id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Record
	if s.db == nil {
		return r, errors.New("signal database is closed")
	}
	stmt, err := s.prepare("SELECT payload,profile,contract,account,status,received FROM signals WHERE id=?", id)
	if err != nil {
		return r, err
	}
	defer C.sqlite3_finalize(stmt)
	rc := C.sqlite3_step(stmt)
	if rc == C.SQLITE_DONE {
		return r, ErrNotFound
	}
	if rc != C.SQLITE_ROW {
		return r, errors.New("signal lookup failed")
	}
	value := func(i int) string {
		return C.GoStringN((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, C.int(i)))), C.int(C.sqlite3_column_bytes(stmt, C.int(i))))
	}
	if json.Unmarshal([]byte(value(0)), &r.Signal) != nil {
		return r, errors.New("invalid persisted signal")
	}
	r.ProfileID = value(1)
	r.ContractID = value(2)
	r.AccountID = value(3)
	r.Status = value(4)
	r.ReceivedAt = value(5)
	return r, nil
}

// Activity is a bounded, sanitized operational event, never a request payload.
type Activity struct{ At, Level, Component, Message string }

func (s *Inbox) RecordActivity(level, component, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("activity store is closed")
	}
	if len(message) > 512 {
		message = message[:512]
	}
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer s.exec("ROLLBACK")
	if err := s.write("INSERT INTO activity(at,level,component,message) VALUES(?,?,?,?)", time.Now().UTC().Format(time.RFC3339Nano), level, component, message); err != nil {
		return err
	}
	if err := s.exec("DELETE FROM activity WHERE id NOT IN (SELECT id FROM activity ORDER BY id DESC LIMIT 2000)"); err != nil {
		return err
	}
	return s.exec("COMMIT")
}
func (s *Inbox) Activity(component ...string) ([]Activity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("activity store is closed")
	}
	query := "SELECT at,level,component,message FROM activity ORDER BY id DESC LIMIT 100"
	if len(component) > 1 {
		return nil, errors.New("only one activity component may be selected")
	}
	if len(component) == 1 {
		query = "SELECT at,level,component,message FROM activity WHERE component=? ORDER BY id DESC LIMIT 100"
	}
	stmt, err := s.prepare(query, component...)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	var out []Activity
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, errors.New("activity lookup failed")
		}
		value := func(i int) string {
			return C.GoStringN((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, C.int(i)))), C.int(C.sqlite3_column_bytes(stmt, C.int(i))))
		}
		out = append(out, Activity{value(0), value(1), value(2), value(3)})
	}
	return out, nil
}
func (s *Inbox) Counts() (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("inbox is closed")
	}
	stmt, err := s.prepare("SELECT status,COUNT(*) FROM signals GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	out := map[string]int{}
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, errors.New("counts unavailable")
		}
		key := C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, 0))))
		out[key] = int(C.sqlite3_column_int(stmt, 1))
	}
	return out, nil
}

// OpenWithClock allows deterministic receipt timestamps in offline integration tests.
func OpenWithClock(path string, clock func() time.Time) (*Inbox, error) {
	if clock == nil {
		return nil, errors.New("clock required")
	}
	s, err := Open(path)
	if err != nil {
		return nil, err
	}
	s.now = clock
	return s, nil
}
