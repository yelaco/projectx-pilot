package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestHourlyReportClaimSurvivesRestartAndDistinguishesDSTHours(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := time.Parse(time.RFC3339, "2026-11-01T01:00:00-05:00")
	second, _ := time.Parse(time.RFC3339, "2026-11-01T01:00:00-06:00")
	for _, hour := range []time.Time{first, second} {
		claimed, err := s.ClaimHourlyAutomationReport(hour, []string{"sanitized status"})
		if err != nil || !claimed {
			t.Fatal("distinct UTC hour not claimed", claimed, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if exists, err := s.HourlyAutomationReportExists(first); err != nil || !exists {
		t.Fatal("claim lost on restart", exists, err)
	}
	if claimed, err := s.ClaimHourlyAutomationReport(first, []string{"new snapshot"}); err != nil || claimed {
		t.Fatal("restart replayed report", claimed, err)
	}
	if claimed, err := s.ClaimHourlyAutomationReport(first.Add(time.Minute), []string{"invalid hour"}); err == nil || claimed {
		t.Fatal("non-hour identity accepted")
	}
}
