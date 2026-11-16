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
