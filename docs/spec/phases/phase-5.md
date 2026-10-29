# Phase 5 — Daily controls (files, Git deploy, cron, logs, SSH)

## Goal
The everyday operations an owner does on a live site: browse/edit files safely, deploy from Git,
schedule jobs, read logs, and manage SSH keys — each scoped to a site and validated at the sink.

## Depends on
Phases 1–4 (jobs, auth, sites+users, per-site Unix user & home). Reads: `00-foundations.md`
F6, F7, F9, F10, F11.

## Deliverables
1. **Scoped file API** (`files.*`): `files.list`, `files.read` (chunked, size-capped), `files.write`
   (temp+atomic rename), `files.mkdir`, `files.move`, `files.delete`, `files.chmod` (bounded modes),
   `files.upload`, `files.archive`/`files.extract` (zip/tar) — **every path resolved inside the
   site's project root** (F9 path rule), symlinks that cross the boundary rejected, archive entries
   checked for traversal/absolute/`..` before extract (tar-slip/zip-slip). Ops run **as the site
   user** (drop privileges), never as root. A read-only safe editor rides `files.read`/`files.write`.
2. **Git deploy** (`deploy.*` / `app.*`): `deploy.configure` (repo URL, branch, project dir, generate
   a per-site **deploy key**), `deploy.pull` (slow job: fetch + checkout a **pinned SHA**, fixed
   argv, run as the site user), `deploy.status`, `deploy.history`, `deploy.rollback` (checkout a prior
   recorded SHA). URL forms limited to `https`/`ssh` (F9); no `file://`/`ext::`; no arbitrary
   post-checkout hooks executed by the agent.
3. **Cron / scheduled jobs** (`cron.*`): per-site scheduled commands realized as **systemd timers +
   units** (or a per-site crontab owned by the site user) — **never raw root cron**. The command is
   run as the site user with a validated argv/working dir; schedule validated; list/enable/disable/
   delete. No `@reboot` root escalation.
4. **Log viewer** (`log.*` / bounded): tail the last N lines (capped) of a site's access/error/PHP
   logs and whitelisted system units (`journalctl --unit` with fixed argv), read-only, size-bounded,
   never arbitrary file paths outside the allowlist.
5. **SSH key management** (`sshkey.*`): add/list/remove **public keys only** to a site user's
   `authorized_keys` (validated key format), SFTP-only toggle per site. **No password SSH**, no
   writing private keys, no shell-account creation.

## Blueprint
- **`internal/verbs/files.go`** — every verb takes `{site, path, ...}`; resolve
  `realpath := EvalSymlinks(Clean(join(root, path)))` and assert `HasPrefix(realpath, root+"/")`
  **before** any op; open with `O_NOFOLLOW` on the final component; do the op via a child that has
  dropped to the site uid/gid (or a `setuid`-less helper that `seteuid`s — head to pick the mechanism
  in the blueprint; default: the agent forks a worker that `setgid`+`setuid`+`setgroups` to the site
  user, then does the fs op). Archive extract streams entries through the F9 archive check.
- **`internal/verbs/deploy.go`** — `deploy.pull` is a **slow** verb (objectKey `site:<name>`): argv
  `git -C <projdir> fetch --depth 50 origin <branch>` then `git -C <projdir> checkout --detach <sha>`;
  record the SHA + timestamp in a `deploys` table; deploy key stored sealed (F12), written to a
  per-site `GIT_SSH_COMMAND` identity file `0600` owned by the site user at use, removed after.
- **`internal/verbs/cron.go`** — render a `novapanel-cron-<site>-<id>.{service,timer}` (or site
  crontab line); validate `OnCalendar`/schedule; `ExecStart` is a fixed argv with the site user;
  `daemon-reload` + enable/start via `internal/exec`.
- **`internal/verbs/sshkey.go`** — parse/validate the public key (`ssh-ed25519`/`ssh-rsa` with a
  sane length), append to the site user's `~/.ssh/authorized_keys` (`0600`, owned by site user,
  `O_NOFOLLOW` parent), idempotent (no duplicate keys).
- **`internal/verbs/log.go`** — allowlist map `{name → (path | journalctl unit)}`; tail with a line
  cap; never accept a raw path.

### Rooting, exclusions, deploy specifics (resolves B-D10, B13, B11)
- **The file API is rooted at the site's `docroot` (`/srv/sites/<site>/public`), NOT the home.** This
  keeps `~/.ssh/authorized_keys`, cron units, and deploy config **out of reach** of `files.*`, so the
  only path to SSH keys is the MFA-gated `sshkey.*` verb (closes the authz-bypass hole). If a site
  legitimately needs files above docroot, that is a separate, explicitly-scoped verb — not a wider
  file root.
- **Path resolution for not-yet-existing targets** (write/mkdir/move-dest/extract): resolve the
  **parent** with `EvalSymlinks`, assert it is inside the root, then append the (validated) base name;
  `files.list{path:""}` on the root itself is allowed (`realpath == root` passes). `O_NOFOLLOW` on the
  final component; a pre-planted symlink at a write target is **refused** (`invalid_argument`), not
  followed or blindly replaced.
- **Archive entries** use `validate.ArchiveEntry(top, name)` (F15): reject absolute paths, `..`,
  backslashes/null, symlink/hardlink/device entries, and a per-archive entry-count + decompressed-size
  cap (decompression-bomb guard). Same rule as the updater untar.
- **Deploy SHA:** `deploy.run{ref}` resolves `ref` (branch or tag) to a concrete SHA with
  `git ls-remote`/`rev-parse` **before** fetching, records it in `deploys.sha`, fetches enough depth
  to contain it, and checks it out detached — so the deployed commit is pinned even though the input
  was a ref. First deploy does `git init` + `remote add` (or a shallow `clone`) when `<projdir>` is
  not yet a repo. Harden git against repo-supplied hooks/filters: run with
  `-c core.hooksPath=/dev/null -c core.fsmonitor=false` and never execute post-checkout build hooks
  from the agent (a build step, if offered, is a separate explicit verb).

## External refs (gathered by the head)
- Zip-slip / tar-slip: validate each archive entry's cleaned path stays inside the target; reject
  absolute paths, `..`, and symlink entries (Snyk zip-slip advisory; same rule as the Phase-0/updater
  untar). Already codified in F9.
- Privilege-drop in Go: do fs/git ops in a forked child that `setgroups`+`setgid`+`setuid` to the
  site user (CloudPanel/Hestia per-site-user prior art). The agent process itself stays root.
- Git deploy safety: pin to a SHA (never float a branch at deploy time); shallow fetch; never execute
  repo-provided hooks from the agent.

## Verification (the gate)
```
1.  Path safety: files.* on `../`, an absolute path, and a symlink pointing outside the site root all
      → code:"invalid_argument"; fuzz with hostile paths (table test) → no escape.
2.  files.write is atomic (temp+rename), runs as the site user (resulting file owned by site user),
      and O_NOFOLLOW blocks a pre-planted symlink at the target.
3.  Archive extract of a crafted zip/tar with a `../` entry → refused, nothing written outside root.
4.  deploy.pull checks out the pinned SHA as the site user; a `file://`/`ext::` URL → refused;
      deploy.rollback restores a prior SHA; deploy key never world-readable, removed after use.
5.  cron creates a systemd timer running as the site user; no root cron entry is ever written;
      disable/delete removes it; `@reboot`-as-root is impossible via the API.
6.  sshkey.add rejects a malformed key and a private key; authorized_keys is 0600 owned by the site
      user; adding the same key twice is idempotent. No password-auth path exists.
7.  Every mutating verb emits an audit event; all run as the site user, never root (test asserts the
      effective uid of the fs/git child).
```

## Definition of Done
- [ ] File API fully scoped to the site root; traversal/symlink/archive-escape all blocked + tested.
- [ ] File/Git/cron ops execute as the **site user**, not root (uid asserted in tests).
- [ ] Git deploy pins a SHA, limits URL forms, uses a sealed per-site deploy key, supports rollback.
- [ ] Cron realized as per-site systemd timers/crontab; never raw root cron.
- [ ] SSH management is public-key only; SFTP-only toggle works; no password auth.
- [ ] All verbs audited.

## Guardrails (this phase)
- **Never run a site operation as root.** The agent is root only to *drop* to the site user; the op
  runs unprivileged.
- **No raw cron/crontab-as-root, no shell in deploy hooks, no arbitrary log paths.**
- **No password SSH, ever**; no private-key material written by the panel.
- Resolve-then-check every path; a check on the pre-resolve path is not enough (symlink TOCTOU).
