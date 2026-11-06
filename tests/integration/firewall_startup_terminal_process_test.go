package integration

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

	execpkg "novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/store"
)

// TestFirewallStartupTerminalProcess kills actual serve at durable boundaries.
// Overlays add paths, fixed fake runners and barriers, never successful substitutes
// for OpenState, audit replay, SQL transactions or journal publication.
func TestFirewallStartupTerminalProcess(t *testing.T) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		t.Skip("installed custody requires root UID/GID owned scratch")
	}
	repo, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	build := firewallDispatcherScratch(t)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	replacements := map[string]string{}
	add := func(target, name, source string) {
		p := build + "/" + name
		must(os.WriteFile(p, []byte(source), 0600))
		replacements[repo+"/"+target] = p
	}
	add("internal/firewall/startup_fixture_overlay.go", "firewall.txt", `package firewall
 import("context";"os";"time";execpkg "novapanel/internal/exec")
 func StartupFixturePlan(ctx context.Context,root string)(*FirewallStartupPlan,error){return prepareFirewallStartup(ctx,runtimeLayout{root:root,rules:root+"/rules"},runtimeManager{})}
 func StartupFixtureRecover(ctx context.Context,root,id string,r execpkg.Runner,confirm bool)error{s,e:=openInstalledJournalRecovery(ctx,runtimeLayout{root:root,rules:root+"/rules"},func()time.Time{return time.Unix(90,0)},r,r);if e!=nil{return e};defer s.Close();if confirm{return s.Confirm(ctx,id)};return s.Revert(ctx,id)}
 func startupBarrier(stage string){if os.Getenv("NP_STARTUP_BARRIER")==stage{if e:=os.WriteFile(os.Getenv("NP_STARTUP_ROOT")+"/ready",[]byte(stage),0600);e!=nil{panic(e)};select{}}}
 `)
	add("internal/agent/startup_fixture_overlay.go", "agent.txt", `package agent
 import("context";"novapanel/internal/firewall")
 func StartupFixture(a *Agent,root string){a.firewallStartupPlan=func(ctx context.Context)(*firewall.FirewallStartupPlan,error){return firewall.StartupFixturePlan(ctx,root)}}
 `)
	src, e := os.ReadFile(repo + "/internal/store/firewall_terminal.go")
	must(e)
	s := strings.Replace(string(src), `"context"`, `"context";"os"`, 1)
	anchor := "\t\tif !exists {"
	if strings.Count(s, anchor) != 1 {
		t.Fatal("store barrier anchor changed")
	}
	s = strings.Replace(s, anchor, `if os.Getenv("NP_STARTUP_BARRIER")=="file" {if e:=os.WriteFile(os.Getenv("NP_STARTUP_ROOT")+"/ready",[]byte("file"),0600);e!=nil{return e};select{}}
 `+anchor, 1)
	add("internal/store/firewall_terminal.go", "store.txt", s)
	src, e = os.ReadFile(repo + "/internal/firewall/journal.go")
	must(e)
	s = string(src)
	for _, point := range []struct{ anchor, replacement string }{
		{"\tif n, writeErr := f.Write(b); writeErr != nil || n != len(b) {", "\tif r.Version==2 && os.Getenv(\"NP_STARTUP_BARRIER\")==\"fault-write\" {return unix.EIO}\n\tif n, writeErr := f.Write(b); writeErr != nil || n != len(b) {"},
		{"\tif err = f.Sync(); err != nil {", "\tif r.Version==2 && os.Getenv(\"NP_STARTUP_BARRIER\")==\"fault-file-sync\" {return unix.EIO}\n\tif err = f.Sync(); err != nil {"},
		{"\tif err = f.Close(); err != nil {", "\tif r.Version==2 && os.Getenv(\"NP_STARTUP_BARRIER\")==\"fault-close\" {return unix.EIO}\n\tif err = f.Close(); err != nil {"},
		{"\tif err = unix.Renameat(dir, \"state.next\", dir, \"state\"); err != nil {", "\tif r.Version==2 && os.Getenv(\"NP_STARTUP_BARRIER\")==\"fault-rename\" {return unix.EIO}\n\tif err = unix.Renameat(dir, \"state.next\", dir, \"state\"); err != nil {"},
		{"\tif err = j.syncDir(); err != nil {", "\tif r.Version==2 && os.Getenv(\"NP_STARTUP_BARRIER\")==\"fault-dir-sync\" {return errors.Join(ErrAcknowledgementCommitted,unix.EIO)}\n\tif err = j.syncDir(); err != nil {"},
		{"\t\tr.Version = 2", "\t\tstartupBarrier(\"sql\")\n\t\tr.Version = 2"},
		{"\tif err = f.Close(); err != nil {", "\tif r.Version == 2 { startupBarrier(\"receipt-file\") }\n\tif err = f.Close(); err != nil {"},
		{"\tif err = j.syncDir(); err != nil {", "\tif r.Version == 2 { startupBarrier(\"receipt-rename\") }\n\tif err = j.syncDir(); err != nil {"},
		{"\t\treturn ctx.Err()\n\t})\n}\n", "\t\tstartupBarrier(\"durable\")\n\t\treturn ctx.Err()\n\t})\n}\n"},
	} {
		if !strings.Contains(s, point.anchor) {
			t.Fatal("journal barrier anchor changed", point.anchor)
		}
		s = strings.Replace(s, point.anchor, point.replacement, 1)
	}
	add("internal/firewall/journal.go", "journal.txt", s)
	add("cmd/panel-agent/startup_fixture_overlay_test.go", "child.txt", startupServeChild)
	b, e := json.Marshal(map[string]any{"Replace": replacements})
	must(e)
	must(os.WriteFile(build+"/overlay.json", b, 0600))
	goBin := ""
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(d) {
			p := d + "/go"
			if st, e := os.Stat(p); e == nil && !st.IsDir() && st.Mode()&0111 != 0 {
				goBin = p
				break
			}
		}
	}
	if goBin == "" {
		t.Fatal("Go missing")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	out, errout, code, e := (execpkg.Real{}).Run(ctx, goBin, []string{"test", "-race", "-c", "-overlay=" + build + "/overlay.json", "-o", build + "/child", "./cmd/panel-agent"}, execpkg.RunOpts{Dir: repo, Env: os.Environ()})
	if e != nil || code != 0 {
		t.Fatalf("build %v %d %s %s", e, code, out, errout)
	}
	for _, enabled := range []bool{false, true} {
		for _, outcome := range []string{"confirmed", "reverted"} {
			for _, barrier := range []string{"file", "sql", "receipt-file", "receipt-rename", "durable", "fault-write", "fault-file-sync", "fault-close", "fault-rename", "fault-dir-sync"} {
				t.Run(fmt.Sprintf("enabled-%t/%s/%s", enabled, outcome, barrier), func(t *testing.T) {
					root := firewallDispatcherScratch(t)
					for _, n := range []string{"journal", "snapshots", "restore", "restore/markers", "rules", "state"} {
						must(os.Mkdir(root+"/"+n, 0700))
					}
					must(os.WriteFile(root+"/journal/lock", nil, 0600))
					const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
					rules := map[string][]byte{}
					for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
						rules[n] = []byte("saved " + n)
						must(os.WriteFile(root+"/rules/"+n, []byte("old"), 0640))
					}
					c, e := firewall.OpenSnapshotCustody(root + "/snapshots")
					must(e)
					digest, e := c.Put(t.Context(), id, firewall.SnapshotInput{PriorEnabled: enabled, Rules: rules, IPTablesSave: []byte("v4"), IP6TablesSave: []byte("v6"), NumberedStatus: []byte("status")})
					must(e)
					must(c.Close())
					j, e := firewall.OpenJournal(root + "/journal")
					must(e)
					r := firewall.JournalRecord{Version: 1, ID: id, Unit: "novapanel-fwrevert-" + id, Deadline: 100, SnapshotSHA256: digest, PriorEnabled: enabled, State: "armed"}
					must(j.Arm(t.Context(), r))
					must(j.Close())
					st, e := store.Open(root + "/state/panel.db")
					must(e)
					must(st.CreateFirewallPending(t.Context(), firewall.Pending{ID: id, RevertUnit: r.Unit, Deadline: time.Unix(100, 0), SnapshotPath: root + "/snapshots/" + id}))
					must(st.Close())
					run := func(stage string) {
						childCtx, kill := context.WithTimeout(t.Context(), 20*time.Second)
						defer kill()
						ready := make(chan bool, 1)
						if stage != "" && !strings.HasPrefix(stage, "fault-") {
							go func() {
								deadline := time.NewTimer(15 * time.Second)
								defer deadline.Stop()
								for {
									if _, e := os.Stat(root + "/ready"); e == nil {
										ready <- true
										kill()
										return
									}
									select {
									case <-deadline.C:
										ready <- false
										kill()
										return
									case <-time.After(time.Millisecond):
									}
								}
							}()
						}
						o, er, c, e := (execpkg.Recovery{}).Run(childCtx, build+"/child", []string{"-test.run=^TestStartupServeChild$", "-test.v"}, execpkg.RunOpts{Env: append(os.Environ(), "NP_STARTUP_ROOT="+root, "NP_STARTUP_OUTCOME="+outcome, "NP_STARTUP_BARRIER="+stage)})
						if stage == "" || strings.HasPrefix(stage, "fault-") {
							if e != nil || c != 0 {
								t.Fatalf("serve restart/fault %v %d %s %s", e, c, o, er)
							}
						} else {
							if !<-ready {
								t.Fatalf("boundary not reached %s %s", o, er)
							}
							if e == nil && c == 0 {
								t.Fatal("child survived kill")
							}
							if _, e = os.Stat(root + "/a.sock"); !os.IsNotExist(e) {
								t.Fatal("listener published before boundary", e)
							}
						}
					}
					run(barrier)
					st, e = store.Open(root + "/state/panel.db")
					must(e)
					must(st.Tx(t.Context(), func(tx *sql.Tx) error {
						var n int
						if e := tx.QueryRow("SELECT count(*) FROM fw_pending").Scan(&n); e != nil {
							return e
						}
						want := 0
						if barrier == "file" {
							want = 1
						}
						if n != want {
							t.Fatalf("pending at %s = %d want %d", barrier, n, want)
						}
						return nil
					}))
					must(st.Close())
					run("")
					run("")
					j, e = firewall.OpenJournal(root + "/journal")
					must(e)
					got, p, e := j.Inspect(t.Context())
					must(e)
					r.State = outcome
					original, e := got.OriginalTerminal()
					must(e)
					if !p || got.Version != 2 || original != r {
						t.Fatal("identity lost", got)
					}
					if j.Arm(t.Context(), r) == nil {
						t.Fatal("reuse admitted")
					}
					must(j.Close())
					commands, e := os.ReadFile(root + "/commands")
					if outcome == "confirmed" {
						if !os.IsNotExist(e) {
							t.Fatal("terminal startup restored", e)
						}
					} else {
						must(e)
						want := "disable\n"
						if enabled {
							want = "--force enable\nreload\n"
						}
						if string(commands) != want {
							t.Fatal("restoration repeated", string(commands))
						}
						if _, e = os.Stat(root + "/restore/markers/" + id + ".json"); e != nil {
							t.Fatal(e)
						}
					}
					st, e = store.Open(root + "/state/panel.db")
					must(e)
					events, e := st.ListEvents(t.Context(), 100, "")
					must(e)
					count := 0
					detail, _ := json.Marshal(r)
					for _, v := range events {
						if v.ID == id {
							count++
							if string(v.Detail) != string(detail) {
								t.Fatal("event changed", v)
							}
						}
					}
					if count != 1 {
						t.Fatal("recovery event count", count)
					}
					must(st.Close())
				})
			}
		}
	}
}

const startupServeChild = `package main
import("context";"errors";"os";"strings";"testing";"time";"novapanel/internal/agent";"novapanel/internal/config";"novapanel/internal/firewall";"novapanel/internal/rpc";execpkg "novapanel/internal/exec";"novapanel/internal/validate")
type startupServeRunner struct{}
func(startupServeRunner)Run(ctx context.Context,p string,a []string,o execpkg.RunOpts)([]byte,[]byte,int,error){
 if p=="/usr/bin/systemctl"&&len(a)==3&&a[1]=="stop"&&strings.HasSuffix(a[2],".timer"){return nil,nil,0,nil}
 if p=="/usr/bin/busctl"{return []byte("{\"type\":\"a(ssssssouso)\",\"data\":[[]]}"),nil,0,nil}
 if p=="/usr/sbin/ufw"{f,e:=os.OpenFile(os.Getenv("NP_STARTUP_ROOT")+"/commands",os.O_APPEND|os.O_CREATE|os.O_WRONLY,0600);if e!=nil{return nil,nil,1,e};defer f.Close();_,e=f.WriteString(strings.Join(a," ")+"\n");return nil,nil,0,e}
 return (safeRunner{}).Run(ctx,p,a,o)
}
func TestStartupServeChild(t *testing.T){root:=os.Getenv("NP_STARTUP_ROOT");if root==""{t.Fatal("missing owned root")};const id="01ARZ3NDEKTSV4RRFFQ69G5FAV";runner:=startupServeRunner{}
 j,e:=firewall.OpenJournal(root+"/journal");if e!=nil{t.Fatal(e)};record,_,e:=j.Inspect(t.Context());j.Close();if e!=nil{t.Fatal(e)}
 if record.State=="armed"{if e=firewall.StartupFixtureRecover(t.Context(),root,id,runner,os.Getenv("NP_STARTUP_OUTCOME")=="confirmed");e!=nil{t.Fatal(e)}}
 cfg:=config.Defaults();cfg.DBPath=root+"/state/panel.db";cfg.SealKeyPath=root+"/state/seal.key";cfg.LogDir=root+"/logs";cfg.SocketPath=root+"/a.sock";cfg.WebGID=uint32(os.Getgid());cfg.SitesRoot=root+"/sites";cfg.ACMEWebroot=root+"/acme";cfg.TLSDir=root+"/tls"
 a:=agent.NewWithDependencies(cfg,runner,&validate.V{});agent.StartupFixture(a,root)
 if strings.HasPrefix(os.Getenv("NP_STARTUP_BARRIER"),"fault-"){e:=serve(t.Context(),a);if e==nil{t.Fatal("publication fault admitted")};if os.Getenv("NP_STARTUP_BARRIER")=="fault-dir-sync"&&!errors.Is(e,firewall.ErrAcknowledgementCommitted){t.Fatal("lost committed semantics",e)};if _,e:=os.Stat(cfg.SocketPath);!os.IsNotExist(e){t.Fatal("fault published socket",e)};return}
 ctx,cancel:=context.WithCancel(t.Context());defer cancel();done:=make(chan error,1);go func(){done<-serve(ctx,a)}();waitAgent(t,a,done)
 c,e:=rpc.Dial(cfg.SocketPath);if e!=nil{t.Fatal(e)};reply,e:=c.Call(t.Context(),"service.status",map[string]any{"unit":"nginx.service"},"");if e!=nil||!reply.OK{t.Fatal(reply,e)};reply,e=c.Call(t.Context(),"firewall.apply",map[string]any{"rules":[]any{map[string]any{"action":"allow","proto":"tcp","port":22}}},"");if reply.OK||reply.Code!="unsupported"{t.Fatal(reply,e)};c.Close();if a.FirewallRecovery!=nil{t.Fatal("public recovery activated")};cancel();select{case e:=<-done:if e!=nil{t.Fatal(e)};case<-time.After(5*time.Second):t.Fatal("drain timeout")};_ = errors.New
}
`
