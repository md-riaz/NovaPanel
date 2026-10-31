package verbs_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"novapanel/internal/rpc"
)

// Run only in a separate test binary built with the matching ephemeral test anchor.
func TestUpdateSignedDispatch(t *testing.T) {
	if os.Getenv("NOVAPANEL_UPDATE_TEST_ANCHOR") != "1" {
		t.Skip("requires test-only ldflags anchor")
	}
	var logs bytes.Buffer
	oldLog := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(oldLog)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{42}, ed25519.SeedSize))
	s, _, _ := sessionHarness(t)
	s.ConfigureJobs()
	s.Agent.Cfg.DeadlineDefaultMs = 25
	s.Agent.Cfg.UpdateManifestURL = "https://release.test/manifest.json?secret=not-for-audit"
	body := `{"version":"9.0.0","security_fix":true,"notes":"security release","artifacts":[{"os":"` + runtime.GOOS + `","arch":"` + runtime.GOARCH + `","filename":"release.tar.gz","sha256":"` + strings.Repeat("a", 64) + `","size":123,"url":"https://release.test/archive"}]}`
	mode := "good"
	calls := 0
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("unbounded check")
		}
		if mode == "timeout" {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		if mode == "network" {
			return nil, errors.New("private-network-detail")
		}
		text := body
		if strings.HasSuffix(r.URL.Path, ".sig") {
			sig := ed25519.Sign(key, []byte(body))
			if mode == "bad-signature" {
				sig[0] ^= 1
			}
			text = base64.StdEncoding.EncodeToString(sig)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(text))}, nil
	})}
	defer func() { http.DefaultClient = old }()
	root := rpc.RootCaller(0)
	for _, tc := range []struct{ mode, code string }{{"good", ""}, {"bad-signature", "invalid_argument"}, {"network", "agent_error"}, {"timeout", "deadline_exceeded"}} {
		mode = tc.mode
		var events []rpc.AuditEvent
		s.DurableAudit = func(e rpc.AuditEvent) error { events = append(events, e); return nil }
		start := time.Now()
		r := s.Dispatch(rpc.WithCaller(context.Background(), root), rpc.Request{ID: sessionID(t), Verb: "update.check", Args: json.RawMessage(`{}`)})
		if r.Code != tc.code || r.OK != (tc.code == "") {
			t.Fatalf("%s: %+v", mode, r)
		}
		if time.Since(start) > time.Second {
			t.Fatal("configured deadline ignored")
		}
		if r.OK {
			data := sessionData(t, r)
			if data["latest"] != "9.0.0" || data["security_fix"] != true || data["current"] != "dev" || len(data) != 4 {
				t.Fatalf("projection %+v", data)
			}
		}
		if len(events) != 1 || events[0].Code != tc.code || events[0].Detail["stage"] != "exec" {
			t.Fatalf("audit %+v", events)
		}
		out, _ := json.Marshal([]any{r, events})
		for _, secret := range []string{"not-for-audit", "private-network-detail", "release.tar.gz", "sha256"} {
			if strings.Contains(string(out)+logs.String(), secret) {
				t.Fatalf("leak: %s", out)
			}
		}
	}
	before := calls
	root.MFAat = s.Agent.Now()
	r := s.Dispatch(rpc.WithCaller(context.Background(), root), rpc.Request{ID: sessionID(t), Verb: "update.apply", Args: json.RawMessage(`{}`)})
	if r.Code != "unsupported" || calls != before {
		t.Fatalf("signed apply not gated: %+v", r)
	}
}
