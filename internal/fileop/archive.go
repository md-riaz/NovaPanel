package fileop

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"novapanel/internal/id"
	"novapanel/internal/validate"

	"golang.org/x/sys/unix"
)

const archiveEntries = 10000
const archiveFileBytes = 32 << 20
const archiveTotalBytes = 256 << 20

type ArchiveRequest struct {
	Base, Name, Operation string
	Paths                 []string
	Archive, Dest         string
	JobID                 string
}

func ArchiveHelper(in io.Reader, out io.Writer) error {
	if os.Geteuid() == 0 {
		return errors.New("refusing root helper")
	}
	var req ArchiveRequest
	if err := json.NewDecoder(io.LimitReader(in, 65536)).Decode(&req); err != nil {
		return err
	}
	if !id.Valid(req.JobID) {
		return json.NewEncoder(out).Encode(Response{Code: "invalid_argument"})
	}
	root, err := OpenDocroot(req.Base, req.Name)
	if err == nil {
		defer root.Close()
		switch req.Operation {
		case "archive":
			err = archive(root, req)
		case "extract":
			err = extract(root, req)
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

func extract(root *os.Root, req ArchiveRequest) error {
	if req.Archive == "" || req.Dest == "" || ValidatePath(req.Archive) != nil || ValidatePath(req.Dest) != nil || path.Clean(req.Dest) == "." {
		return unix.EINVAL
	}
	tmp, err := stageName(req)
	if err != nil {
		return err
	}
	if req.JobID != "" {
		if err = root.RemoveAll(tmp); err != nil {
			return err
		}
	}
	f, err := openArchiveSource(root, path.Clean(req.Archive))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > archiveTotalBytes {
		return unix.EINVAL
	}
	z, err := zip.NewReader(f, info.Size())
	if err != nil {
		return unix.EINVAL
	}
	if len(z.File) > archiveEntries {
		return unix.EINVAL
	}
	if err = root.Mkdir(tmp, 0700); err != nil {
		return err
	}
	defer root.RemoveAll(tmp)
	stageInfo, err := root.Lstat(tmp)
	if err != nil {
		return err
	}
	stageDir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer stageDir.Close()
	top := fmt.Sprintf("/proc/self/fd/%d/%s", stageDir.Fd(), tmp)
	var total uint64
	seen := map[string]bool{}
	for _, entry := range z.File {
		typ := entry.Mode().Type()
		if typ != 0 && typ != os.ModeDir {
			return unix.EINVAL
		}
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "" || name == "." || ValidatePath(name) != nil || (&validate.V{}).ArchiveEntry(top, name) != nil {
			return unix.EINVAL
		}
		name = path.Clean(name)
		if seen[name] {
			return unix.EINVAL
		}
		seen[name] = true
		if entry.UncompressedSize64 > archiveFileBytes || entry.UncompressedSize64 > archiveTotalBytes-total {
			return unix.EINVAL
		}
		total += entry.UncompressedSize64
	}
	for _, entry := range z.File {
		name := path.Clean(strings.TrimSuffix(entry.Name, "/"))
		if err = sameStage(root, tmp, stageInfo); err != nil {
			return err
		}
		if entry.Mode().IsDir() {
			if err = root.MkdirAll(path.Join(tmp, name), 0755); err != nil {
				return err
			}
			continue
		}
		if err = root.MkdirAll(path.Join(tmp, path.Dir(name)), 0755); err != nil {
			return err
		}
		out, e := root.OpenFile(path.Join(tmp, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
		if e != nil {
			return e
		}
		in, e := entry.Open()
		if e != nil {
			out.Close()
			return unix.EINVAL
		}
		n, e := io.Copy(out, io.LimitReader(in, archiveFileBytes+1))
		closeIn := in.Close()
		if e == nil {
			e = out.Chmod(0644)
		}
		closeOut := out.Close()
		if e != nil || closeIn != nil || closeOut != nil || n > archiveFileBytes || uint64(n) != entry.UncompressedSize64 {
			return unix.EINVAL
		}
	}
	if err = sameStage(root, tmp, stageInfo); err != nil {
		return err
	}
	if err = root.Chmod(tmp, 0755); err != nil {
		return err
	}
	// shortcut: a crash after publication replays as conflict, use a journal if exact completion is required.
	return publishArchive(root, tmp, path.Clean(req.Dest))
}

func sameStage(root *os.Root, name string, original os.FileInfo) error {
	current, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !current.IsDir() || !os.SameFile(original, current) {
		return unix.EINVAL
	}
	return nil
}

func publishArchive(root *os.Root, src, dest string) error {
	if _, err := root.Lstat(dest); err == nil {
		return unix.EEXIST
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// shortcut: a site user can race the no-replace precheck, use an atomic confined rename if available.
	return root.Rename(src, dest)
}

func stageName(req ArchiveRequest) (string, error) {
	if req.JobID == "" {
		return tempName()
	}
	if !id.Valid(req.JobID) || (req.Operation != "archive" && req.Operation != "extract") {
		return "", unix.EINVAL
	}
	return ".novapanel-" + req.Operation + "-" + req.JobID, nil
}

func tempName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return ".novapanel-" + hex.EncodeToString(b[:]), nil
}

func openArchiveSource(root *os.Root, name string) (*os.File, error) {
	current, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { current.Close() }()
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		fd, err := unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		current.Close()
		current = os.NewFile(uintptr(fd), part)
	}
	fd, err := unix.Openat(int(current.Fd()), parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func archive(root *os.Root, req ArchiveRequest) error {
	if req.Dest == "" || ValidatePath(req.Dest) != nil || path.Clean(req.Dest) == "." || len(req.Paths) == 0 || len(req.Paths) > archiveEntries {
		return unix.EINVAL
	}
	tmp, err := stageName(req)
	if err != nil {
		return err
	}
	if req.JobID != "" {
		if err = root.RemoveAll(tmp); err != nil {
			return err
		}
	}
	if err = root.Mkdir(tmp, 0700); err != nil {
		return err
	}
	defer root.RemoveAll(tmp)
	stageInfo, err := root.Lstat(tmp)
	if err != nil {
		return err
	}
	f, err := root.OpenFile(path.Join(tmp, "data"), os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := zip.NewWriter(f)
	count, total := 0, int64(0)
	seen := map[string]bool{}
	tempPath := tmp
	var add func(string) error
	add = func(name string) error {
		if ValidatePath(name) != nil || name == "" {
			return unix.EINVAL
		}
		name = path.Clean(name)
		if name == tempPath {
			return nil
		}
		file, err := openArchiveSource(root, name)
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
		if seen[name] {
			return unix.EINVAL
		}
		seen[name] = true
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
	// shortcut: a crash after publication replays as conflict, use a journal if exact completion is required.
	if err = sameStage(root, tmp, stageInfo); err != nil {
		return err
	}
	return publishArchive(root, path.Join(tmp, "data"), path.Clean(req.Dest))
}
