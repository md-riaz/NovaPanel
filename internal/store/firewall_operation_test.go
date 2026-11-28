package store

import (
	"context"
	"encoding/json"
	"fmt"
	"novapanel/internal/exec"
	"novapanel/internal/id"
	"os"
	"path/filepath"
	"testing"
)

type fwRunner struct {
	calls  [][]string
	failAt int
}

func (r *fwRunner) Run(_ context.Context, path string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, append([]string{path}, args...))
	if len(r.calls) == r.failAt {
		return nil, nil, 1, nil
	}
	if len(args) > 0 && args[0] == "status" {
		return []byte("Status: inactive\n"), nil, 0, nil
	}
	return []byte("saved"), nil, 0, nil
}
func TestFirewallOperationAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		fail int
		step string
	}{{0, FirewallApplied}, {4, FirewallPrepared}, {7, FirewallSnapshotted}, {8, FirewallArmed}} {
		t.Run(fmt.Sprint(tc.fail), func(t *testing.T) {
			c := &authClock{now: 1000}
			s, e := OpenWithClock(":memory:", c)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			ctx := context.Background()
			jobID, _ := id.New()
			pendingID, _ := id.New()
			root := t.TempDir()
			rules := filepath.Join(root, "rules")
			if e = os.Mkdir(rules, 0700); e != nil {
				t.Fatal(e)
			}
			for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
				if e = os.WriteFile(filepath.Join(rules, n), []byte(n), 0600); e != nil {
					t.Fatal(e)
				}
			}
			p := FirewallPending{ID: pendingID, SnapshotPath: filepath.Join(root, "snapshot"), RevertUnit: "novapanel-fwrevert-" + pendingID + ".timer", Deadline: 2000}
			j := Job{ID: jobID, Verb: "firewall.apply", ObjectKey: "firewall", Actor: "test", State: "running", Effectful: true, Args: json.RawMessage(`{}`)}
			if e = s.CreateJob(ctx, j); e != nil {
				t.Fatal(e)
			}
			r := &fwRunner{failAt: tc.fail}
			e = s.FirewallApply(ctx, jobID, p, r, rules, nil, 22, "127.0.0.1:8080", 1)
			if (e == nil) != (tc.fail == 0) {
				t.Fatalf("apply: %v", e)
			}
			got, e := s.GetJob(ctx, jobID)
			if e != nil || got.Step != tc.step {
				t.Fatalf("job: %+v %v", got, e)
			}
			if tc.step == FirewallApplied {
				j.State = "done"
				if e = s.UpdateJob(ctx, j); e != nil {
					t.Fatal(e)
				}
				got, e = s.GetJob(ctx, jobID)
				if e != nil || got.Step != FirewallApplied || len(got.Result) == 0 {
					t.Fatalf("checkpoint lost: %+v %v", got, e)
				}
			}
			r.failAt = 0
			before := len(r.calls)
			if e = s.ReconcileFirewall(ctx, jobID, r, rules, false); e != nil {
				t.Fatal(e)
			}
			if tc.step == FirewallArmed || tc.step == FirewallApplied {
				if len(r.calls) != before {
					t.Fatal("reapplied before deadline")
				}
				c.now = 2000
				if e = s.ReconcileFirewall(ctx, jobID, r, rules, false); e != nil {
					t.Fatal(e)
				}
			}
			got, e = s.GetJob(ctx, jobID)
			if e != nil || got.Step != FirewallReverted {
				t.Fatalf("reconcile: %+v %v", got, e)
			}
			if e = s.ReconcileFirewall(ctx, jobID, r, rules, false); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestFirewallConfirm(t *testing.T) {
	ctx := context.Background()
	c := &authClock{now: 1000}
	s, e := OpenWithClock(":memory:", c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	jobID, _ := id.New()
	pendingID, _ := id.New()
	root := t.TempDir()
	rules := filepath.Join(root, "rules")
	if e = os.Mkdir(rules, 0700); e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
		if e = os.WriteFile(filepath.Join(rules, n), []byte(n), 0600); e != nil {
			t.Fatal(e)
		}
	}
	p := FirewallPending{ID: pendingID, SnapshotPath: filepath.Join(root, "snapshot"), RevertUnit: "novapanel-fwrevert-" + pendingID + ".timer", Deadline: 2000}
	j := Job{ID: jobID, Verb: "firewall.apply", ObjectKey: "firewall", Actor: "test", State: "running", Effectful: true, Args: json.RawMessage(`{}`)}
	if e = s.CreateJob(ctx, j); e != nil {
		t.Fatal(e)
	}
	r := &fwRunner{}
	if e = s.FirewallApply(ctx, jobID, p, r, rules, nil, 22, "127.0.0.1:8080", 1); e != nil {
		t.Fatal(e)
	}
	r.failAt = len(r.calls) + 1
	if e = s.ConfirmFirewall(ctx, jobID, pendingID, r); e == nil {
		t.Fatal("expected cancel failure")
	}
	r.failAt = 0
	if e = s.ReconcileFirewall(ctx, jobID, r, rules, false); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetJob(ctx, jobID)
	if e != nil || got.Step != FirewallConfirmed {
		t.Fatalf("%+v %v", got, e)
	}
	if _, e = s.FirewallPending(ctx); e == nil {
		t.Fatal("pending remains")
	}
}

func TestConfirmArmedFirewall(t *testing.T) {
	ctx := context.Background()
	c := &authClock{now: 1000}
	s, err := OpenWithClock(":memory:", c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jobID, _ := id.New()
	pendingID, _ := id.New()
	result, _ := json.Marshal(map[string]string{"pending_id": pendingID})
	if err = s.CreateJob(ctx, Job{ID: jobID, Verb: "firewall.apply", ObjectKey: "firewall", Actor: "test", State: "running", Step: FirewallArmed, Effectful: true, Args: json.RawMessage(`{}`), Result: result}); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveFirewall(ctx, FirewallPending{ID: pendingID, SnapshotPath: "/snapshot", RevertUnit: "timer", Deadline: 2000}); err != nil {
		t.Fatal(err)
	}
	r := &fwRunner{}
	if err = s.ConfirmFirewall(ctx, jobID, pendingID, r); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 || len(r.calls[0]) != 3 || r.calls[0][0] != "/usr/bin/systemctl" || r.calls[0][1] != "stop" || r.calls[0][2] != "novapanel-fwrevert-"+pendingID+".timer" {
		t.Fatalf("cancel calls: %v", r.calls)
	}
	j, err := s.GetJob(ctx, jobID)
	if err != nil || j.Step != FirewallConfirmed {
		t.Fatalf("job: %+v %v", j, err)
	}
}
