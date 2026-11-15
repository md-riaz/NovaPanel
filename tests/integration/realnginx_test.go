package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/platform"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
	_ "novapanel/internal/verbs"
)

// Only execute the compiled test binary in a dedicated, nonroot, capability-free
// container. The external launcher must also assert mounts, networking and limits.
type nginxLab struct {
	t       *testing.T
	root    string
	reloads int
	invalid bool
	modern  bool
}

func (l *nginxLab) command(args ...string) ([]byte, []byte, int, error) {
	out, stderr, code, err := (runner.Real{}).Run(context.Background(), "/usr/sbin/nginx", append([]string{"-p", l.root + "/", "-c", l.root + "/nginx.conf"}, args...), runner.RunOpts{})
	l.t.Logf("real nginx %v exit=%d stderr=%s", args, code, stderr)
	return out, stderr, code, err
}
func (l *nginxLab) Run(_ context.Context, prog string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	files, err := filepath.Glob(l.root + "/sites-enabled/*.conf")
	if err != nil {
		return nil, nil, -1, err
	}
	var b strings.Builder
	for _, f := range files {
		data, e := os.ReadFile(f)
		if e != nil {
			return nil, nil, -1, e
		}
		b.Write(data)
	}
	text := strings.NewReplacer("listen 80;", "listen 8080;", "listen [::]:80;", "listen [::]:8080;", "listen 443 ssl", "listen 8443 ssl", "listen [::]:443 ssl", "listen [::]:8443 ssl", "/var/www/novapanel", l.root+"/maintenance").Replace(b.String())
	if l.invalid {
		text += "\ninvalid_lab_directive;\n"
		l.invalid = false
	}
	if err = os.WriteFile(l.root+"/active.conf", []byte(text), 0600); err != nil {
		return nil, nil, -1, err
	}
	if prog == platform.Nginx && len(args) == 1 && args[0] == "-t" {
		return l.command("-t")
	}
	if prog == platform.Systemctl && strings.Join(args, " ") == "reload nginx" {
		l.reloads++
		out, stderr, code, e := l.command("-s", "reload")
		time.Sleep(150 * time.Millisecond)
		return out, stderr, code, e
	}
	return nil, nil, -1, fmt.Errorf("unexpected lab command %s %v", prog, args)
}
func newNginxLab(t *testing.T) (*nginxLab, *nginx.Applier) {
	t.Helper()
	if os.Getenv("NOVAPANEL_REAL_NGINX") != "1" {
		t.Skip("opt-in dedicated nonroot container only")
	}
	if os.Geteuid() == 0 {
		t.Fatal("refusing root")
	}
	if _, e := os.Stat("/.dockerenv"); e != nil {
		t.Fatal("refusing non-container")
	}
	status, e := os.ReadFile("/proc/self/status")
	if e != nil || !strings.Contains(string(status), "CapEff:\t0000000000000000") || !strings.Contains(string(status), "NoNewPrivs:\t1") {
		t.Fatal("requires zero capabilities and no-new-privileges")
	}
	root, e := os.MkdirTemp("/tmp", "p3-")
	if e != nil {
		t.Fatal(e)
	}
	l := &nginxLab{t: t, root: root}
	_, version, code, err := (runner.Real{}).Run(context.Background(), platform.Nginx, []string{"-v"}, runner.RunOpts{})
	if err != nil || code != 0 {
		t.Fatalf("nginx version: %s %v", version, err)
	}
	var major, minor, patch int
	if _, err = fmt.Sscanf(strings.TrimSpace(string(version)), "nginx version: nginx/%d.%d.%d", &major, &minor, &patch); err != nil {
		t.Fatal(err)
	}
	l.modern = major > 1 || major == 1 && (minor > 25 || minor == 25 && patch >= 1)
	t.Logf("%s modern_http2=%v", strings.TrimSpace(string(version)), l.modern)
	for _, dir := range []string{"sites-available", "sites-enabled", "public", "acme/.well-known/acme-challenge", "maintenance", "tls"} {
		if e = os.MkdirAll(root+"/"+dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	for name, data := range map[string]string{"public/index.html": "phase3-real-nginx", "acme/.well-known/acme-challenge/token": "token.authorization", "active.conf": ""} {
		if e = os.WriteFile(root+"/"+name, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	page, err := os.ReadFile("../../packaging/rootfs/var/www/novapanel/maintenance.html")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(root+"/maintenance/maintenance.html", page, 0644); err != nil {
		t.Fatal(err)
	}
	conf := "worker_processes 1; pid " + root + "/nginx.pid; error_log " + root + "/error.log notice; events { worker_connections 64; } http { access_log " + root + "/access.log; client_body_temp_path " + root + "/body; proxy_temp_path " + root + "/proxy; fastcgi_temp_path " + root + "/fastcgi; uwsgi_temp_path " + root + "/uwsgi; scgi_temp_path " + root + "/scgi; include " + root + "/active.conf; }"
	if e = os.WriteFile(root+"/nginx.conf", []byte(conf), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = l.command(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		l.command("-s", "quit")
		for _, name := range []string{"error.log", "access.log"} {
			b, _ := os.ReadFile(root + "/" + name)
			t.Logf("%s:\n%s", name, b)
		}
		os.RemoveAll(root)
	})
	a := &nginx.Applier{Root: root, Runner: l, Probe: nginx.StateProbe("http://127.0.0.1:8080", "https://127.0.0.1:8443", nil)}
	return l, a
}
func labCertificate(t *testing.T) *certs.Certificate {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	c := &x509.Certificate{SerialNumber: serial, DNSNames: []string{"demo.test", "alias.test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, c, c, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	kd, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	pair, e := certs.Validate(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), c.DNSNames, now)
	if e != nil {
		t.Fatal(e)
	}
	return pair
}
func labGet(t *testing.T, path string, want int, body string) {
	t.Helper()
	c := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, _ := http.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
	r.Host = "demo.test"
	res, e := c.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != want || body != "" && !strings.Contains(string(b), body) {
		t.Fatalf("GET %s status=%d body=%s", path, res.StatusCode, b)
	}
	t.Logf("HTTP %s status=%d body=%q", path, res.StatusCode, b)
}
func TestRealNginxRenderPublishRollback(t *testing.T) {
	l, a := newNginxLab(t)
	ctx := context.Background()
	d := nginx.Desired{Name: "demo", Domains: []string{"demo.test", "alias.test"}, Docroot: l.root + "/public", ACMERoot: l.root + "/acme", ModernHTTP2: l.modern}
	apply := func() {
		t.Helper()
		if e := a.Apply(ctx, d); e != nil {
			t.Fatal(e)
		}
	}
	apply()
	labGet(t, "/", 200, "phase3-real-nginx")
	labGet(t, "/.well-known/acme-challenge/token", 200, "token.authorization")
	d.ForceHTTPS = true
	if e := a.Apply(ctx, d); e == nil {
		t.Fatal("force HTTPS accepted without cert")
	}
	d.ForceHTTPS = false
	if _, e := certs.Install(l.root+"/tls", "demo.test", labCertificate(t)); e != nil {
		t.Fatal(e)
	}
	state, e := nginx.LoadTLS(l.root+"/tls", "demo.test", false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	d.TLS = state
	apply()
	active, err := os.ReadFile(l.root + "/active.conf")
	if err != nil || strings.Contains(string(active), "http2 on;") != l.modern || strings.Contains(string(active), "listen 8443 ssl http2;") == l.modern {
		t.Fatalf("wrong HTTP2 shape: %v\n%s", err, active)
	}
	t.Logf("validated HTTP2 shape modern=%v", l.modern)
	labGet(t, "/", 200, "")
	d.ForceHTTPS = true
	d.HSTSMaxAge = 300
	apply()
	labGet(t, "/", 301, "")
	labGet(t, "/.well-known/acme-challenge/token", 200, "token.authorization")
	labGet(t, "/.well-known/acme-challenge/missing", 404, "")
	d.Maintenance = true
	apply()
	labGet(t, "/.well-known/acme-challenge/token", 200, "token.authorization")
	d.ForceHTTPS = false
	d.HSTSMaxAge = 0
	apply()
	labGet(t, "/", 503, "Site under maintenance")
	labGet(t, "/.well-known/acme-challenge/token", 200, "token.authorization")
	d.Maintenance = false
	apply()
	before, _ := os.ReadFile(l.root + "/sites-available/demo.conf")
	n := l.reloads
	l.invalid = true
	d.Redirects = []nginx.Redirect{{Source: "/old", Target: "https://demo.test/new", Code: 302}}
	if e = a.Apply(ctx, d); e == nil {
		t.Fatal("invalid nginx accepted")
	}
	if l.reloads != n {
		t.Fatal("reloaded invalid nginx")
	}
	after, _ := os.ReadFile(l.root + "/sites-available/demo.conf")
	if string(before) != string(after) {
		t.Fatal("syntax rollback failed")
	}
	d.Redirects = nil
	d.Maintenance = true
	probe := a.Probe
	a.Probe = func(c context.Context, v nginx.Desired) error { v.Maintenance = false; return probe(c, v) }
	if e = a.Apply(ctx, d); e == nil {
		t.Fatal("real 503 accepted by nonmaintenance probe")
	}
	a.Probe = probe
	labGet(t, "/", 200, "phase3-real-nginx")
	d.Maintenance = false
	old := state
	token, e := certs.Stage(l.root+"/tls", "demo.test", labCertificate(t))
	if e != nil {
		t.Fatal(e)
	}
	if e = certs.Publish(token); e != nil {
		t.Fatal(e)
	}
	d.TLS, e = nginx.LoadTLS(l.root+"/tls", "demo.test", false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	d.ForceReload = true
	apply()
	mismatch := d
	mismatch.TLS = old
	if e = probe(ctx, mismatch); e == nil {
		t.Fatal("old pinned leaf accepted replacement")
	}
	if e = certs.Restore(token); e != nil {
		t.Fatal(e)
	}
	d.TLS = old
	apply()
	t.Log("real-nginx manual self-signed exact-leaf pin, pair publication and restoration verified")
}
func TestRealNginxPrimaryRekeyJob(t *testing.T) {
	l, ap := newNginxLab(t)
	ctx := context.Background()
	st, e := store.Open(l.root + "/panel.db")
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	log, e := audit.Open(st, l.root+"/audit.log")
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Nginx = ap
	a.Cfg.TLSDir = l.root + "/tls"
	a.Cfg.ACMEWebroot = l.root + "/acme"
	sid, _ := id.New()
	oid, _ := id.New()
	nid, _ := id.New()
	if _, e = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: l.root + "/public"}, store.Domain{ID: oid, FQDN: "demo.test"}); e != nil {
		t.Fatal(e)
	}
	if e = st.SetSiteStatus(ctx, sid, "active"); e != nil {
		t.Fatal(e)
	}
	if _, e = st.AddDomain(ctx, store.Domain{ID: nid, SiteID: sid, FQDN: "alias.test"}); e != nil {
		t.Fatal(e)
	}
	if _, e = certs.Install(a.Cfg.TLSDir, "demo.test", labCertificate(t)); e != nil {
		t.Fatal(e)
	}
	if e = st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE sites SET cert_source='manual' WHERE id=?", sid)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	run := func(args any) error {
		b, _ := json.Marshal(args)
		res := s.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.update", Args: b})
		if !res.OK {
			return fmt.Errorf("admission: %+v", res)
		}
		j, e := st.StartJob(ctx, res.Data.(jobs.EnqueueResult).Job.ID, a.Now())
		if e != nil {
			return e
		}
		return s.ExecuteJob(ctx, &j)
	}
	if e = run(map[string]any{"site_id": sid, "maintenance": false}); e != nil {
		t.Fatal(e)
	}
	old, _ := os.Readlink(a.Cfg.TLSDir + "/demo.test/current")
	if e = run(map[string]any{"site_id": sid, "primary_domain_id": nid}); e != nil {
		t.Fatal(e)
	}
	domain, e := st.DomainByID(ctx, nid)
	if e != nil || !domain.IsPrimary {
		t.Fatal("primary not committed", e)
	}
	still, _ := os.Readlink(a.Cfg.TLSDir + "/demo.test/current")
	if still != old {
		t.Fatal("old primary path changed")
	}
	if _, e = nginx.LoadTLS(a.Cfg.TLSDir, "alias.test", false, time.Now()); e != nil {
		t.Fatal(e)
	}
	labGet(t, "/", 200, "phase3-real-nginx")
	intent, e := st.TLSIntent(ctx)
	if e != nil || len(intent) != 0 {
		t.Fatal("unfinished intent", e)
	}
	t.Log("real-nginx site.update primary rekey committed SQLite and retained old certificate path")
	probe := ap.Probe
	ap.Probe = func(c context.Context, d nginx.Desired) error { d.Maintenance = false; return probe(c, d) }
	if e = run(map[string]any{"site_id": sid, "primary_domain_id": oid, "maintenance": true}); e == nil {
		t.Fatal("primary rekey ignored real 503 probe failure")
	}
	ap.Probe = probe
	domain, e = st.DomainByID(ctx, nid)
	if e != nil || !domain.IsPrimary {
		t.Fatal("failed rekey changed primary", e)
	}
	still, _ = os.Readlink(a.Cfg.TLSDir + "/demo.test/current")
	if still != old {
		t.Fatal("failed rekey changed retained target pointer")
	}
	labGet(t, "/", 200, "phase3-real-nginx")
	intent, e = st.TLSIntent(ctx)
	if e != nil || len(intent) != 0 {
		t.Fatal("failed rekey left recovery intent", e)
	}
	t.Log("real-nginx failed reverse rekey restored certificate pointer, config, service and SQLite primary")
}
