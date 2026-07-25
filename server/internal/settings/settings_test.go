package settings

import "testing"

func TestSeedAndToggle(t *testing.T) {
	s := New(false, false)
	if s.ProxyEnabled() {
		t.Error("proxy should start disabled")
	}
	s.SetProxyEnabled(true)
	if !s.ProxyEnabled() {
		t.Error("proxy should be enabled after SetProxyEnabled(true)")
	}
	snap := s.Snapshot()
	if !snap.ProxyEnabled || !snap.Modules["generic"] {
		t.Errorf("snapshot not consistent: %+v", snap)
	}
}

func TestApplyIgnoresUnknownModules(t *testing.T) {
	s := New(true, false)
	s.Apply(Snapshot{
		ProxyEnabled: true,
		AccessLog:    true,
		Modules:      map[string]bool{"youtube": true, "bogus": true},
	})
	snap := s.Snapshot()
	if !snap.AccessLog {
		t.Error("access log should be applied")
	}
	if !snap.Modules["youtube"] {
		t.Error("youtube module should be applied")
	}
	if _, ok := snap.Modules["bogus"]; ok {
		t.Error("unknown module should not be stored")
	}
}
