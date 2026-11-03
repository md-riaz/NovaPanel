package verbs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	php "novapanel/internal/render/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func TestPHPSetProductionDispatch(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store = st
	a.Audit = log
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	if err := a.Jobs.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	const sid = "01K8WMXZF57CGNZKX1FZ38DZMA"
	const staticID = "01K8WMXZF57CGNZKX1FZ38DZMC"
	version := "8.3"
	for _, x := range []struct {
		id, domain, name, typ string
		version               *string
	}{{sid, "01K8WMXZF57CGNZKX1FZ38DZMB", "demo", "php", &version}, {staticID, "01K8WMXZF57CGNZKX1FZ38DZMD", "static", "static", nil}} {
		_, err = st.CreateSite(ctx, store.Site{ID: x.id, Name: x.name, Type: x.typ, Docroot: filepath.Join(t.TempDir(), x.name), PHPVersion: x.version}, store.Domain{ID: x.domain, FQDN: x.name + ".example.test"})
		if err != nil {
			t.Fatal(err)
		}
		if err = st.SetSiteStatus(ctx, x.id, "active"); err != nil {
			t.Fatal(err)
		}
	}
	upload := 96
	if _, err = st.MergePHPLimits(ctx, sid, php.LimitsPatch{UploadMB: &upload}); err != nil {
		t.Fatal(err)
	}
	v, ok := rpc.Registry["php.set"]
	if !ok || v.MinRole != rpc.Admin || v.Scope != "sites:write" || !v.Slow || v.ReadOnly || v.Effectful || v.MFA {
		t.Fatalf("registry: %+v", v)
	}
	request := func(c rpc.Caller, body string) rpc.Reply {
		return s.Dispatch(rpc.WithCaller(ctx, c), rpc.Request{Verb: "php.set", Args: json.RawMessage(body)})
	}
	admin := rpc.Caller{Kind: "user", Role: "admin", Session: "admin-session", UserID: "admin-user"}
	valid := fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"memory_mb":512}}`, sid)
	if r := request(admin, valid); r.OK || r.Code != "unsupported" {
		t.Fatalf("missing runtime dependencies must refuse admission: %+v", r)
	}
	if rows, e := st.ListJobs(ctx, store.JobFilter{Limit: 100}); e != nil || len(rows) != 0 {
		t.Fatalf("refusal created jobs: %v %v", rows, e)
	}
	a.Nginx = &nginx.Applier{Runner: phpScratchRunner{}, Probe: func(context.Context, nginx.Desired) error { return nil }}
	a.PHP = &agent.PHPDependencies{Pools: &php.Applier{Runner: phpScratchRunner{}, Probe: func(context.Context, php.Desired) error { return nil }}, RecoveryRoot: t.TempDir(), ResolveIdentity: func(context.Context, store.Site) (uint32, uint32, error) { return 1001, 1001, nil }}
	for _, tc := range []struct {
		name   string
		remove func() func()
	}{
		{"nginx", func() func() { old := a.Nginx; a.Nginx = nil; return func() { a.Nginx = old } }},
		{"nginx runner", func() func() { old := a.Nginx.Runner; a.Nginx.Runner = nil; return func() { a.Nginx.Runner = old } }},
		{"nginx probe", func() func() { old := a.Nginx.Probe; a.Nginx.Probe = nil; return func() { a.Nginx.Probe = old } }},
		{"php", func() func() { old := a.PHP; a.PHP = nil; return func() { a.PHP = old } }},
		{"pools", func() func() { old := a.PHP.Pools; a.PHP.Pools = nil; return func() { a.PHP.Pools = old } }},
		{"pool runner", func() func() {
			old := a.PHP.Pools.Runner
			a.PHP.Pools.Runner = nil
			return func() { a.PHP.Pools.Runner = old }
		}},
		{"pool probe", func() func() {
			old := a.PHP.Pools.Probe
			a.PHP.Pools.Probe = nil
			return func() { a.PHP.Pools.Probe = old }
		}},
		{"identity", func() func() {
			old := a.PHP.ResolveIdentity
			a.PHP.ResolveIdentity = nil
			return func() { a.PHP.ResolveIdentity = old }
		}},
		{"recovery root", func() func() {
			old := a.PHP.RecoveryRoot
			a.PHP.RecoveryRoot = ""
			return func() { a.PHP.RecoveryRoot = old }
		}},
	} {
		t.Run("missing "+tc.name, func(t *testing.T) {
			restore := tc.remove()
			defer restore()
			if r := request(admin, valid); r.OK || r.Code != "unsupported" {
				t.Fatalf("refusal: %+v", r)
			}
			if rows, e := st.ListJobs(ctx, store.JobFilter{Limit: 100}); e != nil || len(rows) != 0 {
				t.Fatalf("unexpected jobs: %v %v", rows, e)
			}
		})
	}
	for _, tc := range []struct {
		name       string
		caller     rpc.Caller
		body, code string
	}{
		{"role", rpc.Caller{Role: "read_only", Session: "reader"}, valid, "forbidden"},
		{"scope", rpc.Caller{Kind: "token", Session: "token", Scopes: []string{"sites:read"}}, valid, "forbidden"},
		{"version", admin, fmt.Sprintf(`{"site_id":%q,"version":"9.9"}`, sid), ""},
		{"static", admin, fmt.Sprintf(`{"site_id":%q,"version":"8.3"}`, staticID), "precondition_failed"},
		{"duplicate", admin, fmt.Sprintf(`{"site_id":%q,"version":"8.3","version":"8.3"}`, sid), ""},
		{"nested duplicate", admin, fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"memory_mb":512,"memory_mb":512}}`, sid), "bad_request"},
		{"nested null", admin, fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"memory_mb":null}}`, sid), "bad_request"},
		{"nested unknown", admin, fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"other":1}}`, sid), "bad_request"},
		{"trailing", admin, fmt.Sprintf(`{"site_id":%q,"version":"8.3"} {}`, sid), "bad_request"},
		{"missing", admin, fmt.Sprintf(`{"site_id":%q}`, sid), "bad_request"},
		{"bad id", admin, `{"site_id":"invalid","version":"8.3"}`, "bad_request"},
		{"null", admin, fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":null}`, sid), ""},
		{"unknown", admin, fmt.Sprintf(`{"site_id":%q,"version":"8.3","hardened":true}`, sid), ""},
		{"bounds", admin, fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"memory_mb":1}}`, sid), "invalid_argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request(tc.caller, tc.body)
			if r.OK || tc.code != "" && r.Code != tc.code {
				t.Fatalf("refusal: %+v", r)
			}
			rows, e := st.ListJobs(ctx, store.JobFilter{Limit: 100})
			if e != nil || len(rows) != 0 {
				t.Fatalf("unexpected jobs %v %v", rows, e)
			}
		})
	}
	deps := a.PHP
	a.PHP = nil
	if err = s.ExecuteJob(ctx, &jobs.Job{Verb: "php.set", CreatedBy: "admin-session", Role: "admin", ObjectKey: "site:" + sid, Args: []byte(valid)}); err == nil || err.Error() != "PHP execution unavailable" {
		t.Fatalf("unfinished executor must fail closed: %v", err)
	}
	a.PHP = deps
	for _, raw := range []string{
		fmt.Sprintf(`{"site_id":%q,"version":"8.3","version":"8.3"}`, sid),
		fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"upload_mb":1,"upload_mb":2}}`, sid),
		fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":null}`, sid),
		fmt.Sprintf(`{"site_id":%q,"version":"8.3","extra":1}`, sid),
		valid + ` {}`,
	} {
		err = s.ExecuteJob(ctx, &jobs.Job{Verb: "php.set", CreatedBy: "admin-session", Role: "admin", ObjectKey: "site:" + sid, Args: []byte(raw)})
		var coded *rpc.CodedError
		if !errors.As(err, &coded) || coded.Code != "bad_request" {
			t.Fatalf("stored raw %s: %v", raw, err)
		}
	}
	r := request(admin, valid)
	if !r.OK {
		t.Fatalf("dispatch: %+v", r)
	}
	enqueued, ok := r.Data.(jobs.EnqueueResult)
	if !ok || enqueued.ClaimToken != "" {
		t.Fatalf("envelope: %+v", r.Data)
	}
	j, err := st.GetJob(ctx, enqueued.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Verb != "php.set" || j.ObjectKey != "site:"+sid || j.State != "queued" || j.CreatedBy != "admin-session" || j.Role != "admin" {
		t.Fatalf("job: %+v", j)
	}
	var args map[string]any
	if err = json.Unmarshal(j.Args, &args); err != nil {
		t.Fatal(err)
	}
	lim, ok := args["limits"].(map[string]any)
	if !ok || args["site_id"] != sid || args["version"] != "8.3" || lim["memory_mb"] != float64(512) {
		t.Fatalf("args: %s", j.Args)
	}
	op, err := st.LoadOperation(ctx, j.ID)
	if err != nil || op.Identity.PHP == nil || op.Identity.PHP.DesiredLimits.MemoryMB != 512 || op.Identity.PHP.DesiredLimits.UploadMB != upload {
		t.Fatalf("operation: %+v %v", op, err)
	}
	status := s.Dispatch(rpc.WithCaller(ctx, admin), rpc.Request{Verb: "job.status", Args: json.RawMessage(fmt.Sprintf(`{"id":%q}`, j.ID))})
	if !status.OK {
		t.Fatalf("status: %+v", status)
	}
	raw, _ := json.Marshal(status.Data)
	var obj map[string]any
	if err = json.Unmarshal(raw, &obj); err != nil || obj["id"] != j.ID || obj["state"] != "queued" {
		t.Fatalf("status %s %v", raw, err)
	}
}
