package platform

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	runner "novapanel/internal/exec"
)

// Characterization only: this is not an implementation of ConfigIsolation.
// A held descriptor avoids granting a numeric child access to host ancestors.
func TestSiteIsolationOwner0400DoesNotQuiesceWriter(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root numeric credential fixture")
	}
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 numeric fixture unavailable")
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
	script := "import os\nassert os.getuid()==23451\nassert os.getgid()==23452\nassert os.getgroups()==[]\nassert os.fstat(0).st_mode & 0o777 == 0o400\nos.fchmod(0,0o600)\nos.pwrite(0,b'REPLACED!',0)\n"
	out, stderr, code, err := (runner.Real{}).Run(ctx, "/usr/bin/python3", []string{"-I", "-c", script}, runner.RunOpts{Uid: 23451, Gid: 23452, Groups: []uint32{}, Stdin: f, MaxOutput: 1024})
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
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 numeric fixture unavailable")
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
	script := "import os,time\nassert os.getuid()==23451\npid=os.fork()\nif pid: os._exit(0)\nos.setsid()\nos.close(1)\nos.close(2)\nend=time.monotonic()+2\nwhile time.monotonic()<end:\n if os.pread(0,1,0)==b'G':\n  os.pwrite(0,b'W',0)\n  os._exit(0)\n time.sleep(.001)\nos._exit(1)\n"
	_, stderr, code, err := (runner.Real{}).Run(ctx, "/usr/bin/python3", []string{"-I", "-c", script}, runner.RunOpts{Uid: 23451, Gid: 23452, Groups: []uint32{}, Stdin: f, MaxOutput: 1024})
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
