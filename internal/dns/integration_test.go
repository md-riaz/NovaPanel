package dns

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestIntegrationValidation(t *testing.T) {
	good := IntegrationSpec{Kind: "cloudflare", Zones: []ZoneBinding{{Zone: "example.com", ProviderZoneID: strings.Repeat("a", 32)}}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, v := range []IntegrationSpec{
		{}, {Kind: "other", Zones: good.Zones}, {Kind: "cloudflare"},
		{Kind: "cloudflare", Zones: []ZoneBinding{{"EXAMPLE.com", strings.Repeat("a", 32)}}},
		{Kind: "cloudflare", Zones: []ZoneBinding{{"example.com", "../bad"}}},
		{Kind: "cloudflare", Zones: append(append([]ZoneBinding{}, good.Zones...), good.Zones...)},
		{Kind: "cloudflare", Zones: []ZoneBinding{{"example.com", strings.Repeat("a", 32)}, {"other.com", strings.Repeat("a", 32)}}},
	} {
		if v.Validate() == nil {
			t.Fatalf("accepted %#v", v)
		}
	}
}

func TestIntegrationTokenRedaction(t *testing.T) {
	const plain = "synthetic-only-token"
	v, err := NewIntegrationToken([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	for _, out := range []string{string(raw), fmt.Sprint(v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v)} {
		if strings.Contains(out, plain) {
			t.Fatal("credential leaked")
		}
	}
	for _, bad := range []string{"", "a\nb", " a", strings.Repeat("a", 4097)} {
		if _, err := NewIntegrationToken([]byte(bad)); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	if string(v.ExecutionBytes()) != plain {
		t.Fatal("token changed")
	}
	v.Destroy()
	if len(v.ExecutionBytes()) != 0 {
		t.Fatal("destroy retained token")
	}
}
