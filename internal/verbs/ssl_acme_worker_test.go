package verbs

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"novapanel/internal/acme"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

type acmeWorkerFixture struct {
	a      *agent.Agent
	server *rpc.Server
	ca     *fakeCA
	sid    string
}

func newACMEWorkerFixture(t *testing.T) *acmeWorkerFixture {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log, err := audit.Open(st, filepath.Join(root, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Secret, err = secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a.Cfg.TLSDir = filepath.Join(root, "tls")
	a.Cfg.ACMEWebroot = filepath.Join(root, "acme")
	a.Nginx.Root = filepath.Join(root, "nginx")
	for _, p := range []string{a.Cfg.TLSDir, filepath.Join(a.Cfg.ACMEWebroot, "demo"), filepath.Join(a.Nginx.Root, "sites-available"), filepath.Join(a.Nginx.Root, "sites-enabled")} {
		if err = os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Chmod(a.Cfg.TLSDir, 0700); err != nil {
		t.Fatal(err)
	}
	ca := newFakeCA(t, filepath.Join(a.Cfg.ACMEWebroot, "demo"))
	a.Cfg.ACMEDirectoryStaging = ca.server.URL + "/directory"
	a.Nginx.Runner = &acmeTestRunner{}
	a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
	sid, _ := id.New()
	did, _ := id.New()
	if _, err = st.CreateSite(context.Background(), store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(context.Background(), sid, "active"); err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(a)
	// Retain production dispatch, decoding, authority and worker; inject only CA roots.
	registry := make(map[string]rpc.Verb, len(server.Registry))
	for k, v := range server.Registry {
		registry[k] = v
	}
	server.Registry = registry
	for _, name := range []string{"ssl.issue", "ssl.renew"} {
		v := registry[name]
		verb := name
		v.Exec = func(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
			return (acmeExecutor{a}).execute(ctx, j, verb, ca.server.Client())
		}
		registry[name] = v
	}
	server.ConfigureJobs()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return &acmeWorkerFixture{a, server, ca, sid}
}
func (f *acmeWorkerFixture) run(t *testing.T, verb string) jobs.Job {
	t.Helper()
	ctx := context.Background()
	reply := f.server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"staging":true}`, f.sid))})
	if !reply.OK {
		t.Fatal(reply)
	}
	enq, ok := reply.Data.(jobs.EnqueueResult)
	if !ok {
		t.Fatalf("expected job: %+v", reply)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, e := f.a.Store.GetJob(ctx, enq.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "done" || j.State == "failed" {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker timeout")
	return jobs.Job{}
}
func TestACMEWorkerCanonicalResults(t *testing.T) {
	f := newACMEWorkerFixture(t)
	for _, verb := range []string{"ssl.issue", "ssl.renew"} {
		j := f.run(t, verb)
		if j.State != "done" {
			t.Fatalf("%s: %+v", verb, j)
		}
		var result struct {
			FQDNs    []string `json:"fqdns"`
			NotAfter int64    `json:"not_after"`
		}
		if err := json.Unmarshal(j.Result, &result); err != nil {
			t.Fatal(err)
		}
		ds, err := f.a.Store.ListDomains(context.Background(), f.sid)
		if err != nil {
			t.Fatal(err)
		}
		if result.NotAfter == 0 || ds[0].CertNotAfter == nil || result.NotAfter != *ds[0].CertNotAfter {
			t.Fatalf("noncanonical result: %s", j.Result)
		}
		var fields map[string]json.RawMessage
		json.Unmarshal(j.Result, &fields)
		if verb == "ssl.issue" {
			if len(fields) != 2 || !reflect.DeepEqual(result.FQDNs, []string{"example.test"}) {
				t.Fatalf("issue: %s", j.Result)
			}
		} else if len(fields) != 1 {
			t.Fatalf("renew: %s", j.Result)
		}
		// Recreate the committed-before-terminal boundary and replay through ExecuteJob.
		j.State = "running"
		if err = f.a.Store.UpdateJob(context.Background(), j); err != nil {
			t.Fatal(err)
		}
		original := append([]byte(nil), j.Result...)
		j.Result = nil
		f.ca.mu.Lock()
		before := f.ca.requests
		f.ca.mu.Unlock()
		if err = f.server.ExecuteJob(context.Background(), &j); err != nil {
			t.Fatal(err)
		}
		f.ca.mu.Lock()
		after := f.ca.requests
		f.ca.mu.Unlock()
		if !bytes.Equal(j.Result, original) || before != after {
			t.Fatalf("replay changed result/CA: %s -> %s, %d/%d", original, j.Result, before, after)
		}
		j.State = "done"
		if err = f.a.Store.UpdateJob(context.Background(), j); err != nil {
			t.Fatal(err)
		}
	}
}
func TestACMEWorkerRenewExpandedSANs(t *testing.T) {
	f := newACMEWorkerFixture(t)
	if j := f.run(t, "ssl.issue"); j.State != "done" {
		t.Fatal(j)
	}
	did, _ := id.New()
	if _, err := f.a.Store.AddDomain(context.Background(), store.Domain{ID: did, SiteID: f.sid, FQDN: "alias.example.test"}); err != nil {
		t.Fatal(err)
	}
	j := f.run(t, "ssl.renew")
	if j.State != "done" {
		t.Fatalf("expanded renewal: %+v", j)
	}
	if _, err := certs.Load(f.a.Cfg.TLSDir, "example.test", []string{"example.test", "alias.example.test"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.ca.mu.Lock()
	defer f.ca.mu.Unlock()
	if f.ca.orders != 2 || f.ca.registrations != 1 || f.ca.replacements[1] == "" || !reflect.DeepEqual(f.ca.domains, []string{"example.test", "alias.example.test"}) {
		t.Fatalf("orders=%d replacements=%v SANs=%v", f.ca.orders, f.ca.replacements, f.ca.domains)
	}
}
func TestACMEWorkerRenewExpiredSource(t *testing.T) {
	f := newACMEWorkerFixture(t)
	if j := f.run(t, "ssl.issue"); j.State != "done" {
		t.Fatal(j)
	}
	// Replace only the scratch installed pair with a structurally valid expired leaf.
	now := time.Now()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(42), DNSNames: []string{"example.test"}, NotBefore: now.Add(-48 * time.Hour), NotAfter: now.Add(-time.Hour), AuthorityKeyId: []byte{1, 2, 3, 4}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb})
	for name, b := range map[string][]byte{"fullchain.pem": certPEM, "privkey.pem": keyPEM} {
		if err = os.WriteFile(filepath.Join(f.a.Cfg.TLSDir, "example.test/current", name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = certs.Load(f.a.Cfg.TLSDir, "example.test", []string{"example.test"}, now); err == nil {
		t.Fatal("expired pair accepted for serving")
	}
	if _, err = nginx.LoadTLS(f.a.Cfg.TLSDir, "example.test", false, now); err == nil {
		t.Fatal("expired pair accepted as nginx TLS state")
	}
	if j := f.run(t, "ssl.renew"); j.State != "done" {
		t.Fatalf("expired renewal: %+v", j)
	}
	if _, err = certs.Load(f.a.Cfg.TLSDir, "example.test", []string{"example.test"}, now); err != nil {
		t.Fatal(err)
	}
	f.ca.mu.Lock()
	defer f.ca.mu.Unlock()
	if f.ca.orders != 2 || f.ca.replacements[1] == "" {
		t.Fatalf("orders=%d replacements=%v", f.ca.orders, f.ca.replacements)
	}
}

func TestACMEWorkerRetryAfterCAFailure(t *testing.T) {
	f := newACMEWorkerFixture(t)
	f.ca.mu.Lock()
	f.ca.fail = true
	f.ca.mu.Unlock()
	if j := f.run(t, "ssl.issue"); j.State != "failed" || j.Code != "agent_error" {
		t.Fatalf("failed CA order: %+v", j)
	}
	f.ca.mu.Lock()
	f.ca.fail = false
	f.ca.mu.Unlock()
	if j := f.run(t, "ssl.issue"); j.State != "done" {
		t.Fatalf("CA retry: %+v", j)
	}
	f.ca.mu.Lock()
	defer f.ca.mu.Unlock()
	if f.ca.orders != 2 || f.ca.registrations != 1 {
		t.Fatalf("retry orders=%d registrations=%d", f.ca.orders, f.ca.registrations)
	}
}

func TestACMEWorkerThrottleBeforeCA(t *testing.T) {
	f := newACMEWorkerFixture(t)
	p := acme.Policy{Store: f.a.Store, Now: f.a.Now}
	for i := 0; i < 5; i++ {
		if err := p.BeforeOrder(context.Background(), acme.Order{Environment: acme.Staging, Domains: []string{"example.test"}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		j := f.run(t, "ssl.issue")
		if j.State != "failed" || j.Code != "rate_limited" {
			t.Fatalf("throttle attempt %d: %+v", i, j)
		}
	}
	f.ca.mu.Lock()
	defer f.ca.mu.Unlock()
	if f.ca.requests != 0 {
		t.Fatalf("throttled requests=%d", f.ca.requests)
	}
}
