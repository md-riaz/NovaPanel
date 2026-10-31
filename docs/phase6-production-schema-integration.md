# Phase 6 production schema integration

This bounded schema-only stack starts at `c47fdfffce9478bfd8cf5e8c2a14327398ae6f77`. It does not complete Phase 6 or permit public firewall activation.

## Immutable predecessor

Only `internal/store/migrations/0006_phase4.sql` is carried from published PR87 commit `2581e7c86376032776197f8da3a80e34cbbc1a38`. Its SHA-256 is `549740ffa8b38ce925b524ad82013b8df75ff97c0346b6f7b3679ef267639698`, identical to foundation `42dfcae0f9ec92989a5a82629c03a24a3ef0c8a2`. No Phase 4 Agent, jobs, platform, executor or startup implementation is imported. Earlier migration bytes and hash meanings remain unchanged.

Production `0007_phase6.sql` has SHA-256 `405e1a61297ac2123ca905b1309be6cf9e34a01e7c34a976646298902102a055`. It contains the entire canonical section-02 schema for repos, backups, fw_pending and webhook_deliveries, plus repos_one_panel. Phase 8 must reuse webhook_deliveries.

PR85's alternative version 0006 has hash `af263cda18b1affbe82be79095b32bdb5d0a835278db2284893f56ed56be24a0`. Such scratch history is incompatible and Store.Open refuses with database hash mismatch. No ledger repair or session-index migration renumbering occurs here. Its owner must independently select the next verified free version before integration.

## Exercised interfaces

- Ordinary Store.Open installs fresh databases and upgrades exact 0005 and 0006 prefixes, preserves seeded identity, audit, schedule, site/domain and applicable database/user relationships, verifies migration hashes and reopens.
- Migration inventory is validated before the writer transaction. Numeric versions must be unique, filenames canonical, and declared hashes complete with no orphan entries. An external embedded-FS overlay exercises actual Store.Open rejection.
- Mid-DDL failure and deferred-foreign-key COMMIT failure roll back the entire migration. Independent SQL reopen checks for absent partial objects/ledger before removing the injected fault and retrying.
- Actual JournalRecovery Confirm/Revert and InstalledRestore produce outcomes for both prior-enabled values. Ordinary OpenState supplies production schema, Audit appends exact recovery detail, and Agent.CommitFirewallTerminal consumes only the matching projection. Journal and immutable snapshot remain retained, repeated consumption runs no manager/restoration commands, and Store lifetime exclusion remains intact.
- Owned child process tests pause after actual durable file append and actual SQL commit, kill through internal/exec, then reopen through ordinary OpenState and consume the same outcome. Fresh and historical-0006 variants use real embedded migrations without positive fixture DDL. Helper publication also runs while a parent retains the Store lifetime lock; the parent releases it before daemon state open.

All command effects are injected fixed-argv runners against private scratch files. The missing-schema negative uses a test-only prior-migration overlay, never deletion of production tables in positive setup. The retained testdata schema is a historical contract reference only.

## Remaining work

The schema slice originally left occupied startup closed. The subsequent terminal-only lifecycle now reconciles eligible outcomes and verifies outcome-preserving v2 receipts before serve; see phase6-terminal-startup-lifecycle.md. Pending, legacy ambiguous receipts and orphan SQL projections still refuse. No archive, deletion or new arm is allowed. Public FirewallRecovery remains unset and firewall.apply unsupported. Full apply, client freshness/authorization and actor audit, backup/restore/verification, self-backup and updater consumers remain source work.

Real UFW/systemd, cgroup cleanup, external access, append-only enforcement, reboot and power-loss testing remain deferred final-product acceptance. Root scratch installed fixtures do not establish ordinary-user behavior or real privileged VM acceptance. Parent owns independent review, final exact-head gates and any publication; no push or merge is performed by this implementation task.
