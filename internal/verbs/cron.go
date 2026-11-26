package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"novapanel/internal/agent"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/platform"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const cronUnitsDir = "/etc/systemd/system"
const systemdAnalyze = "/usr/bin/systemd-analyze"

type cronCreateArgs struct {
	SiteID   string   `json:"site_id"`
	Schedule string   `json:"schedule"`
	Argv     []string `json:"argv"`
	CronID   string   `json:"cron_id,omitempty"`
}
type cronIDArgs struct {
	CronID string `json:"cron_id"`
	SiteID string `json:"site_id,omitempty"`
}

func (v *cronIDArgs) Validate() error {
	if !id.Valid(v.CronID) {
		return rpc.Errf("invalid_argument", "Invalid cron ID")
	}
	return nil
}
func (v *cronCreateArgs) Validate() error {
	if !id.Valid(v.SiteID) || len(v.Argv) == 0 || !filepath.IsAbs(v.Argv[0]) || v.Schedule == "" {
		return rpc.Errf("invalid_argument", "Invalid cron job")
	}
	for _, s := range append(append([]string{}, v.Argv...), v.Schedule) {
		for _, c := range s {
			if unicode.IsControl(c) || c == '%' || c == '$' {
				return rpc.Errf("invalid_argument", "Invalid cron job text")
			}
		}
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "cron.create", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &cronCreateArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*cronCreateArgs).SiteID }, Run: cronCreate, Exec: cronCreateExec})
	rpc.Register(rpc.Verb{Name: "cron.delete", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &cronIDArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*cronIDArgs).SiteID }, ResolveObjectKey: func(ctx context.Context, a *agent.Agent, v any) (string, error) {
		return (cronState{a}).resolve(ctx, v)
	}, Run: func(ctx context.Context, a *agent.Agent, v any) (any, error) {
		return (cronState{a}).mutation(ctx, v, "cron.delete")
	}, Exec: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
		return (cronState{a}).mutationExec(ctx, j)
	}})
	rpc.Register(rpc.Verb{Name: "cron.enable", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &cronIDArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*cronIDArgs).SiteID }, ResolveObjectKey: func(ctx context.Context, a *agent.Agent, v any) (string, error) {
		return (cronState{a}).resolve(ctx, v)
	}, Run: func(ctx context.Context, a *agent.Agent, v any) (any, error) {
		return (cronState{a}).mutation(ctx, v, "cron.enable")
	}, Exec: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
		return (cronState{a}).mutationExec(ctx, j)
	}})
	rpc.Register(rpc.Verb{Name: "cron.disable", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &cronIDArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*cronIDArgs).SiteID }, ResolveObjectKey: func(ctx context.Context, a *agent.Agent, v any) (string, error) {
		return (cronState{a}).resolve(ctx, v)
	}, Run: func(ctx context.Context, a *agent.Agent, v any) (any, error) {
		return (cronState{a}).mutation(ctx, v, "cron.disable")
	}, Exec: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
		return (cronState{a}).mutationExec(ctx, j)
	}})
	rpc.Register(rpc.Verb{Name: "cron.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "sites:read", NewArgs: func() any { return &siteIDArgs{} }, Run: cronList})
}
func cronCommand(ctx context.Context, r runner.Runner, program string, args ...string) error {
	if r == nil {
		return rpc.Errf("agent_error", "Cron execution unavailable")
	}
	_, _, code, err := r.Run(ctx, program, args, runner.RunOpts{})
	if err != nil || code != 0 {
		return rpc.Errf("agent_error", "Cron command failed: %s", program)
	}
	return nil
}
func cronCreate(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := *args.(*cronCreateArgs)
	if v.CronID != "" {
		return nil, rpc.Errf("invalid_argument", "Cron ID is agent assigned")
	}
	if err := v.Validate(); err != nil {
		return nil, err
	}
	if a.Exec == nil {
		return nil, rpc.Errf("agent_error", "Cron execution unavailable")
	}
	_, _, code, err := a.Exec.Run(ctx, systemdAnalyze, []string{"calendar", v.Schedule}, runner.RunOpts{})
	if code > 0 {
		return nil, rpc.Errf("invalid_argument", "Invalid schedule")
	}
	if err != nil || code != 0 {
		return nil, rpc.Errf("agent_error", "Schedule validation unavailable")
	}
	s, err := a.Store.SiteByID(ctx, v.SiteID)
	if err != nil {
		return nil, siteError(err)
	}
	if s.Status != "active" {
		return nil, rpc.Errf("precondition_failed", "Site must be active")
	}
	v.CronID, err = id.New()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(v.Argv)
	if err != nil {
		return nil, err
	}
	row := store.CronJob{ID: v.CronID, SiteID: v.SiteID, Schedule: v.Schedule, ArgvJSON: string(raw), UnitName: cronUnit(v.SiteID, v.CronID), Created: a.Now()}
	return a.Jobs.EnqueueCronCreate(ctx, &v, "site:"+v.SiteID, func(ctx context.Context, tx *sql.Tx, j *jobs.Job) error { return a.Store.InsertCronTx(ctx, tx, row) })
}
func cronUnit(siteID, cronID string) string { return "novapanel-cron-" + siteID + "-" + cronID }

type cronState struct{ a *agent.Agent }

func (c cronState) resolve(ctx context.Context, args any) (string, error) {
	a := c.a
	v := args.(*cronIDArgs)
	if err := v.Validate(); err != nil {
		return "", err
	}
	if v.SiteID != "" {
		return "", rpc.Errf("invalid_argument", "Site ID is agent assigned")
	}
	row, err := a.Store.CronByID(ctx, v.CronID)
	if err != nil {
		return "", siteError(err)
	}
	v.SiteID = row.SiteID
	return "site:" + row.SiteID, nil
}
func (c cronState) mutation(ctx context.Context, args any, name string) (any, error) {
	a := c.a
	v := args.(*cronIDArgs)
	row, err := a.Store.CronByID(ctx, v.CronID)
	if err != nil {
		return nil, siteError(err)
	}
	if v.SiteID != row.SiteID {
		return nil, rpc.Errf("precondition_failed", "Cron site changed")
	}
	return a.Jobs.Enqueue(ctx, name, v, "site:"+v.SiteID)
}
func cronList(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*siteIDArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	if _, err := a.Store.SiteByID(ctx, v.SiteID); err != nil {
		return nil, siteError(err)
	}
	rows, err := a.Store.ListCronBySite(ctx, v.SiteID, 1000)
	if err != nil {
		return nil, err
	}
	type item struct {
		ID       string   `json:"id"`
		Schedule string   `json:"schedule"`
		Argv     []string `json:"argv"`
		Enabled  bool     `json:"enabled"`
	}
	out := make([]item, 0, len(rows))
	for _, r := range rows {
		var argv []string
		if json.Unmarshal([]byte(r.ArgvJSON), &argv) != nil {
			return nil, rpc.Errf("agent_error", "Invalid stored cron arguments")
		}
		out = append(out, item{r.ID, r.Schedule, argv, r.Enabled})
	}
	return struct {
		Jobs []item `json:"jobs"`
	}{out}, nil
}
func cronCreateExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v cronCreateArgs
	if json.Unmarshal(j.Args, &v) != nil || v.Validate() != nil || !id.Valid(v.CronID) {
		return rpc.Errf("invalid_argument", "Invalid cron job")
	}
	row, err := a.Store.CronByID(ctx, v.CronID)
	if err != nil {
		return siteError(err)
	}
	if row.SiteID != v.SiteID || j.ObjectKey != "site:"+row.SiteID {
		return rpc.Errf("precondition_failed", "Cron binding changed")
	}
	if err = (cronState{a}).install(ctx, row); err != nil {
		return err
	}
	j.Result, _ = json.Marshal(map[string]string{"cron_id": row.ID})
	return nil
}
func (c cronState) mutationExec(ctx context.Context, j *jobs.Job) error {
	a := c.a
	var v cronIDArgs
	if json.Unmarshal(j.Args, &v) != nil || v.Validate() != nil || !id.Valid(v.SiteID) || j.ObjectKey != "site:"+v.SiteID {
		return rpc.Errf("invalid_argument", "Invalid cron binding")
	}
	row, err := a.Store.CronByID(ctx, v.CronID)
	if errors.Is(err, sql.ErrNoRows) && j.Verb == "cron.delete" {
		j.Result = json.RawMessage(`{}`)
		return nil
	}
	if err != nil {
		return siteError(err)
	}
	if row.SiteID != v.SiteID || row.UnitName != cronUnit(row.SiteID, row.ID) {
		return rpc.Errf("precondition_failed", "Cron binding changed")
	}
	site, err := a.Store.SiteByID(ctx, row.SiteID)
	if err != nil {
		return siteError(err)
	}
	if site.Status != "active" {
		return rpc.Errf("precondition_failed", "Site must be active")
	}
	switch j.Verb {
	case "cron.enable":
		err = (cronState{a}).install(ctx, row)
	case "cron.disable":
		if err = cronCommand(ctx, a.Exec, platform.Systemctl, "disable", "--now", row.UnitName+".timer"); err == nil {
			err = a.Store.SetCronEnabled(ctx, row.ID, false)
		}
	case "cron.delete":
		if err = cronRemove(ctx, a.Exec, cronUnitsDir, row.UnitName); err == nil {
			err = a.Store.DeleteCron(ctx, row.ID)
		}
	default:
		return rpc.Errf("invalid_argument", "Invalid cron operation")
	}
	if err == nil {
		j.Result = json.RawMessage(`{}`)
	}
	return err
}
func cronRender(row store.CronJob, site store.Site, gid uint32) (string, string, error) {
	var argv []string
	if json.Unmarshal([]byte(row.ArgvJSON), &argv) != nil {
		return "", "", rpc.Errf("agent_error", "Invalid cron arguments")
	}
	v := cronCreateArgs{SiteID: row.SiteID, Schedule: row.Schedule, Argv: argv}
	if v.Validate() != nil || row.UnitName != cronUnit(row.SiteID, row.ID) || !id.Valid(row.ID) {
		return "", "", rpc.Errf("agent_error", "Invalid cron state")
	}
	quoted := make([]string, len(argv))
	for i, s := range argv {
		quoted[i] = strconv.Quote(s)
	}
	service := fmt.Sprintf("[Unit]\nDescription=NovaPanel scheduled job\n[Service]\nType=oneshot\nUser=%s\nGroup=%d\nWorkingDirectory=%s\nExecStart=%s\n", site.Name, gid, site.Docroot, strings.Join(quoted, " "))
	timer := fmt.Sprintf("[Unit]\nDescription=NovaPanel scheduled timer\n[Timer]\nOnCalendar=%s\nPersistent=false\n[Install]\nWantedBy=timers.target\n", row.Schedule)
	return service, timer, nil
}
func cronWrite(dir, name, content string) error {
	f, err := os.CreateTemp(dir, ".novapanel-cron-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0644); err == nil {
		_, err = f.WriteString(content)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}
func cronFiles(dir, name, service, timer string) error {
	if err := cronWrite(dir, name+".service", service); err != nil {
		return err
	}
	return cronWrite(dir, name+".timer", timer)
}
func (c cronState) teardown(ctx context.Context, dir, siteID string) error {
	a := c.a
	if !id.Valid(siteID) {
		return rpc.Errf("invalid_argument", "Invalid site ID")
	}
	rows, err := a.Store.ListCronBySite(ctx, siteID, -1)
	if err != nil {
		return err
	}
	prefix := "novapanel-cron-" + siteID + "-"
	names := map[string]bool{}
	for _, row := range rows {
		if !id.Valid(row.ID) || row.UnitName != cronUnit(siteID, row.ID) {
			return rpc.Errf("agent_error", "Invalid cron unit identity")
		}
		names[row.UnitName] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".service"), ".timer")
		if base != name && id.Valid(strings.TrimPrefix(base, prefix)) {
			names[base] = true
		}
	}
	_, systemdErr := os.Stat("/run/systemd/system")
	if systemdErr != nil && !os.IsNotExist(systemdErr) {
		return systemdErr
	}
	for name := range names {
		if systemdErr == nil {
			if err := cronCommand(ctx, a.Exec, platform.Systemctl, "disable", "--now", name+".timer", name+".service"); err != nil {
				return err
			}
		}
		if err := cronUnlink(dir, name); err != nil {
			return err
		}
	}
	if len(names) > 0 && systemdErr == nil {
		return cronCommand(ctx, a.Exec, platform.Systemctl, "daemon-reload")
	}
	return nil
}

func cronUnlink(dir, name string) error {
	for _, suffix := range []string{".service", ".timer"} {
		if err := os.Remove(filepath.Join(dir, name+suffix)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
func cronRemove(ctx context.Context, r runner.Runner, dir, name string) error {
	if err := cronCommand(ctx, r, platform.Systemctl, "disable", "--now", name+".timer", name+".service"); err != nil {
		return err
	}
	if err := cronUnlink(dir, name); err != nil {
		return err
	}
	return cronCommand(ctx, r, platform.Systemctl, "daemon-reload")
}
func (c cronState) install(ctx context.Context, row store.CronJob) error {
	a := c.a
	site, err := a.Store.SiteByID(ctx, row.SiteID)
	if err != nil {
		return siteError(err)
	}
	if site.Status != "active" || site.Docroot != filepath.Join(a.Cfg.SitesRoot, site.Name, "public") {
		return rpc.Errf("precondition_failed", "Site state changed")
	}
	if a.Exec == nil {
		return rpc.Errf("agent_error", "Cron execution unavailable")
	}
	_, gid, err := php.ResolveUser(ctx, a.Exec, a.Cfg.SitesRoot, site.Name, site.ID)
	if err != nil {
		return err
	}
	service, timer, err := cronRender(row, site, gid)
	if err != nil {
		return err
	}
	err = cronApply(ctx, a.Exec, cronUnitsDir, row, service, timer)
	if err != nil {
		return err
	}
	if err = a.Store.SetCronEnabled(ctx, row.ID, true); err != nil {
		return errors.Join(err, jobs.ErrRetry)
	}
	return nil
}
func cronApply(ctx context.Context, r runner.Runner, dir string, row store.CronJob, service, timer string) error {
	stage, err := os.MkdirTemp("", "novapanel-cron-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	apply := func() error {
		if err := cronFiles(stage, row.UnitName, service, timer); err != nil {
			return err
		}
		if err := cronCommand(ctx, r, systemdAnalyze, "verify", filepath.Join(stage, row.UnitName+".timer")); err != nil {
			return err
		}
		if err := cronFiles(dir, row.UnitName, service, timer); err != nil {
			return err
		}
		if err := cronCommand(ctx, r, platform.Systemctl, "daemon-reload"); err != nil {
			return err
		}
		return cronCommand(ctx, r, platform.Systemctl, "enable", "--now", row.UnitName+".timer")
	}
	if err = apply(); err == nil {
		return nil
	}
	recovery := context.WithoutCancel(ctx)
	var rollback error
	rollback = errors.Join(rollback, cronCommand(recovery, r, platform.Systemctl, "disable", "--now", row.UnitName+".timer", row.UnitName+".service"))
	if row.Enabled {
		rollback = errors.Join(rollback, cronFiles(dir, row.UnitName, service, timer))
	} else {
		rollback = errors.Join(rollback, cronUnlink(dir, row.UnitName))
	}
	rollback = errors.Join(rollback, cronCommand(recovery, r, platform.Systemctl, "daemon-reload"))
	if row.Enabled {
		rollback = errors.Join(rollback, cronCommand(recovery, r, platform.Systemctl, "enable", "--now", row.UnitName+".timer"))
	}
	if rollback != nil {
		return rpc.Errf("agent_error", "Cron rollback divergence: %v", rollback)
	}
	return err
}
