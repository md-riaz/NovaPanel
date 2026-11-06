package update

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplyPublishesTraversableVersion(t *testing.T) {
	original := releasePublicKeyBase64
	t.Cleanup(func() { releasePublicKeyBase64 = original })
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "1.0.0"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("1.0.0", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	// Apply must not broaden permissions on unrelated secrets or the caller's root.
	secret := filepath.Join(root, "secret")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	b := tarBytes(t, binaryHeaders())
	sum := sha256.Sum256(b)
	body, err := json.Marshal(Manifest{Version: "1.2.3", Artifacts: []Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Filename: "panel.tar", Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:]), URL: "https://releases.example/panel.tar"}}})
	if err != nil {
		t.Fatal(err)
	}
	pub, sig := signed(t, string(body))
	releasePublicKeyBase64 = base64.StdEncoding.EncodeToString(pub)
	r := &fakeRunner{}
	probe := func(context.Context) error {
		for _, name := range []string{"", "panel-agent", "panel-web", "novapanelctl"} {
			p := filepath.Join(root, "current", name)
			st, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != 0755 {
				t.Errorf("published %s mode=%04o, want 0755", name, st.Mode().Perm())
			}
		}
		for name, want := range map[string]os.FileMode{root: 0700, secret: 0600, filepath.Join(root, ".update-pending"): 0600} {
			st, err := os.Stat(name)
			if err != nil || st.Mode().Perm() != want {
				t.Fatalf("private path %s: stat=%v err=%v, want %04o", name, st, err, want)
			}
		}
		return nil
	}
	if err := Apply(context.Background(), root, body, sig, "1.0.0", strings.NewReader(string(b)), r, probe); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(root, "current")); err != nil || target != "1.2.3" || r.calls != 1 {
		t.Fatal(target, err, r.calls)
	}
}

func TestVerifyRequiresCanonicalFieldNames(t *testing.T) {
	original := releasePublicKeyBase64
	t.Cleanup(func() { releasePublicKeyBase64 = original })
	body := strings.Replace(goodManifest, `"security_fix":true`, `"security_fix":true,"notes":"release"`, 1)
	for _, field := range []string{"version", "security_fix", "notes", "artifacts", "os", "arch", "filename", "sha256", "size", "url"} {
		for _, alias := range []string{strings.ToUpper(field), strings.ToUpper(field[:1]) + field[1:]} {
			for _, duplicate := range []bool{false, true} {
				name := alias + "/alias-only"
				if duplicate {
					name = alias + "/mixed-duplicate"
				}
				t.Run(name, func(t *testing.T) {
					key := `"` + field + `":`
					replacement := `"` + alias + `":`
					if duplicate {
						// Repeat the same value so rejection cannot depend on value validation.
						start := strings.Index(body, key) + len(key)
						end := strings.IndexAny(body[start:], ",}") + start
						if field == "artifacts" {
							end = len(body) - 1
						}
						replacement = key + body[start:end] + "," + replacement
					}
					mutated := strings.Replace(body, key, replacement, 1)
					pub, sig := signed(t, mutated)
					releasePublicKeyBase64 = base64.StdEncoding.EncodeToString(pub)
					_, _, err := Verify([]byte(mutated), sig, "dev")
					code(t, err, "invalid_argument")
				})
			}
		}
	}
	for _, mutation := range []struct{ old, next string }{
		{`"version":"1.2.3"`, `"version":"1.2.3","Version":"9.0.0"`},
		{`"version":"1.2.3"`, `"version":"1.2.3","ver\u0073ion":"9.0.0"`},
		{`"size":3`, `"size":3,"si\u007ae":4`},
		{`"size":3`, `"size":3,"unknown":4`},
	} {
		mutated := strings.Replace(body, mutation.old, mutation.next, 1)
		pub, sig := signed(t, mutated)
		releasePublicKeyBase64 = base64.StdEncoding.EncodeToString(pub)
		_, _, err := Verify([]byte(mutated), sig, "dev")
		code(t, err, "invalid_argument")
	}
	// Escapes that decode to the canonical spelling remain valid JSON field names.
	canonical := strings.Replace(body, `"version"`, `"ver\u0073ion"`, 1)
	pub, sig := signed(t, canonical)
	releasePublicKeyBase64 = base64.StdEncoding.EncodeToString(pub)
	if _, _, err := Verify([]byte(canonical), sig, "dev"); err != nil {
		t.Fatal(err)
	}
	_, _, err := Verify([]byte(`{"Version":`), sig, "dev")
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("must verify signature before parsing: %v", err)
	}
}
