package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"testing"
)

func TestDatabaseListProjection(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := agent.New()
	a.Store = st
	s := rpc.NewServer(a)
	call := func(c rpc.Caller) rpc.Reply {
		return s.Dispatch(rpc.WithCaller(ctx, c), rpc.Request{Verb: "database.list", Args: json.RawMessage(`{}`)})
	}
	read := rpc.Caller{Role: "read_only"}
	r := call(read)
	if !r.OK {
		t.Fatalf("empty list: %+v", r)
	}
	b, err := json.Marshal(r.Data)
	if err != nil || string(b) != `{"databases":[]}` {
		t.Fatalf("empty: %s %v", b, err)
	}
	db, _ := id.New()
	user, _ := id.New()
	if err = st.CreateDatabasePrimary(ctx, store.DatabasePrimary{ID: db, UserID: user}); err != nil {
		t.Fatal(err)
	}
	r = call(read)
	if !r.OK {
		t.Fatalf("list: %+v", r)
	}
	b, err = json.Marshal(r.Data)
	if err != nil {
		t.Fatal(err)
	}
	var obj struct {
		Databases []struct {
			ID      string  `json:"id"`
			Name    string  `json:"name"`
			SiteID  *string `json:"site_id"`
			Engine  string  `json:"engine"`
			Primary string  `json:"primary_user_id"`
		} `json:"databases"`
	}
	if err = json.Unmarshal(b, &obj); err != nil || len(obj.Databases) != 1 {
		t.Fatalf("projection %s: %v", b, err)
	}
	row := obj.Databases[0]
	if row.ID != db || row.Primary != user || row.SiteID != nil || row.Engine != "mariadb" || row.Name == "" {
		t.Fatalf("projection: %s", b)
	}
	var data map[string]any
	if err = json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	fields := data["databases"].([]any)[0].(map[string]any)
	if len(fields) != 5 {
		t.Fatalf("unexpected fields: %s", b)
	}
	v := rpc.Registry["database.list"]
	if !v.ReadOnly || v.Slow || v.MinRole != rpc.ReadOnly || v.Scope != "databases:read" {
		t.Fatalf("attributes: %+v", v)
	}
	if got := call(rpc.Caller{Kind: "token", Session: "limited", Scopes: []string{"system:read"}}); got.Code != "forbidden" {
		t.Fatalf("scope: %+v", got)
	}
}
