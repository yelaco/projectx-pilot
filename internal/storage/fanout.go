package storage

/*
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"
import (
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unsafe"
)

type TargetJob struct {
	RoutingMode                                                                     string `json:",omitempty"`
	ID, DeliveryID, ProfileID, ContractID                                           string
	AccountID                                                                       int64
	State, Message, Policy                                                          string
	ReceivedAt, ClaimedAt, DispatchedAt, ChecksAt, SubmitAt, AcceptedAt, FinishedAt time.Time
}

func (s *Inbox) fanoutSchema() error {
	return s.exec(`CREATE TABLE IF NOT EXISTS target_jobs(id TEXT PRIMARY KEY,delivery TEXT NOT NULL,profile TEXT NOT NULL,account INTEGER NOT NULL,state TEXT NOT NULL,payload TEXT NOT NULL,UNIQUE(delivery,account));CREATE TABLE IF NOT EXISTS fleet_controls(id INTEGER PRIMARY KEY CHECK(id=1),paused INTEGER NOT NULL);INSERT OR IGNORE INTO fleet_controls VALUES(1,0);`)
}

// ClaimTargets reserves the whole target set or none. Existing deliveries are
// never expanded, replayed or silently retargeted after a configuration change.
func (s *Inbox) ClaimTargets(jobs []TargetJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("execution store closed")
	}
	if len(jobs) < 1 || len(jobs) > 20 {
		return errors.New("target count must be 1..20")
	}
	if err := s.fanoutSchema(); err != nil {
		return err
	}
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer s.exec("ROLLBACK")
	delivery := jobs[0].DeliveryID
	seen := map[int64]bool{}
	stmt, err := s.prepare("SELECT 1 FROM target_jobs WHERE delivery=? LIMIT 1")
	if err != nil {
		return err
	}
	text := C.CString(delivery)
	C.sqlite3_bind_text(stmt, 1, text, -1, (*[0]byte)(C.SQLITE_TRANSIENT))
	C.free(unsafe.Pointer(text))
	rc := C.sqlite3_step(stmt)
	C.sqlite3_finalize(stmt)
	if rc == C.SQLITE_ROW {
		return ErrDuplicate
	}
	if rc != C.SQLITE_DONE {
		return errors.New("target claim lookup failed")
	}
	for _, j := range jobs {
		if j.ID == "" || j.DeliveryID != delivery || j.ProfileID != jobs[0].ProfileID || j.AccountID <= 0 || seen[j.AccountID] {
			return errors.New("invalid fanout identity")
		}
		seen[j.AccountID] = true
		j.State = "claimed"
		b, err := json.Marshal(j)
		if err != nil {
			return err
		}
		if err := s.write("INSERT INTO target_jobs(id,delivery,profile,account,state,payload) VALUES(?,?,?,?,?,?)", j.ID, j.DeliveryID, j.ProfileID, strconv.FormatInt(j.AccountID, 10), j.State, string(b)); err != nil {
			return err
		}
	}
	return s.exec("COMMIT")
}
func (s *Inbox) SaveTarget(j TargetJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("execution store closed")
	}
	switch j.State {
	case "submitted", "uncertain", "blocked", "interrupted":
	default:
		return errors.New("invalid target outcome")
	}
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if err := s.write("UPDATE target_jobs SET state=?,payload=? WHERE id=? AND delivery=? AND account=?", j.State, string(b), j.ID, j.DeliveryID, strconv.FormatInt(j.AccountID, 10)); err != nil {
		return err
	}
	if C.sqlite3_changes(s.db) != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Inbox) targetJobs(query, arg string) ([]TargetJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("execution store closed")
	}
	if err := s.fanoutSchema(); err != nil {
		return nil, err
	}
	stmt, err := s.prepare(query)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	if arg != "" {
		v := C.CString(arg)
		C.sqlite3_bind_text(stmt, 1, v, -1, (*[0]byte)(C.SQLITE_TRANSIENT))
		C.free(unsafe.Pointer(v))
	}
	out := []TargetJob{}
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			return out, nil
		}
		if rc != C.SQLITE_ROW {
			return nil, errors.New("target result read failed")
		}
		raw := C.GoStringN((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, 0))), C.int(C.sqlite3_column_bytes(stmt, 0)))
		var j TargetJob
		if json.Unmarshal([]byte(raw), &j) != nil {
			return nil, errors.New("invalid target result")
		}
		out = append(out, j)
	}
}
func (s *Inbox) TargetJobs() ([]TargetJob, error) {
	return s.targetJobs("SELECT payload FROM target_jobs ORDER BY rowid DESC LIMIT 1000", "")
}
func (s *Inbox) UnfinishedTargets() ([]TargetJob, error) {
	return s.targetJobs("SELECT payload FROM target_jobs WHERE state='claimed'", "")
}
func (s *Inbox) DeliveryTargets(id string) ([]TargetJob, error) {
	return s.targetJobs("SELECT payload FROM target_jobs WHERE delivery=? ORDER BY account", id)
}
func (s *Inbox) FleetPaused() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fanoutSchema(); err != nil {
		return true, err
	}
	stmt, err := s.prepare("SELECT paused FROM fleet_controls WHERE id=1")
	if err != nil {
		return true, err
	}
	defer C.sqlite3_finalize(stmt)
	if C.sqlite3_step(stmt) != C.SQLITE_ROW {
		return true, errors.New("global pause state unavailable")
	}
	return C.sqlite3_column_int(stmt, 0) != 0, nil
}
func (s *Inbox) SetFleetPaused(paused bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fanoutSchema(); err != nil {
		return err
	}
	v := 0
	if paused {
		v = 1
	}
	return s.exec("UPDATE fleet_controls SET paused=" + strconv.Itoa(v) + " WHERE id=1")
}

func milliseconds(a, b time.Time) float64 {
	if a.IsZero() || b.IsZero() {
		return 0
	}
	return float64(b.Sub(a)) / float64(time.Millisecond)
}
func (j TargetJob) DispatchMS() float64   { return milliseconds(j.ReceivedAt, j.DispatchedAt) }
func (j TargetJob) ChecksMS() float64     { return milliseconds(j.ChecksAt, j.SubmitAt) }
func (j TargetJob) GatewayMS() float64    { return milliseconds(j.SubmitAt, j.AcceptedAt) }
func (j TargetJob) AcceptanceMS() float64 { return milliseconds(j.ReceivedAt, j.AcceptedAt) }
