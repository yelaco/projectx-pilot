package storage

/*
#include <sqlite3.h>
#include <stdlib.h>
*/
import "C"
import (
	"errors"
	"strconv"
	"time"
	"unsafe"
)

func validReportIdentity(date, account string) bool {
	day, err := time.Parse("2006-01-02", date)
	if err != nil || day.Format("2006-01-02") != date {
		return false
	}
	id, err := strconv.ParseInt(account, 10, 64)
	return err == nil && id > 0
}
func (s *Inbox) dailyReportSchema() error {
	return s.exec(`CREATE TABLE IF NOT EXISTS daily_trading_reports(day TEXT NOT NULL,account TEXT NOT NULL,summary TEXT NOT NULL,attempted_at TEXT NOT NULL,PRIMARY KEY(day,account))`)
}
func (s *Inbox) DailyReportExists(date, account string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("report store closed")
	}
	if !validReportIdentity(date, account) {
		return false, errors.New("invalid daily report identity")
	}
	if err := s.dailyReportSchema(); err != nil {
		return false, err
	}
	stmt, err := s.prepare("SELECT 1 FROM daily_trading_reports WHERE day=? AND account=?")
	if err != nil {
		return false, err
	}
	defer C.sqlite3_finalize(stmt)
	for n, v := range []string{date, account} {
		p := C.CString(v)
		C.sqlite3_bind_text(stmt, C.int(n+1), p, -1, (*[0]byte)(C.SQLITE_TRANSIENT))
		C.free(unsafe.Pointer(p))
	}
	rc := C.sqlite3_step(stmt)
	if rc == C.SQLITE_ROW {
		return true, nil
	}
	if rc == C.SQLITE_DONE {
		return false, nil
	}
	return false, errors.New("daily report read failed")
}

// ClaimDailyReport records the summary and attempt before optional network delivery.
// It is not proof of successful Discord delivery and never changes execution state.
func (s *Inbox) ClaimDailyReport(date, account, summary string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("report store closed")
	}
	if !validReportIdentity(date, account) || len(summary) == 0 || len(summary) > 8192 {
		return false, errors.New("invalid daily report")
	}
	if err := s.dailyReportSchema(); err != nil {
		return false, err
	}
	if err := s.write(`INSERT OR IGNORE INTO daily_trading_reports(day,account,summary,attempted_at) VALUES(?,?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, date, account, summary); err != nil {
		return false, err
	}
	return C.sqlite3_changes(s.db) == 1, nil
}

// DailyReportingAccounts reads only identity columns, never entire intent payloads.
func (s *Inbox) DailyReportingAccounts(date string) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil, errors.New("report store closed")
	}
	if !validReportIdentity(date, "1") {
		return nil, errors.New("invalid reporting date")
	}
	if err := s.executionSchema(); err != nil {
		return nil, err
	}
	stmt, err := s.prepare("SELECT DISTINCT account FROM execution_intents WHERE session=?")
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	value := C.CString(date)
	C.sqlite3_bind_text(stmt, 1, value, -1, (*[0]byte)(C.SQLITE_TRANSIENT))
	C.free(unsafe.Pointer(value))
	ids := []int64{}
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			return ids, nil
		}
		if rc != C.SQLITE_ROW {
			return nil, errors.New("report account scope unavailable")
		}
		value := C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, 0))))
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("report account identity unavailable")
		}
		ids = append(ids, id)
	}
}
