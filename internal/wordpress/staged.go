package wordpress

import (
	"archive/zip"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
)

// StagedPaths is trusted local installation configuration, never RPC input.
// This preflight does not provision, extract, execute PHP or authorize execution.
type StagedPaths struct{ CLI, Signature, Keyring, Archive, Checksums, VerifierHome string }

var errStaged = errors.New("wordpress: staged artifact preflight refused")

// VerifyStaged hashes actual held bytes and verifies the detached signature.
// No caller-supplied evidence, pins, ownership exemption or executable is accepted.
// The trusted runner is the existing process boundary, not a client attestation.
// A result is metadata only: a later consumer must retain/reverify its own bytes.
func VerifyStaged(ctx context.Context, p StagedPaths, r runner.Runner) (ArtifactEvidence, error) {
	if os.Geteuid() != 0 {
		return ArtifactEvidence{}, errStaged
	}
	return verifyStaged(ctx, p, r, ApprovedArtifacts(), 0, false)
}

func verifyStaged(ctx context.Context, p StagedPaths, r runner.Runner, m Manifest, owner uint32, fixture bool) (ArtifactEvidence, error) {
	return consumeStaged(ctx, p, r, m, owner, fixture, nil)
}

// consume is private and runs before verified descriptors close. No exported
// callback can turn metadata evidence into a substitute for held bytes.
func consumeStaged(ctx context.Context, p StagedPaths, r runner.Runner, m Manifest, owner uint32, fixture bool, consume func(context.Context, *os.File, ArtifactEvidence) error) (e ArtifactEvidence, err error) {
	defer func() {
		if err != nil {
			e = ArtifactEvidence{}
			if ctx.Err() != nil {
				err = ctx.Err()
			} else {
				err = errStaged
			}
		}
	}()
	if r == nil || ctx.Err() != nil {
		return e, errStaged
	}
	budget := 30 * time.Second
	if consume != nil {
		// Full extraction includes bounded archive hashing, thousands of file
		// syncs and restart comparisons. Signature-only preflight stays short.
		budget = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var held []*os.File
	defer func() {
		for _, f := range held {
			_ = f.Close()
		}
	}()
	open := func(p string, dir bool) (*os.File, error) {
		f, err := openStaged(p, dir, owner, fixture)
		if err == nil {
			held = append(held, f)
		}
		return f, err
	}
	cli, err := open(p.CLI, false)
	if err != nil {
		return e, err
	}
	sig, err := open(p.Signature, false)
	if err != nil {
		return e, err
	}
	key, err := open(p.Keyring, false)
	if err != nil {
		return e, err
	}
	archive, err := open(p.Archive, false)
	if err != nil {
		return e, err
	}
	checksums, err := open(p.Checksums, false)
	if err != nil {
		return e, err
	}
	home, err := open(p.VerifierHome, true)
	if err != nil {
		return e, err
	}
	entries, err := home.ReadDir(1)
	if len(entries) != 0 || !errors.Is(err, io.EOF) {
		return e, errStaged
	}
	hash := func(f *os.File, limit int64, want string) (string, error) {
		st, err := f.Stat()
		if err != nil || st.Size() <= 0 || st.Size() > limit {
			return "", errStaged
		}
		h := sha256.New()
		n, err := io.Copy(h, io.NewSectionReader(f, 0, limit+1))
		if err != nil || n != st.Size() {
			return "", errStaged
		}
		got := hex.EncodeToString(h.Sum(nil))
		if got != want {
			return "", errStaged
		}
		return got, nil
	}
	e.CLISHA256, err = hash(cli, 32<<20, m.CLI.SHA256)
	if err != nil {
		return e, err
	}
	e.WordPressSHA256, err = hash(archive, 64<<20, m.WordPress.SHA256)
	if err != nil {
		return e, err
	}
	e.ChecksumsSHA256, err = hash(checksums, 1<<20, m.ChecksumsSHA256)
	if err != nil {
		return e, err
	}
	for _, f := range []*os.File{sig, key} {
		st, err := f.Stat()
		if err != nil || st.Size() <= 0 || st.Size() > 1<<20 {
			return e, errStaged
		}
	}
	if err = verifyStagedArchive(ctx, archive, checksums, m); err != nil {
		return e, err
	}
	fdpath := func(f *os.File) string { return fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), f.Fd()) }
	stdout, stderr, exit, runErr := r.Run(ctx, "/usr/bin/gpgv", []string{"--status-fd=1", "--homedir", fdpath(home), "--keyring", fdpath(key), fdpath(sig), fdpath(cli)}, runner.RunOpts{Dir: fdpath(home), Env: []string{"LANG=C", "PATH=/usr/bin:/bin"}, Groups: []uint32{}, MaxOutput: 16384})
	defer clear(stdout)
	defer clear(stderr)
	if runErr != nil || exit != 0 || ctx.Err() != nil || len(stdout) >= 16384 || !validStagedSignature(stdout, m.CLISignerFingerprint) {
		return e, errStaged
	}
	e.SignatureVerified = true
	e.SignerFingerprint = m.CLISignerFingerprint
	if consume != nil {
		if err := consume(ctx, archive, e); err != nil {
			return e, err
		}
	}
	return e, nil
}

// Every component is opened relative to the preceding no-follow descriptor.
// Fixture mode only relaxes ancestor custody; exported admission never enables it.
func openStaged(p string, directory bool, owner uint32, fixture bool) (*os.File, error) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
		return nil, errStaged
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), "/")
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		dir := i < len(parts)-1 || directory
		if dir {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(int(current.Fd()), part, flags, 0)
		_ = current.Close()
		if err != nil {
			return nil, err
		}
		current = os.NewFile(uintptr(next), part)
		var st unix.Stat_t
		if unix.Fstat(next, &st) != nil {
			current.Close()
			return nil, errStaged
		}
		good := st.Uid == owner && st.Mode&0022 == 0
		if fixture && i < len(parts)-1 {
			good = true
		}
		if i == len(parts)-1 {
			if directory {
				good = good && st.Mode&07777 == 0700
			} else {
				good = good && st.Mode&unix.S_IFMT == unix.S_IFREG && st.Mode&07777 == 0644 && st.Nlink == 1
			}
		}
		if !good {
			current.Close()
			return nil, errStaged
		}
	}
	return current, nil
}
func validStagedSignature(raw []byte, fingerprint string) bool {
	count := 0
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "[GNUPG:]" {
			continue
		}
		switch f[1] {
		case "VALIDSIG":
			if len(f) < 11 || f[2] != fingerprint {
				return false
			}
			count++
		case "BADSIG", "ERRSIG", "EXPSIG", "EXPKEYSIG", "REVKEYSIG", "NO_PUBKEY", "FAILURE", "NODATA":
			return false
		}
	}
	return count == 1
}
func verifyStagedArchive(ctx context.Context, archive, metadata *os.File, m Manifest) error {
	var data struct {
		Checksums map[string]string `json:"checksums"`
	}
	if json.NewDecoder(io.NewSectionReader(metadata, 0, 1<<20)).Decode(&data) != nil || len(data.Checksums) != m.ChecksumEntries {
		return errStaged
	}
	for name, sum := range data.Checksums {
		decoded, err := hex.DecodeString(sum)
		if !safeArchiveName(name) || err != nil || len(decoded) != md5.Size {
			return errStaged
		}
	}
	st, err := archive.Stat()
	if err != nil {
		return err
	}
	z, err := zip.NewReader(archive, st.Size())
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	directories := map[string]bool{}
	var total uint64
	version := false
	for _, f := range z.File {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !strings.HasPrefix(f.Name, "wordpress/") {
			return errStaged
		}
		name := strings.TrimPrefix(f.Name, "wordpress/")
		// ZIP Mode adds ModeDir for a trailing slash even on a symlink or
		// special entry. Check the complete type before classifying names.
		typ := f.Mode().Type()
		if typ != 0 && typ != os.ModeDir {
			return errStaged
		}
		if typ == os.ModeDir {
			if !strings.HasSuffix(f.Name, "/") {
				return errStaged
			}
			name = strings.TrimSuffix(name, "/")
			if (name == "" && f.Name != "wordpress/") || (name != "" && !safeArchiveName(name)) || directories[name] {
				return errStaged
			}
			// The empty relative name represents the explicit root record.
			directories[name] = true
			continue
		}
		if !safeArchiveName(name) || !f.Mode().IsRegular() || seen[name] || f.UncompressedSize64 > 32<<20 {
			return errStaged
		}
		want, ok := data.Checksums[name]
		if !ok {
			return errStaged
		}
		total += f.UncompressedSize64
		if total > 256<<20 {
			return errStaged
		}
		input, err := f.Open()
		if err != nil {
			return err
		}
		h := md5.New()
		var content []byte
		if name == "wp-includes/version.php" {
			content, err = io.ReadAll(io.LimitReader(input, 1<<20))
			if err == nil {
				_, err = h.Write(content)
			}
		} else {
			_, err = io.Copy(h, io.LimitReader(input, 32<<20+1))
		}
		closeErr := input.Close()
		if err != nil || closeErr != nil || hex.EncodeToString(h.Sum(nil)) != want {
			return errStaged
		}
		if name == "wp-includes/version.php" {
			version = strings.Contains(string(content), "$wp_version = '"+m.WordPress.Version+"';")
		}
		seen[name] = true
	}
	for name := range directories {
		if seen[name] {
			return errStaged
		}
		// Check explicit directories as well as files. This final pass is
		// independent of ZIP entry order and includes implicit ancestors.
		if name == "" {
			continue
		}
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return errStaged
			}
		}
	}
	for name := range seen {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return errStaged
			}
		}
	}
	if len(seen) != m.ChecksumEntries || !version {
		return errStaged
	}
	return nil
}
func safeArchiveName(name string) bool {
	return name != "" && name != "." && name != ".." && path.Clean(name) == name && !strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\\x00\r\n")
}
