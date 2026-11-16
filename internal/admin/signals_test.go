package admin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/config"
	"github.com/yelaco/projectx-pilot/internal/projectx"
	"github.com/yelaco/projectx-pilot/internal/signal"
)

type signalReader struct{ accountReader }

func (signalReader) AvailableContracts(context.Context) ([]projectx.Contract, error) {
	return []projectx.Contract{{ID: "mnq-expiry", SymbolID: "F.US.MNQ", TickSize: .25, TickValue: .5, ActiveContract: true}}, nil
}
func TestLocalSignalImportReviewAndRecovery(t *testing.T) {
	m, _, path, state := setup(t)
	t.Cleanup(func() { m.Close() })
	s := app.New(signalReader{}, 0)
	m.Attach(s)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Legacy global scope does not override an explicit profile's symbols.
	m.c.AllowedInstruments = []string{"MGC"}
	profile := Automation{ID: "swift-mnq", Name: "Swift MNQ", Strategy: "swift-mnq-scalping", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}
	if err := m.SaveAutomation(profile); err != nil {
		t.Fatal(err)
	}
	sig := signal.Signal{ID: "alert-001", Strategy: profile.Strategy, Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19995, TakeProfit: 20010, Timestamp: time.Now().UTC()}
	b, _ := json.Marshal(sig)
	if err := m.ImportSignal(profile.ID, "mnq-expiry", string(b)); err != nil {
		t.Fatal(err)
	}
	if m.ImportSignal(profile.ID, "mnq-expiry", string(b)) == nil {
		t.Fatal("duplicate accepted")
	}
	if m.ImportSignal(profile.ID, "wrong-contract", string(b)) == nil {
		t.Fatal("unknown contract accepted")
	}
	outside := sig
	outside.ID = "outside-profile"
	outside.Symbol = "MGC"
	outsideBody, _ := json.Marshal(outside)
	if err := m.ImportSignal(profile.ID, "mnq-expiry", string(outsideBody)); err == nil {
		t.Fatal("profile instrument restriction removed")
	}
	if m.ImportSignal("unknown", "mnq-expiry", string(b)) == nil {
		t.Fatal("unknown profile accepted")
	}
	sig.ID = "wrong-strategy"
	sig.Strategy = "other"
	bad, _ := json.Marshal(sig)
	if m.ImportSignal(profile.ID, "mnq-expiry", string(bad)) == nil {
		t.Fatal("wrong strategy accepted")
	}
	if m.ImportSignal(profile.ID, "mnq-expiry", strings.TrimSuffix(string(b), "}")+`,"account_id":999}`) == nil {
		t.Fatal("account override accepted")
	}
	if err := m.ReviewSignal("alert-001", "reviewed"); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	c, _ := config.Load(path)
	next, _, err := Open(c, path, state)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	rows, err := next.Signals()
	if err != nil || len(rows) != 1 || rows[0].Status != "reviewed" || rows[0].AccountID != "42" {
		t.Fatal(rows, err)
	}
	s.Configure(nil, 0, time.Minute)
	if m.ImportSignal(profile.ID, "mnq-expiry", string(b)) == nil {
		t.Fatal("stale metadata accepted")
	}
}
