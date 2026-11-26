// Package deploy manages the site-owned, private Git deployment state.
package deploy

import (
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strconv"
)

type Request struct {
	Home, Operation string
	Key             []byte
}

// Helper runs only with the site's credentials; paths are opened without symlinks.
func Helper(in io.Reader) error {
	if os.Geteuid() == 0 {
		return errors.New("refusing root helper")
	}
	var req Request
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&req); err != nil {
		return err
	}
	defer clear(req.Key)
	return Apply(req)
}

func Apply(req Request) error {
	home, err := unix.Open(req.Home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(home)
	if req.Operation == "prepare" {
		if err = unix.Mkdirat(home, ".novapanel", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return err
		}
	}
	parent, err := unix.Openat(home, ".novapanel", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) && req.Operation != "prepare" {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	if req.Operation == "teardown" {
		root, err := os.OpenRoot("/proc/self/fd/" + strconv.Itoa(parent))
		if err != nil {
			return err
		}
		defer root.Close()
		return root.RemoveAll("deploy")
	}
	if req.Operation == "prepare" {
		if err = unix.Mkdirat(parent, "deploy", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return err
		}
	}
	dir, err := unix.Openat(parent, "deploy", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) && req.Operation == "remove" {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	if req.Operation != "prepare" && req.Operation != "remove" {
		return errors.New("invalid operation")
	}
	if req.Operation == "prepare" {
		if err = unix.Fchmod(dir, 0700); err != nil {
			return err
		}
	}
	if err = unix.Unlinkat(dir, "id_ed25519", 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	if req.Operation == "remove" || len(req.Key) == 0 {
		return nil
	}
	fd, err := unix.Openat(dir, "id_ed25519", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "id_ed25519")
	_, err = f.Write(req.Key)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
