package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	_ "novapanel/internal/verbs"
	"strings"
	"testing"
)

type databaseDispatch struct{ server *rpc.Server }

func (*databaseDispatch) Close() error { return nil }
func (d *databaseDispatch) Call(ctx context.Context, verb string, args any, token string) (rpc.Reply, error) {
	if verb == "session.whoami" {
		return rpc.Reply{OK: true, Data: map[string]any{"role": "read_only", "csrf_token": "expected"}}, nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return rpc.Reply{}, err
	}
	return d.server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Role: "read_only"}), rpc.Request{Verb: verb, Args: raw}), nil
}
func TestDatabaseListPageRealStoreAndDispatcher(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	user, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateDatabasePrimary(ctx, store.DatabasePrimary{ID: db, UserID: user}); err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = st
	client := &databaseDispatch{server: rpc.NewServer(a)}
	h, err := New(Options{Origin: "https://panel.example", Dial: func() (RPCClient, error) { return client, nil }})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", "/databases", ""))
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	for _, want := range []string{"d_" + strings.ToLower(db), user, "mariadb", "Unassigned"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
