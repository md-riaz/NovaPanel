package database

import "testing"

func TestSafePasswordBase64URL(t *testing.T) {
	for _, value := range []string{"abc-DEF_123", "abcd1234"} {
		if !safePassword(value) {
			t.Fatal("safe password refused")
		}
	}
	for _, value := range []string{"", "x' OR 1=1", "x\\y", "x;y"} {
		if safePassword(value) {
			t.Fatal("unsafe password accepted")
		}
	}
}
