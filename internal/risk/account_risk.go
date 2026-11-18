package risk

import (
	"errors"
	"strconv"
	"strings"
)

func (p Profile) MaxRiskForAccount(account int64) float64 {
	if value, ok := p.AccountRiskOverrides[account]; ok {
		return value
	}
	return p.MaxRiskPerTrade
}
func (p Profile) RiskOverrideText(account int64) string {
	if value, ok := p.AccountRiskOverrides[account]; ok {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	return ""
}
func (p Profile) RiskOverridesText() string {
	lines := []string{}
	for _, account := range p.AccountIDs {
		if value := p.RiskOverrideText(account); value != "" {
			lines = append(lines, strconv.FormatInt(account, 10)+"="+value)
		}
	}
	return strings.Join(lines, "\n")
}
func (p Profile) validateAccountRisk() error {
	if len(p.AccountRiskOverrides) > 20 || len(p.AccountRiskOverrides) > 0 && p.Routing != "multiple" {
		return errors.New("account risk overrides require multiple-account routing and at most 20 targets")
	}
	for account, value := range p.AccountRiskOverrides {
		if !p.HasAccount(account) || !positive(value) {
			return errors.New("account risk overrides must be positive finite amounts for selected accounts")
		}
		if !p.PerTradeOnly && value > p.MaxDailyLoss {
			return errors.New("account risk override exceeds the legacy daily-loss limit")
		}
	}
	return nil
}

// ForAccount freezes an independent risk policy without modifying manager scope.
func (p Profile) ForAccount(account int64) (Profile, error) {
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	if !p.HasAccount(account) {
		return Profile{}, errors.New("account is outside Trade Manager scope")
	}
	scoped := p.Clone()
	scoped.MaxRiskPerTrade = p.MaxRiskForAccount(account)
	scoped.Routing = "single"
	scoped.MultiAccountMode = ""
	scoped.AccountIDs = []int64{account}
	scoped.AccountRiskOverrides = nil
	return scoped, scoped.Validate()
}

func (p Profile) AccountIDsText() string {
	values := []string{}
	for _, account := range p.AccountIDs {
		values = append(values, strconv.FormatInt(account, 10))
	}
	return strings.Join(values, ", ")
}
