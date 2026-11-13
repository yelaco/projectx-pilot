package admin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"projectxbot/internal/config"
	"projectxbot/internal/risk"
	"strings"
	"time"
)

// Policies configure future execution eligibility. They never arm the application.
type AccountPolicy struct {
	AccountID        int64 `json:"account_id"`
	ExecutionAllowed bool  `json:"execution_allowed"`
}
type Automation struct {
	ExecutionMode  string   `json:"execution_mode,omitempty"`
	ContractID     string   `json:"contract_id,omitempty"`
	OrderType      string   `json:"order_type,omitempty"`
	TradeManagerID string   `json:"trade_manager_id,omitempty"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Strategy       string   `json:"strategy"`
	AccountID      int64    `json:"account_id"`
	Instruments    []string `json:"instruments"`
	Source         string   `json:"source"`
}
type Policies struct {
	TradeManagers   []risk.Profile  `json:"trade_managers,omitempty"`
	CredentialOwner string          `json:"credential_owner"`
	Accounts        []AccountPolicy `json:"accounts"`
	Automations     []Automation    `json:"automations"`
}

func (p Policies) Validate() error {
	if len(p.Accounts) > 100 || len(p.Automations) > 100 || len(p.TradeManagers) > 100 {
		return errors.New("maximum 100 accounts and automation profiles")
	}
	seen := map[int64]bool{}
	for _, a := range p.Accounts {
		if a.AccountID <= 0 || seen[a.AccountID] {
			return errors.New("invalid account policy")
		}
		seen[a.AccountID] = true
	}
	managers := map[string]risk.Profile{}
	for _, manager := range p.TradeManagers {
		if err := manager.Validate(); err != nil {
			return err
		}
		if _, ok := managers[manager.ID]; ok {
			return errors.New("duplicate trade manager ID")
		}
		managers[manager.ID] = manager
	}
	names := map[string]bool{}
	for _, a := range p.Automations {
		if a.ID == "" || len(a.ID) > 64 || names[a.ID] {
			return errors.New("invalid automation ID")
		}
		names[a.ID] = true
		for _, r := range a.ID {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return errors.New("automation ID must use lowercase letters, digits, hyphens or underscores")
			}
		}
		if strings.TrimSpace(a.Name) == "" || len(a.Name) > 100 || strings.TrimSpace(a.Strategy) == "" || len(a.Strategy) > 100 || (a.AccountID <= 0 && a.TradeManagerID == "") {
			return errors.New("automation needs a name, strategy and positive account ID")
		}
		if a.TradeManagerID != "" {
			manager, ok := managers[a.TradeManagerID]
			if !ok {
				return errors.New("automation references an unknown trade manager")
			}
			expected := int64(0)
			if manager.Routing == "single" {
				expected = manager.AccountIDs[0]
			}
			if a.AccountID != expected {
				return errors.New("automation account routing does not match its trade manager")
			}
		}
		if a.ExecutionMode != "" && a.ExecutionMode != "review" && a.ExecutionMode != "automatic" {
			return errors.New("execution mode must be review or automatic")
		}
		if a.ExecutionMode == "automatic" {
			prefix := "Automatic profile " + a.ID + ": "
			if a.Source != "tradingview-manual" {
				return errors.New(prefix + "choose TradingView alerts as the signal source in General.")
			}
			if a.TradeManagerID == "" {
				return errors.New(prefix + "assign a single-account Trade Manager; a direct account assignment is not enough.")
			}
			manager, ok := managers[a.TradeManagerID]
			if !ok || manager.Routing != "single" {
				return errors.New(prefix + "the assigned Trade Manager must use single-account routing.")
			}
			if strings.TrimSpace(a.ContractID) == "" {
				return errors.New(prefix + "select an execution contract expiry.")
			}
			if len(a.ContractID) > 128 {
				return errors.New(prefix + "execution contract ID exceeds the supported length.")
			}
			if a.OrderType != "market" && a.OrderType != "limit" {
				return errors.New(prefix + "choose Market or Limit as the entry order type.")
			}
		}
		switch a.Source {
		case "local-manual", "local-strategy", "tradingview-manual":
		default:
			return errors.New("unsupported signal source; remote automation is unavailable")
		}
		if len(a.Instruments) == 0 || len(a.Instruments) > 20 {
			return errors.New("select automation instruments")
		}
		for _, symbol := range a.Instruments {
			if len(symbol) < 1 || len(symbol) > 16 {
				return errors.New("invalid instrument")
			}
			for _, r := range symbol {
				if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
					return errors.New("invalid instrument")
				}
			}
		}
	}
	return nil
}
func (m *Manager) policyPath() string {
	return filepath.Join(filepath.Dir(m.credentialsPath), "automations.json")
}
func (m *Manager) loadPolicies() error {
	b, err := os.ReadFile(m.policyPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(b) > 131072 || json.Unmarshal(b, &m.policies) != nil {
		return errors.New("invalid automation settings")
	}
	if err := m.policies.Validate(); err != nil {
		return err
	}
	if m.policies.CredentialOwner != m.credentials.Username {
		p := clonePolicies(m.policies)
		p.CredentialOwner = m.credentials.Username
		for i := range p.Accounts {
			p.Accounts[i].ExecutionAllowed = false
		}
		return m.savePolicies(p)
	}
	return nil
}
func (m *Manager) Policies() Policies {
	m.mu.Lock()
	defer m.mu.Unlock()
	return clonePolicies(m.policies)
}
func clonePolicies(p Policies) Policies {
	out := Policies{TradeManagers: append([]risk.Profile(nil), p.TradeManagers...), CredentialOwner: p.CredentialOwner, Accounts: append([]AccountPolicy(nil), p.Accounts...), Automations: append([]Automation(nil), p.Automations...)}
	for i := range out.TradeManagers {
		out.TradeManagers[i].AccountIDs = append([]int64(nil), out.TradeManagers[i].AccountIDs...)
	}
	for i := range out.Automations {
		out.Automations[i].Instruments = append([]string(nil), out.Automations[i].Instruments...)
	}
	return out
}
func (m *Manager) savePolicies(p Policies) error {
	if m.executor != nil {
		m.executor.Pause()
	}
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err = config.WritePrivate(m.policyPath(), b); err != nil {
		return errors.New("could not save automation settings")
	}
	m.policies = p
	return nil
}

// SetAccountPermission requires a fresh, discovered account when granting eligibility.
// Denying an existing account is always allowed, including while disconnected.
func (m *Manager) SetAccountPermission(id int64, allowed bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.setAccountPermissionLocked(id, allowed)
}
func (m *Manager) setAccountPermissionLocked(id int64, allowed bool) error {
	if id <= 0 {
		return errors.New("invalid account ID")
	}
	if allowed {
		if m.service == nil {
			return errors.New("account discovery required")
		}
		snapshot := m.service.Snapshot()
		found := false
		for _, a := range snapshot.Accounts {
			if a.ID == id && a.CanTrade {
				found = true
			}
		}
		if !snapshot.Connected || time.Since(snapshot.UpdatedAt) > m.c.RefreshInterval+time.Minute || !found {
			return errors.New("permission requires a fresh snapshot containing a tradable account")
		}
	}
	p := clonePolicies(m.policies)
	p.CredentialOwner = m.credentials.Username
	found := false
	for i := range p.Accounts {
		if p.Accounts[i].AccountID == id {
			p.Accounts[i].ExecutionAllowed = allowed
			found = true
		}
	}
	if !found {
		p.Accounts = append(p.Accounts, AccountPolicy{AccountID: id, ExecutionAllowed: allowed})
	}
	return m.savePolicies(p)
}
func (m *Manager) SaveAutomation(a Automation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.TradeManagerID != "" {
		found := false
		for _, p := range m.policies.TradeManagers {
			if p.ID == a.TradeManagerID {
				found = true
				a.AccountID = 0
				if p.Routing == "single" {
					a.AccountID = p.AccountIDs[0]
				}
			}
		}
		if !found {
			return errors.New("select an existing trade manager")
		}
	}
	allowed := map[string]bool{}
	for _, s := range m.c.AllowedInstruments {
		allowed[s] = true
	}
	for _, s := range a.Instruments {
		if !allowed[s] {
			return errors.New("automation instrument is outside configured instrument allowlist")
		}
	}
	// Profiles can be drafted before discovery; account grants are separately verified.
	p := clonePolicies(m.policies)
	found := false
	for i := range p.Automations {
		if p.Automations[i].ID == a.ID {
			p.Automations[i] = a
			found = true
		}
	}
	if !found {
		p.Automations = append(p.Automations, a)
	}
	if err := m.savePolicies(p); err != nil {
		return err
	}
	return m.syncIngressLocked()
}
func (m *Manager) DeleteAutomation(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := clonePolicies(m.policies)
	for i, a := range p.Automations {
		if a.ID == id {
			p.Automations = append(p.Automations[:i], p.Automations[i+1:]...)
			if err := m.savePolicies(p); err != nil {
				return err
			}
			return m.syncIngressLocked()
		}
	}
	return errors.New("automation profile not found")
}

// Allowed is a saved eligibility preference, never an execution authorization.
func (p Policies) Allowed(id int64) bool {
	for _, a := range p.Accounts {
		if a.AccountID == id {
			return a.ExecutionAllowed
		}
	}
	return false
}
