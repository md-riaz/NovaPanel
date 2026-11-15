package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAnonMintConfiguration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.toml")
	check := func(c Config, err error, rps, burst int) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		v := reflect.ValueOf(c)
		for name, want := range map[string]int{"AnonMintRPS": rps, "AnonMintBurst": burst} {
			field := v.FieldByName(name)
			if !field.IsValid() || int(field.Int()) != want {
				t.Fatalf("%s must be %d", name, want)
			}
		}
		if c.RateLimitRPS != 5 || c.RateLimitBurst != 20 {
			t.Fatal("web budget changed")
		}
	}
	c, err := Load(p, nil)
	check(c, err, 20, 100)
	if err := os.WriteFile(p, []byte("anon_mint_rps = 31\nanon_mint_burst = 123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(p, nil)
	check(c, err, 31, 123)
	t.Setenv("NOVAPANEL_ANON_MINT_RPS", "41")
	t.Setenv("NOVAPANEL_ANON_MINT_BURST", "143")
	c, err = Load(p, nil)
	check(c, err, 41, 143)
	c, err = Load(p, map[string]string{"anon_mint_rps": "51", "anon_mint_burst": "163"})
	check(c, err, 51, 163)
	for _, key := range []string{"anon_mint_rps", "anon_mint_burst"} {
		for _, value := range []string{"0", "-1", "bad"} {
			if _, err := Load(p, map[string]string{key: value}); err == nil {
				t.Fatalf("accepted %s=%s", key, value)
			}
		}
	}
}
