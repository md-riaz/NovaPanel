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

func fixtureHelperLayout(root string) (string, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	defer unix.Close(fd)
	// Only fixtures substitute the owner and start at a held scratch directory.
	return validateHelperDirectory(fd, uint32(os.Getuid()))
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
	// Exercise sealed custody independently of privileged installation provisioning.
	prepare := func(ctx context.Context, key []byte, root string, manifest, signature, bundle *os.File) (HelperInspection, error) {
		return prepareHelperWithLayout(ctx, key, root, manifest, signature, bundle, fixtureHelperLayout)
	}
	got, err := prepare(context.Background(), pub, root, manifest, signature, bundle)
	if os.Getuid() != 0 {
		if _, err := prepareHelper(context.Background(), pub, root, manifest, signature, bundle); err == nil {
			t.Fatal("production accepted non-root installation")
		}
	}
	if err != nil || got.Current != "1.0.0" || got.Candidate != "1.2.3" {
		t.Fatal(got, err)
	}
	if _, err = bundle.WriteAt([]byte("x"), 0); err == nil {
		t.Fatal("sealed bytes changed")
	}
	if _, err = prepare(context.Background(), pub, root, sealedInput(t, append(bytes.Clone(body), ' ')), signature, nil); err == nil {
		t.Fatal("accepted unauthenticated bytes")
	}
	ordinary, err := os.CreateTemp(t.TempDir(), "mutable")
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.Close()
	ordinary.Write(b)
	if _, err = prepare(context.Background(), pub, root, manifest, signature, ordinary); err == nil {
		t.Fatal("accepted mutable bundle")
	}
	if _, err = prepare(context.Background(), pub, root, manifest, signature, sealedInput(t, bytes.Repeat([]byte("x"), len(b)))); err == nil {
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
	if got, err := fixtureHelperLayout(root); err != nil || got != "1.0.0" {
		t.Fatal(got, err)
	}

	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if _, err := validateHelperDirectory(fd, uint32(os.Getuid())^1); err == nil {
		t.Fatal("accepted wrong installation owner")
	}
	if os.Getuid() != 0 {
		if _, err := ValidateHelperLayout(root); err == nil {
			t.Fatal("production accepted non-root layout")
		}
		if _, err := validateHelperDirectory(fd, 0); err == nil {
			t.Fatal("production owner policy accepted non-root directory")
		}
	}
	if err := os.Chmod(filepath.Join(v, "panel-web"), 0775); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureHelperLayout(root); err == nil {
		t.Fatal("accepted writable web binary")
	}
}
