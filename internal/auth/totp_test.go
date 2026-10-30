package auth

import (
	"errors"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"testing"
	"time"
)

type failedEntropy struct{}

func (failedEntropy) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
func TestHostilePHCAndEntropy(t *testing.T) {
	for _, p := range []string{"", "$argon2id$v=19$m=4294967295,t=2,p=1$YWJj$YWJj", "$argon2id$v=19$m=8,t=0,p=0$YWJj$YWJj", "$argon2id$v=19$m=8,t=1,p=256$YWJj$YWJj", "$argon2id$v=19$m=8,t=1,p=1$!!!!!!!!$!!!!!!!!"} {
		if ok, r := Verify("pw", p); ok || r {
			t.Fatal(p)
		}
	}
	if h := hashWithReader("pw", failedEntropy{}); h != "" {
		t.Fatal("entropy failure produced hash")
	}
}
func TestTOTPSkew(t *testing.T) {
	secret, uri, e := GenerateTOTP("owner")
	if e != nil || len(secret) != 32 || uri == "" {
		t.Fatal(secret, uri, e)
	}
	now := time.Unix(1800000000, 0)
	for _, delta := range []int64{-2, -1, 0, 1, 2} {
		code, e := totp.GenerateCodeCustom(secret, now.Add(time.Duration(delta)*30*time.Second), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if e != nil {
			t.Fatal(e)
		}
		step, ok := VerifyTOTP(secret, code, now)
		want := delta >= -1 && delta <= 1
		if ok != want || ok && step != now.Unix()/30+delta {
			t.Fatal(delta, step, ok)
		}
	}
	if _, ok := VerifyTOTP(secret, "12345", now); ok {
		t.Fatal("short code")
	}
}
