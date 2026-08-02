// Package settings holds live, mutable runtime settings that the admin panel can
// change without a restart.
//
// Changes persist when a path is configured. Without that, an operator who
// switched the proxy off in the panel found it back on after any restart or
// crash — the control silently undid itself, which is the worst way for a
// safety switch to behave.
package settings

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// knownModules are the module keys the panel understands. Only "generic" is
// implemented today; the others are placeholders for later milestones.
var knownModules = []string{"generic", "youtube", "telegram"}

// Settings is a thread-safe holder of runtime settings.
type Settings struct {
	mu           sync.RWMutex
	proxyEnabled bool
	accessLog    bool
	modules      map[string]bool

	// path is the JSON file settings persist to; "" keeps them in memory only.
	path   string
	logger *slog.Logger
}

// Persist points the settings at a file and loads whatever is already there,
// so a panel change outlives a restart. A missing or unreadable file leaves the
// values seeded from configuration.
func (s *Settings) Persist(path string, logger *slog.Logger) {
	if path == "" {
		return
	}
	s.mu.Lock()
	s.path = path
	s.logger = logger
	s.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) && logger != nil {
			logger.Warn("could not read saved settings; using the configured values", "path", path, "error", err)
		}
		return
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		if logger != nil {
			logger.Warn("saved settings are unreadable; using the configured values", "path", path, "error", err)
		}
		return
	}
	s.Apply(snap)
}

// save writes the current settings. Must be called with s.mu held.
func (s *Settings) save() {
	if s.path == "" {
		return
	}
	snap := Snapshot{ProxyEnabled: s.proxyEnabled, AccessLog: s.accessLog, Modules: map[string]bool{}}
	for k, v := range s.modules {
		snap.Modules[k] = v
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	if dir := filepath.Dir(s.path); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		if s.logger != nil {
			s.logger.Error("could not save settings", "path", s.path, "error", err)
		}
		return
	}
	if err := os.Rename(tmp, s.path); err != nil && s.logger != nil {
		s.logger.Error("could not save settings", "path", s.path, "error", err)
	}
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
	s.save()
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
	s.save()
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

// Validate rejects a snapshot the panel should never have sent. Unknown module
// keys are the interesting case: silently ignoring them means a typo in the UI
// looks like it worked and the setting simply never takes effect.
func Validate(snap Snapshot) error {
	for name := range snap.Modules {
		if !slices.Contains(knownModules, name) {
			return fmt.Errorf("unknown module %q", name)
		}
	}
	return nil
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
	s.save()
}
