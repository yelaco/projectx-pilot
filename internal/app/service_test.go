package app

import (
	"context"
	"errors"
	"projectxbot/internal/projectx"
	"testing"
	"time"
)

type fake struct{ fail bool }

func (f *fake) Accounts(context.Context) ([]projectx.Account, error) {
	if f.fail {
		return nil, errors.New("offline")
	}
	return []projectx.Account{{ID: 7, Name: "test", Balance: 50000}}, nil
}
func (f *fake) AvailableContracts(context.Context) ([]projectx.Contract, error) {
	return []projectx.Contract{{ID: "x"}}, nil
}
func TestSnapshotRecovery(t *testing.T) {
	f := &fake{}
	s := New(f, 7)
	if s.Snapshot().Connected {
		t.Fatal("startup claims connected")
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	v := s.Snapshot()
	if v.Selected.ID != 7 || !v.Connected {
		t.Fatal(v)
	}
	v.Selected.Name = "mutation"
	v.Accounts[0].Name = "mutation"
	if s.Snapshot().Selected.Name != "test" || s.Snapshot().Accounts[0].Name != "test" {
		t.Fatal("snapshot aliases internal state")
	}
	f.fail = true
	if s.Refresh(context.Background()) == nil || s.Snapshot().Connected || s.Snapshot().Error == "" {
		t.Fatal("stale state not marked")
	}
	f.fail = false
	if s.Refresh(context.Background()) != nil || !s.Snapshot().Connected {
		t.Fatal("did not recover")
	}
}
func TestExplicitAccountSelection(t *testing.T) {
	s := New(&fake{}, 99)
	if s.Refresh(context.Background()) == nil || s.Snapshot().Connected {
		t.Fatal("missing account accepted")
	}
	s = New(&fake{}, 0)
	if s.Refresh(context.Background()) != nil || s.Snapshot().Selected != nil {
		t.Fatal("implicitly selected account")
	}
}

func TestConcurrentSnapshots(t *testing.T) {
	s := New(&fake{}, 7)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = s.Refresh(context.Background())
		}
	}()
	for i := 0; i < 100; i++ {
		_ = s.Snapshot()
	}
	<-done
}

type blockedReader struct{ entered, release chan struct{} }

func (b *blockedReader) Accounts(context.Context) ([]projectx.Account, error) {
	close(b.entered)
	<-b.release
	return []projectx.Account{{ID: 7}}, nil
}
func (b *blockedReader) AvailableContracts(context.Context) ([]projectx.Contract, error) {
	return nil, nil
}
func TestReconfigureDiscardsInFlightSnapshot(t *testing.T) {
	b := &blockedReader{make(chan struct{}), make(chan struct{})}
	s := New(b, 7)
	done := make(chan error, 1)
	go func() { done <- s.Refresh(context.Background()) }()
	<-b.entered
	s.Configure(nil, 0, time.Minute)
	close(b.release)
	if err := <-done; err == nil {
		t.Fatal("old account snapshot was accepted")
	}
	if s.Snapshot().Connected || s.Snapshot().Selected != nil {
		t.Fatal("stale account leaked after reconfiguration")
	}
}
