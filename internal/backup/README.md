# Panel snapshot foundation

`CreateSnapshot(ctx, source, scratch)` runs `VACUUM main INTO ?` against an injected `SQLExecutor`. It includes committed WAL data without checkpointing or copying the live main file. The existing modernc SQLite driver is exercised by the package tests.

- `scratch` must be an existing absolute, same-owner, exactly 0700 directory. Every path component is opened without following symlinks.
- Each call creates an exclusive private directory and an exclusive empty 0600 `panel.db`, then SQLite fills it. Held directory descriptors anchor SQLite's destination through Linux `/proc/self/fd`.
- Success follows file close/fsync and directory fsync. The returned `Snapshot.Path()` contains sensitive database bytes. Consume it only inside trusted agent code, never in a job result or log, and always call `Close()` after consumption.
- SQL failures are deliberately generic to avoid propagating driver errors containing source values. Cancellation remains discoverable with `errors.Is`. Failure removes partial staging; cleanup errors are returned, not ignored.
- Supply a deadline-bound context. `Close` requires exclusive use and is idempotent. This is ordinary filesystem deletion, not secure erasure. A process crash can leave private staging behind; startup reconciliation is not implemented.

## Integration handoff

No production store is opened here. The store currently keeps its tracked SQL pool private. A future store adapter can implement `SQLExecutor.ExecContext` using its existing lifetime admission/cancellation and execute outside a transaction. Do not use `Store.Tx` for VACUUM, export the raw pool, or open a second production store. `*sql.DB` and `*sql.Conn` already implement this interface for scratch testing.

This implements only the snapshot portion of Phase 6's resolved panel self-backup contract. It does not register `panel.backup_config`, define another RPC shape, access config/seal/ACME files, export the master key, create repo passwords, or run restic/services. There is no canonical panel-file manifest schema, so no speculative manifest format is introduced.

Future integration must preserve `panel.backup_config{repo_id} -> job -> {snapshot_id}`, `repo.create{role:'panel'}`, the single-panel-repo conflict rule, sealed repository credentials and F19 owner-bound show-once password claims with out-of-band custody. It must not return scratch paths or secret bytes in durable results. Site and MariaDB backup/restore remain blocked on Phase 4. Full restic read-back verification, recovery ordering, crash reconciliation, config/key packaging and restore drills remain separate work; these tests are not full backup/recovery acceptance.

## Safe verification

Set TMPDIR to the agent's private scratch root, then run:

```
go test ./internal/backup -count=10
go test -race ./internal/backup -count=3
go vet ./internal/backup
```

Tests create only disposable SQLite fixtures. They verify nonempty uncheckpointed WAL capture, source close and snapshot reopen, `integrity_check`, committed-row inclusion during concurrent WAL writes, private modes, symlink/unsafe-root refusal, cancellation, error redaction and staging cleanup.
