package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/yelaco/projectx-pilot/internal/session"
	"gopkg.in/yaml.v3"
)

type Config struct {
	TradeManagerNoTradeDates map[string][]string `yaml:"trade_manager_no_trade_dates,omitempty"`
	NoTradeDates             []string            `yaml:"no_trade_dates,omitempty"`
	NewsFilter               string              `yaml:"news_filter"`
	SignalMaxAge             time.Duration       `yaml:"signal_max_age"`
	Mode                     string              `yaml:"mode"`
	AccountID                int64               `yaml:"account_id"`
	// Deprecated: accepted only when loading older configurations; never enforced.
	AllowedInstruments []string      `yaml:"allowed_instruments,omitempty" json:"-"`
	RequestTimeout     time.Duration `yaml:"request_timeout"`
	RefreshInterval    time.Duration `yaml:"refresh_interval"`
	DashboardListen    string        `yaml:"dashboard_listen"`
	LogLevel           string        `yaml:"log_level"`
}

type Secrets struct{ Username, APIKey, DashboardToken string }

func Load(path string) (Config, error) {
	c := Config{NewsFilter: "us_fomc", SignalMaxAge: 5 * time.Minute, Mode: "read-only", RequestTimeout: 10 * time.Second, RefreshInterval: time.Minute, DashboardListen: "127.0.0.1:8080", LogLevel: "info"}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	if info.Size() > 65536 {
		return c, errors.New("configuration exceeds 64 KiB")
	}
	d := yaml.NewDecoder(io.LimitReader(f, 65537))
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return c, errors.New("invalid configuration YAML")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, errors.New("configuration must contain one YAML document")
	}
	// Profiles now define instrument scope. Retire the old global setting.
	c.AllowedInstruments = nil
	return c, c.Validate()
}

func (c Config) Validate() error {
	if len(c.TradeManagerNoTradeDates) > 50 {
		return errors.New("maximum 50 manager operation plans")
	}
	total := len(c.NoTradeDates)
	for id, dates := range c.TradeManagerNoTradeDates {
		if len(id) < 1 || len(id) > 64 {
			return errors.New("invalid Trade Manager operation ID")
		}
		for _, r := range id {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return errors.New("invalid Trade Manager operation ID")
			}
		}
		if err := session.ValidateNoTradeDates(dates); err != nil {
			return err
		}
		total += len(dates)
	}
	if total > 3000 {
		return errors.New("maximum 3000 operation dates across all plans")
	}

	if err := session.ValidateNoTradeDates(c.NoTradeDates); err != nil {
		return err
	}
	if c.NewsFilter != "" && c.NewsFilter != "us_fomc" && c.NewsFilter != "off" {
		return errors.New("news_filter must be us_fomc or off")
	}
	if c.Mode != "read-only" && c.Mode != "manual-approval" && c.Mode != "automated" {
		return errors.New("mode must be read-only, manual-approval or automated")
	}
	if c.SignalMaxAge < time.Second || c.SignalMaxAge > 15*time.Minute {
		return errors.New("signal_max_age must be 1s..15m")
	}
	if c.AccountID < 0 {
		return errors.New("account_id must be nonnegative")
	}
	if c.RequestTimeout < time.Second || c.RequestTimeout > time.Minute {
		return errors.New("request_timeout must be 1s..60s")
	}
	if c.RefreshInterval < 30*time.Second || c.RefreshInterval > time.Hour {
		return errors.New("refresh_interval must be 30s..1h")
	}
	host, port, err := net.SplitHostPort(c.DashboardListen)
	if err != nil || host != "127.0.0.1" {
		return errors.New("dashboard_listen must use 127.0.0.1")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return errors.New("invalid dashboard port")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("invalid log_level")
	}
	return nil
}

func ReadSecrets() (Secrets, error) {
	s := Secrets{os.Getenv("PROJECTX_USERNAME"), os.Getenv("PROJECTX_API_KEY"), os.Getenv("BOT_DASHBOARD_TOKEN")}
	if s.Username == "" || s.APIKey == "" {
		return Secrets{}, fmt.Errorf("PROJECTX_USERNAME and PROJECTX_API_KEY are required")
	}
	if len(s.DashboardToken) < 32 {
		return Secrets{}, errors.New("BOT_DASHBOARD_TOKEN must contain at least 32 characters")
	}
	return s, nil
}
