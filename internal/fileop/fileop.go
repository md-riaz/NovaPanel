package fileop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

const MaxChunk = 512 << 10

// MaxEntries bounds both directory memory and helper output.
const MaxEntries = 512

type Request struct {
	Base, Name, Operation, Path string
	Offset, Length              int
}
type Entry struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`
	Mtime int64  `json:"mtime"`
}
type Response struct {
	Entries []Entry `json:"entries,omitempty"`
	Content []byte  `json:"content_b64,omitempty"`
	Size    int64   `json:"size,omitempty"`
	EOF     bool    `json:"eof,omitempty"`
	Code    string  `json:"code,omitempty"`
}

// ValidatePath rejects traversal components even when they could resolve inside the root.
func ValidatePath(p string) error {
	if strings.ContainsRune(p, 0) || path.IsAbs(p) {
		return errors.New("invalid path")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return errors.New("invalid path")
		}
	}
	return nil
}

// OpenDocroot anchors the root without following either site-controlled component.
func OpenDocroot(base, name string) (*os.Root, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return nil, errors.New("invalid site name")
	}
	fd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, component := range []string{name, "public"} {
		next, e := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	defer unix.Close(fd)
	return os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", fd))
}

func Helper(in io.Reader, out io.Writer) error {
	if os.Geteuid() == 0 {
		return errors.New("refusing root helper")
	}
	var req Request
	if err := json.NewDecoder(io.LimitReader(in, 65536)).Decode(&req); err != nil {
		return err
	}
	res, err := Apply(req)
	if err != nil {
		res = Response{Code: "agent_error"}
		if errors.Is(err, os.ErrNotExist) {
			res.Code = "not_found"
		} else if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EINVAL) {
			res.Code = "invalid_argument"
		}
	}
	return json.NewEncoder(out).Encode(res)
}

func Apply(req Request) (Response, error) {
	var res Response
	if ValidatePath(req.Path) != nil || req.Offset < 0 || req.Length < 0 {
		return res, unix.EINVAL
	}
	root, err := OpenDocroot(req.Base, req.Name)
	if err != nil {
		return res, err
	}
	defer root.Close()
	p := path.Clean(req.Path)
	f, err := root.OpenFile(p, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return res, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return res, err
	}
	switch req.Operation {
	case "list":
		if !info.IsDir() {
			return res, unix.EINVAL
		}
		entries, err := f.ReadDir(MaxEntries + 1)
		if err != nil && err != io.EOF {
			return res, err
		}
		if len(entries) > MaxEntries {
			return res, unix.EINVAL
		}
		res.Entries = make([]Entry, 0, len(entries))
		for _, e := range entries {
			i, err := e.Info()
			if err != nil {
				return Response{}, err
			}
			typ := "other"
			switch {
			case i.Mode().IsRegular():
				typ = "file"
			case i.IsDir():
				typ = "dir"
			case i.Mode()&os.ModeSymlink != 0:
				typ = "symlink"
			}
			res.Entries = append(res.Entries, Entry{e.Name(), typ, i.Size(), fmt.Sprintf("%04o", i.Mode().Perm()), i.ModTime().Unix()})
		}
	case "read":
		if !info.Mode().IsRegular() {
			return res, unix.EINVAL
		}
		n := req.Length
		if n == 0 || n > MaxChunk {
			n = MaxChunk
		}
		data := make([]byte, n)
		read, err := f.ReadAt(data, int64(req.Offset))
		if err != nil && err != io.EOF {
			return res, err
		}
		res.Content = data[:read]
		res.Size = info.Size()
		res.EOF = int64(req.Offset) >= res.Size || int64(read) >= res.Size-int64(req.Offset)
	default:
		return res, unix.EINVAL
	}
	return res, nil
}
