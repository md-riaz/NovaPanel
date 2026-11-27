# Phase 4 production WordPress activation — head ruling

Status: approved. Resolves the cluster that gates production WordPress install (tracked in code as
#90; the open-rulings list lives in the Phase 4 tracker #62). Spec under docs/spec is
authoritative; this ruling reconciles the self-contradictions and names the production mechanism.

This document adds the ruling only. The code changes and the spec amendments in §11 land with the
Slice 1 implementation PR, so docs and code stay consistent in one change.

## 0. Scope decision (the key move)

Production WordPress activation is scoped, in this first stage, to a **fresh site account that
has never executed site code** (a brand-new `site.create type=wordpress`). This is the contract's
own recommended first scope (`phase4-config-isolation-lifetime-contract.md:46`,
`phase4-isolation-runtime-slice.md:3`).

Rationale: the contract's two hardest obligations — terminating/draining a pre-existing untrusted
workload (`:9`) and *proving* filesystem-IO quiescence, where recursive `cgroup.events
populated=0` is explicitly **not** sufficient (`:27`) — exist only because an existing site may
have live or in-flight site-UID writers. A freshly minted account has never run any site code, so
there is no prior workload to drain and nothing to retire: the domain is empty **by
construction**, and `populated=0` between reviewed steps is a valid emptiness check for this case
only.

Therefore:
- `site.create type=wordpress` on a new account: **activated** by this ruling.
- Install/reinstall into an account that has ever served, and live password rotation that must
  rewrite `wp-config.php` on a serving site: **remain `unsupported`/`precondition_failed`** until a
  later slice supplies the drain + IO-quiescence proof and (if chosen) a dedicated per-site FPM
  master. The code must enforce the freshness precondition and refuse otherwise — never silently
  widen scope.

## 1. The operation-scoped held lifetime owner (production `ConfigIsolation`)

Implement the missing production `ConfigIsolation`
(`internal/jobs/phase4_config.go:83,89`) as a single **held** lifetime object that owns the whole
multi-step install, not independent success booleans (`contract:32`). For the fresh-account scope
its obligations are discharged as follows:

1. Acquire the account lifetime and the trusted-root exclusion under the full ordered lock set,
   close managed launcher admissions, and persist the non-serving disposition before any effect
   (`contract:19,25`). Exclude account deletion, UID/GID/home replacement and managed-root
   relocation for the whole protected operation.
2. Open a **root-owned per-site cgroup v2 install domain** via the existing
   `platform.OpenExecutionDomain` (`site_domain.go:44`); it creates nothing and never adopts a
   populated workload. For a fresh account the domain starts empty; assert `populated=0` before
   admitting the first child (a genuine emptiness check here, not a retirement claim).
3. Admit ONLY the reviewed bootstrap children, each entering the domain **at birth** via
   `exec.RunOpts.Cgroup` + `CLONE_INTO_CGROUP` (already implemented at `exec.go:24,76-84`; there
   is no migration fallback). Assert the domain is empty after each step
   (`contract:28`). The reviewed children are exactly the wp-cli steps in §3.
4. Call the config publish (`configcustody.PublishConfig`) once, synchronously, after the
   download and verify steps, under the held admission (`contract:29`). `wp-config.php`
   is the atomic temp+rename sealed-value write as the site user, mode 0400 (§5).
5. On full success, perform the **explicit serving release** (`contract:32`): enable the site's
   FPM serving (§2). Until this release the account is not admitted to serve.
6. On reopen/replay, recover the same journal, account lifetime and domain identity
   (`contract:30`); a fresh account that never released to serving re-runs the reviewed steps
   idempotently (non-empty-docroot refusal in §8 prevents a destructive re-run).

The note at `contract:44` ("RunOpts has no held cgroup descriptor / at-birth placement") is stale;
that seam exists. The gap this ruling closes is the held lifetime above it plus the per-site
install domain, for the fresh-account case.

## 2. FPM / serving

Install runs its reviewed children in the per-site **install domain**, not in FPM. Steady-state
serving stays on the shared `php8.3-fpm` pool (unchanged render/apply, `pool_apply.go`), and is
enabled only as the §1.5 serving-release step, after a successful install. A **dedicated
root-supervised per-site FPM master** (the contract's recommendation at `:42`) is required only to
protect effects on an already-serving site; it is **deferred** with existing-site rotation/reinstall
and is out of scope here. This ruling does not change the shared-pool serving model for existing
sites.

## 3. Artifact acquisition (decision: online `wp core download`)

Production fetches WordPress online with the spec's wp-cli sequence
(`phase-4.md:428-436`), run as the site user inside the install domain:
`wp core download --version=<pinned> --locale=<pinned>` → `wp core verify-checksums` →
compose wp-config in Go and publish through ConfigIsolation custody → `wp core install --prompt=admin_password`.
WP-CLI does not write wp-config.php; custody journals and publishes it atomically as 0400.
- **Never `--insecure`.** A checksum-verify failure fails the job closed (no serving release, no
  partial publish).
- `wp core download` delivers the **tar.gz**, not the ZIP. Amend the artifact trust contract
  (`internal/wordpress/artifacts.go` and its spec note) to pin the **tar.gz** by version + locale +
  sha256, replacing the ZIP pin. The offline lab fixtures keep serving their ZIP by URL for the
  lab build only; the production pin is the tar.gz the wp.org endpoint actually returns.
- Egress for the download is permitted from within the install domain. Pins are reviewed values,
  updated deliberately (same discipline as other pinned deps). The WordPress 7.1.4 en_US tarball at
  `https://downloads.wordpress.org/release/wordpress-7.1.4.tar.gz` has locally computed SHA-256
  `881e53a4681ff2cd911651b11cccdede5ae477ddf0765af5d6a1a8b219a1d835` over the official HTTPS bytes.

## 4. Verb reconciliation (registry wins)

The registry `03-verb-shapes.md:200-212` is authoritative: WordPress provisioning is
`site.create{type:'wordpress', wordpress:{title, admin_username, admin_email}, ...}` → job →
`{site_id}` plus the single combined F19 claim `{database:{db_id,password}, wordpress:{admin_
username, admin_password}}`. There is no public `wp.install` shape in the registry, and
`phase-4.md:659` directs its removal.

Ruling: the **public** verb is `site.create type=wordpress`; it creates the pending site row and
enqueues the WordPress install job (the job may retain the internal name, but it is NOT a
separately dispatchable public verb with a divergent shape). Remove the public `wp.install`
registration and its pre-existing-site argument shape; the install logic moves under the
`site.create` wordpress job. The job publishes exactly the one combined claim above — generated DB
and admin passwords surface once via F19 and never appear in result/events/audit/plaintext.

## 5. wp-config.php mode

Rule **0400**, owned by the site UID (the lab and the later spec sections agree:
`phase-4.md:599,614,627`; `configcustody` writes 0400). Amend the two "440/400" mentions
(`phase-4.md:47,74`) to 0400. Containing dirs stay as configcustody enforces (site dir 0751, final
dir 02750, custody root 0700).

## 6. Provisioning identity

Rule: allocate the site UID/GID from the **reserved NovaPanel site range** and create the account
with an **explicit** `useradd -u <reserved> -g <reserved>` (not an implicit `-r` allocation), so
the UID is known and validated to be non-colliding with `www-data` and system users **before**
creation (resolves the open "useradd before UID validation" question). The account is fresh by
construction (§0); the install job is the first code to run under it.

## 7. Rotate ↔ wp-config leg

The initial install writes `wp-config.php` with the generated DB password in-job (no rotation at
birth), so the two-store coupling is satisfied for new sites. Live password rotation that must
rewrite `wp-config.php` on an already-serving site stays **deferred** with the existing-site
lifetime (§0): `database.rotate_password` on a WordPress-bound DB continues to return `unsupported`
(`database_rotate.go:140`) until that slice lands. The spec's "same job, not done until both legs
succeed" rule (`phase-4.md:441-463`) governs that future slice; it is not silently waived.

## 8. Destructive guard / non-empty docroot

The registry has no destructive WordPress verbs. Install **refuses a non-empty docroot**
(`phase-4.md:46`) with `precondition_failed` rather than overwriting, which also makes replay
safe. Teardown of a WordPress site goes through the existing `site.delete` path, not a WP verb.

## 9. Journal retirement after delete

After `site.delete`, the sealed config-custody journals (`<JobID>.sealed` under the custody root)
for the deleted account may be retired by an authorized teardown step bound to the delete job.
Specify this as a `site.delete` teardown obligation; a no-custody completed journal is retired only
under that authorized path. (Small; may land as its own follow-up slice.)

## 10. Slice plan (after approval)

1. **Slice 1 — production activation (fresh accounts):** the held lifetime owner (§1) + per-site
   install domain; `site.create type=wordpress` online acquisition (§3) with the combined claim
   (§4); wp-config 0400 (§5); provisioning identity (§6); non-empty-docroot refusal (§8). Remove
   public `wp.install`. Spec amendments (§11). Gate: root container (SSH, remote), real uid-drop,
   real cgroup domain, real wp-cli. The gate stays offline (`--network none`): it stands up a
   pinned local mirror that `downloads.wordpress.org` resolves to, so the online `wp core download`
   plus `verify-checksums` codepath runs against a controlled endpoint serving the pinned tar.gz.
   Everything else in the gate stays offline and host-safe.
2. **Slice 2 — existing-site protected effects:** drain + IO-quiescence proof; dedicated per-site
   FPM master (or the ruled shared-domain alternative); activates reinstall and the live rotate↔
   wp-config leg (§7).
3. **Slice 3 — journal retirement (§9)** if not folded into Slice 1.

## 11. Spec amendments bundled with Slice 1 (raise, don't silently edit)

- `phase-4.md`: Step 3 status pending → activated for fresh accounts (scope noted); 440/400 → 0400;
  remove the `wp.install` alias per `:659`.
- `03-verb-shapes.md`: confirm `site.create type=wordpress` combined-claim shape; ensure no public
  `wp.install` entry.
- artifact trust contract: ZIP pin → tar.gz pin (version+locale+sha256).
- Update the code comments that cite "#90" as the lifetime owner to point at this ruling.

## 12. Gate (decided)

Slice 1's gate exercises the real `wp core download` + `verify-checksums` path, but the root
container stays offline (`--network none`). The gate stands up a pinned local mirror that
`downloads.wordpress.org` resolves to (hosts/DNS override inside the container), serving the pinned
tar.gz by the exact URL wp-cli requests. This tests the online codepath against a controlled
endpoint while keeping the gate offline and host-safe. No real egress to wp.org.
