package dashboard

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yelaco/projectx-pilot/internal/admin"
)

func TestSystemHealthChecklistAndNarrowEndpointAreAuthenticatedAndReadOnly(t *testing.T) {
	m, password, h := managementFixture(t)
	page := requestAs(h, "local", password, "GET", "/system-health", nil)
	if page.Code != 200 {
		t.Fatal("health page unavailable", page.Code, page.Body.String())
	}
	for _, want := range []string{"System checklist", "Show attention, blocked and unverified", `id="system-health-checks"`, "No timestamped observation", "Export checklist", "System health"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatal("missing checklist content", want)
		}
	}
	before := m.ExecutionStatus()
	for _, path := range []string{"/system-health", "/api/system-health"} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatal("unauthenticated health access", path, w.Code)
		}
	}
	api := requestAs(h, "local", password, "GET", "/api/system-health", nil)
	if api.Code != 200 {
		t.Fatal("health endpoint unavailable", api.Code)
	}
	var result admin.SystemHealth
	if err := json.Unmarshal(api.Body.Bytes(), &result); err != nil || len(result.Checks) == 0 {
		t.Fatal("invalid checklist export", err)
	}
	if strings.Contains(api.Body.String(), "WebhookURLs") || strings.Contains(api.Body.String(), "Signal") || strings.Contains(api.Body.String(), "SubmissionDetail") {
		t.Fatal("health endpoint exposed unrelated monitor data")
	}
	after := m.ExecutionStatus()
	if after.Armed != before.Armed || after.RunRequested != before.RunRequested || len(after.Intents) != len(before.Intents) {
		t.Fatal("health observation changed execution")
	}
	if w := requestAs(h, "local", password, "POST", "/system-health", nil); w.Code != 405 {
		t.Fatal("health page accepts an action", w.Code)
	}
}
