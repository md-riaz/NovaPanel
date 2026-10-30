package verbs_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/secret"
	certs "novapanel/internal/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func TestSSLUploadSealedAndPublished(t *testing.T) {
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
	a.Cfg.TLSDir = filepath.Join(root, "tls")
	if err = os.Mkdir(a.Cfg.TLSDir, 0700); err != nil {
		t.Fatal(err)
	}
	ng := filepath.Join(root, "nginx")
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err = os.MkdirAll(filepath.Join(ng, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	a.Nginx.Root = ng
	a.Nginx.Runner = sslRunner{}
	a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
	a.Store = st
	a.Audit = log
	a.Secret, err = secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := id.New()
	did, _ := id.New()
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "ssl-demo", Type: "static", Docroot: "/srv/sites/ssl-demo/public"}, store.Domain{ID: did, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	st.SetSiteStatus(ctx, sid, "active")
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "demo.test"}, DNSNames: []string{"demo.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cp := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	kp := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	raw, _ := json.Marshal(map[string]string{"site_id": sid, "cert_pem": cp, "key_pem": kp})
	for _, caller := range []rpc.Caller{{Role: "read_only"}, {Role: "admin", Kind: "user", MFAat: 0}} {
		r := server.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: "ssl.upload", Args: raw})
		if r.Code != "forbidden" {
			t.Fatalf("auth: %+v", r)
		}
	}
	r := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "ssl.upload", Args: raw})
	if !r.OK {
		t.Fatal(r)
	}
	en := r.Data.(jobs.EnqueueResult)
	j := jobs.Job(en.Job)
	stored, err := a.Jobs.Status(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Args), "PRIVATE KEY") || strings.Contains(string(stored.Args), "CERTIFICATE") || j.Effectful || j.ObjectKey != "site:"+sid {
		t.Fatal("unsafe stored input")
	}
	j, err = st.StartJob(ctx, j.ID, a.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = rpc.Registry["ssl.upload"].Exec(ctx, a, &j); err != nil {
		t.Fatal(err)
	}
	if j.Step != "tls_committed" {
		t.Fatal(j.Step)
	}
	if err = rpc.Registry["ssl.upload"].Exec(ctx, a, &j); err != nil {
		t.Fatal(err)
	}
	j.State = "done"
	if err = st.UpdateJob(ctx, store.Job(j)); err != nil {
		t.Fatal(err)
	}
	stored, err = a.Jobs.Status(ctx, j.ID)
	if err != nil || string(stored.Args) != "{}" {
		t.Fatalf("cleanup: %s %v", stored.Args, err)
	}
	// A valid installed pair, not metadata, is required for issued status.
	a.Cfg.TLSDir = filepath.Join(root, "tls")
	if err = os.MkdirAll(a.Cfg.TLSDir, 0700); err != nil {
		t.Fatal(err)
	}
	c, err := certs.Validate([]byte(cp), []byte(kp), []string{"demo.test"}, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := certs.Stage(a.Cfg.TLSDir, "demo.test", c)
	if err != nil {
		t.Fatal(err)
	}
	if err = certs.Publish(token); err != nil {
		t.Fatal(err)
	}
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE sites SET cert_source='manual' WHERE id=?", sid)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	status := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "read_only"}), rpc.Request{Verb: "ssl.status", Args: json.RawMessage(`{"site_id":"` + sid + `"}`)})
	d := sessionData(t, status)["domains"].([]any)[0].(map[string]any)
	if d["cert_state"] != "issued" {
		t.Fatal(d)
	}
	token.Previous = ""
	if err = certs.Restore(token); err != nil {
		t.Fatal(err)
	}
	status = server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "read_only"}), rpc.Request{Verb: "ssl.status", Args: json.RawMessage(`{"site_id":"` + sid + `"}`)})
	d = sessionData(t, status)["domains"].([]any)[0].(map[string]any)
	if d["cert_state"] != "none" {
		t.Fatal(d)
	}
}

type sslRunner struct{}

func (sslRunner) Run(context.Context, string, []string, runner.RunOpts) ([]byte, []byte, int, error) {
	return nil, nil, 0, nil
}

func TestSSLStatusAndAutoSet(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, _ := id.New()
	did, _ := id.New()
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "ssl-demo", Type: "static", Docroot: "/srv/sites/ssl-demo/public"}, store.Domain{ID: did, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = st
	server := rpc.NewServer(a)
	args := json.RawMessage(`{"site_id":"` + sid + `"}`)
	r := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "read_only"}), rpc.Request{Verb: "ssl.status", Args: args})
	if !r.OK {
		t.Fatalf("status: %+v", r)
	}
	data := sessionData(t, r)
	if data["source"] != "none" || data["force_https"] != false || len(data["domains"].([]any)) != 1 {
		t.Fatal(data)
	}
	for name, slow := range map[string]bool{"ssl.upload": true, "ssl.status": false, "ssl.auto_set": false} {
		v, ok := rpc.Registry[name]
		if !ok || v.Slow != slow || v.Effectful {
			t.Fatalf("registry %s: %+v", name, v)
		}
	}
	v := rpc.Registry["ssl.upload"]
	if !v.MFA || v.MinRole != rpc.Admin || v.Scope != "sites:write" || v.Exec == nil {
		t.Fatal(v)
	}
	// Run the registered handler directly to isolate its store contract from the audit gate.
	v = rpc.Registry["ssl.auto_set"]
	av := v.NewArgs()
	if err = json.Unmarshal([]byte(`{"site_id":"`+sid+`","enabled":false}`), av); err != nil {
		t.Fatal(err)
	}
	if _, err = v.Run(ctx, a, av); err != nil {
		t.Fatal(err)
	}
	site, err := st.SiteByID(ctx, sid)
	if err != nil || site.SSLAuto || site.CertSource != "none" {
		t.Fatalf("%+v %v", site, err)
	}
	av = v.NewArgs()
	json.Unmarshal([]byte(`{"site_id":"`+sid+`"}`), av)
	if valid, ok := av.(interface{ Validate() error }); !ok || valid.Validate() == nil {
		t.Fatal("missing enabled accepted")
	}
}
