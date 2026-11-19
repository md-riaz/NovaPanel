# Phase 5 lifecycle and concurrency ruling

Status: head ruling. This is the upfront ruling `docs/spec/phases/phase-5.md` owes before
`deploy.*`, `cron.*`, and `sshkey.*` may be built. It also pins the privilege-drop mechanism the
`files.*` API reuses. The phase spec and the canonical registry (`docs/spec/01-rpc-and-verbs.md`,
`docs/spec/03-verb-shapes.md`, `docs/spec/02-data-model.md`) remain authoritative; this doc resolves
the lifecycle and concurrency points they deliberately left open. A genuine conflict with the spec is
a blocker to raise, not something to resolve silently in code.

It decides seven things: the privilege-drop mechanism, path rooting for `files.*`, the `site.delete`
teardown order (including the Phase 5 resources it must now reclaim), `cron.*` as systemd timers,
`sshkey` `key_id` resolution, `sshkey` concurrency, and the `sshkey.sftp_only` sshd render path. It
closes with the build order.

## 1. Privilege drop (F15): one path, the child starts already dropped

Every site-user operation runs in a child process that `syscall.Credential` drops to the site
`uid`/`gid` with an empty supplementary-group set. The agent process itself stays root. This is the
existing F15 path and nothing here weakens it:

- `internal/exec/exec.go:85-87` sets `SysProcAttr.Credential = &syscall.Credential{Uid, Gid, Groups}`
  when `Uid` or `Gid` is non-zero.
- `internal/platform/site_exec.go:103-112` forces `o.Uid, o.Gid = s.uid, s.gid` and `Groups = []`,
  and refuses a caller override, so a site runner can only ever run as its own site user.
- `internal/php/user.go:60-73` resolves the site `uid`/`gid`/home from the Unix account and validates
  the `novapanel-site=<ULID>` gecos marker, so an operation never binds to an unprovisioned or
  foreign account.

Ruling: `deploy.*` and `cron.*` already fit this path because they run external programs (`git`,
and for cron the work runs under systemd, see section 4), so they call `exec.Run` with the site
runner and never touch the filesystem as root.

For `files.*`, the operations are direct filesystem calls, not an external program, and Go cannot
safely `setuid` a running multithreaded process (a bare `setuid` affects only the calling thread).
So `files.*` does not drop privileges inline. Instead the agent re-executes its own binary as an
internal, non-public helper subcommand (for example `novapanel __fileop`) through `exec.Run` with
`RunOpts{Uid, Gid}` set to the site user. The child therefore starts already dropped; it performs the
single, path-validated filesystem op confined to the site root and returns the result on stdout. All
privilege drop stays inside the one audited `exec.Run` path, and the agent never performs the op as
root and then `chown`s (which would open a window where a root-owned file exists at an attacker-chosen
path). The helper is not a registered verb and is not reachable over RPC.

## 2. Path rooting and resolution for `files.*`

The file API is rooted at the site docroot `/srv/sites/<name>/public`, not the home
(`phase-5.md:70`). This keeps `~/.ssh/authorized_keys`, cron units, and deploy config out of reach of
`files.*`, so the only path to SSH keys stays the MFA-gated `sshkey.*` verb.

Resolution must be race-safe, not validate-then-open-by-pathname. A `Clean`+`EvalSymlinks`+prefix
check followed by an `open(path)` is a TOCTOU hole: the site user owns the tree, so it can swap an
intermediate directory for a symlink between the check and the open and escape the docroot (for
example into its own `~/.ssh`). `O_NOFOLLOW` on the final component alone does not close this, because
the escape is through an intermediate component.

So the dropped helper opens the docroot once with `os.OpenRoot(docroot)` and performs every operation
through that anchored `*os.Root` handle (`Root.Open`, `Root.OpenFile`, `Root.Mkdir`, `Root.Remove`,
`Root.Stat`, and the rename/move equivalents). An `os.Root` refuses any path that escapes the root and
refuses to traverse a symlink out of it, evaluated against the kernel handle rather than a string, so
the swap-an-intermediate-directory race cannot escape. This is the established pattern in the repo:
`internal/platform/site_remove.go:26`, `internal/render/nginx/apply.go:76`, and
`internal/php/pool_apply.go:121` all operate through `os.OpenRoot`. The Go 1.27 toolchain has it. The
same anchored handle is used for archive extraction: each entry is created through the `*os.Root`, so
a `../` or symlink entry cannot escape even mid-stream. `files.list{path:""}` on the root itself is
allowed (the root handle opened on `.`).

Archive entries (`files.extract`) run through `validate.ArchiveEntry` (F9/F15): reject absolute
paths, `..`, backslashes, null, symlink/hardlink/device entries, and enforce a per-archive entry
count and decompressed-size cap. `files.write` is temp plus atomic rename; `files.chmod` accepts only
`{0644,0640,0600,0755,0750,0700}`.

## 3. `site.delete` teardown order (authoritative)

The current sequence in `internal/verbs/site_delete.go` is authoritative and unchanged through the
point of DB and config teardown:

1. Preflight: no unresolved TLS intents; load and validate the operation journal binding.
2. Acquire the OS-user lock (`site:<id>` then `osuser:<name>`, `LockSiteOSUser` at `:98`).
3. Enumerate attached databases; `precondition_failed` unless `purge_databases=true`.
4. Checkpoint `prepared -> teardown_started` (`:144-148`) to mark the point of irreversible effects.
5. If `purge_databases`: drop DB users then the database through the managed-object journal
   (`managed.go:49-59`, `:154-171`).
6. Remove nginx (symlink, `.conf`/`.conf.lkg`/`.conf.tmp`, test, reload; `:194`, `nginx/remove.go`).
7. Remove TLS certificates from disk and clear the `sites_certificates` rows.
8. Disable the FPM pool (recover or apply-committed), and if `purge_files` record the durable
   `site_pool_removed` step (`:211-231`).

Phase 5 adds a teardown group that must run after step 8 (FPM pool disabled) and before the OS-user
retain-or-remove decision (`:245` onward). These resources are root-owned or system-level, so home
deletion does not reclaim them, and the DB `ON DELETE CASCADE` only drops the rows, not the on-disk or
daemon state; they must be torn down explicitly on every delete, independent of `purge_files`, or a
deleted site leaves live timers, a live sftp rule, and a sealed secret behind:

- Cron: remove the site's timers both by enumerating `cron_jobs` rows and by globbing
  `novapanel-cron-<site>-*.{service,timer}` in `/etc/systemd/system` (the glob catches a unit whose row
  never committed, see section 4). For each, `systemctl disable --now` then remove both unit files then
  `daemon-reload`. Ensure-state: a missing unit is not an error.
- SFTP-only: remove the managed sshd drop-in `sshd_config.d/novapanel-<site>.conf` if present,
  `sshd -t`, reload sshd. Ensure-state, under the global sshd lock (section 7).
- Deploy: remove the sealed deploy key secret `deploy:<site_id>` and any per-site `GIT_SSH_COMMAND`
  identity file.

Each is idempotent ensure-state over an agent-owned name, so replay after a partial delete completes
the teardown and never adopts foreign state, and the group sits before the `teardown_started ->
db_applied` checkpoint (`:254-255`) so a crash re-enters and re-runs it. Then the existing tail runs:
retain the OS user (gecos `novapanel-retained=<ULID>`, `:245`) when not purging files, or on
`purge_files` call `platform.RemoveSite`, which today verifies ownership and returns
`precondition_failed` for manual UID-owned file cleanup rather than running `userdel`
(`site_remove.go:49-62`). That retain-or-manual posture is unchanged by this ruling; the Phase 5 group
above runs before it regardless.

To make the cron glob reliable, `cron.create` uses write-ahead identity: it allocates the `cron_id`
and writes the `cron_jobs` row as `state=creating` before it renders any unit, then flips the row to
enabled only after enable and start succeed (section 4). A permanently failed or never-finished create
therefore always leaves a discoverable row, and the glob is a second net for a row that truly never
landed.

## 4. `cron.*` is systemd timers, ensure-state, bootstrap-window safe

Per-site scheduled jobs are systemd timer plus service units, never root crontab and never
`@reboot`. Names are agent-owned: `novapanel-cron-<site>-<id>.service` and `.timer` in
`/etc/systemd/system`, matching the `cron_jobs.unit_name` column (`02-data-model.md`).

- The `.service` runs the validated fixed argv with `User=<site-user>`, `Group=<site-group>`, and
  `WorkingDirectory=` the docroot, so systemd performs the privilege drop and there is no shell. The
  argv is validated (no shell metacharacters; absolute program path or a resolved one); `schedule`
  accepts any valid systemd `OnCalendar` expression, validated with `systemd-analyze calendar
  <schedule>` (a non-zero exit is `invalid_argument`). The full `OnCalendar` grammar is the contract
  (`03-verb-shapes.md:141`, `00-foundations.md`); convenience aliases like `hourly`/`daily` are
  accepted because systemd itself accepts them, not as the whole accepted set, so a valid calendar
  expression is never rejected.
- Write-ahead identity: `cron.create` allocates the `cron_id` and writes the `cron_jobs` row as
  `state=creating` before rendering any unit, so a failed or interrupted create is always discoverable
  by `site.delete` (section 3). The row flips to enabled only after enable and start succeed.
- Ensure-state (Rule B bootstrap window): writing `.service`, writing `.timer`, `daemon-reload`,
  enable, and start is non-transactional. `cron.create` and `cron.enable` re-render both files,
  `daemon-reload`, and `enable --now` idempotently; they complete their own partial install (a
  `.service` with no `.timer`, or an enabled-but-not-started timer) rather than keying on the presence
  of one file. The `<site>-<id>` name is agent-owned, so completing a partial install never adopts a
  foreign unit. `cron.disable` and `cron.delete` are the ensure-state inverse: `disable --now`, and for
  delete remove both files and `daemon-reload`.
- F8 validate and revert, including the enabled/active state, not just the files: keep a `.lkg` pair
  per unit. Validate the rendered pair with `systemd-analyze verify` before `daemon-reload`. On any
  validate, enable, or start failure the rollback first runs `disable --now` on both units (so a
  partially-enabled or started timer does not survive), then restores the `.lkg` pair (or removes the
  pair when this was a first create), then `daemon-reload`, then returns a coded `agent_error`.
  Removing the files without first disabling would leave systemd holding an enabled-but-fileless unit,
  so the disable is mandatory. The row stays `state=creating` (not enabled) on failure, so the next
  `cron.create`/`enable` replay ensure-states it forward or `site.delete` reclaims it.

## 5. `sshkey` `key_id` resolution: content-addressed fingerprint

SSH public keys live only in the site user's `authorized_keys` on disk; there is no keys table
(`02-data-model.md`). So `key_id` is derived, not stored. The canonical shape is
`sshkey.remove{key_id}` with no `site_id` (`03-verb-shapes.md:146`), so the id alone must locate both
the file and the lock. A bare fingerprint cannot: the same public key legitimately installed on two
sites would share one id, and remove could not tell which site's `authorized_keys` to edit. Therefore
`key_id` is composite: `<site_id>.<fingerprint>`, where the fingerprint is the OpenSSH SHA256
fingerprint of the parsed public-key blob. `sshkey.add{site_id, public_key}` returns this composite;
`sshkey.list{site_id}` returns `{id: <site_id>.<fingerprint>, comment, type, fingerprint}` per line.

`sshkey.remove{key_id}` splits the `site_id` prefix off the id, takes the `site:<site_id>` lock, reads
that site's `authorized_keys`, and removes the line whose fingerprint equals the id's fingerprint part;
no match is `not_found`, a malformed id is `invalid_argument`. The id is still content-addressed within
a site, so it is stable across restarts, needs no table, and keeps `sshkey.add` idempotent (re-adding
an existing key is a no-op returning the same composite id, never a second line). A malformed line
already in `authorized_keys` is skipped during the parse, never fatal.

## 6. `sshkey` concurrency: serialize and atomic replace

`sshkey.add` and `sshkey.remove` are synchronous verbs, but each is a read-modify-write of one
`authorized_keys` file, so they must serialize on the site keyed lock `site:<site_id>` and must
replace the file by atomic temp plus rename, never an in-place append. Two concurrent edits therefore
cannot lose an update. `add` validates the key format (`ssh-ed25519` or `ssh-rsa` with a sane length),
strips any key options (public key only, no `command=`/`from=` injection), and refuses a private key.
The file stays `0600` owned by the site user with an `O_NOFOLLOW` parent open, and the write runs in
the dropped child (section 1) so the site user owns the result.

## 7. `sshkey.sftp_only`: sshd F8 render, validate, reload, revert

`sshkey.sftp_only{enabled}` is a slow, MFA-gated verb that mutates sshd. It renders a per-site
drop-in `sshd_config.d/novapanel-<site>.conf` (an sshd_config.d include, so the main config is never
edited; Ubuntu 24.04 ships the `Include /etc/ssh/sshd_config.d/*.conf` line by default) containing a
`Match User <site-user>` block scoped to that one account, with `ForceCommand internal-sftp`,
`AllowTcpForwarding no`, `X11Forwarding no`, and `PermitTTY no` when enabled; `enabled:false` removes
the drop-in. The `Match User` names exactly the site user, so it never widens to another account.

Because sshd config is global (one daemon for the host), `sshkey.sftp_only` serializes on a single
global `sshd` lock, not just the site lock, so two concurrent sftp toggles cannot interleave their
validate/reload. It follows the F8 pattern already used by nginx (`internal/render/nginx/apply.go:62-187`)
and FPM (`internal/php/pool_apply.go:272-411`): keep a `.lkg`, write temp, atomic rename, validate with
`sshd -t`, reload sshd, and on validate or reload failure restore the `.lkg` and reload again. If the
revert itself fails, fail closed: return a coded error that names the divergence rather than reporting
success, so an operator reconciles rather than the API silently leaving disk and daemon out of sync.
The drop-in is removed by the `site.delete` teardown group (section 3), under the same global lock.

## 8. Build order

One slice per PR, each a fresh branch off `main`, each through the root-container gate and an
independent adversarial review before merge, no `Co-Authored-By` trailer. Each slice that creates a
persistent resource ships that resource's `site.delete` teardown in the same PR, so no window exists
where a resource can be created but a site deleted between PRs orphans it:

1. The dropped-write privilege primitive plus `sshkey.add` / `sshkey.list` / `sshkey.remove`. This
   slice builds the `exec.Run`-with-site-uid helper (section 1) that both `sshkey.*` and `files.*`
   reuse, because `sshkey` writes `~/.ssh/authorized_keys` as the site user and so needs the
   privilege-dropped write and safe site identification first. It adds the composite id (section 5),
   the `authorized_keys` read-modify-write under the site lock (section 6), and the `authorized_keys`
   reclaim on `site.delete`.
2. `files.*` read group (`list`, `read`) built on the same primitive with the `os.OpenRoot` anchored
   resolution (section 2), then the write group (`write`, `mkdir`, `move`, `delete`, `chmod`), then the
   slow group (`upload`, `archive`, `extract`).
3. `deploy.configure` / `deploy.status` / `deploy.history`, then the slow `deploy.run` /
   `deploy.rollback` (pinned SHA, shallow fetch, no repo hooks). Ships the deploy-secret and identity-
   file teardown on `site.delete` (section 3) in the same slice.
4. `cron.*`: the systemd timer renderer plus `create` / `list` / `enable` / `disable` / `delete`, with
   write-ahead rows and the F8 rollback (section 4). Ships the cron-unit teardown on `site.delete`
   (row-enumeration plus the `novapanel-cron-<site>-*` glob, section 3) in the same slice.
5. `sshkey.sftp_only` (the sshd renderer under the global sshd lock, section 7). Ships the sftp drop-in
   teardown on `site.delete` in the same slice.

Slices that touch a real uid, systemd, or sshd carry lab-tagged privileged tests run in the root
container; the synchronous path-safety and validation cases stay untagged.
