package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"time"

	"github.com/yelaco/projectx-pilot/internal/config"
	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/session"
)

// SaveContinuousSetup is one-time local authorization of the selected risk policy,
// selected account/Auto OCO setup and reviewed calendar. It never starts the runner.
func (m *Manager) SaveContinuousSetup(actor, password, id string, c execution.Config, allow bool, a execution.Authorization) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.executionProfileLocked(id)
	if err != nil {
		return err
	}
	if _, _, err = m.managerScheduleLocked(p.TradeManagerID).Window(m.now()); err != nil {
		return err
	}
	perTrade := m.perTradeOnlyLocked(p.TradeManagerID)
	if !a.PracticeConfirmed || !a.BracketsConfirmed || !a.Window.Verified || (!perTrade && (!a.FloorConfirmed || a.Floor <= 0 || math.IsNaN(a.Floor) || math.IsInf(a.Floor, 0))) {
		return errors.New("confirm the selected account/Auto OCO setup and reviewed calendar (legacy policies also need a verified fixed floor)")
	}
	name := ""
	if m.service != nil {
		for _, account := range m.service.Snapshot().Accounts {
			if account.ID == p.AccountID {
				name = account.Name
			}
		}
	}
	if name == "" {
		return errors.New("refresh account discovery before saving trading setup")
	}
	if err = m.saveProfileExecutionLocked(id, c, allow); err != nil {
		return err
	}
	if perTrade {
		a.Floor = 0
	}
	next := m.executionConfig
	next.RunSetup = execution.RunSetup{ProfileID: id, Scope: m.profileScope(p), Floor: a.Floor, AccountName: name, CalendarRevision: session.CalendarRevision, ConfirmedAt: m.now().UTC()}
	next.LastVerifiedFloor = a.Floor
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err = config.WritePrivate(filepath.Join(filepath.Dir(m.credentialsPath), "execution.json"), b); err != nil {
		m.executor.Pause()
		return err
	}
	m.executionConfig = next
	return nil
}
func (m *Manager) continuousReadinessLocked(id string) (Automation, error) {
	p, err := m.executionProfileLocked(id)
	if err != nil {
		return p, err
	}
	c := m.executionConfig
	s := c.RunSetup
	if !c.Enabled || c.AutomationProfileID != id || c.AccountID != p.AccountID || c.TradeManagerID != p.TradeManagerID {
		return p, errors.New("complete this profile's one-time trading setup")
	}
	if s.ProfileID != id || s.Scope != m.profileScope(p) || s.CalendarRevision != session.CalendarRevision || s.ConfirmedAt.IsZero() || (!m.perTradeOnlyLocked(p.TradeManagerID) && s.Floor <= 0) || s.AccountName == "" {
		return p, errors.New("save the Auto OCO and calendar confirmations in Trading setup (legacy policies also need a fixed floor)")
	}
	configured, err := m.configuredAutomationLocked()
	if err != nil {
		return p, err
	}
	if configured.ID != id {
		return p, errors.New("execution settings belong to another profile")
	}
	return p, nil
}
func (m *Manager) ContinuousReadiness(id string) (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.continuousReadinessLocked(id)
	if err != nil {
		return false, err.Error()
	}
	return true, "Ready. Start once; trading waits for verified market hours and respects risk limits."
}

// StartContinuousAutomation is called only by the authenticated, same-origin,
// CSRF-protected local Start form. No payload can invoke or restore this choice.
func (m *Manager) StartContinuousAutomation(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.continuousReadinessLocked(id); err != nil {
		return err
	}
	m.continuousRetryAt = time.Time{}
	if err := m.executor.RequestRun(id); err != nil {
		return err
	}
	if err := m.reconcileContinuousLocked(ctx); err != nil {
		m.executor.Pause()
		return err
	}
	if status := m.executor.Status(); !status.RunRequested {
		return errors.New(status.RunMessage)
	}
	select {
	case m.automationWake <- struct{}{}:
	default:
	}
	return nil
}
func (m *Manager) reconcileContinuousLocked(ctx context.Context) error {
	status := m.executor.Status()
	if !status.RunRequested {
		return nil
	}
	p, err := m.continuousReadinessLocked(status.RunProfileID)
	if err != nil {
		m.executor.Pause()
		m.executor.SetRunMessage(err.Error())
		return nil
	}
	if status.Armed && status.Automated && status.AutomationProfileID == p.ID {
		if err := session.OperationEntryAllowed(m.now(), m.c.NoTradeDates); err != nil {
			m.executor.SetRunMessage("Running · " + err.Error())
		} else if err := session.OperationEntryAllowed(m.now(), m.c.TradeManagerNoTradeDates[p.TradeManagerID]); err != nil {
			m.executor.SetRunMessage("Running · Trade Manager operation day disabled")
		} else if err := m.news.EntryAllowed(m.now(), m.c.NewsFilter); err != nil {
			m.executor.SetRunMessage("Running · " + err.Error())
		} else {
			m.executor.SetRunMessage("Running · accepting eligible signals")
		}
		return nil
	}
	if !m.executor.CanConfigure() {
		m.executor.SetRunMessage("Running · monitoring existing execution; new entries wait for reconciliation")
		return nil
	}
	window, open, err := m.managerScheduleLocked(p.TradeManagerID).Window(m.now())
	if err != nil {
		m.executor.SetRunMessage("Running · waiting: " + err.Error())
		return nil
	}
	if !open {
		m.executor.SetRunMessage("Running · market closed; next entry window " + window.Start.In(session.Chicago).Format("Mon Jan 2 15:04 MST"))
		return nil
	}
	lock, err := m.executionStore.ExecutionLock(fmt.Sprint(p.AccountID), window.ID())
	if err != nil {
		return err
	}
	if lock != "" {
		if lock == "session loss or trade limit reached" || lock == "daily loss limit reached" {
			m.executor.SetRunMessage("Running · session risk limit reached; waiting for the next verified session")
			return nil
		}
		m.executor.Pause()
		m.executor.SetRunMessage("Paused · account lockout requires review: " + lock)
		return nil
	}
	if m.now().Before(m.continuousRetryAt) {
		return nil
	}
	s := m.executionConfig.RunSetup
	a := execution.Authorization{Continuous: true, AutomationConfirmed: true, Window: window, Floor: s.Floor, AccountName: s.AccountName, PracticeConfirmed: true, BracketsConfirmed: true, FloorConfirmed: true}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err = m.armExecutionLocked(callCtx, a); err != nil {
		if !m.executor.Status().RunRequested {
			return nil
		}
		m.continuousRetryAt = m.now().Add(15 * time.Second)
		m.executor.SetRunMessage("Running · waiting for account readiness: " + err.Error())
		return nil
	}
	m.continuousRetryAt = time.Time{}
	m.executor.SetRunMessage("Running · accepting eligible signals")
	return nil
}

// perTradeOnlyLocked reads trusted stored policy, never an alert/form override.
func (m *Manager) perTradeOnlyLocked(id string) bool {
	for _, p := range m.policies.TradeManagers {
		if p.ID == id {
			return p.PerTradeOnly
		}
	}
	return false
}

// Retained legacy controller uses the same Trade Manager schedule as the fleet.
func (m *Manager) managerScheduleLocked(id string) session.Schedule {
	for _, p := range m.policies.TradeManagers {
		if p.ID == id {
			return p.TradingSchedule()
		}
	}
	return session.DefaultSchedule()
}
