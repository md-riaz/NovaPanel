# Phase 0 — Foundation

## Goal
A buildable Go workspace with the RPC spine, the architecture-enforcing tests, and CI + supply-chain
gates — **no product features yet**. The PHP app is frozen out of the way. When this phase is done,
every later phase can add a verb and trust the invariants are machine-enforced.

## Depends on
Nothing. This is the first executable work. Reads: `00-foundations.md` (F1, F2, F3, F10, F13),
`vpspanel-plan.md` §2, §3.

## Deliverables
1. **PHP freeze.** Current PHP NovaPanel preserved at branch/tag `php-legacy`; the Go rewrite owns a
   clean repo root. (Executed on a fresh branch; PHP history stays in git.)
2. **Go workspace**, `module novapanel`:
   - `cmd/panel-agent`, `cmd/panel-web`, `cmd/novapanelctl` — each builds to a binary; at this phase
     `panel-web`/`novapanelctl` are near-empty stubs, `panel-agent` serves the socket + registry.
   - `internal/rpc/` (codec, server, client, session stub, messages registry per F14), **`internal/agent/`**
     (the `Agent` struct, F14 — minimal now), `internal/exec/`, `internal/validate/` (F9/F15
     validators), `internal/config/` (F16), `internal/version/`, and **`internal/verbs/`** (holds the
     seed verbs and self-registers via `init()`; blank-imported by `cmd/panel-agent`).
3. **RPC codec + server** per F2: NDJSON, size caps, close-on-malformed, `SO_PEERCRED` peer gate (F1).
4. **Verb registry** per F13 with **two seed verbs**: `ping` (`Public=true`, returns agent version)
   and `agent.info` (`MinRole=owner`, returns build/version/uptime). `Public` is how "no auth" is
   expressed (F13). No system-touching verbs yet. (`agent.info` is exercised in Phase 1 once the
   local-ctl session exists; Phase 0 verifies `ping` end-to-end.)
5. **Architecture tests** (the invariants, machine-enforced):
   - no-unregistered-verb test (F13).
   - no-shell test: a Go test that walks the AST of `internal/` (and `cmd/`) and fails if anything
     outside `internal/exec` imports `os/exec` or `syscall` for process spawn, or references `"sh"`,
     `"bash"`, `"-c"` as an exec arg. (A plain grep is the CI backstop; the AST test is authoritative.)
   - no-root-web test: `cmd/panel-web/main.go` asserts `os.Geteuid() != 0` at boot (overridable
     `geteuid` var for the test) and refuses to run as root; an AST test fails if `cmd/panel-web` or
     `cmd/novapanelctl` imports `internal/exec`, `internal/store`, `internal/secret`, or
     `golang.org/x/sys/unix` (only `cmd/panel-agent` may).
6. **CI + supply-chain gates** (none exist in the repo today — this is the deliverable):
   `go build ./...`, `go vet`, `staticcheck`, `govulncheck`, `go test ./...`, `gofmt -l` (empty),
   the three architecture tests above, and `systemd-analyze verify` on the packaged units (added in
   Phase 1 — the CI job exists now and is a no-op until units land).
7. **Repo hygiene:** `go.mod`/`go.sum`, `.gitignore`, `Makefile` (or `Taskfile`) with
   `build/test/lint/ci` targets, `LICENSE` handling per the proprietary decision (executed on request).

## Blueprint
- **`internal/rpc/codec.go`** — `DecodeLine([]byte) (Request, error)` (F2 — composes with the server's
  Scanner, which has the 1 MiB request-buffer cap); `Encode(io.Writer, Reply) error` enforces the
  4 MiB reply cap (over-cap → `agent_error`). `Reply` helpers `OK(data)`, `Err(code, msg)`; errors are
  `*CodedError` via `Errf`. One line in, one line out. Table-test malformed/oversized/empty inputs.
- **`internal/rpc/server.go`** — `NewServer(a *Agent)`, `Serve(ln net.Listener)`; `accept` loop; per-conn
  goroutine with a `bufio.Scanner` (buffer cap **1 MiB**) → `DecodeLine` → `Dispatch` → `Encode`; close
  on any decode error. `peerUID(*net.UnixConn)` via `unix.GetsockoptUcred`; per F1 **accept only the web
  uid or uid 0 (root → synthetic owner Caller), reject every other uid before reading any line**; put
  the resolved `Caller` (incl. `Peer`) in ctx.
- **`internal/rpc/messages.go`** — `type Verb`, `Registry`, and `Dispatch(ctx, req) Reply` that:
  looks up `verb` (`unknown_verb` if absent), resolves role from `session` (stub: `ping`/`agent.info`
  only, real sessions in Phase 2), checks `MinRole`/`ReadOnly`, decodes+validates args, runs, maps a
  returned `error` to a `code`. `actor` is passed to audit only (audit lands Phase 1; here it is a
  no-op sink).
- **`internal/exec/exec.go`** — the F15 `Runner` **interface** + `Real` impl
  (`Run(ctx, prog string, args []string, RunOpts)`; RunOpts carries uid/gid/dir/env/stdin), with
  process-group kill on ctx cancel; **used by nothing yet**, but present so no verb reaches for
  `os/exec` directly (the no-shell test enforces it).
- **`cmd/panel-agent/main.go`** — parse config (socket path, web uid), open listener
  (`0660 root:novapanel`), `rpc.Serve`. Refuse to start if not root. No DB yet (Phase 1).
- **Architecture tests in `internal/rpc/arch_test.go`** and a `tools/arch/` or CI script for the greps.

## External refs (gathered by the head — do not re-research)
- `SO_PEERCRED` in Go: `golang.org/x/sys/unix` `GetsockoptUcred` on the accepted `*net.UnixConn`'s
  raw fd (`conn.SyscallConn().Control`). (BHPanel does not use it; this is our added hardening per D5.)
- `govulncheck` (golang.org/x/vuln) and `staticcheck` (honnef.co/go/tools) are the two external
  linters; pin versions in CI.
- Systemd unit hardening template: copy the structure from BHPanel's `bhpaneld.service` (documented in
  `vpspanel-plan-amendments.md`/session notes) but **drop the quota/`quotactl` syscall-filter lines**
  (v1 has no kernel quotas) — the unit itself is a Phase-1 deliverable; Phase 0 only stands up the CI
  job that will verify it.
- NDJSON desync hazard and the 4 MiB cap rationale: BHPanel `web/src/Daemon.php` (F2).

## Verification (the gate — all must pass)
```
1.  `git show-ref php-legacy`                     → the PHP freeze tag/branch exists; repo root is Go-clean.
2.  `go build ./...`                              → all three binaries build.
3.  `go vet ./... && staticcheck ./... && govulncheck ./...`  → clean.
4.  `gofmt -l .`                                  → prints nothing.
5.  `go test ./...`                               → green, including:
      - codec table tests (malformed/oversized/empty → defined errors, connection-close semantics).
      - arch: no-unregistered-verb. A "handler" = a func value assigned to `Verb.Run`/`Verb.Exec` in a
        `rpc.Register(Verb{...})` call inside `internal/verbs`. The test loads the built `Registry` and
        asserts it is non-empty and every `internal/verbs` `Register` call's `Name` is unique; the
        negative fixture (testdata module with a verbs file that defines a `Run` but never calls
        `Register`) is built in a subprocess `go test` and must FAIL.
      - arch: no-shell AST test (the testdata fixture importing `os/exec` outside `internal/exec` FAILS).
      - arch: panel-web refuses euid 0 (via the overridable `geteuid`); only panel-agent imports the
        privileged packages.
6.  [integration] Boot `panel-agent` on a scratch socket; `novapanelctl ping` returns the version; a
    peer whose uid is neither the web uid nor 0 is rejected at connect (peer-gate test).
7.  CI workflow runs steps 2–6 on push and fails the build if any step fails.
```

## Definition of Done
- [ ] `php-legacy` created; Go owns a clean root; `go.mod` is `module novapanel`.
- [ ] Three binaries build; `panel-agent` serves the socket with the `SO_PEERCRED` gate.
- [ ] `ping` + `agent.info` work end-to-end via `novapanelctl`; no other verbs exist.
- [ ] Codec enforces size caps + close-on-malformed; tested.
- [ ] All three architecture tests pass **and demonstrably fail** when their invariant is violated.
- [ ] CI green with `go vet`, `staticcheck`, `govulncheck`, `gofmt`, `go test`, arch tests.
- [ ] No `os/exec` use outside `internal/exec`; no shell anywhere.

## Guardrails (this phase)
- **No feature verbs.** Nothing that touches nginx/php/db/fs/firewall. Only `ping`/`agent.info`.
- **No DB, no audit persistence yet** (Phase 1) — but do not design them out; the dispatch path has a
  no-op audit sink and a stub session resolver with the real interface shape.
- **Do not weaken the peer gate or the no-shell rule to "make something work."** If a later need seems
  to require it, raise it to the head; do not reinterpret.
- Keep `panel-web`/`novapanelctl` as stubs — resist building UI or features early.
