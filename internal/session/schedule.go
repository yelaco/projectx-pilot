package session

import (
	"errors"
	"strconv"
	"time"
)

// Schedule is a Trade Manager's recurring weekday window in America/Chicago.
// Starts at/after 17:00 belong to the preceding calendar day of the trade date.
// Earlier starts belong to the trade date itself. This defines operating hours,
// not proof that a particular product is open; the operator confirms applicability.
type Schedule struct {
	Start     string `json:"start"`
	LastEntry string `json:"last_entry"`
	Flatten   string `json:"flatten"`
}

func DefaultSchedule() Schedule {
	return Schedule{Start: "17:00", LastEntry: "15:00", Flatten: "15:05"}
}

func clockMinutes(value string) (int, error) {
	if len(value) != 5 || value[2] != ':' {
		return 0, errors.New("schedule times must use HH:MM in America/Chicago")
	}
	for _, n := range []int{0, 1, 3, 4} {
		if value[n] < '0' || value[n] > '9' {
			return 0, errors.New("schedule times must use HH:MM in America/Chicago")
		}
	}
	h, _ := strconv.Atoi(value[:2])
	m, _ := strconv.Atoi(value[3:])
	if h > 23 || m > 59 {
		return 0, errors.New("invalid schedule clock time")
	}
	return h*60 + m, nil
}

func (s Schedule) Validate() error {
	start, err := clockMinutes(s.Start)
	if err != nil {
		return err
	}
	last, err := clockMinutes(s.LastEntry)
	if err != nil {
		return err
	}
	flatten, err := clockMinutes(s.Flatten)
	if err != nil {
		return err
	}
	if flatten > 15*60+8 {
		return errors.New("Trade Manager flatten must be by 15:08 America/Chicago")
	}
	if start >= 17*60 {
		start -= 24 * 60
	}
	if start >= last || last > flatten-2 || flatten-start > 23*60 {
		return errors.New("schedule must start before last entry, stop entries at least two minutes before flatten, and fit the 17:00 Chicago session boundary")
	}
	return nil
}

// ForDate constructs civil clock times, rather than adding UTC durations, so
// local schedules remain stable through daylight-saving transitions.
func (s Schedule) ForDate(date string) (Window, error) {
	if err := s.Validate(); err != nil {
		return Window{}, err
	}
	day, err := time.ParseInLocation("2006-01-02", date, Chicago)
	if err != nil || day.Format("2006-01-02") != date {
		return Window{}, errors.New("invalid schedule trading date")
	}
	makeTime := func(value string, previous bool) (time.Time, error) {
		minutes, _ := clockMinutes(value)
		base := day
		if previous {
			base = base.AddDate(0, 0, -1)
		}
		at := time.Date(base.Year(), base.Month(), base.Day(), minutes/60, minutes%60, 0, 0, Chicago)
		// A clock time skipped by DST is not silently normalized to another hour.
		if at.Format("15:04") != value {
			return time.Time{}, errors.New("schedule clock time is unavailable on this date")
		}
		return at, nil
	}
	startMinutes, _ := clockMinutes(s.Start)
	start, err := makeTime(s.Start, startMinutes >= 17*60)
	if err != nil {
		return Window{}, err
	}
	last, err := makeTime(s.LastEntry, false)
	if err != nil {
		return Window{}, err
	}
	flatten, err := makeTime(s.Flatten, false)
	if err != nil {
		return Window{}, err
	}
	return Window{Start: start, LastEntry: last, Flatten: flatten}, nil
}

// Restrict intersects an already verified authorization with the manager's
// operating window. It never widens the authorization or invents verification.
func (s Schedule) Restrict(w Window) (Window, error) {
	manager, err := s.ForDate(w.ID())
	if err != nil {
		return Window{}, err
	}
	if manager.Start.After(w.Start) {
		w.Start = manager.Start
	}
	if manager.LastEntry.Before(w.LastEntry) {
		w.LastEntry = manager.LastEntry
	}
	if manager.Flatten.Before(w.Flatten) {
		w.Flatten = manager.Flatten
	}
	// Respect the safety gap even when the supplied authorization closes earlier.
	if cutoff := w.Flatten.Add(-2 * time.Minute); w.LastEntry.After(cutoff) {
		w.LastEntry = cutoff
	}
	if !w.Start.Before(w.LastEntry) {
		return Window{}, errors.New("Trade Manager schedule does not overlap the verified session")
	}
	return w, nil
}
