package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"novapanel/internal/agent"
	"novapanel/internal/config"
	"novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFirewallVerbContracts(t *testing.T) {
	for _, name := range []string{"firewall.apply", "firewall.confirm", "firewall.revert"} {
		v := rpc.Registry[name]
		if v.MinRole != rpc.Admin || v.Scope != "firewall:write" || !v.MFA {
			t.Fatalf("%s: %+v", name, v)
		}
	}
	v := rpc.Registry["firewall.apply"]
	if !v.Slow || !v.Effectful || v.ObjectKey(nil) != "firewall" {
		t.Fatal("apply attributes")
	}
	if _, err := firewall.Plan([]firewall.Rule{{Action: "deny", Proto: "tcp", Port: 22}}, 22, "127.0.0.1:8080"); err == nil {
		t.Fatal("accepted pinned deny")
	}
	if (&firewallPendingArgs{PendingID: "bad"}).Validate() == nil {
		t.Fatal("accepted invalid pending id")
	}
}

func TestUnknownWebListenPinsPanelPort(t *testing.T) {
	a := agent.New()
	a.Cfg.WebListen = "127.0.0.1:8080"
	a.Cfg.WebListenKnown = false
	plan, err := firewall.Plan(nil, 22, firewallWebListen(a))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, args := range plan {
		if args[len(args)-1] == "8080" {
			found = true
		}
	}
	if !found {
		t.Fatalf("panel port not pinned: %v", plan)
	}
}

type startupFirewallRunner struct {
	active   bool
	restores int
}

func (r *startupFirewallRunner) Run(_ context.Context, path string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	if path == "/usr/bin/systemctl" && len(args) > 0 && args[0] == "is-active" {
		if r.active {
			return nil, nil, 0, nil
		}
		return nil, nil, 3, nil
	}
	if path == "/usr/sbin/ufw" {
		r.restores++
	}
	return nil, nil, 0, nil
}
func TestRecoverFirewallTimerPresence(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, step := range []string{store.FirewallArmed, store.FirewallApplied} {
			t.Run(fmt.Sprintf("active=%t/%s", active, step), func(t *testing.T) {
				root := t.TempDir()
				rules := filepath.Join(root, "rules")
				snap := filepath.Join(root, "snapshot")
				if err := os.MkdirAll(filepath.Join(snap, "ufw"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(rules, 0700); err != nil {
					t.Fatal(err)
				}
				for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
					if err := os.WriteFile(filepath.Join(snap, "ufw", n), []byte(n), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(snap, "enabled"), []byte("0\n"), 0600); err != nil {
					t.Fatal(err)
				}
				s, err := store.Open(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				ctx := context.Background()
				job, _ := id.New()
				pending, _ := id.New()
				res, _ := json.Marshal(map[string]string{"pending_id": pending})
				if err = s.CreateJob(ctx, store.Job{ID: job, Verb: "firewall.apply", ObjectKey: "firewall", Actor: "test", State: "running", Step: step, Effectful: true, Args: json.RawMessage(`{}`), Result: res}); err != nil {
					t.Fatal(err)
				}
				if err = s.ReserveFirewall(ctx, store.FirewallPending{ID: pending, SnapshotPath: snap, RevertUnit: "novapanel-fwrevert-" + pending + ".timer", Deadline: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
					t.Fatal(err)
				}
				runner := &startupFirewallRunner{active: active}
				a := agent.New()
				a.Store = s
				a.Exec = runner
				a.Cfg.DBPath = filepath.Join(root, "panel.db")
				if err = (firewallRecovery{agent: a, rulesDir: rules}).run(ctx); err != nil {
					t.Fatal(err)
				}
				j, err := s.GetJob(ctx, job)
				if err != nil {
					t.Fatal(err)
				}
				if active && (j.Step != step || runner.restores != 0) || !active && (j.Step != store.FirewallReverted || runner.restores != 1) {
					t.Fatalf("job=%s restores=%d", j.Step, runner.restores)
				}
			})
		}
	}
}

func TestSharedWebBindIsPinned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-listen")
	if err := os.WriteFile(path, []byte("0.0.0.0:9443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	webBind, _, err := config.ReadWebListen(path)
	if err != nil {
		t.Fatal(err)
	}
	agentBind, known, err := config.ReadWebListen(path)
	if err != nil || !known {
		t.Fatalf("%v %v", known, err)
	}
	a := agent.New()
	a.Cfg.WebListen = agentBind
	a.Cfg.WebListenKnown = known
	plan, err := firewall.Plan(nil, 22, firewallWebListen(a))
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(webBind)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range plan {
		if args[len(args)-1] == port {
			return
		}
	}
	t.Fatalf("web bind %s is not pinned: %v", webBind, plan)
}

// flakyDisableRunner fails the first failDisables ufw disables, which is the lost-timer restore step.
type flakyDisableRunner struct {
	failDisables int
	disables     int
}

func (r *flakyDisableRunner) Run(_ context.Context, path string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	if path == "/usr/bin/systemctl" && len(args) > 0 && args[0] == "is-active" {
		return nil, nil, 3, nil
	}
	if path == "/usr/sbin/ufw" && len(args) == 2 && args[0] == "--force" && args[1] == "disable" {
		r.disables++
		if r.disables <= r.failDisables {
			return nil, nil, 1, nil
		}
	}
	return nil, nil, 0, nil
}

func TestStartFirewallRecoveryRetriesFailedRestore(t *testing.T) {
	root := t.TempDir()
	rules := filepath.Join(root, "rules")
	snap := filepath.Join(root, "snapshot")
	if err := os.MkdirAll(filepath.Join(snap, "ufw"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rules, 0700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
		if err := os.WriteFile(filepath.Join(snap, "ufw", n), []byte(n), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(snap, "enabled"), []byte("0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	job, _ := id.New()
	pending, _ := id.New()
	res, _ := json.Marshal(map[string]string{"pending_id": pending})
	if err = s.CreateJob(ctx, store.Job{ID: job, Verb: "firewall.apply", ObjectKey: "firewall", Actor: "test", State: "running", Step: store.FirewallApplied, Effectful: true, Args: json.RawMessage(`{}`), Result: res}); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveFirewall(ctx, store.FirewallPending{ID: pending, SnapshotPath: snap, RevertUnit: "novapanel-fwrevert-" + pending + ".timer", Deadline: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	runner := &flakyDisableRunner{failDisables: 1}
	a := agent.New()
	a.Store = s
	a.Exec = runner
	a.Cfg.DBPath = filepath.Join(root, "panel.db")
	notices := 0
	stop := startFirewallRecovery(ctx, firewallRecovery{agent: a, rulesDir: rules}, time.Millisecond, time.Millisecond, func(error, time.Duration) { notices++ })
	defer stop()
	if notices != 1 {
		t.Fatalf("first failed attempt not reported: %d", notices)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = s.FirewallPending(ctx); errors.Is(err, sql.ErrNoRows) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("change never reverted: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	j, err := s.GetJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if j.Step != store.FirewallReverted || runner.disables != 2 {
		t.Fatalf("step %s disables %d", j.Step, runner.disables)
	}
}

// A revert that failed its timer-run retries stays pending while the daemon runs; the sweep must retry it.
func TestFirewallSweepRetriesPendingRevert(t *testing.T) {
	root := t.TempDir()
	rules := filepath.Join(root, "rules")
	snap := filepath.Join(root, "snapshot")
	if err := os.MkdirAll(filepath.Join(snap, "ufw"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rules, 0700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
		if err := os.WriteFile(filepath.Join(snap, "ufw", n), []byte(n), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(snap, "enabled"), []byte("0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	job, _ := id.New()
	pending, _ := id.New()
	res, _ := json.Marshal(map[string]string{"pending_id": pending})
	if err = s.CreateJob(ctx, store.Job{ID: job, Verb: "firewall.apply", ObjectKey: "firewall", Actor: "test", State: "running", Step: store.FirewallApplied, Effectful: true, Args: json.RawMessage(`{}`), Result: res}); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveFirewall(ctx, store.FirewallPending{ID: pending, SnapshotPath: snap, RevertUnit: "novapanel-fwrevert-" + pending + ".timer", Deadline: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	// The first sweep pass fails its restore, so only a later pass can finish the revert.
	runner := &flakyDisableRunner{failDisables: 1}
	a := agent.New()
	a.Store = s
	a.Exec = runner
	a.Cfg.DBPath = filepath.Join(root, "panel.db")
	notices := 0
	stop := startFirewallSweep(ctx, firewallRecovery{agent: a, rulesDir: rules}, time.Millisecond, func(error, time.Duration) { notices++ })
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = s.FirewallPending(ctx); errors.Is(err, sql.ErrNoRows) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sweep never reverted: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	if notices != 1 || runner.disables != 2 {
		t.Fatalf("notices %d disables %d", notices, runner.disables)
	}
	j, err := s.GetJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if j.Step != store.FirewallReverted {
		t.Fatalf("step %s", j.Step)
	}
}
