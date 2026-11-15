package verbs_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

func TestSSLProductionWorker(t *testing.T) {
	for _, tc := range []struct {
		name     string
		caller   rpc.Caller
		failure  bool
		admitted bool
	}{
		{"root", rpc.RootCaller(0), false, true},
		{"stepped-up user", rpc.Caller{Kind: "user", Role: "admin", Session: "session", UserID: "user", MFAat: time.Now().UnixMilli()}, false, true},
		{"automation token", rpc.Caller{Kind: "token", Session: "token", Scopes: []string{"sites:write"}, Automation: true}, false, true},
		{"ordinary token", rpc.Caller{Kind: "token", Session: "token", Scopes: []string{"sites:write"}}, false, false},
		{"token owner label", rpc.Caller{Kind: "token", Role: "owner", Session: "token", Scopes: []string{"sites:write"}}, false, false},
		{"stale user", rpc.Caller{Kind: "user", Role: "owner", Session: "session", MFAat: 1}, false, false},
		{"probe failure", rpc.RootCaller(0), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			st, e := store.Open(filepath.Join(root, "panel.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			log, e := audit.Open(st, filepath.Join(root, "audit.log"))
			if e != nil {
				t.Fatal(e)
			}
			defer log.Close()
			a := agent.New()
			a.Store = st
			a.Audit = log
			a.Secret, e = secret.New(make([]byte, 32))
			if e != nil {
				t.Fatal(e)
			}
			a.Cfg.TLSDir = filepath.Join(root, "tls")
			if e = os.Mkdir(a.Cfg.TLSDir, 0700); e != nil {
				t.Fatal(e)
			}
			a.Nginx.Root = filepath.Join(root, "nginx")
			for _, dir := range []string{"sites-available", "sites-enabled"} {
				if e = os.MkdirAll(filepath.Join(a.Nginx.Root, dir), 0755); e != nil {
					t.Fatal(e)
				}
			}
			a.Nginx.Runner = sslRunner{}
			a.Nginx.Probe = func(context.Context, nginx.Desired) error {
				if tc.failure {
					return errors.New("injected")
				}
				return nil
			}
			sid, _ := id.New()
			did, _ := id.New()
			if _, e = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "demo.test"}); e != nil {
				t.Fatal(e)
			}
			if e = st.SetSiteStatus(ctx, sid, "active"); e != nil {
				t.Fatal(e)
			}
			s := rpc.NewServer(a)
			s.ConfigureJobs()
			cp, kp := uploadPEM(t, 1)
			raw, _ := json.Marshal(map[string]string{"site_id": sid, "cert_pem": cp, "key_pem": kp})
			reply := s.Dispatch(rpc.WithCaller(ctx, tc.caller), rpc.Request{Verb: "ssl.upload", Args: raw})
			if !tc.admitted {
				if reply.OK || reply.Code != "forbidden" {
					t.Fatal(reply)
				}
				return
			}
			if !reply.OK {
				t.Fatal(reply)
			}
			queued := reply.Data.(jobs.EnqueueResult).Job
			if !queued.Automation {
				t.Fatal("MFA admission not snapshotted")
			}
			if tc.caller.Kind == "token" {
				if queued.Role != "token" {
					t.Fatal("token kind lost")
				}
			} else if len(queued.Scopes) != 0 {
				t.Fatal("user/root scopes persisted")
			}
			for _, mutate := range []func(*jobs.Job){
				func(j *jobs.Job) { j.Automation = false },
				func(j *jobs.Job) { j.ObjectKey = "site:other" },
				func(j *jobs.Job) { j.ID = "other" },
				func(j *jobs.Job) { j.Effectful = true },
				func(j *jobs.Job) { j.Role = "token"; j.Scopes = nil },
				func(j *jobs.Job) { j.Role = "read_only" },
				func(j *jobs.Job) { j.Role = "owner"; j.Scopes = []string{"sites:write"}; j.CreatedBy = "token" },
			} {
				bad := queued
				mutate(&bad)
				if s.ExecuteJob(ctx, &bad) == nil {
					t.Fatal("tampered authority/binding accepted")
				}
			}
			workerCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(workerCtx) }()
			defer func() {
				cancel()
				if e := <-done; e != nil {
					t.Error(e)
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			var finished jobs.Job
			for {
				finished, e = st.GetJob(ctx, queued.ID)
				if e != nil {
					t.Fatal(e)
				}
				if finished.State == "done" || finished.State == "failed" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker timeout")
				}
				time.Sleep(time.Millisecond)
			}
			want := "done"
			if tc.failure {
				want = "failed"
			}
			if finished.State != want {
				t.Fatalf("worker: %+v", finished)
			}
			if string(finished.Args) != "{}" {
				t.Fatal("terminal input retained")
			}
			if !tc.failure {
				var result struct {
					FQDNs    []string `json:"fqdns"`
					NotAfter int64    `json:"not_after"`
				}
				if json.Unmarshal(finished.Result, &result) != nil || len(result.FQDNs) != 1 || result.FQDNs[0] != "demo.test" || result.NotAfter <= a.Now() {
					t.Fatalf("result: %s", finished.Result)
				}
				// Terminal input is gone; committed replay must not open it or apply again.
				a.Nginx.Probe = func(context.Context, nginx.Desired) error {
					t.Error("committed replay probed")
					return errors.New("unexpected")
				}
				replay := finished
				replay.Result = nil
				if e = s.ExecuteJob(ctx, &replay); e != nil || string(replay.Result) != string(finished.Result) {
					t.Fatalf("replay: %s %v", replay.Result, e)
				}
			}
			data, e := os.ReadFile(filepath.Join(root, "audit.log"))
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(data), "PRIVATE KEY") || strings.Contains(string(data), "CERTIFICATE") {
				t.Fatal("secret audit")
			}
		})
	}
}
