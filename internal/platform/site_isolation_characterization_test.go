package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
	"time"

	runner "novapanel/internal/exec"
)

// Characterization only: this is not an implementation of ConfigIsolation.
// A held descriptor avoids granting a numeric child access to host ancestors.
func TestSiteIsolationOwner0400DoesNotQuiesceWriter(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root numeric credential fixture")
	}
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "config"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.WriteString("protected"); err != nil {
		t.Fatal(err)
	}
	if err = f.Chown(23451, 23452); err != nil {
		t.Fatal(err)
	}
	if err = f.Chmod(0400); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The descriptor already exists before the protected interval. Chmod cannot
	// revoke it; its owner can also change the mode even without this descriptor.
	out, stderr, code, err := isolationChild(ctx, "owner", f)
	if err != nil || code != 0 || len(out) != 0 || len(stderr) != 0 {
		t.Fatalf("numeric child: exit=%d err=%v stderr=%q", code, err, stderr)
	}
	got := make([]byte, 9)
	if _, err = f.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if string(got) != "REPLACED!" {
		t.Fatalf("expected concrete owner write, got %q", got)
	}
}

func TestSiteIsolationDirectWaitLeavesEscapedNumericWriter(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root numeric credential fixture")
	}
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "config"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The child self-terminates after a bounded wait even if this test fails. Its
	// only write authority is this owned scratch descriptor. No host PID is killed.
	_, stderr, code, err := isolationChild(ctx, "parent", f)
	if err != nil || code != 0 || len(stderr) != 0 {
		t.Fatalf("numeric parent: exit=%d err=%v stderr=%q", code, err, stderr)
	}
	// Release the escaped child only AFTER the runner reports successful Wait.
	if _, err = f.WriteAt([]byte("G"), 0); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 1)
	for ctx.Err() == nil {
		if _, err = f.ReadAt(got, 0); err != nil {
			t.Fatal(err)
		}
		if string(got) == "W" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("expected real escaped writer after direct Wait")
}

// /proc/self/exe avoids opening host ancestors to the numeric child.
// Every child is started through internal/exec.
func isolationChild(ctx context.Context, mode string, f *os.File) ([]byte, []byte, int, error) {
	var uid, gid uint32
	if os.Geteuid() == 0 {
		uid, gid = 23451, 23452
	}
	return (runner.Real{}).Run(ctx, "/proc/self/exe", []string{"-test.run=^TestSiteIsolationNumericHelper$"}, runner.RunOpts{
		Uid: uid, Gid: gid, Groups: []uint32{}, Stdin: f, MaxOutput: 1024,
		Env: []string{"NOVAPANEL_ISOLATION_HELPER=" + mode, "GORACE=atexit_sleep_ms=0"},
	})
}

func TestSiteIsolationNumericHelper(t *testing.T) {
	mode := os.Getenv("NOVAPANEL_ISOLATION_HELPER")
	if mode == "" {
		return
	}
	groups, err := os.Getgroups()
	if err != nil || os.Getuid() != 23451 || os.Getgid() != 23452 || len(groups) != 0 {
		os.Exit(2)
	}
	f := os.Stdin
	switch mode {
	case "owner":
		st, err := f.Stat()
		if err != nil || st.Mode().Perm() != 0400 {
			os.Exit(3)
		}
		if f.Chmod(0600) != nil {
			os.Exit(4)
		}
		if n, err := f.WriteAt([]byte("REPLACED!"), 0); err != nil || n != 9 {
			os.Exit(5)
		}
		os.Exit(0)
	case "parent":
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		go func() {
			_, stderr, code, err := isolationChild(ctx, "escaped", f)
			if err != nil {
				fmt.Fprintf(os.Stderr, "escaped child: %d %v %s", code, err, stderr)
			}
		}()
		end := time.Now().Add(2 * time.Second)
		for time.Now().Before(end) {
			b := make([]byte, 1)
			if _, err := f.ReadAt(b, 0); err != nil {
				os.Exit(6)
			}
			if b[0] == 'R' {
				os.Exit(0)
			}
			time.Sleep(time.Millisecond)
		}
		os.Exit(7)
	case "escaped":
		// Real starts a group leader. Join the parent's group so setsid can
		// create an escaped session without introducing another spawn path.
		pgid, err := unix.Getpgid(os.Getppid())
		if err != nil || unix.Setpgid(0, pgid) != nil {
			os.Exit(8)
		}
		if _, err := unix.Setsid(); err != nil {
			os.Exit(9)
		}
		os.Stdout.Close()
		os.Stderr.Close()
		if _, err := f.WriteAt([]byte("R"), 0); err != nil {
			os.Exit(10)
		}
		end := time.Now().Add(2 * time.Second)
		for time.Now().Before(end) {
			b := make([]byte, 1)
			if _, err := f.ReadAt(b, 0); err != nil {
				os.Exit(11)
			}
			if b[0] == 'G' {
				if _, err := f.WriteAt([]byte("W"), 0); err != nil {
					os.Exit(12)
				}
				os.Exit(0)
			}
			time.Sleep(time.Millisecond)
		}
		os.Exit(13)
	default:
		os.Exit(14)
	}
}
