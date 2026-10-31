# Phase 6 — Backups, firewall, status

## Goal
Trustworthy backups (Restic, verified by reading back), safe restore with a pre-restore snapshot,
a **never-lock-yourself-out** firewall, panel self-backup, and status + manual security updates (D2).

## Depends on
Phases 1–4 (jobs, sites, DB). Reads: `00-foundations.md` F6, F7, F10, F11, F12; `vpspanel-plan.md`
§3.5 (firewall), D2.

## Deliverables
1. **Repo management + restic backups.** `repo.create/list/delete` register a restic repo (local/SFTP/
   S3; **B2 via its S3-compatible endpoint**) — the repo password + object-store/SFTP creds are
   **sealed** (`secrets` name `repo:<id>:*`), the password shown once via the F19 claim; `restic init`
   runs on create. `backup.create/list/restore/verify` then reference a `repo_id`: per-site path backup
   + per-DB **failure-aware stream** backup; all restic secrets go via env/`*_FILE` written to a
   root-`0600` tmp and removed after, never argv. Recurring backups/verify/retention are driven by the
   **F18 scheduler** (a `schedule` row per repo), not an inline loop.
2. **Verify by reading back**, honestly: structural `check` + rolling `check --read-data-subset=n/7`
   + periodic **restore drill**. A clean structural check is necessary but not sufficient.
3. **Restore** with a typed confirmation + **pre-restore safety snapshot**; restore order file → DB →
   full site; restore a DB into a **scratch DB first**, validate, then swap (never DROP+restore in
   place).
4. **UFW safe-apply** (`firewall.status/apply`): **dry-run → arm timed revert (deadman) → apply with
   a never-removable SSH rule → fresh-request confirm (nonce) cancels the revert, else auto-revert**.
5. **Panel self-backup** (`novapanelctl backup-config`): panel DB + agent config + ACME account keys,
   with a defined restore order.
6. **Status + manual updates (D2):** signed-manifest update check (verify-before-parse, anti-rollback)
   with notify + one-click apply.

## Blueprint

### Restic (reference argv — transcribe to argv slices, no shell)
- Secrets by env on the child: `RESTIC_PASSWORD_FILE` (preferred), `AWS_ACCESS_KEY_ID`/
  `AWS_SECRET_ACCESS_KEY` (S3; use **B2's S3-compatible endpoint**, not the native B2 backend),
  SFTP via SSH keys. Never the repo password on argv (restic has no `--password` by design).
- Path backup: `restic -r <repo> backup <path> --tag site:<name> --host <panelhost>`.
- **DB backup — failure-aware (primary):**
  `restic -r <repo> backup --stdin-filename <db>.sql --stdin-from-command -- mysqldump --single-transaction --routines --triggers --events <db>`
  (the agent runs as root → `mysqldump` authenticates via the **`unix_socket`** plugin over
  `/run/mysqld/mysqld.sock`; **no `--host`/`-p`/password on argv**)
  (a non-zero `mysqldump` **fails the snapshot** — plain `--stdin` "will not notice failures"; if ever
  used, the agent builds the pipe in Go, checks the dumper exit, and deletes the snapshot on failure).
- List: `snapshots --json`. Restore: `restore <id> --target <dir> [--include <p>]` (optionally
  `--verify` — gate on `restic restore --help` of the pinned version). Verify: `check`, then
  `check --read-data-subset=n/7` across the week. Retention:
  `forget --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune` (`--dry-run` first).
- **Exit codes drive outcome:** `0` ok; **`3` = could not read some source → FAILURE** (not partial
  ok); `11` = failed to lock repo; `12` = wrong password; unknown code = failure. On `11`: `restic
  unlock` (stale-only) + retry once; `--retry-lock` on normal runs; `unlock --remove-all` only after
  confirming via `list locks` + process check that nothing is live.

### UFW safe-apply (the deadman — reference)
```
1. snapshot: capture `iptables-save`/`ip6tables-save` (stdout → file via exec, not shell `>`) + `ufw status numbered`
2. dry-run:  `firewall.apply{rules:[{action,proto,port,source?,comment?}], default_incoming?, window_min?}`
      is a **full desired set** (not a delta): the agent computes the target ruleset, renders the ufw
      commands, `ufw --dry-run` each, and asserts the pinned SSH allow is present and ordered first.
      Every field is validated (`validate.Port`/`validate.CIDR`, F9); **raw ufw strings are refused**.
3. ARM the revert BEFORE applying: `systemd-run --on-active=15min --timer-property=AccuracySec=1s <fw-revert>`
      (atd-free; fallback `at now + 15 minutes`). The revert restores the snapshotted `/etc/ufw/*.rules`
      **and the prior enabled/disabled state** + `ufw reload` (see the resolved block for the exact
      artifact — this reference line defers to it).
4. apply:    ensure the SSH allow lands FIRST (`prepend`/`insert 1`), then `ufw --force enable`
5. verify:   confirm the SSH allow rule is present; **reachability is a client round-trip** (the admin
      must reach the panel again and call `firewall.confirm` within N min) — the agent does NOT claim
      to prove an external connection from inside the box (see the resolved block)
6. confirm:  cancel the revert (stop the transient unit / `atrm <id>`). No confirmation in N min → auto-revert fires.
```
Model the admin/SSH allow as a **pinned invariant** the engine re-asserts (prepended) on every apply;
it is never deletable or orderable below a deny. (CSF's `TESTING` cron-flush + pinning the SSH IP is
the canonical prior art.)

### Self-backup (reference)
Back up: panel DB; agent config (site/vhost definitions, repo definitions + which cred/`*_FILE` maps
to which repo, retention, firewall ruleset); **ACME account private key(s)** (losing them forces
re-registration + rate-limit risk). Restore order: binary+OS pkgs → agent config+secrets → panel DB →
TLS/ACME account keys → site data+DBs → firewall (SSH first) → cron/timers. **Repo passwords must be
recoverable out-of-band** — never store the only copy inside the repo they unlock.

### Resolved design details (B-D7, DB swap, self-backup, D2)

**Firewall deadman — concrete (B-D7).**
- The revert is a **root** `panel-agent fw-revert <snapshot-dir>` subcommand (NOT the unprivileged
  `novapanelctl` — the revert must restore firewall state as root) run by an **independent transient
  systemd unit** the agent creates with `systemd-run --unit=novapanel-fwrevert-<id> --on-active=<N>min`,
  so it fires **even if the agent is down**. The snapshot is a **dir** holding `ufw/*.rules`, an
  `enabled` marker (ufw's prior active/inactive state), `iptables-save`/`ip6tables-save`, and
  `ufw status numbered`. Revert = restore the `.rules` files, **restore the enabled/disabled state**,
  then `ufw reload` (or `disable` if it was inactive).
- `firewall.apply` **refuses with `conflict` if an `fw_pending` row is already armed** (no second
  armed change). It writes `fw_pending` (`id, snapshot_path, revert_unit, deadline`) and arms the timer
  **before** applying, and returns `{pending_id}` to the caller. **`firewall.confirm{pending_id}`**
  stops the transient `.timer` unit + deletes the `fw_pending` row; **`firewall.revert{pending_id}`**
  runs the revert now + deletes the row. Both are MFA-gated and carry `firewall:write` so an
  automation-grant apply can be confirmed. **When the timer fires on its own**, the root
  `panel-agent fw-revert` subcommand restores state and publishes a terminal journal
  outcome. The daemon alone reconciles and deletes the `fw_pending` row after
  durable DB/audit commit. The helper never opens SQLite. The agent
  turns that marker into an audit event on next start (a fired revert is never silently unlogged, and
  never leaves a stale armed row that would `conflict` future applies).
- **Confirmation is a fresh client round-trip, not an agent self-probe** (the agent cannot prove
  external reachability from inside the box, and an existing keep-alive connection survives UFW's
  ESTABLISHED rule so it would prove nothing). `firewall.apply` returns a one-time **confirm nonce — this *is* the `pending_id`** (an opaque one-shot
  token = `fw_pending.id`, consumed on confirm/revert; **no separate nonce field or column**, and
  `firewall.confirm`/`revert` are **synchronous** per 01, not jobs); the panel serves the confirm
  UI/endpoint with `Cache-Control: no-store`
  and `Connection: close`, so the client must open a **new request** to submit `firewall.confirm`. A
  successful fresh confirm within N minutes (default **`Cfg.firewall_window_min`**, 15) is sufficient
  evidence the panel is still
  reachable (the actual safety goal); if the change broke access, no confirm arrives and the transient
  unit reverts. We do not claim to verify a specific new *TCP* connection — only that a fresh
  authenticated request completed. The **never-removable SSH
  rule** (port from **`Cfg.ssh_port`**, source any unless the admin pins one) is re-asserted
  `prepend`/`insert 1` on every apply. Any `firewall:write` caller (admin+, or an automation-grant
  token) may `firewall.confirm`/`revert` the **single** armed pending — `firewall.apply` returns
  `conflict` if one is already armed, so there is never ambiguity about which; an unknown or expired
  `pending_id` → `not_found`; every confirm/revert is audited with its actor.

**Approved confined journal foundation, superseding direct-DB helper ownership.**
The journal is the recovery authority. Only the daemon writes SQLite; `fw_pending`
is a projection. The independent helper never opens the DB or bypasses its lifetime
flock. The earlier row-deletion wording means daemon reconciliation, not helper SQL.

- A preprovisioned private 0700 owner-controlled root contains one stable 0600
  lock inode and one bounded version-1 state file. All components open no-follow;
  writable/foreign ancestors and nonregular, linked or incorrectly owned files
  fail closed. Root/agent UID is trusted and must not mutate paths concurrently.
- State binds the existing pending ID, exact timer unit, deadline, prior enabled
  state and SHA-256 of an independently held immutable snapshot. Journal JSON
  contains no snapshot bytes, rule comments, credentials or arbitrary paths.
  Snapshot capture/verification/restoration remains a separately reviewed adapter.
- Every decision locks the stable inode and rereads state. Publication uses an
  exclusive temporary file, file fsync, atomic rename and directory fsync.
  Confirmation first publishes `confirming`; a failed preparation sync or crash
  leaves this recoverable exactly like `armed`, never terminal confirmation.
  After preparation succeeds, a second publication renames `confirmed`. This
  rename is the irrevocable logical commit, not proof of directory durability.
  The following successful directory fsync proves durability before timer stop.
  A final-sync or stop failure returns `ErrConfirmationCommitted` together with
  its cause. Callers must not treat it as uncommitted or roll it back. A reader
  seeing `confirmed` must successfully fsync the directory under the same lock
  before suppressing fire or reconciling; on failure it returns an error without
  restoration, timer cancellation or DB acknowledgement. Recovery may finish a
  visible commit's durability; after power loss it follows whichever complete
  state survived. No claim is made that a failed fsync undoes a rename.
  Cancellation is checked immediately before publication rename. Cancellation
  before logical commit leaves `armed`/`confirming` recoverable; after commit it
  does not erase the outcome or prevent timer completion. Exact-deadline confirm
  returns `not_found`. Recovery first publishes `reverting`, then invokes
  idempotent rules-plus-enabled restoration with a durable ID-bound audit marker,
  then publishes `reverted`. Interrupted reverting replays the same ID.
- Terminal state remains until daemon-only DB/audit reconciliation commits and a
  durable acknowledgement is published. Failed reconciliation retries by ID.
  This foundation deliberately refuses all further arms, including terminal reuse;
  reviewed archival/retention and new-apply admission are not implemented.
- Scratch subprocess kills and fixed-clock races prove orchestration on a real
  filesystem only. Boot scheduling, power-loss, daemon cgroup isolation, real
  snapshot custody and disposable-VM UFW/systemd acceptance remain required.
  No production adapter, CLI, RPC wiring, migration or live apply is enabled.

**Firewall confirmation crash-safety gate.** SQLite and systemd do not share a
transaction. Stopping the timer inside a pending-row transaction can leave an
unconfirmed row without a timer if deletion or commit fails, or the process dies.
Until a durable confirmation/reconciliation protocol and independent deadman DB
ownership are implemented and crash-tested, confirmation must return `unsupported`
for a live pending ID **before cancelling anything**. Unknown/expired IDs retain
`not_found`; live apply remains unsupported. A rollback or a best-effort re-arm
alone is not process-death safety. Restore is idempotent by pending ID and must
restore rules plus enabled state and durably publish its audit marker before row
consumption. A failed deletion/commit retains the same ID for replay, never a new
nonce. No new RPC fields or schema are introduced by this gate.

**MariaDB restore swap (no RENAME DATABASE).** Steps: (1) take a **pre-restore safety dump** of the
live DB to the panel repo first (so the current state is always recoverable); (2) create a **scratch
DB** `<db>__r<shortts>` (name bounded to MariaDB's 64-char limit — truncate `<db>` and keep a short
ts); (3) import the backup dump into scratch; (4) **sanity check** = import exit 0 **and** the scratch
DB's table count ≥ 1 and matches the dump's expected set; (5) put the site in **maintenance** (Phase-3
`maintenance` flag) so readers don't see a mixed state; (6) swap **table-by-table** with atomic
`RENAME TABLE live.t TO <db>__old.t, scratch.t TO live.t` (per-statement atomic); **tables in live but
absent from the dump are dropped** (the result equals the backup, not a hybrid); **views/routines/
events** are dropped-and-recreated from the dump's DDL (not RENAME, which they don't support), and a
table **with triggers** is handled by dropping its triggers before the cross-DB rename then recreating
them; (7) drop `<db>__old` and scratch; (8) clear maintenance. A crash mid-swap leaves `<db>__old` +
the pre-restore dump; on restart the agent reports the interrupted restore (effectful, `interrupted`)
for an operator to complete — it does not auto-reswap.

**Self-backup chicken-and-egg (resolves B20).** `panel.backup_config` backs up **`panel.db` +
`/etc/novapanel/agent.toml` + `seal.key` + the ACME account key** to a **dedicated panel repo**. The
circularity (DB holds sealed repo creds; `seal.key` unseals them) is broken by: the panel repo's
password is generated and **shown once to the operator at setup, stored out-of-band** (never inside
any restic repo). Restore order: install binary → restore agent.toml + **seal.key** → restic-restore
the panel repo using the out-of-band password → restore panel.db → ACME keys → per-site data.
- **Panel repo setup (decided).** The dedicated panel repo is created via **`repo.create{role:'panel'}`**
  (03) — **exactly one** panel repo allowed (`conflict` if one exists); `repo.list` exposes `role`
  (02 `repos.role`). Its generated password rides the show-once claim **and** must be stored out-of-band
  (it is the recovery key — never inside any restic repo).
- **Consistent `panel.db` snapshot (decided).** The self-backup takes a **SQLite online-backup /
  `VACUUM INTO` snapshot** of `panel.db` (WAL-consistent), **never a raw main-file copy** (which omits
  uncheckpointed WAL contents), then backs up that snapshot file.

**D2 update protocol (folded in so this phase stands alone).** `update.check` fetches a channel
**`manifest.json` + `manifest.json.sig`**; the agent **verifies the ed25519 signature with a public
key compiled into the binary BEFORE parsing** (a bad signature → `invalid_argument`), refuses a `version` **≤ installed** (anti-rollback → `precondition_failed`),
and checks each artifact's sha256 + size against the signed manifest. `update.apply` (owner, MFA,
effectful) downloads the artifact, **unpacks defensively** (tar-slip: reject abs/`..`/non-regular/
outside-top), swaps the binary, and restarts via systemd; a failed post-restart health check rolls
back to the prior binary. A `security_fix` marker flags the update urgent; "notify" is a persistent
dashboard banner. (Channels + approval-gated CI signing remain Phase-4-of-releases scope, per the
plan; v1 ships single-manifest verify + anti-rollback.)

**D2 pinned contract (decided — release-security).**
- **Signed bytes + sig format:** the signature covers the **exact bytes of `manifest.json`** as fetched
  (no JSON re-canonicalization). `manifest.json.sig` is the 64-byte Ed25519 signature **base64-encoded**
  (RFC 4648, padded; trim surrounding whitespace before decode). Verify `ed25519.Verify(pub,
  manifestBytes, sig)` **before** any JSON parse; failure → `invalid_argument`.
- **Trust anchor:** the 32-byte Ed25519 **public key is injected at release-build time** (ldflags `-X`
  or a release-pipeline-generated file in `internal/update`) — **never a production literal committed to
  source**. An **unset key fails closed**: a dev/CI build with no injected key **refuses all updates**
  (`unsupported`), so an unsigned build can't be hijacked by a forged update. The updater **never**
  accepts a key from the manifest or any downloaded content. Rotation ships a **new binary** (signed by
  the current key) carrying the next key — no in-band key swap. Tests inject a test key through the same
  seam; the **production key comes from the release signer's key custody (project owner)**, not the
  source tree.
- **Manifest schema:** `{ "version":"<semver>", "security_fix"?:bool, "notes"?:string, "artifacts":[ {
  "os","arch","filename","sha256":"<hex>","size":<bytes>,"url":"<https>" } ] }`. The agent selects the
  artifact matching `runtime.GOOS`/`GOARCH` (no match → `unsupported`); integrity comes from the
  **signed** `sha256`+`size` (checked before unpack), never the `url`. This release manifest is distinct
  from the `update.check` RPC reply (03).
- **Version grammar + anti-rollback:** **semver** precedence; accept only a `manifest.version`
  **strictly greater** than installed (`==` and `<` → `precondition_failed`). The stable channel refuses
  prereleases. An installed `dev`/unparseable version sorts **below every release** (dev may update), but
  the manifest version must be a valid final release.
- **Apply + recovery:** the artifact is a tarball of the three binaries (`panel-agent`, `panel-web`,
  `novapanelctl`). Install to a **versioned dir** (`/opt/novapanel/<version>/`), verify sha256+size+sig,
  **unpack defensively** (tar-slip guard), **atomic-swap the `current` symlink** the units point at, and
  restart via systemd; **retain the prior version dir**. A **post-restart health check** (agent answers
  `ping` within `deadline_default_ms`) gates success; failure **rolls the symlink back to the prior
  (local, known-good) version** and restarts — rollback uses the retained **local** artifact, **never** a
  downloaded older release, so anti-rollback holds. A crash mid-swap leaves the job `interrupted`
  (effectful); on restart the agent reconciles the symlink/version and completes or rolls back.

## External refs (gathered — verified)
- restic env vars, argv, `--stdin-from-command` failure-awareness, exit codes (3=failure), default
  `check` does NOT read pack data (`--read-data`/`--read-data-subset=n/t` do), `unlock` stale-only vs
  `--remove-all`, B2→S3-compat recommendation. The `--stdin` "will not notice failures" warning is
  restic's own.
- UFW: `enable`/reset flushes chains and can drop ssh → SSH allow must pre-exist; `--dry-run`,
  `--force` (no TTY), `prepend`/`insert`, delete-by-number from `status numbered`. Deadman pattern
  (schedule revert before the risky change) is the standard anti-lockout; `systemd-run --on-active`
  is the atd-free mechanism.
- Backup freshness monitoring is itself a deadman: alert if newest `snapshots --json` per repo is
  older than the interval (e.g. >26 h for daily) — a silently-deleted backup cron must be noticed.

## Verification (the gate)
```
1.  Backup: per-site path backup succeeds; a DB backup via --stdin-from-command FAILS the snapshot
      when mysqldump is made to exit non-zero (no truncated-but-"ok" snapshot). Exit code 3 → failure.
2.  Verify: `check` passes; a rolling `--read-data-subset=n/7` schedule covers the repo in a week; a
      restore drill restores to a scratch dir and compares.
3.  Restore: requires a typed confirm; takes a pre-restore snapshot; a DB restore goes to a scratch DB
      first then swaps (a forced-bad restore leaves the live DB intact via the pre-snapshot).
4.  Firewall: apply arms a timed revert BEFORE applying; the SSH allow is prepended and present after;
      a simulated "no confirm" fires the revert and restores access; `ufw --force reset`/default-deny
      without an SSH rule is impossible via the API. A fresh-request confirm (no-store nonce) gates it.
5.  Repo lock: a stale lock (exit 11) → unlock(stale)+retry once; `--remove-all` is never issued blindly.
6.  Self-backup: backup-config captures DB+agent config+ACME keys; a documented restore order; repo
      password is not the only-copy-inside-its-own-repo.
7.  Updates (D2): a manifest with a bad signature or an older version is REFUSED before parse/apply;
      a good one shows notify + one-click apply. All **mutating** verbs are jobs; **all** verbs are audited
      (reads like `backup.list`/`firewall.status`/`update.check` are sync, per the 01 table).
```

## Definition of Done
- [ ] Restic backups (path + failure-aware DB stream), secrets never on argv; exit-code 3 = failure.
- [ ] Verify = structural + rolling read-data-subset + restore drill (not structural check alone).
- [ ] Restore: typed confirm + pre-restore snapshot + scratch-DB-then-swap; never DROP+restore in place.
- [ ] UFW safe-apply: dry-run → armed timed revert → SSH-first apply → fresh-request confirm (nonce) → cancel/auto-revert.
- [ ] Never-removable SSH rule re-asserted on every apply.
- [ ] Panel self-backup (DB + agent config + ACME keys) with a restore order; out-of-band repo password.
- [ ] Signed + anti-rollback update check with notify + one-click (D2).

## Guardrails (this phase)
- **Never apply a firewall change without an armed timed revert and a fresh-request confirm.** The SSH
  allow is a pinned, never-removable, prepended invariant.
- **Treat restic exit code 3 as failure;** never let a `--stdin` dumper failure pass as a good backup.
- **Never restore over a live DB without a pre-restore snapshot** (scratch-DB-then-swap).
- **Repo passwords live out-of-band**, never only inside the repo they unlock.
- **Verify by reading back**, not by trusting a structural `check`.
