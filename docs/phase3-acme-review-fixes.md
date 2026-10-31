# ACME independent review fixes

- Renewal reads the old installed pair through the existing bounded, no-follow, single-version loader. It validates the key, chain structure, server usage and primary identity without requiring the newly expanded SAN set. Expired certificates are permitted only as renewal sources; the API returns public PEM, not a usable TLS state. Serving, uploads, status and force-HTTPS retain current-date validation. Fresh orders still request all current names with an ARI replacement identifier and validate the new certificate normally.
- `CommitTLS` persists canonical unix-millis results in the same metadata/replay-marker/intent-deletion transaction. Issue returns `{fqdns,not_after}`, renewal returns `{not_after}`, and committed execution copies the saved result without contacting ACME. Upload behavior is unchanged.
- The executor maps only the durable policy exhaustion sentinel to `rate_limited`; policy reservations still precede all CA requests. The RPC dispatch/client catalog accepts that canonical code. Other policy/storage failures retain their original handling.

## Merge boundary

`internal/store/tls_publication.go` changes only result construction immediately after domain metadata update, originally lines 51-52. It initializes the renewal expiry result and extends the existing upload FQDN branch to issue. No transaction, primary-promotion, journal or schema changes are intended. Preserve these result shapes if the parallel rekey work touches this block.

## Verification

Regression tests exercise production Dispatch, Queue.Run and Server.ExecuteJob with scratch SQLite, scratch TLS/nginx files, a fake runner and a loopback TLS CA signing lego's CSR. They check SAN expansion, expired old pairs, canonical persisted/replayed results, repeat throttled attempts with zero CA requests, and retry after CA failure. Retained-source negative tests cover future dates, wrong primary identity, mismatched keys, unsafe modes and symlinks. Baseline execution failed for missing results, SAN expansion, expired renewal and the wrong throttle code.

Real nginx/systemd, live CA requests, Pebble and production resources are not exercised. No schema, console or rekey changes are included.
