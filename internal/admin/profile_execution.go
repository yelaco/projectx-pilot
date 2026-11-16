package admin

import (
	"context"
	"errors"

	"github.com/yelaco/projectx-pilot/internal/execution"
)

func (m *Manager) executionProfileLocked(id string) (Automation, error) {
	for _, a := range m.policies.Automations {
		if a.ID == id {
			if a.Source != "tradingview-manual" || a.TradeManagerID == "" || a.AccountID <= 0 {
				return a, errors.New("assign this TradingView profile a single-account Trade Manager first")
			}
			for _, p := range m.policies.TradeManagers {
				if p.ID == a.TradeManagerID && p.Routing == "single" && p.HasAccount(a.AccountID) {
					return a, nil
				}
			}
			return a, errors.New("profile Trade Manager must target exactly one account")
		}
	}
	return Automation{}, errors.New("automation profile not found")
}

// SaveProfileExecution derives the execution target from the profile, and binds
// the submitted view to that target so stale/tampered forms cannot switch it.
func (m *Manager) SaveProfileExecution(actor, password, id string, c execution.Config, allowAccount bool) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveProfileExecutionLocked(id, c, allowAccount)
}
func (m *Manager) saveProfileExecutionLocked(id string, c execution.Config, allowAccount bool) error {
	a, err := m.executionProfileLocked(id)
	if err != nil {
		return err
	}
	if c.AccountID != a.AccountID || c.TradeManagerID != a.TradeManagerID {
		return errors.New("profile account or Trade Manager changed; reload this profile before saving")
	}
	status := m.executor.Status()
	if status.RunRequested && status.RunProfileID != id || status.Armed && (!status.Automated || status.AutomationProfileID != id) {
		return errors.New("pause the current execution session before selecting another profile setup")
	}
	if c.Enabled && !allowAccount {
		return errors.New("allow this profile's account to execute before enabling automation")
	}
	if err = m.saveExecutionLocked(c, id); err != nil {
		return err
	}
	if allowAccount != m.policies.Allowed(a.AccountID) {
		return m.setAccountPermissionLocked(a.AccountID, allowAccount)
	}
	return nil
}
func (m *Manager) ArmProfileAutomation(ctx context.Context, actor, password, id string, a execution.Authorization) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	profile, err := m.executionProfileLocked(id)
	if err != nil {
		return err
	}
	if m.executionConfig.AutomationProfileID != id || m.executionConfig.AccountID != profile.AccountID || m.executionConfig.TradeManagerID != profile.TradeManagerID {
		return errors.New("enable execution and save its settings on this profile first")
	}
	approved, err := m.configuredAutomationLocked()
	if err != nil {
		return err
	}
	if approved.ID != id {
		return errors.New("execution settings belong to another profile")
	}
	a.AutomationConfirmed = true
	return m.armExecutionLocked(ctx, a)
}
func (m *Manager) PauseProfileAutomation(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.executor.Status()
	if status.RunRequested && status.RunProfileID == id {
		m.executor.Pause()
		return nil
	}
	if !status.Automated || status.AutomationProfileID != id {
		if status.Armed || status.RunRequested {
			return errors.New("another execution session is armed; this profile cannot pause it")
		}
		return nil
	}
	m.executor.Pause()
	return nil
}
func (m *Manager) FlattenProfileAutomation(ctx context.Context, actor, password, id string) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, i := range m.executor.Status().Intents {
		if i.ProfileID == id && i.Source == "tradingview" && i.State != "closed" && i.State != "cancelled" && i.State != "rejected" {
			return m.executor.Flatten(ctx)
		}
	}
	return errors.New("no active bot-managed execution belongs to this profile")
}
