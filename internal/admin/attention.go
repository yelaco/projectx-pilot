package admin

import (
	"path/filepath"

	"github.com/yelaco/projectx-pilot/internal/attention"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

func (m *Manager) AttentionStatus() attention.Status { return m.notifications.Status() }
func (m *Manager) SaveAttention(actor, password string, enabled bool, webhook string, clear bool) error {
	if err := m.reauthorize(actor, password); err != nil {
		return err
	}
	return m.notifications.Configure(enabled, webhook, clear)
}
func (m *Manager) openAttention() error {
	var err error
	m.notifications, err = attention.Open(filepath.Join(filepath.Dir(m.credentialsPath), "attention-discord.json"))
	return err
}
func (m *Manager) executionAudit(store *storage.Inbox) func(string, string) {
	base := executionAudit(store)
	return func(level, message string) {
		base(level, message)
		if level == "ERROR" && m.notifications != nil {
			m.notifications.Publish(message)
		}
	}
}
