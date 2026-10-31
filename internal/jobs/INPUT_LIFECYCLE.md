# Sealed job input consumer contract

This implements the F6 input lifecycle using the existing `jobs.args_json` TEXT column. No migration or TLS dependency is needed.

## Run / enqueue

- After dispatcher authorization and TLS validation, resolve the parent site from persisted state and call `a.Jobs.EnqueueSealed(ctx, "ssl.upload", args, "site:"+siteID)`.
- Pass the complete payload, including `site_id`, `cert_pem` and `key_pem`. Do not pre-marshal secrets into any persisted resource row. Do not use plain `Enqueue` for secret-bearing inputs.
- Keep canonical `ssl.upload` registry `Effectful=false`, MFA=true and sites:write. This helper neither authorizes verbs nor changes trusted metadata.
- The queue seals before `CreateJobWithClaim`. The versioned JSON envelope contains nonce and ciphertext only. The encrypted payload binds the generated job ID, verb and object key, preventing ciphertext substitution across jobs or resources.
- Enqueue audit and claim reservations use the existing paths. Job JSON excludes Args. Never log input args or custom marshal errors.

## Exec

```go
raw, err := a.Jobs.OpenInput(j)
if err != nil { return err }
defer clear(raw)
// Decode raw into private execution-local args. Revalidate the certificate and
// key against persisted site domains and the execution clock before installation.
```

`OpenInput` accepts only the bound versioned envelope. It does not mutate `j.Args`, so a later `UpdateJob` cannot accidentally persist opened bytes. It returns generic errors without decrypted data. Consumers must not copy plaintext to job Args, Result, Step, Error, event text, audit, logs or web state. Clear decoded byte buffers too; Go strings and decoder copies cannot be reliably erased.

## Recovery versus terminal cleanup

- Process shutdown/context cancellation leaves the job running with ciphertext intact. This is interruption, not an explicit terminal cancellation.
- Restart keeps ciphertext. Idempotent jobs, including ssl.upload, return to queued; effectful jobs become failed/interrupted without automatic execution. An UpdateJob carrying failed/interrupted also retains ciphertext.
- `store.UpdateJob` atomically replaces the envelope with `{}` when state becomes done or failed with a code other than interrupted. This includes executor errors and recovered panics through the existing worker.
- A future explicit job.cancel handler must use the existing failed state, a catalog code other than interrupted, and UpdateJob after its authorization/audit and safe cancellation coordination. There is no canceled schema enum and this change does not implement cancellation RPC or invent one.
- Plain non-envelope args remain unchanged by terminal updates. Cleanup checks durable args, not the executor's mutable copy.
- Destruction means the live row no longer references ciphertext. SQLite WAL, backups and free pages may retain old ciphertext; physical secure erasure is not promised. Plaintext is never inserted.

SSL RPC registration, validation/install integration, retry UI, explicit cancellation coordination, and ACME are outside this queue/store change. A handler that bypasses EnqueueSealed remains responsible for violating F6; store does not infer TLS fields or perform crypto.
