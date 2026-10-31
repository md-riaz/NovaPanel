package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func sealedInput(t *testing.T, body []byte) *os.File {
	t.Helper()
	fd, err := unix.MemfdCreate("update-test", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(fd), "update-test")
	t.Cleanup(func() { f.Close() })
	if _, err = f.Write(body); err != nil {
		t.Fatal(err)
	}
	if _, err = unix.FcntlInt(f.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_SEAL|unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPrepareHelperSealedCustody(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0755)
	os.Mkdir(filepath.Join(root, "1.0.0"), 0755)
	for _, n := range []string{"panel-agent", "panel-web"} {
		os.WriteFile(filepath.Join(root, "1.0.0", n), []byte("old"), 0755)
	}
	os.Symlink("1.0.0", filepath.Join(root, "current"))
	b := tarBytes(t, binaryHeaders())
	sum := sha256.Sum256(b)
	body, err := json.Marshal(Manifest{Version: "1.2.3", Artifacts: []Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Filename: "panel.tar", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(b)), URL: "https://example.test/panel.tar"}}})
	if err != nil {
		t.Fatal(err)
	}
	pub, sig := signed(t, string(body))
	manifest, signature, bundle := sealedInput(t, body), sealedInput(t, sig), sealedInput(t, b)
	// Section readers must not depend on inherited offsets, which are at EOF.
	got, err := prepareHelper(context.Background(), pub, root, manifest, signature, bundle)
	if err != nil || got.Current != "1.0.0" || got.Candidate != "1.2.3" {
		t.Fatal(got, err)
	}
	if _, err = bundle.WriteAt([]byte("x"), 0); err == nil {
		t.Fatal("sealed bytes changed")
	}
	if _, err = prepareHelper(context.Background(), pub, root, sealedInput(t, append(bytes.Clone(body), ' ')), signature, nil); err == nil {
		t.Fatal("accepted unauthenticated bytes")
	}
	ordinary, err := os.CreateTemp(t.TempDir(), "mutable")
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.Close()
	ordinary.Write(b)
	if _, err = prepareHelper(context.Background(), pub, root, manifest, signature, ordinary); err == nil {
		t.Fatal("accepted mutable bundle")
	}
	if _, err = prepareHelper(context.Background(), pub, root, manifest, signature, sealedInput(t, bytes.Repeat([]byte("x"), len(b)))); err == nil {
		t.Fatal("accepted wrong digest")
	}
	if _, err = os.Stat(filepath.Join(root, "1.2.3")); !os.IsNotExist(err) {
		t.Fatal("prepare installed candidate", err)
	}
}

func TestPrepareHelperTrustBeforeInputs(t *testing.T) {
	_, err := PrepareHelper(context.Background(), "/missing", nil, nil, nil)
	code(t, err, "unsupported")
}

func TestHelperVersionLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	v := filepath.Join(root, "1.0.0")
	if err := os.Mkdir(v, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"panel-agent", "panel-web"} {
		if err := os.WriteFile(filepath.Join(v, name), []byte("binary"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("1.0.0", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if got, err := ValidateHelperLayout(root); err != nil || got != "1.0.0" {
		t.Fatal(got, err)
	}
	if err := os.Chmod(filepath.Join(v, "panel-web"), 0775); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateHelperLayout(root); err == nil {
		t.Fatal("accepted writable web binary")
	}
}
