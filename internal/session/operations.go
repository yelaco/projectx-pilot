package session

import (
	"errors"
	"time"
)

func ValidateNoTradeDates(dates []string) error {
	if len(dates) > 1000 {
		return errors.New("maximum 1000 no-trade dates")
	}
	seen := map[string]bool{}
	for _, date := range dates {
		day, err := time.ParseInLocation("2006-01-02", date, Chicago)
		if err != nil || day.Format("2006-01-02") != date || seen[date] {
			return errors.New("invalid or duplicate no-trade date")
		}
		seen[date] = true
	}
	return nil
}
func OperationEntryAllowed(now time.Time, dates []string) error {
	date := CurrentTradingDate(now)
	for _, d := range dates {
		if d == date {
			return errors.New("operation day manually disabled; entries blocked")
		}
	}
	return nil
}

// OperationWeek returns a civil Saturday date, avoiding 24-hour DST arithmetic.
func OperationWeek(now time.Time, value string) (time.Time, error) {
	local := now.In(Chicago)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Chicago)
	if value != "" {
		var err error
		day, err = time.ParseInLocation("2006-01-02", value, Chicago)
		if err != nil || day.Weekday() != time.Saturday {
			return time.Time{}, errors.New("choose a Saturday week start")
		}
	}
	return day.AddDate(0, 0, -(int(day.Weekday())+1)%7), nil
}
