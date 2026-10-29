# Phase 1 — State & plumbing

## Goal
The agent has **durable state, a working job queue, append-only audit, and systemd packaging**, and a
**walking skeleton** verb flows browser → `panel-web` → socket → `panel-agent` → reply end-to-end —
before any real feature rides the channel.

## Depends on
Phase 0. Reads: `00-foundations.md` F1, F5, F6, F7, F10, F11, F13.

## Deliverables
1. **SQLite store** (`internal/store`): `Open()` opens `/var/lib/novapanel/panel.db` with the F5
   pragmas, runs pending migrations, exposes typed accessors. Migration `0001_init.sql` creates the
   core tables that exist now: `schema_migrations`, `users`, `sessions`, `jobs`, `events`,
   `settings`, `secrets` (empty shells for later phases are fine, but `jobs`/`events`/`settings`
   must be complete).
2. **Job queue + worker** (`internal/jobs`) per F6: `Enqueue(verb, args, objectKey) (Job, error)`,
   the single worker goroutine, the keyed per-object lock, `job.status`/`job.list` verbs, and
   **roll-forward recovery** on start (reset `running` → `queued`, re-run from the top). A
   `reconcile` verb stub (no-op registry entry that later phases extend).
3. **Audit** (`internal/audit`) per F11: every dispatch outcome (ok/refused/error/panic) writes an
   `events` row **and** appends to `/var/log/novapanel/audit.log` (`chattr +a`). The deferred
   `recover()` on the dispatch path audits panics.
4. **Secret sealing** (`internal/secret`) per F12: `Seal`/`Open` with the root-held key; a
   `settings`-backed smoke test (seal a value, reopen it). No real secrets consumed yet.
5. **systemd packaging** (`packaging/systemd`): `novapanel-agent.service` (hardened, from the BHPanel
   template minus quota lines), `novapanel-web.service`, `novapanel.tmpfiles` (creates
   `/run/novapanel` and `/var/lib/novapanel`). `systemd-analyze verify` passes (the Phase-0 CI job
   now has units to check).
6. **Walking skeleton:** `service.status{unit}` — a real typed verb that runs
   `systemctl is-active/is-enabled <unit>` via `internal/exec` (argv), validated **unit name**
   (`validate.UnitName`, allowlist of manageable units), returns structured status
   `{unit, active, enabled}`. Exercised end-to-end **via `novapanelctl`** (local, peer-gated — there
   is no web auth until Phase 2, so the skeleton does **not** go through `panel-web` yet). It is
   `Public=false, MinRole=admin`; `novapanelctl` runs as the `novapanel` user and the agent resolves
   a local-admin session for ctl (a bootstrap `kind=user` session minted for the peer-gated ctl, per
   F4). `service.restart{unit}` is the **first slow + effectful verb** (job queue) to exercise F6.
   *(The `panel-web` rendering of `service.status` moves to Phase 2, once sessions exist.)*

## Blueprint
- **`internal/store/store.go`** — `type Store`; `Open(path) (*Store, error)`; `migrate()`; typed
  methods per table added as phases need them. Jobs/events/settings methods complete now.
- **`internal/store/migrations/0001_init.sql`** — `schema_migrations(version, applied_at)`;
  `users(...)`, `sessions(...)` (shapes per F4, used in Phase 2); `jobs(...)` (F6 columns);
  `events(...)` (F11 columns); `settings(key, value_json)`; `secrets(id, name, nonce, ciphertext)`.
- **`internal/jobs/queue.go`** + **`keyedlock.go`** + **`worker.go`** — `Enqueue`, `Worker.Run(ctx)`,
  per-`objectKey` `sync.Mutex` map with reference counting. On start: `recoverRunning()`.
- **`internal/jobs/registry glue`** — a slow verb's `Run` enqueues and returns `{job}`; `job.status`
  reads a row; the worker calls the verb's **step function** (the real work), which must be idempotent
  (F7). Define `type Step func(ctx, *Agent, args) error`.
- **`internal/audit/audit.go`** — `Record(ev Event)`; opens the append-only file once; the dispatch
  path calls it in a `defer` that also catches panics → `outcome:"error", code:"agent_error"`.
- **`internal/secret/secret.go`** — `Seal([]byte) (Sealed, error)`, `Open(Sealed) ([]byte, error)`;
  key load from `/var/lib/novapanel/seal.key` (generate `0600` if absent at install).
- **`internal/verbs/service.go`** — `service.status` (read-only), `service.restart` (slow, MinRole
  admin); unit-name validation via `internal/validate`.
- **`packaging/systemd/novapanel-agent.service`** — `Type=simple`, root, `Restart=on-failure`,
  `StartLimitIntervalSec=300`/`Burst=20` in `[Unit]`, hardening per F11/F5 notes
  (`NoNewPrivileges=no`, `ProtectKernelTunables=yes`, `SystemCallFilter=@system-service @privileged`
  minus `~@obsolete @swap`, **no** StateDirectory/LogsDirectory/RuntimeDirectory, `TimeoutStopSec=30`).
  `novapanel-web.service`: `User=novapanel` (never root), `ProtectSystem=strict`, minimal
  `CapabilityBoundingSet`, `ReadWritePaths` limited.

## External refs (gathered by the head)
- systemd unit hardening: reuse BHPanel `bhpaneld.service`/`bhpanel-web.service` structure (documented
  in session notes), dropping the quota/`quotactl` lines. Key traps already captured: tmpfiles (not
  `RuntimeDirectory=`) for `/run`; no `StateDirectory=` (double-mount traps nested paths); no
  `LogsDirectory=` (systemd re-chowns vs `chattr +a`); `StartLimit*` must be in `[Unit]`.
- `chattr +a` append-only + the agent re-creating `/var/log/novapanel` if missing.
- `go-sqlite3` (cgo) vs `modernc.org/sqlite` (pure-Go) — head to pick in the spec; default
  `modernc.org/sqlite` to keep the build cgo-free unless a perf need proves otherwise.

## Verification (the gate)
```
1.  `systemd-analyze verify packaging/systemd/*.service`         → clean.
2.  Store: open a fresh DB → migrations apply; re-open → no-op; a bad migration fails the build.
3.  Jobs: enqueue a slow verb → returns a job id immediately; worker runs it; job.status → done.
      - per-object serialization: two jobs on the same objectKey never overlap (test with a sentinel).
      - roll-forward: kill the worker mid-job (leave state 'running'); restart → job re-runs to done,
        and the idempotency test (F7) shows the re-run makes no duplicate side effect.
4.  Audit: every dispatch (ok, refused, error, and a forced panic) writes an events row AND an
      audit.log line; the log file is append-only (`chattr +a` set; a truncate attempt fails).
5.  Secret: seal a value, reopen → equal; the sealed blob is not the plaintext; key file is 0600 root.
6.  Walking skeleton [integration]: `novapanelctl service.status nginx` returns structured status over
      the socket; `service.restart` runs as a job (effectful — a crashed run marks `failed`,
      `code:"interrupted"`, not a silent re-run); an invalid/unlisted unit name →
      code:"invalid_argument". (panel-web rendering is Phase 2.)
7.  CI (Phase 0 gate) still green with the new packages + the systemd-analyze job now active.
```

## Definition of Done
- [ ] DB opens with WAL + migrations; `0001_init` complete; re-run is a no-op.
- [ ] Job queue: enqueue/return-now/worker/status; per-object serial; roll-forward recovery proven.
- [ ] Audit writes table + append-only file for ok/refused/error/panic.
- [ ] Secret seal/open round-trips; key is `0600 root`; web never holds it.
- [ ] systemd units verify; web unit is non-root; tmpfiles create `/run` + `/var/lib`.
- [ ] `service.status` flows end-to-end; `service.restart` is a job; both audited.

## Guardrails (this phase)
- **Roll-forward, not compensation** for local resources (F6). Do not build a compensation/rollback
  framework here; a stuck job re-runs from the top.
- **No feature verbs beyond `service.*`.** No nginx/php/db/fs/firewall yet.
- Audit and secrets are load-bearing from now on — **no verb may mutate without an audit event**, and
  **no secret may touch `panel-web`**. A test enforces the first; review enforces the second.
- Keep migrations forward-only; never edit `0001_init.sql` after it ships.
