package session

import (
	"testing"
	"time"
)

func TestManagerScheduleValidation(t *testing.T) {
	valid := []Schedule{DefaultSchedule(), {Start: "08:30", LastEntry: "13:10", Flatten: "13:15"}, {Start: "19:00", LastEntry: "07:35", Flatten: "07:40"}}
	for _, s := range valid {
		if err := s.Validate(); err != nil {
			t.Fatal(s, err)
		}
	}
	invalid := []Schedule{{}, {Start: "8:30", LastEntry: "13:00", Flatten: "13:05"}, {Start: "24:00", LastEntry: "13:00", Flatten: "13:05"}, {Start: "08:30", LastEntry: "13:04", Flatten: "13:05"}, {Start: "16:00", LastEntry: "15:00", Flatten: "15:05"}, {Start: "17:00", LastEntry: "15:06", Flatten: "15:09"}, {Start: "09:00", LastEntry: "08:00", Flatten: "08:05"}}
	for _, s := range invalid {
		if s.Validate() == nil {
			t.Fatal("unsafe schedule accepted", s)
		}
	}
}
func TestManagerWindowsAndHolidayCaps(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, Chicago)
	day := Schedule{Start: "08:30", LastEntry: "13:10", Flatten: "13:15"}
	w, open, err := day.Window(now)
	if err != nil || !open || w.Start.Format("15:04") != "08:30" || w.LastEntry.Format("15:04") != "13:10" || w.Flatten.Format("15:04") != "13:15" {
		t.Fatal(w, open, err)
	}
	later := Schedule{Start: "10:00", LastEntry: "14:00", Flatten: "14:05"}
	next, open, err := later.Window(now)
	if err != nil || open || next.ID() != w.ID() {
		t.Fatal("managers did not retain independent hours", next, open, err)
	}
	holiday := time.Date(2026, 11, 26, 10, 0, 0, 0, Chicago)
	w, open, err = day.Window(holiday)
	if err != nil || !open || w.Flatten.Format("15:04") != "11:40" || w.LastEntry.Format("15:04") != "11:35" {
		t.Fatal("holiday was not capped", w, err)
	}
	narrow := Schedule{Start: "08:30", LastEntry: "10:00", Flatten: "10:05"}
	w, _, err = narrow.Window(holiday.Add(-time.Hour))
	if err != nil || w.Flatten.Format("15:04") != "10:05" {
		t.Fatal("holiday widened a narrow manager schedule", w, err)
	}
	afterClose := Schedule{Start: "12:00", LastEntry: "13:00", Flatten: "13:05"}
	w, open, err = afterClose.Window(holiday)
	if err != nil || open || w.ID() != "2026-11-30" {
		t.Fatal("non-overlapping holiday should skip to next eligible window", w, open, err)
	}
}
func TestOvernightScheduleAndChicagoDST(t *testing.T) {
	overnight := Schedule{Start: "19:00", LastEntry: "07:35", Flatten: "07:40"}
	w, open, err := overnight.Window(time.Date(2026, 11, 13, 7, 0, 0, 0, Chicago))
	if err != nil || !open || w.Start.Format("2006-01-02 15:04") != "2026-11-12 19:00" {
		t.Fatal("overnight mapping incorrect", w, open, err)
	}
	for _, test := range []struct {
		date   string
		offset int
	}{{"2026-03-09", -5 * 3600}, {"2026-11-02", -6 * 3600}} {
		w, err := DefaultSchedule().ForDate(test.date)
		if err != nil {
			t.Fatal(err)
		}
		_, offset := w.Start.Zone()
		if offset != test.offset || w.Start.Hour() != 17 || w.LastEntry.Hour() != 15 {
			t.Fatal("Chicago clocks shifted with DST", w)
		}
	}
}
func TestScheduleRestrictNeverWidensAuthorization(t *testing.T) {
	original, _ := NormalWindow("2026-11-13")
	original.Verified = true
	manager := Schedule{Start: "08:30", LastEntry: "13:00", Flatten: "13:05"}
	restricted, err := manager.Restrict(original)
	if err != nil || !restricted.Verified || restricted.Start.Hour() != 8 || restricted.LastEntry.Hour() != 13 || restricted.Flatten.Format("15:04") != "13:05" {
		t.Fatal(restricted, err)
	}
	original.Flatten = time.Date(2026, 11, 13, 11, 40, 0, 0, Chicago)
	original.LastEntry = original.Flatten.Add(-5 * time.Minute)
	restricted, err = manager.Restrict(original)
	if err != nil || restricted.Flatten.After(original.Flatten) || restricted.LastEntry.After(original.LastEntry) {
		t.Fatal("existing holiday authorization widened", restricted, err)
	}
	original.Verified = false
	restricted, err = manager.Restrict(original)
	if err != nil || restricted.Verified {
		t.Fatal("schedule fabricated verification", restricted, err)
	}
}
