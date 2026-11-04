package platform

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	runner "novapanel/internal/exec"
	"novapanel/internal/validate"
)

var errSiteExecution = errors.New("platform: site execution identity refused")

type siteRunner struct {
	runner     runner.Runner
	root, name string
	uid, gid   uint32
}

// NewSiteRunner binds child credentials to a reserved, provisioned non-login
// account. It neither provisions accounts nor attests filesystem custody,
// quiescence, authorization or exclusion of trusted root identity changes.
// The supplied runner is the existing trusted process boundary, never RPC input.
func NewSiteRunner(ctx context.Context, r runner.Runner, sites, name string, uid, gid uint32) (runner.Runner, error) {
	if r == nil || uid == 0 || gid == 0 || !filepath.IsAbs(sites) || filepath.Clean(sites) != sites || sites == "/" || strings.ContainsAny(sites, "\x00\r\n") || (&validate.V{}).SiteName(name) != nil {
		return nil, errSiteExecution
	}
	s := &siteRunner{runner: r, root: sites, name: name, uid: uid, gid: gid}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *siteRunner) lookup(ctx context.Context, database, key string, fields int) ([]string, error) {
	out, stderr, code, err := s.runner.Run(ctx, "/usr/bin/getent", []string{database, key}, runner.RunOpts{MaxOutput: 4097})
	defer clear(out)
	defer clear(stderr)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || code != 0 || len(out) == 0 || len(out) > 4096 || len(stderr) != 0 {
		return nil, errSiteExecution
	}
	line := strings.TrimSuffix(string(out), "\n")
	if strings.ContainsAny(line, "\x00\r\n") {
		return nil, errSiteExecution
	}
	f := strings.Split(line, ":")
	if len(f) != fields || f[0] != key {
		return nil, errSiteExecution
	}
	return f, nil
}
func numericIdentity(text string) (uint32, bool) {
	n, err := strconv.ParseUint(text, 10, 32)
	return uint32(n), err == nil && n != 0 && strconv.FormatUint(n, 10) == text
}
func (s *siteRunner) check(ctx context.Context) error {
	f, err := s.lookup(ctx, "passwd", s.name, 7)
	if err != nil {
		return err
	}
	uid, ok := numericIdentity(f[2])
	gid, gok := numericIdentity(f[3])
	if !ok || !gok || uid != s.uid || gid != s.gid || f[5] != filepath.Join(s.root, s.name) || f[6] != "/usr/sbin/nologin" {
		return errSiteExecution
	}
	web, err := s.lookup(ctx, "passwd", "www-data", 7)
	if err != nil {
		return err
	}
	webUID, ok := numericIdentity(web[2])
	webGID, gok := numericIdentity(web[3])
	if !ok || !gok || s.uid == webUID || s.gid == webGID {
		return errSiteExecution
	}
	group, err := s.lookup(ctx, "group", "www-data", 4)
	if err != nil {
		return err
	}
	webGID, ok = numericIdentity(group[2])
	if !ok || s.gid == webGID {
		return errSiteExecution
	}
	return nil
}

// Run rechecks the original account and strips supplementary groups. Credentials
// cannot be overridden by a caller. Other typed options, including the explicit
// replacement environment, secret stdin and output bounds retain their meaning.
func (s *siteRunner) Run(ctx context.Context, prog string, args []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	if (o.Uid != 0 && o.Uid != s.uid) || (o.Gid != 0 && o.Gid != s.gid) || len(o.Groups) != 0 {
		return nil, nil, -1, errSiteExecution
	}
	if err := s.check(ctx); err != nil {
		return nil, nil, -1, err
	}
	o.Uid, o.Gid, o.Groups = s.uid, s.gid, []uint32{}
	return s.runner.Run(ctx, prog, args, o)
}
