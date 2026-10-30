# Phase 3 SSL integration status

This records implementation state, not a new schema or a change to F6/F8.
No migration changes are included.

## RPC contracts

- `ssl.status{site_id}` is synchronous, read_only, `sites:read`. Issued status requires a validated installed pair at the agent clock. Reads never repair metadata.
- `ssl.auto_set{site_id,enabled}` is synchronous, admin, `sites:write`. It changes only the renewal preference. Renewal must independently require `cert_source='letsencrypt'`.
- `ssl.upload{site_id,cert_pem,key_pem}` remains admin, MFA, slow, non-effectful, `sites:write`, bound to `site:<site_id>`. Complete inputs are sealed before enqueue and opened only during execution. The execution path revalidates current active-site domains and dates.

## Publication and recovery

The trusted internal checkpoint is the singleton settings key `tls:publication:v1`. Its JSON contains format version 1, job/site IDs, nginx root, the original ordered domain set, TLS Recovery coordinates, and a nonsecret nginx config/LKG/enabled-link snapshot. No certificate, private key, or sealed input is copied into the checkpoint. These fields are agent-generated, never RPC arguments. A singleton intentionally blocks all other nginx applies/removals while unresolved, because nginx validates and reloads the full shared config set.

1. Hold the shared Applier transaction lock. Read and reconcile an existing intent before attempting new publication. Refuse unknown fields, malformed/boundary-mismatched coordinates or changed site/primary/domain bindings. Restore the old pointer, restore config and LKG, validate and reload using a bounded detached recovery context. Reuse the original staged token rather than staging a new version on replay.
2. Stage the pair and persist the checkpoint before Publish. A crash after Stage but before checkpoint can leave an unreferenced owner-only version directory, but cannot change current.
3. Publish, load validated installed TLS state, force nginx validation/reload even if config bytes match, then probe HTTP/ACME and HTTPS against the exact installed leaf. Production uses StateProbe on fixed loopback ports 80/443, including private-CA/manual certificate support.
4. On failure, restore the certificate pointer BEFORE restoring config or recovery reload. Preserve first-install absence. Do not attempt reload when pointer/config recovery fails. Retain intent and sealed input as interrupted recovery state on unresolved failure or panic.
5. After probe and durable LKG write, one SQLite transaction writes manual source/domain expiry metadata, `jobs.step='tls_committed'`, public result `{}`, and deletes the exact intent. A committed replay returns without opening secrets or reloading. The normal queue owns terminal audit/event/state and sealed-input cleanup. The marker remains through terminal publication.

This is a recoverable protocol, NOT an atomic filesystem/SQLite transaction. Files and affected nginx directories are synced. Both certificate versions remain retained.

## Verified with scratch resources

Tests use generated ephemeral certificates, fake runners, scratch nginx/TLS roots and reopened SQLite, never live services or CAs. They cover first install and replacement; checkpoint/pointer publication restart; nginx validation, reload and probe failures; cancellation with detached recovery; panic after reload; committed replay before terminal publication; secret-free persisted inputs/results/checkpoints; and recovery failure refusing further reload.

## Remaining acceptance and operational gaps

- True process-kill/power-loss tests at every fsync/SQLite/audit boundary are not performed. Restart tests reconstruct checkpoints or reopen SQLite; panic injection is not a host power-loss test.
- A recovery failure returns interrupted and retains sealed input/intent. The existing queue has no authorized retry RPC for failed interrupted non-effectful jobs. Operator recovery of that exact job is still needed; unrelated jobs refuse rather than overwrite it. A changed domain/lifecycle/root binding also needs operator repair.
- TLS storage flock acquisition is not context-aware. The detached nginx command context is bounded, but a hostile or stuck cooperating writer holding the filesystem lock can exceed that recovery budget.
- Pre-checkpoint staged directories are not garbage-collected. No retained-version GC policy is implemented.
- Existing site mutations continue to refuse installed TLS state rather than render HTTP-only over it. Intent checks stop lifecycle mutation before changing recovery bindings; full TLS-aware site editing is separate work.
- Audit completion and terminal job publication remain the queue's existing protocol. The committed marker prevents repeat reload after a crash before terminal publication; duplicate completion audit rows remain possible after a crash between audit and terminal update.
- Real nginx/systemd, VM acceptance and live CA requests remain unperformed. ACME, module dependencies and console changes belong to other workers.
