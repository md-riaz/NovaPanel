package wordpress

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// extractTar operates only in an original job's held, non-serving docroot.
// Its caller must verify the held archive and run it as the reserved site UID.
func extractTar(ctx context.Context, archive io.Reader, root *os.Root) error {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	rootInfo, err := root.Stat(".")
	rootStat, ok := infoSys(rootInfo)
	if err != nil || !ok || rootStat.Uid != uint32(os.Geteuid()) {
		return errStaged
	}
	seen := map[string]bool{}
	var total int64
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil || count >= 20000 {
			return errStaged
		}
		name, err := validateTarEntry(h)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		if len(name) > 4096 || seen[name] {
			return errStaged
		}
		seen[name] = true
		total += h.Size
		if total > 512<<20 {
			return errStaged
		}
		for _, component := range strings.Split(name, "/") {
			if strings.HasPrefix(component, ".novapanel-tar-") {
				return errStaged
			}
		}
		parts := strings.Split(name, "/")
		parents := parts[:len(parts)-1]
		if h.Typeflag == tar.TypeDir {
			parents = parts
		}
		for i := range parents {
			dir := strings.Join(parents[:i+1], "/")
			if err := root.Mkdir(dir, 0755); err != nil && !errors.Is(err, os.ErrExist) {
				return errStaged
			}
			info, err := root.Lstat(dir)
			st, ok := infoSys(info)
			if err != nil || !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || st.Uid != uint32(os.Geteuid()) || st.Dev != rootStat.Dev {
				return errStaged
			}
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		sum := sha256.Sum256([]byte(name))
		tmp := path.Join(path.Dir(name), ".novapanel-tar-"+hex.EncodeToString(sum[:]))
		f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
		if err != nil {
			return errStaged
		}
		info, err := f.Stat()
		st, ok := infoSys(info)
		if err != nil || !ok || !info.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) || st.Dev != rootStat.Dev || st.Nlink != 1 {
			f.Close()
			return errStaged
		}
		if err = f.Truncate(0); err == nil {
			_, err = f.Seek(0, io.SeekStart)
		}
		if err == nil {
			var n int64
			n, err = io.CopyN(f, tr, h.Size)
			if err == nil && n != h.Size {
				err = io.ErrUnexpectedEOF
			}
		}
		if err == nil {
			err = f.Chmod(0644)
		}
		if err == nil {
			err = f.Sync()
		}
		if e := f.Close(); err == nil {
			err = e
		}
		if err != nil {
			return errStaged
		}
		if existing, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0); err == nil {
			fileInfo, e := existing.Stat()
			st, ok := infoSys(fileInfo)
			if e != nil || !ok || !fileInfo.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) || st.Dev != rootStat.Dev || st.Nlink != 1 || fileInfo.Size() != h.Size {
				existing.Close()
				return errStaged
			}
			candidate, e := root.Open(tmp)
			if e != nil {
				existing.Close()
				return errStaged
			}
			a, e1 := io.ReadAll(existing)
			b, e2 := io.ReadAll(candidate)
			existing.Close()
			candidate.Close()
			if e1 != nil || e2 != nil || !bytes.Equal(a, b) {
				return errStaged
			}
			if err = root.Remove(tmp); err != nil {
				return errStaged
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err = root.Rename(tmp, name); err != nil {
				return errStaged
			}
		} else {
			return errStaged
		}
	}
}

func infoSys(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return st, ok
}
