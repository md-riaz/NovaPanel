package backup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	runner "novapanel/internal/exec"
	"novapanel/internal/secret"
)

// Password is ephemeral execution custody. Only PublishClaim explicitly releases
// plaintext to the existing F19 publisher, which seals it for the job owner.
// Close clears owned bytes, not every runtime string copy. Do not log RunOpts.
// Password methods require exclusive use.
type Password struct {
	plain  []byte
	sealed secret.Sealed
}

func (Password) Format(s fmt.State, _ rune)    { fmt.Fprint(s, "[repository password redacted]") }
func (*Password) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (p *Password) Close()                     { clear(p.plain); p.plain = nil }
func (p *Password) Sealed() secret.Sealed {
	return secret.Sealed{Nonce: append([]byte(nil), p.sealed.Nonce...), Ciphertext: append([]byte(nil), p.sealed.Ciphertext...)}
}

// OpenPassword opens stored ciphertext only for short-lived execution custody.
func OpenPassword(sealer *secret.Sealer, sealed secret.Sealed) (*Password, error) {
	if sealer == nil {
		return nil, errors.New("repository password unavailable")
	}
	plain, err := sealer.Open(sealed)
	if err != nil || len(plain) == 0 || len(plain) > 1024 {
		clear(plain)
		return nil, errors.New("repository password unavailable")
	}
	return &Password{plain: plain, sealed: secret.Sealed{Nonce: append([]byte(nil), sealed.Nonce...), Ciphertext: append([]byte(nil), sealed.Ciphertext...)}}, nil
}

func GeneratePassword(sealer *secret.Sealer) (*Password, error) {
	if sealer == nil {
		return nil, errors.New("missing repository sealer")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, errors.New("password generation failed")
	}
	defer clear(raw)
	plain := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(plain, raw)
	sealed, err := sealer.Seal(plain)
	if err != nil {
		clear(plain)
		return nil, errors.New("password sealing failed")
	}
	return &Password{plain: plain, sealed: sealed}, nil
}

type ClaimPublisher interface {
	PublishClaim(context.Context, string, map[string]any) error
}

func (p *Password) PublishClaim(ctx context.Context, publisher ClaimPublisher, jobID string) error {
	if p == nil || len(p.plain) == 0 || publisher == nil || jobID == "" {
		return errors.New("repository claim unavailable")
	}
	if err := publisher.PublishClaim(ctx, jobID, map[string]any{"password": string(p.plain)}); err != nil {
		return errors.New("repository claim publication failed")
	}
	return nil
}

// InitRepository is an invocation contract, not durable repository registration.
// Callers own admission, serialization, recovery and F19 publication. The runner
// must never log argv-adjacent options or child output: those may contain secrets.
func InitRepository(ctx context.Context, run runner.Runner, r Repository, p *Password) error {
	if ValidateRepository(r) != nil {
		return ErrRepository
	}
	if p == nil || len(p.plain) == 0 || run == nil {
		return errors.New("repository execution unavailable")
	}
	return runRestic(ctx, run, r, p, []string{"init"})
}

// BackupPanelSnapshot uploads only the private SQLite snapshot. It does not
// package config, seal.key or ACME keys, publish a snapshot ID, mark a durable
// backup complete, or claim read-back verification.
func BackupPanelSnapshot(ctx context.Context, run runner.Runner, r Repository, p *Password, snapshot *Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ValidateRepository(r) != nil || r.Role != "panel" {
		return ErrRepository
	}
	if run == nil || p == nil || len(p.plain) == 0 || snapshot == nil || snapshot.dir == nil {
		return errors.New("panel backup execution unavailable")
	}
	if err := snapshot.validatePath(); err != nil {
		return err
	}
	return runRestic(ctx, run, r, p, []string{"backup", "--tag", "panel", "--", snapshot.path})
}

func runRestic(ctx context.Context, run runner.Runner, r Repository, p *Password, args []string) error {
	argv := append([]string{"-r", r.Location, "--retry-lock", "30s"}, args...)
	_, _, code, err := run.Run(ctx, "/usr/bin/restic", argv, runner.RunOpts{Env: []string{"RESTIC_PASSWORD=" + string(p.plain)}, MaxOutput: 4096})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || code != 0 {
		return errors.New("restic command failed")
	}
	return nil
}
