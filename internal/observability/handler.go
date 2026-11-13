package observability

import (
	"context"
	"log/slog"

	"projectxbot/internal/storage"
)

// Handler preserves ordinary console logging while writing a strict allowlist of
// sanitized operational messages to the dashboard. Attributes are never stored.
type Handler struct {
	Console slog.Handler
	Store   *storage.Inbox
}

func (h Handler) Enabled(c context.Context, l slog.Level) bool { return h.Console.Enabled(c, l) }
func (h Handler) Handle(c context.Context, r slog.Record) error {
	err := h.Console.Handle(c, r)
	safe := ""
	switch r.Message {
	case "read-only snapshot refreshed":
		safe = "ProjectX account and contract snapshot refreshed successfully."
	case "read-only refresh failed":
		safe = "ProjectX snapshot failed. Connection status shows a sanitized reason."
	case "read-only monitor started":
		safe = "Local admin and monitoring service started; trading disabled."
	case "bot stopped":
		safe = "Local bot stopped with an error; inspect the local journal."
	}
	if safe != "" {
		_ = h.Store.RecordActivity(r.Level.String(), "service", safe)
	}
	return err
}
func (h Handler) WithAttrs(a []slog.Attr) slog.Handler {
	return Handler{h.Console.WithAttrs(a), h.Store}
}
func (h Handler) WithGroup(g string) slog.Handler { return Handler{h.Console.WithGroup(g), h.Store} }
