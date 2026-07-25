package auth

import (
	"encoding/hex"
	"testing"
)

// Known-answer vectors generated with Node's crypto.pbkdf2Sync (HMAC-SHA256).
func TestPBKDF2KnownAnswers(t *testing.T) {
	cases := []struct {
		password, salt string
		iter, keyLen   int
		wantHex        string
	}{
		{"password", "saltsaltsaltsalt", 4096, 32, "16e35b12b41f465ca64d069578d750059775ccad5df3ad53d6b204acb122d1ee"},
		{"correct horse", "0123456789abcdef", 1000, 20, "70183c0f60ee9e0441f64efab334e17f97a17f20"},
	}
	for _, c := range cases {
		got := hex.EncodeToString(pbkdf2SHA256([]byte(c.password), []byte(c.salt), c.iter, c.keyLen))
		if got != c.wantHex {
			t.Errorf("pbkdf2(%q,%q,%d,%d) = %s, want %s", c.password, c.salt, c.iter, c.keyLen, got, c.wantHex)
		}
	}
}

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := HashPassword("s3cret-pass")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := VerifyPassword(h, "s3cret-pass")
	if err != nil || !ok {
		t.Fatalf("verify correct: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(h, "wrong-pass")
	if err != nil || ok {
		t.Fatalf("verify wrong: ok=%v err=%v", ok, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, bad := range []string{"", "nope", "pbkdf2_sha256$x$y", "bcrypt$1$a$b"} {
		if _, err := VerifyPassword(bad, "x"); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestHashesAreSalted(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Error("two hashes of the same password should differ (random salt)")
	}
}
