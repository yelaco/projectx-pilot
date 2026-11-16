package reporting

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
)

type Reader interface {
	Accounts(context.Context) ([]projectx.Account, error)
	Trades(context.Context, int64, time.Time, time.Time) ([]projectx.Trade, error)
}
type Store interface {
	DailyReportExists(string, string) (bool, error)
	ClaimDailyReport(string, string, string) (bool, error)
}
type Sender interface{ Enqueue(string) bool }

// Process fetches fresh read-only history, then durably claims each account/day
// before queueing once. Restart never replays a claimed report or touches trades.
func Process(ctx context.Context, now time.Time, ids []int64, reader Reader, store Store, sender Sender) error {
	date, start, end := LatestWindow(now)
	unique := map[int64]bool{}
	pending := []int64{}
	for _, id := range ids {
		if id <= 0 || unique[id] {
			continue
		}
		unique[id] = true
		exists, err := store.DailyReportExists(date, strconv.FormatInt(id, 10))
		if err != nil {
			return err
		}
		if !exists {
			pending = append(pending, id)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	sort.Slice(pending, func(a, b int) bool { return pending[a] < pending[b] })
	accounts := map[int64]bool{}
	if reader != nil {
		rows, err := reader.Accounts(ctx)
		if err == nil {
			seenAccounts := map[int64]bool{}
			for _, a := range rows {
				if seenAccounts[a.ID] {
					accounts[a.ID] = false
					continue
				}
				seenAccounts[a.ID] = true
				if a.Simulated != nil && *a.Simulated {
					accounts[a.ID] = true
				}
			}
		}
	}
	for _, id := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		text := "Trading data unavailable: account identity or API access could not be verified. No zero P&L is assumed."
		if accounts[id] {
			trades, err := reader.Trades(ctx, id, start.UTC(), end.UTC())
			if err == nil {
				valid := true
				for _, trade := range trades {
					if trade.AccountID != id || trade.CreationTimestamp.IsZero() || trade.CreationTimestamp.Before(start) || trade.CreationTimestamp.After(end) {
						valid = false
						break
					}
				}
				summary, err := Summarize(trades)
				if !valid {
					err = fmt.Errorf("account/time scope unavailable")
				}
				if err == nil {
					text = summary.Text()
				} else {
					text = "Trading data unavailable: trade/cost observations are inconsistent. No zero P&L is assumed."
				}
			} else {
				text = "Trading data unavailable: history request failed. No zero P&L is assumed."
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		message := fmt.Sprintf("Trading date %s | Account #%d\nWindow: %s to %s (America/Chicago)\n%s\nAccount-wide history, including manual trades. Unrealized P&L excluded. Closing legs are not round-trip trade counts. History can be corrected later.", date, id, start.Format("2006-01-02 15:04 MST"), end.Format("2006-01-02 15:04 MST"), text)
		claimed, err := store.ClaimDailyReport(date, strconv.FormatInt(id, 10), message)
		if err != nil {
			return err
		}
		if claimed {
			sender.Enqueue(message)
		}
	}
	return nil
}
