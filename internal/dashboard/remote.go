package dashboard

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/yelaco/projectx-pilot/internal/admin"
	"github.com/yelaco/projectx-pilot/internal/app"
	"github.com/yelaco/projectx-pilot/internal/config"
)

type originContextKey struct{}

// AdminHandlerWithRemote keeps the listener loopback-only. The public host must
// pass Access signature/issuer/audience/email checks AND normal app authentication.
func AdminHandlerWithRemote(s *app.Service, token, address string, m *admin.Manager, remote config.RemoteDashboard) (http.Handler, error) {
	if err := remote.Validate(); err != nil {
		return nil, err
	}
	return remoteHandler(handler(s, token, m), address, remote, newAccessVerifier(remote)), nil
}
func remoteHandler(next http.Handler, address string, remote config.RemoteDashboard, verifier *accessVerifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never trust X-Forwarded-Host/Proto to select a public origin or bypass Access.
		if r.Host == address {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), originContextKey{}, "http://"+address)))
			return
		}
		u, _ := url.Parse(remote.Origin)
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if remote.Origin == "" || r.Host != u.Host || err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			http.Error(w, "invalid host or proxy", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		if err := verifier.verify(r.Context(), r.Header.Get("Cf-Access-Jwt-Assertion")); err != nil {
			http.Error(w, "Cloudflare Access authorization required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), originContextKey{}, remote.Origin)))
	})
}

type accessClaims struct {
	jwt.RegisteredClaims
	Email string `json:"email"`
	Type  string `json:"type"`
}
type accessVerifier struct {
	config     config.RemoteDashboard
	client     *http.Client
	mu         sync.Mutex
	keys       map[string]*rsa.PublicKey
	validUntil time.Time
	retryAfter time.Time
	now        func() time.Time
}

func newAccessVerifier(c config.RemoteDashboard) *accessVerifier {
	return &accessVerifier{config: c, now: time.Now, client: &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (v *accessVerifier) verify(ctx context.Context, raw string) error {
	if raw == "" || len(raw) > 16384 {
		return errors.New("missing Access token")
	}
	claims := new(accessClaims)
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok || kid == "" || len(kid) > 256 {
			return nil, errors.New("invalid key ID")
		}
		return v.key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(v.config.TeamDomain), jwt.WithAudience(v.config.Audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(v.now))
	if err != nil || !token.Valid || claims.Subject == "" || claims.Type != "app" {
		return errors.New("invalid Access token")
	}
	for _, email := range v.config.Emails {
		if strings.EqualFold(email, claims.Email) {
			return nil
		}
	}
	return errors.New("Access identity not allowed")
}
func (v *accessVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if now.Before(v.validUntil) && v.keys[kid] != nil {
		return v.keys[kid], nil
	}
	if now.Before(v.retryAfter) {
		return nil, errors.New("Access keys unavailable")
	}
	// Bound key refreshes even when an attacker supplies arbitrary key IDs.
	v.retryAfter = now.Add(time.Minute)
	check, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(check, http.MethodGet, v.config.TeamDomain+"/cdn-cgi/access/certs", nil)
	if err != nil {
		return nil, err
	}
	response, err := v.client.Do(req)
	if err != nil {
		return nil, errors.New("Access keys unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("Access keys unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(body) > 65536 {
		return nil, errors.New("invalid Access keys")
	}
	var data struct {
		Keys []struct{ Kty, Kid, N, E string }
	}
	if json.Unmarshal(body, &data) != nil || len(data.Keys) > 20 {
		return nil, errors.New("invalid Access keys")
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range data.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, e1 := base64.RawURLEncoding.DecodeString(k.N)
		e, e2 := base64.RawURLEncoding.DecodeString(k.E)
		if e1 != nil || e2 != nil || len(n) < 256 || len(n) > 1024 || len(e) == 0 || len(e) > 4 {
			continue
		}
		exp := new(big.Int).SetBytes(e).Int64()
		if exp < 3 || exp%2 == 0 || exp > 2147483647 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exp)}
	}
	if len(keys) == 0 {
		return nil, errors.New("no Access keys")
	}
	v.keys = keys
	v.validUntil = now.Add(time.Hour)
	if keys[kid] == nil {
		return nil, errors.New("unknown Access key")
	}
	return keys[kid], nil
}
