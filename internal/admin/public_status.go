package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"time"

	"github.com/yelaco/projectx-pilot/internal/config"
	"github.com/yelaco/projectx-pilot/internal/publicstatus"
)

type PublicAccountChoice struct {
	ID                              int64
	Alias, Since                    string
	Selected                        bool
	StartingBalance, NetAdjustments string
	LifetimeConfirmed               bool
}

func (m *Manager) publicPath() string {
	return filepath.Join(filepath.Dir(m.credentialsPath), "public-status.json")
}
func (m *Manager) openPublicStatus() error {
	b, err := os.ReadFile(m.publicPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(b) > 32768 || json.Unmarshal(b, &m.publicSettings) != nil {
		return errors.New("invalid public status settings")
	}
	if m.publicSettings.Enabled && m.publicSettings.Origin == m.c.RemoteDashboard.Origin {
		return errors.New("public status must use a separate hostname from the administrator dashboard")
	}
	return m.publicSettings.Validate(m.now())
}

// automatedAccountsLocked uses configured automatic profiles, never all discovered accounts.
func (m *Manager) automatedAccountsLocked() map[int64]bool {
	ids := map[int64]bool{}
	for _, a := range m.policies.Automations {
		if a.ExecutionMode != "automatic" {
			continue
		}
		if a.TradeManagerID != "" {
			for _, p := range m.policies.TradeManagers {
				if p.ID == a.TradeManagerID {
					for _, id := range p.AccountIDs {
						if id > 0 {
							ids[id] = true
						}
					}
				}
			}
		} else if a.AccountID > 0 {
			ids[a.AccountID] = true
		}
	}
	return ids
}
func (m *Manager) PublicSettings() (publicstatus.Settings, []PublicAccountChoice) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.publicSettings
	s.Accounts = clonePublicAccounts(s.Accounts)
	ids := m.automatedAccountsLocked()
	choices := []PublicAccountChoice{}
	for id := range ids {
		c := PublicAccountChoice{ID: id, Alias: "Automated account"}
		for _, a := range s.Accounts {
			if a.AccountID == id {
				c.Alias, c.Since, c.Selected = a.Alias, a.Since, true
				c.LifetimeConfirmed = a.LifetimeConfirmed
				if a.StartingBalance != nil {
					c.StartingBalance = strconv.FormatFloat(*a.StartingBalance, 'f', 2, 64)
				}
				if a.NetAdjustments != nil {
					c.NetAdjustments = strconv.FormatFloat(*a.NetAdjustments, 'f', 2, 64)
				}
			}
		}
		choices = append(choices, c)
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].ID < choices[j].ID })
	for i := range choices {
		if !choices[i].Selected {
			choices[i].Alias = fmt.Sprintf("Account %c", 'A'+i)
		}
	}
	return s, choices
}
func (m *Manager) SavePublicStatus(actor, password string, s publicstatus.Settings) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.automatedAccountsLocked()
	s.Accounts = clonePublicAccounts(s.Accounts)
	for i := range s.Accounts {
		a := &s.Accounts[i]
		if !ids[a.AccountID] {
			return errors.New("only accounts assigned to automatic profiles may be published")
		}
		a.Slug = ""
		for _, old := range m.publicSettings.Accounts {
			if old.AccountID == a.AccountID {
				a.Slug = old.Slug
			}
		}
		if a.Slug == "" {
			var b [12]byte
			if _, err := rand.Read(b[:]); err != nil {
				return errors.New("could not create public account identifier")
			}
			a.Slug = hex.EncodeToString(b[:])
		}
	}
	if s.Enabled && s.Origin == m.c.RemoteDashboard.Origin {
		return errors.New("use a separate public hostname from the administrator dashboard")
	}
	if err := s.Validate(m.now()); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err = config.WritePrivate(m.publicPath(), b); err != nil {
		return errors.New("could not save public status settings")
	}
	m.publicSettings = s
	m.publicSnapshot = publicstatus.Snapshot{Accounts: []publicstatus.Account{}}
	return nil
}

// PublicSnapshot is a local cache read. Public visitors never initiate broker I/O.
func (m *Manager) PublicSnapshot() (publicstatus.Settings, publicstatus.Snapshot) {
	m.mu.Lock()
	s := m.publicSettings
	cached := m.publicSnapshot
	ids := m.automatedAccountsLocked()
	sameCredentials := m.publicCredentials == m.credentials
	states := m.publicStates
	stateAt := m.publicStateAt
	m.mu.Unlock()
	out := publicstatus.Snapshot{CapturedAt: cached.CapturedAt, Accounts: []publicstatus.Account{}}
	for _, a := range s.Accounts {
		if !ids[a.AccountID] {
			continue
		}
		row := publicstatus.Account{TotalStatus: "Lifetime unavailable: data not yet collected", Slug: a.Slug, Alias: a.Alias, State: states[a.AccountID], TotalSince: a.Since, Trades: []publicstatus.Trade{}}
		row.TodayStart, _ = publicstatus.TodayWindow(m.now())
		if sameCredentials {
			for _, c := range cached.Accounts {
				if c.Slug == a.Slug {
					row = c
					row.State = states[a.AccountID]
					today, _ := publicstatus.TodayWindow(m.now())
					if row.TodayStart.Before(today) {
						row.Today = nil
						row.GrossToday = nil
						row.RefreshFailed = true
					}
					break
				}
			}
		}
		if stateAt.IsZero() || m.now().Sub(stateAt) > time.Minute || row.State == "" {
			row.State = "Status unavailable"
		}
		row.StateAt = stateAt
		out.Accounts = append(out.Accounts, row)
	}
	return publicstatus.Settings{Enabled: s.Enabled, Origin: s.Origin}, out
}
func (m *Manager) refreshPublicStates() {
	m.mu.Lock()
	enabled := m.publicSettings.Enabled
	ids := m.automatedAccountsLocked()
	m.mu.Unlock()
	if !enabled {
		return
	}
	views, paused := m.fleet.Snapshot()
	states := map[int64]string{}
	for id := range ids {
		states[id] = "Paused"
		if paused {
			states[id] = "Global pause"
		}
	}
	for _, v := range views {
		if !ids[v.AccountID] {
			continue
		}
		state := "Paused"
		switch {
		case paused:
			state = "Global pause"
		case !v.Status.MonitorRunning:
			state = "Monitor unavailable"
		case v.LockReason != "":
			state = "Attention required"
		case v.Status.Armed && m.now().Before(v.Status.ArmedUntil):
			state = "Running"
		case v.Status.RecoveryPending:
			state = "Waiting for recovery"
		case v.Status.RunRequested:
			state = "Waiting for session / readiness"
		}
		states[v.AccountID] = state
	}
	m.mu.Lock()
	m.publicStates = states
	m.publicStateAt = m.now()
	m.mu.Unlock()
}
func (m *Manager) runPublicStatus(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	next := time.Time{}
	var previous publicstatus.Settings
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		m.refreshPublicStates()
		m.mu.Lock()
		settings := m.publicSettings
		m.mu.Unlock()
		if !reflect.DeepEqual(settings, previous) || !m.now().Before(next) {
			m.collectPublicStatus(ctx)
			previous = settings
			next = m.now().Add(5 * time.Minute)
		}
		timer.Reset(15 * time.Second)
	}
}
func (m *Manager) collectPublicStatus(ctx context.Context) {
	m.mu.Lock()
	s := m.publicSettings
	creds := m.credentials
	ids := m.automatedAccountsLocked()
	var reader publicstatus.Reader
	if s.Enabled && m.c.PublicStatusListen != "" && creds.Username != "" && creds.APIKey != "" {
		_ = m.executionFactory()
		if m.apiClient != nil {
			reader = m.apiClient
		}
	}
	m.mu.Unlock()
	if !s.Enabled {
		return
	}
	configs := []publicstatus.AccountConfig{}
	states := map[int64]string{}
	for _, a := range s.Accounts {
		if ids[a.AccountID] {
			configs = append(configs, a)
			states[a.AccountID] = "Paused"
		}
	}
	call, cancel := context.WithTimeout(ctx, 45*time.Second)
	snap := publicstatus.Collect(call, m.now(), configs, states, reader)
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !reflect.DeepEqual(m.publicSettings, s) || m.credentials != creds {
		return
	}
	// Preserve last successful financial snapshot on a transient failure. Its own
	// timestamp remains unchanged so the UI can mark stale data accurately.
	for i, a := range snap.Accounts {
		if a.DataAvailable || m.publicCredentials != creds {
			continue
		}
		for _, old := range m.publicSnapshot.Accounts {
			if old.Slug == a.Slug {
				old.RefreshFailed = true
				snap.Accounts[i] = old
				break
			}
		}
	}
	m.publicSnapshot = snap
	m.publicCredentials = creds
}

func clonePublicAccounts(rows []publicstatus.AccountConfig) []publicstatus.AccountConfig {
	out := append([]publicstatus.AccountConfig(nil), rows...)
	for i := range out {
		if out[i].StartingBalance != nil {
			v := *out[i].StartingBalance
			out[i].StartingBalance = &v
		}
		if out[i].NetAdjustments != nil {
			v := *out[i].NetAdjustments
			out[i].NetAdjustments = &v
		}
	}
	return out
}
