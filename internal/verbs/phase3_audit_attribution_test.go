package verbs_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func expectedPrincipal(c rpc.Caller) map[string]any {
	if c.Kind == "user" {
		return map[string]any{"kind": "user", "user_id": c.UserID, "session_id": c.Session}
	}
	if c.Role == "owner" && c.Peer == 0 {
		return map[string]any{"kind": "local-root"}
	}
	return map[string]any{"kind": "unresolved"}
}

func TestPhase3AuditObjectAttribution(t *testing.T) {
	const sid = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const did = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	const rid = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	cases := []struct {
		verb       string
		args       map[string]any
		requestKey string
	}{
		{"site.create", map[string]any{"name": "demo", "type": "static", "primary_domain": "demo.test"}, "site:demo"},
		{"site.update", map[string]any{"site_id": sid, "maintenance": true}, "site:" + sid},
		{"site.delete", map[string]any{"site_id": sid}, "site:" + sid},
		{"domain.add", map[string]any{"site_id": sid, "fqdn": "alias.test"}, "site:" + sid},
		{"domain.remove", map[string]any{"domain_id": did}, "domain:" + did},
		{"redirect.add", map[string]any{"site_id": sid, "source": "/old", "target": "https://demo.test/new", "code": 301}, "site:" + sid},
		{"redirect.remove", map[string]any{"redirect_id": rid}, "redirect:" + rid},
		{"ssl.issue", map[string]any{"site_id": sid}, "site:" + sid},
		{"ssl.renew", map[string]any{"site_id": sid}, "site:" + sid},
		{"ssl.upload", map[string]any{"site_id": sid, "cert_pem": "CERTIFICATE sentinel", "key_pem": "PRIVATE KEY sentinel"}, "site:" + sid},
		{"ssl.auto_set", map[string]any{"site_id": sid, "enabled": false}, "site:" + sid},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			h := newPhase2AuditHarness(t)
			h.forbidden = []string{"CERTIFICATE sentinel", "PRIVATE KEY sentinel"}
			h.s.ConfigureJobs()
			ctx := context.Background()
			if _, err := h.s.Agent.Store.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "demo.test"}); err != nil {
				t.Fatal(err)
			}
			if err := h.s.Agent.Store.SetSiteStatus(ctx, sid, "active"); err != nil {
				t.Fatal(err)
			}
			if _, err := h.s.Agent.Store.AddRedirect(ctx, store.Redirect{ID: rid, SiteID: sid, Source: "/old", Target: "https://demo.test/new", Code: 301}); err != nil {
				t.Fatal(err)
			}
			check := func(caller rpc.Caller, code, key string, stages ...string) {
				t.Helper()
				before := len(h.records())
				r := h.s.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: tc.verb, Actor: "untrusted-audit-label", Args: phase2JSON(t, tc.args)})
				if r.Code != code || r.OK != (code == "") {
					t.Fatalf("reply=%+v want %q", r, code)
				}
				records := h.records()[before:]
				// Queue records have their own job_id; check dispatch records here.
				var dispatch []map[string]any
				for _, ev := range records {
					if _, ok := ev["detail"].(map[string]any)["job_id"]; !ok {
						dispatch = append(dispatch, ev)
					}
				}
				if len(dispatch) != len(stages) {
					t.Fatalf("records=%+v", dispatch)
				}
				rows, err := h.s.Agent.Store.ListEvents(ctx, 1000, "")
				if err != nil {
					t.Fatal(err)
				}
				for i, ev := range dispatch {
					outcome, wantCode := "ok", ""
					if i == len(dispatch)-1 {
						wantCode = code
						if code != "" {
							outcome = "refused"
						}
						if code == "agent_error" {
							outcome = "error"
						}
					}
					if ev["actor"] != "untrusted-audit-label" || ev["role"] != caller.Role || ev["verb"] != tc.verb || ev["object_key"] != key || ev["code"] != wantCode || ev["outcome"] != outcome || !reflect.DeepEqual(ev["detail"], map[string]any{"stage": stages[i], "principal": expectedPrincipal(caller)}) {
						t.Fatalf("wrong attribution: %+v", ev)
					}
					found := false
					for _, row := range rows {
						if row.ID == ev["id"] {
							found = true
							var detail map[string]any
							if err := json.Unmarshal(row.Detail, &detail); err != nil {
								t.Fatal(err)
							}
							if row.Actor != ev["actor"] || row.Role != ev["role"] || row.Verb != ev["verb"] || row.ObjectKey != key || row.Code != wantCode || row.Outcome != outcome || float64(row.TS) != ev["ts"] || !reflect.DeepEqual(detail, ev["detail"]) {
								t.Fatal("mirror differs")
							}
						}
					}
					if !found {
						t.Fatal("missing mirror")
					}
				}
			}
			check(rpc.Caller{Peer: 1001}, "unauthenticated", tc.requestKey, "authz")
			check(rpc.Caller{Peer: 1001, Role: "read_only", Kind: "user", UserID: "trusted-user", Session: "trusted-session"}, "forbidden", tc.requestKey, "authz")
			root := rpc.RootCaller(0)
			root.MFAat = h.clock.now
			// Inject only the public handler failure, retaining registry authorization and resolution.
			registry := map[string]rpc.Verb{}
			for k, v := range rpc.Registry {
				registry[k] = v
			}
			h.s.Registry = registry
			v := registry[tc.verb]
			v.Run = func(context.Context, *agent.Agent, any) (any, error) { return nil, rpc.Errf("agent_error", "injected") }
			registry[tc.verb] = v
			resolved := tc.requestKey
			if tc.verb == "domain.remove" || tc.verb == "redirect.remove" {
				resolved = "site:" + sid
			}
			check(root, "agent_error", resolved, "authz", "exec")
			v.Run = func(context.Context, *agent.Agent, any) (any, error) { return struct{}{}, nil }
			registry[tc.verb] = v
			check(root, "", resolved, "authz", "exec")
			if tc.verb == "domain.remove" || tc.verb == "redirect.remove" {
				// Authorization must refuse before invoking the persisted-parent resolver.
				original := registry[tc.verb]
				guarded := original
				guarded.ResolveObjectKey = func(context.Context, *agent.Agent, any) (string, error) {
					t.Fatal("lookup before authorization")
					return "", nil
				}
				registry[tc.verb] = guarded
				check(rpc.Caller{Peer: 1001}, "unauthenticated", tc.requestKey, "authz")
				registry[tc.verb] = original
				if tc.verb == "domain.remove" {
					tc.args["domain_id"] = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
					tc.requestKey = "domain:01ARZ3NDEKTSV4RRFFQ69G5FAY"
				} else {
					tc.args["redirect_id"] = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
					tc.requestKey = "redirect:01ARZ3NDEKTSV4RRFFQ69G5FAY"
				}
				check(root, "not_found", tc.requestKey, "resolve")
			}
			tc.args = map[string]any{"site_id": "bad;site", "domain_id": "bad;child", "redirect_id": "bad;child", "name": "bad;name"}
			check(rpc.Caller{Peer: 1001}, "unauthenticated", "", "authz")
		})
	}
}
