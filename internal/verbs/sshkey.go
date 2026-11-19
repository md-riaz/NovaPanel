package verbs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"novapanel/internal/agent"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/php"
	"novapanel/internal/rpc"
	"novapanel/internal/sshkey"
)

type sshkeyAddArgs struct {
	SiteID    string `json:"site_id"`
	PublicKey string `json:"public_key"`
}
type sshkeyRemoveArgs struct {
	KeyID string `json:"key_id"`
}
type sshkeyListArgs struct {
	SiteID string `json:"site_id"`
}

func (v *sshkeyAddArgs) Validate() error {
	if !id.Valid(v.SiteID) {
		return rpc.Errf("invalid_argument", "Invalid site ID")
	}
	if _, _, err := sshkey.Parse(v.PublicKey); err != nil {
		return rpc.Errf("invalid_argument", "Invalid public key")
	}
	return nil
}
func splitKeyID(s string) (string, string, error) {
	site, fp, ok := strings.Cut(s, ".")
	if !ok || !id.Valid(site) || !strings.HasPrefix(fp, "SHA256:") {
		return "", "", rpc.Errf("invalid_argument", "Invalid key ID")
	}
	b, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(fp, "SHA256:"))
	if err != nil || len(b) != 32 || base64.RawStdEncoding.EncodeToString(b) != strings.TrimPrefix(fp, "SHA256:") {
		return "", "", rpc.Errf("invalid_argument", "Invalid key ID")
	}
	return site, fp, nil
}
func (v *sshkeyRemoveArgs) Validate() error { _, _, err := splitKeyID(v.KeyID); return err }
func (v *sshkeyListArgs) Validate() error {
	if !id.Valid(v.SiteID) {
		return rpc.Errf("invalid_argument", "Invalid site ID")
	}
	return nil
}

func init() {
	rpc.Register(rpc.Verb{Name: "sshkey.add", MinRole: rpc.Admin, Scope: "ssh:write", MFA: true, NewArgs: func() any { return &sshkeyAddArgs{} }, ObjectKey: func(a any) string { return "site:" + a.(*sshkeyAddArgs).SiteID }, Run: sshkeyAdd})
	rpc.Register(rpc.Verb{Name: "sshkey.remove", MinRole: rpc.Admin, Scope: "ssh:write", MFA: true, NewArgs: func() any { return &sshkeyRemoveArgs{} }, ObjectKey: func(a any) string { site, _, _ := splitKeyID(a.(*sshkeyRemoveArgs).KeyID); return "site:" + site }, Run: sshkeyRemove})
	rpc.Register(rpc.Verb{Name: "sshkey.list", MinRole: rpc.ReadOnly, Scope: "sites:read", ReadOnly: true, NewArgs: func() any { return &sshkeyListArgs{} }, ObjectKey: func(a any) string { return "site:" + a.(*sshkeyListArgs).SiteID }, Run: sshkeyList})
}
func sshkeyAdd(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*sshkeyAddArgs)
	res, err := sshkeyRun(ctx, a, v.SiteID, "add", v.PublicKey)
	if err != nil {
		return nil, err
	}
	return struct {
		KeyID string `json:"key_id"`
	}{res.KeyID}, nil
}
func sshkeyRemove(ctx context.Context, a *agent.Agent, args any) (any, error) {
	site, fp, err := splitKeyID(args.(*sshkeyRemoveArgs).KeyID)
	if err != nil {
		return nil, err
	}
	res, err := sshkeyRun(ctx, a, site, "remove", fp)
	if err != nil {
		return nil, err
	}
	if res.NotFound {
		return nil, rpc.Errf("not_found", "SSH key not found")
	}
	return struct{}{}, nil
}
func sshkeyList(ctx context.Context, a *agent.Agent, args any) (any, error) {
	res, err := sshkeyRun(ctx, a, args.(*sshkeyListArgs).SiteID, "list", "")
	if err != nil {
		return nil, err
	}
	return struct {
		Keys []sshkey.Key `json:"keys"`
	}{res.Keys}, nil
}
func sshkeyRun(ctx context.Context, a *agent.Agent, siteID, op, value string) (sshkey.Response, error) {
	var res sshkey.Response
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
	body, err := json.Marshal(sshkey.Request{Home: filepath.Join(a.Cfg.SitesRoot, site.Name), SiteID: siteID, Operation: op, Value: value})
	if err != nil {
		return res, err
	}
	out, _, code, err := a.Exec.Run(ctx, program, []string{"__sshkey"}, runner.RunOpts{Uid: uid, Gid: gid, Groups: []uint32{}, Stdin: bytes.NewReader(body), MaxOutput: 2 << 20})
	if err != nil || code != 0 {
		return res, rpc.Errf("agent_error", "SSH key operation failed")
	}
	if err = json.Unmarshal(out, &res); err != nil {
		return res, rpc.Errf("agent_error", "SSH key operation failed")
	}
	if res.Keys == nil {
		res.Keys = []sshkey.Key{}
	}
	return res, nil
}
