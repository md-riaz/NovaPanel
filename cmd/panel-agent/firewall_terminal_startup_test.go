package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/config"
	execpkg "novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"novapanel/internal/validate"
)

// The overlay changes only installed paths, clock and command adapters. OpenState,
// migration, audit replay, SQL commit, journal publication and serve are real.
func TestFirewallTerminalStartup(t *testing.T) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		t.Skip("installed custody requires root UID/GID scratch fixture")
	}
	if root := os.Getenv("NP_TERMINAL_CHILD"); root != "" {
		terminalStartupChild(t, root)
		return
	}
	root := t.TempDir()
	repo, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	replacements := map[string]string{}
	for _, n := range []string{"runtime_recovery.go", "journal_recovery.go"} {
		b, e := os.ReadFile(repo + "/internal/firewall/" + n)
		if e != nil {
			t.Fatal(e)
		}
		s := string(b)
		if n == "runtime_recovery.go" {
			s = strings.Replace(s, `const productionFirewallRoot = "/var/lib/novapanel/firewall"`, `var productionFirewallRoot = os.Getenv("NP_TERMINAL_CHILD")+"/firewall"`, 1)
		} else {
			s = strings.Replace(s, `time.Now, execpkg.Real{}, execpkg.Recovery{}`, `func()time.Time{return time.Unix(90,0)}, terminalFixtureRunner{}, terminalFixtureRunner{}`, 1)
		}
		p := root + "/" + n + ".txt"
		if e = os.WriteFile(p, []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
		replacements[repo+"/internal/firewall/"+n] = p
	}
	shim := `package firewall
 import("context";"errors";execpkg "novapanel/internal/exec")
 type terminalFixtureRunner struct{}
 func(terminalFixtureRunner)Run(_ context.Context,p string,a []string,_ execpkg.RunOpts)([]byte,[]byte,int,error){
 if p=="/usr/bin/systemctl"&&len(a)==3&&a[0]=="--system"&&a[1]=="stop" {return nil,nil,0,nil}
 if p=="/usr/bin/busctl" {return []byte("{\"type\":\"a(ssssssouso)\",\"data\":[[]]}"),nil,0,nil}
 if p=="/usr/sbin/ufw" {return nil,nil,0,nil};return nil,nil,1,errors.New("unexpected fixture command")}
 `
	p := root + "/shim.txt"
	if e = os.WriteFile(p, []byte(shim), 0600); e != nil {
		t.Fatal(e)
	}
	replacements[repo+"/internal/firewall/terminal_fixture_overlay.go"] = p
	b, _ := json.Marshal(map[string]any{"Replace": replacements})
	if e = os.WriteFile(root+"/overlay.json", b, 0600); e != nil {
		t.Fatal(e)
	}
	goBin := ""
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(d) {
			p := filepath.Join(d, "go")
			if st, e := os.Stat(p); e == nil && !st.IsDir() && st.Mode()&0111 != 0 {
				goBin = p
				break
			}
		}
	}
	if goBin == "" {
		t.Fatal("Go missing from absolute PATH")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	out, errout, code, e := (execpkg.Real{}).Run(ctx, goBin, []string{"test", "-race", "-c=true", "-overlay=" + root + "/overlay.json", "-o", root + "/child", "./cmd/panel-agent"}, execpkg.RunOpts{Dir: repo, Env: os.Environ()})
	if e != nil || code != 0 {
		t.Fatalf("compile: %v %d %s %s", e, code, out, errout)
	}
	out, errout, code, e = (execpkg.Real{}).Run(ctx, root+"/child", []string{"-test.run=^TestFirewallTerminalStartup$", "-test.v"}, execpkg.RunOpts{Env: append(os.Environ(), "NP_TERMINAL_CHILD="+root)})
	t.Logf("actual serve child:\n%s%s", out, errout)
	if e != nil || code != 0 {
		t.Fatalf("actual serve: %v %d", e, code)
	}
}

type terminalStatusRunner struct{}

func (terminalStatusRunner) Run(ctx context.Context, p string, args []string, o execpkg.RunOpts) ([]byte, []byte, int, error) {
	if p == "/usr/sbin/ufw" && strings.Join(args, " ") == "status numbered" {
		return []byte("Status: inactive\n"), nil, 0, nil
	}
	return (safeRunner{}).Run(ctx, p, args, o)
}

func terminalStartupChild(t *testing.T, root string) {
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, enabled := range []bool{false, true} {
		for _, outcome := range []string{"confirmed", "reverted"} {
			for _, kind := range []string{"normal", "file-replay", "receipt-file-replay", "receipt-pending", "receipt-missing", "receipt-collision", "orphan", "absence", "legacy", "delete-fault", "commit-fault"} {
				t.Run(fmt.Sprintf("enabled-%t/%s/%s", enabled, outcome, kind), func(t *testing.T) {
					// Production root is fixed in this child. Remove only this owned prior fixture.
					fw := root + "/firewall"
					must(os.RemoveAll(fw))
					for _, n := range []string{"", "/journal", "/snapshots", "/restore", "/restore/markers", "/rules"} {
						must(os.Mkdir(fw+n, 0700))
					}
					must(os.WriteFile(fw+"/journal/lock", nil, 0600))
					const pending = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
					rules := map[string][]byte{}
					for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
						rules[n] = []byte("fixture " + n)
						must(os.WriteFile(fw+"/rules/"+n, []byte("old"), 0640))
					}
					custody, e := firewall.OpenSnapshotCustody(fw + "/snapshots")
					must(e)
					digest, e := custody.Put(t.Context(), pending, firewall.SnapshotInput{PriorEnabled: enabled, Rules: rules, IPTablesSave: []byte("v4"), IP6TablesSave: []byte("v6"), NumberedStatus: []byte("status")})
					must(e)
					must(custody.Close())
					j, e := firewall.OpenJournal(fw + "/journal")
					must(e)
					r := firewall.JournalRecord{Version: 1, ID: pending, Unit: "novapanel-fwrevert-" + pending, Deadline: 100, SnapshotSHA256: digest, PriorEnabled: enabled, State: "armed"}
					must(j.Arm(t.Context(), r))
					must(j.Close())
					recovery, e := firewall.OpenInstalledJournalRecovery(t.Context())
					must(e)
					if outcome == "confirmed" {
						must(recovery.Confirm(t.Context(), pending))
					} else {
						must(recovery.Revert(t.Context(), pending))
					}
					must(recovery.Close())
					r.State = outcome
					cfg := config.Defaults()
					d, e := os.MkdirTemp(filepath.Dir(root), "s-")
					must(e)
					t.Cleanup(func() { os.RemoveAll(d) })
					cfg.DBPath = d + "/state/panel.db"
					cfg.SealKeyPath = d + "/state/seal.key"
					cfg.LogDir = d + "/log"
					cfg.SocketPath = d + "/a.sock"
					cfg.TLSDir = d + "/tls"
					cfg.SitesRoot = d + "/sites"
					cfg.ACMEWebroot = d + "/acme"
					cfg.WebGID = uint32(os.Getgid())
					must(os.MkdirAll(filepath.Dir(cfg.DBPath), 0700))
					s, e := store.Open(cfg.DBPath)
					must(e)
					must(s.CreateFirewallPending(t.Context(), firewall.Pending{ID: pending, RevertUnit: r.Unit, SnapshotPath: fw + "/snapshots/" + pending, Deadline: time.Unix(100, 0)}))
					must(s.Close())
					receipt := strings.HasPrefix(kind, "receipt-")
					if receipt || kind == "file-replay" {
						a := agent.NewWithDependencies(cfg, terminalStatusRunner{}, &validate.V{})
						cleanup, e := a.OpenState(t.Context())
						must(e)
						must(a.CommitFirewallTerminal(t.Context()))
						must(cleanup())
						s, e = store.Open(cfg.DBPath)
						must(e)
						must(s.Tx(t.Context(), func(tx *sql.Tx) error {
							if kind == "file-replay" || kind == "receipt-file-replay" || kind == "receipt-missing" {
								if _, e := tx.Exec("DELETE FROM events WHERE id=?", pending); e != nil {
									return e
								}
							}
							if kind == "receipt-collision" {
								if _, e := tx.Exec("UPDATE events SET actor='collision' WHERE id=?", pending); e != nil {
									return e
								}
							}
							return nil
						}))
						if kind == "file-replay" || kind == "receipt-pending" {
							must(s.CreateFirewallPending(t.Context(), firewall.Pending{ID: pending, RevertUnit: r.Unit, SnapshotPath: "/retained", Deadline: time.Unix(100, 0)}))
						}
						must(s.Close())
						if kind == "receipt-missing" || kind == "receipt-collision" {
							must(os.Remove(cfg.LogDir + "/audit.log"))
						}
						if receipt {
							ack := r
							ack.Version = 2
							ack.State = "acknowledged"
							ack.TerminalState = outcome
							raw, _ := json.Marshal(ack)
							must(os.WriteFile(fw+"/journal/state", raw, 0600))
						}
					}
					if kind == "orphan" || kind == "absence" {
						must(os.Remove(fw + "/journal/state"))
					}
					if kind == "absence" {
						s, e = store.Open(cfg.DBPath)
						must(e)
						must(s.Tx(t.Context(), func(tx *sql.Tx) error { _, e := tx.Exec("DELETE FROM fw_pending"); return e }))
						must(s.Close())
					}
					if kind == "legacy" {
						ack := r
						ack.State = "acknowledged"
						raw, _ := json.Marshal(ack)
						must(os.WriteFile(fw+"/journal/state", raw, 0600))
					}
					if kind == "delete-fault" || kind == "commit-fault" {
						s, e = store.Open(cfg.DBPath)
						must(e)
						must(s.Tx(t.Context(), func(tx *sql.Tx) error {
							statement := `CREATE TRIGGER terminal_failure BEFORE DELETE ON fw_pending BEGIN SELECT RAISE(ABORT,'delete fault');END;`
							if kind == "commit-fault" {
								statement = `CREATE TABLE startup_parent(id INTEGER PRIMARY KEY);CREATE TABLE startup_child(id INTEGER REFERENCES startup_parent(id) DEFERRABLE INITIALLY DEFERRED);CREATE TRIGGER terminal_failure AFTER DELETE ON fw_pending BEGIN INSERT INTO startup_child VALUES(1);END;`
							}
							_, e := tx.Exec(statement)
							return e
						}))
						must(s.Close())
					}
					refuse := kind == "receipt-pending" || kind == "receipt-missing" || kind == "receipt-collision" || kind == "orphan" || kind == "legacy" || kind == "delete-fault" || kind == "commit-fault"
					if refuse {
						before, _ := os.ReadFile(fw + "/journal/state")
						a := agent.NewWithDependencies(cfg, terminalStatusRunner{}, &validate.V{})
						if e := serve(t.Context(), a); e == nil {
							t.Fatal("inconsistent startup admitted")
						}
						if _, e := os.Stat(cfg.SocketPath); !os.IsNotExist(e) {
							t.Fatal("refusal published socket", e)
						}
						after, _ := os.ReadFile(fw + "/journal/state")
						if string(before) != string(after) {
							t.Fatal("refusal changed evidence")
						}
						s, e = store.Open(cfg.DBPath)
						must(e)
						must(s.Close())
						return
					}
					for boot := 0; boot < 2; boot++ {
						a := agent.NewWithDependencies(cfg, terminalStatusRunner{}, &validate.V{})
						ctx, cancel := context.WithCancel(t.Context())
						done := make(chan error, 1)
						go func() { done <- serve(ctx, a) }()
						defer cancel()
						waitAgent(t, a, done)
						c, e := rpc.Dial(cfg.SocketPath)
						must(e)
						reply, e := c.Call(t.Context(), "service.status", map[string]any{"unit": "nginx.service"}, "")
						if e != nil || !reply.OK {
							t.Fatalf("status: %+v %v", reply, e)
						}
						reply, _ = c.Call(t.Context(), "firewall.apply", map[string]any{"rules": []any{map[string]any{"action": "allow", "proto": "tcp", "port": 22}}}, "")
						if reply.OK || reply.Code != "unsupported" {
							t.Fatalf("apply activated: %+v", reply)
						}
						must(c.Close())
						cancel()
						select {
						case e := <-done:
							must(e)
						case <-time.After(5 * time.Second):
							t.Fatal("drain timeout")
						}
						if kind == "absence" {
							if _, e := os.Stat(fw + "/journal/state"); !os.IsNotExist(e) {
								t.Fatal("absence invented record", e)
							}
							continue
						}
						raw, e := os.ReadFile(fw + "/journal/state")
						must(e)
						if !strings.Contains(string(raw), `"version":2`) || !strings.Contains(string(raw), `"terminal_state":"`+outcome+`"`) {
							t.Fatalf("lost outcome: %s", raw)
						}
						s, e = store.Open(cfg.DBPath)
						must(e)
						events, e := s.ListEvents(t.Context(), 100, "")
						must(e)
						count := 0
						expected, _ := json.Marshal(r)
						for _, v := range events {
							if v.ID == pending {
								count++
								if string(v.Detail) != string(expected) {
									t.Fatalf("changed v1 event: %s", v.Detail)
								}
							}
						}
						if count != 1 {
							t.Fatalf("recovery events %d", count)
						}
						must(s.Close())
						if a.FirewallRecovery != nil {
							t.Fatal("public recovery activated")
						}
					}
				})
			}
		}
	}
}
