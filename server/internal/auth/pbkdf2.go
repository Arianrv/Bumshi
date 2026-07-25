package auth

import (
	"crypto/hmac"
	"crypto/sha256"
)

// pbkdf2SHA256 derives a key of length keyLen from password and salt using
// PBKDF2 with HMAC-SHA256 over iter iterations (RFC 8018). It is implemented on
// the standard library so the module stays dependency-free.
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	dk := make([]byte, 0, numBlocks*hashLen)
	block := make([]byte, 4)
	u := make([]byte, hashLen)

	for i := 1; i <= numBlocks; i++ {
		prf.Reset()
		prf.Write(salt)
		block[0] = byte(i >> 24)
		block[1] = byte(i >> 16)
		block[2] = byte(i >> 8)
		block[3] = byte(i)
		prf.Write(block)
		t := prf.Sum(nil) // U_1
		copy(u, t)

		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0]) // U_n
			for x := range t {
				t[x] ^= u[x]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}
