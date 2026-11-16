package dashboard

import (
	"net/http/httptest"
	"github.com/yelaco/projectx-pilot/internal/app"
	"strings"
	"testing"
)

func TestAccessControls(t *testing.T) {
	s := app.New(nil, 0)
	token := strings.Repeat("x", 32)
	h := HandlerForAddress(s, token, "127.0.0.1:8080")
	for _, tc := range []struct {
		method, path, host string
		auth               bool
		want               int
	}{{"GET", "/", "127.0.0.1:8080", false, 401}, {"GET", "/", "127.0.0.1:8080", true, 200}, {"GET", "/api/status", "127.0.0.1:8080", true, 200}, {"POST", "/", "127.0.0.1:8080", true, 405}, {"GET", "/", "attacker.example", true, 403}, {"GET", "/healthz", "127.0.0.1:8080", false, 503}, {"GET", "/approve", "127.0.0.1:8080", true, 404}} {
		r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
		if tc.auth {
			r.SetBasicAuth("local", token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v got %d", tc, w.Code)
		}
		if strings.Contains(w.Body.String(), token) {
			t.Fatal("token leaked")
		}
	}
}
