package reporting

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/session"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

func number(v float64) *float64 { return &v }
func TestReportingWindowChicagoDueTimeWeekendsAndDST(t *testing.T) {
	for _, tc := range []struct{ at, date string }{{"2026-11-16T16:14:59-06:00", "2026-11-13"}, {"2026-11-16T16:15:00-06:00", "2026-11-16"}, {"2026-11-21T18:00:00-06:00", "2026-11-20"}, {"2026-03-09T16:15:00-05:00", "2026-03-09"}} {
		now, _ := time.Parse(time.RFC3339, tc.at)
		date, start, end := LatestWindow(now)
		if date != tc.date || start.In(session.Chicago).Hour() != 17 || end.In(session.Chicago).Hour() != 16 || end.Sub(start) != 23*time.Hour {
			t.Fatal("incorrect Chicago report interval", tc, date, start, end)
		}
	}
}
func TestSummaryIncludesAllLegCostsAndIgnoresVoidedTrades(t *testing.T) {
	trades := []projectx.Trade{{ID: 1, Size: 2, Fees: number(1), Commissions: number(.5)}, {ID: 2, Size: 1, ProfitAndLoss: number(100), Fees: number(1), Commissions: number(.5)}, {ID: 3, Size: 1, ProfitAndLoss: number(-20), Fees: number(1), Commissions: number(.5)}, {ID: 4, Voided: true, ProfitAndLoss: number(-1000)}}
	got, err := Summarize(trades)
	if err != nil || got.OpeningLegs != 1 || got.ClosingLegs != 2 || got.ClosedContracts != 2 || got.Wins != 1 || got.Losses != 1 || got.Gross != 80 || got.Fees != 3 || got.Commissions != 1.5 || !strings.Contains(got.Text(), "$75.50") {
		t.Fatal("incorrect gross/net/leg accounting", got, err)
	}
	trades[0].Fees = nil
	got, err = Summarize(trades)
	if err != nil || got.CostsKnown || !strings.Contains(got.Text(), "Net after window costs: unavailable") {
		t.Fatal("missing costs presented as zero", got, err)
	}
	trades[2].ID = 1
	if _, err = Summarize(trades); err == nil {
		t.Fatal("duplicate history double-counted")
	}
	trades[2].ID = 3
	trades[2].ProfitAndLoss = number(math.NaN())
	if _, err = Summarize(trades); err == nil {
		t.Fatal("invalid P&L accepted")
	}
}

type reportReader struct {
	calls   int
	failure bool
	now     time.Time
}

func (r *reportReader) Accounts(context.Context) ([]projectx.Account, error) {
	sim := true
	return []projectx.Account{{ID: 42, Simulated: &sim}, {ID: 43, Simulated: &sim}}, nil
}
func (r *reportReader) Trades(_ context.Context, id int64, start, end time.Time) ([]projectx.Trade, error) {
	r.calls++
	if r.failure {
		return nil, errors.New("PRIVATE_API_BODY")
	}
	return []projectx.Trade{{ID: 1, AccountID: id, CreationTimestamp: start.Add(time.Hour), Size: 1, ProfitAndLoss: number(10), Fees: number(1), Commissions: number(1)}}, nil
}

type reportSender struct{ messages []string }

func (s *reportSender) Enqueue(message string) bool {
	s.messages = append(s.messages, message)
	return true
}
func TestDailyReportsAllAccountsClaimOnceAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 16, 16, 15, 0, 0, session.Chicago)
	reader := &reportReader{}
	sender := &reportSender{}
	if err = Process(context.Background(), now, []int64{42, 43, 42}, reader, store, sender); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 2 || reader.calls != 2 {
		t.Fatal("account targets omitted or duplicated")
	}
	for _, message := range sender.messages {
		if !strings.Contains(message, "$8.00") || !strings.Contains(message, "2026-11-16") || strings.Contains(message, "PRIVATE_API_BODY") {
			t.Fatal("invalid report", message)
		}
	}
	_ = store.Close()
	store, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = Process(context.Background(), now, []int64{42, 43}, reader, store, sender); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 2 || reader.calls != 2 {
		t.Fatal("restart replayed daily report")
	}
}
func TestUnavailableHistoryIsReportedWithoutZeroPnL(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "reports.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reader := &reportReader{failure: true}
	sender := &reportSender{}
	if err = Process(context.Background(), time.Date(2026, 11, 16, 17, 0, 0, 0, session.Chicago), []int64{42}, reader, store, sender); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "unavailable") || strings.Contains(sender.messages[0], "$0.00") || strings.Contains(sender.messages[0], "PRIVATE_API_BODY") {
		t.Fatal("unavailable data misrepresented", sender.messages)
	}
}
