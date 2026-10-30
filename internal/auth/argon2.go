// Package auth implements pure password and TOTP cryptography, not authorization.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"golang.org/x/crypto/argon2"
	"io"
	"strconv"
	"strings"
)

const memory = 19456
const iterations = 2
const parallelism = 1

// Hash returns an empty string if secure entropy cannot be obtained. Never persist it.
func Hash(pw string) string { return hashWithReader(pw, rand.Reader) }
func hashWithReader(pw string, r io.Reader) string {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(r, salt); err != nil {
		return ""
	}
	key := argon2.IDKey([]byte(pw), salt, iterations, memory, parallelism, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", memory, iterations, parallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// Verify rejects hostile PHC costs before allocating Argon2 memory. Resource caps
// are 64 MiB, 10 iterations, 4 lanes, 64 salt bytes and 64 output bytes.
func Verify(pw, phc string) (ok, needsRehash bool) {
	if len(phc) > 512 {
		return false, false
	}
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, false
	}
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return false, false
	}
	vals := [3]uint64{}
	for i, prefix := range []string{"m=", "t=", "p="} {
		if !strings.HasPrefix(fields[i], prefix) {
			return false, false
		}
		value := strings.TrimPrefix(fields[i], prefix)
		if value == "" || strings.Trim(value, "0123456789") != "" {
			return false, false
		}
		n, e := strconv.ParseUint(value, 10, 32)
		if e != nil {
			return false, false
		}
		vals[i] = n
	}
	m, t, p := vals[0], vals[1], vals[2]
	if p < 1 || p > 4 || m < 8*p || m > 65536 || t < 1 || t > 10 {
		return false, false
	}
	salt, e := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if e != nil || len(salt) < 8 || len(salt) > 64 {
		return false, false
	}
	expected, e := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if e != nil || len(expected) < 16 || len(expected) > 64 {
		return false, false
	}
	actual := argon2.IDKey([]byte(pw), salt, uint32(t), uint32(m), uint8(p), uint32(len(expected)))
	ok = subtle.ConstantTimeCompare(actual, expected) == 1
	return ok, ok && (m < memory || t < iterations || p < parallelism || len(salt) < 16 || len(expected) < 32)
}
