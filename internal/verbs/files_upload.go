package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/agent"
	"novapanel/internal/fileop"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
)

type filesUploadArgs struct {
	SiteID   string `json:"site_id"`
	Path     string `json:"path"`
	UploadID string `json:"upload_id"`
}

func (v *filesUploadArgs) Validate() error {
	if !id.Valid(v.UploadID) {
		return rpc.Errf("invalid_argument", "Invalid upload ID")
	}
	return validateFiles(v.SiteID, v.Path)
}

func init() {
	rpc.Register(rpc.Verb{Name: "files.upload", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &filesUploadArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*filesUploadArgs).SiteID }, Run: filesUpload, Exec: filesUploadExec})
}

func filesUpload(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*filesUploadArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	a.UploadsMu.Lock()
	defer a.UploadsMu.Unlock()
	info, err := os.Lstat(filepath.Join(a.Cfg.UploadsDir, v.UploadID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, rpc.Errf("not_found", "Upload not found")
	}
	if err != nil {
		return nil, rpc.Errf("agent_error", "Upload unavailable")
	}
	if !info.Mode().IsRegular() {
		return nil, rpc.Errf("invalid_argument", "Invalid upload")
	}
	if info.ModTime().Before(time.Now().Add(-fileop.UploadTTL)) {
		return nil, rpc.Errf("not_found", "Upload expired")
	}
	return a.Jobs.Enqueue(ctx, "files.upload", args, "site:"+v.SiteID)
}

func filesUploadExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v filesUploadArgs
	if json.Unmarshal(j.Args, &v) != nil {
		return rpc.Errf("invalid_argument", "Invalid upload arguments")
	}
	if err := v.Validate(); err != nil {
		return err
	}
	name := filepath.Join(a.Cfg.UploadsDir, v.UploadID)
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return rpc.Errf("not_found", "Upload not found")
		}
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENXIO) || errors.Is(err, unix.ENODEV) {
			return rpc.Errf("invalid_argument", "Invalid upload")
		}
		return rpc.Errf("agent_error", "Upload unavailable")
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return rpc.Errf("agent_error", "Upload unavailable")
	}
	if !info.Mode().IsRegular() || info.Size() > fileop.MaxUpload {
		return rpc.Errf("invalid_argument", "Invalid upload")
	}
	req := fileop.Request{Operation: "upload", Path: v.Path, Length: int(info.Size())}
	if _, err := (fileopService{a}).invoke(ctx, v.SiteID, &req, f, "__fileop"); err != nil {
		return err
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}
