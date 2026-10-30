package verbs

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/rpc"
	"reflect"
	"testing"
)

type serviceRunner struct {
	calls          [][]string
	state, enabled string
	code           int
	err            error
}

func (r *serviceRunner) Run(_ context.Context, p string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, append([]string{p}, args...))
	if args[0] == "is-enabled" {
		return []byte(r.enabled), nil, r.code, r.err
	}
	return []byte(r.state), nil, r.code, r.err
}
func TestServiceStatus(t *testing.T) {
	r := &serviceRunner{state: "active\n", enabled: "enabled\n"}
	a := agent.New()
	a.Exec = r
	s := rpc.NewServer(a)
	reply := s.Dispatch(rpc.WithCaller(context.Background(), rpc.Caller{Role: "read_only"}), rpc.Request{Verb: "service.status", Args: json.RawMessage(`{"unit":"nginx.service"}`)})
	if !reply.OK {
		t.Fatalf("%+v", reply)
	}
	b, _ := json.Marshal(reply.Data)
	if string(b) != `{"unit":"nginx.service","active":true,"enabled":true,"state":"active"}` {
		t.Fatal(string(b))
	}
	if !reflect.DeepEqual(r.calls, [][]string{{"/usr/bin/systemctl", "is-active", "nginx.service"}, {"/usr/bin/systemctl", "is-enabled", "nginx.service"}}) {
		t.Fatal(r.calls)
	}
	for _, u := range []string{"sshd.service", "nginx", "../nginx.service", "--help", "nginx.service;id"} {
		reply = s.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: "service.status", Args: json.RawMessage(`{"unit":"` + u + `"}`)})
		if reply.Code != "invalid_argument" {
			t.Fatalf("%s %+v", u, reply)
		}
	}
}
