// Package ingress stores only inbox routing capabilities, never trading credentials.
package ingress

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"projectxbot/internal/config"
)

type PriceMapping struct {
	Entry  string `json:"entry"`
	Stop   string `json:"stop"`
	Target string `json:"target"`
}

type Route struct {
	MappingMode string       `json:"mapping_mode,omitempty"`
	BuyMapping  PriceMapping `json:"buy_mapping,omitempty"`
	SellMapping PriceMapping `json:"sell_mapping,omitempty"`
	ID          string       `json:"id"`
	Token       string       `json:"token"`
	Strategy    string       `json:"strategy"`
	Symbols     []string     `json:"symbols"`
	Entry       string       `json:"entry"`
	Stop        string       `json:"stop"`
	Target      string       `json:"target"`
}
type Settings struct {
	Enabled     bool    `json:"enabled"`
	QuickTunnel bool    `json:"quick_tunnel"`
	PublicURL   string  `json:"public_url"`
	Routes      []Route `json:"routes"`
}
type Store struct {
	mu    sync.Mutex
	path  string
	state Settings
}

func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, config.WritePrivate(path, []byte("{}\n"))
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 131072 || json.Unmarshal(data, &s.state) != nil {
		return nil, errors.New("invalid webhook settings")
	}
	return s, nil
}
func Load(path string) (Settings, error) {
	b, err := os.ReadFile(path)
	var s Settings
	if err != nil {
		return s, err
	}
	if len(b) > 131072 || json.Unmarshal(b, &s) != nil {
		return s, errors.New("invalid webhook settings")
	}
	return s, nil
}
func clone(s Settings) Settings {
	b, _ := json.Marshal(s)
	var c Settings
	_ = json.Unmarshal(b, &c)
	return c
}
func (s *Store) Snapshot() Settings { s.mu.Lock(); defer s.mu.Unlock(); return clone(s.state) }
func (s *Store) Update(change func(*Settings) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.state)
	if err := change(&next); err != nil {
		return err
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err = config.WritePrivate(s.path, b); err != nil {
		return errors.New("could not save webhook settings")
	}
	s.state = next
	return nil
}
func Token() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

var expr = regexp.MustCompile(`^\{\{(?:open|high|low|close|plot_(?:[0-9]|1[0-9])|plot\("[^"\\\r\n]{1,80}"\))\}\}$`)

func Expression(s string) bool { return s == "" || expr.MatchString(s) }
func PublicURL(s string) bool {
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/") && (u.Port() == "" || u.Port() == "443")
}

// Mapping selects exactly one configured set. Legacy routes without a mode use
// their existing shared fields; separate mappings never silently fall back.
func (r Route) Mapping(direction string) PriceMapping {
	if r.MappingMode == "separate" {
		if direction == "BUY" {
			return r.BuyMapping
		}
		return r.SellMapping
	}
	return PriceMapping{Entry: r.Entry, Stop: r.Stop, Target: r.Target}
}

// WebhookURLs is the canonical set of public profile endpoints. The saved origin
// is configuration, not a claim that the tunnel is connected. Never infer it
// from an incoming Host header or include delivery tokens in URLs.
func (s Settings) WebhookURLs() map[string]string {
	urls := make(map[string]string, len(s.Routes))
	if s.PublicURL == "" || !PublicURL(s.PublicURL) {
		return urls
	}
	base := strings.TrimRight(s.PublicURL, "/")
	for _, r := range s.Routes {
		if r.ID != "" {
			urls[r.ID] = base + "/tradingview/" + url.PathEscape(r.ID)
		}
	}
	return urls
}
func (s Settings) PublicURLKind() string {
	if s.QuickTunnel {
		return "Temporary Quick Tunnel"
	}
	if s.PublicURL != "" {
		return "Permanent hostname"
	}
	return "Not configured"
}
