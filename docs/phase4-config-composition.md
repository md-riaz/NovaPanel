# WordPress config composition and operation custody

## Bounded delegated ruling

This slice chooses a pure agent-owned config generator instead of executing
`wp config create --skip-salts`. It implements that step's fixed configuration
semantics without parsing a site file or running PHP to build configuration.
This is a new bounded implementation choice under the delegated runtime-signature
ruling, not a claim that the canonical reference command has changed. The pure
five-step plan remains unchanged and is not an executable capability.

`configcustody.ComposeConfig` emits a fixed PHP configuration with generated DB
names, TCP 127.0.0.1, utf8mb4, wp_ prefix and eight operation-owned local salts.
Single-quoted PHP data escapes backslash and quote. Neither templates, extra PHP,
remote salts nor client configuration are accepted. `GenerateWordPressCredentials`
generates independent 32-byte base64url passwords and salts before enqueue seals
the existing complete reservation and authority envelope. The pure generator
consumes these sealed equivalent typed fields deterministically on every retry.

## Concrete integration

`Queue.CreateWordPressConfig` opens the original sealed operation, derives the
binding and complete config, calls the approved sealed no-replace publisher, and
only then advances db_applied to config_published. The original Queue.execute
mints an unexported expiring worker lease only after acquiring the persisted
complete operation lock set. Direct calls and retained contexts after worker
return cannot authorize config creation. Startup Queue.Run recovery uses the
same execution path, IDs, salts, password and custody record.

A nonnil trusted `ConfigIsolation` dependency is mandatory. It must resolve and
match the non-login account, exclude unverified docroot entries, quiesce site-UID
writers and prevent trusted root relocation throughout the synchronous effect.
There is no default isolation callback and no production wiring. Scratch tests
provide quiescence by owning an otherwise unused filesystem tree with distinct
numeric UID/GID and no processes for that site. They create no host account.
This does not attest the current pathname after a held-dirfd operation. Site-owned
0400 is not isolation against the file owner. Root ownership, full ancestor
no-follow checks, cross-UID refusal and no-overwrite behavior are unchanged.

Once publication can have begun, failures return sanitized interrupted so the
existing operation owner retains its sealed inputs and original claim reservation
until recovery or the unchanged 24-hour expiry. A swallowed effect error cannot
report success. The adapter does not delete files, including unmanaged entries.
It retains the authenticated file record as required by the approved helper.
Terminal file-record reconciliation/removal is still a separate operation-owner
integration requirement; this slice does not add a permanent credential vault or
claim automatic file-record expiry. SQLite terminal input destruction and F19
projection remain in the existing atomic terminal transaction.

## Verification and remaining gates

Tests-first compile failures were captured for missing composition and worker
APIs; a behavioral RED caught swallowed publication failure before its fix.
Supplementary integration tests exercise actual PHP 8.3 data roundtrip through
internal/exec, actual root/no-follow publication, a SQL checkpoint failure after
rename, queue cancellation, SQLite close/reopen, Queue.Run original-operation
recovery, unchanged bytes, terminal input removal and owner one-shot projection.
The root test subprocess chroots into scratch using the existing exec boundary.
It copies the actual boot ID and its own /proc stat for store lifetime identity;
it does not mount proc, weaken the store, or change the host. Root fixture and
PHP tests explicitly skip when those dependencies are unavailable.

Public productionWP remains unsupported. Missing staged artifact custody, verified
archive/download/checksum routing, actual account quiescence/provisioning, reviewed
PHP ini, database application, core-install, PHP publication/probes and terminal
file-record reconciliation remain runtime activation blockers. No WordPress
artifact was executed. PHP execution here loads only generated config and a trusted
scratch assertion bootstrap, not WordPress. Final VM/power-loss acceptance remains
deferred and unpassed. No source publication or host service changes are included.
