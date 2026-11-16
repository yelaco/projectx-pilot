// Package access manages local administrator credentials. No trading credentials live here.
package access

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/yelaco/projectx-pilot/internal/config"
)

type Info struct {
	ID, Username string
	CreatedAt    time.Time
}
type Identity struct {
	Info
	Secret string
}
type user struct {
	Info
	Hash   string
	Secret string
}
type file struct {
	Users       []user
	BootstrapID string
}
type cached struct {
	fingerprint [32]byte
	secret      string
	until       time.Time
}
type Store struct {
	mu       sync.Mutex
	path     string
	data     file
	cache    map[string]cached
	window   time.Time
	attempts int
	slots    chan struct{}
}

func random() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func validUsername(name string) bool {
	if len(name) < 1 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-@", r)) {
			return false
		}
	}
	return true
}
func validPassword(password string) bool { return len(password) >= 12 && len(password) <= 72 }
func Open(path, bootstrap string) (*Store, error) {
	s := &Store{path: path, cache: map[string]cached{}, slots: make(chan struct{}, 2)}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if !validPassword(bootstrap) {
			return nil, errors.New("initial admin password must contain 12..72 bytes")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(bootstrap), bcrypt.DefaultCost)
		if err != nil {
			return nil, errors.New("could not hash initial password")
		}
		id, err := random()
		if err != nil {
			return nil, err
		}
		secret, err := random()
		if err != nil {
			return nil, err
		}
		s.data = file{Users: []user{{Info: Info{ID: id, Username: "local", CreatedAt: time.Now().UTC()}, Hash: string(hash), Secret: secret}}, BootstrapID: id}
		if err = s.save(s.data); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	if len(b) > 65536 || json.Unmarshal(b, &s.data) != nil || len(s.data.Users) == 0 || len(s.data.Users) > 20 {
		return nil, errors.New("invalid administrator credential store")
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, u := range s.data.Users {
		cost, err := bcrypt.Cost([]byte(u.Hash))
		if err != nil || cost < 10 || cost > 14 || !validUsername(u.Username) || seen[u.Username] || u.ID == "" || ids[u.ID] || len(u.Secret) != 64 {
			return nil, errors.New("invalid administrator record")
		}
		seen[u.Username] = true
		ids[u.ID] = true
	}
	return s, nil
}
func (s *Store) save(next file) error {
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err = config.WritePrivate(s.path, b); err != nil {
		return errors.New("could not save administrator credentials")
	}
	s.data = next
	s.cache = map[string]cached{}
	return nil
}
func (s *Store) List() []Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Info, 0, len(s.data.Users))
	for _, u := range s.data.Users {
		out = append(out, u.Info)
	}
	return out
}
func (s *Store) BootstrapActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.BootstrapID != ""
}
func (s *Store) Verify(username, password string) (Identity, bool) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !validUsername(username) || len(password) > 72 || password == "" {
		return Identity{}, false
	}
	fingerprint := sha256.Sum256([]byte(username + "\x00" + password))
	s.mu.Lock()
	var candidate user
	for _, u := range s.data.Users {
		if u.Username == username {
			candidate = u
			break
		}
	}
	known := candidate.ID != ""
	if !known {
		candidate.Hash = s.data.Users[0].Hash
	}
	if c, ok := s.cache[candidate.ID]; ok && c.fingerprint == fingerprint && c.secret == candidate.Secret && time.Now().Before(c.until) {
		s.mu.Unlock()
		return Identity{candidate.Info, candidate.Secret}, true
	}
	now := time.Now()
	if s.window.IsZero() || now.Sub(s.window) >= time.Minute {
		s.window = now
		s.attempts = 0
	}
	s.attempts++
	allowed := s.attempts <= 40
	s.mu.Unlock()
	if !allowed {
		return Identity{}, false
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return Identity{}, false
	}
	if bcrypt.CompareHashAndPassword([]byte(candidate.Hash), []byte(password)) != nil || !known {
		return Identity{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.data.Users {
		if u.ID == candidate.ID && u.Username == candidate.Username && u.Hash == candidate.Hash && u.Secret == candidate.Secret {
			s.cache[u.ID] = cached{fingerprint, u.Secret, time.Now().Add(time.Minute)}
			return Identity{u.Info, u.Secret}, true
		}
	}
	return Identity{}, false
}

// Change requires the acting administrator's current password even after HTTP authentication.
func (s *Store) Change(actorID, currentPassword, action, targetID, username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var actor *user
	for i := range s.data.Users {
		if s.data.Users[i].ID == actorID {
			actor = &s.data.Users[i]
			break
		}
	}
	if actor == nil || len(currentPassword) > 72 || bcrypt.CompareHashAndPassword([]byte(actor.Hash), []byte(currentPassword)) != nil {
		return errors.New("current administrator password is incorrect")
	}
	next := file{Users: append([]user(nil), s.data.Users...), BootstrapID: s.data.BootstrapID}
	username = strings.ToLower(strings.TrimSpace(username))
	switch action {
	case "add", "update":
		if !validUsername(username) {
			return errors.New("admin username must be 1..64 lowercase letters, digits, dots, underscores, hyphens or @")
		}
		for _, u := range next.Users {
			if u.Username == username && (action == "add" || u.ID != targetID) {
				return errors.New("administrator username already exists")
			}
		}
		if action == "add" && len(next.Users) >= 20 {
			return errors.New("maximum 20 administrator logins")
		}
		if action == "add" || password != "" {
			if !validPassword(password) {
				return errors.New("new password must contain 12..72 bytes")
			}
		}
		hash := ""
		if password != "" {
			b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				return errors.New("could not hash new password")
			}
			hash = string(b)
		}
		secret, err := random()
		if err != nil {
			return errors.New("could not generate credential state")
		}
		if action == "add" {
			id, err := random()
			if err != nil {
				return err
			}
			next.Users = append(next.Users, user{Info: Info{ID: id, Username: username, CreatedAt: time.Now().UTC()}, Hash: hash, Secret: secret})
		} else {
			found := false
			for i := range next.Users {
				if next.Users[i].ID == targetID {
					next.Users[i].Username = username
					next.Users[i].Secret = secret
					if hash != "" {
						next.Users[i].Hash = hash
					}
					found = true
				}
			}
			if !found {
				return errors.New("administrator login not found")
			}
			if targetID == next.BootstrapID {
				next.BootstrapID = ""
			}
		}
	case "delete":
		if len(next.Users) <= 1 {
			return errors.New("cannot remove the last administrator")
		}
		found := false
		for i, u := range next.Users {
			if u.ID == targetID {
				next.Users = append(next.Users[:i], next.Users[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return errors.New("administrator login not found")
		}
		if targetID == next.BootstrapID {
			next.BootstrapID = ""
		}
	default:
		return errors.New("unknown administrator action")
	}
	return s.save(next)
}
