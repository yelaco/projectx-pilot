package admin

import (
	"errors"

	"github.com/yelaco/projectx-pilot/internal/risk"
)

func (m *Manager) SaveTradeManager(profile risk.Profile) error {
	profile = profile.Clone()
	m.mu.Lock()
	defer m.mu.Unlock()
	// Older clients omit schedule fields; they must not erase a custom schedule.
	for _, old := range m.policies.TradeManagers {
		if old.ID == profile.ID && profile.Routing == "multiple" && profile.AccountRiskOverrides == nil {
			profile.AccountRiskOverrides = old.Clone().AccountRiskOverrides
		}
		if old.ID == profile.ID && profile.Routing == "multiple" && profile.MultiAccountMode == "" {
			profile.MultiAccountMode = old.MultiAccountMode
		}
		if old.ID == profile.ID && profile.Schedule == nil && old.Schedule != nil {
			schedule := *old.Schedule
			profile.Schedule = &schedule
		}
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	p := clonePolicies(m.policies)
	found := false
	for i := range p.TradeManagers {
		if p.TradeManagers[i].ID == profile.ID {
			p.TradeManagers[i] = profile
			found = true
		}
	}
	if !found {
		p.TradeManagers = append(p.TradeManagers, profile)
	}
	for i := range p.Automations {
		if p.Automations[i].TradeManagerID == profile.ID {
			p.Automations[i].AccountID = 0
			if profile.Routing == "single" {
				p.Automations[i].AccountID = profile.AccountIDs[0]
			}
		}
	}
	return m.savePolicies(p)
}
func (m *Manager) DeleteTradeManager(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := clonePolicies(m.policies)
	for _, a := range p.Automations {
		if a.TradeManagerID == id {
			return errors.New("trade manager is assigned to an automation; reassign that automation before deleting")
		}
	}
	for i, v := range p.TradeManagers {
		if v.ID == id {
			p.TradeManagers = append(p.TradeManagers[:i], p.TradeManagers[i+1:]...)
			return m.savePolicies(p)
		}
	}
	return errors.New("trade manager not found")
}
