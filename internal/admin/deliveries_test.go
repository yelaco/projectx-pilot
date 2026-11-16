package admin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/signal"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

func TestDeliveryNeedsLocalImportAndRemainsNonExecuting(t *testing.T) {
	m, _, _, state := setup(t)
	s := app.New(signalReader{}, 0)
	m.Attach(s)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	profile := Automation{ID: "swift-mnq", Name: "Swift MNQ", Strategy: "swift-mnq-scalping", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}
	if err := m.SaveAutomation(profile); err != nil {
		t.Fatal(err)
	}
	remote, err := storage.Open(filepath.Join(state, "remote-alerts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	sig := signal.Signal{ID: "incoming-001", Strategy: profile.Strategy, Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19995, TakeProfit: 20010, Timestamp: time.Now().UTC()}
	if err = remote.Insert(storage.Record{Signal: sig}); err != nil {
		t.Fatal(err)
	}
	rows, err := m.Signals()
	if err != nil || len(rows) != 0 {
		t.Fatal("remote delivery automatically imported")
	}
	deliveries, err := m.Deliveries()
	if err != nil || len(deliveries) != 1 || deliveries[0].AccountID != "" {
		t.Fatal(deliveries, err)
	}
	if err = m.ImportDelivery(sig.ID, profile.ID, "mnq-expiry"); err != nil {
		t.Fatal(err)
	}
	rows, err = m.Signals()
	if err != nil || len(rows) != 1 || rows[0].Status != "pending_review" || rows[0].AccountID != "42" {
		t.Fatal("local import state wrong", rows, err)
	}
	if len(m.Policies().Accounts) != 0 {
		t.Fatal("import granted account permissions")
	}
	deliveries, err = m.Deliveries()
	if err != nil || deliveries[0].Status != "reviewed" {
		t.Fatal("delivery not marked imported", err)
	}
	if m.ImportDelivery(sig.ID, profile.ID, "mnq-expiry") == nil {
		t.Fatal("already imported delivery accepted")
	}
}
func TestDeliveryCopyRecoveryAndExpiry(t *testing.T) {
	m, _, _, state := setup(t)
	s := app.New(signalReader{}, 0)
	m.Attach(s)
	_ = s.Refresh(context.Background())
	p := Automation{ID: "swift", Name: "Swift", Strategy: "breakout", AccountID: 42, Instruments: []string{"MNQ"}, Source: "tradingview-manual"}
	if err := m.SaveAutomation(p); err != nil {
		t.Fatal(err)
	}
	remote, err := storage.Open(filepath.Join(state, "remote-alerts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	sig := signal.Signal{ID: "copy-001", Strategy: p.Strategy, Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19995, TakeProfit: 20010, Timestamp: time.Now().UTC()}
	if err = remote.Insert(storage.Record{Signal: sig}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(sig)
	// Simulate exit after writing the local row but before updating the remote inbox.
	if err = m.ImportSignal(p.ID, "mnq-expiry", string(payload)); err != nil {
		t.Fatal(err)
	}
	if err = m.ImportDelivery(sig.ID, p.ID, "mnq-expiry"); err != nil {
		t.Fatal("copy recovery failed", err)
	}
	sig.ID = "expired"
	sig.Timestamp = time.Now().Add(-time.Hour)
	if err = remote.Insert(storage.Record{Signal: sig}); err != nil {
		t.Fatal(err)
	}
	if m.ImportDelivery(sig.ID, p.ID, "mnq-expiry") == nil {
		t.Fatal("expired delivery imported")
	}
	if err = m.RejectDelivery(sig.ID); err != nil {
		t.Fatal("expired delivery could not be discarded", err)
	}
}
