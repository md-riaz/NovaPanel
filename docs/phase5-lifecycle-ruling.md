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

Resolution is enforced in two places, defence in depth:

- The parent verb resolves `realpath := EvalSymlinks(Clean(join(root, path)))` for existing targets
  (or the parent directory for a not-yet-existing write/mkdir/move-dest/extract target), and asserts
  `HasPrefix(realpath, root+"/")` or `realpath == root` before it invokes the helper.
- The dropped helper independently re-validates the same prefix rule and opens the final component
  with `O_NOFOLLOW`. A pre-planted symlink at a write target is refused with `invalid_argument`, never
  followed or blindly replaced.

Archive entries (`files.extract`) run through `validate.ArchiveEntry` (F9/F15): reject absolute
paths, `..`, backslashes, null, symlink/hardlink/device entries, and enforce a per-archive entry
count and decompressed-size cap. `files.write` is temp plus atomic rename; `files.chmod` accepts only
`{0644,0640,0600,0755,0750,0700}`.

## 3. `site.delete` teardown order (authoritative)

The current sequence in `internal/verbs/site_delete.go` is authoritative and unchanged through the
point of DB and config teardown:

1. Preflight: no unresolved TLS intents (`:76-82`); load and validate the operation journal binding
   (`:83-100`).
2. Acquire the site lock then the OS-user lock (`site:<id>` then `osuser:<name>`, `:131-137`).
3. Enumerate attached databases; `precondition_failed` unless `purge_databases=true` (`:151-172`).
4. Checkpoint `prepared -> teardown_started` (`:191`) to mark the point of irreversible effects.
5. If `purge_databases`: drop DB users then the database through the managed-object journal
   (`managed.go:49-59`, `:154-171`).
6. Set site status `deleting` (`:187-188`).
7. Remove nginx (symlink, `.conf`/`.conf.lkg`/`.conf.tmp`, test, reload; `nginx/remove.go:62-70`).
8. Remove TLS certificates from disk and clear the `sites_certificates` rows (`:197-204`).
9. Disable the FPM pool (recover or apply-committed), and if `purge_files` record the durable
   `site_pool_removed` step (`:212-230`).

Phase 5 adds a teardown group that must run after step 9 (FPM pool disabled) and before the OS-user
retain-or-remove decision (steps 10 onward). These resources are root-owned or system-level, so home
deletion does not reclaim them; they must be torn down explicitly on every delete, independent of
`purge_files`, or a deleted site leaves live timers, a live sftp rule, and a sealed secret behind:

- Cron: for each `cron_jobs` row of the site, `systemctl disable --now` the timer and service, remove
  both unit files from `/etc/systemd/system`, `daemon-reload`. Ensure-state: a missing unit is not an
  error. The `cron_jobs` rows cascade on the site row delete (`ON DELETE CASCADE`), but the unit files
  do not, so they are removed here.
- SFTP-only: remove the managed sshd drop-in `sshd_config.d/novapanel-<site>.conf` if present,
  `sshd -t`, reload sshd. Ensure-state.
- Deploy: remove the sealed deploy key secret `deploy:<site_id>` and any per-site `GIT_SSH_COMMAND`
  identity file. `deploys` rows cascade on the site row delete.

Each is idempotent ensure-state over an agent-owned name, so replay after a partial delete completes
the teardown and never adopts foreign state. Then the existing tail runs: retain the OS user (gecos
`novapanel-retained=<ULID>`, `:245`) when not purging files, or on `purge_files` call
`platform.RemoveSite`, which today verifies ownership and returns `precondition_failed` for manual
UID-owned file cleanup rather than running `userdel` (`site_remove.go:49-62`). That retain-or-manual
posture is unchanged by this ruling; the Phase 5 group above runs before it regardless.

## 4. `cron.*` is systemd timers, ensure-state, bootstrap-window safe

Per-site scheduled jobs are systemd timer plus service units, never root crontab and never
`@reboot`. Names are agent-owned: `novapanel-cron-<site>-<id>.service` and `.timer` in
`/etc/systemd/system`, matching the `cron_jobs.unit_name` column (`02-data-model.md`).

- The `.service` runs the validated fixed argv with `User=<site-user>`, `Group=<site-group>`, and
  `WorkingDirectory=` the docroot, so systemd performs the privilege drop and there is no shell. The
  argv is validated (no shell metacharacters; absolute program path or a resolved one); `schedule` is
  validated as a systemd `OnCalendar` expression (reject free-form; a small alias set like
  `hourly`/`daily`/`weekly` maps to canonical `OnCalendar`).
- Ensure-state (Rule B bootstrap window): writing `.service`, writing `.timer`, `daemon-reload`,
  enable, and start is non-transactional. `cron.create` and `cron.enable` re-render both files,
  `daemon-reload`, and `enable --now` idempotently; they complete their own partial install (a
  `.service` with no `.timer`, or an enabled-but-not-started timer) rather than keying on the presence
  of one file. The `<site>-<id>` name is agent-owned, so completing a partial install never adopts a
  foreign unit. `cron.disable` and `cron.delete` are the ensure-state inverse: `disable --now`, and for
  delete remove both files and `daemon-reload`.
- F8 validate and revert: keep a `.lkg` pair per unit. Validate the rendered pair with
  `systemd-analyze verify` before `daemon-reload`; on validate, enable, or start failure, restore the
  `.lkg` pair (or remove the pair when this was a first create) and `daemon-reload`, and return a coded
  `agent_error`. Write the `cron_jobs` row only after enable and start succeed, so the DB never claims
  a job that is not actually scheduled.

## 5. `sshkey` `key_id` resolution: content-addressed fingerprint

SSH public keys live only in the site user's `authorized_keys` on disk; there is no keys table
(`02-data-model.md`). So `key_id` is derived, not stored: it is the OpenSSH SHA256 fingerprint of the
parsed public-key blob (the `SHA256:<base64>` form). `sshkey.list` returns
`{id: fingerprint, comment, type, fingerprint}` for each parsed line.

`sshkey.remove{key_id}` resolves by reading `authorized_keys`, parsing each line, computing its
fingerprint, and removing the line whose fingerprint equals `key_id`; no match is `not_found`. A
content-addressed id is stable across restarts, needs no table, and is collision-safe: a duplicate key
has the same id, consistent with `sshkey.add` idempotency (adding an existing key is a no-op returning
the same `key_id`, never a second line).

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
edited) containing a `Match User <site-user>` block with `ForceCommand internal-sftp`,
`AllowTcpForwarding no`, `X11Forwarding no`, and `PermitTTY no` when enabled; `enabled:false` removes
the drop-in. It follows the F8 pattern already used by nginx (`internal/render/nginx/apply.go:62-187`)
and FPM (`internal/php/pool_apply.go:272-411`): keep a `.lkg`, write temp, atomic rename, validate with
`sshd -t`, reload sshd, and on validate or reload failure restore the `.lkg` and reload again, returning
a coded error. The drop-in is removed by the `site.delete` teardown group (section 3).

## 8. Build order

One slice per PR, each a fresh branch off `main`, each through the root-container gate and an
independent adversarial review before merge, no `Co-Authored-By` trailer:

1. `sshkey.add` / `sshkey.list` / `sshkey.remove`: smallest slice, no new system infrastructure beyond
   the `authorized_keys` read-modify-write, the fingerprint id, and the site lock.
2. `files.*` read group (`list`, `read`), then the write group (`write`, `mkdir`, `move`, `delete`,
   `chmod`), then the slow group (`upload`, `archive`, `extract`). This builds and proves the `__fileop`
   dropped-helper path once, then extends it.
3. `deploy.configure` / `deploy.status` / `deploy.history`, then the slow `deploy.run` / `deploy.rollback`
   (pinned SHA, shallow fetch, no repo hooks).
4. `cron.*`: the systemd timer renderer plus `create` / `list` / `enable` / `disable` / `delete`.
5. `sshkey.sftp_only` (the sshd renderer) and the `site.delete` teardown wiring for cron, deploy, and
   the sftp drop-in (section 3), landed together since they share the sshd and cron removal paths.

Slices that touch a real uid, systemd, or sshd carry lab-tagged privileged tests run in the root
container; the synchronous path-safety and validation cases stay untagged.
