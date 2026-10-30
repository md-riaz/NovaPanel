package verbs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	certs "novapanel/internal/tls"
)

func TestACMEQueuedCanonicalNoop(t *testing.T) {
	for _, verb := range []string{"ssl.issue", "ssl.renew"} {
		t.Run(verb, func(t *testing.T) {
			f := newACMEWorkerFixture(t)
			expectedOrders := 1
			if verb == "ssl.renew" {
				if j := f.run(t, "ssl.issue"); j.State != "done" {
					t.Fatal(j)
				}
				expectedOrders = 2
			}
			ctx := context.Background()
			f.ca.mu.Lock()
			f.ca.lifetime = 90 * 24 * time.Hour
			locked := true
			defer func() {
				if locked {
					f.ca.mu.Unlock()
				}
			}()
			ids := []string{}
			for i := 0; i < 2; i++ {
				r := f.server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"staging":true}`, f.sid))})
				if !r.OK {
					t.Fatal(r)
				}
				ids = append(ids, r.Data.(jobs.EnqueueResult).Job.ID)
			}
			f.ca.mu.Unlock()
			locked = false
			for _, id := range ids {
				deadline := time.Now().Add(10 * time.Second)
				for {
					j, e := f.a.Store.GetJob(ctx, id)
					if e != nil {
						t.Fatal(e)
					}
					if j.State == "done" || j.State == "failed" {
						pair, err := certs.Load(f.a.Cfg.TLSDir, "example.test", []string{"example.test"}, time.Now())
						if err != nil {
							t.Fatal(err)
						}
						want := fmt.Sprintf(`{"fqdns":["example.test"],"not_after":%d}`, pair.NotAfter.UnixMilli())
						if verb == "ssl.renew" {
							want = fmt.Sprintf(`{"not_after":%d}`, pair.NotAfter.UnixMilli())
						}
						if j.State != "done" || string(j.Result) != want || j.Step != "tls_committed" {
							t.Fatalf("noncanonical queued issue: state=%s step=%s result=%s want=%s", j.State, j.Step, j.Result, want)
						}
						j.State = "running"
						if e = f.a.Store.UpdateJob(ctx, j); e != nil {
							t.Fatal(e)
						}
						original := append([]byte(nil), j.Result...)
						j.Result = nil
						if e = f.server.ExecuteJob(ctx, &j); e != nil {
							t.Fatal(e)
						}
						if !bytes.Equal(j.Result, original) {
							t.Fatalf("replay: %s != %s", j.Result, original)
						}
						j.State = "done"
						if e = f.a.Store.UpdateJob(ctx, j); e != nil {
							t.Fatal(e)
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("timeout")
					}
					time.Sleep(time.Millisecond)
				}
			}
			f.ca.mu.Lock()
			defer f.ca.mu.Unlock()
			if f.ca.orders != expectedOrders {
				t.Fatalf("orders=%d", f.ca.orders)
			}
			if r := f.a.Nginx.Runner.(*acmeTestRunner); r.reloads != expectedOrders {
				t.Fatalf("reloads=%d", r.reloads)
			}
		})
	}
}
