package exec

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"strings"
	"testing"
	"unsafe"
)

func TestRecoveryChildPolicy(t *testing.T) {
	if os.Getenv("NOVAPANEL_RECOVERY_CHILD") == "1" {
		var sig int32
		e := unix.Prctl(unix.PR_GET_PDEATHSIG, uintptr(unsafe.Pointer(&sig)), 0, 0, 0)
		if e != nil || sig != int32(unix.SIGKILL) {
			os.Exit(12)
		}
		return
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	out, errout, exit, e := (Recovery{}).Run(context.Background(), exe, []string{"-test.run=^TestRecoveryChildPolicy$"}, RunOpts{Env: []string{"NOVAPANEL_RECOVERY_CHILD=1"}})
	if e != nil || exit != 0 || !strings.Contains(string(out), "PASS") {
		t.Fatalf("%s %s %d %v", out, errout, exit, e)
	}
}
