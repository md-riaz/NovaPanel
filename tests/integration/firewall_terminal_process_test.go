package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	execpkg "novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFirewallTerminalCommitProcess connects real OpenState and ordinary replay
// to owned child death at real file/SQL boundaries. Only paths, runners and
// scheduling barriers are overlaid. Recovery, audit and SQL logic are retained.
func TestFirewallTerminalCommitProcess(t *testing.T) { firewallTerminalMigratedProcess(t, 0) }

func firewallTerminalMigratedProcess(t *testing.T, prefix int) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		t.Skip("installed recovery needs root UID/GID fixture")
	}
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	repo := filepath.Clean(cwd + "/../..")
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
	add("internal/firewall/terminal_fixture_overlay.go", "firewall.txt", `package firewall
import("context";"time";"errors";"os";execpkg "novapanel/internal/exec")
func TerminalFixtureRecover(ctx context.Context,root,id string,r execpkg.Runner,confirm bool)error{
 s,e:=openInstalledJournalRecovery(ctx,runtimeLayout{root:root,rules:root+"/rules"},func()time.Time{if os.Getenv("NP_TERMINAL_RETRY")=="yes"{return time.Unix(101,0)};return time.Unix(90,0)},r,r);if e!=nil{return e};defer s.Close()
 if os.Getenv("NP_TERMINAL_OUTCOME")=="confirm-sync"{n:=0;s.journal.syncDirectory=func()error{n++;if n==2{return errors.New("confirmation final sync")};return s.journal.dir.Sync()}}
 if confirm{return s.Confirm(ctx,id)};return s.Revert(ctx,id)
}
func TerminalFixturePreflight(ctx context.Context,root string)error{return startupFirewall(ctx,runtimeLayout{root:root},runtimeManager{})}
func TerminalFixtureFire(ctx context.Context,root,id string,r execpkg.Runner)error{return recoverInstalled(ctx,id,runtimeLayout{root:root,rules:root+"/rules"},func()time.Time{return time.Unix(101,0)},r)}
`)
	add("internal/agent/terminal_fixture_overlay.go", "agent.txt", `package agent
import("context";"novapanel/internal/firewall")
func TerminalFixtureJournal(a *Agent,root string){a.firewallTerminalJournal=func(ctx context.Context)(*firewall.Journal,error){return firewall.OpenJournalContext(ctx,root+"/journal")}}
`)
	// Pause immediately after appendDurable returns success, before SQL insertion.
	src, e := os.ReadFile(repo + "/internal/store/firewall_terminal.go")
	must(e)
	s := strings.Replace(string(src), `"context"`, `"context";"os"`, 1)
	s = strings.Replace(s, `if !exists {`, `if os.Getenv("NP_TERMINAL_BARRIER")=="file" {if e:=os.WriteFile(os.Getenv("NP_TERMINAL_ROOT")+"/ready",[]byte("file synced"),0600);e!=nil{return e};select{}}
 if !exists {`, 1)
	add("internal/store/firewall_terminal.go", "store.txt", s)
	// Pause after the actual SQL transaction has returned success, before Agent return.
	src, e = os.ReadFile(repo + "/internal/agent/firewall_terminal.go")
	must(e)
	s = strings.Replace(string(src), `"context"`, `"context";"os"`, 1)
	s = strings.Replace(s, `return a.Audit.CommitFirewallTerminal(ctx, j)`, `err=a.Audit.CommitFirewallTerminal(ctx,j)
 if err==nil&&os.Getenv("NP_TERMINAL_BARRIER")=="sql" {if e:=os.WriteFile(os.Getenv("NP_TERMINAL_ROOT")+"/ready",[]byte("sql committed"),0600);e!=nil{return e};select{}}
 return err`, 1)
	add("internal/agent/firewall_terminal.go", "consumer.txt", s)
	add("cmd/panel-agent/terminal_fixture_overlay_test.go", "child.txt", terminalProcessChild)
	b, e := json.Marshal(map[string]any{"Replace": replacements})
	must(e)
	must(os.WriteFile(build+"/overlay.json", b, 0600))
	goBinary := ""
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(d) {
			p := d + "/go"
			if st, e := os.Stat(p); e == nil && !st.IsDir() && st.Mode()&0111 != 0 {
				goBinary = p
				break
			}
		}
	}
	if goBinary == "" {
		t.Fatal("go missing")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	out, errout, code, e := (execpkg.Real{}).Run(ctx, goBinary, []string{"test", "-race", "-c", "-overlay=" + build + "/overlay.json", "-o", build + "/child", "./cmd/panel-agent"}, execpkg.RunOpts{Dir: repo, Env: os.Environ()})
	if e != nil || code != 0 {
		t.Fatalf("compile %v %d %s %s", e, code, out, errout)
	}
	for _, enabled := range []bool{false, true} {
		for _, outcome := range []string{"confirm", "confirm-stop", "confirm-sync", "revert", "revert-retry", "helper"} {
			for _, barrier := range []string{"none", "file", "sql", "helper"} {
				if barrier == "helper" && outcome != "helper" {
					continue
				}
				t.Run(fmt.Sprintf("enabled-%t/%s/%s", enabled, outcome, barrier), func(t *testing.T) {
					root := firewallDispatcherScratch(t)
					for _, n := range []string{"journal", "snapshots", "restore", "restore/markers", "rules", "state"} {
						must(os.Mkdir(root+"/"+n, 0700))
					}
					must(os.WriteFile(root+"/journal/lock", nil, 0600))
					rules := map[string][]byte{}
					for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
						rules[n] = []byte("restore " + n)
						must(os.WriteFile(root+"/rules/"+n, []byte("old"), 0640))
					}
					const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
					custody, e := firewall.OpenSnapshotCustody(root + "/snapshots")
					must(e)
					digest, e := custody.Put(t.Context(), id, firewall.SnapshotInput{PriorEnabled: enabled, Rules: rules, IPTablesSave: []byte("v4"), IP6TablesSave: []byte("v6"), NumberedStatus: []byte("status")})
					must(e)
					must(custody.Close())
					j, e := firewall.OpenJournal(root + "/journal")
					must(e)
					r := firewall.JournalRecord{Version: 1, ID: id, Unit: "novapanel-fwrevert-" + id, Deadline: 100, SnapshotSHA256: digest, PriorEnabled: enabled, State: "armed"}
					must(j.Arm(t.Context(), r))
					must(j.Close())
					db := root + "/state/panel.db"
					if prefix > 0 {
						buildTerminalPrefix(t, repo, db, prefix)
					}
					if prefix == 0 {
						st, e := store.Open(db)
						must(e)
						must(st.CreateFirewallPending(t.Context(), firewall.Pending{ID: id, SnapshotPath: "/not-opened", RevertUnit: r.Unit, Deadline: time.Unix(100, 0)}))
						must(st.Close())
					}
					var st *store.Store
					run := func(barrier string) {
						childCtx, kill := context.WithTimeout(t.Context(), 20*time.Second)
						defer kill()
						ready := make(chan bool, 1)
						if barrier != "none" {
							go func() {
								timer := time.NewTimer(15 * time.Second)
								defer timer.Stop()
								for {
									if _, e := os.Stat(root + "/ready"); e == nil {
										ready <- true
										kill()
										return
									}
									select {
									case <-timer.C:
										ready <- false
										kill()
										return
									case <-time.After(time.Millisecond):
									}
								}
							}()
						}
						o, er, c, e := (execpkg.Recovery{}).Run(childCtx, build+"/child", []string{"-test.run=^TestTerminalFixtureChild$"}, execpkg.RunOpts{Env: []string{"NP_TERMINAL_ROOT=" + root, "NP_TERMINAL_OUTCOME=" + outcome, "NP_TERMINAL_BARRIER=" + barrier, fmt.Sprintf("NP_TERMINAL_PREFIX=%d", prefix)}})
						if barrier == "none" {
							if e != nil || c != 0 {
								t.Fatalf("child %v %d %s %s", e, c, o, er)
							}
						} else {
							if !<-ready {
								t.Fatal("real boundary not reached", string(o), string(er))
							}
							if e == nil && c == 0 {
								t.Fatal("owned child survived kill")
							}
						}
					}
					if outcome == "helper" && barrier == "helper" {
						held, e := store.Open(db)
						must(e)
						run(barrier) // Helper publishes while daemon's lifetime Store lock is held.
						must(held.Close())
					} else {
						run(barrier)
					}
					if barrier != "none" {
						// Independent reopen after abrupt child death. File-only evidence has not
						// consumed SQL; committed SQL has. Ordinary child OpenState then replays it.
						st, e = store.Open(db)
						must(e)
						must(st.Tx(t.Context(), func(tx *sql.Tx) error {
							var n int
							if e := tx.QueryRow("SELECT count(*) FROM fw_pending").Scan(&n); e != nil {
								return e
							}
							want := 1
							if barrier == "sql" || (prefix > 0 && barrier == "helper") {
								want = 0
							}
							if n != want {
								t.Fatalf("boundary pending %d want %d", n, want)
							}
							return nil
						}))
						must(st.Close())
						run("none")
					}
					j, e = firewall.OpenJournal(root + "/journal")
					must(e)
					got, p, e := j.Inspect(t.Context())
					must(e)
					want := "reverted"
					if strings.HasPrefix(outcome, "confirm") {
						want = "confirmed"
					}
					if !p || got.ID != id || got.State != want {
						t.Fatal("terminal evidence lost", got)
					}
					r.State = "armed"
					if j.Arm(t.Context(), r) == nil {
						t.Fatal("new arm admitted")
					}
					must(j.Close())
					st, e = store.Open(db)
					must(e)
					defer st.Close()
					if prefix > 0 {
						must(st.Tx(t.Context(), func(tx *sql.Tx) error {
							var value string
							if e := tx.QueryRow("SELECT value_json FROM settings WHERE key='old-prefix'").Scan(&value); e != nil {
								return e
							}
							if value != "preserved" {
								t.Fatal("upgrade lost old data", value)
							}
							return nil
						}))
					}
					must(st.Tx(t.Context(), func(tx *sql.Tx) error {
						var n int
						if e := tx.QueryRow("SELECT count(*) FROM fw_pending").Scan(&n); e != nil {
							return e
						}
						if n != 0 {
							t.Fatal("projection survived")
						}
						return nil
					}))
					events, e := st.ListEvents(t.Context(), 100, "")
					must(e)
					n := 0
					for _, v := range events {
						if v.ID == id {
							n++
							if v.Actor != "firewall-recovery" || v.TS != 100000 {
								t.Fatal(v)
							}
						}
					}
					if n != 1 {
						t.Fatal("recovery SQL event count", n)
					}
					commands, commandErr := os.ReadFile(root + "/commands")
					if strings.HasPrefix(outcome, "confirm") {
						if !os.IsNotExist(commandErr) {
							t.Fatal("confirmation/commit ran restoration", commandErr)
						}
					} else {
						must(commandErr)
						wantCommands := "disable\n"
						if enabled {
							wantCommands = "--force enable\nreload\n"
						}
						if string(commands) != wantCommands {
							t.Fatalf("commit replay ran extra commands %q", commands)
						}
					}
					if !strings.HasPrefix(outcome, "confirm") {
						for name, want := range rules {
							b, e := os.ReadFile(root + "/rules/" + name)
							must(e)
							if !bytes.Equal(b, want) {
								t.Fatal("restoration missing", name)
							}
						}
						b, e := os.ReadFile(root + "/restore/markers/" + id + ".json")
						must(e)
						if !bytes.Contains(b, []byte(digest)) {
							t.Fatal("unbound marker")
						}
					}
					c, e := firewall.OpenSnapshotCustody(root + "/snapshots")
					must(e)
					got.State = want
					v, e := c.Verify(t.Context(), got)
					must(e)
					must(v.Close())
					must(c.Close())
				})
			}
		}
	}
}

const terminalProcessChild = `package main
import("context";"errors";"os";"testing";"strings";"time";"novapanel/internal/agent";"novapanel/internal/config";"novapanel/internal/firewall";execpkg "novapanel/internal/exec")
type terminalRunner struct{}
func(terminalRunner)Run(ctx context.Context,p string,a []string,o execpkg.RunOpts)([]byte,[]byte,int,error){if p=="/usr/bin/chattr"&&len(a)==2&&a[0]=="+a"{return nil,nil,0,nil};if p=="/usr/bin/systemctl"&&len(a)==3&&a[1]=="stop"&&strings.HasSuffix(a[2],".timer"){if os.Getenv("NP_TERMINAL_OUTCOME")=="confirm-stop"{return nil,nil,1,errors.New("manager stop failed")};return nil,nil,0,nil};if p=="/usr/bin/busctl"{return []byte("{\"type\":\"a(ssssssouso)\",\"data\":[[]]}"),nil,0,nil};if p=="/usr/sbin/ufw"{if os.Getenv("NP_TERMINAL_OUTCOME")=="revert-retry"&&os.Getenv("NP_TERMINAL_RETRY")!="yes"{return nil,nil,1,errors.New("interrupted restoration")};f,e:=os.OpenFile(os.Getenv("NP_TERMINAL_ROOT")+"/commands",os.O_APPEND|os.O_CREATE|os.O_WRONLY,0600);if e!=nil{return nil,nil,1,e};defer f.Close();_,e=f.WriteString(strings.Join(a," ")+"\n");return nil,nil,0,e};return nil,nil,1,errors.New("unexpected command")}
func TestTerminalFixtureChild(t *testing.T){root:=os.Getenv("NP_TERMINAL_ROOT");if root==""{t.Fatal("missing root")};const id="01ARZ3NDEKTSV4RRFFQ69G5FAV";runner:=terminalRunner{}
 cfg:=config.Defaults();cfg.DBPath=root+"/state/panel.db";cfg.SealKeyPath=root+"/state/seal.key";cfg.LogDir=root+"/logs";cfg.SocketPath=root+"/run/agent.sock";cfg.SitesRoot=root+"/sites";cfg.ACMEWebroot=root+"/acme";cfg.TLSDir=root+"/tls"
 a:=agent.NewWithDependencies(cfg,runner,nil)
 open:=func(){cleanup,e:=a.OpenState(t.Context());if e!=nil{t.Fatal(e)};t.Cleanup(func(){if e:=cleanup();e!=nil{t.Error(e)}});agent.TerminalFixtureJournal(a,root)
 if os.Getenv("NP_TERMINAL_PREFIX")=="6" {events,e:=a.Store.ListEvents(t.Context(),100,"");if e!=nil{t.Fatal(e)};found:=false;for _,ev:=range events{if ev.ID==id{found=true}};if !found {if e=a.Store.WithPending(t.Context(),id,func(firewall.Pending)(bool,error){return false,nil});e!=nil{if e=a.Store.CreateFirewallPending(t.Context(),firewall.Pending{ID:id,SnapshotPath:"/not-opened",RevertUnit:"novapanel-fwrevert-"+id,Deadline:time.Unix(100,0)});e!=nil{t.Fatal(e)}}} }
 }
 if os.Getenv("NP_TERMINAL_OUTCOME")!="helper"{open()}
 j,e:=firewall.OpenJournal(root+"/journal");if e!=nil{t.Fatal(e)};record,_,e:=j.Inspect(t.Context());j.Close();if e!=nil{t.Fatal(e)}
 if record.State=="armed"{if os.Getenv("NP_TERMINAL_OUTCOME")=="helper"{handled,e:=dispatchFirewallRevert(t.Context(),[]string{"fw-revert","/var/lib/novapanel/firewall/snapshots/"+id},os.Geteuid(),os.Getegid(),func(ctx context.Context,id string)error{return firewall.TerminalFixtureFire(ctx,root,id,runner)});if !handled||e!=nil{t.Fatal(handled,e)}}else{if e=firewall.TerminalFixtureRecover(t.Context(),root,id,runner,strings.HasPrefix(os.Getenv("NP_TERMINAL_OUTCOME"),"confirm"));e!=nil{if !errors.Is(e,firewall.ErrConfirmationCommitted)&&os.Getenv("NP_TERMINAL_OUTCOME")!="revert-retry"{t.Fatal(e)}}}}
 if os.Getenv("NP_TERMINAL_BARRIER")=="helper"{if e=os.WriteFile(root+"/ready",[]byte("terminal before state"),0600);e!=nil{t.Fatal(e)};select{}}
 if os.Getenv("NP_TERMINAL_OUTCOME")=="helper"{open()}
 if os.Getenv("NP_TERMINAL_OUTCOME")=="revert-retry"&&record.State=="armed" {if a.CommitFirewallTerminal(t.Context())==nil{t.Fatal("incomplete recovery consumed SQL")};if e=os.Setenv("NP_TERMINAL_RETRY","yes");e!=nil{t.Fatal(e)};if e=firewall.TerminalFixtureRecover(t.Context(),root,id,runner,false);e!=nil{t.Fatal(e)}}
 before,e:=os.ReadFile(root+"/journal/state");if e!=nil{t.Fatal(e)}
 if e=a.CommitFirewallTerminal(t.Context());e!=nil{t.Fatal(e)}
 after,e:=os.ReadFile(root+"/journal/state");if e!=nil||string(before)!=string(after){t.Fatal("terminal bytes changed",e)}
 if !errors.Is(firewall.TerminalFixturePreflight(t.Context(),root),firewall.ErrFirewallRecoveryPending){t.Fatal("SQL consumption enabled startup")}
 if a.FirewallRecovery!=nil{t.Fatal("public recovery activated")}
}
`
