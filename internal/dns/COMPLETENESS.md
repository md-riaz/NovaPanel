# Phase 7 contract audit at 5f4bc1e

This is an implementer escalation, not an approved contract amendment. Production IntegrationProbe must remain nil and integration.set must remain unsupported. No new public verbs or migration are proposed for implementation here.

## Six contract-gap classes checked before code

| Class | Canonical evidence | Disposition |
|---|---|---|
| Error codes | 00 F3 lines 109–136; phase-7 lines 52–56; attestation note lines 24–38 | Exact-scope admission has no accepted evidence design or complete refusal mapping. Nil production probe remains unsupported; experimental handler mapping is not a new production policy. |
| Show-once secrets | 03 lines 168–174; 00 F12 lines 509–518 | integration.set consumes a token and returns only integration_id. No new show-once output is needed. |
| Stateless-web reads | 03 lines 168–174; 02 lines 377–381 | Existing DNS read shapes are explicit, but there is no integration inventory/test/delete/rotation verb. Do not invent one to select or remove credentials. |
| Throttle policy | 01 lines 77–80; 00 F6 lines 294–334 | DNS mutations are non-effectful jobs and must safely recover. No durable ambiguous-create identity/reconciliation contract is pinned. A transient POST success or failure does not make replay safe. |
| Config wiring | 00 F16 lines 666–691; phase-7 line 28 | Resolver and expected public IP production configuration remain explicitly unpinned. Keep trusted injected dependencies only. |
| Authz exceptions | 01 line 81; 02 lines 370–381 | integration.set is admin/MFA, not PAT-reachable. Exact lifetime plus zone and shared removal locks are required. Zone-only dispatch must reject ambiguity before provider access; rotation/removal and queue locking need an approved design. |

## Contradictions and missing decisions

- phase-7 lines 54–56 require Zone:Read + DNS:Edit + Cache Purge on exactly the selected zones and validation by probe. Lines 69–70 and 104–105 still describe the narrower DNS-edit-only, one-zone model. The resolved flow wins, but the head should reconcile stale prose.
- phase-7 lines 15 and 91 say records are jobs; line 92 and 01 lines 77–80 distinguish synchronous list/verify. Follow 01, not the broad prose.
- phase-7 lines 16–18 and 103 prohibit proxying mail/auth records, but 03 line 165 exposes only record type and proxied, without a trusted web-purpose classification. A/AAAA/CNAME can also serve mail. A type allowlist cannot prove the stronger requirement. No hostname heuristic is approved.
- 02 lines 383–384 prohibit Phase 7 migration 0008 before approved 0006 and 0007. Scratch DDL tests do not satisfy production installation.
- 02 lines 377–381 allow multiple integrations per zone and require shared removal locks. 03 lines 168–174 contain no lifetime selector or removal verb. The worker must resolve and persist the exact lifetime internally; its atomic admission, mutation journal and removal protocol remain unspecified.

## Minimum-scope provider evidence

Official documentation was fetched again without credentials or API execution:

- [User token verify](https://developers.cloudflare.com/api/resources/user/subresources/tokens/methods/verify/) returns ID, status and optional validity times, not policies.
- [User token details](https://developers.cloudflare.com/api/resources/user/subresources/tokens/methods/get/) requires API Tokens Read or Write.
- [Permission-group catalog](https://developers.cloudflare.com/api/resources/user/subresources/tokens/subresources/permission_groups/methods/list/) also requires API Tokens Read or Write. It describes available permissions, not this token's effective grants.
- [Account-owned token details](https://developers.cloudflare.com/api/resources/accounts/subresources/tokens/methods/get/) requires Account API Tokens Read or Write. Changing to account-owned tokens therefore does not establish a minimum-zone-grant self-attestation path.
- [Account-owned token verify](https://developers.cloudflare.com/api/resources/accounts/subresources/tokens/methods/verify/) likewise reports token validity, not its exact grants or resources.

Successful reads establish access to those resources at that instant. They cannot prove DNS Write or Cache Purge, nor exclude access to other zones. A transient request failure cannot establish absence of a grant either. No successful minimum-scope attestation was observed or is claimed.

## Minimal UNAPPROVED options for the head

1. Retain the exact minimum grants and explicitly accept only disabled production admission plus experimental parser coverage for now. This does not complete Phase 7.
2. Define a separately trusted administrative attestation design without broadening the executing token. Before code, amend 00/02/03 and the phase to pin issuer authentication, token-ID binding, exact grant/resource representation, trusted metadata, evidence custody, expiry/revalidation, revocation, race handling and every refusal code. This is a new trust boundary, not an available fallback.
3. Establish a documented provider-supported read-only self-attestation path that works with only the existing grants, then amend canonical contracts. The five official endpoints above do not provide it.

No extra credential, broader executing-token privileges, dashboard assertion, trial DNS write or cache purge is authorized. Staging DNS-01 and final live-VM acceptance remain unpassed.

## Independently unblocked adapter work

03 line 165 specifies complete-record replacement, while the provider adapter must validate returned record identity. Update previously checked the ID but not the returned owner or type. The bounded external-package regression now requires refusal of a same-ID response for an unrelated owner or type, using only synthetic HTTP. The fix adds the same type and case-insensitive owner checks already used by Create. It needs no admission activation, schema or shared consumer change.

## Earlier token-format candidates

b50984e01db350237ad42f9a372abd1f092faf48 is already an ancestor of the published head. e82aac207d0b90dcee5601a4fc6f1631321beb20 is already present as 378ae5d85ac6608d99c44c22d33fbd8c967d19c3 with matching stable patch ID ddc1a8a94052c3ff4c65c15c7c80e567a07b0d0e. Neither should be applied again. This inventory does not replace independent redaction review.
