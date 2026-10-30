// Package id generates canonical ULIDs with cryptographically random entropy.
package id

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New combines a 48-bit millisecond timestamp and 80 random bits. It does not
// promise monotonic ordering within a millisecond. Entropy failures fail closed.
func New() (string, error) {
	var entropy [10]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	var stamp [10]byte
	ms := uint64(time.Now().UnixMilli())
	for i := 9; i >= 0; i-- {
		stamp[i] = alphabet[ms&31]
		ms >>= 5
	}
	return string(stamp[:]) + base32.NewEncoding(alphabet).WithPadding(base32.NoPadding).EncodeToString(entropy[:]), nil
}

func Valid(s string) bool {
	if len(s) != 26 || s[0] > '7' {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune(alphabet, c) {
			return false
		}
	}
	return true
}
