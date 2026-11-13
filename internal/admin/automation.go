package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"projectxbot/internal/automation"
	"projectxbot/internal/storage"
)

// profileScope binds saved trading setup to the exact source, profile,
// account/owner, strategy and instrument set. Edits invalidate it.
func (m *Manager) profileScope(a Automation) string {
	b, _ := json.Marshal(struct {
		Owner   string
		Profile Automation
	}{m.credentials.Username, a})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (m *Manager) configuredAutomationLocked() (Automation, error) {
	c := m.executionConfig
	for _, a := range m.policies.Automations {
		if a.ID == c.AutomationProfileID {
			if a.Source != "tradingview-manual" || a.ExecutionMode != "automatic" || a.AccountID != c.AccountID || a.TradeManagerID != c.TradeManagerID || !m.policies.Allowed(a.AccountID) {
				return a, errors.New("account eligibility or automatic profile configuration is missing or changed")
			}
			return a, nil
		}
	}
	return Automation{}, errors.New("select a configured automatic profile")
}

func (m *Manager) AutomationResults() ([]storage.AutomationReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.automationResultsLocked()
}
func (m *Manager) automationResultsLocked() ([]storage.AutomationReceipt, error) {
	rows, err := m.executionStore.AutomationReceipts()
	if err != nil {
		return nil, err
	}
	// The manager lock excludes an in-flight dispatcher. A leftover processing
	// claim can now be resolved for display without ever replaying its alert.
	var intents []storage.ExecRow
	for n := range rows {
		if rows[n].State != "processing" {
			continue
		}
		if intents == nil {
			intents, err = m.executionStore.Executions()
			if err != nil {
				return nil, err
			}
		}
		state, message := "interrupted", "Dispatch ended before any durable order intent; this alert will not replay."
		for _, i := range intents {
			if i.ID == rows[n].ID {
				state = "uncertain"
				message = "Durable intent exists; follow order reconciliation. Alert will not replay."
			}
		}
		if err = m.executionStore.FinishAutomation(rows[n].ID, state, message); err != nil {
			return nil, err
		}
		rows[n].State, rows[n].Message = state, message
	}
	return rows, nil
}
func (m *Manager) RunAutomation(ctx context.Context) {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-m.automationWake:
		}
		err := m.processAutomation(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.executor.Pause()
			m.executor.SetRunMessage("Dispatch error: " + err.Error())
			m.Note("ERROR", "automation", "Automatic dispatch paused: "+err.Error())
		}
		delay := 5 * time.Second
		status := m.executor.Status()
		if status.RunRequested || status.Armed && status.Automated {
			delay = time.Second
		}
		timer.Reset(delay)
	}
}
func (m *Manager) processAutomation(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reconcileContinuousLocked(ctx); err != nil {
		return err
	}
	var automatic []Automation
	for _, p := range m.policies.Automations {
		if p.ExecutionMode == "automatic" {
			automatic = append(automatic, p)
		}
	}
	if len(automatic) == 0 {
		return nil
	}
	remote, err := m.remoteStore()
	if err != nil {
		return err
	}
	if _, err = m.automationResultsLocked(); err != nil {
		return err
	}
	status := m.executor.Status()
	// Disarmed and pre-arming deliveries are consumed as blocked outcomes,
	// never held for a later arming. Review-only profiles are untouched.
	for _, p := range automatic {
		pending, err := remote.PendingSince(time.Time{}, p.ID)
		if err != nil {
			return err
		}
		for _, r := range pending {
			received, err := time.Parse(time.RFC3339Nano, r.ReceivedAt)
			if err != nil {
				return err
			}
			if status.Armed && status.Automated && status.AutomationProfileID == p.ID && !received.Before(status.ArmedAt) {
				continue
			}
			receipt := storage.AutomationReceipt{ID: r.Signal.ID, ProfileID: p.ID, AccountID: fmt.Sprint(p.AccountID), ContractID: p.ContractID}
			if err = m.executionStore.ClaimAutomation(receipt); err == nil {
				if err = m.executionStore.FinishAutomation(r.Signal.ID, "blocked", "Automatic execution was not armed for this profile when the alert arrived; it will not replay."); err != nil {
					return err
				}
			} else if !errors.Is(err, storage.ErrDuplicate) {
				return err
			}
			result, err := m.executionStore.AutomationReceipt(r.Signal.ID)
			if err != nil {
				return err
			}
			if err = m.finishDeliveryLocked(remote, r.Signal.ID, result.State); err != nil {
				return err
			}
		}
	}
	if !status.Armed || !status.Automated {
		return nil
	}
	if m.c.Mode != "automated" || !m.executionConfig.Enabled {
		return errors.New("automatic mode is disabled")
	}
	profile, err := m.configuredAutomationLocked()
	if err != nil {
		return err
	}
	rows, err := remote.PendingSince(status.ArmedAt, profile.ID)
	if err != nil {
		return err
	}
	typ := 2
	if profile.OrderType == "limit" {
		typ = 1
	}
	binding := automation.Binding{ProfileID: profile.ID, ContractID: profile.ContractID, Strategy: profile.Strategy, AccountID: profile.AccountID, Instruments: profile.Instruments, OrderType: typ, Approved: true}
	dispatcher := automation.Dispatcher{Engine: m.executor, Store: m.executionStore, Now: m.now}
	for _, r := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.ProfileID != profile.ID {
			continue
		}
		received, err := time.Parse(time.RFC3339Nano, r.ReceivedAt)
		if err != nil {
			return err
		}
		if received.Before(status.ArmedAt) {
			continue
		}
		if !m.executor.Status().Armed {
			break
		}
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = dispatcher.Process(callCtx, binding, r)
		cancel()
		if err != nil {
			return err
		}
		receipt, err := m.executionStore.AutomationReceipt(r.Signal.ID)
		if err != nil {
			return err
		}
		if err = m.finishDeliveryLocked(remote, r.Signal.ID, receipt.State); err != nil {
			return err
		}
		// Audit only sanitized, engine-owned outcome text; never payloads/tokens.
		if audit, err := m.activityStore(); err == nil {
			_ = audit.RecordActivity("INFO", "automation", "Alert outcome: "+receipt.State+". "+receipt.Message)
		}
	}
	return nil
}

func (m *Manager) UpdateAutomationExecution(id, mode, contract, orderType string, managerID ...string) error {
	if len(managerID) > 1 {
		return errors.New("select one Trade Manager")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p := clonePolicies(m.policies)
	for n := range p.Automations {
		if p.Automations[n].ID == id {
			if len(managerID) == 1 {
				selected := managerID[0]
				if selected != "" {
					found := false
					for _, manager := range p.TradeManagers {
						if manager.ID == selected {
							found = true
							if manager.Routing != "single" {
								return errors.New("choose a Trade Manager with single-account routing")
							}
							p.Automations[n].AccountID = manager.AccountIDs[0]
						}
					}
					if !found {
						return errors.New("selected Trade Manager was not found")
					}
				}
				p.Automations[n].TradeManagerID = selected
			}
			p.Automations[n].ExecutionMode = mode
			p.Automations[n].ContractID = contract
			p.Automations[n].OrderType = orderType
			if err := m.savePolicies(p); err != nil {
				return err
			}
			return m.syncIngressLocked()
		}
	}
	return errors.New("automation profile not found")
}
func (m *Manager) AutomationReadiness() (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.configuredAutomationLocked()
	if err != nil {
		return false, err.Error()
	}
	return true, "Profile routing and account eligibility match; local Start is still required."
}

func (m *Manager) finishDeliveryLocked(remote *storage.Inbox, id, state string) error {
	status := "rejected"
	if state == "submitted" || state == "uncertain" {
		status = "reviewed"
	}
	if err := remote.Review(id, status); err != nil && !errors.Is(err, storage.ErrTransition) {
		return err
	}
	return nil
}
