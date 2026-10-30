# Phase 2 — Auth & UI shell

## Goal
A signed-in owner/admin can use a hardened UI: Argon2id passwords, secure sessions, **TOTP MFA**, an
HTMX shell with uniform CSRF, and a read-only dashboard. Roles are owner / admin / read-only — no
tiers.

## Depends on
Phases 0–1 (RPC, store, audit, secret seal). Reads: `00-foundations.md` F4, F11, F12, F13.

## Deliverables
1. **Password hashing (Argon2id).** `session.login` verifies via `golang.org/x/crypto/argon2.IDKey`
   with OWASP params **m=19456 KiB, t=2, p=1**, 16-byte CSPRNG salt, 32-byte hash, stored as a **PHC
   string** `$argon2id$v=19$m=19456,t=2,p=1$<b64salt>$<b64hash>` (RawStdEncoding). Verify with
   `crypto/subtle.ConstantTimeCompare`. Transparent re-hash-on-login when stored params < policy.
2. **Sessions (agent-owned, F4).** Mint a 64-hex token (≥128-bit CSPRNG) on login; **rotate the token
   on every privilege change** (login included — session-fixation defense); idle timeout **10 min**
   (high-value panel), absolute **12 h**, both enforced agent-side; revoke = row delete.
3. **Cookie hardening.** `__Host-` prefixed session cookie: `Secure`, `HttpOnly`, `SameSite=Strict`,
   `Path=/`, **no `Domain`**. Never trust browser defaults.
4. **TOTP MFA.** Enroll (`mfa.enroll`/`mfa.confirm`), verify at login; **SHA-1 / 6 digits / 30 s**
   (Google-Authenticator-compatible), 20-byte Base32 secret **sealed by the agent** (F12), verified
   with `pquerna/otp` **`Skew = 1`** (±1 step, one-time use per window). **8–10 single-use backup
   codes**, stored only as Argon2id hashes, regenerable. MFA **required** for the F4 MFA-gated verbs.
5. **HTMX UI shell + CSRF.** Server-side **synchronizer CSRF token** delivered via a **custom request
   header** (`X-CSRF-Token` through `hx-headers`/`htmx:configRequest`), validated on every mutating
   request (constant-time), **not** carried in a cookie. Plus `Sec-Fetch-Site` check with Origin/
   Referer fallback (log-only rollout first). All state changes are non-GET.
6. **Roles** owner > admin > read_only, enforced in the agent per verb (F4/F13). Read-only dashboard:
   server status + metrics (read verbs only).

## Blueprint
- **`internal/verbs/session.go`** — `session.login{user,password,totp?}`, `session.logout`,
  `session.whoami`; Argon2id verify; token mint/rotate; idle/abs enforcement; audit every attempt
  (success + failure).
- **`internal/auth/argon2.go`** — `Hash(pw) string`, `Verify(pw, phc) (ok, needsRehash bool)`; PHC
  encode/parse.
- **`internal/verbs/mfa.go`** — enroll (generate+seal secret, return `otpauth://` for QR), confirm,
  validate (`Skew=1`, **persistent `mfa_used` replay set — 02 `0003_mfa.sql`, not an in-memory cache**),
  backup-code issue/verify. Enroll seals into `totp_pending`; confirm promotes it to `totp_secret` (02 `0003`).
- **`panel-web`**: session cookie handling (`__Host-`), the HTMX shell, CSRF middleware (synchronizer
  token in session + constant-time header validate in the web tier), `Sec-Fetch-Site`/Origin fallback
  middleware, login + MFA screens, dashboard. On a cookie-less first visit it calls the public
  **`session.start`** verb to mint the anon session + CSRF token before rendering login. On the **login
  POST** (anon session, where `whoami` is non-public) it calls **`session.start` again presenting the
  anon cookie** — idempotent, so it returns that same row's `csrf_token` (F4) — and constant-time
  compares the submitted `X-CSRF-Token` header against it **before** forwarding `session.login`. (For
  authenticated mutating requests the equivalent re-fetch is `session.whoami`.)
  **`panel-web` never hashes or stores passwords/secrets** — it calls the agent.
- **MFA-gate middleware** (agent-side): a verb marked `MFA` (F13) requires the session was minted with
  TOTP and is within a short re-auth window, else `code:"forbidden"` + `reauth_required`.

### Resolved details (B34, B33, C5; most of phase-2 is now settled by F4)
- **First-owner bootstrap + user management.** The installer/first-run creates the single `owner`
  (`user.create` run locally via `novapanelctl`, peer-gated, before any web login). `user.*` verbs:
  `user.create/list/update/delete/set_password` (owner/admin). `session.login` on an unknown user
  does a **dummy Argon2id** to equalize timing (no user enumeration); failures are uniform.
- **TOTP replay store** = a `mfa_used` set of `(user_id, step_counter)` in the agent DB (not memory),
  so a code already accepted within its window is rejected across restarts. Enrollment **forces** MFA
  setup at first owner login is optional; MFA is required only to *call* MFA-gated verbs.
- **QR secret exception (C5):** `mfa.enroll` returns the `otpauth://…` URI **once** for the web tier
  to render a QR; this is the one allowed moment `panel-web` handles the secret (in transit, never
  stored). The Guardrail "never sees a secret in the clear" means never **persists/logs** it — the
  password login form and the enroll QR are forwarding, not storage.
- **CSRF token** lives in the session row (`sessions.csrf_token`, F4), re-fetched per mutating request
  by the stateless web tier: via `session.whoami` for a `kind=user`/`token` session, and via the
  **idempotent `session.start`** for the `anon` pre-login POST (whoami is non-public to anon).
  `panel-web` echoes the submitted token in the `X-CSRF-Token` header and **`panel-web` compares it
  constant-time** against the fetched session value (F4 wins over earlier phase prose: the agent owns
  the session, the CSRF token, and all authorization, but does **not** receive or compare a CSRF header
  — there is no CSRF field in the F2 envelope). It is not a cookie and rotates with the session.
- **Sec-Fetch-Site** ships behind a config flag defaulting to **enforce** for non-safe methods (with
  Origin/Referer fallback); the "log-only first" note is an ops rollout aid, and the gate test sets
  enforce.

## External refs (gathered — verified values to honor)
- **Argon2id:** OWASP minimum **m=19456 KiB, t=2, p=1** (memory already in KiB — pass `19456`, don't
  multiply); `argon2.Version = 0x13` (=19, matches `v=19`). Library returns raw bytes only — you own
  salt gen, PHC encoding, constant-time verify. bcrypt only for legacy; **no justification here**.
- **Sessions:** `__Host-` prefix (forces Secure, forbids Domain, requires Path=/); session ID ≥64-bit
  entropy; idle 2–5 min for high-value (we use 10 as a balance), absolute 4–8 h example (we use 12);
  **session-ID regeneration on privilege change is mandatory** (fixation).
- **CSRF (HTMX):** OWASP — stateful server → **Synchronizer Token**, delivered via custom header for
  XHR/fetch (same-origin-policy protects custom headers); **naive double-submit is INSECURE** (use
  synchronizer, or signed double-submit bound to session with HMAC). SameSite + `Sec-Fetch-Site` are
  **defense in depth**, not a replacement; keep all mutations off GET; Sec-Fetch needs Origin/Referer
  fallback and log-only rollout.
- **TOTP:** RFC 6238 — step 30 s, secret ≥20 bytes; **ship SHA-1/6/30** for app compatibility
  (Google Authenticator ignores non-defaults); `pquerna/otp` default `Skew=0` → **set `Skew=1`**
  (library warns >1 is unsafe). Secrets are password-equivalent → seal with AEAD under the agent key.

## Verification (the gate)
```
1.  Argon2id: a known vector round-trips; stored string is PHC with m=19456,t=2,p=1; verify is
      constant-time; a password stored under weaker params is re-hashed on next login.
2.  Session: login mints a token and ROTATES it (old token invalid after login); idle >10 min and
      absolute >12 h both reject; logout/revoke deletes the row; a forged/unknown token → unauthenticated.
3.  Cookie: session cookie is `__Host-`, Secure, HttpOnly, SameSite=Strict, no Domain (asserted).
4.  MFA: enroll→confirm with a computed TOTP; a code one step away works (Skew=1), two steps away
      fails; a replayed code within its window is rejected; a backup code works once then is consumed;
      the stored TOTP secret is ciphertext, not Base32 plaintext.
5.  MFA gate: an MFA-marked verb called by a non-MFA session → forbidden + reauth_required.
6.  CSRF: a mutating request without a valid X-CSRF-Token → rejected; the token is not in a cookie;
      a cross-site Sec-Fetch-Site on a non-safe method → rejected (with Origin fallback); GET never mutates.
7.  Roles: read_only calling a write verb → forbidden; owner-only verbs reject admin.
8.  Every login attempt (ok + fail) and every privilege change is audited.
```

## Definition of Done
- [ ] Argon2id (OWASP params, PHC, constant-time, re-hash-on-login).
- [ ] Agent-owned sessions: mint, **rotate on login**, idle+absolute timeout, revoke; `__Host-` cookie.
- [ ] TOTP SHA-1/6/30, Skew=1, one-time-use, sealed secret, hashed backup codes; MFA-gated verbs enforced.
- [ ] HTMX shell with synchronizer CSRF via header (not cookie) + Sec-Fetch-Site/Origin fallback.
- [ ] Roles enforced agent-side; read-only dashboard.
- [ ] Auth events audited.

## Guardrails (this phase)
- **`panel-web` never hashes, stores, or sees a password/TOTP secret in the clear** — auth is an agent
  verb; the web tier presents inputs and the returned token only.
- **Rotate the session ID on login** — do not just flip an "authenticated" flag (fixation).
- **No naive double-submit CSRF;** no mutation on GET; `Sec-Fetch-Site`/SameSite are layers, not the
  defense.
- **TOTP `Skew` never > 1;** enforce one-time use and rate-limit verify attempts.
