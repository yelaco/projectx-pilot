package admin

import (
	"context"
	"time"

	"github.com/yelaco/projectx-pilot/internal/reporting"
)

func (m *Manager) runDailyTradingReports(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if m.tradingReports.Status().Enabled {
			now := m.now()
			date, _, _ := reporting.LatestWindow(now)
			m.mu.Lock()
			ids := []int64{}
			if m.executionConfig.AccountID > 0 {
				ids = append(ids, m.executionConfig.AccountID)
			}
			if m.c.AccountID > 0 {
				ids = append(ids, m.c.AccountID)
			}
			for _, policy := range m.policies.TradeManagers {
				ids = append(ids, policy.AccountIDs...)
			}
			var reader reporting.Reader
			if m.credentials.Username != "" && m.credentials.APIKey != "" {
				_ = m.executionFactory()
				if m.apiClient != nil {
					reader = m.apiClient
				}
			}
			m.mu.Unlock()
			historical, err := m.executionStore.DailyReportingAccounts(date)
			if err == nil {
				ids = append(ids, historical...)
				call, cancel := context.WithTimeout(ctx, 45*time.Second)
				err = reporting.Process(call, now, ids, reader, m.executionStore, m.tradingReports)
				cancel()
			}
			if err != nil && ctx.Err() == nil {
				m.Note("WARN", "reporting", "Daily trading report preparation incomplete; remaining unclaimed accounts will be checked again. Trading state unchanged.")
			}
		}
		timer.Reset(time.Minute)
	}
}
