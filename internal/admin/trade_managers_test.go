package admin

import (
	"context"
	"encoding/json"
	"projectxbot/internal/app"
	"projectxbot/internal/risk"
	"projectxbot/internal/signal"
	"testing"
	"time"
)

func TestTradeManagerAssignmentAndMultiAccountContainment(t *testing.T) {
	m, _, path, state := setup(t)
	s := app.New(signalReader{}, 0)
	m.Attach(s)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	policy := risk.Defaults()
	policy.ID = "practice"
	policy.Name = "Practice"
	policy.AccountIDs = []int64{42}
	if err := m.SaveTradeManager(policy); err != nil {
		t.Fatal(err)
	}
	a := Automation{ID: "swift", Name: "Swift", Strategy: "swift", TradeManagerID: policy.ID, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}
	if err := m.SaveAutomation(a); err != nil {
		t.Fatal(err)
	}
	if m.Policies().Automations[0].AccountID != 42 {
		t.Fatal("single routing not bound")
	}
	if m.DeleteTradeManager(policy.ID) == nil {
		t.Fatal("referenced policy deleted")
	}
	policy.Routing = "multiple"
	policy.AccountIDs = []int64{42, 43}
	if err := m.SaveTradeManager(policy); err != nil {
		t.Fatal(err)
	}
	if m.Policies().Automations[0].AccountID != 0 {
		t.Fatal("multi routing silently chose one account")
	}
	alert := signal.Signal{ID: "multi-test", Strategy: "swift", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19995, TakeProfit: 20010, Timestamp: time.Now().UTC()}
	b, _ := json.Marshal(alert)
	if m.ImportSignal("swift", "mnq-expiry", string(b)) == nil {
		t.Fatal("multi-account policy entered single-account import")
	}
	rows, err := m.Signals()
	if err != nil || len(rows) != 0 {
		t.Fatal("multi-account signal persisted as single-account", err)
	}
	if len(m.Policies().Accounts) != 0 || m.Settings().Config.Mode != "read-only" {
		t.Fatal("saving policy enabled account execution")
	}
	c := m.Settings().Config
	next, _, err := Open(c, path, state)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if len(next.Policies().TradeManagers) != 1 || len(next.Policies().TradeManagers[0].AccountIDs) != 2 {
		t.Fatal("policy lost after restart")
	}
	copy := m.Policies()
	copy.TradeManagers[0].AccountIDs[0] = 999
	if m.Policies().TradeManagers[0].AccountIDs[0] != 42 {
		t.Fatal("policy aliases saved state")
	}
}
