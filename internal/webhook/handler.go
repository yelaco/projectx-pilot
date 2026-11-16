// Package webhook only accepts and persists unassigned alerts. It has no trading client.
package webhook

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"github.com/yelaco/projectx-pilot/internal/ingress"
	"sync"
	"time"

	"github.com/yelaco/projectx-pilot/internal/signal"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

type Sink interface {
	Insert(storage.Record) error
	Get(string) (storage.Record, error)
}
type Options struct {
	AfterStore func()
	Resolve    func(string) (ingress.Route, bool)
	Enabled    bool
	Token      string
	MaxAge     time.Duration
	Now        func() time.Time
}

func New(sink Sink, o Options) (http.Handler, error) {
	if !o.Enabled {
		return http.NotFoundHandler(), nil
	}
	if sink == nil || (o.Resolve == nil && len(o.Token) < 32) || o.MaxAge < time.Second || o.MaxAge > 15*time.Minute {
		return nil, errors.New("webhook requires a store, a separate 32+ character token and max age 1s..15m")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	audit := func(level, message string) {
		if a, ok := sink.(interface {
			RecordActivity(string, string, string) error
		}); ok {
			_ = a.RecordActivity(level, "webhook", message)
		}
	}
	expected := sha256.Sum256([]byte(o.Token))
	slots := make(chan struct{}, 4)
	var mu sync.Mutex
	var window time.Time
	count := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/healthz" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"listening","execution":false}`))
			return
		}
		var route ingress.Route
		if o.Resolve != nil {
			var ok bool
			route, ok = o.Resolve(r.URL.Path)
			if !ok {
				http.NotFound(w, r)
				return
			}
		} else if r.URL.Path != "/tradingview" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", 405)
			return
		}
		// No forwarding headers are trusted; rate/concurrency limits are global and bounded.
		mu.Lock()
		now := o.Now()
		if window.IsZero() || now.Sub(window) >= time.Minute {
			window = now
			count = 0
		}
		count++
		allowed := count <= 60
		mu.Unlock()
		if !allowed {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limited", 429)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "busy; retry later", 503)
			return
		}
		tracked := &statusWriter{ResponseWriter: w}
		w = tracked
		defer func() {
			if tracked.status >= 400 {
				audit("WARN", fmt.Sprintf("Webhook request rejected (HTTP %d).", tracked.status))
			} else if tracked.status == 200 {
				audit("INFO", "Duplicate webhook delivery ignored (HTTP 200).")
			}
		}()
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			http.Error(w, "application/json required", 415)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		// Decode a strict envelope, rejecting duplicates and unrecognized fields.
		d := json.NewDecoder(r.Body)
		first, err := d.Token()
		if err != nil || first != json.Delim('{') {
			http.Error(w, "invalid envelope", 400)
			return
		}
		values := map[string]json.RawMessage{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				http.Error(w, "invalid envelope", 400)
				return
			}
			name, ok := key.(string)
			if !ok || (name != "delivery_token" && name != "signal") {
				http.Error(w, "unsupported envelope field", 400)
				return
			}
			if _, duplicate := values[name]; duplicate {
				http.Error(w, "duplicate envelope field", 400)
				return
			}
			var value json.RawMessage
			if d.Decode(&value) != nil {
				http.Error(w, "invalid envelope", 400)
				return
			}
			values[name] = value
		}
		if _, err = d.Token(); err != nil {
			http.Error(w, "invalid envelope", 400)
			return
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			http.Error(w, "invalid or oversized envelope", 400)
			return
		}
		var token string
		if json.Unmarshal(values["delivery_token"], &token) != nil {
			audit("WARN", "Delivery rejected: authentication failed.")
			http.Error(w, "unauthorized", 401)
			return
		}
		requestExpected := expected
		if o.Resolve != nil {
			requestExpected = sha256.Sum256([]byte(route.Token))
		}
		actual := sha256.Sum256([]byte(token))
		if subtle.ConstantTimeCompare(actual[:], requestExpected[:]) != 1 {
			audit("WARN", "Delivery rejected: authentication failed.")
			http.Error(w, "unauthorized", 401)
			return
		}
		sig, err := signal.Parse(string(values["signal"]))
		if err != nil {
			audit("WARN", err.Error())
			slog.Warn("webhook signal rejected", "status", 400, "reason", err.Error())
			http.Error(w, err.Error(), 400)
			return
		}
		if o.Resolve != nil {
			matches := false
			for _, symbol := range route.Symbols {
				if symbol == sig.Symbol {
					matches = true
				}
			}
			if sig.Strategy != route.Strategy || !matches {
				audit("WARN", "Delivery rejected: strategy or instrument does not match the webhook profile.")
				http.Error(w, "signal does not match webhook profile", 422)
				return
			}
		}
		if validationErr := sig.ValidateSchema(now, o.MaxAge); validationErr != nil {
			// Validator messages are fixed strings: never log payloads or credentials.
			audit("WARN", validationErr.Error())
			slog.Warn("webhook signal rejected", "status", 422, "reason", validationErr.Error())
			http.Error(w, validationErr.Error(), 422)
			return
		}
		if err = sink.Insert(storage.Record{Signal: sig, ProfileID: route.ID, ReceivedAt: now.UTC().Format(time.RFC3339Nano)}); err != nil {
			if errors.Is(err, storage.ErrDuplicate) {
				previous, lookupErr := sink.Get(sig.ID)
				if lookupErr != nil {
					http.Error(w, "inbox unavailable; retry later", 503)
					return
				}
				a, _ := json.Marshal(previous.Signal)
				b, _ := json.Marshal(sig)
				if string(a) != string(b) || previous.ProfileID != route.ID {
					audit("WARN", "Delivery rejected: conflicting duplicate ID.")
					http.Error(w, "signal ID conflicts with a prior delivery", 409)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"status":"duplicate"}`))
				return
			}
			// Never acknowledge a delivery which failed durable persistence.
			http.Error(w, "inbox unavailable or full; not accepted", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if o.AfterStore != nil {
			o.AfterStore()
		}
		audit("INFO", "Alert durably received; local routing determines review or approved automatic execution.")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"received","execution_status":"queued"}`))
	}), nil
}

// Managed reads routing settings atomically on each request so revocation applies
// immediately without importing an admin manager or ProjectX client.
func Managed(sink Sink, path string, wake ...func()) (http.Handler, error) {
	var after func()
	if len(wake) > 0 {
		after = wake[0]
	}
	return New(sink, Options{AfterStore: after, Enabled: true, MaxAge: 5 * time.Minute, Resolve: func(p string) (ingress.Route, bool) {
		settings, err := ingress.Load(path)
		if err != nil || !settings.Enabled {
			return ingress.Route{}, false
		}
		for _, r := range settings.Routes {
			if p == "/tradingview/"+r.ID && len(r.Token) >= 32 {
				return r, true
			}
		}
		return ingress.Route{}, false
	}})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
