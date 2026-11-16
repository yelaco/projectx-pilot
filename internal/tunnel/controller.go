// Package tunnel supervises only a fixed Cloudflare Quick Tunnel command.
package tunnel

import (
	"bufio"
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yelaco/projectx-pilot/internal/ingress"
	"github.com/yelaco/projectx-pilot/internal/storage"
)

type Status struct{ State, Error string }
type Controller struct {
	store *ingress.Store
	audit *storage.Inbox
	mu    sync.Mutex
	state Status
	wake  chan struct{}
}

func New(s *ingress.Store, audit *storage.Inbox) *Controller {
	return &Controller{store: s, audit: audit, state: Status{State: "stopped"}, wake: make(chan struct{}, 1)}
}
func (c *Controller) Status() Status { c.mu.Lock(); defer c.mu.Unlock(); return c.state }
func (c *Controller) Wake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
func (c *Controller) set(state, message string) {
	c.mu.Lock()
	changed := c.state.State != state || c.state.Error != message
	c.state = Status{state, message}
	c.mu.Unlock()
	if changed {
		_ = c.audit.RecordActivity("INFO", "tunnel", "Tunnel state: "+state+". "+message)
	}
}

var public = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func (c *Controller) clear() {
	_ = c.store.Update(func(s *ingress.Settings) error {
		if s.QuickTunnel {
			s.PublicURL = ""
		}
		return nil
	})
}
func (c *Controller) Run(ctx context.Context) {
	var cancel context.CancelFunc
	var ended <-chan error
	var retry time.Time
	stop := func() {
		if cancel != nil {
			cancel()
			<-ended
			cancel = nil
			ended = nil
		}
		c.clear()
	}
	defer stop()
	c.clear()
	for {
		settings := c.store.Snapshot()
		wanted := settings.Enabled && settings.QuickTunnel
		if !wanted {
			if cancel != nil {
				stop()
			}
			c.set("stopped", "")
		}
		if wanted && cancel == nil && !time.Now().Before(retry) {
			path, err := exec.LookPath("cloudflared")
			if err != nil {
				c.set("unavailable", "cloudflared is not installed; rerun the setup command.")
				retry = time.Now().Add(30 * time.Second)
			} else {
				child, childCancel := context.WithCancel(ctx)
				cmd := exec.CommandContext(child, path, "tunnel", "--url", "http://127.0.0.1:8090")
				pipe, err := cmd.StdoutPipe()
				if err == nil {
					cmd.Stderr = cmd.Stdout
					err = cmd.Start()
				}
				if err != nil {
					childCancel()
					c.set("failed", "Could not start the local tunnel process.")
					retry = time.Now().Add(15 * time.Second)
				} else {
					cancel = childCancel
					done := make(chan error, 1)
					ended = done
					c.set("starting", "")
					go func() {
						scanner := bufio.NewScanner(pipe)
						scanner.Buffer(make([]byte, 4096), 65536)
						lastDiagnostic := ""
						var lastDiagnosticAt time.Time
						for scanner.Scan() {
							line := scanner.Text()
							if message := diagnostic(line); message != "" && (message != lastDiagnostic || time.Since(lastDiagnosticAt) >= 5*time.Second) {
								lastDiagnostic = message
								lastDiagnosticAt = time.Now()
								_ = c.audit.RecordActivity("WARN", "tunnel", message)
							}
							url := public.FindString(line)
							if url != "" {
								_ = c.store.Update(func(s *ingress.Settings) error {
									if s.Enabled && s.QuickTunnel {
										s.PublicURL = url
									}
									return nil
								})
								c.set("connecting", "")
							}
							if strings.Contains(line, "Registered tunnel connection") {
								c.set("running", "")
							}
						}
						if scanner.Err() != nil {
							childCancel()
						}
						done <- cmd.Wait()
					}()
				}
			}
		}
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-c.wake:
			timer.Stop()
			retry = time.Time{}
		case <-ended:
			timer.Stop()
			cancel()
			cancel = nil
			ended = nil
			c.clear()
			c.set("disconnected", "Tunnel exited; retrying automatically.")
			retry = time.Now().Add(15 * time.Second)
		case <-timer.C:
		}
	}
}

// diagnostic maps known process messages to fixed text. Raw output can include
// request information, so it is never copied to the dashboard event stream.
func diagnostic(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "no such host"):
		return "Tunnel DNS lookup failed. Check this device's internet and DNS connection."
	case strings.Contains(lower, "connection refused"):
		return "Tunnel could not reach the local receiver on port 8090."
	case strings.Contains(lower, "no recent network activity"):
		return "Tunnel connection lost; reconnecting to Cloudflare."
	case strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout"):
		return "Tunnel connection timed out; checking connectivity."
	case strings.Contains(lower, "failed to request quick tunnel"):
		return "Cloudflare could not issue a public test address."
	case strings.Contains(lower, "failed to serve tunnel connection") || strings.Contains(lower, "failed to dial"):
		return "Tunnel connection interrupted; reconnecting to Cloudflare."
	case strings.Contains(line, " ERR ") || strings.HasPrefix(line, "ERR "):
		return "Cloudflare tunnel reported an error; inspect connection status and subsequent retry events."
	case strings.Contains(line, " WRN ") || strings.HasPrefix(line, "WRN "):
		return "Cloudflare tunnel reported a warning; inspect connection status."
	}
	return ""
}
