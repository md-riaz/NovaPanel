package verbs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/validate"
)

const logBytes = 1 << 20
const logLineBytes = 8 << 10

type logSource struct {
	unit string
	site bool
}

var logSources = map[string]logSource{
	"php":     {site: true},
	"nginx":   {unit: "nginx.service"},
	"php-fpm": {unit: "php8.3-fpm.service"},
}

type logTailArgs struct {
	SiteID string `json:"site_id,omitempty"`
	Target string `json:"target"`
	Lines  int    `json:"lines,omitempty"`
}

func (a *logTailArgs) Validate() error {
	source, ok := logSources[a.Target]
	if !ok || strings.Contains(a.Target, "/") || strings.Contains(a.Target, "..") || filepath.IsAbs(a.Target) {
		return rpc.Errf("invalid_argument", "Invalid log target")
	}
	if source.site && !id.Valid(a.SiteID) || !source.site && a.SiteID != "" {
		return rpc.Errf("invalid_argument", "Invalid site ID")
	}
	if a.Lines <= 0 {
		a.Lines = 200
	} else if a.Lines > 1000 {
		a.Lines = 1000
	}
	return nil
}

type logTailReply struct {
	Lines []string `json:"lines"`
}

func init() {
	rpc.Register(rpc.Verb{Name: "log.tail", MinRole: rpc.Admin, Scope: "sites:read", NewArgs: func() any { return &logTailArgs{} }, Run: logTail})
}
func phpLogPath(name string) (string, error) {
	if err := (&validate.V{}).SiteName(name); err != nil {
		return "", err
	}
	return filepath.Join("/var/log/novapanel", name, "php-error.log"), nil
}
func logTail(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*logTailArgs)
	source := logSources[v.Target]
	if source.site {
		site, err := a.Store.SiteByID(ctx, v.SiteID)
		if err != nil {
			return nil, siteError(err)
		}
		path, err := phpLogPath(site.Name)
		if err != nil {
			return nil, err
		}
		lines, err := tailLogFile(path, v.Lines)
		if err != nil {
			return nil, err
		}
		return logTailReply{lines}, nil
	}
	out, _, code, err := a.Exec.Run(ctx, "/usr/bin/journalctl", []string{"--unit=" + source.unit, "--no-pager", "-n", strconv.Itoa(v.Lines), "--output=cat"}, exec.RunOpts{MaxOutput: logBytes})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, rpc.Errf("agent_error", "Cannot read journal")
	}
	return logTailReply{logLines(out, v.Lines)}, nil
}
func tailLogFile(path string, count int) ([]string, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return []string{}, nil
	}
	if errors.Is(err, unix.ELOOP) {
		return nil, rpc.Errf("invalid_argument", "Symlinked log target")
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, rpc.Errf("invalid_argument", "Invalid log file")
	}
	offset := info.Size() - logBytes
	if offset < 0 {
		offset = 0
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, logBytes))
	if err != nil {
		return nil, err
	}
	return logLines(data, count), nil
}
func logLines(data []byte, count int) []string {
	s := strings.TrimSuffix(string(data), "\n")
	if s == "" && len(data) == 0 {
		return []string{}
	}
	lines := strings.Split(s, "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	for i, line := range lines {
		if len(line) > logLineBytes {
			lines[i] = line[:logLineBytes]
		}
	}
	return lines
}
