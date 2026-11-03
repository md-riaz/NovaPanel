package web

import (
	"net/http/httptest"
	"novapanel/internal/rpc"
	"strings"
	"testing"
)

func TestDatabaseListPage(t *testing.T) {
	f := identityRPC()
	f.reply = func(v string) (rpc.Reply, error) {
		switch v {
		case "session.whoami":
			return rpc.Reply{OK: true, Data: map[string]any{"role": "read_only", "csrf_token": "expected"}}, nil
		case "database.list":
			return rpc.Reply{OK: true, Data: map[string]any{"databases": []any{map[string]any{"id": "DB", "name": "d_example<script>", "site_id": nil, "engine": "mariadb", "primary_user_id": "USER"}}}}, nil
		default:
			t.Fatalf("unexpected RPC %s", v)
			return rpc.Reply{}, nil
		}
	}
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("GET", "/databases", ""))
	if w.Code != 200 {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	for _, s := range []string{"Databases", "d_example&lt;script&gt;", "mariadb", "USER"} {
		if !strings.Contains(w.Body.String(), s) {
			t.Errorf("missing %q", s)
		}
	}
	if strings.Contains(w.Body.String(), "d_example<script>") {
		t.Fatal("database name not escaped")
	}
	if len(f.rpcCalls) != 2 || f.rpcCalls[1].verb != "database.list" {
		t.Fatalf("calls: %+v", f.rpcCalls)
	}
}
