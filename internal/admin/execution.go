package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"projectxbot/internal/config"
	"projectxbot/internal/execution"
	"projectxbot/internal/projectx"
	"projectxbot/internal/realtime"
	"projectxbot/internal/session"
	"projectxbot/internal/signal"
	"projectxbot/internal/storage"
)

func (m *Manager) executionFactory() execution.Factory {
	creds, timeout := m.credentials, m.c.RequestTimeout
	return func(account int64) (execution.API, execution.Quotes, error) {
		c, err := projectx.New(creds.Username, creds.APIKey, timeout)
		if err != nil {
			return nil, nil, err
		}
		t, err := projectx.NewTrading(c, account)
		if err != nil {
			return nil, nil, err
		}
		return t, realtime.New(c.StreamToken), nil
	}
}
func (m *Manager) openExecution() error {
	m.executionConfig = execution.Config{RoundTripCosts: 3, SlippageTicks: 2, MaxQuantity: 1, MaxDeviationTicks: 4}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(m.credentialsPath), "execution.json"))
	if err == nil {
		if json.Unmarshal(b, &m.executionConfig) != nil {
			return errors.New("invalid execution configuration")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	store, err := storage.Open(filepath.Join(filepath.Dir(m.credentialsPath), "execution.db"))
	if err != nil {
		return err
	}
	audit, err := m.activityStore()
	if err != nil {
		store.Close()
		return err
	}
	engine, err := execution.New(store, m.executionFactory(), executionAudit(audit))
	if err != nil {
		store.Close()
		return err
	}
	m.executionStore, m.executor = store, engine
	return nil
}
func (m *Manager) RunExecution(ctx context.Context)  { m.executor.Run(ctx) }
func (m *Manager) ExecutionStatus() execution.Status { return m.executor.Status() }
func (m *Manager) ExecutionConfig() execution.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.executionConfig
}
func (m *Manager) reauthorize(actor, password string) error {
	for _, a := range m.access.List() {
		if a.ID == actor {
			id, ok := m.access.Verify(a.Username, password)
			if ok && id.ID == actor {
				return nil
			}
		}
	}
	return errors.New("current administrator password required")
}
func (m *Manager) SaveExecution(actor, password string, c execution.Config) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveExecutionLocked(c, "")
}
func (m *Manager) saveExecutionLocked(c execution.Config, profileID string) error {
	m.executor.Pause()
	if !m.executor.CanConfigure() {
		return errors.New("finish or flatten the active execution before changing trading settings")
	}
	if c.AccountID <= 0 || c.TradeManagerID == "" || c.RoundTripCosts <= 0 || c.RoundTripCosts > 1000 || c.SlippageTicks < 1 || c.SlippageTicks > 100 || c.MaxQuantity < 1 || c.MaxQuantity > 100 || c.MaxDeviationTicks < 0 || c.MaxDeviationTicks > 100 {
		return errors.New("select an account, Trade Manager and bounded positive costs/slippage/quantity")
	}
	var policyFound bool
	for _, p := range m.policies.TradeManagers {
		if p.ID == c.TradeManagerID && p.Routing == "single" && p.HasAccount(c.AccountID) {
			policyFound = true
		}
	}
	if !policyFound {
		return errors.New("Trade Manager must target exactly the selected account")
	}
	if c.Enabled && m.credentials.APIKey == "" {
		return errors.New("configure ProjectX credentials first")
	}
	if c.AccountID == m.executionConfig.AccountID && c.TradeManagerID == m.executionConfig.TradeManagerID && (profileID == "" || profileID == m.executionConfig.AutomationProfileID) {
		c.RunSetup = m.executionConfig.RunSetup
		c.LastVerifiedFloor = m.executionConfig.LastVerifiedFloor
		c.AutomationProfileID = m.executionConfig.AutomationProfileID
	}
	if profileID != "" {
		c.AutomationProfileID = profileID
	}
	b, err := json.Marshal(c)
	if err != nil {
		return errors.New("invalid execution settings")
	}
	next := m.c
	next.Mode = "read-only"
	if c.Enabled {
		next.Mode = "manual-approval"
	}
	// Either interrupted write remains safe: both gates are required when arming.
	if err = config.Save(m.configPath, next); err != nil {
		return err
	}
	m.c = next
	if err = config.WritePrivate(filepath.Join(filepath.Dir(m.credentialsPath), "execution.json"), b); err != nil {
		return err
	}
	m.executionConfig = c
	return nil
}
func (m *Manager) ArmExecution(ctx context.Context, actor, password string, a execution.Authorization) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.armExecutionLocked(ctx, a)
}
func (m *Manager) armExecutionLocked(ctx context.Context, a execution.Authorization) error {
	if (m.c.Mode != "manual-approval" && m.c.Mode != "automated") || !m.executionConfig.Enabled {
		return errors.New("enable local execution first")
	}
	granted := false
	for _, p := range m.policies.Accounts {
		if p.AccountID == m.executionConfig.AccountID && p.ExecutionAllowed {
			granted = true
		}
	}
	if !granted {
		return errors.New("grant execution eligibility for the pinned Practice account on Accounts first")
	}
	if a.AccountName == "" && m.service != nil {
		for _, account := range m.service.Snapshot().Accounts {
			if account.ID == m.executionConfig.AccountID {
				a.AccountName = account.Name
			}
		}
	}
	for _, p := range m.policies.TradeManagers {
		if p.ID == m.executionConfig.TradeManagerID {
			c := m.executionConfig
			c.SignalMaxAge = m.c.SignalMaxAge
			next := m.c
			next.Mode = "manual-approval"
			if a.AutomationConfirmed {
				profile, err := m.configuredAutomationLocked()
				if err != nil {
					return err
				}
				c.Automated = true
				c.AutomationContractID = profile.ContractID
				next.Mode = "automated"
			}
			if next.Mode != m.c.Mode {
				if err := config.Save(m.configPath, next); err != nil {
					return err
				}
				m.c = next
			}
			err := m.executor.Arm(ctx, c, p, a)
			if err == nil {
				nextConfig := m.executionConfig
				nextConfig.LastVerifiedFloor = a.Floor
				b, saveErr := json.Marshal(nextConfig)
				if saveErr == nil {
					saveErr = config.WritePrivate(filepath.Join(filepath.Dir(m.credentialsPath), "execution.json"), b)
				}
				if saveErr != nil {
					m.executor.Pause()
					return errors.New("could not save verified practice defaults; execution remains disarmed")
				}
				m.executionConfig = nextConfig
			}
			if err == nil && c.Automated {
				select {
				case m.automationWake <- struct{}{}:
				default:
				}
			}
			return err
		}
	}
	return errors.New("assigned Trade Manager not found")
}
func (m *Manager) PauseExecution() { m.executor.Pause() }
func (m *Manager) PreviewExecution(ctx context.Context, profileID, contract, payload string) error {
	s, err := signal.Parse(payload)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.c.Mode != "manual-approval" || !m.executionConfig.Enabled {
		return errors.New("local execution is disabled")
	}
	var profile *Automation
	for _, p := range m.policies.Automations {
		if p.ID == profileID {
			v := p
			profile = &v
		}
	}
	if profile == nil || profile.Source != "local-manual" || profile.AccountID != m.executionConfig.AccountID || profile.TradeManagerID != m.executionConfig.TradeManagerID || profile.Strategy != s.Strategy {
		return errors.New("use a local-manual automation assigned to the pinned account and Trade Manager")
	}
	allowed := func(list []string) bool {
		for _, v := range list {
			if v == s.Symbol {
				return true
			}
		}
		return false
	}
	if !allowed(m.c.AllowedInstruments) || !allowed(profile.Instruments) {
		return errors.New("instrument is not allowed")
	}
	if strings.TrimSpace(contract) == "" {
		return errors.New("select an actual contract expiry")
	}
	if err = s.ValidateSchema(time.Now().UTC(), m.c.SignalMaxAge); err != nil {
		return err
	}
	_, err = m.executor.Prepare(ctx, s, contract)
	return err
}
func (m *Manager) SubmitExecution(ctx context.Context, actor, password, token string) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.c.Mode != "manual-approval" || !m.executionConfig.Enabled {
		return errors.New("execution is disabled")
	}
	return m.executor.Submit(ctx, token)
}
func (m *Manager) FlattenExecution(ctx context.Context, actor, password string) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	return m.executor.Flatten(ctx)
}

// PreviewTicket is a local dashboard operation, never an ingress endpoint.
func (m *Manager) PreviewTicket(ctx context.Context, contract, direction string, orderType, quantity, stopTicks, targetTicks int, entry float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.c.Mode != "manual-approval" || !m.executionConfig.Enabled {
		return errors.New("enable and arm local execution first")
	}
	if quantity < 1 || quantity > 100 {
		return errors.New("quantity must be 1..100 whole contracts")
	}
	var symbol string
	if m.service != nil {
		for _, c := range m.service.Snapshot().Contracts {
			if c.ID == contract {
				for _, root := range m.c.AllowedInstruments {
					if c.ActiveContract && strings.HasSuffix(c.SymbolID, "."+root) {
						symbol = root
					}
				}
			}
		}
	}
	allowed := false
	for _, root := range m.c.AllowedInstruments {
		if root == symbol {
			allowed = true
		}
	}
	if symbol == "" || !allowed {
		return errors.New("select a discovered active contract allowed by configuration")
	}
	s, err := m.executor.TicketSignal(ctx, "practice-api-test", symbol, direction, contract, orderType, entry, stopTicks, targetTicks)
	if err != nil {
		return err
	}
	_, err = m.executor.PrepareOrder(ctx, s, contract, orderType, quantity)
	return err
}

// AcquireExecutionLease prevents two native services from monitoring or mutating
// the same durable intents. Call before starting any worker or listener.
func (m *Manager) AcquireExecutionLease() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.executionLease != nil {
		return errors.New("execution monitor lease already held")
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(m.credentialsPath), "execution.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return errors.New("another bot process owns this state directory; stop it before starting this service")
	}
	// Re-read intents after obtaining exclusive process ownership.
	audit, err := m.activityStore()
	if err != nil {
		f.Close()
		return err
	}
	engine, err := execution.New(m.executionStore, m.executionFactory(), executionAudit(audit))
	if err != nil {
		f.Close()
		return err
	}
	m.executor = engine
	m.executionLease = f
	return nil
}

func executionAudit(audit *storage.Inbox) func(string, string) {
	return func(level, message string) {
		_ = audit.RecordActivity(level, "execution", message)
		severity := slog.LevelInfo
		if level == "ERROR" {
			severity = slog.LevelError
		}
		slog.Log(context.Background(), severity, "execution lifecycle", "event", message)
	}
}

func (m *Manager) ReviewOneWayOCOLock(ctx context.Context, actor, password, intentID string) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.executionConfig.AccountID <= 0 {
		return errors.New("select the Practice execution account first")
	}
	reviewCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return m.executor.ReviewOneWayOCOLock(reviewCtx, m.executionConfig.AccountID, intentID)
}

// ExecutionLockInfo reports the application's durable lock, never firm permission.
func (m *Manager) ExecutionLockInfo() (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	date := session.CurrentTradingDate(m.now())
	if m.executionConfig.AccountID <= 0 {
		return date, "", nil
	}
	reason, err := m.executionStore.ExecutionLock(strconv.FormatInt(m.executionConfig.AccountID, 10), date)
	return date, reason, err
}
