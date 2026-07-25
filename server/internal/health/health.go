// Package health provides liveness and readiness HTTP handlers with an
// atomically-updated readiness flag.
package health

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// Checker tracks readiness state and serves health endpoints. The zero value is
// not ready; use New.
type Checker struct {
	ready atomic.Bool
}

// New returns a Checker that starts in the not-ready state.
func New() *Checker { return &Checker{} }

// SetReady marks the service ready (true) or not-ready (false). It is safe for
// concurrent use.
func (c *Checker) SetReady(ready bool) { c.ready.Store(ready) }

// Live reports 200 as long as the process is running (liveness probe).
func (c *Checker) Live(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, "ok")
}

// Ready reports 200 when the service is ready and 503 otherwise (readiness
// probe). Load balancers should route traffic only while this returns 200.
func (c *Checker) Ready(w http.ResponseWriter, _ *http.Request) {
	if c.ready.Load() {
		writeStatus(w, http.StatusOK, "ready")
		return
	}
	writeStatus(w, http.StatusServiceUnavailable, "not ready")
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}
