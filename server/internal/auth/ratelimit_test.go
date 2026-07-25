package auth

import (
	"testing"
	"time"
)

func TestRateLimiterWindow(t *testing.T) {
	cur := time.Now()
	r := NewRateLimiter(3, time.Minute)
	r.now = func() time.Time { return cur }

	for i := 0; i < 3; i++ {
		if !r.Allow("ip") {
			t.Fatalf("attempt %d blocked too early", i+1)
		}
	}
	if r.Allow("ip") {
		t.Fatal("4th attempt should be blocked")
	}
	cur = cur.Add(2 * time.Minute)
	if !r.Allow("ip") {
		t.Fatal("attempt after window should be allowed")
	}
}

func TestRateLimiterReset(t *testing.T) {
	r := NewRateLimiter(1, time.Minute)
	if !r.Allow("ip") {
		t.Fatal("first attempt should pass")
	}
	if r.Allow("ip") {
		t.Fatal("second attempt should be blocked")
	}
	r.Reset("ip")
	if !r.Allow("ip") {
		t.Fatal("attempt after reset should pass")
	}
}
