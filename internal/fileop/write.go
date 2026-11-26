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
		dir, err := parent.Open(".")
		if err != nil {
			return err
		}
		defer dir.Close()
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return err
		}
		tmp := ".novapanel-" + hex.EncodeToString(random[:])
		// Temp starts at 0600; only its site-user owner can access it before the final mode.
		// Its owner bits may briefly exceed that mode; group/other bits never do.
		f, err := parent.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		defer parent.Remove(tmp)
		defer f.Close()
		if _, err = f.Write(content); err != nil {
			return err
		}
		if err = f.Sync(); err != nil {
			return err
		}
		// Mode preservation applies to the file identified when overwrite begins.
		// A site shell user replacing the path during the write is outside that guarantee.
		// This cannot expose old bytes because the write replaces the content, and the
		// site lock prevents concurrent API writes.
		target, info, err := leaf(parent, name)
		exists := err == nil
		mode := os.FileMode(0644)
		if exists {
			target.Close()
			if !info.Mode().IsRegular() {
				return unix.EINVAL
			}
			mode = info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err = f.Chmod(mode); err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		if exists {
			return parent.Rename(tmp, name)
		}
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
