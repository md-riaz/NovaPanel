package firewall

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	execpkg "novapanel/internal/exec"
)

type captureRunner struct {
	calls   [][]string
	opts    []execpkg.RunOpts
	hook    func(int)
	status  string
	failure int
	output  []byte
	err     error
	exit    int
}

func (f *captureRunner) Run(_ context.Context, p string, args []string, o execpkg.RunOpts) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, append([]string{p}, args...))
	f.opts = append(f.opts, o)
	if f.hook != nil {
		f.hook(len(f.calls))
	}
	if len(f.calls) == f.failure {
		return f.output, nil, f.exit, f.err
	}
	if p == "/usr/sbin/ufw" {
		return []byte(f.status), nil, 0, nil
	}
	return []byte(p + " scratch diagnostics\n"), nil, 0, nil
}
func captureSourceFixture(t *testing.T) (string, *captureRunner) {
	t.Helper()
	root := filepath.Join(custodyTempDir(t), "ufw")
	mustCustody(t, os.Mkdir(root, 0755))
	for n, b := range custodyInput().Rules {
		mustCustody(t, os.WriteFile(filepath.Join(root, n), b, 0644))
	}
	// UFW has non-rule config; the artifact inventory must remain closed.
	mustCustody(t, os.WriteFile(filepath.Join(root, "ufw.conf"), []byte("ENABLED=no\n"), 0644))
	return root, &captureRunner{status: "Status: active\n\nTo Action From\n"}
}
func TestUFWCaptureRejectsUnstableSources(t *testing.T) {
	for _, fault := range []string{"bytes", "leaf", "root", "mode", "status", "unknown-status", "oversize-status", "total-size"} {
		t.Run(fault, func(t *testing.T) {
			root, f := captureSourceFixture(t)
			s, e := openUFWSource(root, uint32(os.Geteuid()), f)
			mustCustody(t, e)
			defer s.Close()
			f.hook = func(call int) {
				if call != 2 {
					return
				}
				path := filepath.Join(root, "user.rules")
				switch fault {
				case "bytes":
					mustCustody(t, os.WriteFile(path, []byte("changed"), 0644))
				case "leaf":
					mustCustody(t, os.Rename(path, path+".old"))
					mustCustody(t, os.WriteFile(path, custodyInput().Rules["user.rules"], 0644))
				case "root":
					mustCustody(t, os.Rename(root, root+".old"))
					mustCustody(t, os.Mkdir(root, 0755))
				case "mode":
					mustCustody(t, os.Chmod(path, 0666))
				case "status":
					f.status = "Status: inactive\n"
				}
			}
			switch fault {
			case "unknown-status":
				f.status = "Status: unknown\n"
			case "oversize-status":
				f.status = strings.Repeat("x", snapshotFileLimit+1)
			case "total-size":
				for n := range custodyInput().Rules {
					mustCustody(t, os.WriteFile(filepath.Join(root, n), []byte(strings.Repeat("x", 3<<20)), 0644))
				}
			}
			in, e := s.Capture(context.Background())
			if e == nil {
				t.Fatal("unstable source accepted", fault)
			}
			if in.Rules != nil || in.NumberedStatus != nil {
				t.Fatal("partial capture escaped")
			}
		})
	}
}

func TestUFWCaptureRefusesMalformedStatus(t *testing.T) {
	for _, status := range []string{"Status: active", "Status: inactive\r\n", "Status: active\nStatus: inactive\n", "Status: inactive\nStatus: active\n"} {
		t.Run(strings.ReplaceAll(status, "\n", "_"), func(t *testing.T) {
			root, f := captureSourceFixture(t)
			f.status = status
			s, e := openUFWSource(root, uint32(os.Geteuid()), f)
			mustCustody(t, e)
			defer s.Close()
			if _, e = s.Capture(context.Background()); e == nil {
				t.Fatal("ambiguous status accepted")
			}
		})
	}
}

func TestUFWCaptureAdmissionAndCommandFailures(t *testing.T) {
	for _, fault := range []string{"symlink", "hardlink", "fifo", "missing-ipv6", "empty", "oversize", "writable", "foreign", "ancestor", "symlink-root", "nil-runner", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			root, f := captureSourceFixture(t)
			path := filepath.Join(root, "user6.rules")
			switch fault {
			case "symlink":
				mustCustody(t, os.Remove(path))
				mustCustody(t, os.Symlink("ufw.conf", path))
			case "hardlink":
				mustCustody(t, os.Link(path, path+".link"))
			case "fifo":
				mustCustody(t, os.Remove(path))
				mustCustody(t, unix.Mkfifo(path, 0600))
			case "missing-ipv6":
				mustCustody(t, os.Remove(path))
			case "empty":
				mustCustody(t, os.WriteFile(path, nil, 0644))
			case "oversize":
				mustCustody(t, os.WriteFile(path, []byte(strings.Repeat("x", snapshotFileLimit+1)), 0644))
			case "writable":
				mustCustody(t, os.Chmod(path, 0666))
			case "foreign":
				if os.Geteuid() != 0 {
					t.Skip("chown requires root")
				}
				mustCustody(t, os.Chown(path, 65534, 65534))
			case "ancestor":
				mustCustody(t, os.Chmod(root, 0777))
			case "symlink-root":
				mustCustody(t, os.Rename(root, root+".real"))
				mustCustody(t, os.Symlink(root+".real", root))
			}
			var runner execpkg.Runner = f
			if fault == "nil-runner" {
				runner = nil
			}
			s, e := openUFWSource(root, uint32(os.Geteuid()), runner)
			if e == nil {
				defer s.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if fault == "cancel" {
					cancel()
				}
				_, e = s.Capture(ctx)
			}
			if e == nil || len(f.calls) != 0 {
				t.Fatal("unsafe source invoked command", fault, e, f.calls)
			}
		})
	}
	for call := 1; call <= 4; call++ {
		for _, fault := range []string{"error", "exit", "empty", "overflow", "cancel"} {
			t.Run(fmt.Sprintf("command-%d-%s", call, fault), func(t *testing.T) {
				root, f := captureSourceFixture(t)
				s, e := openUFWSource(root, uint32(os.Geteuid()), f)
				mustCustody(t, e)
				defer s.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				f.failure = call
				switch fault {
				case "error":
					f.err = errors.New("fake failure")
				case "exit":
					f.exit = 1
					f.output = []byte("diagnostic")
				case "overflow":
					f.output = []byte(strings.Repeat("x", snapshotFileLimit+1))
				case "cancel":
					f.output = []byte(f.status)
					f.hook = func(n int) {
						if n == call {
							cancel()
						}
					}
				}
				in, e := s.Capture(ctx)
				if e == nil || in.Rules != nil || len(f.calls) != call {
					t.Fatal("command failure escaped", fault, e, len(f.calls))
				}
			})
		}
	}
}

func TestUFWCaptureImmutableRetry(t *testing.T) {
	root, f := captureSourceFixture(t)
	s, e := openUFWSource(root, uint32(os.Geteuid()), f)
	mustCustody(t, e)
	defer s.Close()
	ctx := context.Background()
	in, e := s.Capture(ctx)
	mustCustody(t, e)
	c, _ := custodyFixture(t)
	digest, e := c.Put(ctx, custodyID, in)
	mustCustody(t, e)
	again, e := c.Put(ctx, custodyID, in)
	mustCustody(t, e)
	if again != digest {
		t.Fatal("retry binding")
	}
	mustCustody(t, os.WriteFile(filepath.Join(root, "user.rules"), []byte("changed between captures"), 0644))
	changed, e := s.Capture(ctx)
	mustCustody(t, e)
	if _, e = c.Put(ctx, custodyID, changed); e == nil {
		t.Fatal("recapture overwrote custody")
	}
	verified, e := c.Verify(ctx, custodyRecord(digest))
	mustCustody(t, e)
	defer verified.Close()
}

func TestUFWCaptureRoundTrip(t *testing.T) {
	for _, active := range []bool{true, false} {
		t.Run(map[bool]string{true: "active", false: "inactive"}[active], func(t *testing.T) {
			root, f := captureSourceFixture(t)
			if !active {
				f.status = "Status: inactive\n"
			}
			s, e := openUFWSource(root, uint32(os.Geteuid()), f)
			mustCustody(t, e)
			defer s.Close()
			in, e := s.Capture(context.Background())
			mustCustody(t, e)
			if in.PriorEnabled != active || !reflect.DeepEqual(in.Rules, custodyInput().Rules) {
				t.Fatal("wrong source/active binding", in)
			}
			want := [][]string{{"/usr/sbin/ufw", "status", "numbered"}, {"/usr/sbin/iptables-save"}, {"/usr/sbin/ip6tables-save"}, {"/usr/sbin/ufw", "status", "numbered"}}
			if !reflect.DeepEqual(f.calls, want) {
				t.Fatal(f.calls)
			}
			for _, o := range f.opts {
				if o.MaxOutput != snapshotFileLimit+1 || !reflect.DeepEqual(o.Env, []string{"LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}) {
					t.Fatal("unbounded/localized command", o)
				}
			}
			c, _ := custodyFixture(t)
			digest, e := c.Put(context.Background(), custodyID, in)
			mustCustody(t, e)
			r := custodyRecord(digest)
			r.PriorEnabled = active
			verified, e := c.Verify(context.Background(), r)
			mustCustody(t, e)
			defer verified.Close()
			if len(verified.files) != 10 {
				t.Fatal("inventory", len(verified.files))
			}
		})
	}
}
