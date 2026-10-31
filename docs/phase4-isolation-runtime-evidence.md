# Isolation runtime slice evidence

Base: b4097215503be0a7ba96cd14e34c593c744e0538.
Worktree: /root/.hermes/cache/scratch/NovaPanel-phase4-isolation-runtime-exclusive.
Logs: /root/.hermes/cache/scratch/phase4-isolation-runtime-evidence/.

- exec-red.log and platform-red.log retain missing-interface RED runs.
- focused-final.log records uncached race execution of internal/exec and internal/platform.
- vet-final.log records whole-repository vet.
- all-tests.log records whole-repository test failure in TestArchitecture. baseline-architecture.log reproduces the same failure on unchanged b409721: pre-existing Python characterization tests use the architecture check's forbidden literal -c. This slice does not conceal the failing gate or weaken the architecture check.
- container-exec.log records real nonroot network-none read-only no-capability container execution. CLONE_INTO_CGROUP refuses with function not implemented and the printf child never executes. This is real kernel/seccomp refusal, not successful containment. Credential-drop test is skipped inside the nonroot container and passes in the root scratch runner suite.
- container-platform-final.log records nonroot admission/drain tests, including retained launch refusal, cancel/drain ordering, concurrent drain deadline and malformed recursive-empty observations. These use injected retirement, not fake kernel acceptance.
- Docker used existing ubuntu:24.04 with COPY-only test binaries, no host mounts, no privileges or services. Explicitly owned containers and image were removed and exact target absence checked. No host account/cgroup/service mutation or PHP execution occurred.

No delegated writable cgroup is available in this confined fixture. Positive at-birth membership, recursive kill under concurrent fork, production cgroup custody, all-writer IO retirement and final VM acceptance remain unverified. Kernel control-file reads/writes are cooperative context checks, not preemptible syscall wall-clock guarantees. Jobs wiring, durable lifetime reservation/recovery and FPM orchestration are not implemented by this slice. The next-consumer contract is phase4-isolation-runtime-slice.md. No ConfigIsolation callback or serving activation is added.
