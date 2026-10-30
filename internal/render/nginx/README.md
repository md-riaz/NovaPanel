# TLS renderer handoff

## Agent wiring

- Load an installed pair with `LoadTLS(cfg.TLSDir, primaryFQDN, staging, now)` and assign the returned opaque `*TLSState` to `Desired.TLS`. The clock argument is a `time.Time`. Staging provenance must come from agent-owned issuance metadata, never RPC input or certificate labels. Manual pairs use `staging=false`; public-root trust is not imposed on manual uploads.
- Set `Desired.ForceHTTPS` and `Desired.HSTSMaxAge` from persisted approved state. The verb owner must enforce all-domain coverage before initially enabling force-HTTPS, and owner-only authorization for explicit HSTS changes. An alias added afterwards is HTTP-only until the installed SAN covers it, even while the existing covered domains remain forced.
- `Render` remains pure. Its TLS state is a validated snapshot at the supplied load time, not a promise of future validity. `Applier.Apply` reloads and revalidates installed state under its mutex before rendering, including unchanged-config calls. `Applier.Now func() time.Time` defaults to real time and supports fixed-clock tests.
- Set `Desired.ModernHTTP2=true` only after startup version detection proves nginx >=1.25.1. The default uses Ubuntu 24.04's legacy `listen ... ssl http2` syntax.
- Replace the agent's HTTP-only callback with `StateProbe("http://127.0.0.1:80", "https://127.0.0.1:443", nil)`. Existing `HTTPProbe(endpoint)` calls remain compatible for HTTP sites and fail closed if used for TLS state. Custom TLS configuration is cloned; pass test CA roots or explicit test-only relaxed verification for pebble, never relax production verification globally.
- Set `Desired.ForceReload=true` after certificate installation/renewal. It bypasses the byte-equality no-op, but still validates, reloads, probes, and promotes the successful configuration to LKG.

## Guarantees and ownership limits

No certificate means HTTP only. A loaded certificate enables HTTPS only for covered domains. Forced HTTPS requires a non-staging loaded pair, preserves HTTP ACME locations, and emits HSTS only on HTTPS when HSTSMaxAge is positive. Canonical 02 defines zero as no HSTS; the mutation owner must persist 300 for the initial approved HSTS action. No preload or includeSubDomains is generated. HTTP and HTTPS probes run for all relevant domains, use Host/SNI, never resolve site DNS, never follow redirects, and reject failed connections or unexpected 5xx. ACME must return exactly 200 or 404. Maintenance 503 is allowed only on the page probe.

The caller must serialize installed `current` swaps with apply and retain/restore the prior certificate version if reload or probe fails. Config LKG restoration alone cannot restore a certificate symlink. The TLS store currently exposes neither a pinned-version path result nor a transactional swap/apply/restore operation; that integration remains outside this package. TLS metadata persistence, ACME, startup version detection, owner authorization, site-to-Desired mapping, and renewal scheduling remain with their owners.

Tests generate ephemeral certificates and use scratch files, loopback httptest listeners, and fake command runners. No real nginx reload, systemd, ACME endpoint, or host site changes are exercised. This is not the full Phase-3 integration acceptance gate.
