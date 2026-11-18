package fleet

import (
	"errors"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"time"
)

// UpdateContractDiscovery consumes only the existing authoritative discovery
// snapshot; it never probes a broker or changes any active intent.
func (f *Fleet) UpdateContractDiscovery(contracts []projectx.Contract, at time.Time, healthy bool, maxAge time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contracts = append([]projectx.Contract(nil), contracts...)
	f.contractsAt = at
	f.contractsHealthy = healthy
	f.contractsMaxAge = maxAge
	for _, r := range f.accounts {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}
func (f *Fleet) resolveContractLocked(p Plan) (string, error) {
	if p.ContractMode == "" || p.ContractMode == "pinned" {
		return p.ContractID, nil
	}
	if p.ContractMode != "active" || (projectx.Contract{SymbolID: p.ContractSymbolID}).RootSymbol() == "" {
		return "", errors.New("verified rollover instrument is unavailable")
	}
	if !f.contractsHealthy || f.contractsAt.IsZero() || f.contractsMaxAge <= 0 || f.now().Sub(f.contractsAt) > f.contractsMaxAge || f.contractsAt.After(f.now().Add(time.Second)) {
		return "", errors.New("fresh active-contract discovery is unavailable")
	}
	selected := ""
	for _, contract := range f.contracts {
		if contract.SymbolID != p.ContractSymbolID || !contract.ActiveContract {
			continue
		}
		if contract.ID == "" || contract.TickSize <= 0 || contract.TickValue <= 0 {
			return "", errors.New("invalid active-contract metadata")
		}
		if selected != "" {
			return "", errors.New("multiple active expiries reported; review required")
		}
		selected = contract.ID
	}
	if selected == "" {
		return "", errors.New("no active expiry for the authorized broker instrument")
	}
	return selected, nil
}
