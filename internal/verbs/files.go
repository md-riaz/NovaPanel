package verbs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"

	"novapanel/internal/agent"
	runner "novapanel/internal/exec"
	"novapanel/internal/fileop"
	"novapanel/internal/id"
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
	rpc.Register(rpc.Verb{Name: "files.list", MinRole: rpc.Admin, Scope: "sites:read", ReadOnly: true, NewArgs: func() any { return &filesListArgs{} }, Run: filesList})
	rpc.Register(rpc.Verb{Name: "files.read", MinRole: rpc.Admin, Scope: "sites:read", ReadOnly: true, NewArgs: func() any { return &filesReadArgs{} }, Run: filesRead})
}
func filesList(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*filesListArgs)
	res, err := runFileop(ctx, a, v.SiteID, fileop.Request{Operation: "list", Path: v.Path})
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
	res, err := runFileop(ctx, a, v.SiteID, fileop.Request{Operation: "read", Path: v.Path, Offset: v.Offset, Length: v.Length})
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
func runFileop(ctx context.Context, a *agent.Agent, siteID string, req fileop.Request) (fileop.Response, error) {
	var res fileop.Response
	if a.Jobs == nil || a.Exec == nil || a.Store == nil {
		return res, rpc.Errf("precondition_failed", "Site account unavailable")
	}
	unlock, err := a.Jobs.LockSite(ctx, siteID)
	if err != nil {
		return res, err
	}
	defer unlock()
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
	req.Base = a.Cfg.SitesRoot
	req.Name = site.Name
	var body bytes.Buffer
	if err = json.NewEncoder(&body).Encode(req); err != nil {
		return res, err
	}
	out, _, code, err := a.Exec.Run(ctx, program, []string{"__fileop"}, runner.RunOpts{Uid: uid, Gid: gid, Groups: []uint32{}, Stdin: &body, MaxOutput: 2 << 20})
	if err != nil || code != 0 {
		return res, rpc.Errf("agent_error", "File operation failed")
	}
	if json.Unmarshal(out, &res) != nil {
		return res, rpc.Errf("agent_error", "File operation failed")
	}
	if res.Code != "" {
		switch res.Code {
		case "not_found", "invalid_argument":
			return res, rpc.Errf(res.Code, "File operation failed")
		default:
			return res, rpc.Errf("agent_error", "File operation failed")
		}
	}
	return res, nil
}
