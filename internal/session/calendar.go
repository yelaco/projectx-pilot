package session

import (
	"errors"
	"time"
)

// CalendarRevision records the official Topstep schedules reviewed for this
// release. Unknown years fail closed instead of inventing holiday hours.
const CalendarRevision = "topstep-2026-reviewed-2026-11-13"

var earlyClose2026 = map[string]string{
	"2026-01-19": "11:45", "2026-02-16": "11:45", "2026-04-03": "08:00",
	"2026-05-25": "11:45", "2026-06-19": "11:45", "2026-07-03": "11:45",
	"2026-09-07": "11:45", "2026-11-26": "11:45", "2026-11-27": "12:00", "2026-12-24": "12:00",
}

// Window returns the current/upcoming manager window, narrowed by reviewed
// Topstep holiday cutoffs. Symbol names do not select or authorize schedules.
func (s Schedule) Window(now time.Time) (window Window, open bool, err error) {
	if err := s.Validate(); err != nil {
		return window, false, err
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
			w, buildErr := s.ForDate(date)
			if buildErr != nil {
				return window, false, buildErr
			}
			w.Verified = true
			if closeTime, ok := earlyClose2026[date]; ok {
				parsed, _ := time.ParseInLocation("2006-01-02 15:04", date+" "+closeTime, Chicago)
				// The published value is already Topstep's position-close cutoff;
				// retain an extra five-minute local flatten margin.
				bound := parsed.Add(-5 * time.Minute)
				if w.Flatten.After(bound) {
					w.Flatten = bound
				}
				if last := bound.Add(-5 * time.Minute); w.LastEntry.After(last) {
					w.LastEntry = last
				}
			}
			if w.Start.Before(w.LastEntry) && now.Before(w.LastEntry) {
				return w, !now.Before(w.Start), nil
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return window, false, errors.New("no verified upcoming trading window")
}
