package secret_test

import (
	"bytes"
	"context"
	"encoding/json"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyLifecycleSealingSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "seal.key")
	s, e := secret.LoadOrCreate(p)
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(p)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, e)
	}
	sealed, e := s.Seal([]byte("password"))
	if e != nil || bytes.Contains(sealed.Ciphertext, []byte("password")) {
		t.Fatal(sealed, e)
	}
	s2, e := secret.LoadOrCreate(p)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := s2.Open(sealed)
	if e != nil || string(plain) != "password" {
		t.Fatal(string(plain), e)
	}
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	b, _ := json.Marshal(sealed)
	if e = db.SetSetting(context.Background(), "smoke", b); e != nil {
		t.Fatal(e)
	}
	b, e = db.Setting(context.Background(), "smoke")
	if e != nil {
		t.Fatal(e)
	}
	var saved secret.Sealed
	if e = json.Unmarshal(b, &saved); e != nil {
		t.Fatal(e)
	}
	if plain, e = s.Open(saved); e != nil || string(plain) != "password" {
		t.Fatal(string(plain), e)
	}
	sealed.Ciphertext[0] ^= 1
	if _, e = s.Open(sealed); e == nil {
		t.Fatal("tampering accepted")
	}
	if _, e = s.Open(secret.Sealed{}); e == nil {
		t.Fatal("malformed accepted")
	}
	os.Chmod(p, 0644)
	if _, e = secret.LoadOrCreate(p); e == nil {
		t.Fatal("unsafe permissions accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(p, link)
	if _, e = secret.LoadOrCreate(link); e == nil {
		t.Fatal("symlink accepted")
	}
}
func TestRedactNestedNoMutation(t *testing.T) {
	original := map[string]any{"password": "shh", "nested": []any{map[string]any{"TOKEN": "npat_id_secret", "message": "Bearer abc.def and nps_id_secret"}}, "public": "safe"}
	out := secret.Redact(original)
	b, _ := json.Marshal(out)
	if bytes.Contains(b, []byte("shh")) || bytes.Contains(b, []byte("abc.def")) || bytes.Contains(b, []byte("npat_id_secret")) || bytes.Contains(b, []byte("nps_id_secret")) {
		t.Fatal(string(b))
	}
	if original["password"] != "shh" || out["public"] != "safe" {
		t.Fatal(out, original)
	}
}
