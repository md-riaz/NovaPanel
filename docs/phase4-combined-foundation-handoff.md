# Combined Phase 4 foundation

The new integration branch starts at `42dfcae`. Operation source `7058539` and PHP dependency chain `c959c1a`, `2f50d87`, `10a479b`, `0d5b028`, `40e61e3` were cherry-picked once. Every source/destination stable patch ID matches. The combined foundation was committed as `9345017` before bounded corrections. No runtime handler or external database execution was enabled.

## Proven corrections

- A real `Queue.Run` is cancelled after `db_applied`, SQLite is closed/reopened, and the original job resumes to atomic completion. The claim formerly leaked every sealed recovery field. It now projects only the canonical password field and remains one-shot.
- Corrupt journal tests formerly accepted missing/duplicate accounts, unmanaged names, wildcard hosts, a wrong primary, trailing JSON and a job-verb mismatch. Typed loading now rejects those identities before lock planning or terminal publication.
- Changed account membership formerly passed pre-effect checks and terminal deletion. Both now compare the recorded database/account snapshot, and current site status, collision and WordPress binding checks are repeated where applicable.
- Real queue startup at the exact operation deadline formerly marked the job `agent_error`. Startup now performs a bounded expiry batch before recovery, and the worker checks expiry once per minute between jobs. The deadline test proves `interrupted`, no runner call and recovery ciphertext removal.
- The first full race run and unchanged retry at `69eafd0` failed `TestACMEWildcardWorkerRefusesDurableInput` during queue cleanup with `interrupted (9)`; an isolated repetition also failed. The added startup expiry transaction could return SQLite cancellation during shutdown. The new cancellation regression fails against the previous worker through a Go overlay; cancellation now drains cleanly, matching existing recovery behavior. These failed logs are retained under `phase4-combined-69eafd0-*` in scratch.

## Limits before consumers

This is not whole Phase 4 acceptance. The runner tests inject external postconditions and a test auditor, not real MariaDB, production file audit, PHP service recovery or power loss. This paragraph records the historical f30a212 boundary. The later admission slice adds a draining startup loop, operation-only admission, a credential-only typed envelope and create/rotate/dbuser authority recovery tests. Global mutation admission before listener publication, full WordPress recovery payloads, canonical store-error mapping, bounded batch concurrency/query-plan evidence and authorized reconcile wiring remain incomplete. Periodic expiry waits for a running worker call to finish, whose mutations must still check expiry independently. The current combined status and two outstanding independent-review blockers are in `phase4-admission-restored-integration.md`.

WordPress and site-delete journal variants and dynamic attached-database locks remain unimplemented, and need a separately reviewed concrete typed contract. PHP restored-service probing and confined in-process rollback acceptance are now recorded in `phase4-restored-service-probe-handoff.md`; durable crash recovery remains a completion gate. Do not mistake the in-process PHP callback for a durable cross-service transaction. The previous proven-unchanged apply defect is already fixed by `40e61e3`, not duplicated here.

All migration bytes and `hashes.json`, including production `0006_phase4.sql`, are unchanged against `42dfcae`. No migration number was allocated. No host services, public CA, live database, remote publication or Phase 4 merge was performed. Earlier foundation audit-flake evidence reported by the coordinator remains unresolved and is not dismissed by this work.
