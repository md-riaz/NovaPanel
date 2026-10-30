package auth

import (
	"crypto/rand"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"strings"
	"time"
)

// GenerateTOTP returns plaintext only for immediate agent sealing and show-once URI delivery.
func GenerateTOTP(account string) (secret, uri string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "NovaPanel", AccountName: account, Period: 30, SecretSize: 20, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1, Rand: rand.Reader})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// VerifyTOTP returns the exact counter to atomically consume in the persistent store.
func VerifyTOTP(secret, code string, now time.Time) (int64, bool) {
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" || now.Unix() < 30 {
		return 0, false
	}
	opts := totp.ValidateOpts{Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	ok, err := totp.ValidateCustom(code, secret, now, opts)
	if err != nil || !ok {
		return 0, false
	}
	// Check current first, matching the library's skew search order.
	opts.Skew = 0
	for _, delta := range []int64{0, 1, -1} {
		at := now.Add(time.Duration(delta) * 30 * time.Second)
		if ok, e := totp.ValidateCustom(code, secret, at, opts); e == nil && ok {
			return at.Unix() / 30, true
		}
	}
	return 0, false
}
