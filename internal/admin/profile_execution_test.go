package admin

import (
	"context"
	"testing"

	"projectxbot/internal/app"
	"projectxbot/internal/execution"
	"projectxbot/internal/risk"
)

func TestProfileCanEnableAndGrantWithoutPracticeExecution(t *testing.T) {
	m, password, _, _ := setup(t)
	if err := m.Save(Update{AccountID: 42, Instruments: []string{"MNQ"}, RefreshSeconds: 60, Username: "offline", APIKey: "fake-test-key"}); err != nil {
		t.Fatal(err)
	}
	service := app.New(accountReader{}, 0)
	m.Attach(service)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := risk.Defaults()
	p.ID = "practice"
	p.Name = "Practice"
	p.AccountIDs = []int64{42}
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	a := Automation{ID: "swift", Name: "Swift", Strategy: "swift", AccountID: 42, TradeManagerID: "practice", Source: "tradingview-manual", Instruments: []string{"MNQ"}, ExecutionMode: "automatic", ContractID: "mnq", OrderType: "market"}
	if err := m.SaveAutomation(a); err != nil {
		t.Fatal(err)
	}
	actor := m.Access().List()[0].ID
	c := execution.Config{Enabled: true, AccountID: 42, TradeManagerID: "practice", RoundTripCosts: 3, SlippageTicks: 2, MaxQuantity: 1, MaxDeviationTicks: 4}
	if err := m.SaveProfileExecution(actor, "wrong", "swift", c, true); err == nil {
		t.Fatal("no reauthentication")
	}
	if err := m.SaveProfileExecution(actor, password, "swift", c, false); err == nil {
		t.Fatal("implicit account grant")
	}
	bad := c
	bad.AccountID = 99
	if err := m.SaveProfileExecution(actor, password, "swift", bad, true); err == nil {
		t.Fatal("account switch accepted")
	}
	if err := m.SaveProfileExecution(actor, password, "swift", c, true); err != nil {
		t.Fatal(err)
	}
	if !m.Policies().Allowed(42) || !m.ExecutionConfig().Enabled || m.ExecutionConfig().AutomationProfileID != "swift" {
		t.Fatal("profile setup not saved")
	}
	if m.ExecutionStatus().Armed {
		t.Fatal("setup automatically armed")
	}
	if ready, why := m.AutomationReadiness(); !ready {
		t.Fatal(why)
	}
	a.ID = "other"
	if err := m.SaveAutomation(a); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveProfileExecution(actor, password, "other", c, true); err != nil {
		t.Fatal(err)
	}
	if ready, why := m.AutomationReadiness(); !ready {
		t.Fatal("new profile routing should be ready without a ticket", why)
	}
}
