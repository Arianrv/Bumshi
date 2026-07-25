package auth

import (
	"sync"
	"time"
)

// RateLimiter is a fixed-window limiter keyed by a string (typically the client
// IP). It throttles admin login attempts to slow brute-force guessing.
type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*hitWindow
	now    func() time.Time
}

type hitWindow struct {
	count int
	reset time.Time
}

// NewRateLimiter allows up to limit attempts per key within window.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:  limit,
		window: window,
		hits:   make(map[string]*hitWindow),
		now:    time.Now,
	}
}

// Allow records an attempt for key and reports whether it is within the limit.
func (r *RateLimiter) Allow(key string) bool {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.hits[key]
	if !ok || now.After(w.reset) {
		r.hits[key] = &hitWindow{count: 1, reset: now.Add(r.window)}
		return true
	}
	w.count++
	return w.count <= r.limit
}

// Reset clears the counter for key (e.g. after a successful login).
func (r *RateLimiter) Reset(key string) {
	r.mu.Lock()
	delete(r.hits, key)
	r.mu.Unlock()
}
