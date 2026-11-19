package publicstatus

import (
	"encoding/json"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstallAssetsStayWithinPublicHostAndPublication(t *testing.T) {
	settings := Settings{Enabled: true, Origin: "https://status.example.test"}
	h := Handler(func() (Settings, Snapshot) { return settings, Snapshot{Accounts: []Account{}} })
	request := func(path, host string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://"+host+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	page := request("/", "status.example.test")
	if !strings.Contains(page.Body.String(), `rel="manifest"`) || !strings.Contains(page.Header().Get("Content-Security-Policy"), "manifest-src 'self'") {
		t.Fatal("manifest blocked or unlinked")
	}
	manifest := request("/manifest.webmanifest", "status.example.test")
	var m struct {
		ID, Name, Display, Scope string
		Start                    string `json:"start_url"`
		Icons                    []struct{ Src, Sizes, Type, Purpose string }
	}
	if manifest.Code != 200 || manifest.Header().Get("Content-Type") != "application/manifest+json" || json.Unmarshal(manifest.Body.Bytes(), &m) != nil {
		t.Fatal("manifest unavailable")
	}
	if m.ID != "/" || m.Start != "/" || m.Scope != "/" || m.Display != "standalone" || len(m.Icons) != 2 {
		t.Fatal("invalid installation scope", m)
	}
	for i, icon := range m.Icons {
		w := request(icon.Src, "status.example.test")
		im, err := png.DecodeConfig(w.Body)
		expected := 192
		if i == 1 {
			expected = 512
		}
		if err != nil || im.Width != expected || im.Height != expected || w.Header().Get("Content-Type") != "image/png" {
			t.Fatal("invalid app icon", icon, err)
		}
	}
	for _, path := range []string{"/manifest.webmanifest", "/icon-192.png", "/icon-512.png", "/install.js"} {
		if request(path, "pilot.example.test").Code != 404 {
			t.Fatal("install asset escaped exact host")
		}
		settings.Enabled = false
		if request(path, "status.example.test").Code != 404 {
			t.Fatal("disabled publication exposes install assets")
		}
		settings.Enabled = true
	}
}
