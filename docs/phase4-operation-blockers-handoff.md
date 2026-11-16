# Phase 4 operation blocker fixes

Base: `a406c53020e2250070cc73eb1dc64586125980d2`.

The original independent queue probes first stopped at the new startup-admission gate. The committed regression ports preserve real SQLite and Queue.Run, call PrepareOperations before admission, use the credential-only input contract, and accept ErrRecoveryBlocked when startup refuses unresolved operations. Deadline injection now happens in PendingOperations after the startup expiry sweeps, because a406 executes pending journals before regular dequeue.

Both reported failures were reproduced before their fixes:

- Exact deadline after startup produced failed/agent_error, expired journal, no ciphertext and zero external calls.
- Attached database.create after active-to-failed site change invoked the runner once.

Changes:

- Map typed operation expiry to interrupted and canonical store refusals to conflict, precondition_failed, locked, invalid_argument or not_found.
- FailOperation samples expiry after acquiring the SQLite write lock. Expired interrupted jobs atomically lose their claim reservation and sealed recovery input; unexpired interrupted jobs retain retry input.
- Reuse the active-site eligibility check for attached database.create before effects and at atomic publication. DB-user creation, rotation and deletion retain their existing under-lock membership checks.
- Add real queue regressions for expiry, changed site/binding/account membership, create/user-create/rotation eligibility, malformed identity and rollback/reopen/no-duplicate effects. Add a direct atomic-publication eligibility test so credential preflight cannot hide a missing store check.

Verified locally:

- `go test -race -count=1 ./internal/jobs ./internal/store`
- `make ci`: build, formatting, vet, staticcheck, all tests, architecture checks, govulncheck and systemd verification passed. No called vulnerabilities; four uncalled required-module findings remain reported by the scanner.
- Original independent matrix port plus original empty-account boundary overlay, race enabled and count=3, passed. Expiry returned interrupted with ciphertext cleared and zero commands; changed eligibility invoked zero commands; rollback/reopen retained one external effect and exactly one database/account/job, and delivered a password-only one-shot claim without replay regeneration.

Evidence lives outside the repository:

- `/root/.hermes/cache/scratch/phase4-a406-blockers-ci.log`
- `/root/.hermes/cache/scratch/phase4-a406-blockers-matrix.log`
- `/root/.hermes/cache/scratch/phase4-a406-final-matrix-overlay.json`

No render paths, migration, runtime consumers, host services, production databases, public CA or publication changed. Real MariaDB, privileged Ubuntu isolation and power-loss acceptance remain unverified. Startup admission intentionally stays closed when unresolved failed journals remain. This is not whole-Phase-4 or release approval.
