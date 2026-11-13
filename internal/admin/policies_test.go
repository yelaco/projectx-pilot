package admin

import (
	"context"
	"testing"

	"projectxbot/internal/app"
	"projectxbot/internal/config"
	"projectxbot/internal/projectx"
)

type accountReader struct{}

func (accountReader) Accounts(context.Context) ([]projectx.Account, error) {
	return []projectx.Account{{ID: 42, CanTrade: true}, {ID: 43, CanTrade: false}}, nil
}
func (accountReader) AvailableContracts(context.Context) ([]projectx.Contract, error) {
	return nil, nil
}
func TestAccountPermissionsAndProfiles(t *testing.T) {
	m, _, path, state := setup(t)
	if len(m.Policies().Accounts) != 0 {
		t.Fatal("grants must default to empty")
	}
	if m.SetAccountPermission(42, true) == nil {
		t.Fatal("grant without discovery")
	}
	s := app.New(accountReader{}, 0)
	m.Attach(s)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.SetAccountPermission(43, true) == nil || m.SetAccountPermission(999, true) == nil {
		t.Fatal("untradable or unknown account granted")
	}
	if err := m.SetAccountPermission(42, true); err != nil {
		t.Fatal(err)
	}
	a := Automation{ID: "mnq-morning", Name: "Morning", Strategy: "breakout", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}
	if err := m.SaveAutomation(a); err != nil {
		t.Fatal(err)
	}
	a.Source = "remote-automated"
	if m.SaveAutomation(a) == nil {
		t.Fatal("remote automated source allowed")
	}
	a.Source = "local-manual"
	a.Instruments = []string{"NQ"}
	if m.SaveAutomation(a) == nil {
		t.Fatal("instrument allowlist bypass")
	}
	a.Instruments = []string{"MNQ"}
	a.Name = "Updated"
	if err := m.SaveAutomation(a); err != nil {
		t.Fatal(err)
	}
	c, _ := config.Load(path)
	next, _, err := Open(c, path, state)
	if err != nil || len(next.Policies().Automations) != 1 || next.Policies().Automations[0].Name != "Updated" {
		t.Fatal("profile persistence failed", err)
	}
	if err = m.SetAccountPermission(42, false); err != nil {
		t.Fatal(err)
	}
	if m.Policies().Accounts[0].ExecutionAllowed {
		t.Fatal("revoke failed")
	}
	if err = m.DeleteAutomation(a.ID); err != nil {
		t.Fatal(err)
	}
	if len(m.Policies().Automations) != 0 {
		t.Fatal("delete failed")
	}
}
func TestCredentialsChangeRevokesPermissions(t *testing.T) {
	m, _, _, _ := setup(t)
	s := app.New(accountReader{}, 0)
	m.Attach(s)
	_ = s.Refresh(context.Background())
	if err := m.SetAccountPermission(42, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(Update{RefreshSeconds: 60, Instruments: []string{"MNQ"}, Username: "new-user", APIKey: "key"}); err != nil {
		t.Fatal(err)
	}
	if m.Policies().Accounts[0].ExecutionAllowed {
		t.Fatal("credential switch retained permission")
	}
}
