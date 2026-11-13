package session

import (
	"errors"
	"time"
	_ "time/tzdata"
)

var Chicago = func() *time.Location {
	l, err := time.LoadLocation("America/Chicago")
	if err != nil {
		panic(err)
	}
	return l
}()

// Window is explicitly confirmed for this contract and date by the local operator.
// It is never an inferred holiday calendar. All times are converted to Chicago.
type Window struct {
	Start, LastEntry, Flatten time.Time
	Verified                  bool
}

func (w Window) Validate(now time.Time) error {
	if !w.Verified || w.Start.IsZero() || w.LastEntry.IsZero() || w.Flatten.IsZero() {
		return errors.New("confirm the instrument's official hours and holiday schedule for this session")
	}
	start, end := w.Start.In(Chicago), w.Flatten.In(Chicago)
	if !w.Start.Before(w.LastEntry) || !w.LastEntry.Before(w.Flatten) || w.Flatten.Sub(w.Start) > 23*time.Hour {
		return errors.New("invalid trading window")
	}
	// Verified narrower holiday/product windows are supported, never wider ones.
	if end.Weekday() == time.Saturday || end.Weekday() == time.Sunday || end.Hour() > 15 || end.Hour() == 15 && end.Minute() > 8 {
		return errors.New("flatten must be on a trading weekday by 15:08 America/Chicago")
	}
	boundary := time.Date(end.Year(), end.Month(), end.Day()-1, 17, 0, 0, 0, Chicago)
	if start.Before(boundary) || w.LastEntry.After(w.Flatten.Add(-2*time.Minute)) {
		return errors.New("window exceeds session boundary or entry safety margin")
	}
	if now.Before(w.Start) || !now.Before(w.LastEntry) {
		return errors.New("outside the verified entry window")
	}
	return nil
}
func (w Window) ID() string { return w.Flatten.In(Chicago).Format("2006-01-02") }

// StartOfSession is fixed to 17:00 Chicago, even for a narrower trading window.
func (w Window) StartOfSession() time.Time {
	end := w.Flatten.In(Chicago)
	return time.Date(end.Year(), end.Month(), end.Day()-1, 17, 0, 0, 0, Chicago)
}

// NormalWindow is a suggested schedule, never a verified holiday calendar.
func NormalWindow(date string) (Window, error) {
	day, err := time.ParseInLocation("2006-01-02", date, Chicago)
	if err != nil {
		return Window{}, errors.New("choose a trading date")
	}
	return Window{Start: time.Date(day.Year(), day.Month(), day.Day()-1, 17, 0, 0, 0, Chicago), LastEntry: day.Add(15 * time.Hour), Flatten: day.Add(15*time.Hour + 5*time.Minute)}, nil
}
func CurrentTradingDate(now time.Time) string {
	local := now.In(Chicago)
	if local.Hour() >= 17 {
		local = local.AddDate(0, 0, 1)
	}
	return local.Format("2006-01-02")
}

// ParseChicago accepts a date/time picker value, and legacy timezone timestamps.
func ParseChicago(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", value, Chicago)
	if err != nil || t.Format("2006-01-02T15:04") != value {
		return time.Time{}, errors.New("choose a valid date and time in America/Chicago")
	}
	return t, nil
}
