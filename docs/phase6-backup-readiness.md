# Phase 6 backup readiness

Baseline: PR80 remote head `e334d522e90a647df49db0575d832a14db38fdc4`.
This assessment owns only `internal/backup` and this backup document. It does not
activate RPC, import Phase 4 runtime, allocate migrations or change shared jobs,
store, agent, runner, firewall or updater code.

## Existing independent components

- `Store.Open` applies canonical schema-only `0006_phase4.sql` and
  `0007_phase6.sql`. Repositories and the single-panel-role unique index exist.
  The older backup README's missing-schema statement was stale.
- `CreateRepository` atomically stores public metadata and a sealed password;
  `ListRepositories` includes role; `RepositoryPassword` returns ciphertext.
- `GeneratePassword`, `OpenPassword` and `PublishClaim` provide ephemeral custody
  and the existing F19 publisher. The scratch queue test proves owner binding,
  close/reopen persistence and one-shot refusal. It is not a repo worker test.
- `CreatePanelSnapshot` holds store lifetime admission over `VACUUM main INTO`.
  Backup tests prove committed WAL capture, integrity, concurrent writer progress,
  private staging, identity validation and changed-path refusal before consumption.
- `InitRepository` and `BackupPanelSnapshot` invoke fixed `/usr/bin/restic`
  through the existing runner with an explicit password environment. All nonzero
  exits fail, including source-read exit 3. Child output/errors are discarded.
  Pre-cancelled init now refuses before invoking the injected runner, matching
  the existing snapshot consumer. This does not prevent cancellation after admission.

## Production requirements still missing

| Contract | Current implementation and gate |
| --- | --- |
| `repo.create/list/delete` | No registered handlers or workers. Init, metadata and claim exist separately. Durable init/publication recovery, credential inputs and deletion policy are not composed. |
| Sealed remote credentials | Only repository password storage/execution exists. S3/SFTP credential custody and child configuration are missing. URL validation is not remote-backend acceptance. |
| `backup.create/list` | No registered handlers, catalog publication or restic JSON snapshot-ID parsing. SQLite-only upload cannot be called a complete panel backup. |
| Failure-aware DB backup | No dump consumer or `--stdin-from-command` implementation. Phase 4 MariaDB runtime is absent here. A fake restic exit-3 test does not prove dump failure handling. |
| Lock handling | `--retry-lock 30s` exists. Exit 11 fails closed; stale-only unlock plus one retry and process/liveness checks are missing. No `--remove-all` recovery exists. |
| `backup.verify` | No structural/read-data consumer, rolling `n/7` schedule, restore drill or freshness/retention wiring. Scheduler name allowlisting alone does not implement these. |
| `backup.restore` | No typed target-name confirmation, pre-restore snapshot, include confinement, scratch DB validation, maintenance or table/object swap. Phase 4 DB runtime remains a prerequisite. |
| `panel.backup_config` | No registered worker or `novapanelctl backup-config` entry point. Agent config, seal key and ACME key inventory/custody/packaging are not implemented. |
| Recovery custody | Out-of-band panel password remains mandatory. The canonical binary/config+seal-key/panel-DB/ACME/site-data restore order is documented in phase-6, not executed or accepted here. |

## Proposed boundaries requiring approval

These are proposals, not approved APIs or permission to implement the workflows.

1. A repository lifecycle owner must bind existing `InitRepository`, atomic
   metadata/password storage and F19 publication to the original durable job.
   Decide interrupted init adoption, secret-input cleanup and delete semantics
   before registering canonical repo verbs.
2. A backup execution/result adapter can use the existing runner and repository
   password custody, but needs bounded snapshot JSON parsing and exact catalog
   publication semantics. Verification must return distinct structural/read-data
   evidence, not mark structural success as restored-data proof.
3. A panel artifact-custody owner must define a closed inventory and stable
   read/cleanup rules for config, seal key and environment-specific ACME material.
   Consume the existing WAL snapshot without exposing sensitive source paths or
   keys in job results. Do not claim that a standalone SQL snapshot contains all
   recoverable filesystem/config state.
4. Site/DB backup and restore must wait for the approved Phase 4 runtime and its
   database/maintenance bindings. Resolve live-target identity and serialization,
   validate typed confirmation again at execution, complete a safety snapshot
   before mutation, and validate scratch data before any live swap.

## Verification boundaries

All local checks use scratch SQLite and injected restic. No restic executable is
installed in this session; no real repository, download or restore drill ran.
No host service, account, production path, firewall or external credential was used.
The cancellation regression first failed with `cancelled initialization invoked
restic 1 times`, then passed after the entry-point context check.

Local checks passed `go test ./internal/backup -count=3`,
`go test -race ./internal/backup -count=1`, `go vet ./internal/backup`,
the focused store repository/snapshot tests and `go test ./tools/arch -count=1`.
The broad `go test ./... -count=1` printed passes for cmd/internal packages but
hit the 300-second outer timeout. A tests/tools retry with `-timeout=180s`
failed on the integration package's aggregate deadline while
`TestFirewallStartupTerminalProcess/enabled-true/reverted/receipt-file` was
running. This is not a complete-suite pass or a diagnosed backup regression;
firewall tests/code remain outside this task's ownership.

Local package/race/vet results are component evidence only. Phase 6's real restic
backup/dump-failure/restore/readback tests and final disposable-VM acceptance remain
unmet. The canonical sources are `00-foundations.md`, phase-6, 01, 03, 02, 06 and
05-adr; no spec or authorization decision is changed by this assessment.
