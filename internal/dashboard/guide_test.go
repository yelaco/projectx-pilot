package dashboard

import (
	"github.com/yelaco/projectx-pilot/internal/app"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestStandaloneGuideNeedsNoRuntimeStateOrLogin(t *testing.T) {
	secret := strings.Repeat("private-test-secret", 3)
	// A nil application makes accidental snapshot access fail: this route must
	// remain a static document, even before the ProjectX service is configured.
	h := HandlerForAddress(nil, secret, "127.0.0.1:8080")
	for _, path := range []string{"/guide", "/guide.css"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path+"?account=do-not-display-this", nil)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			expectedType := "text/html; charset=utf-8"
			if path == "/guide.css" {
				expectedType = "text/css; charset=utf-8"
			}
			if response.Header().Get("Content-Type") != expectedType {
				t.Fatal("incorrect content type")
			}
			if response.Header().Get("Content-Length") != strconv.Itoa(response.Body.Len()) {
				t.Fatal("incorrect content length")
			}
			if response.Header().Get("WWW-Authenticate") != "" {
				t.Fatal("guide requested an admin login")
			}
			if !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("security headers absent")
			}
			body := response.Body.String()
			for _, forbidden := range []string{secret, "do-not-display-this", "data-live-monitor", "/api/monitor", "<script", "<form"} {
				if strings.Contains(body, forbidden) {
					t.Fatal("static guide contains runtime data or controls", forbidden)
				}
			}
			if path == "/guide" {
				for _, want := range []string{"Quick start", "Connection &amp; accounts", "Trade managers &amp; position sizing", "Webhook &amp; HTTPS delivery", "Monitoring &amp; status meanings", "Troubleshooting", "Read-only", "2026 holiday cutoffs", "/guide.css"} {
					if !strings.Contains(body, want) {
						t.Fatal("missing guide content", want)
					}
				}
				if strings.Contains(body, `class="sidebar"`) || strings.Contains(body, "/admin.js") {
					t.Fatal("guide rendered inside admin shell")
				}
			}
			head := httptest.NewRecorder()
			h.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "http://127.0.0.1:8080"+path, nil))
			if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != response.Header().Get("Content-Length") {
				t.Fatal("HEAD does not match GET")
			}
		})
	}
	// Public static documentation must not make existing APIs public.
	for _, path := range []string{"/", "/api/status", "/api/monitor", "/admin.css"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil))
		if response.Code != 401 {
			t.Fatal("admin authentication changed", path, response.Code)
		}
	}
}

func TestGuideMethodsAndHostBoundary(t *testing.T) {
	h := HandlerForAddress(nil, strings.Repeat("x", 32), "127.0.0.1:8080")
	for _, path := range []string{"/guide", "/guide/", "/guide.css"} {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(method, "http://127.0.0.1:8080"+path, nil))
			if response.Code != 405 || response.Header().Get("Allow") != "GET, HEAD" {
				t.Fatal("guide accepts a mutation", path, method, response.Code)
			}
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", "http://foreign.example"+path, nil))
		if response.Code != 403 {
			t.Fatal("guide bypassed host validation", path)
		}
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1:8080/guide/", nil))
	if response.Code != 308 || response.Header().Get("Location") != "/guide" {
		t.Fatal("guide canonical URL missing")
	}
}

func TestGuideNavigationAndDashboardLink(t *testing.T) {
	ids := map[string]bool{}
	for _, match := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(guideHTML, -1) {
		if ids[match[1]] {
			t.Fatal("duplicate section", match[1])
		}
		ids[match[1]] = true
	}
	for _, match := range regexp.MustCompile(`href="#([^"]+)"`).FindAllStringSubmatch(guideHTML, -1) {
		if !ids[match[1]] {
			t.Fatal("broken guide jump link", match[1])
		}
	}
	for _, match := range regexp.MustCompile(`href="(/[^"#]*)"`).FindAllStringSubmatch(guideHTML, -1) {
		path := match[1]
		if path == "/guide" || path == "/guide.css" {
			continue
		}
		name, _, _, _ := pageInfo(path)
		if name == "" {
			t.Fatal("guide links to an unknown dashboard route", path)
		}
	}
	m, password, _ := managementFixture(t)
	response := requestAs(AdminHandler(nil, password, "127.0.0.1:8080", m), "local", password, "GET", "/guide", nil)
	if response.Code != 200 {
		t.Fatal("managed guide unavailable")
	}
	// Render a real dashboard to verify that the button opens the separate guide
	// without replacing in-progress settings forms.
	service := app.New(nil, 0)
	response = requestAs(AdminHandler(service, password, "127.0.0.1:8080", m), "local", password, "GET", "/", nil)
	if response.Code != 200 {
		t.Fatal("dashboard unavailable", response.Code)
	}
	link := regexp.MustCompile(`<a class="guide-button"[^>]*>.*?</a>`).FindString(response.Body.String())
	for _, required := range []string{`href="/guide"`, `target="_blank"`, `rel="noopener noreferrer"`, "Guide", "ⓘ"} {
		if !strings.Contains(link, required) {
			t.Fatal("missing guide button behavior", required)
		}
	}
}
