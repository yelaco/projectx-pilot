package admin

import (
	"errors"

	"projectxbot/internal/risk"
)

func (m *Manager) SaveTradeManager(profile risk.Profile) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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
