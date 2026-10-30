package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestDomainExpansionAtomicDedupDurability(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	_, err = s.CreateSite(ctx, Site{ID: "site", Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, Domain{ID: "primary", FQDN: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE sites SET status='active',cert_source='letsencrypt',force_https=1,ssl_auto=0 WHERE id='site'")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	parent := Job{ID: "parent", Verb: "domain.add", ObjectKey: "site:site", CreatedBy: "token", Role: "token", Scopes: []string{"sites:write"}, Actor: "trusted", Args: json.RawMessage(`{}`)}
	if err = s.CreateJob(ctx, parent); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _, e := s.AddDomainWithExpansion(ctx, Domain{ID: fmt.Sprint("alias", n), SiteID: "site", FQDN: fmt.Sprintf("alias%d.example.test", n)}, parent.ID, 100)
			if e != nil {
				t.Error(e)
			}
		}(n)
	}
	wg.Wait()
	children, err := s.ListJobs(ctx, JobFilter{Verb: "ssl.issue"})
	if err != nil || len(children) != 1 {
		t.Fatalf("dedupe=%+v %v", children, err)
	}
	child := children[0]
	if child.CreatedBy != "token" || child.Role != "token" || len(child.Scopes) != 1 || !child.Effectful {
		t.Fatal(child)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := s.GetJob(ctx, child.ID)
	if err != nil || durable.State != "queued" {
		t.Fatalf("restart %+v %v", durable, err)
	}
	// A terminal failure must not cause the same added domain's retry to issue again.
	durable.State = "failed"
	durable.Code = "agent_error"
	if err = s.UpdateJob(ctx, durable); err != nil {
		t.Fatal(err)
	}
	d, err := s.DomainByID(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, retry, err := s.AddDomainWithExpansion(ctx, d, parent.ID, 200)
	if err != nil || retry != nil {
		t.Fatalf("retry=%+v %v", retry, err)
	}
	// Transaction failure cannot leave a new desired row with no durable follow-up.
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `CREATE TRIGGER fail_expansion BEFORE INSERT ON jobs WHEN NEW.verb='ssl.issue' BEGIN SELECT RAISE(ABORT,'injected'); END`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.AddDomainWithExpansion(ctx, Domain{ID: "rollback", SiteID: "site", FQDN: "rollback.example.test"}, parent.ID, 300)
	if err == nil {
		t.Fatal("insertion failure ignored")
	}
	if _, err = s.DomainByID(ctx, "rollback"); err != sql.ErrNoRows {
		t.Fatalf("domain survived failed publication: %v", err)
	}
}

func TestDomainExpansionPolicyAndProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, source         string
		force, staging, want bool
	}{
		{"manual", "manual", true, false, false}, {"http", "letsencrypt", false, false, false}, {"none", "none", true, false, false}, {"staging", "letsencrypt", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, e := Open(":memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			_, e = s.CreateSite(ctx, Site{ID: "s", Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, Domain{ID: "p", FQDN: "example.test"})
			if e != nil {
				t.Fatal(e)
			}
			e = s.Tx(ctx, func(tx *sql.Tx) error {
				if _, e := tx.ExecContext(ctx, "UPDATE sites SET status='active',cert_source=?,force_https=?", tc.source, tc.force); e != nil {
					return e
				}
				_, e := tx.ExecContext(ctx, "UPDATE domains SET cert_staging=?", tc.staging)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			e = s.CreateJob(ctx, Job{ID: "parent", Verb: "domain.add", ObjectKey: "site:s", CreatedBy: "local-root", Role: "owner", Args: json.RawMessage(`{}`)})
			if e != nil {
				t.Fatal(e)
			}
			d, c, e := s.AddDomainWithExpansion(ctx, Domain{ID: "alias", SiteID: "s", FQDN: "alias.test"}, "parent", 100)
			if e != nil || (c != nil) != tc.want || d.CertState != "none" {
				t.Fatalf("%+v %+v %v", d, c, e)
			}
			if c != nil {
				var a struct {
					Staging bool `json:"staging"`
				}
				if json.Unmarshal(c.Args, &a) != nil || a.Staging != tc.staging {
					t.Fatal(string(c.Args))
				}
			}
		})
	}
}
