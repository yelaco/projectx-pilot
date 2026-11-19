package publicstatus

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/session"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeReader struct {
	missingBalance bool
	balance        float64
	rows           []projectx.Trade
	fail           bool
	calls          int
	duplicate      bool
}

func (f *fakeReader) Accounts(context.Context) ([]projectx.Account, error) {
	f.calls++
	b := true
	a := projectx.Account{ID: 987654, Name: "PRIVATE-BROKER-NAME", Simulated: &b, Balance: f.balance, BalanceKnown: !f.missingBalance}
	if f.duplicate {
		return []projectx.Account{a, a}, nil
	}
	return []projectx.Account{a}, nil
}
func (f *fakeReader) Trades(context.Context, int64, time.Time, time.Time) ([]projectx.Trade, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("PRIVATE-RAW-ERROR")
	}
	return f.rows, nil
}
func num(n float64) *float64 { return &n }
func TestCollectNetCostsScopesAndPublicDTO(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, session.Chicago)
	start, _ := TodayWindow(now)
	f := &fakeReader{balance: 50097, rows: []projectx.Trade{{ID: 1, AccountID: 987654, ContractID: "CON.F.US.MCL.Z26", OrderID: 765432, CreationTimestamp: start.Add(time.Hour), Price: 60, Side: 0, Size: 1, Fees: num(1), Commissions: num(.5)}, {ID: 2, AccountID: 987654, ContractID: "CON.F.US.MCL.Z26", OrderID: 765433, CreationTimestamp: start.Add(2 * time.Hour), Price: 61, Side: 1, Size: 1, ProfitAndLoss: num(100), Fees: num(1), Commissions: num(.5)}, {ID: 3, AccountID: 987654, CreationTimestamp: start.Add(3 * time.Hour), Size: 1, ProfitAndLoss: num(-1000), Voided: true}}}
	cfg := []AccountConfig{{AccountID: 987654, Slug: "opaque", Alias: "Account A", Since: "2026-11-01", StartingBalance: num(50000), NetAdjustments: num(0), LifetimeConfirmed: true}}
	snap := Collect(context.Background(), now, cfg, map[int64]string{987654: "Running"}, f)
	a := snap.Accounts[0]
	if !a.DataAvailable || a.Today == nil || *a.Today != 97 || a.Total == nil || *a.Total != 97 || len(a.Trades) != 2 || a.Trades[0].Instrument != "MCL" {
		t.Fatal("incorrect public accounting", a)
	}
	b, _ := json.Marshal(snap)
	for _, secret := range []string{"987654", "765432", "PRIVATE", "CON.F.US", "account_id", "orderId"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("private metadata leaked", secret, string(b))
		}
	}
	f.missingBalance = true
	if a := Collect(context.Background(), now, cfg, map[int64]string{987654: "Paused"}, f).Accounts[0]; a.Total != nil {
		t.Fatal("missing account balance establishes lifetime")
	}
	f.missingBalance = false
	f.balance = 50147
	cfg[0].NetAdjustments = num(50)
	if a := Collect(context.Background(), now, cfg, map[int64]string{987654: "Paused"}, f).Accounts[0]; a.Total == nil || *a.Total != 97 {
		t.Fatal("non-trading credits counted as trading profit")
	}
	cfg[0].NetAdjustments = num(0)
	f.balance = 50100
	if a := Collect(context.Background(), now, cfg, map[int64]string{987654: "Paused"}, f).Accounts[0]; a.Total != nil {
		t.Fatal("unreconciled partial history labeled lifetime")
	}
	f.balance = 50097
	f.rows[0].Fees = nil
	a = Collect(context.Background(), now, cfg, map[int64]string{987654: "Paused"}, f).Accounts[0]
	if a.Today != nil || a.Total != nil || a.GrossToday == nil {
		t.Fatal("unknown costs treated as zero", a)
	}
	f.fail = true
	a = Collect(context.Background(), now, cfg, map[int64]string{987654: "Paused"}, f).Accounts[0]
	if a.DataAvailable || a.Today != nil || a.Total != nil {
		t.Fatal("failed history treated as zero")
	}
	f.fail = false
	f.duplicate = true
	a = Collect(context.Background(), now, cfg, map[int64]string{987654: "Paused"}, f).Accounts[0]
	if a.DataAvailable {
		t.Fatal("duplicate identity accepted")
	}
	if len(Collect(context.Background(), now, cfg, map[int64]string{}, f).Accounts) != 0 {
		t.Fatal("unassigned account published")
	}
}
func TestTotalSinceAndTodayHaveSeparateBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, session.Chicago)
	start, _ := TodayWindow(now)
	f := &fakeReader{rows: []projectx.Trade{{ID: 1, AccountID: 987654, CreationTimestamp: start.Add(time.Hour), Side: 0, Size: 1, ProfitAndLoss: num(25), Fees: num(0), Commissions: num(0)}}}
	cfg := []AccountConfig{{AccountID: 987654, Slug: "opaque", Alias: "Account A", Since: "2026-11-19", StartingBalance: num(0), NetAdjustments: num(0), LifetimeConfirmed: true}}
	a := Collect(context.Background(), now, cfg, map[int64]string{987654: "Paused"}, f).Accounts[0]
	if a.Today == nil || *a.Today != 25 || a.Total == nil || *a.Total != 0 {
		t.Fatal("history before total start incorrectly included", a)
	}
	cfg[0].Since = ""
	a = Collect(context.Background(), now, cfg, map[int64]string{987654: "Paused"}, f).Accounts[0]
	if a.Total != nil {
		t.Fatal("unconfigured lifetime invented")
	}
	f.rows[0].AccountID = 9
	a = Collect(context.Background(), now, cfg, map[int64]string{987654: "Paused"}, f).Accounts[0]
	if a.DataAvailable {
		t.Fatal("wrong-account history accepted")
	}
}
func TestChicagoTodayBoundaryAcrossDST(t *testing.T) {
	for _, date := range []string{"2026-03-08T16:59:00-05:00", "2026-11-01T16:59:00-06:00", "2026-11-01T17:00:00-06:00"} {
		n, _ := time.Parse(time.RFC3339, date)
		start, end := TodayWindow(n)
		if start.In(session.Chicago).Hour() != 17 || start.After(n) || !end.Equal(n) {
			t.Fatal("invalid display session", start, end)
		}
		if n.Sub(start) > 25*time.Hour {
			t.Fatal("DST window widened")
		}
	}
}
func TestPublicHandlerExactHostReadOnlyAndNoFallback(t *testing.T) {
	s := Settings{Enabled: true, Origin: "https://status.example.test", Accounts: []AccountConfig{{AccountID: 987654, Slug: "opaque", Alias: "Account A"}}}
	snap := Snapshot{Accounts: []Account{{Slug: "opaque", Alias: "Account A", Trades: []Trade{}}}}
	h := Handler(func() (Settings, Snapshot) { return s, snap })
	for _, tc := range []struct {
		method, path, host string
		code               int
	}{{"GET", "/", "status.example.test", 200}, {"GET", "/api/status", "status.example.test", 200}, {"GET", "/accounts/opaque", "status.example.test", 200}, {"GET", "/accounts/987654", "status.example.test", 404}, {"GET", "/settings", "status.example.test", 404}, {"POST", "/api/status", "status.example.test", 405}, {"GET", "/", "pilot.example.test", 404}, {"GET", "/", "status.example.test:443", 404}} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:8091"+tc.path, nil)
		r.Host = tc.host
		r.Header.Set("X-Forwarded-Host", "status.example.test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable public data")
		}
		if strings.Contains(w.Body.String(), "987654") {
			t.Fatal("config serialized")
		}
	}
	s.Enabled = false
	r := httptest.NewRequest("GET", "https://status.example.test/api/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("disabled publication still visible")
	}
}
func TestSettingsRejectUnsafePublication(t *testing.T) {
	now := time.Now()
	good := Settings{Enabled: true, Origin: "https://status.example.test", Accounts: []AccountConfig{{AccountID: 1, Slug: "opaque", Alias: "Account A"}}}
	if err := good.Validate(now); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://status.example.test", "https://status.example.test/path", "https://status.example.test:443", "https://user@status.example.test", "https://status.example.test?"} {
		s := good
		s.Origin = origin
		if s.Validate(now) == nil {
			t.Fatal("unsafe origin accepted", origin)
		}
	}
	for _, alias := range []string{"<script>", "https://secret.example", "account\nadmin"} {
		s := good
		s.Accounts = append([]AccountConfig(nil), good.Accounts...)
		s.Accounts[0].Alias = alias
		if s.Validate(now) == nil {
			t.Fatal("unsafe alias accepted")
		}
	}
}
