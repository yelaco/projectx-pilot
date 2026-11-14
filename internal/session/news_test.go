package session

import (
	"testing"
	"time"
)

func TestUSFOMCSessions(t *testing.T) {
	for _, tc := range []struct {
		at, mode string
		blocked  bool
	}{
		{"2026-12-07T16:59:59", "", false},
		{"2026-12-07T17:00:00", "", true},
		{"2026-12-08T09:00:00", "us_fomc", true},
		{"2026-12-09T16:59:59", "", true},
		{"2026-12-09T17:00:00", "", false},
		{"2026-03-17T09:00:00", "", true},
		{"2026-11-13T09:00:00", "", false},
		{"2027-10-26T09:00:00", "", true},
		{"2027-11-17T09:00:00", "", true},
		{"2027-12-08T09:00:00", "", false}, // replaced tentative December meeting
		{"2028-01-03T09:00:00", "", true},
		{"2028-01-03T09:00:00", "off", false},
		{"2026-12-08T09:00:00", "off", false},
		{"2026-11-13T09:00:00", "invalid", true},
	} {
		at, err := time.ParseInLocation("2006-01-02T15:04:05", tc.at, Chicago)
		if err != nil {
			t.Fatal(err)
		}
		for _, z := range []*time.Location{Chicago, time.UTC, time.FixedZone("laptop", 7*3600)} {
			if got := NewsEntryAllowed(at.In(z), tc.mode) != nil; got != tc.blocked {
				t.Errorf("%s %s zone=%s blocked=%v", tc.at, tc.mode, z, got)
			}
		}
	}
	if len(fomcDays) != 32 {
		t.Fatal("incomplete two-year meeting calendar")
	}
}
