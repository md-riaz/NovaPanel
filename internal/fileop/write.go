package fileop

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

func ValidMode(m uint32) bool {
	switch m {
	case 0644, 0640, 0600, 0755, 0750, 0700:
		return true
	}
	return false
}

// leaf opens the final component without following it, including for unreadable files.
func leaf(root *os.Root, p string) (*os.File, os.FileInfo, error) {
	f, err := root.OpenFile(p, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	i, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if i.Mode()&os.ModeSymlink != 0 {
		f.Close()
		return nil, nil, unix.ELOOP
	}
	return f, i, nil
}

func applyWrite(root *os.Root, req Request, content []byte) error {
	p := path.Clean(req.Path)
	if p == "." && req.Operation != "move" {
		return unix.EINVAL
	}
	switch req.Operation {
	case "mkdir":
		err := root.Mkdir(p, 0755)
		if errors.Is(err, os.ErrExist) {
			f, _, e := leaf(root, p)
			if e == nil {
				f.Close()
			} else if errors.Is(e, unix.ELOOP) {
				return e
			}
		}
		return err
	case "delete":
		return root.Remove(p)
	case "chmod":
		if !ValidMode(req.Mode) {
			return unix.EINVAL
		}
		f, _, err := leaf(root, p)
		if err != nil {
			return err
		}
		defer f.Close()
		return unix.Fchmodat(int(f.Fd()), "", req.Mode, unix.AT_EMPTY_PATH)
	case "write":
		if len(content) > MaxWrite {
			return unix.EINVAL
		}
		parent, err := root.OpenRoot(path.Dir(p))
		if err != nil {
			return err
		}
		defer parent.Close()
		name := path.Base(p)
		f, info, err := leaf(parent, name)
		exists := err == nil
		if exists {
			f.Close()
			if !info.Mode().IsRegular() {
				return unix.EINVAL
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return err
		}
		tmp := ".novapanel-" + hex.EncodeToString(random[:])
		f, err = parent.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0644)
		if err != nil {
			return err
		}
		defer parent.Remove(tmp)
		if err = f.Chmod(0644); err == nil {
			_, err = f.Write(content)
		}
		if e := f.Close(); err == nil {
			err = e
		}
		if err != nil {
			return err
		}
		if exists {
			return parent.Rename(tmp, name)
		}
		dir, err := parent.Open(".")
		if err != nil {
			return err
		}
		defer dir.Close()
		return unix.Renameat2(int(dir.Fd()), tmp, int(dir.Fd()), name, unix.RENAME_NOREPLACE)
	case "move":
		if ValidatePath(req.From) != nil || ValidatePath(req.To) != nil || path.Clean(req.From) == "." || path.Clean(req.To) == "." {
			return unix.EINVAL
		}
		from, err := root.OpenRoot(path.Dir(path.Clean(req.From)))
		if err != nil {
			return err
		}
		defer from.Close()
		to, err := root.OpenRoot(path.Dir(path.Clean(req.To)))
		if err != nil {
			return err
		}
		defer to.Close()
		f, _, err := leaf(from, path.Base(path.Clean(req.From)))
		if err != nil {
			return err
		}
		f.Close()
		a, err := from.Open(".")
		if err != nil {
			return err
		}
		defer a.Close()
		b, err := to.Open(".")
		if err != nil {
			return err
		}
		defer b.Close()
		return unix.Renameat2(int(a.Fd()), path.Base(path.Clean(req.From)), int(b.Fd()), path.Base(path.Clean(req.To)), unix.RENAME_NOREPLACE)
	}
	return unix.EINVAL
}
