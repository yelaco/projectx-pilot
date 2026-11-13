package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"projectxbot/internal/ingress"
	"projectxbot/internal/signal"
	"projectxbot/internal/storage"
)

func alert(now time.Time) signal.Signal {
	return signal.Signal{ID: "swift-001", Strategy: "swift-mnq-scalping", Symbol: "MNQ", Direction: "BUY", Entry: 20000, StopLoss: 19995, TakeProfit: 20010, Timestamp: now}
}
func envelope(token string, s signal.Signal) string {
	b, _ := json.Marshal(struct {
		Token  string        `json:"delivery_token"`
		Signal signal.Signal `json:"signal"`
	}{token, s})
	return string(b)
}
func send(h http.Handler, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://tunnel.example/tradingview", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestReceivePersistAndDeduplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-alerts.db")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	token := strings.Repeat("x", 64)
	options := Options{Enabled: true, Token: token, MaxAge: 5 * time.Minute, Now: func() time.Time { return now }}
	h, err := New(store, options)
	if err != nil {
		t.Fatal(err)
	}
	body := envelope(token, alert(now))
	if w := send(h, body); w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if w := send(h, body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	changed := alert(now)
	changed.Entry = 20001
	if w := send(h, envelope(token, changed)); w.Code != 409 {
		t.Fatal("ID collision not rejected", w.Code)
	}
	rows, err := store.Recent()
	if err != nil || len(rows) != 1 || rows[0].AccountID != "" || rows[0].ContractID != "" || rows[0].ProfileID != "" {
		t.Fatal("delivery must stay unassigned", rows, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h, err = New(store, options)
	if err != nil {
		t.Fatal(err)
	}
	if w := send(h, body); w.Code != 200 {
		t.Fatal("restart lost deduplication", w.Code)
	}
}
func TestReceiverRejectsUnsafeRequests(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	token := strings.Repeat("x", 64)
	h, _ := New(store, Options{Enabled: true, Token: token, MaxAge: time.Minute, Now: func() time.Time { return now }})
	expired := alert(now.Add(-2 * time.Minute))
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"wrong token", envelope("wrong", alert(now)), 401},
		{"missing token", `{"signal":{}}`, 401},
		{"expired", envelope(token, expired), 422},
		{"account override", `{"account_id":123,"delivery_token":"` + token + `","signal":{}}`, 400},
		{"duplicate envelope field", `{"delivery_token":"a","delivery_token":"b","signal":{}}`, 400},
		{"oversized", strings.Repeat("x", 9000), 400},
		{"malformed", "{", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := send(h, tc.body)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
			if strings.Contains(w.Body.String(), token) {
				t.Fatal("token leaked")
			}
		})
	}
	disabled, _ := New(nil, Options{})
	if w := send(disabled, envelope(token, alert(now))); w.Code != 404 {
		t.Fatal("enabled by default")
	}
	if _, err = New(store, Options{Enabled: true, Token: "weak", MaxAge: time.Minute}); err == nil {
		t.Fatal("weak token accepted")
	}
	for _, path := range []string{"/", "/settings", "/api/status"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://tunnel.example"+path, nil))
		if w.Code != 404 {
			t.Fatal("admin route exposed", path)
		}
	}
	events, eventErr := store.Activity()
	if eventErr != nil || len(events) == 0 {
		t.Fatal("missing persisted receiver diagnostics", eventErr)
	}
	eventJSON, _ := json.Marshal(events)
	if strings.Contains(string(eventJSON), token) {
		t.Fatal("delivery token leaked into activity")
	}
	rows, err := store.Recent()
	if err != nil || len(rows) != 0 {
		t.Fatal("bad requests persisted", rows, err)
	}
}
func TestRateLimitAndConcurrentDelivery(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	token := strings.Repeat("t", 32)
	h, _ := New(store, Options{Enabled: true, Token: token, MaxAge: time.Minute, Now: func() time.Time { return now }})
	body := envelope(token, alert(now))
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := send(h, body)
			if w.Code != 202 && w.Code != 200 && w.Code != 503 {
				t.Error(w.Code)
			}
		}()
	}
	wg.Wait()
	rows, err := store.Recent()
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	for i := 0; i < 60; i++ {
		send(h, body)
	}
	if w := send(h, body); w.Code != 429 {
		t.Fatal("rate limit missing", w.Code)
	}
}

type failingSink struct{}

func (failingSink) Insert(storage.Record) error        { return storage.ErrTransition }
func (failingSink) Get(string) (storage.Record, error) { return storage.Record{}, storage.ErrNotFound }
func TestPersistenceFailureIsNotAcknowledged(t *testing.T) {
	token := strings.Repeat("t", 32)
	now := time.Now().UTC()
	h, _ := New(failingSink{}, Options{Enabled: true, Token: token, MaxAge: time.Minute})
	if w := send(h, envelope(token, alert(now))); w.Code != 503 {
		t.Fatal("acknowledged failed persistence", w.Code)
	}
}

func TestActionableValidationAndContinuousTickerIDs(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	token := strings.Repeat("x", 64)
	h, _ := New(store, Options{Enabled: true, Token: token, MaxAge: time.Minute, Now: func() time.Time { return now }})
	s := alert(now)
	s.ID = "swift-mnq-buy-MNQ1!-2026-11-13T09:00:00Z"
	if w := send(h, envelope(token, s)); w.Code != 202 {
		t.Fatal("continuous ticker ID rejected", w.Code, w.Body.String())
	}
	s.ID = "bad-levels"
	s.Entry = 22000
	w := send(h, envelope(token, s))
	if w.Code != 422 || !strings.Contains(w.Body.String(), "take_profit above entry") {
		t.Fatal("missing price direction diagnostic", w.Code, w.Body.String())
	}
	s = alert(now.Add(-2 * time.Minute))
	s.ID = "expired-test"
	w = send(h, envelope(token, s))
	if w.Code != 422 || !strings.Contains(w.Body.String(), "expired") {
		t.Fatal("missing expiry diagnostic", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), token) {
		t.Fatal("token leaked")
	}
}

func TestManagedProfileRotationAndDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ingress.json")
	settings, err := ingress.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(dir, "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	token := strings.Repeat("t", 64)
	_ = settings.Update(func(s *ingress.Settings) error {
		s.Routes = []ingress.Route{{ID: "swift", Token: token, Strategy: "swift-mnq-scalping", Symbols: []string{"MNQ"}}}
		return nil
	})
	h, err := Managed(store, path)
	if err != nil {
		t.Fatal(err)
	}
	request := func(token string) int {
		body := envelope(token, alert(time.Now().UTC()))
		r := httptest.NewRequest("POST", "http://public.test/tradingview/swift", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := request(token); got != 404 {
		t.Fatal("receiving enabled by default", got)
	}
	_ = settings.Update(func(s *ingress.Settings) error { s.Enabled = true; return nil })
	if got := request(token); got != 202 {
		t.Fatal(got)
	}
	rows, _ := store.Recent()
	if len(rows) != 1 || rows[0].ProfileID != "swift" || rows[0].AccountID != "" {
		t.Fatal("profile route assigned an account", rows)
	}
	_ = settings.Update(func(s *ingress.Settings) error { s.Routes[0].Token = strings.Repeat("n", 64); return nil })
	if got := request(token); got != 401 {
		t.Fatal("old token still works", got)
	}
	_ = settings.Update(func(s *ingress.Settings) error { s.Routes = nil; return nil })
	if got := request(token); got != 404 {
		t.Fatal("deleted profile still works", got)
	}
}
