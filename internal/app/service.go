package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/yelaco/projectx-pilot/internal/projectx"
)

type Reader interface {
	Accounts(context.Context) ([]projectx.Account, error)
	AvailableContracts(context.Context) ([]projectx.Contract, error)
}
type Snapshot struct {
	Mode        string              `json:"mode"`
	Connected   bool                `json:"connected"`
	Selected    *projectx.Account   `json:"selected_account,omitempty"`
	Accounts    []projectx.Account  `json:"accounts"`
	Contracts   []projectx.Contract `json:"contracts"`
	UpdatedAt   time.Time           `json:"updated_at"`
	LastAttempt time.Time           `json:"last_attempt"`
	Error       string              `json:"error,omitempty"`
}
type Service struct {
	opMu       sync.Mutex
	wake       chan struct{}
	interval   time.Duration
	generation uint64
	api        Reader
	accountID  int64
	mu         sync.RWMutex
	state      Snapshot
}

func New(api Reader, id int64) *Service {
	return &Service{wake: make(chan struct{}, 1), api: api, accountID: id, state: Snapshot{Mode: "read-only"}}
}
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.state
	v.Accounts = append([]projectx.Account(nil), v.Accounts...)
	v.Contracts = append([]projectx.Contract(nil), v.Contracts...)
	if v.Selected != nil {
		x := *v.Selected
		v.Selected = &x
	}
	return v
}
func (s *Service) Refresh(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.RLock()
	api, id, generation := s.api, s.accountID, s.generation
	s.mu.RUnlock()
	if api == nil {
		s.mu.Lock()
		if generation == s.generation {
			s.state.Connected = false
			s.state.Error = "Configure ProjectX credentials in Settings"
		}
		s.mu.Unlock()
		return errors.New("ProjectX credentials not configured")
	}
	accounts, err := api.Accounts(ctx)
	var contracts []projectx.Contract
	if err == nil {
		contracts, err = api.AvailableContracts(ctx)
	}
	var selected *projectx.Account
	if err == nil && id != 0 {
		for _, a := range accounts {
			if a.ID == id {
				v := a
				selected = &v
				break
			}
		}
		if selected == nil {
			err = errAccountNotFound
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.generation {
		return errors.New("configuration changed during refresh; result discarded")
	}
	s.state.LastAttempt = time.Now().UTC()
	if err != nil {
		s.state.Connected = false
		s.state.Error = refreshFailure(err)
		return err
	}
	s.state = Snapshot{Mode: "read-only", Connected: true, Accounts: accounts, Contracts: contracts, Selected: selected, UpdatedAt: time.Now().UTC(), LastAttempt: s.state.LastAttempt}
	return nil
}

// Configure invalidates old snapshots before a new account/client can be observed.
func (s *Service) Configure(api Reader, id int64, interval time.Duration) {
	s.mu.Lock()
	s.api = api
	s.accountID = id
	s.interval = interval
	s.generation++
	s.state = Snapshot{Mode: "read-only"}
	s.mu.Unlock()
	s.RequestRefresh()
}
func (s *Service) RequestRefresh() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Run(ctx context.Context, interval time.Duration, log *slog.Logger) {
	s.mu.Lock()
	if s.interval == 0 {
		s.interval = interval
	}
	s.mu.Unlock()
	refresh := func() {
		err := s.Refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Warn("read-only refresh failed", "error", err)
		} else {
			log.Info("read-only snapshot refreshed")
		}
	}
	refresh()
	for {
		s.mu.RLock()
		wait := s.interval
		s.mu.RUnlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
		refresh()
	}
}
