package cloudflare_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"novapanel/internal/dns"
	"novapanel/internal/dns/cloudflare"
	"strings"
	"testing"
)

func TestClientFormattingRedactsCredential(t *testing.T) {
	const token = "formatting-synthetic-credential-never-use"
	c, err := cloudflare.New(token, map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("request lost its authorization credential")
		}
		return nil, errors.New(token)
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, requestErr := c.Create(context.Background(), "example.com", dns.Record{Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1})
	if requestErr == nil {
		t.Fatal("transport failure was accepted")
	}
	values := map[string]any{
		"pointer":        c,
		"value":          *c,
		"struct":         struct{ Client cloudflare.Client }{*c},
		"pointer struct": struct{ Client *cloudflare.Client }{c},
		"slice":          []any{c, *c},
		"map":            map[string]any{"pointer": c, "value": *c},
		"error":          requestErr,
	}
	check := func(t *testing.T, text string) {
		t.Helper()
		for _, secret := range []string{token, hex.EncodeToString([]byte(token)), strings.ToUpper(hex.EncodeToString([]byte(token)))} {
			if strings.Contains(text, secret) {
				t.Fatal("credential escaped diagnostic projection")
			}
		}
	}
	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			// Include unsupported verbs, which otherwise recursively dump struct fields.
			for _, verb := range "vTtbcdoOxXUeEfFgGspqzw" {
				for _, flags := range []string{"", "+", "#", " ", "020", "-20", ".8", "+#20.8"} {
					format := "%" + flags + string(verb)
					t.Run(format, func(t *testing.T) {
						text := fmt.Sprintf(format, value)
						check(t, text)
						check(t, fmt.Errorf(format, value).Error())
						if (name == "pointer" || name == "value") && verb != 'T' && verb != 'p' && verb != 'w' && text != "Cloudflare client (redacted)" {
							t.Fatal("client formatting exposed its fields instead of the redacted projection")
						}
					})
				}
			}
			check(t, fmt.Sprint(value))
			check(t, fmt.Errorf("client: %v", value).Error())
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			check(t, string(data))
		})
	}
}
