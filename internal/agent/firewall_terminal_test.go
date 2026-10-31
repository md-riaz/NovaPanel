package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/config"
	"novapanel/internal/firewall"
	"novapanel/internal/store"
)

func terminalAgent(t *testing.T, schema bool, terminal ...string) (*Agent, *firewall.Journal, firewall.JournalRecord, func()) {
	t.Helper()
	d := t.TempDir()
	cfg := config.Defaults()
	cfg.DBPath = d + "/state/panel.db"
	cfg.SealKeyPath = d + "/state/seal.key"
	cfg.LogDir = d + "/logs"
	cfg.SocketPath = d + "/run/agent.sock"
	cfg.SitesRoot = d + "/sites"
	cfg.ACMEWebroot = d + "/acme"
	cfg.TLSDir = d + "/tls"
	mustTerminal(t, os.MkdirAll(filepath.Dir(cfg.DBPath), 0700))
	s, e := store.Open(cfg.DBPath)
	mustTerminal(t, e)
	if schema {
		ddl, e := os.ReadFile("../store/testdata/0007_phase6.sql")
		mustTerminal(t, e)
		mustTerminal(t, s.Tx(t.Context(), func(tx *sql.Tx) error { _, e := tx.Exec(string(ddl)); return e }))
	}
	mustTerminal(t, s.Close())
	a := NewWithDependencies(cfg, &startupRunner{}, nil)
	cleanup, e := a.OpenState(t.Context())
	mustTerminal(t, e)
	jr := d + "/journal"
	mustTerminal(t, os.Mkdir(jr, 0700))
	mustTerminal(t, os.WriteFile(jr+"/lock", nil, 0600))
	j, e := firewall.OpenJournal(jr)
	mustTerminal(t, e)
	r := firewall.JournalRecord{Version: 1, ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Unit: "novapanel-fwrevert-01ARZ3NDEKTSV4RRFFQ69G5FAV", Deadline: 100, SnapshotSHA256: strings.Repeat("a", 64), State: "armed"}
	mustTerminal(t, j.Arm(t.Context(), r))
	if len(terminal) > 0 && terminal[0] == "reverted" {
		mustTerminal(t, j.Fire(t.Context(), r.ID, func() time.Time { return time.Unix(100, 0) }, func(firewall.JournalRecord) error { return nil }))
		r.State = "reverted"
	} else {
		mustTerminal(t, j.Confirm(t.Context(), r.ID, func() time.Time { return time.Unix(90, 0) }, func(string) error { return nil }))
		r.State = "confirmed"
	}
	if schema {
		mustTerminal(t, a.Store.CreateFirewallPending(t.Context(), firewall.Pending{ID: r.ID, RevertUnit: r.Unit, SnapshotPath: "/never-opened", Deadline: time.Unix(r.Deadline, 0)}))
	}
	a.firewallTerminalJournal = func(ctx context.Context) (*firewall.Journal, error) { return firewall.OpenJournalContext(ctx, jr) }
	return a, j, r, func() { mustTerminal(t, j.Close()); mustTerminal(t, cleanup()) }
}
func mustTerminal(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

func TestFirewallTerminalCommitConnected(t *testing.T) {
	a, j, r, close := terminalAgent(t, true)
	defer close()
	mustTerminal(t, a.CommitFirewallTerminal(t.Context()))
	got, p, e := j.Inspect(t.Context())
	mustTerminal(t, e)
	if !p || got != r {
		t.Fatalf("terminal evidence changed: %+v", got)
	}
	events, e := a.Store.ListEvents(t.Context(), 100, "")
	mustTerminal(t, e)
	found := false
	for _, v := range events {
		if v.ID == r.ID {
			found = true
			if v.Actor != "firewall-recovery" || v.Verb != "firewall.confirm" || v.TS != r.Deadline*1000 {
				t.Fatal(v)
			}
		}
	}
	if !found {
		t.Fatal("missing SQL recovery event")
	}
	b, e := os.ReadFile(a.Cfg.LogDir + "/audit.log")
	mustTerminal(t, e)
	raw, _ := json.Marshal(r)
	if !strings.Contains(string(b), string(raw)) {
		t.Fatal("ordinary audit missing terminal detail")
	}
	if e = a.Store.WithPending(t.Context(), r.ID, func(firewall.Pending) (bool, error) { t.Fatal("pending survived"); return false, nil }); e == nil {
		t.Fatal("pending absence not reported")
	}
	if other, e := store.Open(a.Cfg.DBPath); e == nil {
		other.Close()
		t.Fatal("lifetime lock lost")
	}
	if a.FirewallRecovery != nil {
		t.Fatal("public recovery activated")
	}
}
