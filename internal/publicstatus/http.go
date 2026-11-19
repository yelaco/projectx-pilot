package publicstatus

import (
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

//go:embed page.html status.css status.js
var assets embed.FS
var page = template.Must(template.New("public").ParseFS(assets, "page.html"))

func serve(w http.ResponseWriter, r *http.Request, source func() (Settings, Snapshot)) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	settings, snap := source()
	u, _ := url.Parse(settings.Origin)
	if !settings.Enabled || u == nil || r.Host != u.Host {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Read-only status", 405)
		return
	}
	path := r.URL.Path
	switch path {
	case "/status.css", "/status.js":
		data, _ := assets.ReadFile(strings.TrimPrefix(path, "/"))
		ct := "text/css; charset=utf-8"
		if path == "/status.js" {
			ct = "application/javascript; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	case "/api/status":
		// Serialize only the public DTO, never configuration or internal snapshots.
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodHead {
			_ = json.NewEncoder(w).Encode(snap)
		}
	default:
		slug := ""
		if path != "/" {
			if !strings.HasPrefix(path, "/accounts/") {
				http.NotFound(w, r)
				return
			}
			slug = strings.TrimPrefix(path, "/accounts/")
			found := false
			for _, a := range snap.Accounts {
				if a.Slug == slug {
					found = true
				}
			}
			if !found {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method != http.MethodHead {
			_ = page.ExecuteTemplate(w, "page.html", slug)
		}
	}
}
