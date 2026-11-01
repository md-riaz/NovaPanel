package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func waitACMEAdmissionJob(t *testing.T, f *acmeWorkerFixture, reply rpc.Reply) jobs.Job {
	t.Helper()
	if !reply.OK {
		t.Fatal(reply)
	}
	raw, err := json.Marshal(reply.Data)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope) != 2 || envelope["job"] == nil || envelope["claim"] == nil {
		t.Fatalf("noncanonical admission: %s", raw)
	}
	enq := reply.Data.(jobs.EnqueueResult)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := f.a.Store.GetJob(context.Background(), enq.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == "done" || j.State == "failed" {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker timeout")
	return jobs.Job{}
}

func TestACMEAdmissionCanonicalNoopEnvelope(t *testing.T) {
	f := newACMEWorkerFixture(t)
	f.ca.mu.Lock()
	f.ca.lifetime = 90 * 24 * time.Hour
	f.ca.mu.Unlock()
	if j := f.run(t, "ssl.issue"); j.State != "done" {
		t.Fatal(j)
	}
	f.ca.mu.Lock()
	before := f.ca.requests
	f.ca.mu.Unlock()
	for _, verb := range []string{"ssl.issue", "ssl.renew"} {
		args := fmt.Sprintf(`{"site_id":%q}`, f.sid)
		if verb == "ssl.issue" {
			args = fmt.Sprintf(`{"site_id":%q,"staging":true}`, f.sid)
		}
		reply := f.server.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: json.RawMessage(args)})
		j := waitACMEAdmissionJob(t, f, reply)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(j.Result, &fields); err != nil {
			t.Fatal(err)
		}
		wantFields := 1
		if verb == "ssl.issue" {
			wantFields = 2
			if string(fields["fqdns"]) != `["example.test"]` {
				t.Fatal(string(j.Result))
			}
		}
		if j.State != "done" || j.Step != "tls_committed" || len(fields) != wantFields || fields["not_after"] == nil {
			t.Fatalf("noncanonical terminal job: %+v", j)
		}
	}
	f.ca.mu.Lock()
	defer f.ca.mu.Unlock()
	if f.ca.requests != before {
		t.Fatalf("no-op made %d additional CA requests", f.ca.requests-before)
	}
}

func TestACMERenewPublicShape(t *testing.T) {
	f := newACMEWorkerFixture(t)
	for _, extra := range []string{`"staging":true`, `"staging":false`, `"staging":null`, `"wildcard":false`, `"unknown":true`} {
		r := f.server.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: "ssl.renew", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,%s}`, f.sid, extra))})
		if r.Code != "bad_request" {
			t.Fatalf("%s: %+v", extra, r)
		}
	}
}

func TestACMERenewPreservesInstalledEnvironment(t *testing.T) {
	for _, staging := range []bool{false, true} {
		t.Run(fmt.Sprint(staging), func(t *testing.T) {
			f := newACMEWorkerFixture(t)
			f.ca.mu.Lock()
			f.ca.lifetime = 90 * 24 * time.Hour
			f.ca.mu.Unlock()
			if j := f.run(t, "ssl.issue"); j.State != "done" {
				t.Fatal(j)
			}
			f.a.Cfg.ACMEUseStaging = !staging
			f.a.Cfg.ACMEEmail = "test@example.test"
			if err := f.a.Store.Tx(context.Background(), func(tx *sql.Tx) error {
				_, err := tx.Exec("UPDATE domains SET cert_staging=? WHERE site_id=?", staging, f.sid)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			f.ca.mu.Lock()
			before := f.ca.requests
			f.ca.mu.Unlock()
			r := f.server.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: "ssl.renew", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q}`, f.sid))})
			j := waitACMEAdmissionJob(t, f, r)
			if j.State != "done" {
				t.Fatal(j)
			}
			want := fmt.Sprintf(`{"site_id":%q,"staging":%t}`, f.sid, staging)
			if string(j.Args) != want {
				t.Fatalf("durable environment: %s want %s", j.Args, want)
			}
			f.ca.mu.Lock()
			defer f.ca.mu.Unlock()
			if f.ca.requests != before {
				t.Fatal("valid renewal contacted CA")
			}
		})
	}
}

func TestACMERenewWorkerRejectsChangedEnvironmentAndUnknownFields(t *testing.T) {
	for _, extra := range []string{`"staging":false`, `"staging":true,"unknown":true`} {
		t.Run(extra, func(t *testing.T) {
			f := newACMEWorkerFixture(t)
			if j := f.run(t, "ssl.issue"); j.State != "done" {
				t.Fatal(j)
			}
			f.ca.mu.Lock()
			before := f.ca.requests
			f.ca.mu.Unlock()
			jid, _ := id.New()
			j := jobs.Job{ID: jid, Verb: "ssl.renew", ObjectKey: "site:" + f.sid, Role: "owner", CreatedBy: "scheduler@local", Effectful: true, State: "running", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,%s}`, f.sid, extra))}
			if err := f.a.Store.CreateJob(context.Background(), j); err != nil {
				t.Fatal(err)
			}
			err := f.server.ExecuteJob(context.Background(), &j)
			coded, ok := err.(*rpc.CodedError)
			want := "precondition_failed"
			if extra != `"staging":false` {
				want = "bad_request"
			}
			if !ok || coded.Code != want {
				t.Fatalf("want %s, got %v", want, err)
			}
			f.ca.mu.Lock()
			defer f.ca.mu.Unlock()
			if f.ca.requests != before {
				t.Fatal("invalid scheduled renewal contacted CA")
			}
		})
	}
}

func TestACMERenewRefusesMissingOrInconsistentProvenance(t *testing.T) {
	for _, state := range []string{"missing_pair", "missing_metadata", "mixed_environment"} {
		t.Run(state, func(t *testing.T) {
			f := newACMEWorkerFixture(t)
			if j := f.run(t, "ssl.issue"); j.State != "done" {
				t.Fatal(j)
			}
			switch state {
			case "missing_pair":
				if err := os.Remove(filepath.Join(f.a.Cfg.TLSDir, "example.test/current")); err != nil {
					t.Fatal(err)
				}
			case "missing_metadata":
				if err := f.a.Store.Tx(context.Background(), func(tx *sql.Tx) error {
					_, err := tx.Exec("UPDATE domains SET cert_state='none', cert_not_after=NULL WHERE site_id=?", f.sid)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			case "mixed_environment":
				did, _ := id.New()
				if _, err := f.a.Store.AddDomain(context.Background(), store.Domain{ID: did, SiteID: f.sid, FQDN: "alias.example.test"}); err != nil {
					t.Fatal(err)
				}
				if err := f.a.Store.Tx(context.Background(), func(tx *sql.Tx) error {
					_, err := tx.Exec("UPDATE domains SET cert_state='issued',cert_staging=0,cert_not_after=(SELECT cert_not_after FROM domains WHERE site_id=? AND is_primary=1) WHERE id=?", f.sid, did)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			r := f.server.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: "ssl.renew", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q}`, f.sid))})
			if r.Code != "precondition_failed" {
				t.Fatal(r)
			}
		})
	}
}
