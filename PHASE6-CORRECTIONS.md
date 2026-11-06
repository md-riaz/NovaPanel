# Phase 6 independent review corrections

Source basis is unpublished 183c6764fb097d4f807df170df7e44ba52e1f566. No publication or host manager/UFW commands were performed.

## Corrected behavior

- Require empty SuccessExitStatus as well as restart-prevent/force status sets. Nonzero helper failures must remain failures under Restart=on-failure.
- Verify an existing timer's next monotonic elapse against the remaining absolute deadline, not its original OnActive interval against a shrinking countdown.
- Verify retained waiting or spent timers before starting the same service immediately for expired or reverting authority. Never stop, replace or reset that owner. Future admission still requires active/waiting and a live bounded schedule.
- Compare stable identity and safety properties across readback. Ignore unrelated typed accounting counters and ExecStart execution timestamps, while retaining exact executable, argv and ignore-failure checks.
- Refuse OnClockChange and OnTimezoneChange triggers.

The independent external probes are now retained repository tests. Each of the five reported defects had a separate failing run before its production correction. Logs 01 through 05 under .phase6-evidence retain those results. The first connected acceptance assertion also failed against the old fixture before manager-owned child launching was implemented.

## Connected source acceptance

TestFirewallStartupStoreIndependence runs six cases: prior-enabled and prior-disabled, each with success, injected command failure and abrupt child process exit. The fake manager Runner actually starts the compiled command test binary through internal/exec.Recovery. That child dispatches the real fw-revert helper composition with private scratch layout and fake UFW commands. The command overlay binary is race-instrumented. No direct post-startup helper invocation substitutes for manager launch.

For failures and abrupt death, the first process leaves the original ID in reverting state. Startup reopens the journal, adopts the same fake manager pair, starts the same service, and launches a new helper process. The parent holds a real Store lifetime lock throughout. Both the initial and replay paths remain independent of it. Assertions cover all six restored file contents and modes, exact enabled/disabled command sequence, ID/digest-bound marker, reverted journal and unchanged Store/sidecar bytes and lock ownership.

A barrier-controlled temporal matrix pauses startup at launch, service readback and timer readback. Independent confirmation and helper processes run while startup is paused. Separate rows change journal identity/state or cancel startup. Assertions require retained evidence, explicit changed-journal detection and no manager error hidden by ErrFirewallRecoveryPending. Two concurrent startup callers also synchronize absent discovery, then adopt the exact same configuration after ambiguous competing launch. A separate matrix covers armed/confirming expired and predeadline reverting with waiting and spent existing timers.

## Verification and limitations

Final completed gates used the verified scratch Go and aux-bin, GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off:

- Focused firewall/agent/exec/command uncached race: exit 0.
- Packaging/dispatcher/startup integration uncached race: exit 0.
- Temporal and independent regression race matrix, count=3: exit 0.
- make ci: exit 0.
- make phase6-test: exit 0.
- go test -race -count=1 -json ./...: exit 0, 28 package passes, 1631 test/subtest passes, nine skips, no failures.

An earlier phase6-test run exposed an existing fixture/source-walk race: custody fixtures were created and removed inside the source tree while the architecture checker traversed it. Two real failing runs were observed. Fixture allocation now uses the launcher's private TMPDIR, without changing production ownership or writable-ancestor admission. The final gate passed with that correction. Earlier failure evidence is retained.

The nine whole-suite skips were TestScratchAgent, TestRealBackendConsoleBrowser, TestRealBackendConsoleDiagnostics, TestRealPebbleNginxJobs, TestRealNginxRenderPublishRollback, TestRealNginxPrimaryRekeyJob, TestRealPebbleSchedulerMutationAcceptance, TestPrimaryTLSKillHelper and TestUpdateSignedDispatch. Root UID/GID source fixtures ran here and were not skipped. A distinct ordinary-user run was not performed.

Disposable-VM manager encoding/cgroups/cleanup, real UFW access restoration, reboot, clock rollback and power-loss acceptance remain deferred. Fake manager scheduling is source acceptance, not proof of real manager timing. Mandatory preflight before SQL, absent production Phase 6 migrations, retained terminal reconciliation gate and closed public apply/confirm/revert behavior remain unchanged. Exact-commit independent re-review is still required before publication.
