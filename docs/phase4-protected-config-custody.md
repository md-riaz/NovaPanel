# Protected WordPress config custody

This slice implements file publication and restart recovery only. It does not open PHP execution or register a WordPress runtime consumer.

## Internal delegated ruling

Canonical Phase 4 requires site-owned 0400 no-follow atomic publication, sealed operation secrets, and restart exceptions limited to the owning journal. The existing queue already seals the reserved WordPress identities and credentials. The missing file consumer needs its own recoverable custody record, not a change to shared operation terminality.

Use an agent-owned 0700 custody directory on the same filesystem as the docroot. Before any publication, persist a sealed envelope binding the exact job, site, database and account IDs, resolved UID/GID, docroot and expected config bytes. A root-owned 0600 encrypted record is the only durable recovery input. The only plaintext file is the intended protected config or its private publication temporary file. Do not put configuration, passwords or salts in job fields, replies, audit or errors.

The trusted caller supplies complete config bytes after the approved config-create and local-salt steps. This API does not validate PHP or authorize those steps. It must not accept client config bytes. A later runtime consumer must hold the persisted operation's complete lock set, check its authority/expiry, resolve the non-login account, exclude unverified docroot entries and quiesce the site UID throughout publication. A site-owned 0400 file does not resist its owner changing its mode. Descriptor custody alone cannot replace that execution isolation.

Open every absolute path component without following symlinks. Custody ancestors are root-owned and not group/world writable. Site ancestors retain that policy until the final home and docroot. Home is the resolved site UID with 0751 mode; docroot is that UID with 2750 mode. The configured root remains agent-owned. Refuse all existing config files before preparing a new record. Restart requires the identical binding and bytes from the original sealed record; conflicting files, hardlinks, modes, ownership or IDs fail closed. Never overwrite a destination. A rename followed by cancellation or directory-sync failure retains the encrypted record; same-ID reopen/retry confirms exact bytes and syncs both directories before success. The record is retained for the operation owner to reconcile and eventually clean up. This slice does not invent independent job completion or expiry cleanup.

The exported entry point always enforces root custody. Tests use a private owner substitution and injected file-boundary operations against real scratch files. Those tests do not establish power-loss durability, real site-UID isolation or final VM acceptance.
