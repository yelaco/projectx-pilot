package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/yelaco/projectx-pilot/internal/config"
	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/fleet"
	"github.com/yelaco/projectx-pilot/internal/risk"
	"github.com/yelaco/projectx-pilot/internal/session"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

func (m *Manager) setupPath() string {
	return filepath.Join(filepath.Dir(m.credentialsPath), "automation-setups.json")
}
func (m *Manager) initFleet() error {
	m.profileSetups = map[string]fleet.Plan{}
	b, err := os.ReadFile(m.setupPath())
	if err == nil {
		if len(b) > 1<<20 || json.Unmarshal(b, &m.profileSetups) != nil {
			return errors.New("invalid per-profile trading setup store")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// Migration preserves configuration only; no recovered profile is started.
	if len(m.profileSetups) == 0 && m.executionConfig.RunSetup.ProfileID != "" {
		id := m.executionConfig.RunSetup.ProfileID
		if p, err := m.planLocked(id); err == nil && len(p.Targets()) == 1 && m.executionConfig.RunSetup.Scope == m.profileScope(func() Automation {
			for _, a := range m.policies.Automations {
				if a.ID == id {
					return a
				}
			}
			return Automation{}
		}()) && m.executionConfig.RunSetup.CalendarRevision == session.CalendarRevision {
			p.Config = m.executionConfig
			p.Config.SignalMaxAge = m.c.SignalMaxAge
			p.Config.NewsFilter = m.c.NewsFilter
			p.Config.Automated = true
			p.Config.AutomationProfileID = id
			p.Config.AutomationContractID = p.ContractID
			p.CalendarRevision = session.CalendarRevision
			p.ConfirmedAt = m.executionConfig.RunSetup.ConfirmedAt
			p.Floor = m.executionConfig.RunSetup.Floor
			p.Scope = m.fleetScopeLocked(p)
			m.profileSetups[id] = p
		}
	}
	audit, err := m.activityStore()
	if err != nil {
		return err
	}
	m.fleet, err = fleet.New(m.executionStore, m.executionFactory(), m.news, m.now, m.executionAudit(audit))
	if err != nil {
		return err
	}
	return m.fleet.SetOperationPlan(m.c.NoTradeDates, m.c.TradeManagerNoTradeDates)
}
func (m *Manager) planLocked(id string) (fleet.Plan, error) {
	for _, p := range m.policies.Automations {
		if p.ID != id {
			continue
		}
		if p.ExecutionMode != "automatic" || p.Source != "tradingview-manual" || p.ContractID == "" || (p.OrderType != "market" && p.OrderType != "limit") {
			return fleet.Plan{}, errors.New("configure automatic webhook handling, contract and entry order type")
		}
		for _, manager := range m.policies.TradeManagers {
			if manager.ID == p.TradeManagerID {
				typ := 2
				if p.OrderType == "limit" {
					typ = 1
				}
				return fleet.Plan{ProfileID: id, Strategy: p.Strategy, ContractID: p.ContractID, Instruments: append([]string(nil), p.Instruments...), Policy: manager.Clone(), OrderType: typ}, nil
			}
		}
		return fleet.Plan{}, errors.New("assign a Trade Manager with explicit target accounts")
	}
	return fleet.Plan{}, errors.New("automation profile not found")
}
func (m *Manager) fleetScopeLocked(p fleet.Plan) string {
	b, _ := json.Marshal(struct {
		Owner   string
		Profile Automation
		Policy  risk.Profile
	}{Owner: m.credentials.Username, Policy: p.Policy, Profile: func() Automation {
		for _, a := range m.policies.Automations {
			if a.ID == p.ProfileID {
				return a
			}
		}
		return Automation{}
	}()})
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}
func (m *Manager) SaveProfileSetup(actor, password, id string, c execution.Config, allow bool, a execution.Authorization) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.planLocked(id)
	if err != nil {
		return err
	}
	expected := int64(0)
	if len(p.Targets()) == 1 {
		expected = p.Targets()[0]
	}
	if c.AccountID != expected {
		return errors.New("profile target changed; reload this page before saving")
	}
	if c.TradeManagerID != p.Policy.ID {
		return errors.New("setup Trade Manager differs from this profile")
	}
	if !a.PracticeConfirmed || !a.BracketsConfirmed || !a.Window.Verified {
		return errors.New("confirm selected account(s), Auto OCO and the session calendar")
	}
	if _, _, err := p.Policy.TradingSchedule().Window(m.now()); err != nil {
		return err
	}
	if c.RoundTripCosts <= 0 || math.IsNaN(c.RoundTripCosts) || math.IsInf(c.RoundTripCosts, 0) || c.RoundTripCosts > 1000 || c.SlippageTicks < 1 || c.SlippageTicks > 100 || c.MaxQuantity < 1 || c.MaxQuantity > 100 || c.MaxDeviationTicks < 0 || c.MaxDeviationTicks > 100 {
		return errors.New("invalid cost, slippage, quantity or deviation setting")
	}
	if !p.Policy.PerTradeOnly {
		if len(p.Targets()) > 1 {
			return errors.New("Multi-account routing needs per-trade-only risk; save the Trade Manager using per-trade-only before setup")
		}
		if !a.FloorConfirmed || a.Floor <= 0 || math.IsNaN(a.Floor) || math.IsInf(a.Floor, 0) {
			return errors.New("legacy policy requires a verified fixed floor")
		}
		p.Floor = a.Floor
	}
	if c.Enabled && m.credentials.APIKey == "" {
		return errors.New("configure ProjectX credentials first")
	}
	// Grant/revoke the complete target set, never silently reduce multi-account scope.
	policies := clonePolicies(m.policies)
	for _, account := range p.Targets() {
		if allow && !m.policies.Allowed(account) {
			if m.service == nil {
				return errors.New("refresh account discovery before granting targets")
			}
			fresh := m.service.Snapshot()
			if !fresh.Connected || m.now().Sub(fresh.UpdatedAt) > 2*m.c.RefreshInterval {
				return errors.New("fresh account discovery required")
			}
			found := false
			for _, v := range fresh.Accounts {
				if v.ID == account && v.CanTrade {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("account #%d is missing or canTrade=false", account)
			}
		}
		found := false
		for n := range policies.Accounts {
			if policies.Accounts[n].AccountID == account {
				policies.Accounts[n].ExecutionAllowed = allow
				found = true
			}
		}
		if !found {
			policies.Accounts = append(policies.Accounts, AccountPolicy{AccountID: account, ExecutionAllowed: allow})
		}
	}
	m.fleet.PauseProfile(id)
	if err := m.savePolicies(policies); err != nil {
		return err
	}
	c.SignalMaxAge = m.c.SignalMaxAge
	c.NewsFilter = m.c.NewsFilter
	c.Automated = true
	c.AutomationProfileID = id
	c.AutomationContractID = p.ContractID
	p.Config = c
	p.Config.AccountID = 0
	if len(p.Targets()) == 1 {
		p.Config.AccountID = p.Targets()[0]
	}
	p.CalendarRevision = session.CalendarRevision
	p.ConfirmedAt = m.now().UTC()
	p.Scope = m.fleetScopeLocked(p)
	next := map[string]fleet.Plan{}
	for k, v := range m.profileSetups {
		next[k] = v
	}
	next[id] = p
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err = config.WritePrivate(m.setupPath(), b); err != nil {
		return errors.New("could not save profile setup")
	}
	nextConfig := m.c
	if c.Enabled {
		nextConfig.Mode = "automated"
	}
	if err := config.Save(m.configPath, nextConfig); err != nil {
		return err
	}
	m.c = nextConfig
	m.profileSetups = next
	return nil
}
func (m *Manager) FleetReadiness(id string) (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.readyPlanLocked(id)
	if err != nil {
		return false, err.Error()
	}
	return true, "Ready. Each selected account has an independent runner; Start reserves the complete target set."
}
func (m *Manager) readyPlanLocked(id string) (fleet.Plan, error) {
	p, err := m.planLocked(id)
	if err != nil {
		return p, err
	}
	saved, ok := m.profileSetups[id]
	if !ok || !saved.Config.Enabled || saved.ConfirmedAt.IsZero() {
		return p, errors.New("complete this profile's trading setup")
	}
	if saved.CalendarRevision != session.CalendarRevision {
		return p, errors.New("reviewed session calendar changed; confirm this profile’s trading setup again")
	}
	if saved.Scope != m.fleetScopeLocked(p) {
		return p, errors.New("profile/account/risk scope changed; save its trading setup again")
	}
	for _, a := range p.Targets() {
		if !m.policies.Allowed(a) {
			return p, fmt.Errorf("account #%d does not have execution permission", a)
		}
	}
	if m.c.Mode != "automated" && m.c.Mode != "manual-approval" {
		return p, errors.New("automatic execution is disabled in configuration")
	}
	p.Config = saved.Config
	p.Config.SignalMaxAge = m.c.SignalMaxAge
	p.Config.NewsFilter = m.c.NewsFilter
	p.Scope = saved.Scope
	p.ConfirmedAt = saved.ConfirmedAt
	p.Floor = saved.Floor
	return p, nil
}
func (m *Manager) StartProfile(ctx context.Context, id string) error {
	m.mu.Lock()
	p, err := m.readyPlanLocked(id)
	f := m.fleet
	if err == nil && m.c.Mode != "automated" {
		next := m.c
		next.Mode = "automated"
		err = config.Save(m.configPath, next)
		if err == nil {
			m.c = next
		}
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	// A manual engine may manage a separate account, but never share ownership.
	manual := m.executor.Status()
	hasActive := false
	for _, i := range manual.Intents {
		if i.State != "closed" && i.State != "cancelled" && i.State != "rejected" {
			hasActive = true
		}
	}
	if !hasActive {
		for _, account := range p.Targets() {
			if manual.AccountID == account && manual.Armed {
				m.executor.Pause()
				break
			}
		}
	}
	err = f.Start(ctx, p, func(account int64) bool {
		s := m.executor.Status()
		if s.AccountID == account && (s.Armed || s.RunRequested) {
			return true
		}
		for _, i := range s.Intents {
			if i.AccountID == account && i.State != "closed" && i.State != "cancelled" && i.State != "rejected" {
				return true
			}
		}
		return false
	})
	if err == nil {
		m.refreshUserAccounts()
		m.WakeAutomation()
	}
	return err
}
func (m *Manager) PauseProfile(id string) error { m.fleet.PauseProfile(id); return nil }
func (m *Manager) FlattenProfile(ctx context.Context, actor, password, id string) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	results := m.fleet.Flatten(ctx, id, 0)
	if len(results) == 0 {
		return errors.New("no account runners belong to this profile")
	}
	for account, message := range results {
		m.Note("WARN", "execution", fmt.Sprintf("Account #%d profile flatten: %s", account, message))
	}
	m.Note("WARN", "execution", "Profile flatten requested; inspect each account runner for verification")
	return nil
}
func (m *Manager) GlobalPause() error  { err := m.fleet.PauseAll(true); m.executor.Pause(); return err }
func (m *Manager) GlobalResume() error { return m.fleet.Resume() }
func (m *Manager) GlobalFlatten(ctx context.Context, actor, password string) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	if err := m.GlobalPause(); err != nil {
		return err
	}
	results := m.fleet.Flatten(ctx, "", 0)
	for account, message := range results {
		m.Note("WARN", "execution", fmt.Sprintf("Account #%d flatten: %s", account, message))
	}
	for _, i := range m.executor.Status().Intents {
		if i.State != "closed" && i.State != "cancelled" && i.State != "rejected" {
			return m.executor.Flatten(ctx)
		}
	}
	return nil
}
func (m *Manager) RunnerSnapshot() ([]fleet.View, bool) { return m.fleet.Snapshot() }
func (m *Manager) ProfileConfig(id string) execution.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.profileSetups[id]; ok {
		return p.Config
	}
	return execution.Config{RoundTripCosts: 3, SlippageTicks: 2, MaxQuantity: 1, MaxDeviationTicks: 4}
}
func (m *Manager) ProfileStatus(id string) execution.Status {
	views, _ := m.fleet.Snapshot()
	s := execution.Status{Intents: []execution.Intent{}, Automated: true, AutomationProfileID: id, RunProfileID: id}
	for _, r := range views {
		for _, i := range r.Status.Intents {
			if i.ProfileID == id {
				s.Intents = append(s.Intents, i)
			}
		}
		if r.ProfileID != id {
			continue
		}
		s.RunRequested = s.RunRequested || r.Status.RunRequested
		s.Armed = s.Armed || r.Status.Armed
		s.RunMessage = "Account runners active; inspect each account on Execution runners"
		if !r.Status.Armed {
			s.RunMessage = "Some targets are waiting or blocked; inspect Execution runners"
		}
		if r.Status.SubmissionDetail != nil {
			s.SubmissionDetail = r.Status.SubmissionDetail
		}
	}
	return s
}
func (m *Manager) pauseChangedProfiles(next Policies) {
	if m.fleet == nil {
		return
	}
	for _, old := range m.policies.Automations {
		changed := true
		for _, p := range next.Automations {
			if p.ID == old.ID && reflect.DeepEqual(p, old) {
				changed = false
			}
		}
		for _, oldPolicy := range m.policies.TradeManagers {
			if oldPolicy.ID != old.TradeManagerID {
				continue
			}
			found := false
			for _, p := range next.TradeManagers {
				if p.ID == oldPolicy.ID && reflect.DeepEqual(p, oldPolicy) {
					found = true
				}
			}
			if !found {
				changed = true
			}
			for _, account := range oldPolicy.AccountIDs {
				if m.policies.Allowed(account) && !next.Allowed(account) {
					changed = true
				}
			}
		}
		if changed || next.CredentialOwner != m.policies.CredentialOwner {
			m.fleet.PauseProfile(old.ID)
		}
	}
}

// processFleet claims fanout while holding only short-lived configuration locks.
// Network execution is performed by account workers and never under m.mu.
func (m *Manager) processFleet(ctx context.Context) error {
	m.mu.Lock()
	remote, err := m.remoteStore()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	type route struct {
		plan    fleet.Plan
		profile Automation
	}
	routes := []route{}
	for _, a := range m.policies.Automations {
		if a.ExecutionMode != "automatic" {
			continue
		}
		p, err := m.planLocked(a.ID)
		if err != nil {
			continue
		}
		p.Config.SignalMaxAge = m.c.SignalMaxAge
		if saved, ok := m.profileSetups[a.ID]; ok {
			p.Config = saved.Config
			p.Scope = saved.Scope
			p.ConfirmedAt = saved.ConfirmedAt
		}
		if ready, err := m.readyPlanLocked(a.ID); err != nil {
			p.Scope = "not-authorized"
		} else {
			p = ready
		}
		routes = append(routes, route{p, a})
	}
	m.mu.Unlock()
	for _, route := range routes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		records, err := remote.PendingSince(time.Time{}, route.profile.ID)
		if err != nil {
			return err
		}
		if len(records) >= 20 {
			m.WakeAutomation()
		}
		for _, r := range records {
			if err := m.fleet.Dispatch(r, route.plan); err != nil {
				return err
			}
			if err := remote.Review(r.Signal.ID, "reviewed"); err != nil && !errors.Is(err, storage.ErrTransition) {
				return err
			}
		}
	}
	return nil
}

// runtime coordinates manual recovery and all account-specific automatic monitors.
func (m *Manager) runExecutionFleet(ctx context.Context) {
	var wg sync.WaitGroup
	if m.notifications != nil {
		wg.Add(1)
		go func() { defer wg.Done(); m.notifications.Run(ctx) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); m.fleet.Run(ctx) }()
	m.executor.Run(ctx)
	wg.Wait()
}

func (m *Manager) RuntimeStatus() execution.Status {
	s := m.executor.Status()
	views, _ := m.fleet.Snapshot()
	for _, r := range views {
		s.RunRequested = s.RunRequested || r.Status.RunRequested
		s.Armed = s.Armed || r.Status.Armed
		s.Intents = append(s.Intents, r.Status.Intents...)
	}
	sort.SliceStable(s.Intents, func(i, j int) bool {
		a, b := s.Intents[i], s.Intents[j]
		active := func(state string) bool { return state != "closed" && state != "cancelled" && state != "rejected" }
		if active(a.State) != active(b.State) {
			return active(a.State)
		}
		return a.Updated.After(b.Updated)
	})
	return s
}
func (m *Manager) ProfileStatuses() map[string]execution.Status {
	views, _ := m.fleet.Snapshot()
	out := map[string]execution.Status{}
	for _, r := range views {
		if r.ProfileID == "" {
			continue
		}
		s := out[r.ProfileID]
		s.Automated = true
		s.AutomationProfileID = r.ProfileID
		s.RunProfileID = r.ProfileID
		s.RunRequested = s.RunRequested || r.Status.RunRequested
		s.Armed = s.Armed || r.Status.Armed
		s.RunMessage = "Account runners active; inspect per-account readiness"
		s.Intents = append(s.Intents, r.Status.Intents...)
		out[r.ProfileID] = s
	}
	return out
}
func (m *Manager) PauseRunner(account int64) error { m.fleet.PauseAccount(account); return nil }
func (m *Manager) FlattenRunner(ctx context.Context, actor, password string, account int64) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	results := m.fleet.Flatten(ctx, "", account)
	if len(results) == 0 {
		return errors.New("runner account not found")
	}
	for id, message := range results {
		m.Note("WARN", "execution", fmt.Sprintf("Account #%d flatten: %s", id, message))
	}
	return nil
}
func (m *Manager) TargetResults() ([]storage.TargetJob, error) { return m.executionStore.TargetJobs() }

func (m *Manager) FleetConfigured() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.profileSetups {
		if p.Config.Enabled {
			return true
		}
	}
	return false
}

func (m *Manager) WaitRuntime(ctx context.Context) error {
	if err := m.fleet.WaitReady(ctx); err != nil {
		return err
	}
	return m.executor.WaitReady(ctx)
}

func (m *Manager) ReviewRunnerOCOLock(ctx context.Context, actor, password string, account int64, intent string) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	return m.fleet.ReviewOCOLock(ctx, account, intent)
}

func (m *Manager) ReviewRunnerStaleQuoteLock(ctx context.Context, actor, password string, account int64, intent string) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	return m.fleet.ReviewStaleQuoteLock(ctx, account, intent)
}

func (m *Manager) ProfileFloor(id string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.profileSetups[id].Floor
}
