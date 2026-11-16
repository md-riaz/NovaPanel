package verbs_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func TestSiteCreateReservationRetryUsesExistingID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(root, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Cfg.SitesRoot = filepath.Join(root, "sites")
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	call := func() rpc.Reply {
		return s.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.create", Args: json.RawMessage(`{"name":"demo","type":"static","primary_domain":"demo.test"}`)})
	}
	first := call()
	if !first.OK {
		t.Fatal(first)
	}
	second := call()
	if !second.OK {
		t.Fatal(second)
	}
	site, err := st.SiteByName(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, reply := range []rpc.Reply{first, second} {
		j := reply.Data.(jobs.EnqueueResult).Job
		persisted, err := st.GetJob(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		var args struct {
			SiteID string `json:"site_id"`
		}
		if err := json.Unmarshal(persisted.Args, &args); err != nil {
			t.Fatal(err)
		}
		var returned struct {
			SiteID string `json:"site_id"`
		}
		if err := json.Unmarshal(j.Args, &returned); err != nil || returned.SiteID != site.ID {
			t.Fatalf("returned job args = %s, error = %v; want site %s", j.Args, err, site.ID)
		}
		token := reply.Data.(jobs.EnqueueResult).ClaimToken
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(decoded) != 32 {
			t.Fatalf("invalid claim token: %q (%v)", token, err)
		}
		hash := sha256.Sum256([]byte(token))
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var stored string
			if err := tx.QueryRowContext(ctx, "SELECT claim_hash FROM job_claims WHERE job_id=?", j.ID).Scan(&stored); err != nil {
				return err
			}
			if stored != hex.EncodeToString(hash[:]) {
				t.Errorf("claim hash does not match returned token")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if args.SiteID != site.ID {
			t.Fatalf("job site %q != reserved %q", args.SiteID, site.ID)
		}
	}
}

func TestSiteCreatePreservesExistingOnFailure(t *testing.T) {
	for _, scenario := range []string{"jobs", "job_claims", "name conflict", "domain conflict"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			st, err := store.Open(filepath.Join(root, "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			log, err := audit.Open(st, filepath.Join(root, "audit.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			a := agent.New()
			a.Store = st
			a.Audit = log
			a.Cfg.SitesRoot = filepath.Join(root, "sites")
			s := rpc.NewServer(a)
			s.ConfigureJobs()
			call := func(args string) rpc.Reply {
				return s.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.create", Args: json.RawMessage(args)})
			}
			if r := call(`{"name":"demo","type":"static","primary_domain":"demo.test"}`); !r.OK {
				t.Fatal(r)
			}
			before, err := st.SiteByName(ctx, "demo")
			if err != nil {
				t.Fatal(err)
			}
			domains, err := st.ListDomains(ctx, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			args := `{"name":"demo","type":"static","primary_domain":"demo.test"}`
			switch scenario {
			case "jobs", "job_claims":
				if err := st.Tx(ctx, func(tx *sql.Tx) error {
					_, e := tx.ExecContext(ctx, "CREATE TRIGGER fail_existing BEFORE INSERT ON "+scenario+" BEGIN SELECT RAISE(ABORT,'injected'); END")
					return e
				}); err != nil {
					t.Fatal(err)
				}
			case "name conflict":
				args = `{"name":"demo","type":"static","primary_domain":"other.test"}`
			case "domain conflict":
				args = `{"name":"other","type":"static","primary_domain":"demo.test"}`
			}
			if r := call(args); r.OK {
				t.Fatal("expected failure", r)
			}
			after, err := st.SiteByName(ctx, "demo")
			if err != nil {
				t.Fatal(err)
			}
			afterDomains, err := st.ListDomains(ctx, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(domains, afterDomains) {
				t.Fatal("existing site or domains changed")
			}
			if err := st.Tx(ctx, func(tx *sql.Tx) error {
				for _, table := range []string{"sites", "domains", "jobs", "job_claims"} {
					var n int
					if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
						return err
					}
					if n != 1 {
						t.Errorf("%s has %d rows, want 1", table, n)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSiteCreateDeniedAdmissionLeavesNoRows(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(root, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Cfg.SitesRoot = filepath.Join(root, "sites")
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	a.CloseMutationAdmission()
	r := s.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.create", Args: json.RawMessage(`{"name":"demo","type":"static","primary_domain":"demo.test"}`)})
	if r.OK {
		t.Fatal("expected admission denial")
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"sites", "domains", "jobs", "job_claims"} {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("%s has %d rows", table, n)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSiteCreateReservationRollback(t *testing.T) {
	for _, table := range []string{"jobs", "job_claims"} {
		t.Run(table, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			st, err := store.Open(filepath.Join(root, "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			log, err := audit.Open(st, filepath.Join(root, "audit.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			a := agent.New()
			a.Store = st
			a.Audit = log
			a.Cfg.SitesRoot = filepath.Join(root, "sites")
			s := rpc.NewServer(a)
			s.ConfigureJobs()
			err = st.Tx(ctx, func(tx *sql.Tx) error {
				_, e := tx.ExecContext(ctx, "CREATE TRIGGER fail_reservation BEFORE INSERT ON "+table+" BEGIN SELECT RAISE(ABORT,'injected'); END")
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			r := s.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.create", Args: json.RawMessage(`{"name":"demo","type":"static","primary_domain":"demo.test"}`)})
			if r.OK {
				t.Fatal("expected reservation failure")
			}
			err = st.Tx(ctx, func(tx *sql.Tx) error {
				for _, name := range []string{"sites", "domains", "jobs", "job_claims"} {
					var n int
					if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+name).Scan(&n); e != nil {
						return e
					}
					if n != 0 {
						t.Errorf("%s retained %d rows", name, n)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
