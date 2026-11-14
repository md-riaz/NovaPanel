package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/rpc"
)

type phpProbeRunner struct {
	calls             [][]string
	cliCode, unitCode int
	cliErr, unitErr   error
	unitState         string
}

func (r *phpProbeRunner) Run(_ context.Context, p string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, append([]string{p}, args...))
	if p == "/usr/bin/php8.3" {
		return nil, nil, r.cliCode, r.cliErr
	}
	return []byte(r.unitState), nil, r.unitCode, r.unitErr
}
func TestPHPListAvailability(t *testing.T) {
	for _, tc := range []struct {
		name   string
		runner phpProbeRunner
		want   string
	}{
		{"available", phpProbeRunner{unitState: "loaded\n"}, `{"versions":["8.3"],"default":"8.3"}`},
		{"cli missing", phpProbeRunner{cliCode: -1, cliErr: errors.New("missing")}, `{"versions":[],"default":"8.3"}`},
		{"cli fails", phpProbeRunner{cliCode: 1}, `{"versions":[],"default":"8.3"}`},
		{"unit absent", phpProbeRunner{unitState: "not-found\n"}, `{"versions":[],"default":"8.3"}`},
		{"unit probe fails", phpProbeRunner{unitCode: 1, unitErr: errors.New("failed")}, `{"versions":[],"default":"8.3"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := agent.New()
			a.Exec = &tc.runner
			reply := rpc.NewServer(a).Dispatch(rpc.WithCaller(context.Background(), rpc.Caller{Role: "read_only"}), rpc.Request{Verb: "php.list", Args: json.RawMessage(`{}`)})
			if !reply.OK {
				t.Fatalf("%+v", reply)
			}
			b, err := json.Marshal(reply.Data)
			if err != nil || string(b) != tc.want {
				t.Fatalf("%s %v", b, err)
			}
			wantCalls := 2
			if tc.runner.cliCode != 0 || tc.runner.cliErr != nil {
				wantCalls = 1
			}
			if len(tc.runner.calls) != wantCalls {
				t.Fatalf("calls: %v", tc.runner.calls)
			}
			if len(tc.runner.calls) == 0 || !reflect.DeepEqual(tc.runner.calls[0], []string{"/usr/bin/php8.3", "-v"}) {
				t.Fatal(tc.runner.calls)
			}
			if len(tc.runner.calls) > 1 && !reflect.DeepEqual(tc.runner.calls[1], []string{"/usr/bin/systemctl", "show", "--property=LoadState", "--value", "php8.3-fpm.service"}) {
				t.Fatal(tc.runner.calls)
			}
		})
	}
}
