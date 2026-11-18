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
	"unsafe"
)

// ClaimRoundRobin atomically freezes one target and advances its manager cursor.
// No-ready outcomes are blocked rows with AccountID=0, never execution targets.
// Existing deliveries never advance, expand, switch modes, or replay.
func (s *Inbox) ClaimRoundRobin(manager string, candidates []TargetJob, eligible map[int64]bool) (TargetJob, error) {
	var empty TargetJob
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return empty, errors.New("execution store closed")
	}
	if manager == "" || len(manager) > 64 || len(candidates) < 2 || len(candidates) > 20 {
		return empty, errors.New("distribution requires a manager and 2..20 targets")
	}
	first := candidates[0]
	seen := map[int64]bool{}
	for _, j := range candidates {
		if j.ID == "" || j.DeliveryID == "" || j.DeliveryID != first.DeliveryID || j.ProfileID == "" || j.ProfileID != first.ProfileID || j.ContractID != first.ContractID || j.Policy != first.Policy || j.AccountID <= 0 || seen[j.AccountID] {
			return empty, errors.New("invalid distribution targets")
		}
		seen[j.AccountID] = true
	}
	if err := s.fanoutSchema(); err != nil {
		return empty, err
	}
	if err := s.exec(`CREATE TABLE IF NOT EXISTS distribution_cursors(manager TEXT PRIMARY KEY,last_account INTEGER NOT NULL)`); err != nil {
		return empty, err
	}
	if err := s.exec("BEGIN IMMEDIATE"); err != nil {
		return empty, err
	}
	defer s.exec("ROLLBACK")
	// Both copy and distribution write target_jobs, providing cross-mode dedup.
	count, err := s.distributionScalar("SELECT COUNT(*) FROM target_jobs WHERE delivery=?", first.DeliveryID)
	if err != nil {
		return empty, err
	}
	if count > 0 {
		return empty, ErrDuplicate
	}
	last, err := s.distributionScalar("SELECT last_account FROM distribution_cursors WHERE manager=?", manager)
	if err != nil {
		return empty, err
	}
	start := 0
	for n, j := range candidates {
		if j.AccountID == last {
			start = (n + 1) % len(candidates)
			break
		}
	}
	selected := empty
	for n := 0; n < len(candidates); n++ {
		j := candidates[(start+n)%len(candidates)]
		if eligible[j.AccountID] {
			selected = j
			break
		}
	}
	if selected.AccountID == 0 {
		selected = first
		selected.AccountID = 0
		selected.ID = first.ID + ":unassigned"
		selected.State = "blocked"
		selected.Message = "Distribution blocked: no ready account among configured targets; no delayed replay"
		selected.FinishedAt = first.ClaimedAt
	} else {
		selected.State = "claimed"
	}
	selected.RoutingMode = "round_robin"
	raw, err := json.Marshal(selected)
	if err != nil {
		return empty, err
	}
	if err = s.write(`INSERT INTO target_jobs(id,delivery,profile,account,state,payload) VALUES(?,?,?,?,?,?)`, selected.ID, selected.DeliveryID, selected.ProfileID, strconv.FormatInt(selected.AccountID, 10), selected.State, string(raw)); err != nil {
		return empty, err
	}
	if selected.AccountID != 0 {
		if err = s.write(`INSERT INTO distribution_cursors(manager,last_account) VALUES(?,?) ON CONFLICT(manager) DO UPDATE SET last_account=excluded.last_account`, manager, strconv.FormatInt(selected.AccountID, 10)); err != nil {
			return empty, err
		}
	}
	if err = s.exec("COMMIT"); err != nil {
		return empty, err
	}
	return selected, nil
}
func (s *Inbox) distributionScalar(query, arg string) (int64, error) {
	stmt, err := s.prepare(query)
	if err != nil {
		return 0, err
	}
	defer C.sqlite3_finalize(stmt)
	value := C.CString(arg)
	C.sqlite3_bind_text(stmt, 1, value, -1, (*[0]byte)(C.SQLITE_TRANSIENT))
	C.free(unsafe.Pointer(value))
	switch C.sqlite3_step(stmt) {
	case C.SQLITE_ROW:
		return int64(C.sqlite3_column_int64(stmt, 0)), nil
	case C.SQLITE_DONE:
		return 0, nil
	default:
		return 0, errors.New("distribution claim lookup failed")
	}
}
