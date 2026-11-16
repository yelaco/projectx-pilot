package admin

import (
	"context"
	"errors"
	"github.com/yelaco/projectx-pilot/internal/realtime"
)

func (m *Manager) userToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	_ = m.executionFactory()
	client := m.apiClient
	m.mu.Unlock()
	if client == nil {
		return "", errors.New("ProjectX credentials required")
	}
	return client.StreamToken(ctx)
}
func (m *Manager) RunUserData(ctx context.Context) { m.refreshUserAccounts(); m.userHub.Run(ctx) }
func (m *Manager) refreshUserAccounts() {
	views, _ := m.fleet.Snapshot()
	ids := []int64{}
	for _, v := range views {
		ids = append(ids, v.AccountID)
	}
	m.userHub.SetAccounts(ids)
}
func (m *Manager) UserDataStatus() realtime.UserStatus { return m.userHub.Status() }
