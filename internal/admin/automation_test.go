package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/yelaco/projectx-pilot/internal/execution"
	"github.com/yelaco/projectx-pilot/internal/risk"
)

func automaticFixture(t *testing.T) (*Manager, string, string, string, string) {
	t.Helper()
	m, password, path, state := setup(t)
	if err := m.Save(Update{AccountID: 42, Instruments: []string{"MNQ", "MGC"}, RefreshSeconds: 60, Username: "offline-test", APIKey: "not-a-real-key"}); err != nil {
		t.Fatal(err)
	}
	p := risk.Defaults()
	p.ID = "practice"
	p.Name = "Practice"
	p.AccountIDs = []int64{42}
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAutomation(Automation{ID: "swift", Name: "Swift", Strategy: "swift", TradeManagerID: "practice", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual", ExecutionMode: "automatic", ContractID: "mnq", OrderType: "market"}); err != nil {
		t.Fatal(err)
	}
	// Offline fixture stands in for a fresh discovered account permission grant.
	policies := clonePolicies(m.policies)
	policies.Accounts = []AccountPolicy{{AccountID: 42, ExecutionAllowed: true}}
	if err := m.savePolicies(policies); err != nil {
		t.Fatal(err)
	}
	actor := m.Access().List()[0].ID
	if err := m.SaveProfileExecution(actor, password, "swift", execution.Config{Enabled: true, AccountID: 42, TradeManagerID: "practice", RoundTripCosts: 3, SlippageTicks: 2, MaxQuantity: 1, MaxDeviationTicks: 4}, true); err != nil {
		t.Fatal(err)
	}
	return m, password, actor, path, state
}
func TestAutomaticRoutingReadyWithoutProviderTicketButRemainsDisarmed(t *testing.T) {
	m, _, _, path, state := automaticFixture(t)
	if ready, why := m.AutomationReadiness(); !ready {
		t.Fatal(why)
	}
	if m.ExecutionConfig().RemoteApprovalReference != "" {
		t.Fatal("fixture unexpectedly has a ticket")
	}
	if m.ExecutionStatus().Armed || m.ExecutionStatus().RunRequested {
		t.Fatal("setup started execution")
	}
	config := m.Settings().Config
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	next, _, err := Open(config, path, state)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if ready, why := next.AutomationReadiness(); !ready {
		t.Fatal(why)
	}
	if next.ExecutionStatus().Armed || next.ExecutionStatus().RunRequested {
		t.Fatal("restart restored execution")
	}
}
func TestAutomaticProfileRequiresExplicitRoutingAndLocalAuthorization(t *testing.T) {
	m, password, actor, _, _ := automaticFixture(t)
	if err := m.ArmExecution(context.Background(), actor, password, execution.Authorization{AutomationConfirmed: true}); err == nil {
		t.Fatal("missing local authorization accepted", err)
	}
	original := m.Policies().Automations[0]
	for _, change := range []func(*Automation){func(a *Automation) { a.ContractID = "" }, func(a *Automation) { a.OrderType = "unknown" }, func(a *Automation) { a.TradeManagerID = "" }, func(a *Automation) { a.Source = "local-manual" }} {
		invalid := original
		change(&invalid)
		if err := m.SaveAutomation(invalid); err == nil {
			t.Fatal("invalid automatic configuration accepted")
		}
	}
	if err := m.processAutomation(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := m.AutomationResults()
	if err != nil || len(rows) != 0 {
		t.Fatal("disarmed worker claimed alerts", rows, err)
	}
}

func TestExistingTradeManagerMustBeAssignedToAutomaticProfile(t *testing.T) {
	m, _, _, _ := setup(t)
	p := risk.Defaults()
	p.ID = "practice-conservative"
	p.Name = "Practice"
	p.AccountIDs = []int64{42}
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	a := Automation{ID: "swift", Name: "Swift", Strategy: "swift", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}
	if err := m.SaveAutomation(a); err != nil {
		t.Fatal(err)
	}
	err := m.UpdateAutomationExecution("swift", "automatic", "mnq", "market")
	if err == nil || !strings.Contains(err.Error(), "assign a Trade Manager") {
		t.Fatal("existing unassigned manager was treated as assigned", err)
	}
	if m.Policies().Automations[0].ExecutionMode != "" {
		t.Fatal("failed validation mutated configuration")
	}
	if err = m.UpdateAutomationExecution("swift", "automatic", "mnq", "market", "practice-conservative"); err != nil {
		t.Fatal(err)
	}
	saved := m.Policies().Automations[0]
	if saved.TradeManagerID != p.ID || saved.AccountID != 42 || saved.ContractID != "mnq" || saved.OrderType != "market" || saved.ExecutionMode != "automatic" {
		t.Fatal(saved)
	}
	if m.ExecutionStatus().Armed {
		t.Fatal("assigning routing armed trading")
	}
	for _, tc := range []struct{ contract, order, reason string }{{"", "market", "select an execution contract"}, {"mnq", "", "choose Market or Limit"}} {
		err = m.UpdateAutomationExecution("swift", "automatic", tc.contract, tc.order, p.ID)
		if err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Fatal("unspecific validation error", err)
		}
	}
}
