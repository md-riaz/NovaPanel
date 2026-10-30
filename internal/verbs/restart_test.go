package verbs

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"reflect"
	"testing"
)

func TestServiceRestartExec(t *testing.T) {
	v, ok := rpc.Registry["service.restart"]
	if !ok {
		t.Fatal("restart not registered")
	}
	if v.MinRole != rpc.Admin || !v.Slow || !v.Effectful || v.Scope != "system:write" || v.ObjectKey(&unitArgs{Unit: "nginx.service"}) != "service:nginx.service" {
		t.Fatalf("wrong policy %+v", v)
	}
	a := agent.New()
	runner := &serviceRunner{}
	a.Exec = runner
	j := jobs.Job{Args: json.RawMessage(`{"unit":"nginx.service"}`)}
	before := a.Now()
	if e := v.Exec(context.Background(), a, &j); e != nil {
		t.Fatal(e)
	}
	var result struct {
		Unit        string `json:"unit"`
		RestartedAt int64  `json:"restarted_at"`
	}
	if e := json.Unmarshal(j.Result, &result); e != nil {
		t.Fatal(e)
	}
	if result.Unit != "nginx.service" || result.RestartedAt < before || result.RestartedAt > a.Now() {
		t.Fatalf("result %s", j.Result)
	}
	if !reflect.DeepEqual(runner.calls, [][]string{{"/usr/bin/systemctl", "restart", "nginx.service"}}) {
		t.Fatal(runner.calls)
	}
	j.Args = json.RawMessage(`{"unit":"sshd;bad"}`)
	if e := v.Exec(context.Background(), a, &j); e == nil {
		t.Fatal("invalid unit accepted")
	}
	if len(runner.calls) != 1 {
		t.Fatal("invalid unit executed")
	}
	runner.code = 1
	j.Args = json.RawMessage(`{"unit":"nginx.service"}`)
	if e := v.Exec(context.Background(), a, &j); e == nil {
		t.Fatal("failed restart accepted")
	}
}
