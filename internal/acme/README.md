# ACME consumer contract

This package implements lego-backed HTTP-01 only. It does not complete `ssl.issue` or `ssl.renew` RPCs, durable throttling, renewal scheduling, certificate publication, nginx reload/probe/rollback, or coverage updates.

## API

```go
NewAccount(sealer *secret.Sealer, env Environment, directory, email string) (AccountRecord, error)
New(cfg Config, record AccountRecord) (*Client, error)
(*Client).Issue(ctx context.Context, allSiteDomains []string, webroot string) (*tls.Certificate, error)
(*Client).Renew(ctx context.Context, allSiteDomains []string, webroot string, previousFullchain []byte) (*tls.Certificate, error)
NewWebroot(root string, domains []string) (*Webroot, error)
(*Webroot).Present(domain, token, keyAuth string) error
(*Webroot).CleanUp(domain, token, keyAuth string) error
(*Webroot).Close() error
```

The returned opaque certificate can be passed directly to the existing `internal/tls` installation APIs. Key bytes are not exposed in JSON or formatting. `Issue` and `Renew` request every supplied site domain in one bundled SAN order. Put the persisted primary first. Renew uses `ObtainRequest.ReplacesCertID`, not deprecated `Renew`, so an expanded site domain set is not silently replaced by the old certificate's SAN list. Lego only sends `replaces` when the directory advertises renewalInfo. Scheduling and ARI suggested-window queries are not implemented here.

`Environment` is `Staging` or `Production`. `New` defaults an empty environment/directory to staging only. Production requires explicit HTTPS directory and nonempty valid email. Read directory/email/environment from agent Cfg, not RPC-supplied URLs. `NewAccount` requires an explicit directory/environment. A production account is not allowed to open a staging record, nor can any record move between directory URLs or emails.

`NewAccount` generates a P-256 key and seals the key, registration, directory, email and record name together. Save the initial ciphertext under `acme:account:staging` or `acme:account:prod` BEFORE use. `New` never generates a replacement for a missing or corrupt account. `Config.SaveAccount` must durably replace that exact record; registration is saved before the certificate order. A failed save aborts ordering and remains retryable with the same client or the previously saved key. No terms acceptance is implicit; set `TermsOfServiceAgreed` explicitly after coordinator policy permits it.

## Mandatory coordinator policy

`BeforeOrder` and `SaveAccount` are required, with no no-op defaults. BeforeOrder runs before directory fetch, registration or ordering. It must reserve persisted budgets atomically, under the coordinator's site/account locks, and return the canonical `rate_limited` error on exhaustion. Implement the spec's registered-domain 50/7d, exact-set 5/7d and auth-failure 5/hour windows and rollover in settings. Lego's own in-memory request pacing is not this policy. Account concurrency across different Client instances is also coordinator-owned.

After any ambiguous CA error, conservatively retain the reservation and charge potential auth failures; this package returns a redacted `ErrCA` rather than forwarding upstream errors containing challenge material. No failure classification or durable accounting is supplied. Never blindly refund failed attempts. Idempotent no-op detection, due-date decisions, interrupted-job recovery, auditing, staging flags, certificate-source checks, and secret cleanup remain coordinator requirements. Wildcards return canonical `unsupported` before network I/O; other package validation errors require coordinator mapping to F3 codes.

The coordinator must load and revalidate the previously installed certificate before `Renew`; the prior fullchain is only used to derive replacement identity here. Do not accept that PEM as arbitrary RPC input. Account records must stay agent-only. Callbacks are trusted agent code and must not include secrets in errors.

## Webroot and transport

Pass an existing absolute, canonical site webroot whose ancestors are controlled by the agent. The root and challenge directories must belong to the effective agent uid, be world-readable/traversable and not group/other-writable. For production this means root-owned directories with mode 0755. The provider creates `.well-known/acme-challenge` beneath that root. Its dirfd-relative no-follow opens reject child symlinks; token creation is exclusive, never overwrites an existing file, and writes only a bounded validated token/thumbprint pair. Tokens are 22..128 base64url characters, thumbprints exactly 43; at most 100 active tokens. Challenge files are 0644 and synced before approval. Cleanup only unlinks tokens this provider created. Close removes remaining active files; a process crash can leave stale tokens, which require coordinator cleanup while issuance is stopped. Never recursively delete an untrusted path.

Only HTTP-01 is configured. No listening socket, DNS provider, port override, shell, host service, or production path is opened by the package itself. Nginx must serve this same root over port 80 without a redirect. Per-site serialization prevents cooperating issuers from interfering.

HTTP uses normal TLS verification, no redirects, a same-origin endpoint policy, a 1 MiB response read bound and an operation context on every request. The same-origin rule intentionally rejects CAs that advertise endpoints/issuer fetches on another origin; support for such CAs needs explicit reviewed allowlisting. Config.HTTPClient contributes only its trusted RoundTripper, useful for private test roots, not cookies or redirect policy. Default transport obeys its normal environment proxy settings. Timeout defaults to two minutes, maximum ten. Lego's internal polling/backoff sleeps and trusted coordinator callbacks are synchronous and not context-interruptible; cancellation prevents further HTTP requests but can return after a polling sleep. Do not describe this as a hard wall-clock deadline.

## Dependency pin and authoritative signatures

Approved major is `github.com/go-acme/lego/v4`, pinned here to **v4.26.0**, tag commit `42f057cc723ef14b96119e6579e56cdbe9a63521`. Shared go.mod/go.sum were deliberately not edited. Before coding, retrieved the pinned pkg.go.dev documentation and downloaded the actual tagged source:

- https://pkg.go.dev/github.com/go-acme/lego/v4@v4.26.0/lego confirms `NewConfig(registration.User)`, `Config.HTTPClient`, and `NewClient(*Config)`.
- https://pkg.go.dev/github.com/go-acme/lego/v4@v4.26.0/registration confirms `User` methods, `Register(RegisterOptions)` and registration resource.
- https://pkg.go.dev/github.com/go-acme/lego/v4@v4.26.0/certificate confirms `Obtain(ObtainRequest)`, `Bundle`, `ReplacesCertID`, `MakeARICertID`, and PEM resource fields.
- https://github.com/go-acme/lego/blob/v4.26.0/acme/api/order.go confirms `ReplacesCertID` becomes `replaces` only when renewalInfo is advertised.
- https://github.com/go-acme/lego/blob/v4.26.0/challenge/http01/http_challenge.go confirms the provider Present/CleanUp lifecycle.

The alternate modfile compile added these requirements, without upgrading existing requirements:

```diff
+ github.com/go-acme/lego/v4 v4.26.0
+ github.com/cenkalti/backoff/v4 v4.3.0 // indirect
+ github.com/go-jose/go-jose/v4 v4.1.2 // indirect
+ github.com/miekg/dns v1.1.68 // indirect
+ golang.org/x/net v0.60.0 // indirect
+ golang.org/x/text v0.43.0 // indirect
```

Integration owner can apply `go get github.com/go-acme/lego/v4@v4.26.0` and review generated go.mod/go.sum changes. For isolated reproduction, copy the repository mod/sum to a new scratch `acme.mod`/`acme.sum`, then run:

```sh
export PATH=/root/.hermes/cache/scratch/novapanel-toolchain/bin:$PATH
go mod edit -modfile=/root/.hermes/cache/scratch/novapanel-acme.mod -require=github.com/go-acme/lego/v4@v4.26.0
go test -mod=mod -modfile=/root/.hermes/cache/scratch/novapanel-acme.mod -race ./internal/acme
go vet -modfile=/root/.hermes/cache/scratch/novapanel-acme.mod ./internal/acme
```

Tests use only temporary files and a loopback TLS fake CA. They execute real lego JWS requests, registration, authorization, webroot publication/cleanup, CSR generation, certificate parsing, all-site SAN coverage, and replacement orders. The fake CA signs the actual CSR with an in-memory test key. It does not claim ACME conformance or verify JWS signatures. Pebble and real nginx/CA acceptance remain integration gates; no live CA, real DNS, Docker, host service or production certificate was used.
