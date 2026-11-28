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
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationSetIsIdempotentAndRejectsForeignZone(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a := &agent.Agent{Store: s, Secret: sealer, ProbeDNS: func(_ context.Context, _ string, zones []string) (map[string]string, error) {
		ids := map[string]string{}
		for _, z := range zones {
			ids[z] = "provider-" + z
		}
		return ids, nil
	}}
	args := &integrationSetArgs{Kind: "cloudflare", Token: "first", Zones: []string{"example.com"}}
	first, err := integrationSet(ctx, a, args)
	if err != nil {
		t.Fatal(err)
	}
	args.Token = "second"
	same, err := integrationSet(ctx, a, args)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(first)
	repeated, _ := json.Marshal(same)
	if !bytes.Equal(original, repeated) {
		t.Fatal("same-zone integration identity changed")
	}
	args.Zones = append(args.Zones, "other.example")
	second, err := integrationSet(ctx, a, args)
	if err != nil {
		t.Fatal(err)
	}
	a1, _ := json.Marshal(first)
	a2, _ := json.Marshal(second)
	if !bytes.Equal(a1, a2) {
		t.Fatal("integration identity changed")
	}
	if _, err = dnsList(ctx, a, &dnsZoneArgs{Zone: "example.com"}); err != nil {
		t.Fatal("DNS zone unavailable after re-set")
	}
	zone, err := s.DNSZoneByName(ctx, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.DNSCredential(ctx, zone.IntegrationID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := sealer.Open(secret.Sealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext})
	if err != nil || string(plain) != "second" {
		t.Fatal("token did not rotate")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	foreign, _ := id.New()
	zoneID, _ := id.New()
	if _, err = db.Exec("INSERT INTO integrations(id,kind,name,config_json,created) VALUES(?,'other','other','{}',1)", foreign); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO dns_zones(id,integration_id,zone,provider_zone_id,created) VALUES(?,?,?,?,1)", zoneID, foreign, "foreign.example", "foreign"); err != nil {
		t.Fatal(err)
	}
	args.Token = "third"
	args.Zones = []string{"example.com", "foreign.example"}
	if _, err = integrationSet(ctx, a, args); err == nil {
		t.Fatal("foreign zone accepted")
	} else {
		var coded *rpc.CodedError
		if !errors.As(err, &coded) || coded.Code != "precondition_failed" {
			t.Fatalf("wrong error: %v", err)
		}
	}
	sealed, err = s.DNSCredential(ctx, zone.IntegrationID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err = sealer.Open(secret.Sealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext})
	if err != nil || string(plain) != "second" {
		t.Fatal("conflict changed token")
	}
}

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
	a := &agent.Agent{Store: s, Secret: sealer, ProbeDNS: func(context.Context, string, []string) (map[string]string, error) {
		return nil, errors.New("token leaked by provider")
	}}
	args := &integrationSetArgs{Kind: "cloudflare", Token: "token leaked by provider", Zones: []string{"example.com"}}
	if err := args.Validate(); err != nil {
		t.Fatal(err)
	}
	_, err = integrationSet(context.Background(), a, args)
	if err == nil || err.Error() == "token leaked by provider" {
		t.Fatal("probe error was not safely rejected")
	}
	a.ProbeDNS = func(context.Context, string, []string) (map[string]string, error) {
		return map[string]string{"example.com": "zone-id"}, nil
	}
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
	a := &agent.Agent{Store: s, Secret: sealer, Audit: auditLog, ProbeDNS: func(_ context.Context, got string, zones []string) (map[string]string, error) {
		if got != token || len(zones) != 1 || zones[0] != "example.com" {
			t.Fatal("incorrect probe inputs")
		}
		return map[string]string{"example.com": "zone-id"}, nil
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
	a.ProbeDNS = func(context.Context, string, []string) (map[string]string, error) { return nil, errors.New(token) }
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
