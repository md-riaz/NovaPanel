package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"testing"
)

func TestDatabaseListEmpty(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := agent.New()
	a.Store = s
	a.Exec = &databaseRunner{}
	r := rpc.NewServer(a).Dispatch(rpc.WithCaller(context.Background(), rpc.Caller{Role: "read_only"}), rpc.Request{Verb: "database.list", Args: json.RawMessage(`{}`)})
	if !r.OK {
		t.Fatalf("database.list: %+v", r)
	}
	b, err := json.Marshal(r.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"databases":[]}` {
		t.Fatalf("unexpected response %s", b)
	}
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO databases(id,name,created) VALUES('db-a','d_example',1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r = rpc.NewServer(a).Dispatch(rpc.WithCaller(context.Background(), rpc.Caller{Role: "read_only"}), rpc.Request{Verb: "database.list", Args: json.RawMessage(`{}`)})
	b, err = json.Marshal(r.Data)
	if err != nil || !r.OK || string(b) != `{"databases":[{"id":"db-a","name":"d_example","site_id":null,"engine":"mariadb"}]}` {
		t.Fatalf("projection: %+v %s %v", r, b, err)
	}
	denied := rpc.NewServer(a).Dispatch(rpc.WithCaller(context.Background(), rpc.Caller{Kind: "token", Session: "limited", Scopes: []string{"system:read"}}), rpc.Request{Verb: "database.list", Args: json.RawMessage(`{}`)})
	if denied.Code != "forbidden" {
		t.Fatalf("scope: %+v", denied)
	}
}
