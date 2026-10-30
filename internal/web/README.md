# Web shell

Stateless chi + html/template renderer with self-hosted HTMX and compiled Tailwind CSS, following the canonical stack decision in docs/spec/05-adr.md. The agent remains the session, authorization and persistence authority. The web process only uses the existing RPC client.

## Anonymous login

PR77 F4 defines `session.start` as idempotent get-or-create. Login GET presents the existing cookie; login POST re-fetches the same anonymous row's CSRF via that verb, compares `X-CSRF-Token` constant-time, then forwards only canonical login fields. Login replaces the hardened session cookie and redirects to the dashboard. Authenticated mutations re-fetch their CSRF via `session.whoami`. No session/CSRF cache, CSRF cookie, or new RPC field exists.

## Transport rate limit

F16's `rate_limit_rps` and `rate_limit_burst` default to 5 and 20. `panel-web` reads the existing config loader once at startup from `/etc/novapanel/agent.toml`, or the `NOVAPANEL_CONFIG` path, with `NOVAPANEL_RATE_LIMIT_RPS` / `NOVAPANEL_RATE_LIMIT_BURST` environment overrides. Invalid settings fail startup. No account resolution or agent-state access is needed.

Every routed request carrying the canonical session cookie spends one token before dialing RPC, including anonymous login and authenticated requests. Only the credential passed to RPC keys the bucket; Authorization, role and forwarded-address headers do not change it. Origin and duplicate-cookie refusals run first; security headers wrap all responses. Exhaustion returns HTTP 429 with `Retry-After: 1` without an RPC connection. Agent credential-failure budgets are unchanged.

The process retains at most 4096 SHA-256 credential keys and token counters, not credentials or session authority. On capacity pressure it removes only fully replenished buckets; if all budgets remain active, new keyed requests fail closed with 429. Assets and cookie-less login bootstrap remain reachable. The spec defines no cookie-less transport identity, so no IP/header/global policy is invented; the agent's existing per-peer mint throttle still applies. Limits are process-local and reset on restart.

## Checks

- `go test ./internal/web ./cmd/panel-web`
- `go test -race ./internal/web ./cmd/panel-web`
- `go vet ./internal/web ./cmd/panel-web`
- Install jsdom 26.1.0 and jsqr 1.4.0 in a scratch npm prefix, then run `NODE_PATH=/path/to/scratch/node_modules node internal/web/assets_test.cjs`. This tests the CSRF event and independently decodes the rendered enrollment SVG with jsQR. These are test-only dependencies, not served assets.

The module includes chi; no scratch module override is needed.

## Real browser acceptance

Install `playwright@1.58.2` in a scratch npm prefix, then run:

```sh
NODE_PATH=/path/to/scratch/node_modules \
NOVAPANEL_BROWSER_NODE="$(command -v node)" \
NOVAPANEL_BROWSER_ARTIFACTS=/path/to/scratch/browser-artifacts \
go test ./internal/web/testdata/browser -run TestBrowserAuthentication -count=1 -v
```

`NOVAPANEL_CHROME` optionally selects the Chrome executable. The default is `/usr/bin/google-chrome`. The test uses a disposable TLS server, real SQLite/auth/dispatcher, and an injected web peer identity. It never connects to a host agent, invokes service commands, or grants implicit host-root identity. Its test-only expiry endpoint is not in the production router. The browser driver and process launcher live in testdata, outside production packages. The audit assertion rejects credential/session leakage.

The idempotent `session.start` implementation is integrated. Real Chromium acceptance passes anonymous reload and login against the actual dispatcher. The QR payload uses `data-enrollment`, not `data-uri`: html/template classifies the latter as a navigation URL and filters the `otpauth` scheme.

## Assets

- `assets/htmx.min.js` is self-hosted HTMX. Its license is `assets/htmx.LICENSE`.
- `assets/qrcode.js` is Kazuhiko Arase's MIT-licensed QR Code Generator, with the upstream copyright/license notice retained. Enrollment uses its byte-mode encoder and a local DOM-created SVG, without a remote QR service.
- `styles/input.css` is the Tailwind 4.1.18 build input. Rebuild using scratch-installed `@tailwindcss/cli` 4.1.18 with this directory as the project source, resolving `tailwindcss` from that scratch installation, and emit `assets/app.css`. No CDN or Node runtime is needed by panel-web.

Real Chromium acceptance verifies the isolated auth-agent flow, anonymous reload/login, HTMX submits, enrollment/confirmation, backup show-once/replay, reauthentication rotation, timeout, role refusal, CSRF/Origin/Fetch Metadata refusal, and logout. The audit check verifies successful and failed login events and rejects known secret shapes. Final privileged VM acceptance remains unverified. Dashboard metrics are read-only placeholders until their later-phase agent verbs exist.
