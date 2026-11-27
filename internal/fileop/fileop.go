package fileop

import (
	"bufio"
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
const MaxWrite = 512 << 10
const MaxUpload = 100 << 20

// MaxEntries bounds both directory memory and helper output.
const MaxEntries = 512

type Request struct {
	Base, Name, Operation, Path string
	From, To                    string
	Offset, Length              int
	Mode                        uint32
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
	// Linux permits backslashes in names, but files.* deliberately rejects them.
	if strings.ContainsAny(p, "\\\x00") || path.IsAbs(p) {
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
	reader := bufio.NewReaderSize(in, 65537)
	header, err := reader.ReadSlice('\n')
	if err != nil || len(header) > 65536 {
		return errors.New("invalid file operation header")
	}
	var req Request
	if err := json.Unmarshal(header, &req); err != nil {
		return err
	}
	var content []byte
	if req.Operation == "write" {
		if req.Length < 0 || req.Length > MaxWrite {
			return json.NewEncoder(out).Encode(Response{Code: "invalid_argument"})
		}
		content = make([]byte, req.Length)
		if _, err := io.ReadFull(reader, content); err != nil {
			return err
		}
	}
	var res Response
	if req.Operation == "upload" {
		err = upload(req, reader)
	} else {
		res, err = apply(req, content)
	}
	if err != nil {
		res = Response{Code: errorCode(err)}
	}
	return json.NewEncoder(out).Encode(res)
}

func errorCode(err error) string {
	if errors.Is(err, errForeignStage) {
		return "precondition_failed"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "not_found"
	}
	if errors.Is(err, unix.EEXIST) || errors.Is(err, unix.ENOTEMPTY) {
		return "conflict"
	}
	var pe *os.PathError
	if errors.As(err, &pe) && pe.Err.Error() == "path escapes from parent" {
		return "invalid_argument"
	}
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EINVAL) {
		return "invalid_argument"
	}
	return "agent_error"
}

func Apply(req Request) (Response, error) {
	return apply(req, nil)
}

func apply(req Request, content []byte) (Response, error) {
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
	if req.Operation != "read" && req.Operation != "list" {
		return res, applyWrite(root, req, content)
	}
	f, err := root.OpenFile(p, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		var pe *os.PathError
		// os.Root's escape error is unexported, so recognize it only at the confined open.
		if errors.As(err, &pe) && pe.Err.Error() == "path escapes from parent" {
			return res, unix.EINVAL
		}
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

func upload(req Request, content io.Reader) error {
	if req.Length < 0 || req.Length > MaxUpload || ValidatePath(req.Path) != nil || path.Clean(req.Path) == "." {
		return unix.EINVAL
	}
	root, err := OpenDocroot(req.Base, req.Name)
	if err != nil {
		return err
	}
	defer root.Close()
	return place(root, path.Clean(req.Path), content, int64(req.Length), true)
}
