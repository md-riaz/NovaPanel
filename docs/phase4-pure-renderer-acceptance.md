# Phase 4 pure renderer acceptance

## Scope and history

Local branch `integration/phase4-pure-contracts-20261031` starts at the approved stacked base `936d848024db3f38ff101757d2a71cb962879d56`. Only the requested source patches were cherry-picked:

| Source | Integrated | Stable patch ID |
| --- | --- | --- |
| `93fe16a916ca3b6c9ee5ed889bf561c352591359` | `60c3f5424d95bd6dce98a3edb3251109ed5f44cb` | `606b92670f6a7da77c52f00dfd43ae22c6164b24` |
| `13784d059efb9793442a82b5a9f3803b5ea6b883` | `47725fdd1552c72dfd48bbf975cd6b86411e0acf` | `b8187e59f7facf5fac3fe6aad1f0e1b876366668` |

Author identity/date and canonical base ancestry are preserved. The candidate SQL remains under testdata, agrees with canonical DDL, and does not change the five shipped migrations or hashes. No runtime handlers, production migration, deployment, PR, push, host service or public CA action is included.

## Real engine checks

Run `python3 tools/phase4_renderer_lab.py` from a clean committed worktree with Docker and the repository's Go toolchain on PATH. The launcher refuses uncommitted inputs and records its exact HEAD and image metadata in a unique directory under TMPDIR.

Pinned engines:

- PHP image `php@sha256:d41f4e57e97732698eddc7e17dcc75027ebf4f549aaf3724894a9f9ff062f8ba`, image ID `sha256:dc52c57f2ace56a14a116518a49c36f76118c324175af02840c8882c3277467e`. Observed PHP 8.3.31.
- Official MariaDB 10.11 image downloaded and pinned as `mariadb@sha256:d769e37960821dfdcf096954f6e7ac89f747e116c1bba98e48edd265dfc7deae`, image ID `sha256:72b6ab51626b71b6118295b90c9408e537ff1cfdaa0b35c9930e8c2ee49a47bf`. Observed MariaDB 10.11.19 on Ubuntu 22.04, not the final Ubuntu 24.04 deployment.

Every engine container runs nonroot with all capabilities dropped, no-new-privileges, read-only rootfs, network none, no published ports, host mounts or devices, 768 MiB RAM with no extra swap, one CPU, 96 PIDs and bounded tmpfs. MariaDB's image-declared data volume is covered by tmpfs, not an anonymous disk volume. The launcher inspects confinement before/after and verifies exact-name removal.

- FPM `-t` accepts the exact rendered pool plus scratch global settings. A second config changes only the production socket/log paths to scratch and starts FPM as UID/GID 33. Its socket is `33:33 0660`. This is a same-UID test, not evidence of root dropping to a distinct site UID or fixing cross-UID nginx access.
- MariaDB boots as UID/GID 999 using disposable tmpfs data. Its isolated bootstrap SQL root account has normal authentication and no password. This deliberately does not model production root unix_socket authentication.
- The real renderer's CREATE/GRANT/DROP SQL executes without changing sql_mode. Observed mode is `STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION`.
- The generated account connects through TCP 127.0.0.1 with an ephemeral generated password in a 0600 container tmpfs client file. Passwords and SQL input never appear in argv or command logs.
- Own-database CREATE/INSERT/SELECT succeeds. SELECT against the underscore-replaced lookalike, unrelated database and mysql.user fails with MariaDB error 1142. SHOW GRANTS confirms the literal escaped database pattern and scoped privilege list. The lab does not claim full WordPress privilege compatibility.

First successful engine evidence at `f0b11270e2cb41d31ee350df813ecd5e8b29ef40`: `/root/.hermes/cache/scratch/phase4-renderers-o7os9upg`. Initial readiness error 2002 and bootstrap hostname warning are retained. The readiness retry then succeeds. Confined race-built PHP/database/store package binaries also pass at that SHA in `/root/.hermes/cache/scratch/phase4-contract-gates-r1rl65pn`; build, formatting, vet and architecture backstop pass. Earlier gate attempts retained Docker-copy/read-only and default-tmpfs-noexec launcher failures, not code failures. Source transfer was changed to nonroot stdin streaming into bounded executable scratch tmpfs, without weakening rootfs/capability/network restrictions.

## Canonical-main reconciliation remains separate

Remote-tracking main observed at `df67e2e98ac3a4aa6f476fece364c797adf3d6cc`, whose #86 diff was read. It adds ssl_auto to ssl.status, redirects to site.get, and permits idempotent sealed-input recovery with an intent journal while retaining effectful interruption semantics. The stacked base already carries both reply fields. Its older F6 generic secret-input paragraph still needs the new recovery-class wording at reconciliation.

Do not replace stacked canonical files wholesale with remote main: that would discard approved Phase 4 shapes/DDL and Phase 3 audit, admission and primary-promotion amendments. The D2 trust-anchor amendment at `c4aa352` is also absent from this stack and must be retained during reconciliation. After the actual authorized PR81 merge, record the real main SHA, rebase/reapply the pure patches and planning amendment as appropriate, reconcile F6 explicitly, compare patch IDs and canonical fixtures again, then rerun all exact-head gates. This branch neither performs nor pretends that merge or publication.

## Unpassed deployment boundaries

Root unix_socket admin authentication, production FPM pool/socket custody, distinct worker/site identities, cross-site filesystem isolation, actual nginx PHP responses, privileged apply/reload, crash-safe terminal job/claim/journal integration, WordPress installation/checksums and final Ubuntu VM acceptance remain unpassed. No operational Phase 4 opening or release completion follows from these local checks.
