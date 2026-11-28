package verbs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/dns"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

type dnsFailureProvider struct {
	dns.Provider
	fail    *atomic.Bool
	message string
}

func (p dnsFailureProvider) List(ctx context.Context, zone string) ([]dns.Record, error) {
	if p.fail.Load() {
		return nil, errors.New(p.message)
	}
	return p.Provider.List(ctx, zone)
}
func TestDNSSecretMatrix(t *testing.T) {
	const token = "dns-secret-fixture-never-persist-4938761"
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "panel.db")
	auditPath := filepath.Join(dir, "audit.log")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	auditLog, err := audit.Open(s, auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer auditLog.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	fake := &dns.Fake{}
	var fail atomic.Bool
	a := agent.New()
	a.Store = s
	a.Audit = auditLog
	a.Secret = sealer
	a.ProbeDNS = func(context.Context, string, []string) (map[string]string, error) {
		return map[string]string{"example.com": "zone-id"}, nil
	}
	a.DNSProvider = func(string, map[string]string) dns.Provider {
		return dnsFailureProvider{Provider: fake, fail: &fail, message: token}
	}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	var output bytes.Buffer
	old := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(old)
	call := func(verb string, args any) rpc.Reply {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: raw})
		encoded, err := json.Marshal(reply)
		if err != nil || bytes.Contains(encoded, []byte(token)) {
			t.Fatal("token exposed in RPC reply")
		}
		return reply
	}
	setup := call("integration.set", integrationSetArgs{Kind: "cloudflare", Token: token, Zones: []string{"example.com"}})
	if !setup.OK {
		t.Fatalf("integration: %+v", setup)
	}
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(worker) }()
	defer func() { cancel(); <-done }()
	wait := func(reply rpc.Reply, want string) store.Job {
		t.Helper()
		if !reply.OK {
			t.Fatalf("admission failed: %+v", reply)
		}
		queued, ok := reply.Data.(jobs.EnqueueResult)
		if !ok {
			t.Fatal("missing queued job")
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			job, err := s.GetJob(ctx, queued.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if job.State == "done" || job.State == "failed" {
				encoded, _ := json.Marshal(job)
				if bytes.Contains(encoded, []byte(token)) {
					t.Fatal("token exposed in job")
				}
				events, err := s.JobEvents(ctx, job.ID)
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ = json.Marshal(events)
				if bytes.Contains(encoded, []byte(token)) {
					t.Fatal("token exposed in job events")
				}
				if job.State != want {
					t.Fatalf("unexpected job state %s, code %s", job.State, job.Code)
				}
				return job
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("job timeout")
		return store.Job{}
	}
	created := wait(call("dns.record.create", map[string]any{"zone": "example.com", "type": "A", "name": "www.example.com", "value": "192.0.2.1"}), "done")
	var result struct {
		RecordID string `json:"record_id"`
	}
	if json.Unmarshal(created.Result, &result) != nil || result.RecordID == "" {
		t.Fatal("missing record id")
	}
	listed := call("dns.record.list", map[string]any{"zone": "example.com"})
	if !listed.OK {
		t.Fatal("list failed")
	}
	fail.Store(true)
	update := wait(call("dns.record.update", map[string]any{"zone": "example.com", "record_id": result.RecordID, "value": "192.0.2.2"}), "failed")
	if update.Code != "interrupted" || strings.Contains(update.Error, token) {
		t.Fatal("provider error exposed")
	}
	fail.Store(false)
	if err = reconcileDNS(ctx, a); err != nil {
		t.Fatal(err)
	}
	wait(call("dns.record.delete", map[string]any{"zone": "example.com", "record_id": result.RecordID}), "done")
	fail.Store(true)
	interruptedCreate := wait(call("dns.record.create", map[string]any{"zone": "example.com", "type": "A", "name": "other.example.com", "value": "192.0.2.9"}), "failed")
	if interruptedCreate.Code != "interrupted" || strings.Contains(interruptedCreate.Error, token) {
		t.Fatal("create error exposed")
	}
	var pending struct {
		RecordID string `json:"record_id"`
	}
	if json.Unmarshal(interruptedCreate.Args, &pending) != nil || pending.RecordID == "" {
		t.Fatal("missing interrupted record id")
	}
	fail.Store(false)
	if err = reconcileDNS(ctx, a); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	interruptedDelete := wait(call("dns.record.delete", map[string]any{"zone": "example.com", "record_id": pending.RecordID}), "failed")
	if interruptedDelete.Code != "interrupted" || strings.Contains(interruptedDelete.Error, token) {
		t.Fatal("delete error exposed")
	}
	fail.Store(false)
	if err = reconcileDNS(ctx, a); err != nil {
		t.Fatal(err)
	}
	bad := call("dns.record.create", map[string]any{"zone": "example.com", "type": "TXT", "name": "example.com", "value": "test", "proxied": true})
	if bad.OK || bad.Code != "invalid_argument" {
		t.Fatal("proxy guard failed")
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var events int
	if err = db.QueryRow("SELECT count(*) FROM events WHERE verb LIKE 'dns.record.%'").Scan(&events); err != nil || events < 4 {
		t.Fatal("missing DNS audit events")
	}
	for _, path := range []string{dbPath, dbPath + "-wal", auditPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(token)) {
			t.Fatalf("token in %s", filepath.Base(path))
		}
	}
	if bytes.Contains(output.Bytes(), []byte(token)) {
		t.Fatal("token in log")
	}
	redacted, err := json.Marshal(secret.Redact(map[string]any{"token": token}))
	if err != nil || bytes.Contains(redacted, []byte(token)) {
		t.Fatal("redaction failed")
	}
}
