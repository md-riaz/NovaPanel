package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
)

var errDomain = errors.New("platform: execution domain refused")

// ExecutionDomain owns launcher admission and recursive process retirement only.
// It does not attest account freshness, IO retirement or filesystem custody.
// Trusted root must exclude all outside launch, migration and replacement.
type ExecutionDomain struct {
	mu         sync.Mutex
	retireGate chan struct{}
	closed     bool
	active     int
	idle       chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	runner     runner.Runner
	capability *runner.Cgroup
	file       *os.File
	retire     func(context.Context) error
}

func newExecutionDomain(r runner.Runner, c *runner.Cgroup, retire func(context.Context) error) *ExecutionDomain {
	ctx, cancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	return &ExecutionDomain{retireGate: make(chan struct{}, 1), ctx: ctx, cancel: cancel, idle: idle, runner: r, capability: c, retire: retire}
}

// OpenExecutionDomain consumes an already provisioned, empty root-controlled
// cgroup v2 domain. It creates nothing and never adopts a populated workload.
func OpenExecutionDomain(path string) (*ExecutionDomain, error) {
	if filepath.Clean(path) != path || !strings.HasPrefix(path, "/sys/fs/cgroup/") {
		return nil, errDomain
	}
	fd, err := unix.Open("/", unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_RDONLY, 0)
	if err != nil {
		return nil, errDomain
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 {
			unix.Close(fd)
			return nil, errDomain
		}
		next, e := unix.Openat(fd, part, unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_RDONLY, 0)
		unix.Close(fd)
		if e != nil {
			return nil, errDomain
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), "execution-domain")
	fail := func() (*ExecutionDomain, error) { f.Close(); return nil, errDomain }
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 {
		return fail()
	}
	cap, err := runner.NewCgroup(f)
	if err != nil {
		return fail()
	}
	d := newExecutionDomain(runner.Real{}, cap, nil)
	d.file = f
	d.retire = d.killAndWait
	// A root-owned directory alone does not exclude delegated migration files.
	for _, name := range []string{"cgroup.procs", "cgroup.threads", "cgroup.subtree_control"} {
		control, err := d.control(name, unix.O_RDONLY)
		if err != nil {
			cap.Close()
			return fail()
		}
		control.Close()
	}
	typ, err := d.readControl("cgroup.type")
	if err != nil || typ != "domain\n" {
		cap.Close()
		return fail()
	}
	empty, err := d.empty()
	if err != nil || !empty {
		cap.Close()
		return fail()
	}
	return d, nil
}

// Run places children at birth and holds admission until the runner returns.
// Callers must still supply validated numeric credentials and reviewed commands.
func (d *ExecutionDomain) Run(ctx context.Context, prog string, args []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	d.mu.Lock()
	if d.closed || o.Cgroup != nil || ctx.Err() != nil {
		d.mu.Unlock()
		return nil, nil, -1, errDomain
	}
	if d.active == 0 {
		d.idle = make(chan struct{})
	}
	d.active++
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.active--
		if d.active == 0 {
			close(d.idle)
		}
		d.mu.Unlock()
	}()
	work, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(d.ctx, cancel)
	defer cancel()
	defer stop()
	// Check lifetime cancellation synchronously as well as through AfterFunc.
	if d.ctx.Err() != nil {
		return nil, nil, -1, errDomain
	}
	o.Cgroup = d.capability
	return d.runner.Run(work, prog, args, o)
}

// Drain permanently closes admission, cancels and drains admitted launches, then
// kills and observes recursive emptiness. Supply an independent cleanup deadline.
// A nil result is not IO retirement and does not authorize protected publication.
func (d *ExecutionDomain) Drain(ctx context.Context) error {
	d.mu.Lock()
	d.closed = true
	d.cancel()
	idle := d.idle
	d.mu.Unlock()
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("%w: cleanup deadline required", errDomain)
	}
	select {
	case <-idle:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case d.retireGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-d.retireGate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.retire(ctx)
}

// Close revokes admission and releases descriptors only after admitted calls
// return. It neither kills a domain nor claims successful retirement.
func (d *ExecutionDomain) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	d.cancel()
	if d.active != 0 {
		return errDomain
	}
	select {
	case d.retireGate <- struct{}{}:
	default:
		return errDomain
	}
	defer func() { <-d.retireGate }()
	var err error
	if d.capability != nil {
		err = d.capability.Close()
	}
	if d.file != nil {
		err = errors.Join(err, d.file.Close())
		d.file = nil
	}
	return err
}
func (d *ExecutionDomain) control(name string, flags int) (*os.File, error) {
	if d.file == nil {
		return nil, errDomain
	}
	fd, err := unix.Openat(int(d.file.Fd()), name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, errDomain
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, errDomain
	}
	return os.NewFile(uintptr(fd), name), nil
}
func (d *ExecutionDomain) readControl(name string) (string, error) {
	f, err := d.control(name, unix.O_RDONLY)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 {
		return "", errDomain
	}
	return string(b), nil
}
func (d *ExecutionDomain) empty() (bool, error) {
	text, err := d.readControl("cgroup.events")
	if err != nil {
		return false, err
	}
	return parseDomainEmpty(text)
}

func parseDomainEmpty(text string) (bool, error) {
	found := false
	empty := false
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return false, errDomain
		}
		if fields[0] == "populated" {
			if found || (fields[1] != "0" && fields[1] != "1") {
				return false, errDomain
			}
			found = true
			empty = fields[1] == "0"
		}
	}
	if !found {
		return false, errDomain
	}
	return empty, nil
}
func (d *ExecutionDomain) killAndWait(ctx context.Context) error {
	f, err := d.control("cgroup.kill", unix.O_WRONLY)
	if err != nil {
		return err
	}
	n, err := f.WriteString("1")
	closeErr := f.Close()
	if err != nil || n != 1 || closeErr != nil {
		return errDomain
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		empty, err := d.empty()
		if err != nil {
			return err
		}
		if empty {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
