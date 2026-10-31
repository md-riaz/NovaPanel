package integration

import (
	"bytes"
	"context"
	"encoding/json"
	execpkg "novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFirewallDispatcherStoreIndependence compiles test-only overlays. Neither
// the installed CLI nor the firewall package exports a fixture path override.
func TestFirewallDispatcherStoreIndependence(t *testing.T) {
	firewallDispatcherStoreIndependence(t, false)
}

func TestFirewallStartupStoreIndependence(t *testing.T) {
	firewallDispatcherStoreIndependence(t, true)
}

func firewallDispatcherStoreIndependence(t *testing.T, startup bool) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		t.Skip("fixed runtime metadata requires root UID/GID fixtures")
	}
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	repo := filepath.Clean(filepath.Join(cwd, "../.."))
	// Custody checks every ancestor. A sudo test process does not make the
	// runner-owned checkout safe. The root launcher must supply a private,
	// root-owned TMPDIR with equally trusted ancestors; do not repair checkout
	// permissions or fall back to a globally writable system temporary directory.
	root := firewallDispatcherScratch(t)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, n := range []string{"journal", "snapshots", "restore", "restore/markers", "rules"} {
		must(os.Mkdir(root+"/"+n, 0700))
	}
	must(os.WriteFile(root+"/journal/lock", nil, 0600))
	const pending = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	rules := map[string][]byte{}
	for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
		rules[n] = []byte("scratch restore " + n + "\n")
		must(os.WriteFile(root+"/rules/"+n, []byte("old"), 0640))
	}
	c, e := firewall.OpenSnapshotCustody(root + "/snapshots")
	must(e)
	digest, e := c.Put(t.Context(), pending, firewall.SnapshotInput{PriorEnabled: true, Rules: rules, IPTablesSave: []byte("v4"), IP6TablesSave: []byte("v6"), NumberedStatus: []byte("status")})
	must(e)
	must(c.Close())
	j, e := firewall.OpenJournal(root + "/journal")
	must(e)
	must(j.Arm(t.Context(), firewall.JournalRecord{Version: 1, ID: pending, Unit: "novapanel-fwrevert-" + pending, Deadline: 100, SnapshotSHA256: digest, PriorEnabled: true, State: "armed"}))
	must(j.Close())
	st, e := store.Open(root + "/panel.db")
	must(e)
	defer st.Close()
	before := map[string][]byte{}
	for _, n := range []string{"panel.db", "panel.db-wal", "panel.db-shm", "panel.db.pid"} {
		b, e := os.ReadFile(root + "/" + n)
		if e == nil {
			before[n] = b
		} else if !os.IsNotExist(e) {
			t.Fatal(e)
		}
	}
	// A second real Store cannot acquire the held lifetime lock.
	if other, e := store.Open(root + "/panel.db"); e == nil {
		other.Close()
		t.Fatal("fixture does not hold Store lifetime lock")
	}
	shim := `package firewall
import("context";"time";execpkg "novapanel/internal/exec")
func FixtureRecover(ctx context.Context,id,root string,r execpkg.Runner)error{return recoverInstalled(ctx,id,runtimeLayout{root:root,rules:root+"/rules"},func()time.Time{return time.Unix(100,0)},r)}
`
	child := `package main
import("context";"os";"testing";"strings";execpkg "novapanel/internal/exec";"novapanel/internal/firewall")
type fixtureRunner struct{t *testing.T;calls []string}
func(f *fixtureRunner)Run(_ context.Context,p string,a []string,_ execpkg.RunOpts)([]byte,[]byte,int,error){f.calls=append(f.calls,p+" "+strings.Join(a," "));return nil,nil,0,nil}
func TestFixtureDispatcherChild(t *testing.T){root:=os.Getenv("NP_DISPATCH_ROOT");if root==""{t.Fatal("missing fixture")};f:=&fixtureRunner{t:t};handled,e:=dispatchFirewallRevert(t.Context(),[]string{"fw-revert","/var/lib/novapanel/firewall/snapshots/01ARZ3NDEKTSV4RRFFQ69G5FAV"},os.Geteuid(),os.Getegid(),func(ctx context.Context,id string)error{return firewall.FixtureRecover(ctx,id,root,f)});if !handled||e!=nil{t.Fatalf("dispatch %t %v",handled,e)};if strings.Join(f.calls,";")!="/usr/sbin/ufw --force enable;/usr/sbin/ufw reload"{t.Fatal(f.calls)}}
`
	// All substitutions below are test-only overlays bound to this checkout.
	must(os.WriteFile(root+"/shim.go.txt", []byte(shim), 0600))
	must(os.WriteFile(root+"/child.go.txt", []byte(child), 0600))
	replacements := map[string]string{repo + "/internal/firewall/fixture_dispatch_overlay.go": root + "/shim.go.txt", repo + "/cmd/panel-agent/fixture_dispatch_overlay_test.go": root + "/child.go.txt"}
	packagePath := "./cmd/panel-agent"
	if startup {
		fixtureSource, e := os.ReadFile(repo + "/internal/firewall/runtime_manager_test.go")
		must(e)
		fixtureSource = []byte(strings.Split(string(fixtureSource), "func TestRuntimeManagerLaunchReadback")[0])
		fixtureSource = []byte(strings.Replace(string(fixtureSource), "\n\t\"time\"", "", 1))
		must(os.WriteFile(root+"/manager-fixture.go.txt", fixtureSource, 0600))
		startupShim := `package firewall
import("context";"testing";"time")
func FixtureStartup(t *testing.T,root string)error{j,e:=OpenJournal(root+"/journal");if e!=nil{return e};r,_,e:=j.Inspect(t.Context());j.Close();if e!=nil{return e};f:=newManagerFixture(t,r);m:=runtimeManager{runner:f,now:func()time.Time{return time.Unix(90,0)},monotonic:func()(uint64,error){return 1000000,nil}};e=startupFirewall(context.Background(),runtimeLayout{root:root,rules:root+"/rules"},m);if len(f.calls)<10{t.Fatal("no manager execution")};return e}
`
		agentShim := `package agent
import("context";"testing";"novapanel/internal/firewall")
func FixtureStartup(a *Agent,t *testing.T,root string){a.firewallStartup=func(context.Context)error{return firewall.FixtureStartup(t,root)}}
`
		child = `package main
import("context";"errors";"os";"testing";"novapanel/internal/agent";"novapanel/internal/config";"novapanel/internal/firewall";"novapanel/internal/validate")
func TestFixtureDispatcherChild(t *testing.T){root:=os.Getenv("NP_DISPATCH_ROOT");cfg:=config.Defaults();cfg.DBPath=root+"/panel.db";cfg.TLSDir="relative-state-sentinel";a:=agent.NewWithDependencies(cfg,safeRunner{},&validate.V{});agent.FixtureStartup(a,t,root);if e:=serve(t.Context(),a);!errors.Is(e,firewall.ErrFirewallRecoveryPending){t.Fatal(e)};if a.Store!=nil||a.Jobs!=nil||a.Scheduler!=nil{t.Fatal("state/workers initialized")};handled,e:=dispatchFirewallRevert(context.Background(),[]string{"fw-revert","/var/lib/novapanel/firewall/snapshots/01ARZ3NDEKTSV4RRFFQ69G5FAV"},os.Geteuid(),os.Getegid(),func(ctx context.Context,id string)error{return firewall.FixtureRecover(ctx,id,root,&fixtureRunner{t:t})});if !handled||e!=nil{t.Fatal(handled,e)}}
type fixtureRunner struct{t *testing.T}
func(f *fixtureRunner)Run(ctx context.Context,p string,a []string,o execpkg.RunOpts)([]byte,[]byte,int,error){if p!="/usr/sbin/ufw"{f.t.Fatal(p)};return nil,nil,0,nil}
`
		child = strings.Replace(child, `"novapanel/internal/validate")`, `"novapanel/internal/validate";execpkg "novapanel/internal/exec")`, 1)
		must(os.WriteFile(root+"/startup-shim.go.txt", []byte(startupShim), 0600))
		must(os.WriteFile(root+"/agent-shim.go.txt", []byte(agentShim), 0600))
		must(os.WriteFile(root+"/child.go.txt", []byte(child), 0600))
		replacements[repo+"/internal/firewall/fixture_manager_overlay.go"] = root + "/manager-fixture.go.txt"
		replacements[repo+"/internal/firewall/fixture_startup_overlay.go"] = root + "/startup-shim.go.txt"
		replacements[repo+"/internal/agent/fixture_startup_overlay.go"] = root + "/agent-shim.go.txt"
	}
	overlay, e := json.Marshal(map[string]any{"Replace": replacements})
	must(e)
	must(os.WriteFile(root+"/overlay.json", overlay, 0600))
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	goBinary := ""
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, "go")
		if st, e := os.Stat(candidate); e == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			goBinary = candidate
			break
		}
	}
	if strings.TrimSpace(goBinary) == "" {
		t.Fatal("Go executable missing from absolute PATH entries")
	}
	out, errout, exit, e := (execpkg.Real{}).Run(ctx, goBinary, []string{"test", "-c", "-overlay=" + root + "/overlay.json", "-o", root + "/child", packagePath}, execpkg.RunOpts{Dir: repo, Env: os.Environ()})
	if e != nil || exit != 0 {
		t.Fatalf("compile %v %d %s %s", e, exit, out, errout)
	}
	out, errout, exit, e = (execpkg.Recovery{}).Run(ctx, root+"/child", []string{"-test.run=^TestFixtureDispatcherChild$"}, execpkg.RunOpts{Env: []string{"NP_DISPATCH_ROOT=" + root}})
	if e != nil || exit != 0 {
		t.Fatalf("child %v %d %s %s", e, exit, out, errout)
	}
	for n, want := range rules {
		b, e := os.ReadFile(root + "/rules/" + n)
		must(e)
		if !bytes.Equal(b, want) {
			t.Fatal("wrong restored bytes", n)
		}
		s, e := os.Stat(root + "/rules/" + n)
		must(e)
		if s.Mode().Perm() != 0640 {
			t.Fatal("mode changed")
		}
	}
	marker, e := os.ReadFile(root + "/restore/markers/" + pending + ".json")
	must(e)
	if !bytes.Contains(marker, []byte(digest)) {
		t.Fatal("unbound marker")
	}
	b, e := os.ReadFile(root + "/journal/state")
	must(e)
	var state firewall.JournalRecord
	must(json.Unmarshal(b, &state))
	if state.State != "reverted" || state.ID != pending {
		t.Fatal(state)
	}
	for n, want := range before {
		b, e := os.ReadFile(root + "/" + n)
		must(e)
		if !bytes.Equal(b, want) {
			t.Fatal("Store file changed", n)
		}
	}
	if other, e := store.Open(root + "/panel.db"); e == nil {
		other.Close()
		t.Fatal("child released parent Store lock")
	}
}
