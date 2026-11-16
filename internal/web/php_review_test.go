package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	_ "novapanel/internal/verbs"
)

type phpDispatch struct{ server *rpc.Server }

func (*phpDispatch) Close() error { return nil }
func (p *phpDispatch) Call(ctx context.Context, verb string, args any, token string) (rpc.Reply, error) {
	if verb == "session.whoami" {
		return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
	}
	b, _ := json.Marshal(args)
	return p.server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: b}), nil
}
func TestPHPReviewRealDispatcherPoll(t *testing.T) {
	p := &phpDispatch{rpc.NewServer(agent.New())}
	h, err := New(Options{Origin: "https://panel.example", Dial: func() (RPCClient, error) { return p, nil }})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", "/sites/SITE/php/jobs/JOB", ""))
	if w.Code != 501 {
		t.Fatalf("expected queue unavailable after decoding, got %d: %s", w.Code, w.Body.String())
	}
}
func TestPHPReviewJobBinding(t *testing.T) {
	for _, tc := range []struct{ verb, key string }{{"php.set", "site:OTHER"}, {"site.delete", "site:SITE"}, {"php.set", ""}} {
		t.Run(tc.verb+tc.key, func(t *testing.T) {
			f := identityRPC()
			f.reply = func(v string) (rpc.Reply, error) {
				if v == "session.whoami" {
					return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
				}
				if v == "job.status" {
					return rpc.Reply{OK: true, Data: map[string]any{"id": "JOB", "verb": tc.verb, "object_key": tc.key, "state": "done"}}, nil
				}
				t.Fatalf("unexpected call %s", v)
				return rpc.Reply{}, nil
			}
			w := httptest.NewRecorder()
			setup(t, f).ServeHTTP(w, request("GET", "/sites/SITE/php/jobs/JOB", ""))
			if w.Code != 404 || strings.Contains(w.Body.String(), "Job completed") {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestPHPReviewFormWithoutPersistedLimits(t *testing.T) {
	f := identityRPC()
	f.reply = func(v string) (rpc.Reply, error) {
		if v == "session.whoami" {
			return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
		}
		return rpc.Reply{OK: true, Data: map[string]any{"site": map[string]any{"type": "php", "php_version": "8.3"}}}, nil
	}
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("GET", "/sites/SITE", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `action="/sites/SITE/php"`) || !strings.Contains(w.Body.String(), "Optional hardened PHP is unavailable.") {
		t.Fatalf("missing form or notice: %d %s", w.Code, w.Body.String())
	}
}

func TestPHPReviewOptionalLimits(t *testing.T) {
	for _, tc := range []struct {
		body   string
		limits map[string]any
	}{{"version=8.3", nil}, {"version=8.3&memory_mb=512", map[string]any{"memory_mb": 512}}, {"version=8.3&memory_mb=&upload_mb=64", map[string]any{"upload_mb": 64}}} {
		t.Run(tc.body, func(t *testing.T) {
			f := identityRPC()
			f.reply = func(v string) (rpc.Reply, error) {
				if v == "session.whoami" {
					return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
				}
				return rpc.Reply{OK: true, Data: map[string]any{"job": map[string]any{"id": "JOB", "state": "queued"}}}, nil
			}
			w := httptest.NewRecorder()
			setup(t, f).ServeHTTP(w, request("POST", "/sites/SITE/php", tc.body))
			if w.Code != 202 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			args := f.rpcCalls[1].args.(map[string]any)
			got, exists := args["limits"]
			if tc.limits == nil {
				if exists {
					t.Fatalf("unexpected limits: %#v", got)
				}
			} else if !reflect.DeepEqual(got, tc.limits) {
				t.Fatalf("limits %#v", got)
			}
		})
	}
}
