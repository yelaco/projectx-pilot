// Package risk provides validated policy configuration and a pure sizing calculator.
// Execution supplies reconciled observations and durable order reservations.
package risk

import (
	"errors"
	"math"
	"strings"
)

type Profile struct {
	PerTradeOnly           bool    `json:"per_trade_only,omitempty"`
	ID                     string  `json:"id"`
	Name                   string  `json:"name"`
	Routing                string  `json:"routing"`
	AccountIDs             []int64 `json:"account_ids"`
	MaxRiskPerTrade        float64 `json:"max_risk_per_trade"`
	MaxDailyLoss           float64 `json:"max_daily_loss"`
	MaxTradesPerSession    int     `json:"max_trades_per_session"`
	MaxConcurrentPositions int     `json:"max_concurrent_positions"`
	MaxOpenRisk            float64 `json:"max_open_risk"`
	MaxSignalRisk          float64 `json:"max_signal_risk"`
	TargetProfit           float64 `json:"target_profit"`
}

func Defaults() Profile {
	return Profile{Routing: "single", MaxRiskPerTrade: 150, MaxDailyLoss: 450, MaxTradesPerSession: 6, MaxConcurrentPositions: 1, MaxOpenRisk: 150, MaxSignalRisk: 150, TargetProfit: 200}
}
func (p Profile) HasAccount(id int64) bool {
	for _, a := range p.AccountIDs {
		if a == id {
			return true
		}
	}
	return false
}
func (p Profile) Validate() error {
	if p.ID == "" || len(p.ID) > 64 || strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 {
		return errors.New("trade manager needs a valid ID and name")
	}
	for _, r := range p.ID {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return errors.New("trade manager ID must use lowercase letters, digits, hyphens or underscores")
		}
	}
	if p.Routing != "single" && p.Routing != "multiple" {
		return errors.New("routing must be single or multiple accounts")
	}
	if len(p.AccountIDs) < 1 || len(p.AccountIDs) > 20 || (p.Routing == "single" && len(p.AccountIDs) != 1) || (p.Routing == "multiple" && len(p.AccountIDs) < 2) {
		return errors.New("single routing requires one account; multiple routing requires 2..20 accounts")
	}
	seen := map[int64]bool{}
	for _, a := range p.AccountIDs {
		if a <= 0 || seen[a] {
			return errors.New("account IDs must be positive and unique")
		}
		seen[a] = true
	}
	if !positive(p.MaxRiskPerTrade) {
		return errors.New("maximum risk per trade must be a finite positive amount")
	}
	if p.PerTradeOnly {
		return nil
	}
	for _, v := range []float64{p.MaxRiskPerTrade, p.MaxDailyLoss, p.MaxOpenRisk, p.MaxSignalRisk} {
		if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("risk and loss limits must be finite positive amounts")
		}
	}
	if p.MaxRiskPerTrade > p.MaxDailyLoss || p.MaxOpenRisk > p.MaxDailyLoss {
		return errors.New("per-trade and open-risk limits cannot exceed the per-account daily loss limit")
	}
	if p.MaxTradesPerSession < 1 || p.MaxTradesPerSession > 100 || p.MaxConcurrentPositions < 1 || p.MaxConcurrentPositions > 20 {
		return errors.New("trade count must be 1..100 and concurrent positions 1..20")
	}
	if p.TargetProfit < 0 || math.IsNaN(p.TargetProfit) || math.IsInf(p.TargetProfit, 0) {
		return errors.New("profit objective must be a finite nonnegative amount")
	}
	return nil
}
