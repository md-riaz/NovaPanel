# Certificate publication

```go
token, err := tls.Stage(root, primary, validatedCertificate)
// Handle err; persist token in trusted caller-owned recovery state if needed.
err = tls.Publish(token)
// Handle err; current may already have changed if the directory sync failed.
// Apply nginx configuration, force reload even if rendered bytes are unchanged,
// then probe. On failure, restore the certificate pointer BEFORE reloading LKG:
err = tls.Restore(token)
```

APIs:

```go
type Recovery struct {
    Root string `json:"root"`
    Primary string `json:"primary"`
    Version string `json:"version"`
    Previous string `json:"previous"`
}
func Stage(root, primary string, c *Certificate) (Recovery, error)
func Publish(token Recovery) error
func Restore(token Recovery) error
func Install(root, primary string, c *Certificate) (string, error)
```

- Stage syncs the pair and directories without changing `current`. Previous is empty on first install. Version directories are retained; no garbage collection runs here.
- Publish and Restore validate confined, owner-only storage and relative ULID versions. They use an advisory directory lock to serialize cooperating pointer writers and return `conflict` if `current` no longer matches either the expected or already-completed target. Repeating an already-completed operation is safe while the retained pairs remain intact.
- Restore removes `current` for first-install recovery. Restore does not reload nginx, restore config, or update database metadata.
- The token has no PEM bytes and can round-trip through JSON. Its storage coordinates are trusted internal input, not RPC arguments. Callers must retain the token and serialize the complete publish/apply/probe/restore sequence across the affected site. Directory ancestors must remain owner-controlled and stable.
- Pair-file checks enforce ownership, type, mode and bounded size. Use Load with execution-time domains and an injected clock to validate certificate content; publication does not invent a clock or reject an expired prior pair needed for rollback.
- Install remains a Stage followed by Publish convenience API. Concurrent incompatible installs may now return `conflict`; use explicit tokens for recovery.

## Crash limits

Successful Stage syncs both files, the version directory, site directory and storage root. Successful Publish/Restore syncs the site after pointer rename/removal. A post-rename sync error does not imply the pointer stayed unchanged. Keep the token and recover explicitly.

This package has no durable transaction spanning TLS, nginx config, loaded workers, job state and certificate metadata. A process crash before the caller durably records the token can leave an orphan staged directory. A crash after publication can leave the new pointer without a verified reload/probe. The integration owner must persist intent before Publish, retain the old pair, and choose recovery or roll-forward from trusted intent on restart. No automatic startup recovery or power-loss fault-injection guarantee is claimed here.
