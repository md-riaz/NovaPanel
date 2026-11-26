package fileop

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

const archiveEntries = 10000
const archiveFileBytes = 32 << 20
const archiveTotalBytes = 256 << 20

type ArchiveRequest struct {
	Base, Name, Operation string
	Paths                 []string
	Archive, Dest         string
}

func ArchiveHelper(in io.Reader, out io.Writer) error {
	if os.Geteuid() == 0 {
		return errors.New("refusing root helper")
	}
	var req ArchiveRequest
	if err := json.NewDecoder(io.LimitReader(in, 65536)).Decode(&req); err != nil {
		return err
	}
	root, err := OpenDocroot(req.Base, req.Name)
	if err == nil {
		defer root.Close()
		switch req.Operation {
		case "archive":
			err = archive(root, req)
		default:
			err = unix.EINVAL
		}
	}
	res := Response{}
	if err != nil {
		res.Code = errorCode(err)
	}
	return json.NewEncoder(out).Encode(res)
}

func tempName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return ".novapanel-" + hex.EncodeToString(b[:]), nil
}

func archive(root *os.Root, req ArchiveRequest) error {
	if req.Dest == "" || ValidatePath(req.Dest) != nil || path.Clean(req.Dest) == "." || len(req.Paths) == 0 || len(req.Paths) > archiveEntries {
		return unix.EINVAL
	}
	parent, err := root.OpenRoot(path.Dir(path.Clean(req.Dest)))
	if err != nil {
		return err
	}
	defer parent.Close()
	dir, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	tmp, err := tempName()
	if err != nil {
		return err
	}
	f, err := parent.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer parent.Remove(tmp)
	defer f.Close()
	w := zip.NewWriter(f)
	count, total := 0, int64(0)
	var add func(string) error
	add = func(name string) error {
		if ValidatePath(name) != nil || name == "" {
			return unix.EINVAL
		}
		name = path.Clean(name)
		file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if errors.Is(err, unix.ELOOP) {
			return nil
		}
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		count++
		if count > archiveEntries {
			return unix.EINVAL
		}
		if info.IsDir() {
			if name != "." {
				if _, err := w.Create(strings.TrimSuffix(name, "/") + "/"); err != nil {
					return err
				}
			}
			entries, err := file.ReadDir(archiveEntries + 1)
			if err != nil && err != io.EOF {
				return err
			}
			if len(entries) > archiveEntries {
				return unix.EINVAL
			}
			for _, entry := range entries {
				if err := add(path.Join(name, entry.Name())); err != nil {
					return err
				}
			}
			return nil
		}
		if info.Size() > archiveFileBytes || info.Size() < 0 {
			return unix.EINVAL
		}
		out, err := w.Create(name)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(file, archiveFileBytes+1))
		total += n
		if err != nil {
			return err
		}
		if n > archiveFileBytes || total > archiveTotalBytes {
			return unix.EINVAL
		}
		return nil
	}
	for _, p := range req.Paths {
		if err := add(p); err != nil {
			w.Close()
			return err
		}
	}
	if err = w.Close(); err != nil {
		return err
	}
	if err = f.Chmod(0644); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return unix.Renameat2(int(dir.Fd()), tmp, int(dir.Fd()), path.Base(path.Clean(req.Dest)), unix.RENAME_NOREPLACE)
}
