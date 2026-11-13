package risk

import (
	"math"
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
