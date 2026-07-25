package auth

import (
	"testing"
	"time"
)

func TestSessionCreateGetDelete(t *testing.T) {
	s := NewSessionStore(time.Hour)
	tok, csrf, err := s.Create("admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tok == "" || csrf == "" || tok == csrf {
		t.Fatalf("bad token/csrf: %q %q", tok, csrf)
	}
	sess, ok := s.Get(tok)
	if !ok || sess.User != "admin" || sess.CSRF != csrf {
		t.Fatalf("Get mismatch: %+v ok=%v", sess, ok)
	}
	s.Delete(tok)
	if _, ok := s.Get(tok); ok {
		t.Fatal("session still present after delete")
	}
}

func TestSessionExpiry(t *testing.T) {
	s := NewSessionStore(time.Minute)
	cur := time.Now()
	s.now = func() time.Time { return cur }
	tok, _, _ := s.Create("admin")
	if _, ok := s.Get(tok); !ok {
		t.Fatal("fresh session should be valid")
	}
	cur = cur.Add(2 * time.Minute)
	if _, ok := s.Get(tok); ok {
		t.Fatal("expired session should be gone")
	}
}
