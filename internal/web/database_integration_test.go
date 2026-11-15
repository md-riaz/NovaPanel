package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	_ "novapanel/internal/verbs"
	"strings"
	"testing"
)

type databaseDispatch struct{ server *rpc.Server }

type databaseReadyRunner struct{}

func (databaseReadyRunner) Run(_ context.Context, _ string, _ []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	b, _ := io.ReadAll(opts.Stdin)
	s := string(b)
	if strings.Contains(s, "FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='novapanel'") {
		return []byte("1\n"), nil, 0, nil
	}
	if strings.HasPrefix(s, "SELECT CONCAT(") {
		return []byte("1:5:5:2:2:0:0:0\n"), nil, 0, nil
	}
	return []byte("0\n"), nil, 0, nil
}

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
	a.Exec = databaseReadyRunner{}
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
	for _, want := range []string{"d_" + strings.ToLower(db), "mariadb", "Detached"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
