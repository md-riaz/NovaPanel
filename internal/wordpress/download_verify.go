package wordpress

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// VerifyDownloadedArchive validates the held site's cached tarball before use.
func VerifyDownloadedArchive(path string, uid uint32) error {
	return verifyDownloadedArchive(path, uid, ApprovedArtifacts().WordPress.SHA256)
}

func verifyDownloadedArchive(path string, uid uint32, expected string) error {
	errRefused := errors.New("wordpress: downloaded archive refused")
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return errRefused
	}
	f := os.NewFile(uintptr(fd), "wordpress archive")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uid || st.Nlink != 1 || st.Size <= 0 || st.Size > 64<<20 {
		return errRefused
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (64<<20)+1))
	if err != nil || n != st.Size || hex.EncodeToString(h.Sum(nil)) != expected {
		return errRefused
	}
	return nil
}
