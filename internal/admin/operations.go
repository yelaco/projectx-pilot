package admin

import (
	"errors"
	"sort"
	"time"

	"projectxbot/internal/config"
	"projectxbot/internal/session"
)

type OperationDay struct {
	Date, Name, Reason           string
	NewsWarning                  string
	Manual, News, Weekend, Today bool
	General                      bool
}
type OperationWeek struct {
	Start, End, Previous, Next, Revision string
	Days                                 []OperationDay
	TradeManagerID, TradeManagerName     string
}

func (m *Manager) OperationDays(value string) (OperationWeek, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.operationDaysLocked(value, "")
}
func (m *Manager) OperationDaysForManager(value, id string) (OperationWeek, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.operationDaysLocked(value, id)
}
func (m *Manager) operationDaysLocked(value, id string) (OperationWeek, error) {
	start, err := session.OperationWeek(m.now(), value)
	if err != nil {
		return OperationWeek{}, err
	}
	w := OperationWeek{Start: start.Format("2006-01-02"), End: start.AddDate(0, 0, 6).Format("2006-01-02"), Previous: start.AddDate(0, 0, -7).Format("2006-01-02"), Next: start.AddDate(0, 0, 7).Format("2006-01-02"), Revision: "Forex Factory weekly JSON"}
	list := m.c.NoTradeDates
	if id != "" {
		found := false
		for _, tm := range m.policies.TradeManagers {
			if tm.ID == id {
				found = true
				w.TradeManagerID = id
				w.TradeManagerName = tm.Name
			}
		}
		if !found {
			return OperationWeek{}, errors.New("Trade Manager not found")
		}
		list = m.c.TradeManagerNoTradeDates[id]
	}
	for n := 0; n < 7; n++ {
		day := start.AddDate(0, 0, n)
		date := day.Format("2006-01-02")
		d := OperationDay{Date: date, Name: day.Weekday().String(), Weekend: day.Weekday() == time.Saturday || day.Weekday() == time.Sunday, Today: date == session.CurrentTradingDate(m.now())}
		for _, v := range list {
			if v == date {
				d.Manual = true
			}
		}
		if id != "" {
			for _, v := range m.c.NoTradeDates {
				if v == date {
					d.General = true
				}
			}
		}
		if m.c.NewsFilter != "off" {
			d.NewsWarning = m.news.CoverageWarning(date)
		}
		if err := m.news.DayAllowed(date, m.c.NewsFilter); err != nil {
			d.News = true
			d.Reason = err.Error()
		}
		w.Days = append(w.Days, d)
	}
	return w, nil
}

// SaveOperationDays edits only the submitted Saturday-Friday interval. Other
// weeks are preserved. An unchecked manual box never overrides the news gate.
func (m *Manager) SaveOperationDays(week string, blocked []string, news string, scope ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := ""
	if len(scope) > 1 {
		return errors.New("one Trade Manager scope required")
	}
	if len(scope) == 1 {
		id = scope[0]
	}
	if id != "" {
		found := false
		for _, tm := range m.policies.TradeManagers {
			if tm.ID == id {
				found = true
			}
		}
		if !found {
			return errors.New("Trade Manager not found")
		}
	}
	start, err := session.OperationWeek(m.now(), week)
	if err != nil {
		return err
	}
	if week == "" {
		return errors.New("week start is required")
	}
	if err := session.ValidateNoTradeDates(blocked); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for n := 0; n < 7; n++ {
		allowed[start.AddDate(0, 0, n).Format("2006-01-02")] = true
	}
	for _, date := range blocked {
		if !allowed[date] {
			return errors.New("no-trade date is outside the submitted week")
		}
	}
	if id == "" && news != "us_fomc" && news != "off" {
		return errors.New("choose US FOMC or off")
	}
	next := m.c
	list := m.c.NoTradeDates
	if id != "" {
		list = m.c.TradeManagerNoTradeDates[id]
	}
	updated := append([]string(nil), blocked...)
	for _, date := range list {
		if !allowed[date] {
			updated = append(updated, date)
		}
	}
	sort.Strings(updated)
	if id == "" {
		next.NoTradeDates = updated
		next.NewsFilter = news
	} else {
		next.TradeManagerNoTradeDates = map[string][]string{}
		for k, v := range m.c.TradeManagerNoTradeDates {
			next.TradeManagerNoTradeDates[k] = append([]string(nil), v...)
		}
		next.TradeManagerNoTradeDates[id] = updated
	}
	if err := next.Validate(); err != nil {
		return err
	}
	// Disarm before committing a policy change, without stopping position monitoring.
	m.executor.Pause()
	if m.fleet != nil {
		if id == "" {
			_ = m.fleet.PauseAll(false)
		} else {
			for _, p := range m.policies.Automations {
				if p.TradeManagerID == id {
					m.fleet.PauseProfile(p.ID)
				}
			}
		}
	}
	if err := config.Save(m.configPath, next); err != nil {
		return errors.New("could not save operation days")
	}
	m.c = next
	if m.fleet != nil {
		if err := m.fleet.SetOperationPlan(next.NoTradeDates, next.TradeManagerNoTradeDates); err != nil {
			return err
		}
	}
	return m.executor.SetOperationPlan(next.NoTradeDates, next.TradeManagerNoTradeDates)
}
