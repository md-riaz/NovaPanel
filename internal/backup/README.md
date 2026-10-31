# Panel snapshot foundation

`CreateSnapshot(ctx, source, scratch)` runs `VACUUM main INTO ?` against an injected `SQLExecutor`. It includes committed WAL data without checkpointing or copying the live main file. The existing modernc SQLite driver is exercised by the package tests.

- `scratch` must be an existing absolute, same-owner, exactly 0700 directory. Every path component is opened without following symlinks.
- Each call creates an exclusive private directory and an exclusive empty 0600 `panel.db`, then SQLite fills it. Held directory descriptors anchor SQLite's destination through Linux `/proc/self/fd`.
- Success follows file close/fsync and directory fsync. The returned `Snapshot.Path()` contains sensitive database bytes. Consume it only inside trusted agent code, never in a job result or log, and always call `Close()` after consumption.
- SQL failures are deliberately generic to avoid propagating driver errors containing source values. Cancellation remains discoverable with `errors.Is`. Failure removes partial staging; cleanup errors are returned, not ignored.
- Supply a deadline-bound context. `Close` requires exclusive use and is idempotent. This is ordinary filesystem deletion, not secure erasure. A process crash can leave private staging behind; startup reconciliation is not implemented.

## Integration handoff

`Store.CreatePanelSnapshot` now holds lifetime admission and cancellation over the entire staging operation and uses the existing private SQL pool outside a transaction. It does not export the pool or open another store. Tests use disposable stores only.

`Store.CreateRepository`, `ListRepositories` and `RepositoryPassword` atomically persist public metadata and sealed password bytes under `repo:<id>:password`. The store checks ID/name conflicts and the single-panel-repo rule inside its transaction. Listing includes `role`. The canonical Phase 6 `repos` table is still absent from production migrations. Tests install canonical DDL and a partial unique panel-role index as a fixture only. No migration number is allocated here because Phase 4 owns the next production migration. Missing tables fail closed.

`GeneratePassword` and `OpenPassword` retain plaintext only in short-lived execution custody, redact formatting and JSON, and clear owned buffers on `Close`. `PublishClaim` uses the existing F19 queue publisher. A real scratch-store claim test proves sealed publication survives reopen, refuses another owner, allows one read and refuses replay. Store the claimed recovery password out-of-band. No password is returned as repository metadata or durable job output.

`InitRepository` and `BackupPanelSnapshot` use the sole existing runner boundary with fixed `/usr/bin/restic`, argv slices, an explicit child-only `RESTIC_PASSWORD` environment, no inherited environment and no shell. Validation permits only local absolute paths, `sftp://` URLs and S3-compatible HTTPS locations without embedded passwords, queries or fragments. Snapshot execution requires a panel-role repository and a live private snapshot. All nonzero exits, including 3, 11 and 12, fail. Child output and runner errors are discarded to prevent secret echo. Tests inject fake restic only. Runner options must never be logged. Strings copied by Go or the runner are not guaranteed to be erased.

These are internal integration contracts, not registered `repo.create` or `panel.backup_config` workers. No complete-backup row, snapshot ID, recovery acceptance or read-back verification is claimed. Remote credentials, stale-lock reconciliation, init-versus-metadata recovery, atomic repository/claim publication, scheduler wiring, config/key packaging and restore drills remain unwired. No config, seal-key or ACME key source is opened or captured.

Preserve the canonical `panel.backup_config{repo_id} -> job -> {snapshot_id}` and `repo.create{role:'panel'}` shapes when wiring workers. Serialize the init/publication lifecycle, publish the generated password through the owner-bound F19 claim and never mark a snapshot-only operation as a complete panel backup. Site and MariaDB backup/restore remain blocked on Phase 4. Firewall deadman runtime belongs to the firewall integration and is not implemented here.

## Safe verification

Set TMPDIR to the agent's private scratch root, then run:

```
go test ./internal/backup -count=10
go test -race ./internal/backup -count=3
go vet ./internal/backup
```

Tests create only disposable SQLite fixtures. They verify nonempty uncheckpointed WAL capture, source close and snapshot reopen, `integrity_check`, committed-row inclusion during concurrent WAL writes, private modes, symlink/unsafe-root refusal, cancellation, error redaction and staging cleanup.
