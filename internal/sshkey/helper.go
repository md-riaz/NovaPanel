package sshkey

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

type Request struct{ Home, SiteID, Operation, Value string }
type Response struct {
	Keys     []Key  `json:"keys,omitempty"`
	KeyID    string `json:"key_id,omitempty"`
	NotFound bool   `json:"not_found,omitempty"`
}

// Helper runs only after the process has started with site credentials.
func Helper(in io.Reader, out io.Writer) error {
	if os.Geteuid() == 0 {
		return errors.New("refusing root helper")
	}
	var req Request
	if err := json.NewDecoder(io.LimitReader(in, 32768)).Decode(&req); err != nil {
		return err
	}
	res, err := Apply(req)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(res)
}

func Apply(req Request) (Response, error) {
	var result Response
	home, err := unix.Open(req.Home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return result, err
	}
	defer unix.Close(home)
	if req.Operation == "add" {
		if err = unix.Mkdirat(home, ".ssh", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return result, err
		}
	}
	dir, err := unix.Openat(home, ".ssh", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) && req.Operation != "add" {
		result.NotFound = req.Operation == "remove"
		result.Keys = []Key{}
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer unix.Close(dir)
	if req.Operation == "add" || req.Operation == "remove" {
		if err = unix.Fchmod(dir, 0700); err != nil {
			return result, err
		}
	}
	fd, err := unix.Openat(dir, "authorized_keys", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	var data []byte
	var existingMode os.FileMode
	if err == nil {
		f := os.NewFile(uintptr(fd), "authorized_keys")
		info, e := f.Stat()
		if e != nil {
			f.Close()
			return result, e
		}
		if !info.Mode().IsRegular() {
			f.Close()
			return result, errors.New("authorized_keys is not a regular file")
		}
		existingMode = info.Mode().Perm()
		data, err = io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		if err != nil {
			return result, err
		}
		if len(data) > 1<<20 {
			return result, errors.New("authorized_keys too large")
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return result, err
	}
	var target ssh.PublicKey
	var comment string
	if req.Operation == "add" {
		target, comment, err = Parse(req.Value)
		if err != nil {
			return result, err
		}
		result.KeyID = req.SiteID + "." + ssh.FingerprintSHA256(target)
	}
	var lines bytes.Buffer
	found := false
	result.Keys = []Key{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), (1<<20)+1)
	for scanner.Scan() {
		line := scanner.Text()
		k, c, e := Parse(line)
		if e == nil {
			fp := ssh.FingerprintSHA256(k)
			result.Keys = append(result.Keys, Key{ID: req.SiteID + "." + fp, Comment: c, Type: k.Type(), Fingerprint: fp})
			if (req.Operation == "add" && fp == ssh.FingerprintSHA256(target)) || (req.Operation == "remove" && fp == req.Value) {
				found = true
				if req.Operation == "remove" {
					continue
				}
			}
		}
		lines.WriteString(line)
		lines.WriteByte('\n')
	}
	if err = scanner.Err(); err != nil {
		return result, err
	}
	if req.Operation == "list" {
		return result, nil
	}
	if req.Operation == "remove" && !found {
		result.NotFound = true
		return result, nil
	}
	if req.Operation == "add" {
		if found && existingMode == 0600 {
			return result, nil
		}
		if !found {
			lines.Write(bytes.TrimSpace(ssh.MarshalAuthorizedKey(target)))
			if comment != "" {
				lines.WriteByte(' ')
				lines.WriteString(comment)
			}
			lines.WriteByte('\n')
		}
	} else if req.Operation != "remove" {
		return result, errors.New("invalid operation")
	}
	if lines.Len() > 1<<20 {
		return result, errors.New("authorized_keys too large")
	}
	f, err := os.CreateTemp(fmt.Sprintf("/proc/self/fd/%d", dir), ".authorized_keys-")
	if err != nil {
		return result, err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(lines.Bytes())
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return result, err
	}
	if err = unix.Renameat(dir, name[strings.LastIndexByte(name, '/')+1:], dir, "authorized_keys"); err != nil {
		return result, err
	}
	return result, unix.Fsync(dir)
}
