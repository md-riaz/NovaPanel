# Internal journal recovery

This slice adds an internal synchronous consumer, not public activation. The fixed context-aware production opener owns journal and immutable custody handles under the root-owned installed layout. Callers must not race Close with operations. Journal's stable-inode flock remains the decision authority across handles and processes.

Confirm delegates admission and publication to Journal.Confirm. Only after durable confirmation does it stop the exact timer and verify manager readback. Completion has a fresh ten-second deadline derived with context.WithoutCancel, retaining request values. Postcommit manager failures preserve ErrConfirmationCommitted. Same-ID confirmed retries remain deadline-limited.

Revert delegates to RevertVerified and shares the helper's lazy installed restoration callback. It synchronously restores verified descriptor bytes, prior enabled state and durable bound marker. It does not require helper cgroup admission because it is an internal trusted-root consumer; the independent installed helper still requires exact service cgroup admission. Recovery uses the recovery-specific child-lifetime Runner. No timer or service is stopped during Revert. Interrupted reverting can finish after expiry; terminal manual retries refuse.

No SQL, audit reconciliation, acknowledgement, archival, new-arm policy, migration, RPC, UI or ordinary Agent.FirewallRecovery wiring is introduced. All retained states still block startup. Tests use owned scratch roots and injected manager/UFW runners only. Process interruption establishes filesystem replay, not power-loss durability. Real manager/UFW, cgroup descendant cleanup, reboot and power-loss acceptance remain deferred final-product VM checks.
