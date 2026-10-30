package acme_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"novapanel/internal/acme"
	"novapanel/internal/secret"
)

func TestAccountRecordsBoundToEnvironmentAndDirectory(t *testing.T) {
	s, _ := secret.New(bytes.Repeat([]byte{7}, 32))
	record, err := acme.NewAccount(s, acme.Staging, "https://staging.example.test/directory", "ops@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "acme:account:staging" {
		t.Fatal(record.Name)
	}
	cfg := acme.Config{Environment: acme.Staging, Directory: "https://staging.example.test/directory", Email: "ops@example.test", Sealer: s, SaveAccount: func(acme.AccountRecord) error { return nil }, BeforeOrder: func(acme.Order) error { return nil }}
	if _, err := acme.New(cfg, record); err != nil {
		t.Fatal(err)
	}
	cfg.Environment = acme.Production
	if _, err := acme.New(cfg, record); err == nil {
		t.Fatal("accepted staging account in production")
	}
	cfg.Environment = acme.Staging
	cfg.Directory = "https://other.example.test/directory"
	if _, err := acme.New(cfg, record); err == nil {
		t.Fatal("accepted wrong directory")
	}
	for _, value := range []any{record, &record} {
		encoded, _ := json.Marshal(value)
		for _, output := range []string{string(encoded), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if bytes.Contains([]byte(output), []byte("PRIVATE KEY")) || bytes.Contains([]byte(output), []byte("ops@example.test")) {
				t.Fatal("account plaintext exposed")
			}
		}
	}
}
