package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestArtifactIntegrity(t *testing.T) {
	sum := sha256.Sum256([]byte("abc"))
	a := Artifact{Size: 3, SHA256: hex.EncodeToString(sum[:])}
	if err := verifyArtifact(context.Background(), a, strings.NewReader("abc"), io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"ab", "abcd", "abd"} {
		code(t, verifyArtifact(context.Background(), a, strings.NewReader(s), io.Discard), "invalid_argument")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(verifyArtifact(ctx, a, strings.NewReader("abc"), io.Discard), context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	if err := verifyArtifact(context.Background(), a, strings.NewReader("abc"), brokenWriter{}); err == nil {
		t.Fatal("write error ignored")
	}
	var out bytes.Buffer
	code(t, verifyArtifact(context.Background(), Artifact{Size: maxArtifactBytes + 1}, strings.NewReader("abc"), &out), "invalid_argument")
	if out.Len() != 0 {
		t.Fatal("wrote invalid artifact")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
