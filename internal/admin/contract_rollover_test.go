package admin

import (
	"context"
	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/risk"
	"testing"
)

type rolloverReader struct{ accountReader }

func (rolloverReader) AvailableContracts(context.Context) ([]projectx.Contract, error) {
	return []projectx.Contract{{ID: "old-mcl", SymbolID: "F.US.MCLE", TickSize: .01, TickValue: 1, ActiveContract: false}, {ID: "next-mcl", SymbolID: "F.US.MCLE", TickSize: .01, TickValue: 1, ActiveContract: true}}, nil
}
func TestRolloverModeBindsBrokerInstrumentIndependentlyOfAlertSymbol(t *testing.T) {
	m, _, _, _ := setup(t)
	p := risk.Defaults()
	p.ID = "risk"
	p.Name = "Risk"
	p.AccountIDs = []int64{42}
	p.PerTradeOnly = true
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAutomation(Automation{ID: "mcl", Name: "MCL", Strategy: "strategy", Source: "tradingview-manual", Instruments: []string{"MCL"}, TradeManagerID: "risk"}); err != nil {
		t.Fatal(err)
	}
	service := app.New(rolloverReader{}, 0)
	m.Attach(service)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateAutomationContractMode(context.Background(), "mcl", "automatic", "old-mcl", "market", "active"); err != nil {
		t.Fatal(err)
	}
	profile := m.Policies().Automations[0]
	if profile.ContractMode != "active" || profile.ContractSymbolID != "F.US.MCLE" || profile.ContractID != "old-mcl" {
		t.Fatal("rollover identity was inferred from alert label or expiry", profile)
	}
	m.mu.Lock()
	plan, err := m.planLocked("mcl")
	m.mu.Unlock()
	if err != nil || plan.ContractSymbolID != "F.US.MCLE" || plan.ContractMode != "active" {
		t.Fatal("rollover authorization absent from frozen plan", plan, err)
	}
	if err := m.UpdateAutomationContractMode(context.Background(), "mcl", "automatic", "unknown", "market", "active"); err == nil {
		t.Fatal("unknown contract granted rolling authority")
	}
	if err := m.UpdateAutomationContractMode(context.Background(), "mcl", "automatic", "old-mcl", "market", "pinned"); err != nil {
		t.Fatal(err)
	}
	if a := m.Policies().Automations[0]; a.ContractSymbolID != "" || a.ContractMode != "pinned" {
		t.Fatal("pinned mode retained dynamic authority")
	}
}
