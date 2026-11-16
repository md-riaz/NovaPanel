//go:build wordpress_offline_lab

package offline

import (
	"context"
	"net/http"
	"testing"
)

func TestTransport(t *testing.T) {
	client := &http.Client{Transport: Transport{}}
	for url := range allowed {
		resp, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	for _, url := range []string{"https://example.com/", "https://api.wordpress.org/core/checksums/1.0/?locale=en_US&version=7.1.4"} {
		if _, err := client.Get(url); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/", nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("accepted POST")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("accepted canceled request")
	}
}
