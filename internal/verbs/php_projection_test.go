package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	php "novapanel/internal/render/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"testing"
)

func TestSiteGetPersistedPHPLimits(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := agent.New()
	a.Store = st
	s := rpc.NewServer(a)
	for _, kind := range []string{"static", "php", "wordpress"} {
		t.Run(kind, func(t *testing.T) {
			sid, _ := id.New()
			did, _ := id.New()
			site := store.Site{ID: sid, Name: "site-" + kind, Type: kind, Docroot: "/srv/sites/site-" + kind + "/public"}
			if kind != "static" {
				version := "8.3"
				site.PHPVersion = &version
			}
			if _, e := st.CreateSite(ctx, site, store.Domain{ID: did, FQDN: kind + ".example.test"}); e != nil {
				t.Fatal(e)
			}
			read := func() map[string]any {
				t.Helper()
				r := s.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "read_only"}), rpc.Request{Verb: "site.get", Args: json.RawMessage(`{"site_id":"` + sid + `"}`)})
				return sessionData(t, r)
			}
			if kind == "static" {
				data := read()
				value, exists := data["php_limits"]
				if !exists || value != nil {
					t.Fatalf("static limits must be explicit null: %+v", data)
				}
				return
			}
			data := read()
			if value, exists := data["php_limits"]; !exists || value != nil {
				t.Fatalf("uncommitted limits must be null, not inferred defaults: %+v", data)
			}
			memory, upload, seconds := 512, 100, 120
			if _, e := st.MergePHPLimits(ctx, sid, php.LimitsPatch{MemoryMB: &memory, UploadMB: &upload, MaxExecSec: &seconds}); e != nil {
				t.Fatal(e)
			}
			data = read()
			got, _ := json.Marshal(data["php_limits"])
			if string(got) != `{"max_exec_sec":120,"memory_mb":512,"upload_mb":100}` {
				t.Fatalf("persisted limits not projected: %s", got)
			}
			upload = 80
			if _, e := st.MergePHPLimits(ctx, sid, php.LimitsPatch{UploadMB: &upload}); e != nil {
				t.Fatal(e)
			}
			got, _ = json.Marshal(read()["php_limits"])
			if string(got) != `{"max_exec_sec":120,"memory_mb":512,"upload_mb":80}` {
				t.Fatal(string(got))
			}
		})
	}
}
