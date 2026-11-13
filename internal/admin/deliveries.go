package admin

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"

	"projectxbot/internal/storage"
)

func (m *Manager) remoteStore() (*storage.Inbox, error) {
	if m.remote == nil {
		s, err := storage.Open(filepath.Join(filepath.Dir(m.credentialsPath), "remote-alerts.db"))
		if err != nil {
			return nil, errors.New("webhook inbox unavailable")
		}
		m.remote = s
	}
	return m.remote, nil
}
func (m *Manager) Deliveries() ([]storage.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.remoteStore()
	if err != nil {
		return nil, err
	}
	return s.Recent()
}
func (m *Manager) RejectDelivery(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.remoteStore()
	if err != nil {
		return err
	}
	return s.Review(id, "rejected")
}

// ImportDelivery is only invoked by an authenticated, CSRF-protected local form.
// The webhook receiver has no reference to Manager or this method.
func (m *Manager) ImportDelivery(id, profileID, contractID string) error {
	m.mu.Lock()
	if _, err := m.executionStore.AutomationReceipt(id); err == nil {
		m.mu.Unlock()
		return errors.New("this alert already entered automatic processing; inspect its execution result")
	} else if !errors.Is(err, storage.ErrNotFound) {
		m.mu.Unlock()
		return err
	}
	remote, err := m.remoteStore()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	delivery, err := remote.Get(id)
	if err != nil {
		return err
	}
	if delivery.Status != "pending_review" {
		return errors.New("delivery is no longer awaiting local import")
	}
	if delivery.ProfileID != "" && delivery.ProfileID != profileID {
		return errors.New("delivery belongs to another webhook profile")
	}
	payload, _ := json.Marshal(delivery.Signal)
	// Recover a crash between the local insert and marking the delivery imported.
	// Only an exact signal/profile/contract/account match can complete that copy.
	m.mu.Lock()
	local, err := m.inboxStore()
	account := ""
	for _, p := range m.policies.Automations {
		if p.ID == profileID {
			account = strconv.FormatInt(p.AccountID, 10)
		}
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	existing, lookupErr := local.Get(id)
	if lookupErr == nil {
		previous, _ := json.Marshal(existing.Signal)
		if string(previous) != string(payload) || existing.ProfileID != profileID || existing.ContractID != contractID || existing.AccountID != account {
			return errors.New("signal ID conflicts with a different local import")
		}
	} else if errors.Is(lookupErr, storage.ErrNotFound) {
		if err = m.ImportSignal(profileID, contractID, string(payload)); err != nil {
			return err
		}
	} else {
		return lookupErr
	}
	// These two databases are not one transaction. Local unique IDs make retries
	// safe if the process exits before this status update.
	return remote.Review(id, "reviewed")
}
