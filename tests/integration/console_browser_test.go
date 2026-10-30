package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/config"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

// Runs the production panel-web binary with the real UID-gated Unix transport,
// supervisor, queue and SQLite. Only privileged commands and nginx probe are injected.
func TestRealBackendConsoleBrowser(t *testing.T) {
	node := os.Getenv("NOVAPANEL_BROWSER_NODE")
	if node == "" {
		t.Skip("set NOVAPANEL_BROWSER_NODE for injected real-backend Chromium acceptance")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires disposable root runner for web UID drop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	// Short socket path, explicitly under the runner's scratch TMPDIR.
	scratch, err := os.MkdirTemp("", "np-browser-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(scratch)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Chmod(scratch, 0755))
	for _, target := range []struct {
		name string
		args []string
	}{
		{"agent.test", []string{"test", "-c", "-o", filepath.Join(scratch, "agent.test"), "./cmd/panel-agent"}},
		{"panel-web", []string{"build", "-o", filepath.Join(scratch, "panel-web"), "./cmd/panel-web"}},
	} {
		c := exec.CommandContext(ctx, "go", target.args...)
		c.Dir = root
		out, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("build %s: %v %s", target.name, e, out)
		}
	}
	cfg := config.Defaults()
	cfg.SocketPath = filepath.Join(scratch, "agent.sock")
	cfg.DBPath = filepath.Join(scratch, "state", "panel.db")
	cfg.SealKeyPath = filepath.Join(scratch, "state", "seal.key")
	cfg.LogDir = filepath.Join(scratch, "log")
	cfg.ACMEWebroot = filepath.Join(scratch, "acme")
	cfg.SitesRoot = filepath.Join(scratch, "sites")
	cfg.TLSDir = filepath.Join(scratch, "tls")
	cfg.WebUID = 65534
	cfg.WebGID = 65534
	cfg.ACMEDirectoryProd = "http://127.0.0.1:1/directory"
	cfg.ACMEDirectoryStaging = cfg.ACMEDirectoryProd
	raw, err := json.Marshal(cfg)
	must(err)
	daemon := exec.CommandContext(ctx, filepath.Join(scratch, "agent.test"), "-test.run=^TestSafeDaemonHelper$")
	daemon.Env = append(os.Environ(), "NOVAPANEL_SAFE_HELPER=1", "NOVAPANEL_SAFE_CONSOLE=1", "NOVAPANEL_SAFE_CONFIG="+string(raw))
	must(daemon.Start())
	defer func() { daemon.Process.Kill(); daemon.Wait() }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, e := net.Dial("unix", cfg.SocketPath)
		if e == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon startup timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
	client, err := rpc.Dial(cfg.SocketPath)
	must(err)
	defer client.Close()
	// Setup uses the existing local-root dispatcher seam, not a production auth bypass.
	reply, err := client.Call(ctx, "user.create", map[string]string{"username": "browserowner", "email": "browser@example.test", "role": "owner", "password": "disposable browser password"}, "")
	must(err)
	if !reply.OK {
		t.Fatalf("create scratch owner: %s", reply.Code)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	addr := listener.Addr().String()
	listener.Close()
	upstream, _ := url.Parse("http://" + addr)
	proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(upstream))
	defer proxy.Close()
	// chdir occurs before dropping credentials; /root ancestors remain private.
	web := exec.CommandContext(ctx, "/usr/bin/python3", "-c", `import os
os.setgroups([]);os.setgid(65534);os.setuid(65534)
os.execve('./panel-web',['./panel-web'],os.environ)`)
	web.Dir = scratch
	web.Env = append(os.Environ(), "NOVAPANEL_CONFIG=/dev/null", "NOVAPANEL_SOCKET_PATH=/proc/self/cwd/agent.sock", "NOVAPANEL_WEB_LISTEN="+addr, "NOVAPANEL_WEB_ORIGIN="+proxy.URL)
	must(web.Start())
	defer func() { web.Process.Kill(); web.Wait() }()
	for {
		r, e := http.Get("http://" + addr + "/assets/app.js")
		if e == nil {
			r.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("web startup timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	certTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.test"}, DNSNames: []string{"example.test", "alias.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, &key.PublicKey, key)
	must(err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	must(err)
	// PEM travels over stdin only, never command arguments, logs or artifacts.
	payload, _ := json.Marshal(map[string]string{"cert": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), "key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))})
	browser := exec.CommandContext(ctx, node, filepath.Join(root, "tests/integration/console_browser.cjs"), proxy.URL)
	browser.Stdin = strings.NewReader(string(payload))
	out, err := browser.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		info, statErr := os.Lstat(cfg.TLSDir)
		if statErr != nil {
			t.Logf("TLS publication root stat: %v", statErr)
		} else {
			t.Logf("TLS publication root mode: %s", info.Mode())
		}
		t.Fatalf("real-backend Chromium: %v", err)
	}
	// Release the daemon's lifetime store lock before independent readback.
	must(daemon.Process.Signal(os.Interrupt))
	must(daemon.Wait())
	// Independent persisted-state readback, not just browser success text.
	st, err := store.Open(cfg.DBPath)
	must(err)
	defer st.Close()
	sites, err := st.ListSites(ctx)
	must(err)
	if len(sites) != 0 {
		t.Fatal("deleted site remains in SQLite")
	}
	jobs, err := st.ListJobs(ctx, store.JobFilter{Limit: 100})
	must(err)
	counts := map[string]int{}
	var siteID string
	for _, job := range jobs {
		if job.State != "done" || job.Code != "" {
			t.Fatalf("job %s ended %s/%s", job.Verb, job.State, job.Code)
		}
		counts[job.Verb]++
		if job.Verb == "ssl.upload" {
			if string(job.Args) != "{}" || job.Step != "tls_committed" {
				t.Fatal("upload did not commit and clear sealed input")
			}
			siteID = strings.TrimPrefix(job.ObjectKey, "site:")
			var result struct {
				FQDNs    []string `json:"fqdns"`
				NotAfter int64    `json:"not_after"`
			}
			must(json.Unmarshal(job.Result, &result))
			if len(result.FQDNs) != 2 || result.FQDNs[0] != "alias.test" || result.FQDNs[1] != "example.test" || result.NotAfter != certTemplate.NotAfter.Truncate(time.Second).UnixMilli() {
				t.Fatal("upload result differs from persisted domains/certificate")
			}
		}
	}
	for verb, count := range map[string]int{"site.create": 1, "site.update": 4, "domain.add": 1, "redirect.add": 1, "ssl.upload": 1, "site.delete": 1} {
		if counts[verb] != count {
			t.Fatalf("%s jobs=%d, want %d", verb, counts[verb], count)
		}
	}
	domains, err := st.ListDomains(ctx, siteID)
	must(err)
	if siteID == "" || len(domains) != 0 {
		t.Fatal("deleted domains remain")
	}
	for _, domain := range []string{"example.test", "alias.test"} {
		if _, err := os.Lstat(filepath.Join(cfg.TLSDir, domain)); !os.IsNotExist(err) {
			t.Fatal("deleted TLS directory remains")
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.SitesRoot, "browserdemo", "public")); err != nil {
		t.Fatal("non-purge deletion removed site files")
	}
	audit, err := os.ReadFile(filepath.Join(cfg.LogDir, "audit.log"))
	must(err)
	for _, secret := range []string{"disposable browser password", "BEGIN PRIVATE KEY", "otpauth://", "nps_"} {
		if strings.Contains(string(audit), secret) {
			t.Fatal("audit secret leak")
		}
	}
	t.Log(fmt.Sprintf("Verified actual SQLite completed jobs=%d, deletion and secret-free durable audit", len(jobs)))
}
