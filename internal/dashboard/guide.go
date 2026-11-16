package dashboard

import (
	"bytes"
	_ "embed"
	"html/template"
	"net/http"
	"strconv"

	"github.com/yelaco/projectx-pilot/internal/session"
)

// The guide is a standalone, static document. It never receives runtime account,
// credential, administrator, profile or execution state, and needs no login.
//
//go:embed guide.html
var guideHTML string

//go:embed guide.css
var guideCSS string

var guidePage = template.Must(template.New("guide").Parse(guideHTML))

func serveGuide(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/guide", "/guide/", "/guide.css":
	default:
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	if r.URL.Path == "/guide/" {
		http.Redirect(w, r, "/guide", http.StatusPermanentRedirect)
		return true
	}
	content := []byte(guideCSS)
	contentType := "text/css; charset=utf-8"
	if r.URL.Path == "/guide" {
		var output bytes.Buffer
		if err := guidePage.Execute(&output, struct{ CalendarRevision string }{session.CalendarRevision}); err != nil {
			http.Error(w, "Guide unavailable", http.StatusInternalServerError)
			return true
		}
		content = output.Bytes()
		contentType = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(content)
	}
	return true
}
