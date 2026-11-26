package verbs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"

	"novapanel/internal/agent"
	runner "novapanel/internal/exec"
	"novapanel/internal/fileop"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/rpc"
)

type filesListArgs struct {
	SiteID string `json:"site_id"`
	Path   string `json:"path"`
}
type filesReadArgs struct {
	SiteID string `json:"site_id"`
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Length int    `json:"length,omitempty"`
}

type filesWriteArgs struct {
	SiteID     string `json:"site_id"`
	Path       string `json:"path"`
	ContentB64 string `json:"content_b64"`
}
type filesMkdirArgs struct {
	SiteID string `json:"site_id"`
	Path   string `json:"path"`
}
type filesMoveArgs struct {
	SiteID string `json:"site_id"`
	From   string `json:"from"`
	To     string `json:"to"`
}
type filesDeleteArgs struct {
	SiteID string `json:"site_id"`
	Path   string `json:"path"`
}
type filesChmodArgs struct {
	SiteID string `json:"site_id"`
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
}

type filesArchiveArgs struct {
	SiteID string   `json:"site_id"`
	Paths  []string `json:"paths"`
	Dest   string   `json:"dest"`
}
type filesExtractArgs struct {
	SiteID  string `json:"site_id"`
	Archive string `json:"archive"`
	Dest    string `json:"dest"`
}

func (v *filesArchiveArgs) Validate() error {
	if len(v.Paths) == 0 || len(v.Paths) > 10000 || v.Dest == "" {
		return rpc.Errf("invalid_argument", "Invalid archive paths")
	}
	if err := validateFiles(v.SiteID, v.Dest); err != nil {
		return err
	}
	for _, p := range v.Paths {
		if p == "" || fileop.ValidatePath(p) != nil {
			return rpc.Errf("invalid_argument", "Invalid archive path")
		}
	}
	return nil
}
func (v *filesExtractArgs) Validate() error {
	if v.Archive == "" || v.Dest == "" {
		return rpc.Errf("invalid_argument", "Invalid archive paths")
	}
	if err := validateFiles(v.SiteID, v.Archive); err != nil {
		return err
	}
	return validateFiles(v.SiteID, v.Dest)
}

func (v *filesWriteArgs) Validate() error {
	if err := validateFiles(v.SiteID, v.Path); err != nil {
		return err
	}
	if len(v.ContentB64) > base64.StdEncoding.EncodedLen(fileop.MaxWrite) {
		return rpc.Errf("invalid_argument", "File content exceeds 512 KiB; use files.upload for larger files")
	}
	content, err := base64.StdEncoding.DecodeString(v.ContentB64)
	if err != nil {
		return rpc.Errf("invalid_argument", "Invalid base64 file content")
	}
	if len(content) > fileop.MaxWrite {
		return rpc.Errf("invalid_argument", "File content exceeds 512 KiB; use files.upload for larger files")
	}
	return nil
}
func (v *filesMkdirArgs) Validate() error  { return validateFiles(v.SiteID, v.Path) }
func (v *filesDeleteArgs) Validate() error { return validateFiles(v.SiteID, v.Path) }
func (v *filesMoveArgs) Validate() error {
	if err := validateFiles(v.SiteID, v.From); err != nil {
		return err
	}
	return validateFiles(v.SiteID, v.To)
}
func (v *filesChmodArgs) Validate() error {
	if !fileop.ValidMode(v.Mode) {
		return rpc.Errf("invalid_argument", "Invalid file mode")
	}
	return validateFiles(v.SiteID, v.Path)
}

func validateFiles(site, path string) error {
	if !id.Valid(site) || fileop.ValidatePath(path) != nil {
		return rpc.Errf("invalid_argument", "Invalid site ID or path")
	}
	return nil
}
func (v *filesListArgs) Validate() error { return validateFiles(v.SiteID, v.Path) }
func (v *filesReadArgs) Validate() error {
	if v.Offset < 0 || v.Length < 0 {
		return rpc.Errf("invalid_argument", "Invalid offset or length")
	}
	return validateFiles(v.SiteID, v.Path)
}

func init() {
	rpc.Register(rpc.Verb{Name: "files.archive", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &filesArchiveArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*filesArchiveArgs).SiteID }, Run: filesArchive, Exec: filesArchiveExec})
	rpc.Register(rpc.Verb{Name: "files.extract", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &filesExtractArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*filesExtractArgs).SiteID }, Run: filesExtract, Exec: filesExtractExec})
	rpc.Register(rpc.Verb{Name: "files.list", MinRole: rpc.Admin, Scope: "sites:read", ReadOnly: true, NewArgs: func() any { return &filesListArgs{} }, Run: filesList})
	rpc.Register(rpc.Verb{Name: "files.read", MinRole: rpc.Admin, Scope: "sites:read", ReadOnly: true, NewArgs: func() any { return &filesReadArgs{} }, Run: filesRead})
	rpc.Register(rpc.Verb{Name: "files.write", MinRole: rpc.Admin, Scope: "sites:write", NewArgs: func() any { return &filesWriteArgs{} }, Run: filesWrite})
	rpc.Register(rpc.Verb{Name: "files.mkdir", MinRole: rpc.Admin, Scope: "sites:write", NewArgs: func() any { return &filesMkdirArgs{} }, Run: filesMkdir})
	rpc.Register(rpc.Verb{Name: "files.move", MinRole: rpc.Admin, Scope: "sites:write", NewArgs: func() any { return &filesMoveArgs{} }, Run: filesMove})
	rpc.Register(rpc.Verb{Name: "files.delete", MinRole: rpc.Admin, Scope: "sites:write", NewArgs: func() any { return &filesDeleteArgs{} }, Run: filesDelete})
	rpc.Register(rpc.Verb{Name: "files.chmod", MinRole: rpc.Admin, Scope: "sites:write", NewArgs: func() any { return &filesChmodArgs{} }, Run: filesChmod})
}
func filesArchive(ctx context.Context, a *agent.Agent, args any) (any, error) {
	return a.Jobs.Enqueue(ctx, "files.archive", args, "site:"+args.(*filesArchiveArgs).SiteID)
}
func filesExtract(ctx context.Context, a *agent.Agent, args any) (any, error) {
	return a.Jobs.Enqueue(ctx, "files.extract", args, "site:"+args.(*filesExtractArgs).SiteID)
}
func filesArchiveExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v filesArchiveArgs
	if json.Unmarshal(j.Args, &v) != nil {
		return rpc.Errf("invalid_argument", "Invalid archive arguments")
	}
	if err := v.Validate(); err != nil {
		return err
	}
	if err := (fileopService{a}).archive(ctx, v.SiteID, fileop.ArchiveRequest{JobID: j.ID, Operation: "archive", Paths: v.Paths, Dest: v.Dest}); err != nil {
		return err
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}
func filesExtractExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v filesExtractArgs
	if json.Unmarshal(j.Args, &v) != nil {
		return rpc.Errf("invalid_argument", "Invalid extract arguments")
	}
	if err := v.Validate(); err != nil {
		return err
	}
	if err := (fileopService{a}).archive(ctx, v.SiteID, fileop.ArchiveRequest{JobID: j.ID, Operation: "extract", Archive: v.Archive, Dest: v.Dest}); err != nil {
		return err
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}
func filesWrite(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*filesWriteArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	content, _ := base64.StdEncoding.DecodeString(v.ContentB64)
	return (fileopService{a}).mutate(ctx, v.SiteID, fileop.Request{Operation: "write", Path: v.Path, Length: len(content)}, content)
}
func filesMkdir(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*filesMkdirArgs)
	return (fileopService{a}).mutate(ctx, v.SiteID, fileop.Request{Operation: "mkdir", Path: v.Path}, nil)
}
func filesMove(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*filesMoveArgs)
	return (fileopService{a}).mutate(ctx, v.SiteID, fileop.Request{Operation: "move", From: v.From, To: v.To}, nil)
}
func filesDelete(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*filesDeleteArgs)
	return (fileopService{a}).mutate(ctx, v.SiteID, fileop.Request{Operation: "delete", Path: v.Path}, nil)
}
func filesChmod(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*filesChmodArgs)
	return (fileopService{a}).mutate(ctx, v.SiteID, fileop.Request{Operation: "chmod", Path: v.Path, Mode: v.Mode}, nil)
}
func (s fileopService) mutate(ctx context.Context, siteID string, req fileop.Request, content []byte) (any, error) {
	_, err := s.run(ctx, siteID, req, content)
	if err != nil {
		return nil, err
	}
	return struct{}{}, nil
}
func filesList(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*filesListArgs)
	res, err := (fileopService{a}).run(ctx, v.SiteID, fileop.Request{Operation: "list", Path: v.Path}, nil)
	if err != nil {
		return nil, err
	}
	if res.Entries == nil {
		res.Entries = []fileop.Entry{}
	}
	return struct {
		Entries []fileop.Entry `json:"entries"`
	}{res.Entries}, nil
}
func filesRead(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*filesReadArgs)
	res, err := (fileopService{a}).run(ctx, v.SiteID, fileop.Request{Operation: "read", Path: v.Path, Offset: v.Offset, Length: v.Length}, nil)
	if err != nil {
		return nil, err
	}
	if res.Content == nil {
		res.Content = []byte{}
	}
	return struct {
		Content []byte `json:"content_b64"`
		Size    int64  `json:"size"`
		EOF     bool   `json:"eof"`
	}{res.Content, res.Size, res.EOF}, nil
}

type fileopService struct{ a *agent.Agent }

func (s fileopService) run(ctx context.Context, siteID string, req fileop.Request, content []byte) (fileop.Response, error) {
	a := s.a
	var res fileop.Response
	if a.Jobs == nil || a.Exec == nil || a.Store == nil {
		return res, rpc.Errf("precondition_failed", "Site account unavailable")
	}
	unlock, err := a.Jobs.LockSite(ctx, siteID)
	if err != nil {
		return res, err
	}
	defer unlock()
	return s.invoke(ctx, siteID, &req, content, "__fileop")
}

// invoke is called under the synchronous site lock or the worker's object lock.
func (s fileopService) invoke(ctx context.Context, siteID string, request any, content []byte, helper string) (fileop.Response, error) {
	a := s.a
	var res fileop.Response
	if a.Exec == nil || a.Store == nil {
		return res, rpc.Errf("precondition_failed", "Site account unavailable")
	}
	site, err := a.Store.SiteByID(ctx, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return res, rpc.Errf("not_found", "Site not found")
	}
	if err != nil {
		return res, err
	}
	if site.Status != "active" {
		return res, rpc.Errf("precondition_failed", "Site unavailable")
	}
	uid, gid, err := php.ResolveUser(ctx, a.Exec, a.Cfg.SitesRoot, site.Name, site.ID)
	if err != nil {
		return res, err
	}
	program, err := os.Executable()
	if err != nil {
		return res, err
	}
	switch req := request.(type) {
	case *fileop.Request:
		req.Base, req.Name = a.Cfg.SitesRoot, site.Name
	case *fileop.ArchiveRequest:
		req.Base, req.Name = a.Cfg.SitesRoot, site.Name
	default:
		return res, rpc.Errf("agent_error", "Invalid helper request")
	}
	var body bytes.Buffer
	if err = json.NewEncoder(&body).Encode(request); err != nil {
		return res, err
	}
	out, _, code, err := a.Exec.Run(ctx, program, []string{helper}, runner.RunOpts{Uid: uid, Gid: gid, Groups: []uint32{}, Stdin: io.MultiReader(&body, bytes.NewReader(content)), MaxOutput: 2 << 20})
	if err != nil || code != 0 {
		return res, rpc.Errf("agent_error", "File operation failed")
	}
	if json.Unmarshal(out, &res) != nil {
		return res, rpc.Errf("agent_error", "File operation failed")
	}
	if res.Code != "" {
		switch res.Code {
		case "not_found", "invalid_argument", "conflict":
			return res, rpc.Errf(res.Code, "File operation failed")
		default:
			return res, rpc.Errf("agent_error", "File operation failed")
		}
	}
	return res, nil
}

func (s fileopService) archive(ctx context.Context, siteID string, req fileop.ArchiveRequest) error {
	_, err := s.invoke(ctx, siteID, &req, nil, "__archive")
	return err
}
