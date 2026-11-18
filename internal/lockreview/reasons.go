// Package lockreview classifies fixed local lock reasons. Unknown reasons fail closed.
package lockreview

type Info struct {
	Category, Guidance       string
	Reviewable, TerminalOnly bool
}

func IsRisk(reason string) bool {
	return reason == "daily loss limit reached" || reason == "session loss or trade limit reached" || reason == "liquidation buffer reached"
}
func Classify(reason string) Info {
	if IsRisk(reason) {
		return Info{Category: "Risk limit", Guidance: "Daily-loss, trade-count and liquidation-buffer locks cannot be waived by confirmation. Fresh risk/session eligibility is required."}
	}
	switch reason {
	case "market data stale while managing exposure":
		return Info{Category: "Stale quote", Guidance: "Use the dedicated stale-quote review; current exposure can be acknowledged without closing it."}
	case "protective orders missing, pending or inconsistent":
		return Info{Category: "Legacy OCO checker", Guidance: "Use the dedicated historical OCO review with its required evidence and two fresh flat-account observations."}
	case "submission did not confirm acceptance; reconciliation required", "submitted order absent from history; no automatic retry is permitted", "duplicate parent orders detected", "accepted order differs from durable intent", "entry cancellation uncertain", "emergency management not verified flat; use TopstepX immediately", "local emergency flatten requested":
		return Info{Category: "Order reconciliation", Guidance: "The original execution must first reconcile to a matching terminal result. Confirmation never resolves an uncertain order or retries a mutation.", Reviewable: true, TerminalOnly: true}
	case "unexpected concurrent account exposure", "position exposure differs from observed entry fills", "flat observations with working orders; review and cancel orders locally if appropriate":
		return Info{Category: "Exposure discrepancy", Guidance: "Review fresh account observations and confirm leaving all positions/orders untouched. New entries still require flatness.", Reviewable: true}
	case "entry restriction reached with a partial fill; review remaining entry locally", "scheduled exit reminder reached; position preserved for operator", "scheduled flatten cutoff reached":
		return Info{Category: "Schedule or entry restriction", Guidance: "Acknowledgement leaves the account untouched. It does not extend entry hours or change the captured schedule.", Reviewable: true}
	case "session costs or P&L cannot be verified":
		return Info{Category: "Accounting data", Guidance: "Acknowledgement does not make missing risk inputs valid. Fresh accounting/risk checks remain mandatory before new entries.", Reviewable: true}
	case "duplicate or invalid protective order observation", "protective order parent link missing or different from entry", "protective order contract differs from position", "protective order side is not opposite the entry", "protective order is not open", "protective order has no remaining quantity", "bracket prices unavailable", "OCO pair distance differs from plan", "protective OCO linkage could not be verified", "stop price differs from planned protection", "target price differs from planned protection", "unsupported protective order type", "stop or target coverage does not match filled exposure", "weighted bracket prices differ from actual exposure":
		return Info{Category: "Protection discrepancy", Guidance: "You may acknowledge the observed protection discrepancy without changing orders. This does not certify protection or allow another entry while exposure remains.", Reviewable: true}
	case "":
		return Info{}
	default:
		return Info{Category: "Unrecognized lock", Guidance: "This lock has no verified review policy. Inspect the original reason; no force-unlock is available."}
	}
}

// AutoRecoverable identifies technical faults whose completed execution may be
// released after fresh flat/no-order confirmation. It never waives live faults,
// ambiguous writes, legacy repairs, unknown reasons or risk locks.
func AutoRecoverable(reason string) bool {
	switch Classify(reason).Category {
	case "Exposure discrepancy", "Protection discrepancy", "Accounting data":
		return true
	default:
		return false
	}
}
