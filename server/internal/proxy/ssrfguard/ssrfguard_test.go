package ssrfguard

import (
	"net/netip"
	"testing"
)

func TestIsPublic(t *testing.T) {
	cases := []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.0.0.1", false},
		{"172.16.5.4", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false}, // cloud metadata (link-local)
		{"fe80::1", false},
		{"fc00::1", false}, // ULA
		{"100.64.0.1", false},
		{"0.0.0.0", false},
		{"224.0.0.1", false},
		{"::ffff:127.0.0.1", false}, // IPv4-mapped loopback
	}
	for _, c := range cases {
		addr, err := netip.ParseAddr(c.ip)
		if err != nil {
			t.Fatalf("parse %s: %v", c.ip, err)
		}
		if got := IsPublic(addr); got != c.public {
			t.Errorf("IsPublic(%s) = %v, want %v", c.ip, got, c.public)
		}
	}
}

func TestControlBlocksPrivate(t *testing.T) {
	if err := Control("tcp", "127.0.0.1:80", nil); err == nil {
		t.Error("Control allowed loopback")
	}
	if err := Control("tcp", "10.0.0.1:443", nil); err == nil {
		t.Error("Control allowed private")
	}
}

func TestControlAllowsPublic(t *testing.T) {
	if err := Control("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("Control blocked public: %v", err)
	}
}

func TestControlRejectsUnparsable(t *testing.T) {
	if err := Control("tcp", "not-an-address", nil); err == nil {
		t.Error("Control should reject an address without a port")
	}
}
