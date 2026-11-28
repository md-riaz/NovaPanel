package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

type firewallApplyArgs struct {
	Rules     []firewall.Rule `json:"rules"`
	WindowMin int             `json:"window_min,omitempty"`
}
type firewallPendingArgs struct {
	PendingID string `json:"pending_id"`
}

func (v *firewallPendingArgs) Validate() error {
	if !id.Valid(v.PendingID) {
		return rpc.Errf("invalid_argument", "invalid pending id")
	}
	return nil
}
func (v *firewallApplyArgs) Validate() error {
	if v.WindowMin < 0 || v.WindowMin > 1440 {
		return rpc.Errf("invalid_argument", "invalid firewall window")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "firewall.status", MinRole: rpc.Admin, Scope: "system:read", ReadOnly: true, NewArgs: func() any { return &emptyArgs{} }, Run: firewallStatus})
	rpc.Register(rpc.Verb{Name: "firewall.apply", MinRole: rpc.Admin, Scope: "firewall:write", Slow: true, Effectful: true, MFA: true, NewArgs: func() any { return &firewallApplyArgs{} }, ObjectKey: func(any) string { return "firewall" }, Run: firewallApply, Exec: firewallApplyExec})
	rpc.Register(rpc.Verb{Name: "firewall.confirm", MinRole: rpc.Admin, Scope: "firewall:write", MFA: true, NewArgs: func() any { return &firewallPendingArgs{} }, Run: firewallConfirm})
	rpc.Register(rpc.Verb{Name: "firewall.revert", MinRole: rpc.Admin, Scope: "firewall:write", MFA: true, NewArgs: func() any { return &firewallPendingArgs{} }, Run: firewallRevert})
}
func firewallLock(ctx context.Context, a *agent.Agent) (func() error, error) {
	return firewall.Lock(ctx, filepath.Join(filepath.Dir(a.Cfg.DBPath), "firewall.lock"))
}
func firewallSSHPort(ctx context.Context, a *agent.Agent) (int, error) {
	out, _, code, err := a.Exec.Run(ctx, "/usr/sbin/sshd", []string{"-T"}, exec.RunOpts{MaxOutput: 1 << 20})
	if err != nil || code != 0 {
		return 0, rpc.Errf("precondition_failed", "cannot determine SSH port")
	}
	port := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "port" {
			n, e := strconv.Atoi(fields[1])
			if e != nil || n < 1 || n > 65535 || port != 0 {
				return 0, rpc.Errf("precondition_failed", "ambiguous SSH port")
			}
			port = n
		}
	}
	if port == 0 || port != a.Cfg.SSHPort {
		return 0, rpc.Errf("precondition_failed", "configured SSH port does not match sshd")
	}
	return port, nil
}
func firewallApply(ctx context.Context, a *agent.Agent, args any) (any, error) {
	if a.Store == nil || a.Jobs == nil || a.Exec == nil {
		return nil, rpc.Errf("precondition_failed", "firewall unavailable")
	}
	v := args.(*firewallApplyArgs)
	port, err := firewallSSHPort(ctx, a)
	if err != nil {
		return nil, err
	}
	if _, err = firewall.Plan(v.Rules, port, firewallWebListen(a)); err != nil {
		return nil, err
	}
	if _, err = a.Store.FirewallPending(ctx); err == nil {
		return nil, rpc.Errf("conflict", "firewall change pending")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return a.Jobs.Enqueue(ctx, "firewall.apply", v, "firewall")
}
func firewallApplyExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	if j == nil || j.ObjectKey != "firewall" || a.Store == nil || a.Exec == nil {
		return rpc.Errf("precondition_failed", "firewall unavailable")
	}
	var v firewallApplyArgs
	if err := json.Unmarshal(j.Args, &v); err != nil {
		return err
	}
	if err := v.Validate(); err != nil {
		return err
	}
	unlock, err := firewallLock(ctx, a)
	if err != nil {
		return err
	}
	defer unlock()
	if j.Attempts > 1 {
		return rpc.Errf("interrupted", "firewall apply requires reconciliation")
	}
	port, err := firewallSSHPort(ctx, a)
	if err != nil {
		return err
	}
	window := v.WindowMin
	if window == 0 {
		window = a.Cfg.FirewallWindowMin
	}
	if window <= 0 || window > 1440 {
		return rpc.Errf("invalid_argument", "invalid firewall window")
	}
	pendingID, err := id.New()
	if err != nil {
		return err
	}
	root := filepath.Join(filepath.Dir(a.Cfg.DBPath), "firewall-snapshots")
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	p := store.FirewallPending{ID: pendingID, SnapshotPath: filepath.Join(root, pendingID), RevertUnit: "novapanel-fwrevert-" + pendingID + ".timer", Deadline: a.Now() + int64(window)*int64(time.Minute/time.Millisecond)}
	return a.Store.FirewallApply(ctx, j.ID, p, a.Exec, "/etc/ufw", v.Rules, port, firewallWebListen(a), window)
}
func resolveFirewallJob(ctx context.Context, s *store.Store, pendingID string) (string, error) {
	p, err := s.FirewallPending(ctx)
	if errors.Is(err, sql.ErrNoRows) || err == nil && p.ID != pendingID {
		return "", rpc.Errf("not_found", "firewall change not found")
	}
	if err != nil {
		return "", err
	}
	job, err := s.FirewallJob(ctx, pendingID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", rpc.Errf("not_found", "firewall change not found")
	}
	return job, err
}
func firewallConfirm(ctx context.Context, a *agent.Agent, args any) (any, error) {
	unlock, err := firewallLock(ctx, a)
	if err != nil {
		return nil, err
	}
	defer unlock()
	pending := args.(*firewallPendingArgs).PendingID
	job, err := resolveFirewallJob(ctx, a.Store, pending)
	if err != nil {
		return nil, err
	}
	if err = a.Store.ConfirmFirewall(ctx, job, pending, a.Exec); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}
func firewallRevert(ctx context.Context, a *agent.Agent, args any) (any, error) {
	unlock, err := firewallLock(ctx, a)
	if err != nil {
		return nil, err
	}
	defer unlock()
	job, err := resolveFirewallJob(ctx, a.Store, args.(*firewallPendingArgs).PendingID)
	if err != nil {
		return nil, err
	}
	if err = a.Store.ReconcileFirewall(ctx, job, a.Exec, "/etc/ufw", true); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

// RecoverFirewall resolves any interrupted pending change before admission opens.
func RecoverFirewall(ctx context.Context, a *agent.Agent) error {
	unlock, err := firewallLock(ctx, a)
	if err != nil {
		return err
	}
	defer unlock()
	p, err := a.Store.FirewallPending(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	job, err := a.Store.FirewallJob(ctx, p.ID)
	if err != nil {
		return err
	}
	j, err := a.Store.GetJob(ctx, job)
	if err != nil {
		return err
	}
	if j.Step == store.FirewallArmed || j.Step == store.FirewallApplied || j.Step == store.FirewallSnapshotted || j.Step == store.FirewallConfirmed {
		_, _, code, e := a.Exec.Run(ctx, "/usr/bin/systemctl", []string{"is-active", "--quiet", p.RevertUnit}, exec.RunOpts{})
		if e != nil || code != 0 {
			return a.Store.ReconcileFirewallLostTimer(ctx, job, a.Exec, "/etc/ufw")
		}
	}
	return a.Store.ReconcileFirewall(ctx, job, a.Exec, "/etc/ufw", false)
}

func firewallStatus(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	enabled, rules, err := firewall.Status(ctx, a.Exec)
	if err != nil {
		return nil, err
	}
	type pending struct {
		ID       string `json:"id"`
		Deadline int64  `json:"deadline"`
	}
	var p *pending
	row, err := a.Store.FirewallPending(ctx)
	if err == nil {
		p = &pending{ID: row.ID, Deadline: row.Deadline}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return struct {
		Enabled bool                  `json:"enabled"`
		Rules   []firewall.StatusRule `json:"rules"`
		Pending *pending              `json:"pending,omitempty"`
	}{enabled, rules, p}, nil
}

// Unknown bind provenance keeps the configured panel port open.
func firewallWebListen(a *agent.Agent) string {
	if a.Cfg.WebListenKnown {
		return a.Cfg.WebListen
	}
	_, port, err := net.SplitHostPort(a.Cfg.WebListen)
	if err != nil {
		return ""
	}
	return net.JoinHostPort("", port)
}
