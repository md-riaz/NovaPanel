package jobs

import (
	"encoding/json"
	"errors"
	"novapanel/internal/rpcerr"
	"testing"
)

func TestSiteDeleteFailureResultWhitelist(t *testing.T) {
	const good = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	cases := []struct {
		name, verb, payload string
		err                 error
		want                string
	}{
		{"valid and extra", "site.delete", `{"attached_database_ids":["` + good + `"],"password":"secret"}`, rpcerr.Errf("precondition_failed", "secret"), `{"attached_database_ids":["` + good + `"]}`},
		{"invalid id", "site.delete", `{"attached_database_ids":["password"]}`, rpcerr.Errf("precondition_failed", "secret"), ""},
		{"wrong code", "site.delete", `{"attached_database_ids":["` + good + `"]}`, errors.New("secret"), ""},
		{"bad json", "site.delete", `broken`, rpcerr.Errf("precondition_failed", "secret"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, s, _, ctx := fixture(t)
			r, e := q.Enqueue(ctx, tc.verb, map[string]any{}, "site:test")
			if e != nil {
				t.Fatal(e)
			}
			j, e := s.StartJob(ctx, r.Job.ID, 1)
			if e != nil {
				t.Fatal(e)
			}
			j.Result = json.RawMessage(tc.payload)
			if e = q.finish(ctx, j, tc.err); e != nil {
				t.Fatal(e)
			}
			got, e := s.GetJob(ctx, j.ID)
			if e != nil {
				t.Fatal(e)
			}
			if string(got.Result) != tc.want || got.Error != "job execution failed" {
				t.Fatalf("result=%s error=%s", got.Result, got.Error)
			}
		})
	}
}
