package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	runner "novapanel/internal/exec"
	"novapanel/internal/secret"
)

type fakeRestic struct {
	code     int
	calls    int
	password string
	args     []string
}

func (f *fakeRestic) Run(_ context.Context, prog string, args []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	f.calls++
	f.args = append([]string{}, args...)
	if prog != "/usr/bin/restic" {
		return nil, nil, -1, errors.New("unexpected program")
	}
	for _, v := range o.Env {
		if strings.HasPrefix(v, "RESTIC_PASSWORD=") {
			f.password = strings.TrimPrefix(v, "RESTIC_PASSWORD=")
		}
	}
	if f.code != 0 {
		return []byte(f.password), []byte(f.password), f.code, errors.New(f.password)
	}
	return nil, nil, 0, nil
}
func TestOpenStoredPasswordForExecution(t *testing.T) {
	sealer, _ := secret.New(make([]byte, 32))
	sealed, _ := sealer.Seal([]byte("stored-fixture-password"))
	p, err := OpenPassword(sealer, sealed)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	f := &fakeRestic{}
	r := Repository{ID: "r", Name: "r", Kind: "local", Role: "panel", Location: "/srv/recovery"}
	if err = InitRepository(context.Background(), f, r, p); err != nil || f.password != "stored-fixture-password" {
		t.Fatal(err)
	}
	sealed.Ciphertext[0] ^= 1
	if _, err = OpenPassword(sealer, sealed); err == nil {
		t.Fatal("tamper accepted")
	}
}

func TestGeneratedPasswordSealedClaimAndFakeInit(t *testing.T) {
	sealer, _ := secret.New(make([]byte, 32))
	p, err := GeneratePassword(sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	plain, err := sealer.Open(p.Sealed())
	if err != nil || len(plain) < 32 {
		t.Fatal("invalid sealed password")
	}
	for _, text := range []string{fmt.Sprint(p), fmt.Sprintf("%+v", p), fmt.Sprintf("%#v", p), fmt.Sprintf("%#v", *p)} {
		if strings.Contains(text, string(plain)) {
			t.Fatal("format leak")
		}
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), string(plain)) {
		t.Fatal("JSON leak")
	}
	publisher := &claimRecorder{}
	if err = p.PublishClaim(context.Background(), publisher, "job1"); err != nil || publisher.job != "job1" || publisher.password != string(plain) {
		t.Fatal("claim failure", err)
	}
	f := &fakeRestic{}
	r := Repository{ID: "r", Name: "r", Kind: "local", Role: "panel", Location: "/srv/recovery"}
	if err = InitRepository(context.Background(), f, r, p); err != nil || f.calls != 1 || f.password != string(plain) {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(f.args, " "), string(plain)) {
		t.Fatal("argv leak")
	}
	f.code = 3
	if err = InitRepository(context.Background(), f, r, p); err == nil || strings.Contains(err.Error(), string(plain)) {
		t.Fatal("exit failure/leak", err)
	}
	p.Close()
	if err = InitRepository(context.Background(), f, r, p); err == nil {
		t.Fatal("closed credential accepted")
	}
}

type claimRecorder struct{ job, password string }

func (c *claimRecorder) PublishClaim(_ context.Context, j string, fields map[string]any) error {
	c.job = j
	c.password = fields["password"].(string)
	return nil
}
