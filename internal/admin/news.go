package admin

import (
	"context"
	"projectxbot/internal/news"
)

func (m *Manager) RunNews(ctx context.Context) {
	m.news.Run(ctx, func(level, message string) { m.Note(level, "news", message) })
}
func (m *Manager) NewsStatus() news.Status { return m.news.Status() }
func (m *Manager) RefreshNews(ctx context.Context) error {
	err := m.news.Refresh(ctx)
	if err != nil {
		m.Note("WARN", "news", "Manual news refresh failed: "+err.Error())
	} else {
		m.Note("INFO", "news", "Forex Factory weekly news feed refreshed successfully.")
	}
	return err
}
