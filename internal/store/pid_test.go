package store

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	execx "novapanel/internal/exec"
)

func TestPidfileReusedLivePID(t *testing.T) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	ticks := strings.Fields(string(stat[strings.LastIndex(string(stat), ") ")+2:]))[19]
	for _, stale := range []struct{ name, boot, ticks string }{
		{"legacy live unrelated PID", "", ""},
		{"previous boot", "00000000-0000-0000-0000-000000000000", ticks},
		{"previous process", strings.TrimSpace(string(boot)), "0"},
	} {
		t.Run(stale.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "db")
			raw := strconv.Itoa(os.Getpid()) + " " + stale.boot + " " + stale.ticks + "\n"
			if stale.boot == "" {
				raw = strconv.Itoa(os.Getpid()) + "\n"
			}
			if err := os.WriteFile(p+".pid", []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(p)
			if err != nil {
				t.Fatalf("unrelated live PID blocked startup: %v", err)
			}
			defer s.Close()
			got, err := os.ReadFile(p + ".pid")
			want := strconv.Itoa(os.Getpid()) + " " + strings.TrimSpace(string(boot)) + " " + ticks + "\n"
			if err != nil || string(got) != want {
				t.Fatalf("pidfile = %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestPidfileOwnerAndSafety(t *testing.T) {
	for _, tc := range []struct{ name, kind string }{
		{"matching live identity", "live"},
		{"invalid boot", "boot"},
		{"invalid ticks", "ticks"},
		{"missing fields", "missing"},
		{"extra fields", "extra"},
		{"overflow PID", "pid"},
		{"public file", "mode"},
		{"symlink", "symlink"},
		{"hardlink", "hardlink"},
		{"oversized", "size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "db")
			if err := writePID(p + ".pid"); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(p + ".pid")
			if err != nil {
				t.Fatal(err)
			}
			fields := strings.Fields(string(raw))
			switch tc.kind {
			case "boot":
				fields[1] = "not-a-boot"
				raw = []byte(strings.Join(fields, " "))
			case "ticks":
				fields[2] = "-1"
				raw = []byte(strings.Join(fields, " "))
			case "missing":
				raw = []byte(strings.Join(fields[:2], " "))
			case "extra":
				raw = append(raw, []byte("extra")...)
			case "pid":
				fields[0] = "4294967297"
				raw = []byte(strings.Join(fields, " "))
			case "size":
				raw = bytes.Repeat([]byte("x"), 97)
			}
			if err := os.WriteFile(p+".pid", raw, 0600); err != nil {
				t.Fatal(err)
			}
			switch tc.kind {
			case "mode":
				if err := os.Chmod(p+".pid", 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink":
				if err := os.Rename(p+".pid", p+".target"); err != nil {
					t.Fatal(err)
				}
				link := os.Link
				if tc.kind == "symlink" {
					link = os.Symlink
				}
				if err := link(p+".target", p+".pid"); err != nil {
					t.Fatal(err)
				}
			}
			if s, err := Open(p); err == nil {
				s.Close()
				t.Fatal("unsafe/live pidfile accepted")
			}
			got, err := os.ReadFile(p + ".pid")
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("refused pidfile changed: %q, %v", got, err)
			}
		})
	}
}

func TestPidfileLegacyRecoveryRequiresDBLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db")
	lock, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	raw := []byte(strconv.Itoa(os.Getpid()) + "\n")
	if err := os.WriteFile(p+".pid", raw, 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(p); err == nil {
		s.Close()
		t.Fatal("held database lock ignored")
	}
	got, err := os.ReadFile(p + ".pid")
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("locked pidfile changed: %q, %v", got, err)
	}
}

func TestPIDDeathHelper(t *testing.T) {
	if os.Getenv("NOVAPANEL_PID_DEATH_HELPER") != "1" {
		return
	}
	s, err := Open(os.Getenv("NOVAPANEL_PID_DEATH_DB"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s // Deliberately leave the store open when the process exits.
	fmt.Fprintln(os.Stdout, "ready")
	io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestPidfileRecoveryAfterProcessDeath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db")
	input, feed, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer feed.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, _, err := (execx.Real{}).Run(ctx, os.Args[0], []string{"-test.run=^TestPIDDeathHelper$"}, execx.RunOpts{
			Env:   []string{"NOVAPANEL_PID_DEATH_HELPER=1", "NOVAPANEL_PID_DEATH_DB=" + p},
			Stdin: input,
		})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(p + ".pid"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s, err := Open(p); err == nil {
		s.Close()
		t.Fatal("live owner lock ignored")
	}
	feed.Close()
	if err := <-done; err != nil {
		t.Fatalf("helper exit: %v", err)
	}
	s, err := Open(p)
	if err != nil {
		t.Fatalf("dead owner blocked startup: %v", err)
	}
	defer s.Close()
}

func TestPidfileLifetimeAndStaleRecovery(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db")
	if e := os.WriteFile(p+".pid", []byte("2147483647\n"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(p + ".pid")
	if e != nil || len(strings.Fields(string(raw))) != 3 || strings.Fields(string(raw))[0] != strconv.Itoa(os.Getpid()) {
		t.Fatal(string(raw), e)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Tx(context.Background(), func(*sql.Tx) error { close(entered); <-release; return nil })
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	if _, e = os.Stat(p + ".pid"); e != nil {
		t.Fatal(e)
	}
	close(release)
	<-done
	if e = <-closed; e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(p + ".pid"); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}
