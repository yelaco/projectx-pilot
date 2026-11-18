package risk

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestPolicyValidation(t *testing.T) {
	valid := Defaults()
	valid.ID = "practice"
	valid.Name = "Practice"
	valid.AccountIDs = []int64{42}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Profile){func(p *Profile) { p.AccountIDs = []int64{42, 43} }, func(p *Profile) { p.Routing = "multiple" }, func(p *Profile) { p.MaxRiskPerTrade = math.NaN() }, func(p *Profile) { p.MaxDailyLoss = -1 }, func(p *Profile) { p.MaxTradesPerSession = 0 }, func(p *Profile) { p.MaxConcurrentPositions = 0 }, func(p *Profile) { p.MaxOpenRisk = 1000 }, func(p *Profile) { p.MaxSignalRisk = 0 }, func(p *Profile) { p.AccountIDs = []int64{-1} }} {
		p := valid
		change(&p)
		if p.Validate() == nil {
			t.Fatal("invalid risk policy accepted", p)
		}
	}
	multi := valid
	multi.Routing = "multiple"
	multi.AccountIDs = []int64{42, 43}
	if err := multi.Validate(); err != nil {
		t.Fatal(err)
	}
	multi.AccountIDs = []int64{42, 42}
	if multi.Validate() == nil {
		t.Fatal("duplicate accounts accepted")
	}
}

func TestLegacyDefaultScheduleKeepsPolicyJSONScope(t *testing.T) {
	p := Defaults()
	p.ID = "legacy"
	p.Name = "Legacy"
	p.AccountIDs = []int64{42}
	original, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(original), `"schedule"`) {
		t.Fatal("implicit schedule changed legacy scope JSON")
	}
	_ = p.TradingSchedule()
	after, err := json.Marshal(p.Clone())
	if err != nil || string(after) != string(original) {
		t.Fatal("default schedule changed saved identity", err)
	}
}

func TestMultiAccountDistributionIsExplicitAndDefaultsToCopy(t *testing.T) {
	p := Defaults()
	p.ID = "manager"
	p.Name = "Manager"
	p.Routing = "multiple"
	p.AccountIDs = []int64{1, 2}
	p.PerTradeOnly = true
	if p.DistributionMode() != "copy" || p.Validate() != nil {
		t.Fatal("legacy policy did not retain copy semantics")
	}
	p.MultiAccountMode = "round_robin"
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Routing = "single"
	p.AccountIDs = []int64{1}
	if p.Validate() == nil {
		t.Fatal("round robin silently reduced to one account")
	}
	p.Routing = "multiple"
	p.AccountIDs = []int64{1, 2}
	p.MultiAccountMode = "unknown"
	if p.Validate() == nil {
		t.Fatal("unknown routing mode accepted")
	}
}

func TestAccountRiskOverridesValidateCloneAndFreezeIndependently(t *testing.T) {
	p := Defaults()
	p.ID = "manager"
	p.Name = "Manager"
	p.Routing = "multiple"
	p.AccountIDs = []int64{1, 2, 3}
	p.PerTradeOnly = true
	p.AccountRiskOverrides = map[int64]float64{1: 36, 2: 72}
	for account, want := range map[int64]float64{1: 36, 2: 72, 3: 150} {
		scoped, err := p.ForAccount(account)
		if err != nil || scoped.MaxRiskPerTrade != want || len(scoped.AccountIDs) != 1 || scoped.AccountIDs[0] != account || scoped.AccountRiskOverrides != nil {
			t.Fatal("wrong account policy", account, scoped, err)
		}
	}
	cloned := p.Clone()
	cloned.AccountRiskOverrides[1] = 999
	if p.AccountRiskOverrides[1] != 36 {
		t.Fatal("cloned manager shares risk override map")
	}
	if _, err := p.ForAccount(99); err == nil {
		t.Fatal("scoped an unauthorized account")
	}
	for _, values := range []map[int64]float64{{99: 50}, {1: 0}, {1: -1}, {1: math.NaN()}, {1: math.Inf(1)}} {
		invalid := p.Clone()
		invalid.AccountRiskOverrides = values
		if invalid.Validate() == nil {
			t.Fatal("invalid override accepted", values)
		}
	}
}
