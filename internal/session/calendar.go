package session

import (
	"errors"
	"strings"
	"time"
)

// CalendarRevision records the official Topstep schedules reviewed for this
// release. Unknown years/instruments fail closed instead of inferring hours.
const CalendarRevision = "topstep-2026-reviewed-2026-11-13"

var earlyClose2026 = map[string]string{
	"2026-01-19": "11:45", "2026-02-16": "11:45", "2026-04-03": "08:00",
	"2026-05-25": "11:45", "2026-06-19": "11:45", "2026-07-03": "11:45",
	"2026-09-07": "11:45", "2026-11-26": "11:45", "2026-11-27": "12:00", "2026-12-24": "12:00",
}

// ContinuousWindow returns the first current/upcoming verified window. Open
// means new entries are permitted by the calendar, not that the account is safe.
// A published early-close deadline gets another five-minute flatten margin.
func ContinuousWindow(now time.Time, symbols []string) (window Window, open bool, err error) {
	if len(symbols) == 0 {
		return window, false, errors.New("calendar needs an instrument")
	}
	for _, s := range symbols {
		if s != "MNQ" && s != "MGC" {
			return window, false, errors.New("continuous calendar currently supports MNQ and MGC only")
		}
	}
	day, err := time.ParseInLocation("2006-01-02", CurrentTradingDate(now), Chicago)
	if err != nil {
		return window, false, err
	}
	for n := 0; n < 10; n++ {
		date := day.Format("2006-01-02")
		if day.Year() != 2026 {
			return window, false, errors.New("calendar verification is unavailable for this year; waiting for a reviewed calendar update")
		}
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday && date != "2026-01-01" && date != "2026-12-25" {
			w, _ := NormalWindow(date)
			w.Verified = true
			if closeTime, ok := earlyClose2026[date]; ok {
				parsed, _ := time.ParseInLocation("2006-01-02 15:04", date+" "+closeTime, Chicago)
				w.Flatten = parsed.Add(-5 * time.Minute)
				w.LastEntry = w.Flatten.Add(-5 * time.Minute)
			}
			if now.Before(w.LastEntry) {
				return w, !now.Before(w.Start), nil
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return window, false, errors.New("no verified upcoming trading window")
}
func SupportsContinuousSymbolID(symbolID string) bool {
	return strings.HasSuffix(symbolID, ".MNQ") || strings.HasSuffix(symbolID, ".MGC")
}
