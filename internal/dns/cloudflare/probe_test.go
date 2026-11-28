package cloudflare

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProbeZoneVisibility(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("unexpected request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true,"result":[{"name":"example.com"}]}`)), Header: make(http.Header)}, nil
	})}
	if err := probe(context.Background(), client, "test-token", []string{"example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := probe(context.Background(), client, "test-token", []string{"other.example"}); err == nil {
		t.Fatal("unexpected zone accepted")
	}
}
