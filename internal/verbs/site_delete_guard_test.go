package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestLegacySiteDeleteRefusesBeforeEffects(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		attached   bool
	}{{"php", "php", false}, {"wordpress", "wordpress", false}, {"static with database", "static", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			st, err := store.Open(filepath.Join(root, "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			a := agent.New()
			a.Store = st
			a.Cfg.SitesRoot = filepath.Join(root, "sites")
			sid, _ := id.New()
			did, _ := id.New()
			_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: tc.kind, Docroot: filepath.Join(a.Cfg.SitesRoot, "demo", "public")}, store.Domain{ID: did, FQDN: "demo.test"})
			if err != nil {
				t.Fatal(err)
			}
			if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
				t.Fatal(err)
			}
			if tc.attached {
				db, _ := id.New()
				user, _ := id.New()
				if err = st.CreateDatabasePrimary(ctx, store.DatabasePrimary{ID: db, UserID: user, SiteID: sid}); err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := json.Marshal(map[string]any{"site_id": sid, "purge_files": true})
			server := rpc.NewServer(a)
			reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.delete", Args: raw})
			if reply.OK || reply.Code != "unsupported" {
				t.Fatalf("unsafe deletion admitted: %+v", reply)
			}
			j := jobs.Job{Args: raw}
			if err = rpc.Registry["site.delete"].Exec(ctx, a, &j); err == nil {
				t.Fatal("unsafe deletion accepted")
			}
			site, err := st.SiteByID(ctx, sid)
			if err != nil || site.Status != "active" {
				t.Fatalf("refusal changed site: %+v %v", site, err)
			}
		})
	}
}
