package observability

import (
	"io"
	"log/slog"
	"path/filepath"
	"projectxbot/internal/storage"
	"strings"
	"testing"
)

func TestOnlySanitizedEventsPersist(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	logger := slog.New(Handler{Console: slog.NewJSONHandler(io.Discard, nil), Store: store})
	logger.Warn("read-only refresh failed", "error", "SECRET_TOKEN")
	logger.Info("unknown SECRET_PAYLOAD")
	rows, err := store.Activity()
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if strings.Contains(rows[0].Message, "SECRET") {
		t.Fatal("secret persisted")
	}
}
