package verbs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationSetProbeFailure(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a := &agent.Agent{Store: s, Secret: sealer, ProbeDNS: func(context.Context, string, []string) error { return errors.New("token leaked by provider") }}
	args := &integrationSetArgs{Kind: "cloudflare", Token: "token leaked by provider", Zones: []string{"example.com"}}
	if err := args.Validate(); err != nil {
		t.Fatal(err)
	}
	_, err = integrationSet(context.Background(), a, args)
	if err == nil || err.Error() == "token leaked by provider" {
		t.Fatal("probe error was not safely rejected")
	}
	a.ProbeDNS = func(context.Context, string, []string) error { return nil }
	if _, err := integrationSet(context.Background(), a, args); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationSetSecretsStaySealed(t *testing.T) {
	const token = "cloudflare-test-token-never-persist-0123456789"
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(dir, "audit.log")
	auditLog, err := audit.Open(s, auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer auditLog.Close()
	a := &agent.Agent{Store: s, Secret: sealer, Audit: auditLog, ProbeDNS: func(_ context.Context, got string, zones []string) error {
		if got != token || len(zones) != 1 || zones[0] != "example.com" {
			t.Fatal("incorrect probe inputs")
		}
		return nil
	}}
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	srv := rpc.NewServer(a)
	args, err := json.Marshal(integrationSetArgs{Kind: "cloudflare", Token: token, Zones: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := rpc.WithCaller(context.Background(), rpc.RootCaller(0))
	req := rpc.Request{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Verb: "integration.set", Args: args}
	reply := srv.Dispatch(ctx, req)
	if !reply.OK {
		t.Fatalf("integration failed: %+v", reply)
	}
	encoded, err := json.Marshal(reply)
	if err != nil || bytes.Contains(encoded, []byte(token)) {
		t.Fatal("secret in reply")
	}
	a.ProbeDNS = func(context.Context, string, []string) error { return errors.New(token) }
	failure := srv.Dispatch(ctx, req)
	if failure.OK || bytes.Contains([]byte(failure.Error), []byte(token)) {
		t.Fatal("secret in error")
	}
	redacted, err := json.Marshal(secret.Redact(map[string]any{"token": token}))
	if err != nil || bytes.Contains(redacted, []byte(token)) {
		t.Fatal("secret redaction failed")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var nonce, ciphertext []byte
	if err := db.QueryRow("SELECT nonce,ciphertext FROM secrets WHERE name LIKE 'integration:%:token'").Scan(&nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	plain, err := sealer.Open(secret.Sealed{Nonce: nonce, Ciphertext: ciphertext})
	if err != nil || string(plain) != token {
		t.Fatal("stored token is not sealed correctly")
	}
	var jobs, events int
	if err := db.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("unexpected job")
	}
	if err := db.QueryRow("SELECT count(*) FROM events WHERE verb='integration.set'").Scan(&events); err != nil || events == 0 {
		t.Fatal("missing audit events")
	}
	for _, file := range []string{path, path + "-wal", auditPath} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(token)) {
			t.Fatalf("secret found in %s", filepath.Base(file))
		}
	}
	if bytes.Contains(logs.Bytes(), []byte(token)) {
		t.Fatal("secret found in log")
	}
}
