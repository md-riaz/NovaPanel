package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyCannotCommitAfterHealthDeadline(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "1.0.0"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("1.0.0", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	body := tarBytes(t, binaryHeaders())
	sum := sha256.Sum256(body)
	a := Artifact{Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &fakeRunner{}
	err := apply(ctx, root, "1.2.3", a, bytes.NewReader(body), r, func(context.Context) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expired health result accepted: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(root, "current")); err != nil || target != "1.0.0" || r.calls != 2 {
		t.Fatalf("rollback target=%q restarts=%d err=%v", target, r.calls, err)
	}
}
