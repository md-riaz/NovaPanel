# Phase 3 SSL integration status

This is an implementation handoff, not approval of a new schema or a change to F6/F8.
No migration changes are included.

## Available RPC contracts

- `ssl.status{site_id}` is synchronous, read_only, `sites:read`. Its source and force_https fields report persisted desired state. Per-domain issued status requires loading and validating the actual pinned installed pair at the agent clock. A missing, unsafe, malformed or expired pair never reports issued. This read does not mutate metadata.
- `ssl.auto_set{site_id,enabled}` is synchronous, admin, `sites:write`. Enabled is required. It updates the active site's existing `ssl_auto` preference only. It does not issue, schedule, change `cert_source`, change coverage, or reload. Renewal consumers must independently require `cert_source='letsencrypt'`; a manual certificate must never auto-renew even if this preference is true.
- `ssl.upload{site_id,cert_pem,key_pem}` is admin, `sites:write`, MFA, slow, non-effectful. Run validates against persisted active-site domains and seals the complete input before enqueue under `site:<site_id>`. Exec opens only the job-bound envelope, checks verb/object-key/parent/lifecycle again, and revalidates against execution-time domains and dates. Terminal cleanup uses the foundation's existing UpdateJob path.

## Explicit publication blocker

Upload Exec currently returns `precondition_failed` after validation, before Stage or any filesystem publication. Therefore a successful enqueue is NOT a successful certificate install. This is deliberately fail-closed and Phase 3 is not complete.

The foundation's Stage/Publish/Restore API persists complete versioned pairs but explicitly does not coordinate nginx or database state. Current nginx Apply automatically restores config and reloads on probe failure before its caller regains control. Calling Restore after Apply returns would reload the prior config against the newly published certificate. This is the wrong recovery order.

Required before enabling publication:

1. Approve and document an internal durable job checkpoint format using existing step/result_json fields, or an explicitly approved alternative. Checkpoint coordinates are agent-generated, not RPC-controlled, and must contain no secret material.
2. Persist the Stage recovery token before Publish. Retries must read the durable token instead of staging a fresh version. Bind it to the current site, primary, configured TLS root and original domain set. Treat malformed/inconsistent checkpoints as refusal, never silent restart.
3. Add a serialized nginx/TLS publication operation that restores the previous certificate pointer BEFORE config restoration/recovery reload. It must hold the shared nginx apply lock for this entire sequence, preserve first-install absence, and handle cancellation with a bounded detached recovery context.
4. Coordinate successful probe, LKG promotion, site cert_source/manual and per-domain metadata, and terminal job publication across every crash boundary. Replayed successful jobs must not reload again. Do not overwrite the checkpoint with the public result until recovery no longer needs it.
5. Cover crash after Stage, checkpoint, Publish, reload, probe, metadata commit and terminal audit with scratch restart tests. Keep secret cleanup on terminal errors and retries. Keep unrelated site mutations and reconcile from applying HTTP-only state over an unresolved TLS publication.

No live nginx/systemd operations or ACME requests were made. Real-service and VM acceptance remain unperformed. ACME and console workers own separate deliverables and are not part of this commit.
