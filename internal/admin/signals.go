package admin

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yelaco/projectx-pilot/internal/signal"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

func (m *Manager) inboxStore() (*storage.Inbox, error) {
	if m.inbox == nil {
		s, err := storage.Open(filepath.Join(filepath.Dir(m.credentialsPath), "signals.db"))
		if err != nil {
			return nil, errors.New("could not open local signal inbox")
		}
		m.inbox = s
	}
	return m.inbox, nil
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var a, b error
	if m.inbox != nil {
		a = m.inbox.Close()
	}
	if m.remote != nil {
		b = m.remote.Close()
	}
	var c error
	if m.activity != nil {
		c = m.activity.Close()
	}
	var d error
	if m.executionStore != nil {
		d = m.executionStore.Close()
	}
	var leaseErr error
	if m.executionLease != nil {
		leaseErr = m.executionLease.Close()
		m.executionLease = nil
	}
	return errors.Join(a, b, c, d, leaseErr)
}
func (m *Manager) Signals() ([]storage.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.inboxStore()
	if err != nil {
		return nil, err
	}
	return s.Recent()
}
func (m *Manager) ImportSignal(profileID, contractID, payload string) error {
	sig, err := signal.Parse(payload)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var profile *Automation
	for _, a := range m.policies.Automations {
		if a.ID == profileID {
			v := a
			profile = &v
			break
		}
	}
	if profile == nil {
		return errors.New("select an existing automation profile")
	}
	if profile.TradeManagerID != "" {
		for _, p := range m.policies.TradeManagers {
			if p.ID == profile.TradeManagerID && p.Routing == "multiple" {
				return errors.New("this profile routes to multiple accounts; multi-account review and execution are not implemented")
			}
		}
	}
	if profile.Source != "tradingview-manual" && profile.Source != "local-manual" {
		return errors.New("this profile does not accept manually imported alerts")
	}
	if sig.Strategy != profile.Strategy {
		return errors.New("signal strategy does not match selected profile")
	}
	contains := func(list []string, s string) bool {
		for _, v := range list {
			if v == s {
				return true
			}
		}
		return false
	}
	if !contains(profile.Instruments, sig.Symbol) {
		return errors.New("signal instrument is not allowed by this profile")
	}
	if m.service == nil {
		return errors.New("contract discovery required before import")
	}
	snapshot := m.service.Snapshot()
	if !snapshot.Connected || time.Since(snapshot.UpdatedAt) > m.c.RefreshInterval+time.Minute {
		return errors.New("refresh ProjectX first; contract metadata is unavailable or stale")
	}
	foundAccount := false
	for _, a := range snapshot.Accounts {
		if a.ID == profile.AccountID {
			foundAccount = true
		}
	}
	if !foundAccount {
		return errors.New("profile account was not found in the current account snapshot")
	}
	tick := 0.0
	for _, c := range snapshot.Contracts {
		if c.ID == contractID && c.ActiveContract && strings.HasSuffix(c.SymbolID, "."+sig.Symbol) && c.TickValue > 0 {
			tick = c.TickSize
			break
		}
	}
	if tick <= 0 {
		return errors.New("select an active contract matching the signal symbol from discovered metadata")
	}
	if err = sig.Validate(time.Now().UTC(), m.c.SignalMaxAge, tick); err != nil {
		return err
	}
	store, err := m.inboxStore()
	if err != nil {
		return err
	}
	return store.Insert(storage.Record{Signal: sig, ProfileID: profileID, ContractID: contractID, AccountID: strconv.FormatInt(profile.AccountID, 10)})
}

// Review records human review only. It is deliberately not execution authorization.
func (m *Manager) ReviewSignal(id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.inboxStore()
	if err != nil {
		return err
	}
	return s.Review(id, status)
}
