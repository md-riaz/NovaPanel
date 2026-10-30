# Scheduler and mutation acceptance

- Base: `465967584d7914d0b0573fdc3a94701bbd0ecad0`.
- Tested source: `0fc5c1d1e7321d5746df6cd0139bae07f4aa08ac`. The later evidence commit adds no executable changes.
- Result: `TestRealPebbleSchedulerMutationAcceptance` PASS, 38.66 seconds, 16 successful jobs plus one intentionally failed nginx validation job.
- `go test ./... -count=1` and `go vet ./tests/integration` passed on that source. `git diff BASE --exit-code -- internal cmd` returned zero.
- Artifact archive and binary SHA256 are in `manifest.json`. Raw lab directory: `/root/.hermes/cache/scratch/phase3-pebble-qc1d9wln`.

## Reproduce

Use the pinned Pebble executable extracted by the existing lab setup. No host service commands are required.

```sh
export PATH=/root/.hermes/cache/scratch/novapanel-toolchain/bin:$PATH
NOVAPANEL_PEBBLE_BINARY=/root/.hermes/cache/scratch/phase3-pebble-qxbfsmj8/pebble \
NOVAPANEL_PEBBLE_TEST='^TestRealPebbleSchedulerMutationAcceptance$' \
python3 tools/pebble_nginx_lab.py
go test ./... -count=1
go vet ./tests/integration
git diff 465967584d7914d0b0573fdc3a94701bbd0ecad0 --exit-code -- internal cmd
```

The launcher requires clean tracked source and records its exact SHA. It inspects the container before start, after execution and before removal. Assertions require nonroot UID/GID 65534, all capabilities dropped, no-new-privileges, network none, no mounts, no ports, no privilege, and bounded memory/CPU/PIDs. `teardown.txt` records verified container absence.

## Proven boundaries

- Scheduler `Tick` with injected time, persisted real SQLite schedules, real `Queue.Run`, `Server.ExecuteJob`, lego, genuine Pebble HTTP01 validation and real nginx publication.
- Exactly 31 days remains ineligible; exactly 30 days schedules renewal. The renewal changes the version and reloads once. Daily cadence survives complete store/audit/server/queue reconstruction. Repeated ticks and duplicate issue create no jobs, reloads or durable rate-budget changes.
- Explicit staging provenance reaches the scheduled job. Separate local Pebble accounts exercise staging and production config slots; both endpoints remain private loopback. This is not public CA issuance.
- Forced HTTPS precedes `domain.add`. The scheduler selects `ssl.issue` for expansion, passes explicit production-slot provenance and obtains both SANs. Both domains persist issued coverage. The HTTP01 missing path remains 404, not redirected.
- Disabled automatic renewal and manual source with `ssl_auto=true` both cross daily cadence without renewal jobs or reloads, including an already-existing automatic schedule.
- Actual queued success coverage includes site create/update/delete, domain add/remove, redirect add/remove, SSL issue/renew/upload and TLS primary promotion. `site.create` uses `site:<name>`; every other queued mutation uses `site:<id>`.
- `ssl.auto_set` is deliberately synchronous in the implemented registry. Its success audit and persisted preference are checked. This is not falsely described as a queued mutation.
- A held first real executor plus a second admitted same-site update proves the second stays queued, then both finish serially. The wrapper delegates to `Server.ExecuteJob`; it does not replace handlers.
- Each successful job has exactly one correlated durable enqueue and terminal event with matching actor label, trusted stored role, verb, object key and outcome. Every implemented mutation also has an exact read-only authorization refusal audit. An actor label of `owner` grants no authority.
- A real nginx syntax failure yields one terminal `error/agent_error` audit from the production worker. File audit records match SQLite mirror IDs and fields.
- Genuine Pebble output is reused as manual upload input. Enqueue arguments contain no plaintext PEM. DB, WAL and file audit contain neither PEM markers nor the full certificate/private key inputs.
- Launcher asserts exactly four CA orders and four issued certificates, with exactly two environment accounts. Logs show real Pebble challenge GET 200 responses served by nginx.

## Limits and findings

- Scratch provisioning replaces Unix user creation with scratch docroot/challenge-directory creation. Ownership/useradd/getent, purge-files deletion, Ubuntu 24.04/systemd, chattr and production ports 80/443 are not accepted by this lab. Another worker owns provisioning validation.
- nginx is Alpine 1.29.5 with 8080/8443 adaptation. Deprecated legacy HTTP2 warnings are expected and visible in raw evidence.
- Scheduler ticks are called deterministically with injected time; the production timed goroutine is not waited for over wall-clock days.
- Real failure coverage uses site.update. Per-verb injected worker failures/panics, token/MFA variants, crash-during-publication and arbitrary cross-site parallel execution are separate existing tests, not newly proven by this matrix.
- Initial fixture failure omitted the webroot directory. The test-only provisioning fixture now creates it. A transition from staging to a new account via renewal failed because the old certificate belonged to the other account. The environment transition correctly uses fresh issuance instead; both failures remain in scratch evidence directories `phase3-pebble-hghts1dc` and `phase3-pebble-jvr4w5ng`.
- No core source was changed. No host services, public CA, production state or push were used.
