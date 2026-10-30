package verbs_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
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
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

type publicationRunner struct {
	fail    string
	calls   int
	observe func([]string)
}

func (r *publicationRunner) Run(ctx context.Context, _ string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	if r.observe != nil {
		r.observe(args)
	}
	if ctx.Err() != nil {
		return nil, nil, 1, ctx.Err()
	}
	if args[0] == r.fail {
		r.fail = ""
		return nil, nil, 1, errors.New("injected")
	}
	return nil, nil, 0, nil
}
func uploadPEM(t *testing.T, serial int64) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: []string{"demo.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	k, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k}))
}
func TestSSLPublicationFailuresAndRestart(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		for _, failure := range []string{"-t", "reload", "probe", "cancel", "crash", "checkpoint", "published", "committed"} {
			t.Run(failure+map[bool]string{false: "/first", true: "/replacement"}[replacement], func(t *testing.T) {
				ctx := context.Background()
				root := t.TempDir()
				db := filepath.Join(root, "panel.db")
				st, err := store.Open(db)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { st.Close() }()
				log, err := audit.Open(st, filepath.Join(root, "audit.log"))
				if err != nil {
					t.Fatal(err)
				}
				defer log.Close()
				a := agent.New()
				a.Store = st
				a.Audit = log
				a.Secret, _ = secret.New(make([]byte, 32))
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
				r := &publicationRunner{}
				a.Nginx.Root = ng
				a.Nginx.Runner = r
				a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
				sid, _ := id.New()
				did, _ := id.New()
				_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "demo.test"})
				if err != nil {
					t.Fatal(err)
				}
				if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
					t.Fatal(err)
				}
				server := rpc.NewServer(a)
				server.ConfigureJobs()
				enqueue := func(serial int64) (jobs.Job, string, string) {
					cp, kp := uploadPEM(t, serial)
					raw, _ := json.Marshal(map[string]string{"site_id": sid, "cert_pem": cp, "key_pem": kp})
					res := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "ssl.upload", Args: raw})
					if !res.OK {
						t.Fatal(res)
					}
					j, e := st.StartJob(ctx, res.Data.(jobs.EnqueueResult).Job.ID, a.Now())
					if e != nil {
						t.Fatal(e)
					}
					return j, cp, kp
				}
				if replacement {
					j, _, _ := enqueue(1)
					if err = server.ExecuteJob(ctx, &j); err != nil {
						t.Fatal(err)
					}
					j.State = "done"
					if err = st.UpdateJob(ctx, j); err != nil {
						t.Fatal(err)
					}
				}
				previous, _ := os.Readlink(filepath.Join(a.Cfg.TLSDir, "demo.test/current"))
				oldConfig, _ := os.ReadFile(filepath.Join(ng, "sites-available/demo.conf"))
				j, cp, kp := enqueue(2)
				execCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				switched := false
				r.fail = failure
				r.observe = func(args []string) {
					pointer, _ := os.Readlink(filepath.Join(a.Cfg.TLSDir, "demo.test/current"))
					if pointer != previous {
						switched = true
					} else if switched && execCtx.Err() != nil && args[0] == "reload" { /* detached recovery succeeded */
					}
				}
				a.Nginx.Probe = func(context.Context, nginx.Desired) error {
					switch failure {
					case "probe":
						return errors.New("probe")
					case "cancel":
						cancel()
						return execCtx.Err()
					case "crash":
						panic("restart")
					}
					return nil
				}
				if failure == "checkpoint" || failure == "published" {
					c, e := certs.Validate([]byte(cp), []byte(kp), []string{"demo.test"}, time.Now())
					if e != nil {
						t.Fatal(e)
					}
					token, e := certs.Stage(a.Cfg.TLSDir, "demo.test", c)
					if e != nil {
						t.Fatal(e)
					}
					err = a.Nginx.Transaction(ctx, func(tx *nginx.Transaction) error {
						snap, e := tx.Snapshot("demo")
						if e != nil {
							return e
						}
						raw, _ := json.Marshal(map[string]any{"version": 1, "job_id": j.ID, "site_id": sid, "nginx_root": ng, "domains": []string{"demo.test"}, "token": token, "config": snap})
						return st.BeginTLSIntent(ctx, raw)
					})
					if err != nil {
						t.Fatal(err)
					}
					if failure == "published" {
						if err = certs.Publish(token); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					panicked := false
					func() {
						defer func() {
							if recover() != nil {
								panicked = true
							}
						}()
						err = server.ExecuteJob(execCtx, &j)
					}()
					if failure == "crash" && (panicked || err == nil) {
						t.Fatal("panic did not preserve recovery input")
					}
					if failure != "crash" && failure != "committed" && err == nil {
						t.Fatal("failure ignored")
					}
				}
				intent, e := st.TLSIntent(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if strings.Contains(string(intent), "PRIVATE KEY") || strings.Contains(string(intent), "CERTIFICATE") {
					t.Fatal("secret journal")
				}
				if failure != "crash" && failure != "checkpoint" && failure != "published" && failure != "committed" {
					pointer, _ := os.Readlink(filepath.Join(a.Cfg.TLSDir, "demo.test/current"))
					if pointer != previous {
						t.Fatal("pointer not restored")
					}
					got, _ := os.ReadFile(filepath.Join(ng, "sites-available/demo.conf"))
					if string(got) != string(oldConfig) {
						t.Fatal("config not restored")
					}
					site, _ := st.SiteByID(ctx, sid)
					if site.CertSource != map[bool]string{false: "none", true: "manual"}[replacement] {
						t.Fatal("metadata changed")
					}
					if len(intent) != 0 {
						t.Fatal("rollback intent retained")
					}
				}
				if len(intent) > 0 {
					if e = a.Nginx.Apply(ctx, nginx.Desired{}); e == nil {
						t.Fatal("unresolved intent did not block apply")
					}
				}
				// Reopen durable SQLite and replay the running job, including a commit before terminal audit.
				if e = st.Close(); e != nil {
					t.Fatal(e)
				}
				st, e = store.Open(db)
				if e != nil {
					t.Fatal(e)
				}
				a.Store = st
				if e = st.RecoverJobs(ctx, a.Now()); e != nil {
					t.Fatal(e)
				}
				j, e = st.StartJob(ctx, j.ID, a.Now())
				if e != nil {
					t.Fatal(e)
				}
				before := r.calls
				r.fail = ""
				r.observe = nil
				a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
				if e = server.ExecuteJob(ctx, &j); e != nil {
					t.Fatal(e)
				}
				if failure == "committed" && r.calls != before {
					t.Fatal("committed replay reloaded")
				}
				site, e := st.SiteByID(ctx, sid)
				if e != nil || site.CertSource != "manual" {
					t.Fatalf("%+v %v", site, e)
				}
				installed, e := certs.Load(a.Cfg.TLSDir, "demo.test", []string{"demo.test"}, time.Now())
				if e != nil || installed == nil {
					t.Fatal(e)
				}
				data, readErr := os.ReadFile(filepath.Join(root, "audit.log"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if strings.Contains(string(data), "PRIVATE KEY") || strings.Contains(string(data), "CERTIFICATE") {
					t.Fatal("audit secret")
				}
				intent, e = st.TLSIntent(ctx)
				if e != nil || len(intent) > 0 {
					t.Fatalf("intent: %s %v", intent, e)
				}
				j.State = "done"
				if e = st.UpdateJob(ctx, j); e != nil {
					t.Fatal(e)
				}
				stored, e := st.GetJob(ctx, j.ID)
				if e != nil || string(stored.Args) != "{}" || !strings.Contains(string(stored.Result), `"fqdns":["demo.test"]`) || !strings.Contains(string(stored.Result), `"not_after":`) {
					t.Fatal("terminal secrets/result")
				}
			})
		}
	}
}
