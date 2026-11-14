package session

import (
	"errors"
	"time"
)

// Reviewed official Federal Reserve meeting days, including both days of each
// scheduled meeting. 2027 reflects the revised November 12, 2026 announcement.
const NewsCalendarRevision = "us-fomc-2026-2027-reviewed-2026-11-14"

var fomcDays = map[string]bool{
	"2026-01-27": true, "2026-01-28": true, "2026-03-17": true, "2026-03-18": true,
	"2026-04-28": true, "2026-04-29": true, "2026-06-16": true, "2026-06-17": true,
	"2026-07-28": true, "2026-07-29": true, "2026-09-15": true, "2026-09-16": true,
	"2026-10-27": true, "2026-10-28": true, "2026-12-08": true, "2026-12-09": true,
	"2027-01-26": true, "2027-01-27": true, "2027-03-16": true, "2027-03-17": true,
	"2027-04-27": true, "2027-04-28": true, "2027-06-08": true, "2027-06-09": true,
	"2027-07-27": true, "2027-07-28": true, "2027-09-14": true, "2027-09-15": true,
	"2027-10-26": true, "2027-10-27": true, "2027-11-16": true, "2027-11-17": true,
}

// NewsEntryAllowed controls entries only, never protective/emergency management.
// Empty means US FOMC enabled, including existing configurations after upgrade.
// A meeting date blocks its full Chicago trading session (previous day 17:00).
func NewsEntryAllowed(now time.Time, mode string) error {
	if mode == "off" {
		return nil
	}
	if mode != "" && mode != "us_fomc" {
		return errors.New("unknown news filter; entries blocked")
	}
	date := CurrentTradingDate(now)
	if date[:4] != "2026" && date[:4] != "2027" {
		return errors.New("US FOMC calendar unavailable for this year; entries blocked")
	}
	if fomcDays[date] {
		return errors.New("US FOMC meeting trading session; entries blocked")
	}
	return nil
}
