package update

import (
	"context"
	"crypto/ed25519"
	"io"
	"novapanel/internal/exec"
	"runtime"
)

// Apply verifies the manifest again at the independent helper seam. The artifact
// reader and private root belong to the helper, not to RPC request arguments.
// No daemon restart may execute this function in the daemon's process.
func Apply(ctx context.Context, root string, body, signature []byte, installed string, artifact io.Reader, runner exec.Runner, ping func(context.Context) error) error {
	key, err := releaseKey()
	if err != nil {
		return err
	}
	return applySigned(ctx, key, root, body, signature, installed, artifact, runner, ping)
}
func applySigned(ctx context.Context, key ed25519.PublicKey, root string, body, signature []byte, installed string, artifact io.Reader, runner exec.Runner, ping func(context.Context) error) error {
	m, a, err := verifyManifest(key, body, signature, installed, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	return apply(ctx, root, m.Version, a, artifact, runner, ping)
}

// Recover rolls an interrupted update back using only retained local binaries.
// Call from an independent helper with exclusive installation-directory ownership.
func Recover(ctx context.Context, root string, runner exec.Runner) error {
	if err := Enabled(); err != nil {
		return err
	}
	return recoverApply(ctx, root, runner)
}
