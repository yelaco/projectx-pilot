package execution

import (
	"errors"
	"github.com/yelaco/projectx-pilot/internal/session"
	"time"
)

// SetOperationPlan changes entry restrictions without stopping position management.
func (e *Engine) SetOperationPlan(dates []string, managers map[string][]string) error {
	if err := session.ValidateNoTradeDates(dates); err != nil {
		return err
	}
	copied := map[string][]string{}
	for id, list := range managers {
		if err := session.ValidateNoTradeDates(list); err != nil {
			return err
		}
		copied[id] = append([]string(nil), list...)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.operationDays = append([]string(nil), dates...)
	e.managerOperationDays = copied
	return nil
}
func (e *Engine) operationAllowed(now time.Time, manager string) error {
	if err := session.OperationEntryAllowed(now, e.operationDays); err != nil {
		return err
	}
	if err := session.OperationEntryAllowed(now, e.managerOperationDays[manager]); err != nil {
		return errors.New("Trade Manager operation day disabled; entries blocked")
	}
	return nil
}
