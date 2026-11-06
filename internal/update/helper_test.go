package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type forbiddenReader struct{ t *testing.T }

func (r forbiddenReader) Read([]byte) (int, error) {
	r.t.Fatal("artifact read before trust")
	return 0, io.EOF
}
func TestApplyMissingTrustHasNoEffects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	r := &fakeRunner{}
	code(t, Apply(context.Background(), root, []byte("bad JSON"), nil, "dev", forbiddenReader{t}, r, func(context.Context) error { t.Fatal("ping before trust"); return nil }), "unsupported")
	code(t, Recover(context.Background(), root, r), "unsupported")
	if _, err := os.Lstat(root); !os.IsNotExist(err) || r.calls != 0 {
		t.Fatal(err, r.calls)
	}
}
func TestSignedApplyAuthenticatesBeforeStaging(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "1.0.0"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("1.0.0", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	b := tarBytes(t, binaryHeaders())
	sum := sha256.Sum256(b)
	m := Manifest{Version: "1.2.3", Artifacts: []Artifact{{OS: "linux", Arch: "amd64", Filename: "panel.tar", Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:]), URL: "https://releases.example/panel.tar"}}}
	body, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	pub, sig := signed(t, string(body))
	r := &fakeRunner{}
	code(t, applySigned(context.Background(), pub, root, append(body, ' '), sig, "1.0.0", forbiddenReader{t}, r, func(context.Context) error { return nil }), "invalid_argument")
	if err := applySigned(context.Background(), pub, root, body, sig, "1.0.0", bytes.NewReader(b), r, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(filepath.Join(root, "current")); target != "1.2.3" {
		t.Fatal(target)
	}
}
func TestFailedRollbackKeepsRecoveryRecord(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"1.0.0", "1.2.3"} {
		if err := os.Mkdir(filepath.Join(root, v), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("1.2.3", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".update-pending"), []byte(`{"previous":"1.0.0","next":"1.2.3"}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{fail: true}
	if recoverApply(context.Background(), root, r) == nil {
		t.Fatal("restart failure hidden")
	}
	if _, err := os.Stat(filepath.Join(root, ".update-pending")); err != nil {
		t.Fatal("recovery record lost", err)
	}
	r.fail = false
	if err := recoverApply(context.Background(), root, r); err != nil {
		t.Fatal(err)
	}
}
func TestApplyCancellationRollsBack(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "1.0.0"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("1.0.0", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	b := tarBytes(t, binaryHeaders())
	sum := sha256.Sum256(b)
	a := Artifact{Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}
	r := &fakeRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := apply(ctx, root, "1.2.3", a, bytes.NewReader(b), r, func(context.Context) error { cancel(); return context.Canceled })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(filepath.Join(root, "current")); target != "1.0.0" || r.calls != 2 {
		t.Fatal(target, r.calls)
	}
}
