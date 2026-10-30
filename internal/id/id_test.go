package id

import (
	"strings"
	"testing"
	"time"
)

func TestNewULID(t *testing.T) {
	seen := map[string]bool{}
	before := time.Now().UnixMilli()
	for range 1000 {
		s, err := New()
		if err != nil || !Valid(s) || seen[s] {
			t.Fatalf("invalid or duplicate ULID %q: %v", s, err)
		}
		seen[s] = true
		var ms int64
		for _, c := range s[:10] {
			ms = ms*32 + int64(strings.IndexRune(alphabet, c))
		}
		if ms < before || ms > time.Now().UnixMilli() {
			t.Fatalf("timestamp %d", ms)
		}
	}
}
func TestValid(t *testing.T) {
	for _, s := range []string{"00000000000000000000000000", "7ZZZZZZZZZZZZZZZZZZZZZZZZZ", "01ARZ3NDEKTSV4RRFFQ69G5FAV"} {
		if !Valid(s) {
			t.Errorf("rejected %q", s)
		}
	}
	for _, s := range []string{"", "1", "8ZZZZZZZZZZZZZZZZZZZZZZZZZ", "01ARZ3NDEKTSV4RRFFQ69G5FAI", "01arz3ndektsv4rrffq69g5fav"} {
		if Valid(s) {
			t.Errorf("accepted %q", s)
		}
	}
}
