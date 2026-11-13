package storage

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"projectxbot/internal/signal"
)

func record(id string) Record {
	return Record{Signal: signal.Signal{ID: id, Strategy: "swift", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19995, TakeProfit: 20010, Timestamp: time.Now().UTC()}, ProfileID: "practice", ContractID: "contract", AccountID: "42"}
}
func TestRestartDedupAndReview(t *testing.T) {
	p := filepath.Join(t.TempDir(), "signals.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Insert(record("one")); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Insert(record("one")); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	rows, err := s.Recent()
	if err != nil || len(rows) != 1 || rows[0].Status != "pending_review" {
		t.Fatal(rows, err)
	}
	if err = s.Review("one", "reviewed"); err != nil {
		t.Fatal(err)
	}
	if err = s.Review("one", "rejected"); !errors.Is(err, ErrTransition) {
		t.Fatal("reviewed signal changed state", err)
	}
	if err = s.Review("missing", "reviewed"); !errors.Is(err, ErrTransition) {
		t.Fatal(err)
	}
	if err = s.Review("one", "execute"); !errors.Is(err, ErrTransition) {
		t.Fatal("execution state accepted")
	}
	rows, err = s.Recent()
	if err != nil || rows[0].Status != "reviewed" {
		t.Fatal(rows, err)
	}
}
func TestConcurrentDuplicateImports(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "signals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Insert(record("same"))
			if err == nil {
				success.Add(1)
			} else if !errors.Is(err, ErrDuplicate) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("duplicate accepted", success.Load())
	}
	rows, err := s.Recent()
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
}
func TestSQLPayloadIsData(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "signals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := record("one")
	r.ContractID = "';DROP TABLE signals;--"
	if err = s.Insert(r); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Recent()
	if err != nil || len(rows) != 1 || rows[0].ContractID != r.ContractID {
		t.Fatal(rows, err)
	}
}

func TestPendingInboxIsBounded(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "signals.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Seed capacity in one transaction rather than performing 1000 fsyncs in a unit test.
	err = s.exec(`WITH RECURSIVE seq(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM seq WHERE x<1000) INSERT INTO signals SELECT 'seed-'||x,'{}','profile','contract','42','pending_review','now' FROM seq;`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Insert(record("overflow")) == nil {
		t.Fatal("unbounded pending queue")
	}
	if err = s.Review("seed-1", "rejected"); err != nil {
		t.Fatal(err)
	}
	if err = s.Insert(record("now-fits")); err != nil {
		t.Fatal(err)
	}
}

func TestActivityComponentFiltering(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, component := range []string{"admin", "tunnel", "webhook"} {
		if err = s.RecordActivity("INFO", component, "Safe event"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Activity("tunnel")
	if err != nil || len(rows) != 1 || rows[0].Component != "tunnel" {
		t.Fatal(rows, err)
	}
}
