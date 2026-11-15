package verbs_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

// A conflict appearing after admission must not leave a live create blocking F20.9.
func TestDatabaseCreateQueueSQLitePublicationConflict(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	log, err := audit.Open(s, filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	seal, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingDatabaseRunner{databaseRunner: &databaseRunner{}}
	a := agent.New()
	a.Store, a.Audit, a.Exec = s, log, runner
	a.Jobs = jobs.NewQueue(s, seal, log)
	server := rpc.NewServer(a)
	caller := rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "owner", MFAat: time.Now().UnixMilli()})
	reply := server.Dispatch(caller, rpc.Request{Verb: "database.create", Args: json.RawMessage(`{"name":"testdb"}`)})
	if !reply.OK {
		t.Fatalf("admission: %+v", reply)
	}
	en := reply.Data.(jobs.EnqueueResult)
	// The competing row is SQLite-only: MariaDB remains available for this job's DDL.
	d := store.Database{ID: "other-db", Name: "testdb", Engine: "mariadb"}
	u := store.DatabaseUser{ID: "other-user", DatabaseID: d.ID, Username: "other_user"}
	if err = s.ReserveDatabase(ctx, d, u); err != nil {
		t.Fatal(err)
	}
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	a.Jobs.SetRunner(server.ExecuteJob)
	go func() { done <- a.Jobs.Run(worker) }()
	defer func() { cancel(); <-done }()
	deadline := time.After(3 * time.Second)
	for {
		j, err := s.GetJob(ctx, en.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == "failed" {
			if j.Code != "conflict" {
				t.Fatalf("failure code: %q", j.Code)
			}
			break
		}
		if j.State == "done" {
			t.Fatal("conflicting create succeeded")
		}
		select {
		case <-deadline:
			t.Fatalf("conflicting create remained live: state=%s step=%s", j.State, j.Step)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, _, err = s.DatabaseWithUsers(ctx, d.ID); err != nil {
		t.Fatalf("competing metadata lost: %v", err)
	}
	var intent struct {
		DBID     string `json:"db_id"`
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	if err = json.Unmarshal(en.Job.Args, &intent); err != nil {
		t.Fatal(err)
	}
	if got := runner.objects["schema:testdb"]; got != "deleting\t1\t"+intent.DBID {
		t.Fatalf("schema tombstone: %q", got)
	}
	if got := runner.objects["user:"+intent.Username+"@127.0.0.1"]; got != "deleting\t1\t"+intent.UserID {
		t.Fatalf("user tombstone: %q", got)
	}
	statements := strings.Join(runner.statements, "\n")
	if !strings.Contains(statements, "DROP DATABASE IF EXISTS `testdb`") || !strings.Contains(statements, "DROP USER IF EXISTS '"+intent.Username+"'@'127.0.0.1'") {
		t.Fatal("owned objects were not dropped")
	}
}
