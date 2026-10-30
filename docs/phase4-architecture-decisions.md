# Phase 4 architecture decisions

# Phase 4 canonical amendment proposal

Status: internally approved canonical decision text under delegated planning authority, based on 82f76cc0ec17dfa8f3fc46a0536e371c5fe9b08f. This scratch packet supersedes the earlier proposal and independent-review corrections where resolved here. The accompanying patch is drafted and checked, not applied or published. Production integration remains gated; the narrowly documented stacked-development alternative needs an explicit parent record.

## Entry and unchanged boundaries

Phase 3 candidate 82f76cc0ec17dfa8f3fc46a0536e371c5fe9b08f is implementation/local-safe acceptance complete subject to published exact-head architecture review and actual remote CI. The unresolved browser create/poll timeout risk is accepted for dependent development under the user's delegation, not dismissed as fixed. Parent approves the documented internal Phase 4 decisions. Final privileged Ubuntu VM checks are explicitly deferred until project-end acceptance and remain unpassed. Standard Phase 4 integration requires reviewed and separately authorized merged Phase 3 plus canonical amendment publication and an explicitly opened slice. No main merge is authorized by this packet. While external review waits, only a parent-recorded stacked-development exception may open isolated pure PHP/DB tests and their minimal implementations on this exact unmerged SHA. Such work is not merged prerequisite completion, canonical publication, deployment or full Phase 4 acceptance.

Keep Ubuntu 24.04, PHP 8.3, MariaDB 10.11, home `0751`, docroot `site:www-data 2750`, non-login site UID and private per-file/subtree modes. A traversable home is not private. Worker UID is the site UID; FPM socket owner/group is `www-data:www-data 0660`; reject `listen.acl_users` and `listen.acl_groups`. Keep compatibility PHP defaults, F15 typed execution and explicit environment, F6/F19 claims, admin socket authentication and application TCP `127.0.0.1`. No installer, host bootstrap, host service mutation, public CA, phpMyAdmin, additional PHP version or destructive WordPress operation.

## Insert in 03: concrete WordPress input and claims

Replace the existing site.create shape with:

```text
site.create{name?, type:'static'|'php'|'wordpress', primary_domain,
            php_version?, wordpress?:{title:string,admin_username:string,admin_email:string}}
  -> job -> {site_id} + claim only for type=wordpress
```

`wordpress` is required exactly when type is wordpress and forbidden otherwise. Version is absent or `8.3` for php/wordpress and forbidden for static. No caller-supplied administrator password, DB name, DB username, download URL or arbitrary CLI argument is accepted. The server generates independent DB and WordPress administrator passwords with crypto/rand, 32 random bytes encoded base64url without padding. Both are sealed before any durable write or external mutation and reused by this operation on restart.

Use these exact validation rules before resource creation: title is nonempty valid UTF-8, at most 200 bytes, has no C0/DEL characters; admin_username matches `^[A-Za-z][A-Za-z0-9_.-]{0,59}$`; admin_email is at most 254 bytes and is a single bare address accepted by net/mail.ParseAddress with Address equal to the input, no display name, CR, LF or NUL. Reject invalid values with invalid_argument. Render title only as typed argv and HTML-escape display. The authoritative domain supplies the installation URL. Do not invent email addresses or external credentials.

WordPress job result remains `{site_id}`. One F19 claim contains exactly:

```json
{"database":{"db_id":"<ULID>","name":"<generated>","username":"<generated>","host":"127.0.0.1","password":"<generated>"},"wordpress":{"admin_username":"<validated input>","admin_email":"<validated input>","admin_password":"<generated>"}}
```

DB create claim is `{password:<generated>}` and result is `{db_id,db_name,db_user}`. DB rotation claim is `{password:<generated>}` and result is `{}`. dbuser.create claim is `{password:<generated>}` and result is `{dbuser_id}`. Preserve the original owner-session/token-plus-claim-token check, publication-anchored TTL checked inside SQL, delete-on-first-read, memory-only browser token and already-consumed-claim completion-audit exception. No credential in result/events/audit/plaintext SQLite or WAL/browser persistence. The protected application wp-config necessarily retains its DB password; show-once governs panel delivery, not application storage.

Separate calls use separate stdin consumers:

```text
/usr/bin/php8.3 /usr/local/lib/novapanel/wp-cli-2.12.0.phar config create ... --prompt=dbpass
/usr/bin/php8.3 /usr/local/lib/novapanel/wp-cli-2.12.0.phar core install ... --admin_user=<validated> --admin_email=<validated> --skip-email --prompt=admin_password
```

The applicable password plus newline is stdin. Do not use --dbpass, --admin_password=value, --admin_pass, --allow-root, a shell, PHP eval, or a password-update workaround. Suppress raw child output from persistent job/audit logs and sanitize returned failures. Prove the stdin contract in the chosen target runtime before installing a site.

## Insert in 03: generated names, primary account and deletion

Replace database.create args with `{site_id?}` and dbuser.create args with `{db_id}`. Remove raw optional name/username rather than silently accepting them. Generate the DB name as `d_` plus the lowercase resource ULID and the user as `u_` plus the lowercase user ULID; validate through F9. MariaDB username length must also fit 10.11. Collisions fail conflict without adopting an unmanaged DB/account.

A database has exactly one primary db_users account, created with it. database.rotate_password targets that persisted primary account, never first-row selection. Additional accounts made by dbuser.create receive the same per-database allowlist and are not primary. There is no secondary-user rotation API in this phase. database.list adds primary_user_id; site.get adds wordpress_database_id when bound, and php_limits for PHP/WordPress, null for static. Do not expose passwords in these projections.

Keep database.delete `{db_id}` and dbuser.delete `{dbuser_id}`. Refuse deletion of a WordPress-bound DB or any primary account with precondition_failed. Delete the DB through site.delete purge_databases or first delete its site with retention to detach it. Additional dbuser deletion remains allowed under the parent DB/site locks. Unknown target is not_found at initial resolution; durable ensure-absent replay is a no-op only for the job's recorded resource identities.

Extend site.delete to `{site_id,purge_files?:bool=false,purge_databases?:bool=false}`. Keep registry MFA and roles/scopes unchanged. purge_files controls only the existing Phase 3 file/account contract. purge_databases=false drops the WordPress binding and detaches every attached DB with site_id=NULL in the final metadata transaction; DB data/accounts and any retained application files/config remain. Warn that retained wp-config still contains a live credential. purge_databases=true drops every attached MariaDB DB and its managed accounts before removing their SQLite rows, then completes the existing site deletion sequence. This is intentionally all attached databases, not only WordPress. No inferred consent from purge_files. Flags are independent; retaining files while purging DBs is allowed and warns that the retained app will be unusable. Replay uses a durable snapshot of generated DB/account identifiers taken under locks before any deletion. Never delete unmanaged resources or recreate retained accounts on replay.

## Insert in 02: exact Phase 4 forward migration DDL

Reserve `0006_phase4.sql` only if still the next unused migration at authorized implementation time; otherwise allocate the next number without editing 0001 through 0005. Register its actual computed hash in hashes.json. Replace the unimplemented Phase 4 DDL section with this DDL. Existing sites and shipped migrations remain unchanged.

```sql
CREATE TABLE site_php_limits (
  site_id TEXT PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
  memory_mb INTEGER NOT NULL DEFAULT 256 CHECK(memory_mb BETWEEN 32 AND 2048),
  upload_mb INTEGER NOT NULL DEFAULT 64 CHECK(upload_mb BETWEEN 1 AND 1024),
  max_exec_sec INTEGER NOT NULL DEFAULT 60 CHECK(max_exec_sec BETWEEN 1 AND 600),
  CHECK(upload_mb <= memory_mb)
);
CREATE TABLE databases (
  id TEXT PRIMARY KEY,
  site_id TEXT REFERENCES sites(id) ON DELETE SET NULL,
  name TEXT NOT NULL UNIQUE,
  engine TEXT NOT NULL DEFAULT 'mariadb' CHECK(engine='mariadb'),
  primary_user_id TEXT NOT NULL UNIQUE,
  created INTEGER NOT NULL,
  UNIQUE(id,site_id),
  FOREIGN KEY(primary_user_id,id) REFERENCES db_users(id,db_id)
    DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE db_users (
  id TEXT PRIMARY KEY,
  db_id TEXT NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
  username TEXT NOT NULL UNIQUE,
  host TEXT NOT NULL DEFAULT '127.0.0.1' CHECK(host='127.0.0.1'),
  created INTEGER NOT NULL,
  UNIQUE(id,db_id)
);
CREATE TABLE wordpress_bindings (
  site_id TEXT PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
  db_id TEXT NOT NULL UNIQUE,
  FOREIGN KEY(db_id,site_id) REFERENCES databases(id,site_id)
);
CREATE TABLE phase4_operations (
  job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE RESTRICT,
  kind TEXT NOT NULL CHECK(kind IN ('database_create','database_rotate','dbuser_create','wordpress_install','site_delete','database_delete','dbuser_delete')),
  site_id TEXT,
  db_id TEXT,
  checkpoint TEXT NOT NULL CHECK(checkpoint IN ('prepared','db_applied','config_published','verified','done','expired')),
  identities_json TEXT NOT NULL,
  reconciled INTEGER NOT NULL DEFAULT 0 CHECK(reconciled IN (0,1)),
  nonce BLOB,
  ciphertext BLOB,
  expires INTEGER NOT NULL,
  updated INTEGER NOT NULL,
  CHECK((nonce IS NULL)=(ciphertext IS NULL)),
  CHECK(checkpoint NOT IN ('done','expired') OR ciphertext IS NULL)
);
CREATE INDEX idx_phase4_operations_expiry ON phase4_operations(expires,job_id);
```

The deferred composite FK requires database plus primary user insertion in one transaction and forbids cross-database primary binding, primary-user deletion, or changing the binding to a missing user. The WordPress binding requires that the database belongs to that site. Create binding only after installation verifies consistency; a site in creating/failed state is not an installed WordPress app. Validate type=wordpress and immutable binding in typed store transactions. WordPress creation inserts its PHP limits defaults. php.set merges only supplied fields into persisted values, validates the full merged tuple, atomically persists after verified pool publication, and restores the previous desired state on failure. site.update.php_version delegates to that same path; no second PHP policy. Reject php.set on static sites. Pure reads never infer settings by parsing pool files. Memory also bounds post_max_size/upload_max_filesize; post_max_size equals upload_mb and upload_max_filesize equals upload_mb. pm.max_children is fixed at 2 for this bounded phase, not a caller-controlled value; raising it needs a separate deployment-capacity decision.

No plaintext password column is added. The operation payload is F12-sealed and contains generated credentials, salts, bound identities and desired protected-config bytes or equivalent typed fields. site_id/db_id are historical operation identifiers, deliberately not cascading FKs. The journal cannot disappear when its resource is removed. Nonsecret checkpoints are `prepared`, `db_applied`, `config_published`, `verified`, `done`, `expired`; delete uses `prepared`, `db_applied`, `done`. There is no separately committed claim_published state. Each mutation first ensures the recorded postcondition, then records its checkpoint, so a crash between effect and checkpoint reuses the same credential and identifiers. A DB transaction is not held during external IO. Commit verified metadata, completed job state/result, F19 sealed claim, journal done and recovery nonce/ciphertext removal in one SQLite transaction. For deletes the same transaction omits the claim. Rollback exposes none of these changes. Integrate this typed terminal method with the existing completion-audit boundary and preserve its consumed-claim exception. A done operation never republishes a consumed/expired claim. This requires a typed atomic store method, not separate best-effort writes.

Set operation expiry at preparation +24 hours, not extended by retries. Before each external mutation/restart, check expiry using the injected clock. Active operation records block conflicting mutation admission even while a crashed/failed job awaits recovery. Do not permit jobs retention deletion while a journal references it. Clear nonce/ciphertext at successful claim publication, or at verified terminal cleanup for jobs without claims. On startup before mutation admission opens, and once per minute thereafter, select at most 100 expired unfinished operations with the SQL below. In one transaction per bounded batch, clear their nonce/ciphertext, set checkpoint=expired and fail only their still-unfinished jobs with code=interrupted and a sanitized expiry message. Publish no claim. Never alter a done operation or completed job. Expired, unreconciled operations block only their conflicting resources until an authorized reconcile verifies a consistent or explicitly failed non-serving disposition and sets reconciled=1. Done and reconciled expired tombstones do not block admission. Use precondition_failed for bound-DB/primary-user refusals, not_found for unknown initial targets, conflict for ownership/membership/collision changes, locked for active conflicting operation, interrupted for expired recovery, and agent_error for integrity/custody failures. Ciphertext removal is logical erasure; WAL/backups may retain old encrypted bytes, never plaintext. Keep bounded nonsecret tombstones until normal job retention removes them via explicit deletion ordering, after reconciliation where needed. Expiry during inconsistent DB/config state leaves the site failed and non-serving; a fresh authorized rotation with a new sealed password must reconcile DB/config before reactivation. Do not delete the last recoverable ciphertext early while continuing the old operation. No permanent credential vault, automatic renewed expiry or rollback to guessed credentials.

## Insert in F6 and 01: deterministic locking

Canonical existing-resource keys are site:<persisted_site_id> and db:<persisted_db_id>; create reservation keys are site:<validated_name> or db:<generated_name>. php.set uses site:<site_id>. Every DB operation resolves its parent database and optional persisted site after authorization, never from client-provided guessed bindings. For dbuser operations resolve dbuser_id to parent database first.

Keep the existing jobs.object_key as the canonical primary/audit key. Add one narrow shared lock-set helper used by DB operations, WordPress install, PHP/site mutations and site.delete: collect all required keys, deduplicate, acquire site keys first in bytewise order, then DB keys in bytewise order, release in reverse order. Hold them from post-resolution revalidation through effect, checkpoint and final metadata publication. Never wait for a site lock while holding a DB lock. Do not recursively acquire the queue's already-held primary lock; extend keyedlock admission to acquire the full approved set once, rather than layering incompatible helper locks. Non-Phase-4 jobs retain a singleton set.

For a DB attached to a site the set includes site:<id> and db:<id>. Detached DBs need only db:<id>. Before locks, reserve all generated ULIDs and names in trusted durable job identity metadata. WordPress creation acquires site:<name>, site:<reserved_id>, db:<generated_name> and db:<reserved_id> together, site category first and bytewise within each category, before any resource creation. Never add an earlier site key while holding a reservation/DB lock. If row allocation requires an initial reservation lock, release it, acquire the full ordered set and revalidate durable creating ownership before any external effect. DB create uses its generated name and persisted-ID alias together with its optional site key. Later operations share persisted-ID keys. Admission must persist resolved IDs/names in trusted durable arguments, and worker execution rechecks parent membership under locks. For site.delete enumerate attached DBs only after holding its site lock; attachment/create also requires that site lock. Acquire the resulting DB set in order and revalidate before any effect. If a plan's pre-resolved memberships changed, release all and fail conflict rather than acquire an out-of-order lock. This is a bounded lock-set extension, not a new workflow scheduler. Recovery obtains exactly the same protection from the journal identities.

## Insert in phase-4 and packaging reference: artifact custody and local salts

Adopt WP-CLI 2.12.0 from `https://github.com/wp-cli/wp-cli/releases/download/v2.12.0/wp-cli-2.12.0.phar`, SHA-256 `ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c`, signer fingerprint `63AF7AA15067C05616FDDD88A3A2E8F226F0BC06`. Download to quarantine, verify digest and detached signature against the separately reviewed official fingerprint, then install root-owned 0644 outside site homes at `/usr/local/lib/novapanel/wp-cli-2.12.0.phar`. Refuse execution if installed bytes or custody differ. The URL is versioned, not immutable. Reverify approved bytes, signature and reviewed official fingerprint for every acquired or installed artifact. Signature verification evidence is retained in /root/.hermes/cache/scratch/phase4-decision-review-zisocmmu/signature-verification.log and reverified in this packet; trust is official fingerprint over HTTPS plus valid signature, not independently certified web of trust. Do not invent a trust key, fetch an arbitrary key on mismatch, or call the PHAR executable just to inspect it.

Generate eight independent WordPress keys/salts locally with crypto/rand and store them only in the sealed operation until protected config publication. Use WP-CLI config create with `--skip-salts` and stdin DB password, then add the generated constants with the agent's typed config publisher before core install. Do not use --extra-php stdin, PHP eval or the remote salt fallback. wp-config is a no-follow, atomically published site-owned 0400 regular file under the site lock. Refuse an existing/unmanaged or nonempty docroot; restart may resume only files bound to its own journal. Adopt WordPress 7.1.4, locale en_US, official ZIP https://downloads.wordpress.org/release/wordpress-7.1.4.zip, SHA-256 a147146c159362778ecbbbfb07bb20ddf4e85b130f971c824e656a8f938c5f87, 37239942 bytes. Pin the full official metadata from https://api.wordpress.org/core/checksums/1.0/?version=7.1.4&locale=en_US. Retained ZIP matched all 3782 entries. Retained full official checksum JSON SHA-256 is bb718bddf197e0098f05395dd9b228fad70b394926ee9d70a8c6a67cf20781e7; preserve its exact bytes as the offline fixture alongside the archive, and retain the official runtime verification path. This SHA is a locally computed pin over official HTTPS bytes, not an upstream signed SHA. Retain the unchanged official wp core verify-checksums command as an additional runtime gate; do not replace it with our ZIP scan, change checksum source files, use --insecure, or assume PHP/WP-CLI compatibility without the later real lab. Candidate grant allowlist stays exactly the phase spec's DB-scoped list until real install and upgrade/schema acceptance for that release passes. Failure blocks release; it does not justify global privileges or plugin-driven permission expansion.

Add only `d /run/php/novapanel 0755 root root -` and reviewed dependency metadata to packaging. make release-stage still builds binaries and maintenance.tar, not units/accounts/dependency installation. Remove phase-4's obsolete home 0750, `or wp.install`, secret argv example, nightly-admin_password claim and all-verbs-jobs language. Remove host secure-installation execution from Phase 4. Only Slow mutations queue; ssl.auto_set stays synchronous.

## Canonical application checklist

- Amend 00 F6 locking/recovery only; preserve F19 without weakening its guarantees.
- Amend 01 lock notes only; retain roles/scopes/MFA/Slow/Effectful values.
- Amend 02 with the DDL and lifetime above before coding a migration.
- Amend 03 shapes, validations, projections, claim keys and deletion flags exactly above.
- Amend phase-4, packaging-reference and 06 with corrected custody, stdin, salts, lock/deletion and acceptance contracts.
- Update docs/phase4-architecture-decisions.md and docs/phase4-dependency-map.md to record the approval SHA and prerequisite limitation.
- Internally approve the fixed 24-hour recovery lifetime, PHP defaults/bounds, generated-only names, independent purge flags, artifact pins and ordered-lock correction under existing delegation. Publication and implementation remain gated. Optional hardened PHP is deferred, not an unfulfilled Phase 4 completion check.

## Standalone deletion identity and expiry query

`identities_json` is canonical, validated nonsecret JSON bound immutably to job_id and kind. It contains the resource IDs, generated MariaDB names and exact account username/host pairs, original optional site ID and deletion flags. Do not include credentials. `database_delete` snapshots its DB and every managed account; `dbuser_delete` snapshots its parent DB, target account and primary/binding refusal checks. `site_delete` snapshots all attached managed DB/accounts under its site lock before effects. Retain this same journal after deleting resource rows. A restart may ensure absence only of these recorded identities; never re-resolve an unknown name to adopt another resource. No second scheduler or permanent secret store. Delete checkpoints commit after each ensure-absent effect, and final resource-row cleanup, job done and journal done commit atomically. No claim is created for deletion.

Expiry query, bound now is injected unix-ms, with the terminal filter applied before the limit:

```sql
SELECT job_id FROM phase4_operations INDEXED BY idx_phase4_operations_expiry
WHERE expires <= ? AND checkpoint NOT IN ('done','expired')
ORDER BY expires,job_id LIMIT 100;
```

Preparation expiry is exactly now + 86400000 ms. Claim delivery TTL starts at actual atomic publication per F19, not preparation. An expired operation cannot continue old external mutations. A fresh authorized operation may reconcile only after resolving the old blocker under the complete lock set.

## Literal MariaDB grants

Identifier quoting and privilege-pattern escaping are different operations. Validate generated identifiers, escape literal backslashes and `_`/`%` for the GRANT database pattern, then quote the identifier with the typed SQL renderer. Do not apply grant-pattern escaping to CREATE/DROP DATABASE identifiers. Example intent is a grant to literal d_abc, never dXabc. Inspect SHOW GRANTS and prove the application account cannot read a lookalike database whose underscore is replaced by a character, as well as an unrelated DB. Preserve exactly SELECT,INSERT,UPDATE,DELETE,CREATE,DROP,ALTER,INDEX,CREATE TEMPORARY TABLES,LOCK TABLES,REFERENCES. Do not silently change sql_mode or assume backticks remove wildcard semantics. Real MariaDB 10.11 tests must validate the chosen driver/renderer escaping under its actual mode before acceptance.

## Optional hardened PHP decision

Defer the owner opt-in hardened PHP profile until a later owner-approved request shape, persistence model and compatibility-warning acceptance contract. Phase 4 exposes no hardened field, toggle or claim of support. Its verification and DoD require the compatibility default, cgi.fix_pathinfo=0, bounded limits, log confinement, UID/filesystem/socket/DB isolation and explicit UI statement that optional hardened PHP is unavailable. The optional feature is outside this phase's amended completion contract, not a silently skipped required test. Home 0751 enables traversal only; docroot site:www-data 2750 and private files/subtrees enforce confidentiality.


Publication commit SHA: record the actual amendment commit at publication, not a predicted SHA. New Phase 4 base: record the actual merged main SHA only after the gate opens.
