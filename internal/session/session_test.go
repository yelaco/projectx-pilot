package session

import (
	"testing"
	"time"
)

func TestSessionCutoffAndDST(t *testing.T) {
	for _, date := range []string{"2026-03-09", "2026-11-02", "2026-11-13"} {
		day, err := time.ParseInLocation("2006-01-02", date, Chicago)
		if err != nil {
			t.Fatal(err)
		}
		w := Window{Verified: true, Start: day.Add(8 * time.Hour), LastEntry: day.Add(15 * time.Hour), Flatten: day.Add(15*time.Hour + 5*time.Minute)}
		if err = w.Validate(day.Add(9 * time.Hour)); err != nil {
			t.Fatal(date, err)
		}
		if err = w.Validate(w.LastEntry); err == nil {
			t.Fatal("entry at cutoff accepted")
		}
		boundary := w.StartOfSession()
		if boundary.Hour() != 17 || boundary.Location() != Chicago {
			t.Fatal(boundary)
		}
		_, offset := boundary.Zone()
		if date == "2026-03-09" && offset != -5*3600 || date == "2026-11-02" && offset != -6*3600 {
			t.Fatal("incorrect DST", boundary)
		}
	}
}
func TestUnknownHoursAndWeekendsFailClosed(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, Chicago)
	w := Window{Start: now.Add(-time.Hour), LastEntry: now.Add(time.Hour), Flatten: now.Add(2 * time.Hour)}
	if w.Validate(now) == nil {
		t.Fatal("unknown calendar accepted")
	}
	w.Verified = true
	w.Flatten = time.Date(2026, 11, 13, 15, 9, 0, 0, Chicago)
	if w.Validate(now) == nil {
		t.Fatal("late flatten accepted")
	}
	w.Flatten = time.Date(2026, 11, 14, 12, 0, 0, 0, Chicago)
	if w.Validate(now) == nil {
		t.Fatal("weekend accepted")
	}
}

func TestNormalPresetAndChicagoPickerOffsets(t *testing.T) {
	for _, tc := range []struct {
		date   string
		offset int
	}{{"2026-03-09", -5 * 3600}, {"2026-11-13", -6 * 3600}} {
		w, err := NormalWindow(tc.date)
		if err != nil {
			t.Fatal(err)
		}
		if w.Verified {
			t.Fatal("preset claimed official calendar verification")
		}
		if w.Start.In(Chicago).Hour() != 17 || w.LastEntry.In(Chicago).Hour() != 15 || w.Flatten.In(Chicago).Minute() != 5 {
			t.Fatal(w)
		}
		pick, err := ParseChicago(tc.date + "T09:30")
		if err != nil {
			t.Fatal(err)
		}
		_, offset := pick.Zone()
		if offset != tc.offset {
			t.Fatal("DST offset incorrect", pick)
		}
		w.Verified = true
		if err = w.Validate(pick); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ParseChicago("2026-03-08T02:30"); err == nil {
		t.Fatal("nonexistent DST clock time accepted")
	}
	if _, err := NormalWindow("bad-date"); err == nil {
		t.Fatal("invalid date accepted")
	}
	now := time.Date(2026, 11, 12, 18, 0, 0, 0, Chicago)
	if CurrentTradingDate(now) != "2026-11-13" {
		t.Fatal("evening session date wrong")
	}
}

func TestReviewedContinuousCalendar(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, Chicago)
	w, open, err := DefaultSchedule().Window(now)
	if err != nil || !open || w.Flatten.In(Chicago).Format("15:04") != "11:40" || w.LastEntry.In(Chicago).Format("15:04") != "11:35" {
		t.Fatal("Thanksgiving cutoff incorrect", w, open, err)
	}
	now = time.Date(2026, 12, 25, 10, 0, 0, 0, Chicago)
	w, open, err = DefaultSchedule().Window(now)
	if err != nil || open || w.ID() != "2026-12-28" {
		t.Fatal("closed holiday/weekend mishandled", w, open, err)
	}

	if _, _, err = DefaultSchedule().Window(time.Date(2027, 1, 4, 9, 0, 0, 0, Chicago)); err == nil {
		t.Fatal("unknown calendar year inferred")
	}
	for _, day := range []string{"2026-03-09", "2026-11-02"} {
		d, _ := time.ParseInLocation("2006-01-02", day, Chicago)
		w, open, err = DefaultSchedule().Window(d.Add(9 * time.Hour))
		if err != nil || !open || w.Start.In(Chicago).Hour() != 17 {
			t.Fatal("calendar DST incorrect", w, err)
		}
	}
}
