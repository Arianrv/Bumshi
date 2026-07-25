// Package auth provides password hashing, session management, CSRF tokens, and
// login rate limiting for the admin panel — all on the standard library so the
// binary stays dependency-free.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// pbkdf2Iterations follows OWASP guidance for PBKDF2-HMAC-SHA256.
	pbkdf2Iterations = 600_000
	pbkdf2KeyLen     = 32
	saltLen          = 16
	hashScheme       = "pbkdf2_sha256"
)

var b64 = base64.RawStdEncoding

// ErrInvalidHash indicates a stored hash string is malformed.
var ErrInvalidHash = errors.New("auth: invalid password hash format")

// HashPassword returns a self-describing PHC-style hash for password:
//
//	pbkdf2_sha256$<iterations>$<base64-salt>$<base64-key>
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	dk := pbkdf2SHA256([]byte(password), salt, pbkdf2Iterations, pbkdf2KeyLen)
	return strings.Join([]string{
		hashScheme,
		strconv.Itoa(pbkdf2Iterations),
		b64.EncodeToString(salt),
		b64.EncodeToString(dk),
	}, "$"), nil
}

// VerifyPassword reports whether password matches encoded, using a constant-time
// comparison. It returns false (not an error) on any mismatch, and an error only
// when encoded is malformed.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return false, ErrInvalidHash
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false, ErrInvalidHash
	}
	salt, err := b64.DecodeString(parts[2])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := b64.DecodeString(parts[3])
	if err != nil {
		return false, ErrInvalidHash
	}
	got := pbkdf2SHA256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
