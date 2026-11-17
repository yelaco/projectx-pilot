package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/config"
)

func remoteFixture(t *testing.T) (config.RemoteDashboard, *accessVerifier, func(func(*accessClaims)) *http.Request) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	c := config.RemoteDashboard{Origin: "https://pilot.example.test", TeamDomain: "https://unit.cloudflareaccess.com", Audience: strings.Repeat("a", 64), Emails: []string{"owner@example.test"}}
	v := newAccessVerifier(c)
	v.keys = map[string]*rsa.PublicKey{"unit": &key.PublicKey}
	v.validUntil = time.Now().Add(time.Hour)
	request := func(change func(*accessClaims)) *http.Request {
		claims := accessClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: c.TeamDomain, Subject: "user", Audience: jwt.ClaimStrings{c.Audience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute))}, Email: c.Emails[0], Type: "app"}
		if change != nil {
			change(&claims)
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "unit"
		raw, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", c.Origin+"/", nil)
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Cf-Access-Jwt-Assertion", raw)
		return r
	}
	return c, v, request
}
func TestRemoteAccessRequiresSignedIdentityAndAppLogin(t *testing.T) {
	c, v, request := remoteFixture(t)
	h := remoteHandler(handler(app.New(nil, 0), "test-password", nil), "127.0.0.1:8080", c, v)
	for _, tc := range []struct {
		name   string
		change func(*accessClaims)
		auth   bool
		want   int
	}{
		{"both layers", nil, true, 200}, {"app login missing", nil, false, 401},
		{"wrong email", func(c *accessClaims) { c.Email = "other@example.test" }, true, 403},
		{"wrong audience", func(c *accessClaims) { c.Audience = jwt.ClaimStrings{"other"} }, true, 403},
		{"wrong issuer", func(c *accessClaims) { c.Issuer = "https://evil.example" }, true, 403},
		{"expired", func(c *accessClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }, true, 403},
		{"missing expiry", func(c *accessClaims) { c.ExpiresAt = nil }, true, 403},
		{"service token", func(c *accessClaims) { c.Type = "service" }, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request(tc.change)
			if tc.auth {
				r.SetBasicAuth("local", "test-password")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
		})
	}
	for _, tc := range []string{"missing", "tampered", "foreign-peer", "foreign-host", "forwarded-spoof"} {
		t.Run(tc, func(t *testing.T) {
			r := request(nil)
			r.SetBasicAuth("local", "test-password")
			switch tc {
			case "missing":
				r.Header.Del("Cf-Access-Jwt-Assertion")
			case "tampered":
				raw := r.Header.Get("Cf-Access-Jwt-Assertion")
				parts := strings.Split(raw, ".")
				sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
				sig[0] ^= 1
				parts[2] = base64.RawURLEncoding.EncodeToString(sig)
				r.Header.Set("Cf-Access-Jwt-Assertion", strings.Join(parts, "."))
			case "foreign-peer":
				r.RemoteAddr = "192.0.2.1:1234"
			case "foreign-host":
				r.Host = "evil.example"
			case "forwarded-spoof":
				r.Header.Del("Cf-Access-Jwt-Assertion")
				r.Header.Set("X-Forwarded-Host", "127.0.0.1:8080")
				r.Header.Set("X-Forwarded-Proto", "https")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal(w.Code)
			}
		})
	}
	// Local recovery remains available even when Access/key retrieval is down.
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/", nil)
	r.SetBasicAuth("local", "test-password")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestRemoteFormsPreserveCSRFAndPasswordChecks(t *testing.T) {
	m, password, _ := managementFixture(t)
	c, v, request := remoteFixture(t)
	h := remoteHandler(handler(app.New(nil, 0), password, m), "127.0.0.1:8080", c, v)
	get := request(nil)
	get.URL.Path = "/settings"
	get.SetBasicAuth("local", password)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, get)
	marker := `name="csrf" value="`
	_, tail, ok := strings.Cut(w.Body.String(), marker)
	if !ok {
		t.Fatal("csrf absent")
	}
	csrf, _, _ := strings.Cut(tail, `"`)
	for _, tc := range []struct {
		origin, csrf string
		want         int
	}{{c.Origin, csrf, 303}, {"https://evil.example", csrf, 403}, {c.Origin, "wrong", 403}, {"http://pilot.example.test", csrf, 403}} {
		values := url.Values{"csrf": {tc.csrf}}
		r := request(nil)
		r.Method = "POST"
		r.URL.Path = "/fleet/pause"
		r.Body = http.NoBody
		r.Body = io.NopCloser(strings.NewReader(values.Encode()))
		r.ContentLength = int64(len(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", tc.origin)
		r.SetBasicAuth("local", password)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.origin, w.Code, w.Body.String())
		}
	}
}
func TestAccessKeyFetchBoundedCachedAndFailsClosed(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "unit", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	defer server.Close()
	v := newAccessVerifier(config.RemoteDashboard{TeamDomain: server.URL})
	for n := 0; n < 2; n++ {
		if _, err := v.key(context.Background(), "unit"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := v.key(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown key accepted")
	}
	if calls != 1 {
		t.Fatalf("key refresh storm: %d", calls)
	}
}
