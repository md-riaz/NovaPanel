# Phase 6 — Backups, firewall, status

## Goal
Trustworthy backups (Restic, verified by reading back), safe restore with a pre-restore snapshot,
a **never-lock-yourself-out** firewall, panel self-backup, and status + manual security updates (D2).

## Depends on
Phases 1–4 (jobs, sites, DB). Reads: `00-foundations.md` F6, F7, F10, F11, F12; `vpspanel-plan.md`
§3.5 (firewall), D2.

## Deliverables
1. **Restic backups** (`backup.create/list/restore/verify`, targets local/SFTP/S3/B2): per-site path
   backup + per-DB **stream** backup that is **failure-aware**; secrets via env/`*_FILE`, never argv.
2. **Verify by reading back**, honestly: structural `check` + rolling `check --read-data-subset=n/7`
   + periodic **restore drill**. A clean structural check is necessary but not sufficient.
3. **Restore** with a typed confirmation + **pre-restore safety snapshot**; restore order file → DB →
   full site; restore a DB into a **scratch DB first**, validate, then swap (never DROP+restore in
   place).
4. **UFW safe-apply** (`firewall.status/apply`): **dry-run → arm timed revert (deadman) → apply with
   a never-removable SSH rule → verify a NEW connection → confirm cancels the revert**.
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
2. dry-run:  `ufw --dry-run <changes>`; assert the SSH allow is present and ordering is safe
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
  **before** applying, and returns that `fw_pending.id` to the caller. **`firewall.confirm{id}`** (the
  `fw_pending.id`) stops the transient `.timer` unit + deletes the row; **`firewall.revert{id}`** runs
  the revert now + deletes the row. Both are MFA-gated and carry `firewall:write` so an automation-grant
  apply can be confirmed. When the timer fires on its own, `fw-revert` restores state and leaves a
  marker the agent turns into an audit event on next start (a fired revert is not silently unlogged).
- **"Verify a new connection" is a client round-trip, not an agent self-probe** (the agent cannot
  prove external reachability from inside the box). After apply, the admin's browser/CLI must reach
  the panel again and call `firewall.confirm` within N minutes (default **15**); if the change broke
  their access, the confirm never arrives and the transient unit reverts. The **never-removable SSH
  rule** (port from config, source any unless the admin pins one) is re-asserted `prepend`/`insert 1`
  on every apply.

**MariaDB restore swap (no RENAME DATABASE).** Restore the dump into a **scratch DB**
`<db>__r<shortts>` (bounded to the 47-char name limit — truncate `<db>` if needed), validate (import
exit 0 + a sanity check), take a **pre-restore safety dump** of the live DB, then swap **table-by-
table with atomic `RENAME TABLE`** (`RENAME TABLE live.t TO old.t, scratch.t TO live.t` is atomic per
statement), finally drop the scratch/old DBs. A crash mid-swap leaves the pre-restore dump for manual
recovery.

**Self-backup chicken-and-egg (resolves B20).** `panel.backup_config` backs up **`panel.db` +
`/etc/novapanel/agent.toml` + `seal.key` + the ACME account key** to a **dedicated panel repo**. The
circularity (DB holds sealed repo creds; `seal.key` unseals them) is broken by: the panel repo's
password is generated and **shown once to the operator at setup, stored out-of-band** (never inside
any restic repo). Restore order: install binary → restore agent.toml + **seal.key** → restic-restore
the panel repo using the out-of-band password → restore panel.db → ACME keys → per-site data.

**D2 update protocol (folded in so this phase stands alone).** `update.check` fetches a channel
**`manifest.json` + `manifest.json.sig`**; the agent **verifies the ed25519 signature with a public
key compiled into the binary BEFORE parsing**, refuses a `version` **≤ installed** (anti-rollback),
and checks each artifact's sha256 + size against the signed manifest. `update.apply` (owner, MFA,
effectful) downloads the artifact, **unpacks defensively** (tar-slip: reject abs/`..`/non-regular/
outside-top), swaps the binary, and restarts via systemd; a failed post-restart health check rolls
back to the prior binary. A `security_fix` marker flags the update urgent; "notify" is a persistent
dashboard banner. (Channels + approval-gated CI signing remain Phase-4-of-releases scope, per the
plan; v1 ships single-manifest verify + anti-rollback.)

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
      without an SSH rule is impossible via the API. A NEW connection check gates confirmation.
5.  Repo lock: a stale lock (exit 11) → unlock(stale)+retry once; `--remove-all` is never issued blindly.
6.  Self-backup: backup-config captures DB+agent config+ACME keys; a documented restore order; repo
      password is not the only-copy-inside-its-own-repo.
7.  Updates (D2): a manifest with a bad signature or an older version is REFUSED before parse/apply;
      a good one shows notify + one-click apply. All verbs are jobs + audited.
```

## Definition of Done
- [ ] Restic backups (path + failure-aware DB stream), secrets never on argv; exit-code 3 = failure.
- [ ] Verify = structural + rolling read-data-subset + restore drill (not structural check alone).
- [ ] Restore: typed confirm + pre-restore snapshot + scratch-DB-then-swap; never DROP+restore in place.
- [ ] UFW safe-apply: dry-run → armed timed revert → SSH-first apply → new-connection verify → confirm/cancel.
- [ ] Never-removable SSH rule re-asserted on every apply.
- [ ] Panel self-backup (DB + agent config + ACME keys) with a restore order; out-of-band repo password.
- [ ] Signed + anti-rollback update check with notify + one-click (D2).

## Guardrails (this phase)
- **Never apply a firewall change without an armed timed revert and a new-connection verify.** The SSH
  allow is a pinned, never-removable, prepended invariant.
- **Treat restic exit code 3 as failure;** never let a `--stdin` dumper failure pass as a good backup.
- **Never restore over a live DB without a pre-restore snapshot** (scratch-DB-then-swap).
- **Repo passwords live out-of-band**, never only inside the repo they unlock.
- **Verify by reading back**, not by trusting a structural `check`.
