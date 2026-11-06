package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func TestDBUserList(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	const db = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const other = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO databases(id,name,created) VALUES (?, 'd_one', 1), (?, 'd_two', 1)`, db, other)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO db_users(id,db_id,username,owner,created) VALUES ('owner',?,'owner_name',1,1), ('secondary',?,'secondary_name',0,2), ('other',?,'other_name',0,3)`, db, db, other)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = s
	a.Exec = &databaseRunner{}
	server := rpc.NewServer(a)
	call := func(c rpc.Caller, args string) rpc.Reply {
		return server.Dispatch(rpc.WithCaller(ctx, c), rpc.Request{Verb: "dbuser.list", Args: json.RawMessage(args)})
	}
	r := call(rpc.Caller{Role: "read_only"}, `{"db_id":"`+db+`"}`)
	b, _ := json.Marshal(r.Data)
	if !r.OK || string(b) != `{"users":[{"id":"secondary","username":"secondary_name"}]}` {
		t.Fatalf("list: %+v %s", r, b)
	}
	r = call(rpc.Caller{Kind: "token", Session: "token", Scopes: []string{"system:read"}}, `{"db_id":"`+db+`"}`)
	if r.Code != "forbidden" {
		t.Fatalf("scope: %+v", r)
	}
	r = call(rpc.Caller{Role: "read_only"}, `{"db_id":"bad"}`)
	if r.Code != "invalid_argument" {
		t.Fatalf("invalid: %+v", r)
	}
	r = call(rpc.Caller{Role: "read_only"}, `{"db_id":"01ARZ3NDEKTSV4RRFFQ69G5FAX"}`)
	if r.Code != "not_found" {
		t.Fatalf("missing: %+v", r)
	}
	r = call(rpc.Caller{Kind: "token", Session: "token", Scopes: []string{"databases:read"}}, `{"db_id":"`+db+`"}`)
	if !r.OK {
		t.Fatalf("read scope: %+v", r)
	}
	r = call(rpc.Caller{Role: "read_only"}, `{"db_id":"`+other+`"}`)
	b, _ = json.Marshal(r.Data)
	if !r.OK || string(b) != `{"users":[{"id":"other","username":"other_name"}]}` {
		t.Fatalf("other: %+v %s", r, b)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { _, err := tx.Exec(`DELETE FROM db_users WHERE db_id=?`, other); return err }); err != nil {
		t.Fatal(err)
	}
	r = call(rpc.Caller{Role: "read_only"}, `{"db_id":"`+other+`"}`)
	b, _ = json.Marshal(r.Data)
	if !r.OK || string(b) != `{"users":[]}` {
		t.Fatalf("empty: %+v %s", r, b)
	}
}
