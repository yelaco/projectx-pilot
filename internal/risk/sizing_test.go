package risk

import (
	"math"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/signal"
	"testing"
	"time"
)

func TestSizing(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	p := Defaults()
	p.ID = "practice"
	p.Name = "Practice"
	p.AccountIDs = []int64{42}
	s := signal.Signal{ID: "one", Strategy: "manual", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19990, TakeProfit: 20020, Timestamp: now}
	c := projectx.Contract{ID: "contract", ActiveContract: true, TickSize: .25, TickValue: .5}
	inputs := SizingInputs{Known: true, SessionKnown: true, ObservedAt: now, LiquidationHeadroom: 1000, RoundTripCosts: 2, SlippageTicks: 2, MaxQuantity: 10, Quote: 20000, QuoteAt: now, MaxDeviationTicks: 4}
	tests := []struct {
		name     string
		edit     func(*SizingInputs)
		quantity int
	}{
		{"cost-aware", func(*SizingInputs) {}, 6}, // $23 each, $138 planned
		{"daily-loss", func(a *SizingInputs) { a.SessionLoss = 400 }, 2},
		{"unrealized-loss", func(a *SizingInputs) { a.SessionLoss = 400; a.UnrealizedLoss = 30 }, 0},
		{"liquidation", func(a *SizingInputs) { a.LiquidationHeadroom = 45 }, 1},
		{"size-cap", func(a *SizingInputs) { a.MaxQuantity = 1 }, 1},
		{"trade-limit", func(a *SizingInputs) { a.Trades = 6 }, 0},
		{"position-limit", func(a *SizingInputs) { a.Positions = 1 }, 0},
		{"existing-risk", func(a *SizingInputs) { a.OpenRisk = 10 }, 0},
		{"unknown-account", func(a *SizingInputs) { a.Known = false }, 0},
		{"unknown-session", func(a *SizingInputs) { a.SessionKnown = false }, 0},
		{"stale-account", func(a *SizingInputs) { a.ObservedAt = now.Add(-time.Minute) }, 0},
		{"stale-quote", func(a *SizingInputs) { a.QuoteAt = now.Add(-time.Minute) }, 0},
		{"future-quote", func(a *SizingInputs) { a.QuoteAt = now.Add(time.Second) }, 0},
		{"deviation", func(a *SizingInputs) { a.Quote = 20002 }, 0},
		{"unknown-floor", func(a *SizingInputs) { a.LiquidationHeadroom = 0 }, 0},
		{"nonfinite-cost", func(a *SizingInputs) { a.RoundTripCosts = math.NaN() }, 0},
		{"negative-loss", func(a *SizingInputs) { a.SessionLoss = -1 }, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := inputs
			tc.edit(&a)
			plan, err := Size(p, s, c, a, now, 5*time.Minute, 5*time.Second)
			if tc.quantity == 0 {
				if err == nil {
					t.Fatalf("accepted unsafe plan %+v", plan)
				}
				return
			}
			if err != nil || plan.Quantity != tc.quantity || plan.PlannedRisk > plan.Budget || plan.StopTicks != 40 || plan.TargetTicks != 80 {
				t.Fatalf("%+v %v", plan, err)
			}
		})
	}
	for _, edit := range []func(*signal.Signal){
		func(s *signal.Signal) { s.Entry = 20000.1 },
		func(s *signal.Signal) { s.StopLoss = 20001 },
		func(s *signal.Signal) { s.Timestamp = now.Add(-10 * time.Minute) },
	} {
		bad := s
		edit(&bad)
		if _, err := Size(p, bad, c, inputs, now, 5*time.Minute, 5*time.Second); err == nil {
			t.Fatal("invalid signal sized")
		}
	}
	s.Direction = "SELL"
	s.StopLoss = 20010
	s.TakeProfit = 19980
	if plan, err := Size(p, s, c, inputs, now, 5*time.Minute, 5*time.Second); err != nil || plan.Quantity != 6 {
		t.Fatal(plan, err)
	}
}

func TestPerTradeOnlySizingHasNoHiddenSessionOrFloorCaps(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	p := Profile{ID: "simple", Name: "Simple", Routing: "single", AccountIDs: []int64{42}, PerTradeOnly: true, MaxRiskPerTrade: 150}
	s := signal.Signal{ID: "one", Strategy: "test", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19990, TakeProfit: 20020, Timestamp: now}
	c := projectx.Contract{ID: "mnq", ActiveContract: true, TickSize: .25, TickValue: .5}
	a := SizingInputs{Known: true, SessionKnown: true, ObservedAt: now, QuoteAt: now, Quote: 20000, SessionLoss: 10000, Trades: 1000, RoundTripCosts: 2, SlippageTicks: 2, MaxQuantity: 100, MaxDeviationTicks: 4}
	plan, err := Size(p, s, c, a, now, time.Minute, time.Second)
	if err != nil || plan.Quantity != 6 || plan.PlannedRisk != 138 {
		t.Fatal(plan, err)
	}
	// A larger stop must reduce size, and the dollar budget is always enforced.
	s.StopLoss = 19900
	if _, err = Size(p, s, c, a, now, time.Minute, time.Second); err == nil {
		t.Fatal("accepted a single contract over risk budget")
	}
	for _, amount := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		p.MaxRiskPerTrade = amount
		if p.Validate() == nil {
			t.Fatal("invalid maximum risk accepted", amount)
		}
	}
}

func TestDeviationDisableIsExplicitAndKeepsRiskAndFreshness(t *testing.T) {
	now := time.Now().UTC()
	p := Profile{ID: "simple", Name: "Simple", Routing: "single", AccountIDs: []int64{42}, PerTradeOnly: true, MaxRiskPerTrade: 150}
	s := signal.Signal{ID: "signal", Strategy: "test", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19990, TakeProfit: 20020, Timestamp: now}
	c := projectx.Contract{ID: "mnq", ActiveContract: true, TickSize: .25, TickValue: .5}
	a := SizingInputs{Known: true, SessionKnown: true, ObservedAt: now, QuoteAt: now, Quote: 20100, RoundTripCosts: 2, SlippageTicks: 2, MaxQuantity: 100, MaxDeviationTicks: 0}
	if _, err := Size(p, s, c, a, now, time.Minute, time.Second); err == nil {
		t.Fatal("zero ticks implicitly disabled deviation")
	}
	a.DisablePriceDeviation = true
	plan, err := Size(p, s, c, a, now, time.Minute, time.Second)
	if err != nil || plan.Quantity != 6 || plan.PlannedRisk > 150 {
		t.Fatal("explicit disable did not preserve sizing", plan, err)
	}
	a.QuoteAt = now.Add(-time.Minute)
	if _, err = Size(p, s, c, a, now, time.Minute, time.Second); err == nil {
		t.Fatal("disabled deviation accepted stale data")
	}
	a.QuoteAt = now
	p.MaxRiskPerTrade = 1
	if _, err = Size(p, s, c, a, now, time.Minute, time.Second); err == nil {
		t.Fatal("disabled deviation bypassed dollar risk")
	}
}
