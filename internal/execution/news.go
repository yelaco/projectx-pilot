package execution

import (
	"errors"
	"time"
)

type NewsCalendar interface{ EntryAllowed(time.Time, string) error }

func (e *Engine) SetNewsCalendar(calendar NewsCalendar) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.newsCalendar = calendar
}
func (e *Engine) newsAllowed(at time.Time, mode string) error {
	if mode == "off" {
		return nil
	}
	if e.newsCalendar == nil {
		return errors.New("Forex Factory news calendar unavailable; entries blocked")
	}
	return e.newsCalendar.EntryAllowed(at, mode)
}
