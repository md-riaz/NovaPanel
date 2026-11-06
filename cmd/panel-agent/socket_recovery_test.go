package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/store"
)

func TestRecoveryRefusesUnsafeOrLivePaths(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("scratch socket ownership requires root")
	}
	for _, kind := range []string{"file", "symlink", "live", "reserved"} {
		t.Run(kind, func(t *testing.T) {
			a := scratchAgent(t)
			dir := filepath.Dir(a.Cfg.SocketPath)
			if e := os.MkdirAll(dir, 0755); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "file":
				if e := os.WriteFile(a.Cfg.SocketPath, []byte("keep"), 0600); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				if e := os.Symlink("missing", a.Cfg.SocketPath); e != nil {
					t.Fatal(e)
				}
			case "live":
				l, e := net.ListenUnix("unix", &net.UnixAddr{Name: a.Cfg.SocketPath, Net: "unix"})
				if e != nil {
					t.Fatal(e)
				}
				defer l.Close()
			case "reserved":
				l, e := net.ListenUnix("unix", &net.UnixAddr{Name: a.Cfg.SocketPath, Net: "unix"})
				if e != nil {
					t.Fatal(e)
				}
				l.SetUnlinkOnClose(false)
				l.Close()
				release, e := reserveSocket(a.Cfg.SocketPath)
				if e != nil {
					t.Fatal(e)
				}
				defer release()
			}
			before, e := os.Lstat(a.Cfg.SocketPath)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if e = serveScratch(ctx, a); e == nil {
				t.Fatal("accepted existing path")
			}
			after, e := os.Lstat(a.Cfg.SocketPath)
			if e != nil || !os.SameFile(before, after) {
				t.Fatalf("existing path replaced: %v", e)
			}
		})
	}
}

func TestRecoveryRequiresLifetimeOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("scratch socket ownership requires root")
	}
	a := scratchAgent(t)
	if e := os.MkdirAll(filepath.Dir(a.Cfg.DBPath), 0700); e != nil {
		t.Fatal(e)
	}
	owner, e := store.Open(a.Cfg.DBPath)
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close()
	if e = os.MkdirAll(filepath.Dir(a.Cfg.SocketPath), 0755); e != nil {
		t.Fatal(e)
	}
	l, e := net.ListenUnix("unix", &net.UnixAddr{Name: a.Cfg.SocketPath, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	l.SetUnlinkOnClose(false)
	l.Close()
	before, e := os.Lstat(a.Cfg.SocketPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = serveScratch(context.Background(), a); e == nil {
		t.Fatal("started without lifetime ownership")
	}
	after, e := os.Lstat(a.Cfg.SocketPath)
	if e != nil || !os.SameFile(before, after) {
		t.Fatalf("removed socket without lifetime ownership: %v", e)
	}
}

func TestSocketReservationRejectsUnsafeLock(t *testing.T) {
	for _, kind := range []string{"symlink", "mode"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.sock")
			if kind == "symlink" {
				if e := os.Symlink("missing", path+".lock"); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := os.WriteFile(path+".lock", nil, 0644); e != nil {
					t.Fatal(e)
				}
			}
			if release, e := reserveSocket(path); e == nil {
				release()
				t.Fatal("unsafe socket lock accepted")
			}
		})
	}
}
