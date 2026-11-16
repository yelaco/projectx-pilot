// Package reporting calculates read-only daily summaries. It has no trading capability.
package reporting

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/session"
)

// LatestWindow is a reporting convention, never exchange-calendar authorization.
// Reports become due at 16:15 Chicago for the interval previous 17:00 to 16:00.
func LatestWindow(now time.Time) (date string, start, end time.Time) {
	local := now.In(session.Chicago)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, session.Chicago)
	due := time.Date(day.Year(), day.Month(), day.Day(), 16, 15, 0, 0, session.Chicago)
	if local.Before(due) {
		day = day.AddDate(0, 0, -1)
	}
	for day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		day = day.AddDate(0, 0, -1)
	}
	start = time.Date(day.Year(), day.Month(), day.Day()-1, 17, 0, 0, 0, session.Chicago)
	end = time.Date(day.Year(), day.Month(), day.Day(), 16, 0, 0, 0, session.Chicago)
	return day.Format("2006-01-02"), start, end
}

type Summary struct {
	OpeningLegs, ClosingLegs, ClosedContracts, Wins, Losses, BreakEven int
	Gross, Fees, Commissions                                           float64
	CostsKnown                                                         bool
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// ProjectX returns null P&L for opening legs and gross P&L for closing legs.
// Fees and commissions must be summed across all nonvoided legs, including opens.
func Summarize(trades []projectx.Trade) (Summary, error) {
	s := Summary{CostsKnown: true}
	seen := map[int64]bool{}
	for _, t := range trades {
		if t.ID <= 0 || seen[t.ID] {
			return Summary{}, errors.New("invalid or duplicate trade identity")
		}
		seen[t.ID] = true
		if t.Voided {
			continue
		}
		if t.Size < 1 {
			return Summary{}, errors.New("trade quantity unavailable")
		}
		if t.Fees == nil || t.Commissions == nil || !finiteValue(t.Fees) || !finiteValue(t.Commissions) || *t.Fees < 0 || *t.Commissions < 0 {
			s.CostsKnown = false
		} else {
			s.Fees += *t.Fees
			s.Commissions += *t.Commissions
		}
		if t.ProfitAndLoss == nil {
			s.OpeningLegs++
			continue
		}
		if !finite(*t.ProfitAndLoss) {
			return Summary{}, errors.New("realized P&L unavailable")
		}
		s.ClosingLegs++
		s.ClosedContracts += t.Size
		s.Gross += *t.ProfitAndLoss
		switch {
		case *t.ProfitAndLoss > 0:
			s.Wins++
		case *t.ProfitAndLoss < 0:
			s.Losses++
		default:
			s.BreakEven++
		}
	}
	if !finite(s.Gross) || !finite(s.Fees) || !finite(s.Commissions) || !finite(s.Gross-s.Fees-s.Commissions) {
		return Summary{}, errors.New("invalid report totals")
	}
	return s, nil
}
func finiteValue(v *float64) bool { return v != nil && finite(*v) }
func (s Summary) Text() string {
	costs := "Fees / commissions: unavailable\nNet after window costs: unavailable"
	if s.CostsKnown {
		costs = fmt.Sprintf("Fees: $%.2f | Commissions: $%.2f\nNet after window costs: $%.2f", s.Fees, s.Commissions, s.Gross-s.Fees-s.Commissions)
	}
	return fmt.Sprintf("Opening legs: %d | Closing legs: %d | Closed contracts: %d\nWinning / losing / break-even closing legs: %d / %d / %d\nGross realized P&L: $%.2f\n%s", s.OpeningLegs, s.ClosingLegs, s.ClosedContracts, s.Wins, s.Losses, s.BreakEven, s.Gross, costs)
}
