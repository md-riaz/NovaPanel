# Combined primary console and ACME integration

## Included

- Base `7433bf010201aa2baec79ddc9e92b271058dc4b6` includes the console and canonical `site.update.primary_domain_id` contract.
- ACME patches from `081a76cc2176c99197a4b6b3567133133082d245` were selected in order: `eb167f9`, `a6bf21a`, `72fd8ce`, `081a76c`.
- Publication commit `a6555c8` was deliberately omitted. Its stable patch ID equals already integrated `e7d0a3d` and original `c8bc09c`: `6ea0f94794bc2141a4697df3aeffc1d56053bba4`.
- Site updates, redirects and domain additions/removals retain installed TLS through `nginx.LoadTLS`. Actual SAN coverage partitions covered HTTPS and uncovered HTTP hosts. Persisted coverage labels do not establish certificate validity.
- Initial force-HTTPS enable requires the current pair to cover every domain, with non-staging provenance. Later aliases do not remove forced HTTPS from already covered hosts. HSTS still requires owner authorization and forced HTTPS.
- Same-primary update is a no-op, including with TLS. Real TLS primary changes remain refused at admission, execution and the atomic store boundary.

## Remaining blockers

### TLS primary re-key

The version-1 `tlsIntent` is an unchanged-primary publication journal. Recovery compares `Token.Primary` to the current SQLite primary, and `CommitTLS` does not atomically change primary IDs. It cannot safely perform a primary move.

A complete implementation needs a separate versioned intent or explicit extension binding old and new primary IDs/FQDNs, the unchanged domain set, old and new certificate recovery tokens, the nginx snapshot and all metadata to commit. It must validate the installed pair against the target without contacting a CA, retain the old primary's path and version directories, stage the same pair beneath the new path, persist intent before pointer/config publication, and atomically commit the primary plus job replay marker after the probe. Recovery must accept only the expected pre-commit or committed metadata state. Rollback must restore both pointer/config states before reloading old config. Restart tests must cover every boundary plus failed reload/probe/metadata commit. No existing SSL publication code was weakened to approximate this.

### Automatic SAN expansion on domain.add

Manual additions work as HTTP until a covering upload. Automatic additions also remain safely HTTP, but automatic expansion enqueue is not implemented here. The existing integration entry point is `ssl.issue{site_id,staging}`: its full-domain-set validation misses an uncovered alias and issues a replacement SAN pair through durable publication. The expansion must preserve the installed primary's staging provenance, not use the current global default.

Do not enqueue blindly from each domain.add retry. A durable parent-job/child-job binding is needed to avoid duplicate expansion jobs and issuance after crashes; domain state, enqueue intent and job replay need an atomic or retry-safe handoff. Child jobs must retain trusted identity/scopes and explicit effectful policy rather than inheriting the non-effectful domain job's flags. Coordinate this with scheduler work. No direct CA call belongs in the mutation renderer.

## API consumers

No verb shape changed. Console/CLI consumers continue using `site.update`, `domain.add/remove`, `redirect.add/remove`, and SSL verbs. Existing console primary selection now accepts same-primary TLS retries but still receives `precondition_failed` for a real TLS primary move. A successful domain.add result does not mean an uncovered alias is HTTPS-ready; consumers should read `ssl.status` and request explicit `ssl.issue` until the durable expansion handoff lands.

## Safety scope

Only scratch SQLite/files, generated local certificates, loopback fake CA tests and injected command runners are used. No host service command, public CA request, production deployment or push was performed. Opt-in integration and browser acceptance are separate from default Go gates.
