//go:build wordpress_offline_lab

package offline

import "testing"

func TestResolve(t *testing.T) {
	for url, entry := range allowed {
		b, err := Resolve(url)
		if err != nil || !matches(b, entry.hash) {
			t.Fatalf("fixture %s: %v", url, err)
		}
		b[0] ^= 1
		if matches(b, entry.hash) {
			t.Fatalf("tampered fixture %s accepted", url)
		}
	}
	if _, err := Resolve("https://example.com/"); err == nil {
		t.Fatal("unknown URL accepted")
	}
}
