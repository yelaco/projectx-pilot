// Package publicstatus exposes only explicitly published, sanitized read-only data.
package publicstatus

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/reporting"
	"github.com/yelaco/projectx-pilot/internal/session"
)

type AccountConfig struct {
	StartingBalance   *float64 `json:"starting_balance,omitempty"`
	NetAdjustments    *float64 `json:"net_adjustments,omitempty"`
	LifetimeConfirmed bool     `json:"lifetime_confirmed,omitempty"`
	AccountID         int64    `json:"account_id"`
	Slug              string   `json:"slug"`
	Alias             string   `json:"alias"`
	Since             string   `json:"since,omitempty"`
}
type Settings struct {
	Enabled  bool            `json:"enabled"`
	Origin   string          `json:"origin"`
	Accounts []AccountConfig `json:"accounts"`
}

var slugPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func (s Settings) Validate(now time.Time) error {
	if !s.Enabled {
		return nil
	}
	u, err := url.Parse(s.Origin)
	if err != nil || u.Scheme != "https" || u.Host != u.Hostname() || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]+\.[a-z]{2,}$`).MatchString(u.Host) {
		return errors.New("public status requires an exact HTTPS hostname")
	}
	if len(s.Accounts) == 0 || len(s.Accounts) > 20 {
		return errors.New("select 1–20 automated accounts for publication")
	}
	ids, slugs := map[int64]bool{}, map[string]bool{}
	aliases := map[string]bool{}
	for _, a := range s.Accounts {
		if a.AccountID <= 0 || ids[a.AccountID] || !slugPattern.MatchString(a.Slug) || slugs[a.Slug] || len(strings.TrimSpace(a.Alias)) < 1 || len(a.Alias) > 64 || aliases[strings.ToLower(strings.TrimSpace(a.Alias))] {
			return errors.New("invalid or duplicate public account alias")
		}
		for _, c := range a.Alias {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ' ' || c == '-' || c == '_') {
				return errors.New("aliases may contain letters, numbers, spaces, hyphens and underscores")
			}
		}
		if a.LifetimeConfirmed && (a.Since == "" || a.StartingBalance == nil || a.NetAdjustments == nil) {
			return errors.New("lifetime setup requires inception date, original balance and known net non-trading adjustments")
		}
		if a.StartingBalance != nil && (!finite(*a.StartingBalance) || *a.StartingBalance < 0) || a.NetAdjustments != nil && !finite(*a.NetAdjustments) {
			return errors.New("lifetime balances and adjustments must be finite")
		}
		if a.Since != "" {
			start, e := time.ParseInLocation("2006-01-02", a.Since, session.Chicago)
			if e != nil || start.After(now) || start.Before(now.AddDate(-10, 0, 0)) {
				return errors.New("history start must be within the last ten years")
			}
		}
		ids[a.AccountID], slugs[a.Slug] = true, true
		aliases[strings.ToLower(strings.TrimSpace(a.Alias))] = true
	}
	return nil
}

type Trade struct {
	At         time.Time `json:"at"`
	Instrument string    `json:"instrument"`
	Side       string    `json:"side"`
	Quantity   int       `json:"quantity"`
	Price      float64   `json:"price"`
	Gross      *float64  `json:"gross"`
	Costs      *float64  `json:"costs"`
}
type Account struct {
	TotalStatus   string    `json:"total_status"`
	StateAt       time.Time `json:"state_at"`
	RefreshFailed bool      `json:"refresh_failed"`
	Slug          string    `json:"slug"`
	Alias         string    `json:"alias"`
	State         string    `json:"state"`
	UpdatedAt     time.Time `json:"updated_at"`
	TodayStart    time.Time `json:"today_start"`
	TotalSince    string    `json:"total_since,omitempty"`
	Today         *float64  `json:"today"`
	Total         *float64  `json:"total"`
	GrossToday    *float64  `json:"gross_today"`
	GrossTotal    *float64  `json:"gross_total"`
	Trades        []Trade   `json:"trades"`
	MoreTrades    bool      `json:"more_trades"`
	DataAvailable bool      `json:"data_available"`
}
type Snapshot struct {
	CapturedAt time.Time `json:"captured_at"`
	Accounts   []Account `json:"accounts"`
}
type Reader interface {
	Accounts(context.Context) ([]projectx.Account, error)
	Trades(context.Context, int64, time.Time, time.Time) ([]projectx.Trade, error)
}

// TodayWindow is a display convention only, never execution calendar authority.
func TodayWindow(now time.Time) (time.Time, time.Time) {
	n := now.In(session.Chicago)
	start := time.Date(n.Year(), n.Month(), n.Day(), 17, 0, 0, 0, session.Chicago)
	if n.Before(start) {
		start = start.AddDate(0, 0, -1)
	}
	return start, now.UTC()
}
func Collect(ctx context.Context, now time.Time, configs []AccountConfig, states map[int64]string, reader Reader) Snapshot {
	snap := Snapshot{CapturedAt: now.UTC(), Accounts: []Account{}}
	identities := map[int64]bool{}
	balances := map[int64]float64{}
	balanceKnown := map[int64]bool{}
	if reader != nil && len(configs) > 0 {
		rows, err := reader.Accounts(ctx)
		if err == nil {
			seen := map[int64]bool{}
			for _, r := range rows {
				if seen[r.ID] {
					identities[r.ID] = false
					continue
				}
				seen[r.ID] = true
				identities[r.ID] = r.Simulated != nil && *r.Simulated
				balances[r.ID] = r.Balance
				balanceKnown[r.ID] = r.BalanceKnown
			}
		}
	}
	today, end := TodayWindow(now)
	for _, cfg := range configs {
		a := Account{TotalStatus: "Lifetime unavailable: inception and adjustments not confirmed", Slug: cfg.Slug, Alias: cfg.Alias, State: states[cfg.AccountID], TodayStart: today.UTC(), TotalSince: cfg.Since, Trades: []Trade{}}
		if a.State == "" {
			continue
		} // An account removed from automation must disappear.
		if identities[cfg.AccountID] && ctx.Err() == nil {
			start := today
			if cfg.Since != "" {
				d, _ := time.ParseInLocation("2006-01-02", cfg.Since, session.Chicago)
				if d.Before(start) {
					start = d
				}
			}
			rows, err := reader.Trades(ctx, cfg.AccountID, start.UTC(), end)
			valid := err == nil && rows != nil && len(rows) <= 10000 && ctx.Err() == nil
			for _, r := range rows {
				if r.AccountID != cfg.AccountID || r.CreationTimestamp.Before(start) || r.CreationTimestamp.After(end) || r.CreationTimestamp.IsZero() || r.Side < 0 || r.Side > 1 || math.IsNaN(r.Price) || math.IsInf(r.Price, 0) {
					valid = false
				}
			}
			summary, err := reporting.Summarize(rows)
			if valid && err == nil {
				todayRows := []projectx.Trade{}
				for _, r := range rows {
					if !r.CreationTimestamp.Before(today) {
						todayRows = append(todayRows, r)
					}
				}
				daily, e := reporting.Summarize(todayRows)
				if e == nil {
					a.GrossToday = ptr(daily.Gross)
					if daily.CostsKnown {
						a.Today = ptr(daily.Gross - daily.Fees - daily.Commissions)
					}
					if cfg.LifetimeConfirmed && cfg.Since != "" && cfg.StartingBalance != nil && cfg.NetAdjustments != nil {
						since, _ := time.ParseInLocation("2006-01-02", cfg.Since, session.Chicago)
						totalRows := []projectx.Trade{}
						for _, r := range rows {
							if !r.CreationTimestamp.Before(since) {
								totalRows = append(totalRows, r)
							}
						}
						summary, _ = reporting.Summarize(totalRows)
						a.TotalStatus = "Lifetime unavailable: costs missing or balance not reconciled"
						net := summary.Gross - summary.Fees - summary.Commissions
						expected := balances[cfg.AccountID] - *cfg.StartingBalance - *cfg.NetAdjustments
						if summary.CostsKnown && balanceKnown[cfg.AccountID] && finite(expected) && finite(balances[cfg.AccountID]) && math.Abs(expected-net) <= 0.02 {
							a.Total = ptr(net)
							a.GrossTotal = ptr(summary.Gross)
							a.TotalStatus = "Lifetime · operator-confirmed inception; balance reconciled"
						}
					}
					a.UpdatedAt = now.UTC()
					a.DataAvailable = true
					rows = append([]projectx.Trade(nil), rows...)
					sort.Slice(rows, func(i, j int) bool { return rows[i].CreationTimestamp.After(rows[j].CreationTimestamp) })
					for _, r := range rows {
						if r.Voided {
							continue
						}
						if len(a.Trades) == 200 {
							a.MoreTrades = true
							break
						}
						side := "Buy"
						if r.Side == 1 {
							side = "Sell"
						}
						t := Trade{At: r.CreationTimestamp.UTC(), Instrument: instrument(r.ContractID), Side: side, Quantity: r.Size, Price: r.Price, Gross: r.ProfitAndLoss}
						if r.Fees != nil && r.Commissions != nil && finite(*r.Fees) && finite(*r.Commissions) && *r.Fees >= 0 && *r.Commissions >= 0 {
							t.Costs = ptr(*r.Fees + *r.Commissions)
						}
						a.Trades = append(a.Trades, t)
					}
				}
			}
		}
		snap.Accounts = append(snap.Accounts, a)
	}
	return snap
}
func ptr(v float64) *float64 { return &v }
func finite(v float64) bool  { return !math.IsNaN(v) && !math.IsInf(v, 0) }

var contractPattern = regexp.MustCompile(`^CON\.F\.[A-Z]{1,4}\.([A-Z0-9]{1,12})\.[A-Z0-9]{1,12}$`)

func instrument(id string) string {
	m := contractPattern.FindStringSubmatch(id)
	if len(m) == 2 {
		return m[1]
	}
	return "Futures"
}

// Only this handler is installed on the public listener. No dashboard fallback.
func Handler(source func() (Settings, Snapshot)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serve(w, r, source) })
}
