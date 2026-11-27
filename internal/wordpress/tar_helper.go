package wordpress

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// ErrDownloadRetry identifies a transport interruption, never verification failure.
var ErrDownloadRetry = errors.New("wordpress: download interrupted")

const ExtractRetryExit = 75

func transientDownload(err error) bool {
	if errors.Is(err, errStaged) {
		return false
	}
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, unix.ECONNRESET) || errors.Is(err, unix.ECONNREFUSED)
}

// ExtractFreshCore downloads and extracts as the already-confined site UID.
func ExtractFreshCore(ctx context.Context, docroot string) error {
	if os.Geteuid() == 0 || !filepath.IsAbs(docroot) || filepath.Clean(docroot) != docroot || filepath.Base(docroot) != "public" {
		return errStaged
	}
	home := filepath.Dir(docroot)
	workspace, err := os.OpenRoot(filepath.Join(home, ".novapanel-wpcli", "cache"))
	if err != nil {
		return errStaged
	}
	defer workspace.Close()
	if err := workspace.Mkdir("core", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return errStaged
	}
	info, err := workspace.Lstat("core")
	st, ok := infoSys(info)
	if err != nil || !ok || !info.IsDir() || st.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSymlink != 0 {
		return errStaged
	}
	core, err := workspace.OpenRoot("core")
	if err != nil {
		return errStaged
	}
	defer core.Close()
	m := ApprovedArtifacts()
	name := "wordpress-" + m.WordPress.Version + "-" + m.Locale + ".tar.gz"
	verify := func(f *os.File) error {
		info, err := f.Stat()
		st, ok := infoSys(info)
		if err != nil || !ok || !info.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || info.Size() <= 0 || info.Size() > 64<<20 {
			return errStaged
		}
		h := sha256.New()
		n, err := io.Copy(h, io.NewSectionReader(f, 0, info.Size()))
		if err != nil || n != info.Size() || hex.EncodeToString(h.Sum(nil)) != m.WordPress.SHA256 {
			return errStaged
		}
		return nil
	}
	archive, err := core.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		client := http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || req.URL.Hostname() != "wordpress.org" || len(via) > 3 {
				return errStaged
			}
			return nil
		}}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, m.WordPress.URL, nil)
		if e != nil {
			return errStaged
		}
		resp, e := client.Do(req)
		if e != nil {
			if transientDownload(e) {
				return ErrDownloadRetry
			}
			return errStaged
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return ErrDownloadRetry
		}
		if resp.StatusCode != http.StatusOK || resp.ContentLength > 64<<20 {
			return errStaged
		}
		tmp := ".novapanel-download.tmp"
		f, e := core.OpenFile(tmp, os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
		if e != nil {
			return errStaged
		}
		info, e := f.Stat()
		st, ok := infoSys(info)
		if e != nil || !ok || !info.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
			f.Close()
			return errStaged
		}
		if e = f.Truncate(0); e == nil {
			_, e = f.Seek(0, io.SeekStart)
		}
		if e == nil {
			_, e = io.Copy(f, io.LimitReader(resp.Body, (64<<20)+1))
			if transientDownload(e) {
				e = ErrDownloadRetry
			}
		}
		if e == nil {
			e = f.Sync()
		}
		if e == nil {
			e = verify(f)
		}
		if e2 := f.Close(); e == nil {
			e = e2
		}
		if e != nil {
			if errors.Is(e, ErrDownloadRetry) {
				return e
			}
			return errStaged
		}
		if e = core.Rename(tmp, name); e != nil {
			return errStaged
		}
		archive, err = core.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	}
	if err != nil {
		return errStaged
	}
	defer archive.Close()
	if err = verify(archive); err != nil {
		return err
	}
	fd, err := unix.Open(docroot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errStaged
	}
	dir := os.NewFile(uintptr(fd), "docroot")
	defer dir.Close()
	root, err := os.OpenRoot("/proc/self/fd/" + fmt.Sprint(fd))
	if err != nil {
		return errStaged
	}
	defer root.Close()
	return extractTar(ctx, archive, root)
}
