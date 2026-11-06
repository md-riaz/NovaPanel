package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSiteDeleteAttachedDatabaseThroughQueue(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store, a.Audit = st, log
	a.Cfg.SitesRoot = filepath.Join(root, "sites")
	sid, _ := id.New()
	domain, _ := id.New()
	dbid, _ := id.New()
	uid, _ := id.New()
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: filepath.Join(a.Cfg.SitesRoot, "demo", "public")}, store.Domain{ID: domain, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ReserveDatabase(ctx, store.Database{ID: dbid, SiteID: &sid, Name: "attached_db", Engine: "mariadb"}, store.DatabaseUser{ID: uid, DatabaseID: dbid, Username: "attached_user"}); err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	call := func(verb, args string) rpc.Reply {
		return server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: json.RawMessage(args)})
	}
	if r := call("site.delete", `{"site_id":"../bad"}`); r.Code != "invalid_argument" {
		t.Fatalf("invalid input: %+v", r)
	}
	r := call("site.delete", `{"site_id":"`+sid+`"}`)
	if !r.OK {
		t.Fatalf("enqueue: %+v", r)
	}
	jobid := r.Data.(jobs.EnqueueResult).Job.ID
	a.Jobs.SetRunner(server.ExecuteJob)
	runctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(runctx) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		j, e := st.GetJob(ctx, jobid)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job: %+v", j)
		}
		time.Sleep(time.Millisecond * 5)
	}
	r = call("job.status", `{"id":"`+jobid+`"}`)
	b, _ := json.Marshal(r.Data)
	if !r.OK || !strings.Contains(string(b), `"code":"precondition_failed"`) || !strings.Contains(string(b), `"attached_database_ids":["`+dbid+`"]`) {
		t.Fatalf("status: %s %+v", b, r)
	}
	if _, err = st.SiteByID(ctx, sid); err != nil {
		t.Fatal(err)
	}
}
