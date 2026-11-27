package wordpress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
)

const (
	ToolPath          = "/usr/lib/novapanel/wpcli/wp-cli-2.12.0.phar"
	ToolSignaturePath = ToolPath + ".asc"
	ToolKeyringPath   = "/usr/lib/novapanel/wpcli/wp-cli-release.gpg"
	ToolINIPath       = "/usr/lib/novapanel/wpcli/php.ini"
	toolINIHash       = "cae981c2c37aaae8473b4a6616b51ee43f220b44bb7e2430b6a51187d0e45dd2"
	toolKeyringHash   = "fd1efb9e77bc9a2b6e207e0e731f5360d2bb0d83c8065383875ea60919c6cbf6"
)

// VerifyTool verifies held root-owned tool bytes, reviewed ini and release signature.
func VerifyTool(ctx context.Context, r runner.Runner) (ArtifactEvidence, error) {
	if err := verifyToolDirectories(); err != nil {
		return ArtifactEvidence{}, err
	}
	return verifyTool(ctx, r, [4]string{ToolPath, ToolSignaturePath, ToolKeyringPath, ToolINIPath}, 0, false)
}

func verifyToolDirectories() error {
	dir := filepath.Dir(ToolPath)
	for p := dir; ; p = filepath.Dir(p) {
		var st unix.Stat_t
		if unix.Lstat(p, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != 0 || st.Mode&0022 != 0 || st.Mode&0001 == 0 {
			return errStaged
		}
		for _, name := range []string{"wp-cli.yml", "wp-cli.local.yml"} {
			if _, err := os.Lstat(filepath.Join(p, name)); !os.IsNotExist(err) {
				return errStaged
			}
		}
		if p == "/" {
			break
		}
	}
	var st unix.Stat_t
	scan := filepath.Join(dir, "empty-ini")
	if unix.Lstat(scan, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != 0 || st.Mode&0022 != 0 || st.Mode&0005 != 0005 {
		return errStaged
	}
	entries, err := os.ReadDir(scan)
	if err != nil || len(entries) != 0 {
		return errStaged
	}
	return nil
}

func verifyTool(ctx context.Context, r runner.Runner, paths [4]string, owner uint32, fixture bool) (ArtifactEvidence, error) {
	if r == nil || ctx.Err() != nil {
		return ArtifactEvidence{}, errStaged
	}
	var files [4]*os.File
	for i, path := range paths {
		f, err := openStaged(path, false, owner, fixture)
		if err != nil {
			for _, h := range files {
				if h != nil {
					h.Close()
				}
			}
			return ArtifactEvidence{}, errStaged
		}
		files[i] = f
	}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if st, err := files[1].Stat(); err != nil || st.Size() <= 0 || st.Size() > 1<<20 {
		return ArtifactEvidence{}, errStaged
	}
	manifest := ApprovedArtifacts()
	for i, expected := range map[int]string{0: manifest.CLI.SHA256, 2: toolKeyringHash, 3: toolINIHash} {
		st, err := files[i].Stat()
		if err != nil || st.Size() <= 0 || st.Size() > 32<<20 {
			return ArtifactEvidence{}, errStaged
		}
		h := sha256.New()
		n, err := io.Copy(h, io.NewSectionReader(files[i], 0, st.Size()))
		if err != nil || n != st.Size() || hex.EncodeToString(h.Sum(nil)) != expected {
			return ArtifactEvidence{}, errStaged
		}
	}
	fdpath := func(f *os.File) string { return fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), f.Fd()) }
	out, _, code, err := r.Run(ctx, "/usr/bin/gpgv", []string{"--status-fd=1", "--keyring", fdpath(files[2]), fdpath(files[1]), fdpath(files[0])}, runner.RunOpts{Dir: "/", Env: []string{"LANG=C", "PATH=/usr/bin:/bin"}, Groups: []uint32{}, MaxOutput: 16384})
	if err != nil || code != 0 || ctx.Err() != nil || len(out) >= 16384 || !validStagedSignature(out, manifest.CLISignerFingerprint) {
		return ArtifactEvidence{}, errStaged
	}
	return ArtifactEvidence{CLISHA256: manifest.CLI.SHA256, WordPressSHA256: manifest.WordPress.SHA256, ChecksumsSHA256: manifest.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: manifest.CLISignerFingerprint}, nil
}
