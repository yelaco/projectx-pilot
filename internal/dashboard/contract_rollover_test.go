package dashboard

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/yelaco/projectx-pilot/internal/admin"
	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/risk"
)

func TestAutomaticRolloverFormUsesVerifiedBrokerIdentity(t *testing.T) {
	m, password, h := managementFixture(t)
	policy := risk.Defaults()
	policy.ID = "roll-risk"
	policy.Name = "Risk"
	policy.AccountIDs = []int64{7}
	policy.PerTradeOnly = true
	if err := m.SaveTradeManager(policy); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveAutomation(admin.Automation{ID: "roll", Name: "Roll", Strategy: "strategy", Source: "tradingview-manual", Instruments: []string{"MES"}, TradeManagerID: policy.ID}); err != nil {
		t.Fatal(err)
	}
	service := app.New(allContractReader{}, 0)
	m.Attach(service)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	csrf := formCSRF(t, requestAs(h, "local", password, "GET", "/automations/profiles/roll?tab=execution", nil))
	values := url.Values{"csrf": {csrf}, "id": {"roll"}, "execution_mode": {"automatic"}, "contract_id": {"old"}, "order_type": {"market"}, "contract_mode": {"active"}, "contract_symbol_id": {"F.US.UNTRUSTED"}}
	response := requestAs(h, "local", password, "POST", "/automation-execution", values)
	if response.Code != 303 {
		t.Fatal(response.Code, response.Body.String())
	}
	profile := m.Policies().Automations[0]
	if profile.ContractMode != "active" || profile.ContractSymbolID != "F.US.MES" {
		t.Fatal("form supplied broker identity or lost rollover mode", profile)
	}
	page := requestAs(h, "local", password, "GET", "/automations/profiles/roll?tab=execution", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Automatic rollover is enabled") || !strings.Contains(page.Body.String(), `value="active" selected`) {
		t.Fatal("rollover mode not visible", page.Code)
	}
	values.Set("contract_mode", "unknown")
	if response = requestAs(h, "local", password, "POST", "/automation-execution", values); response.Code != 400 {
		t.Fatal("unknown contract mode accepted", response.Code)
	}
}
