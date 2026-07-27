package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// AccessUser is an end-user credential the browser app uses to reach the proxy.
type AccessUser struct {
	ID      string     `json:"id"`
	Label   string     `json:"label"`
	Token   string     `json:"token"`
	Created time.Time  `json:"created"`
	Expires *time.Time `json:"expires,omitempty"` // nil = never expires
}

// Expired reports whether the user's access has lapsed.
func (u AccessUser) Expired() bool {
	return u.Expires != nil && time.Now().After(*u.Expires)
}

// AccessStore holds the roster of access users. The roster itself is persisted
// to disk so it survives restarts; what users browse is never recorded.
type AccessStore struct {
	mu    sync.RWMutex
	users map[string]AccessUser
	path  string // JSON file the roster is persisted to; "" = memory only
}

// NewAccessStore returns a store. When path is non-empty the roster is loaded
// from that JSON file (if present) and re-saved on every change, so access
// users persist across restarts.
func NewAccessStore(path string) (*AccessStore, error) {
	s := &AccessStore{users: make(map[string]AccessUser), path: path}
	if path == "" {
		return s, nil
	}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("access store: %w", err)
	}
	return s, nil
}

// load reads the roster from disk. A missing file is treated as an empty store.
func (s *AccessStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var users []AccessUser
	if err := json.Unmarshal(data, &users); err != nil {
		return err
	}
	s.mu.Lock()
	for _, u := range users {
		if u.ID != "" {
			s.users[u.ID] = u
		}
	}
	s.mu.Unlock()
	return nil
}

// save atomically writes the roster to disk. Must be called with s.mu held.
func (s *AccessStore) save() error {
	if s.path == "" {
		return nil
	}
	users := make([]AccessUser, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Created.Before(users[j].Created) })
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Create adds a new access user with a random id and token. expires may be nil
// (never expires). The returned error covers both randomness and persistence
// failures; on a persistence failure the user still exists in memory.
func (s *AccessStore) Create(label string, expires *time.Time) (AccessUser, error) {
	id, err := randomHex(8)
	if err != nil {
		return AccessUser{}, err
	}
	token, err := randomHex(24)
	if err != nil {
		return AccessUser{}, err
	}
	u := AccessUser{ID: id, Label: label, Token: token, Created: time.Now().UTC(), Expires: expires}
	s.mu.Lock()
	s.users[id] = u
	err = s.save()
	s.mu.Unlock()
	return u, err
}

// List returns all access users, oldest first.
func (s *AccessStore) List() []AccessUser {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AccessUser, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Delete removes an access user by id and persists the change.
func (s *AccessStore) Delete(id string) error {
	s.mu.Lock()
	delete(s.users, id)
	err := s.save()
	s.mu.Unlock()
	return err
}

// Authorized reports whether token matches a stored access user that has not
// expired. Token comparison is constant-time. This gates the proxy when
// BUMSHI_PROXY_REQUIRE_TOKEN is enabled.
func (s *AccessStore) Authorized(token string) bool {
	if token == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var matched *AccessUser
	for id, u := range s.users {
		if subtle.ConstantTimeCompare([]byte(u.Token), []byte(token)) == 1 {
			hit := s.users[id]
			matched = &hit
		}
	}
	if matched == nil {
		return false
	}
	return !matched.Expired()
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
