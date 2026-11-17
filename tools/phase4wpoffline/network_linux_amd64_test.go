//go:build wordpress_offline_lab && linux && amd64

package main

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNetworkRestriction(t *testing.T) {
	if os.Getenv("WP_LAB_CHECK_INHERIT") == "1" {
		if _, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0); !errors.Is(err, unix.EPERM) {
			t.Fatalf("inherited socket restriction: %v", err)
		}
		return
	}
	if os.Getenv("WP_LAB_FILTER_CHILD") == "1" {
		if err := denyNetwork(); err != nil {
			t.Fatal(err)
		}
		if _, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0); !errors.Is(err, unix.EPERM) {
			t.Fatalf("socket: %v", err)
		}
		if _, _, errno := unix.Syscall(unix.SYS_IO_URING_SETUP, 1, 0, 0); errno != unix.EPERM {
			t.Fatalf("io_uring: %v", errno)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestNetworkRestriction$")
		child.Env = append(os.Environ(), "WP_LAB_CHECK_INHERIT=1")
		if out, err := child.CombinedOutput(); err != nil {
			t.Fatalf("exec inheritance: %v: %s", err, out)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestNetworkRestriction$")
	cmd.Env = append(os.Environ(), "WP_LAB_FILTER_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated filter test: %v: %s", err, out)
	}
}
