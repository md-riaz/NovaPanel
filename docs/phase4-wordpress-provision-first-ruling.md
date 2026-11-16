# Phase 4 Step 3 — provision-first WordPress install (head ruling)

Status: ruling, 2026-11-16. Amends the `site.create type=wordpress` shape in `docs/spec/03-verb-shapes.md` and the WordPress-install build order in `docs/spec/phases/phase-4.md`. Authoritative for Step 3 implementation.

## Why this ruling exists

The durable foundation (#87) seals the WordPress install operation's reservation at admission. `store.prepareWordPress` validates that reservation through `validWordPressIdentity` → `wordpress.BuildInstall`, which requires a nonzero, non-sentinel account `UID`/`GID` (`internal/wordpress/validation.go:33`) and verified `ArtifactEvidence` (`wordpress.ValidateArtifacts`: matching SHA-256 pins plus `SignatureVerified`). For a brand-new site neither exists at admission: the OS account is not created yet, so there is no UID, and no artifacts are staged or verified, so there is no evidence. A single-call `site.create type=wordpress` therefore cannot seal a valid reservation. This blocked four implementation attempts. The resolution is to obtain the UID and the evidence before the install operation is admitted.

## The two-phase flow

### Phase 1 — `site.create{type:'wordpress', wordpress:{title,admin_username,admin_email}, primary_domain, name?}`

Provisions the site exactly as a `php` site does today (Step 2, merged): create the OS account and FPM pool, which allocates the numeric UID/GID through `useradd`, and persist the site row with `type='wordpress'` and an install state of `pending`. Validate the `wordpress` object shape here (already committed: `138b889`) and persist the administrator identity (title, username, email) with the site so Phase 2 does not re-accept it from the client.

Result: `{site_id}`. No F19 claim is produced in Phase 1 (amends `03-verb-shapes.md`, which previously folded the claim into `site.create`). A `wordpress` site in `pending` install state is a provisioned account with no WordPress files.

### Phase 2 — `wp.install{site_id}`

Admits and runs the WordPress install operation against the already-provisioned account. Steps:

1. Resolve the provisioned account's real UID/GID (from the account, the same lookup `php` uses) and the persisted administrator identity. Refuse with `precondition_failed` if the site is not a `wordpress` site in `pending` install state, or if the account is missing.
2. Obtain verified `ArtifactEvidence` by staging and verifying the pinned artifacts (`wordpress.VerifyStaged`). The staging source is contract-selected:
   - Lab/offline builds (`wordpress_offline_lab`): the pinned fixtures via `internal/wordpress/offline` (`Resolve`), so Phase 2 admission and the full checkpoint machine are buildable and testable now with zero network.
   - Production: a download-then-verify staging step. This production staging path is deferred to the privileged-VM finish (#90); until it exists, `wp.install` in a production build returns `unsupported` for the staging step, never a fabricated evidence struct.
3. Generate and seal the credentials (`jobs.GenerateWordPressCredentials`: DB password, admin password, eight local CSPRNG salts). Build `wordpress.InstallInput` with the resolved UID/GID, the reserved DB/user IDs, site name, primary domain, locale, and administrator, plus the verified `ArtifactEvidence`, and admit `jobs.EnqueueWordPress`.
4. The existing checkpoint machine drives `prepared → db_applied → config_published → done`: DB create via the frozen `database.ManagedCreate`; `CreateWordPressConfig` publishes the 0400 site-owned `wp-config.php` through config custody; the site-user WP-CLI steps (`wordpress.BuildInstall` plan) run the actual install.
5. The live WP-CLI execution and the protected `wp-config` publication require the deferred operation-scoped lifetime handle and stay gated/dormant exactly as in PR #139; admission, sealing, DB create, config composition, and the F19 claim are live and buildable now.
6. Result `{site_id}`; one F19 claim with `{database:{db_id,name,username,host,password}, wordpress:{admin_username,admin_email,admin_password}}` (`wordPressClaim`). No secret in result/events/audit/plaintext.

## What is unchanged

The sealed-reservation, claim, recovery, conflict, and locking machinery (`EnqueueWordPress`, `prepareWordPress`, `OpenWordPressCredentials`, `wordPressClaim`, the checkpoints, `Operation.Keys()`) is reused as-is. The reservation still requires UID + evidence at admission; this ruling only guarantees both exist before admission, so no crash-safety machinery changes. The MariaDB lifecycle (Step 1) and the php-site provisioning (Step 2) are frozen and reused.

## rotate↔wp-config, destructive ops

`database.rotate_password` for a WordPress-backed DB binds the `wp-config` rewrite into the same job (sealed-password reuse, atomic temp+rename, not-done-until-both, roll-forward); the existing-site protected write is gated behind the deferred lifetime handle (existing-site mutation deferred per `docs/phase4-config-isolation-lifetime-contract.md`), never an unguarded mutation, never silently waived. Destructive WordPress ops (update/clone/staging) are registered to return `unsupported` explicitly until Phase 6.

## Build order

1. Phase 1 `site.create type=wordpress` provisioning (reuse the php path) and the `pending` install state.
2. `wp.install{site_id}` Phase 2: lab artifact staging/verify, reservation build with resolved UID+evidence, `EnqueueWordPress`, checkpoint drive with live WP-CLI gated.
3. rotate↔wp-config leg (gated existing-site write), destructive-op guard.
4. Tests with the fake runner and the offline fixtures closing Verification rows 5-6, plus checkpoint recovery and the rotate two-store test. Production download-staging and live WP-CLI execution deferred to #90.
