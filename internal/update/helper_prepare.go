package update

import (
	"context"
	"crypto/ed25519"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
	"novapanel/internal/rpcerr"
)

// HelperInstallRoot is packaging policy, never an RPC or environment parameter.
const HelperInstallRoot = "/opt/novapanel"

// HelperInspection contains no URLs, bytes, paths, or authority to restart services.
// Preparation is deliberately not an apply admission or durable job handoff.
type HelperInspection struct {
	Current   string `json:"current"`
	Candidate string `json:"candidate"`
}

// PrepareHelper accepts only Linux sealed memfds inherited from the launcher.
// Seals preserve exact-byte custody even if the launcher dies or retains a dup.
// The release anchor is checked before touching paths or descriptors.
func PrepareHelper(ctx context.Context, root string, manifest, signature, bundle *os.File) (HelperInspection, error) {
	key, err := releaseKey()
	if err != nil {
		return HelperInspection{}, err
	}
	return prepareHelper(ctx, key, root, manifest, signature, bundle)
}

func prepareHelper(ctx context.Context, key ed25519.PublicKey, root string, manifest, signature, bundle *os.File) (HelperInspection, error) {
	return prepareHelperWithLayout(ctx, key, root, manifest, signature, bundle, ValidateHelperLayout)
}

func prepareHelperWithLayout(ctx context.Context, key ed25519.PublicKey, root string, manifest, signature, bundle *os.File, layout func(string) (string, error)) (HelperInspection, error) {
	var out HelperInspection
	if err := ctx.Err(); err != nil {
		return out, err
	}
	body, err := sealedHelperBytes(manifest, maxManifestBytes)
	if err != nil {
		return out, err
	}
	sig, err := sealedHelperBytes(signature, 1024)
	if err != nil {
		return out, err
	}
	// Authentication happens before layout access and before bundle inspection.
	m, a, err := verifyManifest(key, body, sig, "", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return out, err
	}
	current, err := layout(root)
	if err != nil {
		return out, err
	}
	if _, _, err = verifyManifest(key, body, sig, current, runtime.GOOS, runtime.GOARCH); err != nil {
		return out, err
	}
	size, err := sealedHelperSize(bundle, maxArtifactBytes)
	if err != nil {
		return out, err
	}
	if size != a.Size {
		return out, helperInputError()
	}
	if err = verifyArtifact(ctx, a, io.NewSectionReader(bundle, 0, size), io.Discard); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	return HelperInspection{Current: current, Candidate: m.Version}, nil
}

func helperInputError() error {
	return rpcerr.Errf("precondition_failed", "unsafe helper input or installation layout")
}

func sealedHelperSize(f *os.File, limit int64) (int64, error) {
	if f == nil {
		return 0, helperInputError()
	}
	// F_GET_SEALS fails on ordinary files. A read-only descriptor alone does not
	// stop another process from rewriting or truncating the underlying inode.
	seals, err := unix.FcntlInt(f.Fd(), unix.F_GET_SEALS, 0)
	required := unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE
	if err != nil || seals&required != required {
		return 0, helperInputError()
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > limit {
		return 0, helperInputError()
	}
	return st.Size(), nil
}

func sealedHelperBytes(f *os.File, limit int64) ([]byte, error) {
	n, err := sealedHelperSize(f, limit)
	if err != nil {
		return nil, err
	}
	b := make([]byte, n)
	_, err = io.ReadFull(io.NewSectionReader(f, 0, n), b)
	return b, err
}

// ValidateHelperLayout checks a versioned installation without mutating it.
// Every ancestor must be root-owned, non-symlink and not group/world writable.
// Descriptor-relative traversal prevents a checked ancestor from being replaced
// with a symlink during the walk. Trusted root can still replace the installation;
// active apply will require a stable-root lifetime and exclusive ownership handoff.
func ValidateHelperLayout(root string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return "", helperInputError()
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", helperInputError()
	}
	defer func() { unix.Close(fd) }()
	for _, part := range strings.Split(strings.TrimPrefix(root, "/"), "/") {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return "", helperInputError()
		}
		unix.Close(fd)
		fd = next
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 {
			return "", helperInputError()
		}
	}
	return validateHelperDirectory(fd, 0)
}

// validateHelperDirectory checks the held installation directory. Production
// reaches it only after the root-owned ancestor walk above.
func validateHelperDirectory(fd int, owner uint32) (string, error) {
	var rootStat unix.Stat_t
	if unix.Fstat(fd, &rootStat) != nil || rootStat.Uid != owner || rootStat.Mode&0022 != 0 || rootStat.Mode&0005 != 0005 {
		return "", helperInputError()
	}
	target := make([]byte, 256)
	n, err := unix.Readlinkat(fd, "current", target)
	if err != nil || n == len(target) {
		return "", helperInputError()
	}
	current := string(target[:n])
	v, ok := parseVersion(current)
	if !ok || v.pre != "" || filepath.Base(current) != current {
		return "", helperInputError()
	}
	versionFD, err := unix.Openat(fd, current, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", helperInputError()
	}
	defer unix.Close(versionFD)
	var st unix.Stat_t
	if unix.Fstat(versionFD, &st) != nil || st.Uid != owner || st.Mode&0022 != 0 || st.Mode&0005 != 0005 {
		return "", helperInputError()
	}
	// The web unit must traverse the installation and execute its binary.
	if unix.Fstat(fd, &st) != nil || st.Mode&0005 != 0005 {
		return "", helperInputError()
	}
	for _, name := range []string{"panel-agent", "panel-web"} {
		b, e := unix.Openat(versionFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if e != nil {
			return "", helperInputError()
		}
		e = unix.Fstat(b, &st)
		unix.Close(b)
		if e != nil || st.Uid != owner || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07022 != 0 || st.Mode&0005 != 0005 {
			return "", helperInputError()
		}
	}
	return current, nil
}
