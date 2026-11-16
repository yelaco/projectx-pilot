package risk

import (
	"errors"
	"math"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/signal"
)

// SizingInputs must come from a reconciled, session-scoped account observation.
// This calculator never authenticates, reserves risk or authorizes submission.
type SizingInputs struct {
	DisablePriceDeviation bool // trusted execution configuration, never an alert field
	Known                 bool
	ObservedAt            time.Time
	SessionKnown          bool
	SessionLoss           float64 // net realized losses and costs, conservatively not offset by gains
	UnrealizedLoss        float64
	OpenRisk              float64 // remaining loss to stops, in addition to unrealized loss
	LiquidationHeadroom   float64 // authoritative current distance to the account floor
	Trades                int
	Positions             int
	RoundTripCosts        float64 // per-contract commissions plus fees
	SlippageTicks         int
	MaxQuantity           int
	Quote                 float64
	QuoteAt               time.Time
	MaxDeviationTicks     int
}

type SizingPlan struct {
	Quantity        int
	StopTicks       int
	TargetTicks     int
	RiskPerContract float64
	PlannedRisk     float64
	Budget          float64
}

// Size uses supplied authoritative metadata and rejects incomplete observations.
// A valid plan is only a calculation, never an execution approval.
func Size(p Profile, s signal.Signal, c projectx.Contract, a SizingInputs, now time.Time, maxSignalAge, maxDataAge time.Duration) (SizingPlan, error) {
	fail := func(message string) (SizingPlan, error) { return SizingPlan{}, errors.New(message) }
	if err := p.Validate(); err != nil {
		return SizingPlan{}, err
	}
	if maxSignalAge <= 0 || maxDataAge <= 0 || now.IsZero() {
		return fail("valid freshness limits required")
	}
	if err := s.Validate(now, maxSignalAge, c.TickSize); err != nil {
		return SizingPlan{}, err
	}
	if c.ID == "" || !c.ActiveContract || !positive(c.TickValue) {
		return fail("active authoritative contract metadata required")
	}
	// Contract-to-symbol/account binding belongs to the caller's trusted resolver.
	if !a.Known || !a.SessionKnown {
		return fail("reconciled account and verified session inputs required")
	}
	if !fresh(a.ObservedAt, now, maxDataAge) || !fresh(a.QuoteAt, now, maxDataAge) {
		return fail("account observation or market quote is stale")
	}
	for _, v := range []float64{a.SessionLoss, a.UnrealizedLoss, a.OpenRisk, a.RoundTripCosts} {
		if !nonnegative(v) {
			return fail("risk observations and costs must be finite and nonnegative")
		}
	}
	if (!p.PerTradeOnly && !positive(a.LiquidationHeadroom)) || !positive(a.Quote) || a.Trades < 0 || a.Positions < 0 || a.SlippageTicks < 0 || a.MaxDeviationTicks < 0 || a.MaxQuantity < 1 {
		return fail("incomplete risk, price or quantity constraints")
	}
	if !p.PerTradeOnly && a.Trades >= p.MaxTradesPerSession {
		return fail("session trade limit reached")
	}
	if !p.PerTradeOnly && a.Positions >= p.MaxConcurrentPositions {
		return fail("concurrent position limit reached")
	}
	// Until per-position reservation is implemented, existing exposure must deny
	// every new entry, even for policies permitting several positions.
	if a.Positions != 0 || a.OpenRisk != 0 {
		return fail("existing exposure requires position-level risk reconciliation")
	}
	if !a.DisablePriceDeviation && math.Abs(a.Quote-s.Entry)/c.TickSize > float64(a.MaxDeviationTicks)+1e-6 {
		return fail("signal price exceeds market deviation limit")
	}
	stopTicks := math.Round(math.Abs(s.Entry-s.StopLoss) / c.TickSize)
	targetTicks := math.Round(math.Abs(s.TakeProfit-s.Entry) / c.TickSize)
	if stopTicks < 1 || targetTicks < 1 || stopTicks > math.MaxInt32 || targetTicks > math.MaxInt32 {
		return fail("bracket distance is outside supported tick range")
	}
	unitRisk := (stopTicks+float64(a.SlippageTicks))*c.TickValue + a.RoundTripCosts
	if !positive(unitRisk) {
		return fail("invalid estimated per-contract risk")
	}
	budget := p.MaxRiskPerTrade
	if !p.PerTradeOnly {
		dailyRemaining := p.MaxDailyLoss - a.SessionLoss - a.UnrealizedLoss - a.OpenRisk
		budget = math.Min(budget, math.Min(p.MaxSignalRisk, math.Min(p.MaxOpenRisk-a.OpenRisk, math.Min(dailyRemaining, a.LiquidationHeadroom-a.OpenRisk))))
	}

	if !positive(budget) || budget < unitRisk {
		return fail("remaining risk cannot accommodate one contract")
	}
	rawQuantity := math.Min(math.Floor(budget/unitRisk), float64(a.MaxQuantity))
	if !positive(rawQuantity) || rawQuantity > math.MaxInt32 {
		return fail("invalid whole-contract quantity")
	}
	quantity := int(rawQuantity)
	// Guard rounding at the budget boundary; never round risk upward to fit.
	for quantity > 0 && float64(quantity)*unitRisk > budget {
		quantity--
	}
	if quantity < 1 {
		return fail("remaining risk cannot accommodate one contract")
	}
	return SizingPlan{Quantity: quantity, StopTicks: int(stopTicks), TargetTicks: int(targetTicks), RiskPerContract: unitRisk, PlannedRisk: float64(quantity) * unitRisk, Budget: budget}, nil
}
func positive(v float64) bool    { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
func nonnegative(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
func fresh(t, now time.Time, age time.Duration) bool {
	return !t.IsZero() && !t.After(now) && now.Sub(t) <= age
}
