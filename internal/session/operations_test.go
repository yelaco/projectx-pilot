package session

import (
	"testing"
	"time"
)

func TestOperationWeekAndSessionBoundaries(t *testing.T) {
	for _, at := range []string{"2026-11-14", "2026-11-15", "2026-11-20"} {
		d, _ := time.ParseInLocation("2006-01-02", at, Chicago)
		w, err := OperationWeek(d, "")
		if err != nil || w.Format("2006-01-02") != "2026-11-14" {
			t.Fatal(at, w, err)
		}
	}
	day, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-15 16:59", Chicago)
	if OperationEntryAllowed(day, []string{"2026-11-16"}) != nil {
		t.Fatal("Monday blocked too early")
	}
	if OperationEntryAllowed(day.Add(time.Minute), []string{"2026-11-16"}) == nil {
		t.Fatal("Sunday evening is Monday trading date")
	}
	for _, dates := range [][]string{{"bad"}, {"2026-02-30"}, {"2026-11-16", "2026-11-16"}} {
		if ValidateNoTradeDates(dates) == nil {
			t.Fatal("invalid dates accepted")
		}
	}
}
