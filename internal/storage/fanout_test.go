package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestFanoutAtomicFrozenAndPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	jobs := []TargetJob{{ID: "a", DeliveryID: "delivery", ProfileID: "p", AccountID: 1, Policy: "frozen"}, {ID: "b", DeliveryID: "delivery", ProfileID: "p", AccountID: 1, Policy: "frozen"}}
	if s.ClaimTargets(jobs) == nil {
		t.Fatal("duplicate target accepted")
	}
	got, _ := s.DeliveryTargets("delivery")
	if len(got) != 0 {
		t.Fatal("partial claim committed")
	}
	jobs[1].AccountID = 2
	if err = s.ClaimTargets(jobs); err != nil {
		t.Fatal(err)
	}
	expanded := append(jobs, TargetJob{ID: "c", DeliveryID: "delivery", ProfileID: "p", AccountID: 3})
	if !errors.Is(s.ClaimTargets(expanded), ErrDuplicate) {
		t.Fatal("delivery retargeted")
	}
	jobs[0].State = "submitted"
	if err = s.SaveTarget(jobs[0]); err != nil {
		t.Fatal(err)
	}
	if err = s.SetFleetPaused(true); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	paused, err := s.FleetPaused()
	if err != nil || !paused {
		t.Fatal("pause lost", err)
	}
	unfinished, err := s.UnfinishedTargets()
	if err != nil || len(unfinished) != 1 || unfinished[0].AccountID != 2 || unfinished[0].Policy != "frozen" {
		t.Fatal("claim lost", unfinished, err)
	}
	got, _ = s.DeliveryTargets("delivery")
	if len(got) != 2 {
		t.Fatal("target set changed")
	}
}

func TestAccountReservationMigratesGlobalIndex(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.executionSchema(); err != nil {
		t.Fatal(err)
	}
	if err = s.exec(`CREATE UNIQUE INDEX one_active_bot_execution ON execution_intents((1)) WHERE state NOT IN ('closed','rejected','cancelled')`); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveExecution(ExecRow{ID: "a", Account: "1", State: "uncertain", Payload: `{"source":"tradingview"}`}); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveExecution(ExecRow{ID: "b", Account: "2", State: "accepted", Payload: `{"source":"tradingview"}`}); err != nil {
		t.Fatal("global index not migrated", err)
	}
	if s.ReserveExecution(ExecRow{ID: "c", Account: "1", State: "accepted", Payload: `{}`}) == nil {
		t.Fatal("same account double reserved")
	}
	rows, err := (ScopedExecution{Inbox: s, Account: "1"}).Executions()
	if err != nil || len(rows) != 1 || rows[0].ID != "a" {
		t.Fatal("account recovery leaked", rows, err)
	}
	rows, err = (ScopedExecution{Inbox: s, Manual: true}).Executions()
	if err != nil || len(rows) != 0 {
		t.Fatal("manual monitor owns automatic intent", rows, err)
	}
}

func TestUnfinishedRecoveryIsNotLimitedByDisplay(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for n := 0; n < 1005; n++ {
		id := fmt.Sprint(n)
		if err = s.ClaimTargets([]TargetJob{{ID: id, DeliveryID: id, ProfileID: "p", AccountID: 1, Policy: "frozen"}}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.TargetJobs()
	if err != nil || len(rows) != 1000 {
		t.Fatal(len(rows), err)
	}
	rows, err = s.UnfinishedTargets()
	if err != nil || len(rows) != 1005 {
		t.Fatal("recovery truncated", len(rows), err)
	}
}
