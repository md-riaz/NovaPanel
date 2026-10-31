# PHP production consumer follow-up

Base: `dc9168cd7213bae274cf3a24faee7f38205ceae2`, preserving the previous worker's compatibility warning. Its parent is pinned live `2581e7c86376032776197f8da3a80e34cbbc1a38`. This bounded slice is not Phase 4 completion or whole-phase review approval.

## Requirement map

| Canonical requirement | Production consumer | Executed assertion | Remaining source gap |
| --- | --- | --- | --- |
| 03 php.list and 01 synchronous read-only sites:read | internal/verbs/php.go registration and handler | TestPHPListReadContract dispatches all user roles and a scoped token, denies wrong scope and unknown hardened field, requires exactly versions=[8.3] and default=8.3 without store/queue/runner | None for supported-version policy; it deliberately does not claim host installation/readiness |
| 03 site.get persisted php_limits | internal/verbs/site.go siteGet reads Store.GetPHPLimits | TestSiteGetPersistedPHPLimits uses real SQLite through RPC for static/php/wordpress, nondefault tuples and partial metadata changes | Does not make metadata-only test writes a production php.set implementation |
| PHP desired settings visible without unsupported mutation controls | internal/web/templates/console.html site.get | TestPHPPersistedLimitsView executes HTTP template rendering for read_only/admin/owner with exact values and no PHP POST form | Browser automation and real-backend browser integration not executed in this slice |
| Optional hardened PHP explicitly unavailable | Previous dc9168c compatibility notice retained unchanged | TestPHPCompatibilityAvailabilityNotice rerun in full web and race tests | None for warning; no hardened feature was added |
| php.set and site.update.php_version share publication path | Still absent/refused in internal/verbs/site_mutations.go | Existing regressions remain green; no fabricated success callback or helper-only completion | Agent has no PHP runtime dependency; nginx.ApplyPHP requires atomic durable caller publication and is in-process only. No production worker consumes it. Durable cross-service crash/reopen publication and lifetime-aware FPM orchestration remain absent |
| PHP/WordPress create, UID/socket/IO containment | site_create.go still refuses nonstatic | Full suite confirms existing fail-closed behavior | Complete operation lifetime admission, all-launcher FPM containment, IO retirement and serving release required by phase4-config-isolation-lifetime-contract.md are not provided by this read-only slice |

Missing limits return explicit null, not guessed defaults or parsed pool bytes. Creating/failed resources can legitimately lack committed limits. No read mutates the site or queues work.

## Tests

Using PATH `/root/.hermes/cache/scratch/phase6-toolchain-verified-20261031/go/bin:/root/.hermes/cache/scratch/phase6-toolchain-verified-20261031/aux-bin:$PATH`, `GOPROXY=off`, `GOTOOLCHAIN=local`, Go reports `go1.27.3 linux/amd64`.

- RED: TestPHPListReadContract returned unknown_verb before registration.
- RED: TestSiteGetPersistedPHPLimits found absent/null projections before wiring.
- RED: TestPHPPersistedLimitsView found missing settings before template wiring.
- PASS: `go test -race ./internal/verbs ./internal/web -count=1`.
- PASS: `go vet ./internal/verbs ./internal/web`.
- Initial full-suite run found the intentionally exact registered-verb allowlist needed php.list; updated only that expected entry.
- PASS after that update: `go test ./... -count=1` and `go test -race ./internal/rpc -count=1`.
- PASS: `git diff --check`.

Self-review checked registry role/scope/synchronous policy, absence of external process execution, persisted-only projections, explicit null behavior and escaped read-only templates. Independent review remains the integration owner's responsibility; no nested reviewer was available to this subagent.

## Required parent decision

Before enabling mutation consumers, resolve FPM admission ownership in the existing lifetime contract: dedicated per-site supervised masters versus explicitly server-wide maintenance of the shared master and every managed launcher. Choose and verify the IO retirement/backend contract. Wire durable original-operation lifetime and cross-service recovery before exposing php.set or PHP creation. This requires jobs/Agent/platform/render ownership outside this task's permitted files. A simple nonnil callback is not sufficient.

No push, merge, host account/service/cgroup/PHP mutation or production activation occurred. Final privileged Ubuntu VM, root-FPM distinct UID/socket ownership, cross-site confidentiality, crash/power-loss and real service acceptance remain unpassed.
