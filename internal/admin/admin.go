// Package admin manages local configuration and explicitly approved execution.
package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"projectxbot/internal/tunnel"
	"strings"
	"sync"
	"time"

	"projectxbot/internal/access"
	"projectxbot/internal/app"
	"projectxbot/internal/config"
	"projectxbot/internal/execution"
	"projectxbot/internal/ingress"
	"projectxbot/internal/projectx"
	"projectxbot/internal/storage"
)

type credentials struct {
	Username string `json:"username"`
	APIKey   string `json:"api_key"`
}
type Settings struct {
	Config                 config.Config
	Username               string
	HasCredentials         bool
	EnvironmentCredentials bool
}
type Update struct {
	NewsFilter       string
	AccountID        int64
	Instruments      []string
	RefreshSeconds   int
	Username, APIKey string
	ClearCredentials bool
}
type Manager struct {
	continuousRetryAt           time.Time
	now                         func() time.Time
	automationWake              chan struct{}
	executionLease              *os.File
	executor                    *execution.Engine
	executionStore              *storage.Inbox
	executionConfig             execution.Config
	access                      *access.Store
	activity                    *storage.Inbox
	startedAt                   time.Time
	tunnelStatus                func() tunnel.Status
	ingress                     *ingress.Store
	wakeTunnel                  func()
	remote                      *storage.Inbox
	inbox                       *storage.Inbox
	policies                    Policies
	mu                          sync.Mutex
	c                           config.Config
	credentials                 credentials
	configPath, credentialsPath string
	environment                 bool
	service                     *app.Service
}

// Open supports first-run setup without ProjectX credentials. Environment credentials take precedence.
func Open(c config.Config, configPath, stateDir string) (*Manager, string, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, "", err
	}
	if err := os.Chmod(stateDir, 0700); err != nil {
		return nil, "", err
	}
	m := &Manager{now: time.Now, automationWake: make(chan struct{}, 1), startedAt: time.Now(), c: c, configPath: configPath, credentialsPath: filepath.Join(stateDir, "credentials.json")}
	data, err := os.ReadFile(m.credentialsPath)
	if err == nil {
		if err = json.Unmarshal(data, &m.credentials); err != nil {
			return nil, "", errors.New("invalid saved credentials")
		}
	} else if !os.IsNotExist(err) {
		return nil, "", err
	}
	user, key := os.Getenv("PROJECTX_USERNAME"), os.Getenv("PROJECTX_API_KEY")
	if user != "" || key != "" {
		if user == "" || key == "" {
			return nil, "", errors.New("both ProjectX environment credentials must be set")
		}
		m.credentials = credentials{user, key}
		m.environment = true
	}
	if (m.credentials.Username == "") != (m.credentials.APIKey == "") {
		return nil, "", errors.New("incomplete saved credentials")
	}
	routing, err := ingress.Open(filepath.Join(stateDir, "ingress.json"))
	if err != nil {
		return nil, "", err
	}
	m.ingress = routing
	if err := m.loadPolicies(); err != nil {
		return nil, "", err
	}
	if err := m.syncIngressLocked(); err != nil {
		return nil, "", err
	}
	usersPath := filepath.Join(stateDir, "admins.json")
	token := ""
	if _, err := os.Stat(usersPath); os.IsNotExist(err) {
		token = os.Getenv("BOT_DASHBOARD_TOKEN")
		if token == "" {
			path := filepath.Join(stateDir, "admin-password")
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				b := make([]byte, 32)
				if _, err = rand.Read(b); err != nil {
					return nil, "", err
				}
				data = []byte(hex.EncodeToString(b) + "\n")
				if err = config.WritePrivate(path, data); err != nil {
					return nil, "", err
				}
			} else if err != nil {
				return nil, "", err
			}
			token = strings.TrimSpace(string(data))
		}
		if len(token) < 32 {
			return nil, "", errors.New("admin password must contain at least 32 characters")
		}

	} else if err != nil {
		return nil, "", err
	} else {
		// Once migrated, stale environment/bootstrap credentials never override users.
		if b, err := os.ReadFile(filepath.Join(stateDir, "admin-password")); err == nil {
			token = strings.TrimSpace(string(b))
		}
	}
	users, err := access.Open(usersPath, token)
	if err != nil {
		return nil, "", err
	}
	m.access = users
	if err := m.openExecution(); err != nil {
		return nil, "", err
	}
	return m, token, nil
}
func (m *Manager) Reader() (app.Reader, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reader()
}
func (m *Manager) reader() (app.Reader, error) {
	if m.credentials.APIKey == "" {
		return nil, nil
	}
	return projectx.New(m.credentials.Username, m.credentials.APIKey, m.c.RequestTimeout)
}
func (m *Manager) Attach(s *app.Service) { m.mu.Lock(); defer m.mu.Unlock(); m.service = s }
func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.c
	c.AllowedInstruments = append([]string(nil), c.AllowedInstruments...)
	return Settings{Config: c, Username: m.credentials.Username, HasCredentials: m.credentials.APIKey != "", EnvironmentCredentials: m.environment}
}
func (m *Manager) Save(u Update) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.executor != nil {
		m.executor.Pause()
		if !m.executor.CanConfigure() {
			return errors.New("cannot change connection settings while execution needs monitoring")
		}
	}
	if u.RefreshSeconds < 30 || u.RefreshSeconds > 3600 {
		return errors.New("refresh must be 30..3600 seconds")
	}
	next := m.c
	next.AccountID = u.AccountID
	if u.NewsFilter != "" {
		next.NewsFilter = u.NewsFilter
	}
	next.AllowedInstruments = u.Instruments
	next.RefreshInterval = time.Duration(u.RefreshSeconds) * time.Second
	if err := next.Validate(); err != nil {
		return err
	}
	creds := m.credentials
	changed := u.ClearCredentials || u.Username != creds.Username || u.APIKey != ""
	if m.environment && changed {
		return errors.New("credentials are managed by the service environment")
	}
	if u.ClearCredentials {
		creds = credentials{}
	} else {
		if u.Username != creds.Username && u.APIKey == "" {
			return errors.New("changing username requires an API key")
		}
		creds.Username = strings.TrimSpace(u.Username)
		if u.APIKey != "" {
			creds.APIKey = u.APIKey
		}
		if (creds.Username == "") != (creds.APIKey == "") {
			return errors.New("username and API key must both be provided")
		}
		if len(creds.Username) > 256 || len(creds.APIKey) > 4096 {
			return errors.New("credentials exceed size limit")
		}
	}
	// Disk changes precede runtime changes. A persistence failure leaves the old runtime config active.
	// These are two atomic files, not a cross-file transaction; a crash can leave a new key with old settings.
	if changed {
		p := clonePolicies(m.policies)
		p.CredentialOwner = creds.Username
		for i := range p.Accounts {
			p.Accounts[i].ExecutionAllowed = false
		}
		if err := m.savePolicies(p); err != nil {
			return err
		}
		b, _ := json.Marshal(creds)
		if err := config.WritePrivate(m.credentialsPath, b); err != nil {
			return errors.New("could not save credentials")
		}
	}
	if err := config.Save(m.configPath, next); err != nil {
		return errors.New("could not save configuration")
	}
	m.c = next
	m.credentials = creds
	if m.executor != nil {
		if err := m.executor.SetFactory(m.executionFactory()); err != nil {
			return err
		}
	}
	api, err := m.reader()
	if err != nil {
		return err
	}
	if m.service != nil {
		m.service.Configure(api, next.AccountID, next.RefreshInterval)
	}
	return nil
}

func (m *Manager) RoutingStore() *ingress.Store { return m.ingress }
func (m *Manager) AttachTunnelWake(wake func()) { m.wakeTunnel = wake }

func (m *Manager) Access() *access.Store { return m.access }
func (m *Manager) ChangeAccess(actor, current, action, target, username, password string) error {
	if err := m.access.Change(actor, current, action, target, username, password); err != nil {
		return err
	}
	if m.executor != nil {
		m.executor.Pause()
	}
	if !m.access.BootstrapActive() {
		_ = os.Remove(filepath.Join(filepath.Dir(m.credentialsPath), "admin-password"))
	}
	m.Note("INFO", "access", "Administrator credentials updated; existing authorization was revoked as applicable.")
	return nil
}

// OpenWithClock supplies a deterministic policy clock for offline UI/controller tests.
func OpenWithClock(c config.Config, configPath, stateDir string, clock func() time.Time) (*Manager, string, error) {
	if clock == nil {
		return nil, "", errors.New("clock required")
	}
	m, token, err := Open(c, configPath, stateDir)
	if err != nil {
		return nil, "", err
	}
	m.now = clock
	return m, token, nil
}
