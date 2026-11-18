package storage

/*
#include <sqlite3.h>
*/
import "C"
import (
	"encoding/json"
	"errors"
	"time"
)

func (s *Inbox) hourlyReportSchema() error {
	return s.exec(`CREATE TABLE IF NOT EXISTS hourly_automation_reports(hour TEXT PRIMARY KEY,summaries TEXT NOT NULL,attempted_at TEXT NOT NULL)`)
}
func validReportHour(hour time.Time) bool {
	return !hour.IsZero() && hour.Equal(hour.UTC().Truncate(time.Hour))
}
func (s *Inbox) HourlyAutomationReportExists(hour time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("report store closed")
	}
	if !validReportHour(hour) {
		return false, errors.New("invalid report hour")
	}
	if err := s.hourlyReportSchema(); err != nil {
		return false, err
	}
	stmt, err := s.prepare("SELECT 1 FROM hourly_automation_reports WHERE hour=?", hour.UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	defer C.sqlite3_finalize(stmt)
	switch C.sqlite3_step(stmt) {
	case C.SQLITE_ROW:
		return true, nil
	case C.SQLITE_DONE:
		return false, nil
	default:
		return false, errors.New("hourly report read failed")
	}
}

// ClaimHourlyAutomationReport records sanitized snapshots before best-effort
// delivery. A claim is not delivery confirmation; old claims are never replayed.
func (s *Inbox) ClaimHourlyAutomationReport(hour time.Time, messages []string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return false, errors.New("report store closed")
	}
	if !validReportHour(hour) || len(messages) < 1 || len(messages) > 32 {
		return false, errors.New("invalid hourly report")
	}
	for _, message := range messages {
		if message == "" || len([]rune(message)) > 1600 {
			return false, errors.New("invalid hourly report message")
		}
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		return false, errors.New("invalid hourly report")
	}
	if err = s.hourlyReportSchema(); err != nil {
		return false, err
	}
	if err = s.write(`INSERT OR IGNORE INTO hourly_automation_reports(hour,summaries,attempted_at) VALUES(?,?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, hour.UTC().Format(time.RFC3339), string(raw)); err != nil {
		return false, err
	}
	return C.sqlite3_changes(s.db) == 1, nil
}
