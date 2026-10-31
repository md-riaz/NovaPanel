# Typed WordPress and site-delete journal foundation

Base `eace6847929e04dbe63caf4f18e5092c8d3beb7e`. This work adds internal store/queue contracts only. It does not enable a runtime consumer or satisfy whole-Phase-4 acceptance.

## Contracts

- `store.SiteDelete` and `store.WordPressInstall` extend `OperationKind` using the existing approved migration. Migration 0006 and its hash remain unchanged.
- `OperationRequest` adds independent `PurgeFiles`, `PurgeDatabases` and optional `WordPress *WordPressReservation` fields.
- `OperationIdentity` adds the full persisted `Site` snapshot, ordered `Attached []OperationDatabase` containing every managed DB/account, `BindingID`, independent deletion flags and an optional WordPress reservation.
- `WordPressReservation` contains `DomainID`, `wordpress.InstallInput` and `wordpress.ArtifactEvidence`. The pure package from `67612c56822008d1dd330b00841a312d8f842bed` is integrated unchanged. Evidence remains public verification metadata, not runtime custody proof.
- `Queue.EnqueueOperation` supports typed site deletion. It snapshots attached identities under the queue's site lock. Execution acquires the journal's complete ordered set and compares site, binding, database and account membership before calling a runner and again at terminal publication.
- `Queue.EnqueueWordPress(ctx, store.WordPressReservation, WordPressCredentials)` reserves site/domain/database/account ULIDs before any resource rows or effects. `WordPressCredentials` contains DB/admin passwords plus eight salts. The trusted admitting agent must generate them independently. The queue seals an envelope bound to job, owner, authority, IDs and the entire reservation.
- `Queue.OpenWordPressCredentials(ctx, jobID)` validates that envelope under the full operation lock set. Salts are not projected into the F19 claim. The claim has exactly canonical `database` and `wordpress` objects, while the job result contains only `site_id`.
- WordPress terminal publication atomically inserts site, primary domain, database/account, binding and default PHP limits, publishes the sealed claim, finishes the job/journal and removes recovery ciphertext. Site deletion atomically removes the binding/site and either purges attached metadata or detaches retained databases. No deletion claim exists.
- Typed unfinished journals now own running-job restart recovery even when `Effectful=true`. Ordinary effectful jobs retain the existing interrupted behavior. Unknown/corrupt journal identities still refuse startup before external execution.
- Durable blocker checks include reservation aliases, so a different reserved site ID cannot reuse an unfinished WordPress site's name. Alias checks currently scan unfinished journals, rather than adding an unapproved index or migration.

## Verified foundation tests

Tests use real scratch SQLite and real Queue.Run/startup with injected fake runners. Cancellation after a checkpoint, close/reopen, restored authority, retained credentials, retry without repeating the recorded fake effect, independent purge flags, claim erasure and one-shot replay are exercised. WordPress terminal failure is injected on the final journal-done update through the real queue. Metadata publication rolls back and sealed input survives for retry. Site-delete terminal failure is tested directly. Stale site/docroot, binding, attached database and account membership refuse before the deletion runner. A syntactically valid administrator substitution fails sealed-envelope binding.

Executed locally:

- `go test -race -count=1 ./internal/store ./internal/jobs ./internal/wordpress ./internal/agent ./cmd/panel-agent`
- `go test -race ./internal/jobs -run 'TestWordPressQueueRecoveryAndClaim|TestSiteDelete' -count=3`
- `make ci`, including architecture checks, passed before the final test-only rollback addition. The scanner reports no called vulnerabilities and four required-module-only findings.

These are fake-runner foundation results, not real MariaDB, WordPress, protected-config, nginx/FPM or power-loss results.

## Remaining runtime gates

No verb registry or production handler is modified. Existing attached-site deletion without a typed journal remains unsupported. PHP/WordPress site creation and DB consumers remain gated. The production worker is not a WordPress runtime adapter, and cannot successfully consume these empty nonsecret job arguments as a completed installation.

Before runtime opening, provide the actual nonroot provisioned identity and filesystem custody model, independent crypto/rand generation, held no-follow artifacts and empty-docroot protection, protected 0400 config publication and journal-bound recovery, precise effect/postcondition ordering, real PHP/WordPress stdin and official-checksum acceptance, MariaDB unix_socket/grant acceptance, all-kind expiry/failure/audit/authority matrices and privileged Ubuntu acceptance. A future adapter must not mutate the snapshotted site desired state between preparation and terminal commit without a reviewed typed transition. Site-delete external account/file/nginx cleanup is not implemented here. Domain/redirect/config custody and runtime inventory are not attested by the persisted Site snapshot alone.

No host services, production DB, public CA, push or PR mutation was used. Independent combined review is still required.
