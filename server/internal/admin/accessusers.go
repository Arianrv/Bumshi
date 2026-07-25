package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// AccessUser is an end-user credential the browser app uses to reach the proxy.
type AccessUser struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	Token   string    `json:"token"`
	Created time.Time `json:"created"`
}

// AccessStore is an in-memory store of access users (RAM-only; see DESIGN §8.4).
type AccessStore struct {
	mu    sync.RWMutex
	users map[string]AccessUser
}

// NewAccessStore returns an empty store.
func NewAccessStore() *AccessStore {
	return &AccessStore{users: make(map[string]AccessUser)}
}

// Create adds a new access user with a random id and token.
func (s *AccessStore) Create(label string) (AccessUser, error) {
	id, err := randomHex(8)
	if err != nil {
		return AccessUser{}, err
	}
	token, err := randomHex(24)
	if err != nil {
		return AccessUser{}, err
	}
	u := AccessUser{ID: id, Label: label, Token: token, Created: time.Now().UTC()}
	s.mu.Lock()
	s.users[id] = u
	s.mu.Unlock()
	return u, nil
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

// Delete removes an access user by id.
func (s *AccessStore) Delete(id string) {
	s.mu.Lock()
	delete(s.users, id)
	s.mu.Unlock()
}

// Valid reports whether token matches a stored access user. Reserved for future
// proxy access-gating; comparison is constant-time.
func (s *AccessStore) Valid(token string) bool {
	if token == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found bool
	for _, u := range s.users {
		if subtle.ConstantTimeCompare([]byte(u.Token), []byte(token)) == 1 {
			found = true
		}
	}
	return found
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
