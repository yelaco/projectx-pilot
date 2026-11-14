package admin

import (
	"testing"
	"time"

	"projectxbot/internal/config"
	"projectxbot/internal/risk"
	"projectxbot/internal/session"
)

func TestOperationPlansHierarchyAndPersistence(t *testing.T) {
	m, _, path, state := setup(t)
	m.now = func() time.Time { return time.Date(2026, 12, 5, 9, 0, 0, 0, session.Chicago) }
	// Saved policies identify scopes; no accounts are armed by this test.
	for _, id := range []string{"first", "second"} {
		p := risk.Defaults()
		p.ID = id
		p.Name = id
		p.AccountIDs = []int64{42}
		if err := m.SaveTradeManager(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SaveOperationDays("2026-12-05", []string{"2026-12-07"}, "us_fomc"); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveOperationDays("2026-12-05", []string{"2026-12-10"}, "", "first"); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveOperationDays("2026-12-12", []string{"2026-12-14"}, "", "first"); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveOperationDays("2026-12-05", nil, "", "second"); err != nil {
		t.Fatal(err)
	}
	w, err := m.OperationDaysForManager("2026-12-05", "first")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Days) != 7 || w.Days[0].Name != "Saturday" || w.Days[6].Name != "Friday" {
		t.Fatal("week order")
	}
	if !w.Days[2].General || w.Days[2].Manual || w.Days[3].News || w.Days[4].News || w.Days[3].NewsWarning == "" || !w.Days[5].Manual {
		t.Fatal("inheritance or FOMC blocks missing", w.Days)
	}
	second, err := m.OperationDaysForManager("2026-12-05", "second")
	if err != nil {
		t.Fatal(err)
	}
	if second.Days[5].Manual || !second.Days[2].General {
		t.Fatal("manager scope leaked")
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.NoTradeDates) != 1 || len(saved.TradeManagerNoTradeDates["first"]) != 2 || saved.NewsFilter != "us_fomc" {
		t.Fatal("lost another week or general plan")
	}
	restarted, _, err := Open(saved, path, state)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restored, err := restarted.OperationDaysForManager("2026-12-05", "first")
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Days[2].General || !restored.Days[5].Manual || restarted.ExecutionStatus().Armed {
		t.Fatal("restart lost plan or restored arming")
	}
	for _, tc := range []struct {
		week, id string
		dates    []string
	}{
		{"2026-12-06", "", nil}, {"2026-12-05", "missing", nil}, {"2026-12-05", "first", []string{"2026-12-14"}},
	} {
		if err := m.SaveOperationDays(tc.week, tc.dates, "us_fomc", tc.id); err == nil {
			t.Fatal("invalid edit accepted")
		}
	}
	if err := m.SaveOperationDays("2026-12-05", nil, "", "first"); err != nil {
		t.Fatal(err)
	}
	saved, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.TradeManagerNoTradeDates["first"]) != 1 || saved.TradeManagerNoTradeDates["first"][0] != "2026-12-14" || len(saved.NoTradeDates) != 1 {
		t.Fatal("clearing week erased other scopes")
	}
}
