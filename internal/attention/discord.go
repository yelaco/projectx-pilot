// Package attention sends optional outbound reports. It has no trading capability.
package attention

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yelaco/projectx-pilot/internal/config"
)

type Settings struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"webhook_url"`
}
type Status struct {
	Enabled, Configured bool
	LastError           string
	LastSent            time.Time
	Dropped             int
}
type report struct {
	message    string
	generation uint64
	at         time.Time
}
type Discord struct {
	healthURL  string
	daily      bool
	mu         sync.Mutex
	path       string
	settings   Settings
	status     Status
	generation uint64
	seen       map[[32]byte]time.Time
	queue      chan report
	client     *http.Client
	next       time.Time
	suspended  bool
}

var hookPath = regexp.MustCompile(`^/api/(v[0-9]+/)?webhooks/[0-9]+/[A-Za-z0-9_-]+$`)

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Host != "discord.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !hookPath.MatchString(u.Path) {
		return errors.New("use an HTTPS discord.com API webhook URL without query parameters")
	}
	return nil
}
func Open(path string) (*Discord, error) {
	d := &Discord{path: path, seen: map[[32]byte]time.Time{}, queue: make(chan report, 64)}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	d.client = &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	file, err := os.Open(path)
	var b []byte
	if err == nil {
		defer file.Close()
		b, err = io.ReadAll(io.LimitReader(file, 4097))
	}
	if err == nil {
		if len(b) > 4096 || json.Unmarshal(b, &d.settings) != nil {
			return nil, errors.New("invalid Discord notification settings")
		}
		if d.settings.URL != "" {
			if err = ValidateURL(d.settings.URL); err != nil {
				return nil, err
			}
		}
		if d.settings.Enabled && d.settings.URL == "" {
			return nil, errors.New("enabled Discord notifications require a webhook")
		}
	} else if !os.IsNotExist(err) {
		return nil, errors.New("could not read Discord notification settings")
	}
	return d, nil
}

// OpenDaily creates an independent sender/settings store for read-only trading reports.
func OpenDaily(path string) (*Discord, error) {
	d, err := Open(path)
	if err != nil {
		return nil, err
	}
	d.daily = true
	return d, nil
}

// SetLocalDashboard supplies a diagnostic link only; it accepts no public host.
func (d *Discord) SetLocalDashboard(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	n, parseErr := strconv.Atoi(port)
	if err != nil || parseErr != nil || host != "127.0.0.1" || n < 1 || n > 65535 {
		return errors.New("health link requires the local dashboard address")
	}
	d.mu.Lock()
	d.healthURL = "http://" + net.JoinHostPort(host, port) + "/system-health"
	d.mu.Unlock()
	return nil
}
func (d *Discord) Configure(enabled bool, raw string, clear bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	next := d.settings
	if clear {
		next.URL = ""
	}
	if strings.TrimSpace(raw) != "" {
		next.URL = strings.TrimSpace(raw)
	}
	if next.URL != "" {
		if err := ValidateURL(next.URL); err != nil {
			return err
		}
	}
	if enabled && next.URL == "" {
		return errors.New("configure a Discord webhook before enabling reports")
	}
	next.Enabled = enabled
	b, _ := json.Marshal(next)
	if err := config.WritePrivate(d.path, b); err != nil {
		return errors.New("could not save Discord notification settings")
	}
	d.settings = next
	d.generation++
	d.seen = map[[32]byte]time.Time{}
	d.suspended = false
	d.status.LastError = ""
	return nil
}
func (d *Discord) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.status
	s.Enabled = d.settings.Enabled
	s.Configured = d.settings.URL != ""
	return s
}

// Publish is bounded and never waits for network I/O. Reports while disabled are discarded.
func (d *Discord) Publish(message string) { d.Enqueue(message) }

// Enqueue reports whether this best-effort message entered the bounded queue.
func (d *Discord) Enqueue(message string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.settings.Enabled || d.suspended {
		return false
	}
	now := time.Now().UTC()
	key := sha256.Sum256([]byte(message))
	if at, ok := d.seen[key]; ok && now.Sub(at) < 5*time.Minute {
		return false
	}
	if len(d.seen) >= 256 {
		for k, at := range d.seen {
			if now.Sub(at) >= 5*time.Minute {
				delete(d.seen, k)
			}
		}
		if len(d.seen) >= 256 {
			d.status.Dropped++
			return false
		}
	}
	r := report{message: message, generation: d.generation, at: now}
	select {
	case d.queue <- r:
		d.seen[key] = now
		return true
	default:
		d.status.Dropped++
		d.status.LastError = "Notification queue full; inspect local reports"
		return false
	}
}
func (d *Discord) Run(ctx context.Context) {
	defer d.client.CloseIdleConnections()
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-d.queue:
			d.mu.Lock()
			delay := time.Until(d.next)
			d.mu.Unlock()
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			d.mu.Lock()
			valid := d.settings.Enabled && !d.suspended && r.generation == d.generation
			endpoint := d.settings.URL
			d.mu.Unlock()
			if valid {
				d.send(ctx, endpoint, r)
			}
		}
	}
}
func (d *Discord) send(ctx context.Context, endpoint string, r report) {
	// Never include signals, credentials, webhook URLs or raw gateway errors.
	text := "ProjectX Pilot — attention required\n" + r.at.Format(time.RFC3339) + "\n" + r.message + "\nAutomatic flattening is disabled. Review the local dashboard and exact account."
	if d.daily {
		text = "ProjectX Pilot — daily trading report\n" + r.at.Format(time.RFC3339) + "\n" + r.message + "\nRead-only report; no trading action requested."
	}
	if !d.daily {
		d.mu.Lock()
		link := d.healthURL
		d.mu.Unlock()
		if link == "" {
			text += "\nDiagnostics: System health in your local dashboard (/system-health)."
		} else {
			text += "\nSystem health (on your laptop): " + link
		}
	}
	chars := []rune(text)
	if len(chars) > 1900 {
		text = string(chars[:1900])
	}
	body, _ := json.Marshal(map[string]any{"content": text, "allowed_mentions": map[string]any{"parse": []string{}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?wait=true", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		d.mu.Lock()
		defer d.mu.Unlock()
		if r.generation == d.generation {
			d.status.LastError = "Discord delivery unconfirmed; inspect local report settings (no automatic retry)"
		}
		return
	}
	defer resp.Body.Close()
	// Parse bounded responses outside the lock: delivery must never block Publish.
	delay := 0.0
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		delay, _ = strconv.ParseFloat(resp.Header.Get("X-RateLimit-Reset-After"), 64)
	}
	if resp.StatusCode == 429 {
		delay, _ = strconv.ParseFloat(resp.Header.Get("Retry-After"), 64)
		if delay <= 0 {
			var limit struct {
				RetryAfter float64 `json:"retry_after"`
			}
			_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&limit)
			delay = limit.RetryAfter
		}
		if delay <= 0 {
			delay = 60
		}
	}
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.generation != d.generation {
		return
	}
	if delay > 0 {
		if delay > 86400 {
			delay = 86400
		}
		d.next = time.Now().Add(time.Duration(delay * float64(time.Second)))
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		d.status.LastSent = time.Now().UTC()
		d.status.LastError = ""
		return
	}
	d.status.LastError = fmt.Sprintf("Discord notification failed (HTTP %d); inspect local reports", resp.StatusCode)
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 {
		d.suspended = true
		d.status.LastError += ". Delivery suspended until settings are saved again"
	}
}
