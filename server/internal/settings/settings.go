// Package settings holds live, mutable runtime settings that the admin panel can
// change without a restart. It is seeded from static configuration at startup.
// RAM-only: changes do not persist across restarts.
package settings

import "sync"

// knownModules are the module keys the panel understands. Only "generic" is
// implemented today; the others are placeholders for later milestones.
var knownModules = []string{"generic", "youtube", "telegram"}

// Settings is a thread-safe holder of runtime settings.
type Settings struct {
	mu           sync.RWMutex
	proxyEnabled bool
	accessLog    bool
	modules      map[string]bool
}

// Snapshot is a serialisable, read-only view of the settings.
type Snapshot struct {
	ProxyEnabled bool            `json:"proxyEnabled"`
	AccessLog    bool            `json:"accessLog"`
	Modules      map[string]bool `json:"modules"`
}

// New seeds settings from configuration.
func New(proxyEnabled, accessLog bool) *Settings {
	s := &Settings{
		proxyEnabled: proxyEnabled,
		accessLog:    accessLog,
		modules:      make(map[string]bool, len(knownModules)),
	}
	for _, m := range knownModules {
		s.modules[m] = false
	}
	s.modules["generic"] = proxyEnabled
	return s
}

// ProxyEnabled reports whether the web proxy should serve requests.
func (s *Settings) ProxyEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.proxyEnabled
}

// SetProxyEnabled toggles the proxy; the generic module tracks it.
func (s *Settings) SetProxyEnabled(v bool) {
	s.mu.Lock()
	s.proxyEnabled = v
	s.modules["generic"] = v
	s.mu.Unlock()
}

// AccessLog reports whether per-request access logging is on.
func (s *Settings) AccessLog() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.accessLog
}

// SetAccessLog toggles access logging.
func (s *Settings) SetAccessLog(v bool) {
	s.mu.Lock()
	s.accessLog = v
	s.mu.Unlock()
}

// Snapshot returns a copy of the current settings.
func (s *Settings) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mods := make(map[string]bool, len(s.modules))
	for k, v := range s.modules {
		mods[k] = v
	}
	return Snapshot{ProxyEnabled: s.proxyEnabled, AccessLog: s.accessLog, Modules: mods}
}

// Apply updates the editable fields from a snapshot, ignoring unknown modules.
func (s *Settings) Apply(snap Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proxyEnabled = snap.ProxyEnabled
	s.accessLog = snap.AccessLog
	s.modules["generic"] = snap.ProxyEnabled
	for _, m := range knownModules {
		if v, ok := snap.Modules[m]; ok {
			s.modules[m] = v
		}
	}
}
