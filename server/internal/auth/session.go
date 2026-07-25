package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Session is an authenticated admin session.
type Session struct {
	User   string
	CSRF   string
	Expiry time.Time
}

// SessionStore is an in-memory session store. Sessions are lost on restart,
// which is acceptable under the RAM-only privacy model — the admin simply logs
// in again.
type SessionStore struct {
	mu      sync.Mutex
	byToken map[string]Session
	ttl     time.Duration
	now     func() time.Time
}

// NewSessionStore returns an empty store whose sessions live for ttl.
func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{byToken: make(map[string]Session), ttl: ttl, now: time.Now}
}

// Create starts a session for user and returns its token and CSRF token.
func (s *SessionStore) Create(user string) (token, csrf string, err error) {
	if token, err = randomToken(); err != nil {
		return "", "", err
	}
	if csrf, err = randomToken(); err != nil {
		return "", "", err
	}
	s.mu.Lock()
	s.byToken[token] = Session{User: user, CSRF: csrf, Expiry: s.now().Add(s.ttl)}
	s.mu.Unlock()
	return token, csrf, nil
}

// Get returns the session for token if it exists and has not expired.
func (s *SessionStore) Get(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byToken[token]
	if !ok {
		return Session{}, false
	}
	if s.now().After(sess.Expiry) {
		delete(s.byToken, token)
		return Session{}, false
	}
	return sess, true
}

// Delete removes a session (logout).
func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	delete(s.byToken, token)
	s.mu.Unlock()
}

// Count returns the number of live sessions, pruning expired ones.
func (s *SessionStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for t, sess := range s.byToken {
		if now.After(sess.Expiry) {
			delete(s.byToken, t)
		}
	}
	return len(s.byToken)
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
