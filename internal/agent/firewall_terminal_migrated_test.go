package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	execpkg "novapanel/internal/exec"
)

// Compile fixture-only access to the installed recovery constructor. Production
// migrations, OpenState, audit and terminal reconciliation remain unmodified.
func TestFirewallTerminalMigratedState(t *testing.T) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		t.Skip("installed recovery fixture requires root UID/GID")
	}
	repo, err := filepath.Abs("../..")
	mustTerminal(t, err)
	build := t.TempDir()
	shim := build + "/firewall.go"
	mustTerminal(t, os.WriteFile(shim, []byte(`package firewall
import("context";"time";execpkg "novapanel/internal/exec")
func MigratedFixtureRecovery(ctx context.Context,root,id string,confirm bool,r execpkg.Runner)error{
 s,e:=openInstalledJournalRecovery(ctx,runtimeLayout{root:root,rules:root+"/rules"},func()time.Time{return time.Unix(90,0)},r,r);if e!=nil{return e};defer s.Close();if confirm{return s.Confirm(ctx,id)};return s.Revert(ctx,id)
}`), 0600))
	child := build + "/child.go"
	mustTerminal(t, os.WriteFile(child, []byte(migratedTerminalFixture), 0600))
	b, err := json.Marshal(map[string]any{"Replace": map[string]string{repo + "/internal/firewall/migrated_fixture.go": shim, repo + "/internal/agent/migrated_fixture_test.go": child}})
	mustTerminal(t, err)
	mustTerminal(t, os.WriteFile(build+"/overlay.json", b, 0600))
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	out, stderr, code, err := (execpkg.Real{}).Run(ctx, migratedGo(t), []string{"test", "-race", "-count=1", "-v", "-overlay=" + build + "/overlay.json", "./internal/agent", "-run", "^TestMigratedTerminalFixture$"}, execpkg.RunOpts{Dir: repo, Env: os.Environ(), MaxOutput: 1 << 20})
	t.Log(string(out))
	if err != nil || code != 0 {
		t.Fatalf("migrated Agent fixture: %v %d %s", err, code, stderr)
	}
}

func migratedGo(t *testing.T) string {
	t.Helper()
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(d) {
			p := filepath.Join(d, "go")
			if st, e := os.Stat(p); e == nil && !st.IsDir() && st.Mode()&0111 != 0 {
				return p
			}
		}
	}
	t.Fatal("Go compiler missing from absolute PATH")
	return ""
}

func testPriorFirewallSchema(t *testing.T) {
	t.Helper()
	repo, e := filepath.Abs("../..")
	mustTerminal(t, e)
	build := t.TempDir()
	raw, e := os.ReadFile(repo + "/internal/store/migrations/hashes.json")
	mustTerminal(t, e)
	var hashes map[string]string
	mustTerminal(t, json.Unmarshal(raw, &hashes))
	delete(hashes, "0006")
	delete(hashes, "0007")
	b, e := json.Marshal(hashes)
	mustTerminal(t, e)
	mustTerminal(t, os.WriteFile(build+"/hashes.json", b, 0600))
	replacements := map[string]string{repo + "/internal/store/migrations/hashes.json": build + "/hashes.json", repo + "/internal/store/migrations/0006_phase4.sql": "", repo + "/internal/store/migrations/0007_phase6.sql": ""}
	b, e = json.Marshal(map[string]any{"Replace": replacements})
	mustTerminal(t, e)
	mustTerminal(t, os.WriteFile(build+"/overlay.json", b, 0600))
	env := append(os.Environ(), "NP_PRIOR_SCHEMA=yes")
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	out, stderr, code, e := (execpkg.Real{}).Run(ctx, migratedGo(t), []string{"test", "-count=1", "-overlay=" + build + "/overlay.json", "./internal/agent", "-run", "^TestFirewallTerminalMissingSchema$"}, execpkg.RunOpts{Dir: repo, Env: env, MaxOutput: 1 << 20})
	if e != nil || code != 0 {
		t.Fatalf("prior schema: %v %d %s %s", e, code, out, stderr)
	}
}

const migratedTerminalFixture = `package agent
import("context";"crypto/sha256";"encoding/hex";"path/filepath";"database/sql";"encoding/json";"errors";"fmt";"os";"strings";"testing";"time";"novapanel/internal/config";"novapanel/internal/firewall";"novapanel/internal/store";execpkg "novapanel/internal/exec")
type migratedRunner struct{calls int}
func(r *migratedRunner)Run(ctx context.Context,p string,a []string,o execpkg.RunOpts)([]byte,[]byte,int,error){
 r.calls++
 if p=="/usr/bin/chattr"&&len(a)==2&&a[0]=="+a"{return nil,nil,0,nil}
 if p=="/usr/bin/systemctl"&&len(a)==3&&a[1]=="stop"&&strings.HasSuffix(a[2],".timer"){return nil,nil,0,nil}
 if p=="/usr/bin/busctl"{return []byte("{\"type\":\"a(ssssssouso)\",\"data\":[[]]}"),nil,0,nil}
 if p=="/usr/sbin/ufw"&&(strings.Join(a," ")=="disable"||strings.Join(a," ")=="--force enable"||strings.Join(a," ")=="reload"){return nil,nil,0,nil}
 return nil,nil,1,errors.New("unexpected command")
}
func TestMigratedTerminalFixture(t *testing.T){
 for _,prefix:=range []int{0,6}{for _,enabled:=range []bool{false,true}{for _,outcome:=range []string{"confirmed","reverted"}{t.Run(fmt.Sprintf("prefix-%d/%s/enabled-%t",prefix,outcome,enabled),func(t *testing.T){
 root:=t.TempDir();mustTerminal(t,os.Chmod(root,0700))
 for _,d:=range []string{"state","journal","snapshots","restore","restore/markers","rules"}{mustTerminal(t,os.Mkdir(root+"/"+d,0700))};mustTerminal(t,os.WriteFile(root+"/journal/lock",nil,0600))
 rules:=map[string][]byte{};for _,n:=range []string{"after.rules","after6.rules","before.rules","before6.rules","user.rules","user6.rules"}{rules[n]=[]byte("verified "+n);mustTerminal(t,os.WriteFile(root+"/rules/"+n,[]byte("old"),0640))}
 const id="01ARZ3NDEKTSV4RRFFQ69G5FAV"
 c,e:=firewall.OpenSnapshotCustody(root+"/snapshots");mustTerminal(t,e);digest,e:=c.Put(t.Context(),id,firewall.SnapshotInput{PriorEnabled:enabled,Rules:rules,IPTablesSave:[]byte("v4"),IP6TablesSave:[]byte("v6"),NumberedStatus:[]byte("status")});mustTerminal(t,e);defer c.Close()
 j,e:=firewall.OpenJournal(root+"/journal");mustTerminal(t,e);defer j.Close();r:=firewall.JournalRecord{Version:1,ID:id,Unit:"novapanel-fwrevert-"+id,Deadline:100,SnapshotSHA256:digest,PriorEnabled:enabled,State:"armed"};mustTerminal(t,j.Arm(t.Context(),r))
 cfg:=config.Defaults();cfg.DBPath=root+"/state/panel.db";cfg.SealKeyPath=root+"/state/seal.key";cfg.LogDir=root+"/logs";cfg.SocketPath=root+"/run/agent.sock";cfg.SitesRoot=root+"/sites";cfg.ACMEWebroot=root+"/acme";cfg.TLSDir=root+"/tls"
 if prefix>0{fixturePrefix(t,cfg.DBPath,prefix)}
 runner:=&migratedRunner{};a:=NewWithDependencies(cfg,runner,nil);cleanup,e:=a.OpenState(t.Context());mustTerminal(t,e);defer cleanup()
 mustTerminal(t,firewall.MigratedFixtureRecovery(t.Context(),root,id,outcome=="confirmed",runner));r.State=outcome
 mustTerminal(t,a.Store.CreateFirewallPending(t.Context(),firewall.Pending{ID:id,RevertUnit:r.Unit,SnapshotPath:"/never-opened",Deadline:time.Unix(100,0)}))
 a.firewallTerminalJournal=func(ctx context.Context)(*firewall.Journal,error){return firewall.OpenJournalContext(ctx,root+"/journal")}
 before,e:=os.ReadFile(root+"/journal/state");mustTerminal(t,e);calls:=runner.calls
 mustTerminal(t,a.CommitFirewallTerminal(t.Context()));terminalCounts(t,a,0,1);mustTerminal(t,a.CommitFirewallTerminal(t.Context()));terminalCounts(t,a,0,1)
 after,e:=os.ReadFile(root+"/journal/state");mustTerminal(t,e);if string(before)!=string(after){t.Fatal("terminal changed")};if calls!=runner.calls{t.Fatal("consumption reran commands")}
 got,p,e:=j.Inspect(t.Context());mustTerminal(t,e);if !p||got!=r{t.Fatal(got)};v,e:=c.Verify(t.Context(),r);mustTerminal(t,e);mustTerminal(t,v.Close())
 if outcome=="reverted"{for n,w:=range rules{b,e:=os.ReadFile(root+"/rules/"+n);mustTerminal(t,e);if string(b)!=string(w){t.Fatal("restoration",n)}};if _,e:=os.Stat(root+"/restore/markers/"+id+".json");e!=nil{t.Fatal(e)}}
 events,e:=a.Store.ListEvents(t.Context(),100,"");mustTerminal(t,e);want:=terminalEvent(r);found:=0;for _,ev:=range events{if ev.ID==id{found++;if ev.TS!=want.TS||ev.Actor!=want.Actor||ev.Verb!=want.Verb||ev.ObjectKey!=want.ObjectKey||ev.Outcome!=want.Outcome||string(ev.Detail)!=string(want.Detail){t.Fatal(ev)}}};if found!=1{t.Fatal("SQL event count",found)}
 b,e:=os.ReadFile(cfg.LogDir+"/audit.log");mustTerminal(t,e);raw,_:=json.Marshal(r);if !strings.Contains(string(b),string(raw)){t.Fatal("ordinary audit missing exact detail")}
 if other,e:=store.Open(cfg.DBPath);e==nil{other.Close();t.Fatal("lifetime lock lost")};if a.FirewallRecovery!=nil{t.Fatal("public recovery opened")}
 mustTerminal(t,a.Store.Tx(t.Context(),func(tx *sql.Tx)error{var n int;return tx.QueryRow("SELECT count(*) FROM schema_migrations WHERE version='0007'").Scan(&n)}))
 })}}}
}
func fixturePrefix(t *testing.T,path string,prefix int){
 f,e:=os.OpenFile(path,os.O_CREATE|os.O_EXCL|os.O_RDWR,0600);mustTerminal(t,e);mustTerminal(t,f.Close());db,e:=sql.Open("sqlite",path);mustTerminal(t,e);defer db.Close();tx,e:=db.Begin();mustTerminal(t,e);defer tx.Rollback();repo,e:=filepath.Abs("../..");mustTerminal(t,e);entries,e:=os.ReadDir(repo+"/internal/store/migrations");mustTerminal(t,e)
 for _,entry:=range entries{var v int;n:=entry.Name();if !strings.HasSuffix(n,".sql"){continue};_,e=fmt.Sscanf(n,"%04d_",&v);mustTerminal(t,e);if v>prefix{continue};b,e:=os.ReadFile(repo+"/internal/store/migrations/"+n);mustTerminal(t,e);_,e=tx.Exec(string(b));mustTerminal(t,e);h:=sha256.Sum256(b);_,e=tx.Exec("INSERT INTO schema_migrations VALUES(?,?,1)",fmt.Sprintf("%04d",v),hex.EncodeToString(h[:]));mustTerminal(t,e)};mustTerminal(t,tx.Commit())
}
`
