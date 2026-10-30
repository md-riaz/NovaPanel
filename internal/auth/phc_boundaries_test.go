package auth

import (
	"strings"
	"testing"
)

func TestPHCRejectsNonCanonicalBase64(t *testing.T) {
	const phc = "$argon2id$v=19$m=8192,t=1,p=1$KioqKioqKioqKioqKioqKg$yj601DUth0a4EptCqIumdHhvbbS9VSmIznW3AoXb2n4"
	for _, field := range []int{4, 5} {
		for _, invalid := range []string{"\n", "\r", "\r\n", " ", "\t", "=", "_", "-"} {
			t.Run(strings.Join([]string{[]string{"salt", "hash"}[field-4], invalid}, "/"), func(t *testing.T) {
				parts := strings.Split(phc, "$")
				parts[field] = parts[field][:8] + invalid + parts[field][8:]
				if ok, rehash := Verify("known", strings.Join(parts, "$")); ok || rehash {
					t.Fatalf("accepted noncanonical field: ok=%v rehash=%v", ok, rehash)
				}
			})
		}
	}
	for _, bad := range []string{
		strings.Replace(phc, "KioqKg$", "KioqKh$", 1),
		strings.TrimSuffix(phc, "4") + "5",
	} {
		if ok, rehash := Verify("known", bad); ok || rehash {
			t.Fatal("accepted nonzero base64 padding bits")
		}
	}
}

// Compare valid and invalid password paths with the same full Argon2 work.
// Benchmarks report timing; noisy wall-clock ratios are not security assertions.
func BenchmarkVerifyPassword(b *testing.B) {
	const phc = "$argon2id$v=19$m=8192,t=1,p=1$KioqKioqKioqKioqKioqKg$yj601DUth0a4EptCqIumdHhvbbS9VSmIznW3AoXb2n4"
	for _, pw := range []string{"known", "wrong"} {
		b.Run(pw, func(b *testing.B) {
			for b.Loop() {
				ok, _ := Verify(pw, phc)
				if ok != (pw == "known") {
					b.Fatal("unexpected verification result")
				}
			}
		})
	}
}
