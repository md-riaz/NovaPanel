// Package exec is the sole external-program boundary. It never invokes a shell.
package exec

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const DefaultMaxOutput = 1 << 20

type RunOpts struct {
	Dir       string
	Env       []string
	Stdin     io.Reader
	Uid, Gid  uint32
	Groups    []uint32
	MaxOutput int
	Cgroup    *Cgroup
}
type Runner interface {
	Run(context.Context, string, []string, RunOpts) ([]byte, []byte, int, error)
}
type Real struct{}

var _ Runner = Real{}

// boundedWriter retains a prefix while draining excess data so children cannot
// block on a full pipe. The cap applies independently to stdout and stderr.
type boundedWriter struct {
	data []byte
	max  int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := w.max - len(w.data); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}
func (Real) Run(ctx context.Context, prog string, args []string, o RunOpts) ([]byte, []byte, int, error) {
	if !filepath.IsAbs(prog) {
		return nil, nil, -1, fmt.Errorf("program must be absolute")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, -1, err
	}
	max := o.MaxOutput
	if max <= 0 {
		max = DefaultMaxOutput
	}
	stdout, stderr := &boundedWriter{max: max}, &boundedWriter{max: max}
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Dir = o.Dir
	cmd.Env = append([]string{}, o.Env...)
	cmd.Stdin = o.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if o.Cgroup != nil {
		held, err := o.Cgroup.duplicate()
		if err != nil {
			return nil, nil, -1, err
		}
		defer held.Close()
		cmd.SysProcAttr.UseCgroupFD = true
		cmd.SysProcAttr.CgroupFD = int(held.Fd())
	}
	if o.Uid != 0 || o.Gid != 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: o.Uid, Gid: o.Gid, Groups: o.Groups}
	}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 250 * time.Millisecond
	err := cmd.Run()
	exit := -1
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stdout.data, stderr.data, exit, err
}
