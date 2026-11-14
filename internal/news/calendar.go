// Package news maintains a validated, cached Forex Factory weekly calendar.
package news

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"projectxbot/internal/config"
	"projectxbot/internal/session"
)

const URL = "https://nfs.faireconomy.media/ff_calendar_thisweek.json"
const MaxAge = 72 * time.Hour
const RefreshInterval = 6 * time.Hour
const maxBytes = 1 << 20

var newYork = func() *time.Location {
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return l
}()

type Event struct {
	Title    string    `json:"title"`
	Country  string    `json:"country"`
	Date     time.Time `json:"date"`
	Impact   string    `json:"impact"`
	Forecast string    `json:"forecast"`
	Previous string    `json:"previous"`
}
type Snapshot struct {
	Week      string    `json:"week"`
	FetchedAt time.Time `json:"fetched_at"`
	Events    []Event   `json:"events"`
}
type cache struct {
	Version   int        `json:"version"`
	Snapshots []Snapshot `json:"snapshots"`
}
type Status struct {
	Source, LastError         string
	LastSuccess, LastAttempt  time.Time
	Weeks                     []string
	RefreshHours, MaxAgeHours int
}
type Calendar struct {
	mu          sync.RWMutex
	fetchMu     sync.Mutex
	path        string
	client      *http.Client
	now         func() time.Time
	snapshots   map[string]Snapshot
	lastError   string
	lastAttempt time.Time
}

func New(path string, client *http.Client, now func() time.Time) *Calendar {
	if now == nil {
		now = time.Now
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("feed redirect refused") }}
	}
	c := &Calendar{path: path, client: client, now: now, snapshots: map[string]Snapshot{}}
	if path == "" {
		return c
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return c
	}
	if err != nil {
		c.lastError = "Saved news cache unavailable; waiting for refresh"
		return c
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		c.lastError = "Saved news cache unavailable; waiting for refresh"
		return c
	}
	var saved cache
	if json.Unmarshal(data, &saved) != nil || saved.Version != 1 || len(saved.Snapshots) > 3 {
		c.lastError = "Saved news cache invalid; waiting for refresh"
		return c
	}
	for _, s := range saved.Snapshots {
		week, err := validate(s.Events)
		if err != nil || week != s.Week || s.FetchedAt.IsZero() || s.FetchedAt.After(now().Add(5*time.Minute)) {
			c.snapshots = map[string]Snapshot{}
			c.lastError = "Saved news cache invalid; waiting for refresh"
			return c
		}
		c.snapshots[s.Week] = s
		if s.FetchedAt.After(c.lastAttempt) {
			c.lastAttempt = s.FetchedAt
		}
	}
	return c
}
func sourceWeek(t time.Time) string {
	t = t.In(newYork)
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, newYork)
	return d.AddDate(0, 0, -int(d.Weekday())).Format("2006-01-02")
}
func eventKind(title string) (bool, error) {
	t := strings.ToLower(strings.Join(strings.Fields(title), " "))
	switch t {
	case "fomc statement", "fomc press conference", "fomc economic projections", "federal funds rate", "fomc rate decision", "fomc interest rate decision":
		return true, nil
	case "fomc meeting minutes", "fomc minutes", "fomc financial stability report":
		return false, nil
	}
	if strings.HasPrefix(t, "fomc member ") && strings.HasSuffix(t, " speaks") {
		return false, nil
	}
	if strings.Contains(t, "fomc") {
		return false, errors.New("unrecognized USD FOMC event; calendar review needed")
	}
	return false, nil
}
func validate(events []Event) (string, error) {
	if len(events) == 0 || len(events) > 2000 {
		return "", errors.New("feed event count invalid")
	}
	week := ""
	for _, e := range events {
		if e.Title == "" || len(e.Title) > 256 || len(e.Country) != 3 || e.Date.IsZero() || len(e.Impact) > 32 || len(e.Forecast) > 256 || len(e.Previous) > 256 {
			return "", errors.New("feed event schema invalid")
		}
		if e.Country != "All" {
			for _, r := range e.Country {
				if r < 'A' || r > 'Z' {
					return "", errors.New("feed currency schema invalid")
				}
			}
		}
		w := sourceWeek(e.Date)
		if week == "" {
			week = w
		}
		if w != week {
			return "", errors.New("feed spans multiple source weeks")
		}
		if e.Country == "USD" {
			if _, err := eventKind(e.Title); err != nil {
				return "", err
			}
		}
	}
	return week, nil
}
func (c *Calendar) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := Status{Source: URL, LastError: c.lastError, LastAttempt: c.lastAttempt, RefreshHours: 6, MaxAgeHours: 72}
	for week, v := range c.snapshots {
		s.Weeks = append(s.Weeks, week)
		if v.FetchedAt.After(s.LastSuccess) {
			s.LastSuccess = v.FetchedAt
		}
	}
	// At most three keys, sorted without exposing event payloads.
	for a := 0; a < len(s.Weeks); a++ {
		for b := a + 1; b < len(s.Weeks); b++ {
			if s.Weeks[b] < s.Weeks[a] {
				s.Weeks[a], s.Weeks[b] = s.Weeks[b], s.Weeks[a]
			}
		}
	}
	return s
}
func (c *Calendar) EntryAllowed(at time.Time, mode string) error {
	if mode == "off" {
		return nil
	}
	if mode != "" && mode != "us_fomc" {
		return errors.New("unknown news filter; entries blocked")
	}
	date := session.CurrentTradingDate(at)
	return c.DayAllowed(date, mode)
}
func (c *Calendar) DayAllowed(date, mode string) error {
	if mode == "off" {
		return nil
	}
	if mode != "" && mode != "us_fomc" {
		return errors.New("unknown news filter; entries blocked")
	}
	d, err := time.ParseInLocation("2006-01-02", date, newYork)
	if err != nil {
		return errors.New("invalid news date; entries blocked")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap, ok := c.snapshots[sourceWeek(d.Add(12*time.Hour))]
	if !ok {
		return errors.New("Forex Factory feed does not cover this trading date; entries blocked")
	}
	age := c.now().Sub(snap.FetchedAt)
	if age > MaxAge || age < -5*time.Minute {
		return errors.New("Forex Factory news cache stale; entries blocked")
	}
	for _, e := range snap.Events {
		if e.Country != "USD" || session.CurrentTradingDate(e.Date) != date {
			continue
		}
		match, err := eventKind(e.Title)
		if err != nil {
			return err
		}
		if match {
			return errors.New("US FOMC policy event in Forex Factory feed; trading session blocked")
		}
	}
	return nil
}
func (c *Calendar) Refresh(ctx context.Context) error {
	// Never queue parallel HTTP requests from refresh buttons/workers.
	if !c.fetchMu.TryLock() {
		return errors.New("news refresh already running")
	}
	defer c.fetchMu.Unlock()
	now := c.now().UTC()
	c.mu.Lock()
	if !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < 5*time.Minute {
		c.mu.Unlock()
		return errors.New("news refresh cooldown: wait five minutes between requests")
	}
	c.lastAttempt = now
	c.mu.Unlock()
	err := c.fetch(ctx, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.lastError = err.Error()
	} else {
		c.lastError = ""
	}
	return err
}
func (c *Calendar) fetch(ctx context.Context, now time.Time) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, URL, nil)
	if err != nil {
		return errors.New("news request unavailable")
	}
	req.Header.Set("User-Agent", "ProjectXBot/1.0")
	req.Header.Set("Accept", "application/json")
	response, err := c.client.Do(req)
	if err != nil {
		return errors.New("Forex Factory request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("Forex Factory returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return errors.New("Forex Factory response unavailable or oversized")
	}
	var events []Event
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&events); err != nil {
		return errors.New("Forex Factory JSON invalid")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("Forex Factory JSON has trailing content")
	}
	week, err := validate(events)
	if err != nil {
		return err
	}
	sourceStart, _ := time.ParseInLocation("2006-01-02", week, newYork)
	currentStart, _ := time.ParseInLocation("2006-01-02", sourceWeek(now), newYork)
	if sourceStart.Before(currentStart) || sourceStart.After(currentStart.AddDate(0, 0, 7)) {
		return errors.New("Forex Factory response covers an unexpected or outdated week")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	next := map[string]Snapshot{}
	for k, v := range c.snapshots {
		if !v.FetchedAt.Before(now.Add(-14 * 24 * time.Hour)) {
			next[k] = v
		}
	}
	next[week] = Snapshot{Week: week, FetchedAt: now, Events: events}
	if len(next) > 3 {
		oldest := ""
		for k := range next {
			if oldest == "" || k < oldest {
				oldest = k
			}
		}
		delete(next, oldest)
	}
	saved := cache{Version: 1}
	for _, v := range next {
		saved.Snapshots = append(saved.Snapshots, v)
	}
	if c.path != "" {
		b, err := json.Marshal(saved)
		if err != nil || len(b) > maxBytes {
			return errors.New("news cache exceeds storage limit")
		}
		if err := config.WritePrivate(c.path, b); err != nil {
			return errors.New("news cache could not be saved")
		}
	}
	c.snapshots = next
	return nil
}
func (c *Calendar) Run(ctx context.Context, audit ...func(string, string)) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		call, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := c.Refresh(call)
		cancel()
		if len(audit) > 0 {
			if err != nil {
				audit[0]("WARN", "News feed refresh failed: "+err.Error())
			} else {
				audit[0]("INFO", "Forex Factory weekly news feed refreshed successfully.")
			}
		}
		wait := RefreshInterval
		if err != nil {
			wait = 30 * time.Minute
		}
		timer.Reset(wait)
	}
}
