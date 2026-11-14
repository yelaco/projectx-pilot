package news

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"projectxbot/internal/session"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func body(events []Event) string                                      { b, _ := json.Marshal(events); return string(b) }
func event(title, country, date string) Event {
	at, err := time.Parse(time.RFC3339, date)
	if err != nil {
		panic(err)
	}
	return Event{Title: title, Country: country, Date: at, Impact: "High"}
}
func fakeCalendar(t *testing.T, events []Event) (*Calendar, *time.Time, *int) {
	t.Helper()
	now := time.Date(2026, 12, 7, 12, 0, 0, 0, time.UTC)
	calls := 0
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != URL || r.Header.Get("User-Agent") != "ProjectXBot/1.0" {
			t.Error("unexpected request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body(events))), Header: make(http.Header)}, nil
	})}
	c := New(filepath.Join(t.TempDir(), "news.json"), client, func() time.Time { return now })
	return c, &now, &calls
}
func TestFeedPolicyDaysAndRestart(t *testing.T) {
	events := []Event{
		event("FOMC Member Waller Speaks", "USD", "2026-12-07T12:00:00-05:00"),
		event("FOMC Meeting Minutes", "USD", "2026-12-08T14:00:00-05:00"),
		event("Federal Funds Rate", "USD", "2026-12-09T14:00:00-05:00"),
		event("FOMC Statement", "USD", "2026-12-09T14:00:00-05:00"),
		event("FOMC Press Conference", "USD", "2026-12-09T14:30:00-05:00"),
		event("FOMC Economic Projections", "USD", "2026-12-09T14:00:00-05:00"),
		event("Bank Holiday", "All", "2026-12-10T00:00:00-05:00"),
	}
	c, now, calls := fakeCalendar(t, events)
	if c.EntryAllowed(*now, "us_fomc") == nil {
		t.Fatal("missing feed allowed entries")
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		at      string
		blocked bool
	}{
		{"2026-12-08T16:59:59", false}, {"2026-12-08T17:00:00", true},
		{"2026-12-09T16:59:59", true}, {"2026-12-09T17:00:00", false},
		{"2026-12-07T09:00:00", false}, {"2026-12-08T09:00:00", false},
		{"2026-12-14T09:00:00", true},
	} {
		at, _ := time.ParseInLocation("2006-01-02T15:04:05", tc.at, session.Chicago)
		for _, zone := range []*time.Location{time.UTC, time.FixedZone("laptop", 7*3600)} {
			if got := c.EntryAllowed(at.In(zone), "") != nil; got != tc.blocked {
				t.Fatal(tc.at, got)
			}
		}
	}
	if err := c.Refresh(context.Background()); err == nil || *calls != 1 {
		t.Fatal("cooldown failed")
	}
	restored := New(c.path, c.client, func() time.Time { return *now })
	if err := restored.DayAllowed("2026-12-09", ""); err == nil {
		t.Fatal("restart lost event block")
	}
	if err := restored.DayAllowed("2026-12-08", ""); err != nil {
		t.Fatal(err)
	}
	if err := restored.Refresh(context.Background()); err == nil || *calls != 1 {
		t.Fatal("restart bypassed success cooldown")
	}
	info, err := os.Stat(c.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("cache permissions", err)
	}
	*now = now.Add(73 * time.Hour)
	if restored.DayAllowed("2026-12-08", "") == nil {
		t.Fatal("stale cache allowed entries")
	}
	if restored.EntryAllowed(*now, "off") != nil {
		t.Fatal("off did not disable gate")
	}
}
func TestInvalidFeedFailsClosed(t *testing.T) {
	valid := event("CPI m/m", "USD", "2026-12-09T08:30:00-05:00")
	for _, tc := range []struct {
		name, data string
		status     int
	}{
		{"forbidden", "", 403}, {"empty", "[]", 200}, {"malformed", "<html>", 200},
		{"trailing", body([]Event{valid}) + "{}", 200},
		{"oversized", strings.Repeat("x", maxBytes+1), 200},
		{"unknown-fomc", body([]Event{event("FOMC Unexpected Policy Announcement", "USD", "2026-12-09T14:00:00-05:00")}), 200},
		{"old-week", body([]Event{event("CPI", "USD", "2026-11-09T08:30:00-05:00")}), 200},
		{"mixed-weeks", body([]Event{valid, event("CPI", "USD", "2026-12-14T08:30:00-05:00")}), 200},
		{"bad-currency", body([]Event{event("FOMC Statement", "usd", "2026-12-09T14:00:00-05:00")}), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := fakeCalendar(t, nil)
			c.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.data))}, nil
			})}
			if err := c.Refresh(context.Background()); err == nil {
				t.Fatal("invalid feed accepted")
			}
			if c.Status().LastError == "" || c.DayAllowed("2026-12-08", "") == nil {
				t.Fatal("failure silently enabled trading")
			}
		})
	}
}
func TestRefreshFailurePreservesCacheAndWeekRollover(t *testing.T) {
	c, now, _ := fakeCalendar(t, []Event{event("CPI", "USD", "2026-12-09T08:30:00-05:00")})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(6 * time.Hour)
	c.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("private server response"))}, nil
	})}
	if c.Refresh(context.Background()) == nil || c.DayAllowed("2026-12-08", "") != nil {
		t.Fatal("did not retain usable cache")
	}
	if strings.Contains(c.Status().LastError, "private") {
		t.Fatal("response body leaked")
	}
	*now = now.Add(6 * time.Hour)
	c.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body([]Event{event("FOMC Statement", "USD", "2026-12-16T14:00:00-05:00")})))}, nil
	})}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.Status().Weeks) != 2 || c.DayAllowed("2026-12-08", "") != nil || c.DayAllowed("2026-12-16", "") == nil {
		t.Fatal("week rollover lost or confused coverage")
	}
}
func TestRunCancellationAndConcurrentRefresh(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-release:
			return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
	})}
	c := New("", client, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	<-entered
	if c.Refresh(context.Background()) == nil || calls.Load() != 1 {
		t.Fatal("concurrent refresh was not bounded")
	}
	cancel()
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}
func TestForexFactoryIntegration(t *testing.T) {
	if os.Getenv("PROJECTX_NEWS_INTEGRATION") != "1" {
		t.Skip("explicitly enable read-only external-feed integration")
	}
	c := New(filepath.Join(t.TempDir(), "calendar.json"), nil, time.Now)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	t.Log("Validated weekly feed; events remain private; covered source weeks:", c.Status().Weeks)
}

func TestEveningEventAndDST(t *testing.T) {
	c, now, _ := fakeCalendar(t, []Event{event("FOMC Statement", "USD", "2026-12-06T20:00:00-05:00")})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.DayAllowed("2026-12-07", "") == nil {
		t.Fatal("Sunday evening policy event did not block Monday session")
	}
	// Explicit source offsets and exchange time zones are independent of the laptop.
	c2 := New("", nil, func() time.Time { return *now })
	c2.snapshots["2026-03-15"] = Snapshot{Week: "2026-03-15", FetchedAt: *now, Events: []Event{event("FOMC Statement", "USD", "2026-03-18T14:00:00-04:00")}}
	at, _ := time.ParseInLocation("2006-01-02 15:04", "2026-03-17 17:00", session.Chicago)
	if c2.EntryAllowed(at, "") == nil {
		t.Fatal("DST policy session was not blocked")
	}
}
