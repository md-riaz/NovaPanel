package verbs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

func TestACMERecoveryWithUnrelatedCompositeSnapshot(t *testing.T) {
	f := newACMEWorkerFixture(t)
	ctx := context.Background()
	st := f.a.Store
	sid, _ := id.New()
	oldID, _ := id.New()
	newID, _ := id.New()
	if _, err := st.CreateSite(ctx, store.Site{ID: sid, Name: "hold", Type: "static", Docroot: "/srv/sites/hold/public"}, store.Domain{ID: oldID, FQDN: "hold.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDomain(ctx, store.Domain{ID: newID, SiteID: sid, FQDN: "alias.hold.example.test"}); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"hold.example.test", "alias.hold.example.test"}
	now := time.Now()
	leaf := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: names, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := certs.Validate(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), names, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = certs.Install(f.a.Cfg.TLSDir, names[0], pair); err != nil {
		t.Fatal(err)
	}
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE sites SET cert_source='manual' WHERE id=?`, sid)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var finalizerOnce, crashOnce sync.Once
	finalizer, crash := make(chan struct{}), make(chan struct{})
	f.a.Nginx.Probe = func(ctx context.Context, desired nginx.Desired) error {
		if desired.Name == "hold" && desired.Maintenance {
			finalizerOnce.Do(func() { close(finalizer) })
			return errors.New("injected finalizer failure")
		}
		if desired.Name == "demo" && desired.TLS != nil {
			crashOnce.Do(func() { close(crash) })
			<-ctx.Done()
			panic("simulated crash")
		}
		return nil
	}
	args, _ := json.Marshal(map[string]any{"site_id": sid, "primary_domain_id": newID, "maintenance": true})
	reply := f.server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.update", Args: args})
	if !reply.OK {
		t.Fatal(reply)
	}
	parent := reply.Data.(jobs.EnqueueResult).Job.ID
	select {
	case <-finalizer:
	case <-time.After(10 * time.Second):
		t.Fatal("finalizer not reached")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		children, err := st.OperationChildren(ctx, parent)
		intent, intentErr := st.TLSIntent(ctx)
		if err == nil && intentErr == nil && len(children) == 2 && children[0].Status == "committed" && len(children[0].Identity.TLS) != 0 && children[1].Status == "pending" && len(intent) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("composite did not settle: %+v %v intent=%s %v", children, err, intent, intentErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	args, _ = json.Marshal(map[string]any{"site_id": f.sid, "staging": true})
	reply = f.server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "ssl.issue", Args: args})
	if !reply.OK {
		t.Fatal(reply)
	}
	issue := reply.Data.(jobs.EnqueueResult).Job.ID
	select {
	case <-crash:
	case <-time.After(15 * time.Second):
		t.Fatal("ACME publication not reached")
	}
	f.stop()
	raw, err := st.TLSIntent(ctx)
	var binding struct {
		JobID string `json:"job_id"`
	}
	if err != nil || json.Unmarshal(raw, &binding) != nil || binding.JobID != issue {
		t.Fatalf("missing ACME intent: %s %v", raw, err)
	}
	f.a.Nginx.Probe = func(_ context.Context, desired nginx.Desired) error {
		if desired.Name == "hold" && desired.Maintenance {
			return errors.New("injected finalizer failure")
		}
		return nil
	}
	f.server.ConfigureJobs()
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_ = f.a.Jobs.PrepareStartup(startup)
	j, err := st.GetJob(ctx, issue)
	if err != nil || j.State != "done" {
		t.Fatalf("ACME recovery: %+v %v", j, err)
	}
	if _, err = certs.Load(f.a.Cfg.TLSDir, "example.test", []string{"example.test"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if raw, err = st.TLSIntent(ctx); err != nil || len(raw) != 0 {
		t.Fatalf("intent remains: %s %v", raw, err)
	}
	if anomaly, err := st.Setting(ctx, "tls:publication:unreconciled:v1"); err == nil {
		t.Fatalf("orphaned: %s", anomaly)
	}
	children, err := st.OperationChildren(ctx, parent)
	if err != nil || len(children) != 2 || children[0].Status != "committed" || children[1].Status != "pending" {
		t.Fatalf("composite changed: %+v %v", children, err)
	}
}
