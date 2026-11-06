package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"novapanel/internal/exec"
	"os"
	"path/filepath"
	"testing"
)

type fakeRunner struct {
	calls int
	fail  bool
}

func (f *fakeRunner) Run(context.Context, string, []string, exec.RunOpts) ([]byte, []byte, int, error) {
	f.calls++
	if f.fail {
		return nil, nil, 1, errors.New("restart failed")
	}
	return nil, nil, 0, nil
}
func TestApplyInstallLock(t *testing.T) {
	root := t.TempDir()
	release, err := lockInstall(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	code(t, recoverApply(context.Background(), root, &fakeRunner{}), "conflict")
}

func TestApplyAndRollbackLocal(t *testing.T) {
	for _, unhealthy := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "rollback"}[unhealthy], func(t *testing.T) {
			root := t.TempDir()
			if e := os.Mkdir(filepath.Join(root, "1.0.0"), 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.Symlink("1.0.0", filepath.Join(root, "current")); e != nil {
				t.Fatal(e)
			}
			b := tarBytes(t, binaryHeaders())
			sum := sha256.Sum256(b)
			a := Artifact{Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}
			r := &fakeRunner{}
			probe := func(context.Context) error {
				if unhealthy {
					return errors.New("no ping")
				}
				return nil
			}
			err := apply(context.Background(), root, "1.2.3", a, bytes.NewReader(b), r, probe)
			target, e := os.Readlink(filepath.Join(root, "current"))
			if e != nil {
				t.Fatal(e)
			}
			if unhealthy {
				if err == nil || target != "1.0.0" || r.calls != 2 {
					t.Fatal(err, target, r.calls)
				}
			} else {
				if err != nil || target != "1.2.3" || r.calls != 1 {
					t.Fatal(err, target, r.calls)
				}
			}
			if _, err = os.Stat(filepath.Join(root, "1.0.0")); err != nil {
				t.Fatal("prior lost")
			}
		})
	}
}
func TestApplyInterruptedRecovery(t *testing.T) {
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
	r := &fakeRunner{}
	if err := recoverApply(context.Background(), root, r); err != nil {
		t.Fatal(err)
	}
	target, _ := os.Readlink(filepath.Join(root, "current"))
	if target != "1.0.0" || r.calls != 1 {
		t.Fatal(target, r.calls)
	}
	if err := recoverApply(context.Background(), root, r); err != nil || r.calls != 1 {
		t.Fatal("not idempotent", err, r.calls)
	}
}
