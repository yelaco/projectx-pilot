package dashboard

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/yelaco/projectx-pilot/internal/admin"
	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/config"
	"github.com/yelaco/projectx-pilot/internal/risk"
)

func TestOperationDaysPageAndProtectedSave(t *testing.T) {
	t.Setenv("PROJECTX_USERNAME", "")
	t.Setenv("PROJECTX_API_KEY", "")
	t.Setenv("BOT_DASHBOARD_TOKEN", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m, password, err := admin.Open(c, path, filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	p := risk.Defaults()
	p.ID = "first"
	p.Name = "First manager"
	p.AccountIDs = []int64{42}
	if err := m.SaveTradeManager(p); err != nil {
		t.Fatal(err)
	}
	h := AdminHandler(app.New(nil, 0), password, "127.0.0.1:8080", m)
	get := httptest.NewRequest("GET", "http://127.0.0.1:8080/operation-days?week=2026-12-05", nil)
	get.SetBasicAuth("local", password)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, get)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, text := range []string{"General plan", "First manager", "No calendar data", "2026-12-05", "2026-12-11"} {
		if !strings.Contains(body, text) {
			t.Fatal("missing", text)
		}
	}
	if strings.Index(body, "Saturday") > strings.Index(body, "Friday") {
		t.Fatal("wrong calendar order")
	}
	csrf := regexp.MustCompile(`name="csrf" value="([a-f0-9]+)"`).FindStringSubmatch(body)
	if len(csrf) != 2 {
		t.Fatal("missing CSRF")
	}
	for _, tc := range []struct {
		csrf, origin, manager, date string
		want                        int
	}{
		{"bad", "http://127.0.0.1:8080", "first", "2026-12-10", 403},
		{csrf[1], "http://evil.test", "first", "2026-12-10", 403},
		{csrf[1], "http://127.0.0.1:8080", "missing", "2026-12-10", 400},
		{csrf[1], "http://127.0.0.1:8080", "first", "2026-12-14", 400},
		{csrf[1], "http://127.0.0.1:8080", "first", "2026-12-10", 303},
	} {
		v := url.Values{"csrf": {tc.csrf}, "week": {"2026-12-05"}, "trade_manager_id": {tc.manager}, "blocked": {tc.date}}
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/operation-days", strings.NewReader(v.Encode()))
		r.SetBasicAuth("local", password)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, tc.want, w.Body.String())
		}
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.NoTradeDates) != 0 || len(saved.TradeManagerNoTradeDates["first"]) != 1 || saved.TradeManagerNoTradeDates["first"][0] != "2026-12-10" || saved.NewsFilter != "us_fomc" {
		t.Fatal("incorrect scope saved")
	}
}
