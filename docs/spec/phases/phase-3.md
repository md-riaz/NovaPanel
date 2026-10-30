# Phase 3 — Nginx sites, domains, TLS

## Goal
Create and serve real sites: a site+domain model, a **structured Nginx renderer** with the
render→validate→atomic→reload→probe→revert loop (F8), static-site workflow, and **ACME HTTP-01**
certificate issuance via jobs. No raw directives from users; force-HTTPS only after a valid cert.

## Depends on
Phases 1–2 (jobs, auth, audit). Reads: `00-foundations.md` F6, F7, F8, F9, F10, F11.

## Deliverables
1. **Site + domain model** (`sites`, `domains` tables; `stack_driver='nginx'` for v1), verbs
   `site.create/update/delete/list/get`, `domain.add/remove`, all **slow** (jobs, `objectKey
   site:<name>`).
2. **Structured Nginx renderer** (`internal/render/nginx`): pure `desired → bytes` from **structured
   fields only** (root, index, php on/off, redirects, subdomain, maintenance) — never a raw-directive
   passthrough. Hostile-name unit tests (F9). The apply side runs the full F8 loop.
3. **Static sites only in Phase 3.** `site.create type:'static'` end-to-end, **including the per-site
   Unix user + docroot** (created here — see the pass-2 reconciliation block; no "shared owner" seam).
   `type:'php'|'wordpress'` is **refused in Phase 3** (`code:"unsupported"`) and lands in Phase 4 with
   the FPM pool; the renderer emits no PHP `location` for a site with no `php_version`, so the probe
   never hits a missing FPM socket.
4. **ACME HTTP-01 issuance** (`ssl.issue`, `ssl.renew`, `ssl.status`) as jobs: challenge served on
   **:80** at `/.well-known/acme-challenge/` (never redirected), cert written atomically, nginx
   reloaded, **force-HTTPS gated on a valid installed cert**.
5. **Redirects / subdomains / maintenance mode** as structured fields (no raw config).

## Blueprint

### Nginx vhost (two server blocks — reference, head-approved)
The renderer emits exactly this shape; every `{{…}}` is a validated field (F9), never empty:
```nginx
server {                                   # :80 — ACME on :80, everything else redirected
    listen 80; listen [::]:80;
    server_name {{domain}};
    location ^~ /.well-known/acme-challenge/ {    # MUST be above the redirect, never 301'd
        root /var/www/acme/{{site}};              # world-traversable (B-D8), NOT under /var/lib (0700)
        default_type "text/plain";
        try_files $uri =404;
    }
    location / { return 301 https://$host$request_uri; }
}
server {                                   # :443 — TLS
    listen 443 ssl; listen [::]:443 ssl;
    {{http2_directive}}                    # renderer emits `http2 on;` (nginx≥1.25.1) or folds `http2`
                                           # into the listen line on 1.24 (F0a) — NEVER a bare `http2 on;` on 1.24
    server_name {{all_site_domains}};      # one block per site; server_name lists every domain of the site
    root /srv/sites/{{site}}/public; index index.php index.html;
    ssl_certificate     /etc/novapanel/tls/{{primary_fqdn}}/current/fullchain.pem;   # versioned dir + symlink (B-D9)
    ssl_certificate_key /etc/novapanel/tls/{{primary_fqdn}}/current/privkey.pem;
    location / { try_files $uri $uri/ /index.php?$query_string; }
    location ~ \.php$ {
        try_files $uri =404;                       # cgi.fix_pathinfo RCE guard — load-bearing
        fastcgi_split_path_info ^(.+?\.php)(/.*)$;
        fastcgi_pass unix:/run/php/novapanel/{{site}}.sock;   # pool lands in Phase 4
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;  # MUST set explicitly on Debian
        fastcgi_param PATH_INFO $fastcgi_path_info;
        fastcgi_hide_header X-Powered-By;
    }
    location ~ /\. { deny all; }
    add_header X-Content-Type-Options nosniff always;
    add_header X-Frame-Options SAMEORIGIN always;
    add_header Referrer-Policy strict-origin-when-cross-origin always;
    # HSTS only after a valid cert is proven stable (trap below)
}
```

### The apply loop (F8, concrete for nginx)
```
render → write /etc/nginx/sites-available/<site>.conf.tmp (O_NOFOLLOW parent)
       → atomic rename into sites-available, symlink/rename into sites-enabled
       → `nginx -t`  (argv; on failure: rename the prior good file back, DO NOT reload, return agent_error)
       → `systemctl reload nginx`  (skip if rendered hash unchanged)
       → post-reload probe: HTTPS GET the vhost (SNI+Host) AND GET the :80 ACME path returns 200/404 (NOT 301)
       → on probe failure: restore last-known-good file + reload; return agent_error
```
Keep a last-known-good copy of every rendered file. `nginx -t` is necessary **but not sufficient**
(it does not connect FPM upstreams, does not check cert validity/CN, misses logic errors) — the
post-reload request probe is the real safety net.

### Render states, docroot ownership, cert atomicity (B-D8 / B-D9)
- **Three render states** (the renderer takes the site's cert+flag state, never assumes a cert):
  1. **no cert** — emit only the :80 server: serve the docroot over HTTP **and** the ACME location;
     **no :443 block** (so `nginx -t` never references a missing `fullchain.pem`).
  2. **cert present, `force_https=0`** — :80 serves + ACME (no blanket 301) and :443 serves.
  3. **cert present, `force_https=1`** — :80 does ACME then 301→https; :443 serves; HSTS allowed.
  `force_https` is a `sites` column (02-data-model) set by `site.update`; it is **refused**
  (`precondition_failed`) until a **valid cert** exists = parses, unexpired, a SAN covers the domain,
  and the key matches; a **staging** cert is not "valid" for force-HTTPS.
- **Docroot ownership (so nginx can serve a `0750 site:site` home):** docroot is
  `/srv/sites/<site>/public` owned `site:www-data`, mode `2750` (setgid, group-read+traverse); the
  site home is `0751` so `www-data` can traverse to it. nginx (`www-data`) thus reads static files and
  can `stat` for `try_files` without being able to write. (Owner model stated once here; Phase 4
  creates the user/dirs to match.)
- **Atomic cert install:** write the new cert+key into a **versioned dir** `tls_dir/<fqdn>/<ulid>/`
  then swap a single symlink `tls_dir/<fqdn>/current → <ulid>/`; nginx references
  `…/current/fullchain.pem` + `…/current/privkey.pem`, so a reload never sees a half-written pair
  (replaces the impossible "two independent atomic renames").
- **HSTS** is emitted only in state 3, starting `max-age=300`; a separate, explicit action raises it;
  never `preload` automatically.

### ACME (lego as a library)
- **Library (pinned): `github.com/go-acme/lego/v4`** (stable; verified signatures). Do not use v5 in
  v1. The implementer confirms exact signatures on pkg.go.dev for the pinned minor before coding.
- HTTP-01 via the **webroot provider** pointed at `/var/www/acme/<site>` (same dir nginx's :80 ACME
  `root` serves; world-traversable per B-D8). `ObtainRequest{Domains, Bundle:true}` →
  `res.Certificate` (fullchain), `res.PrivateKey`, `res.IssuerCertificate`, each PEM.
- **Default to the staging directory** in dev/tests (`lego.LEDirectoryStaging`); prod only on real
  issuance. Prefer ARI-aware renewal (`Renew` is deprecated).
- **No wildcards via HTTP-01** (needs DNS-01 → gated behind Phase 7).

### Pass-2 reconciliations (resolve review contradictions)
- **Per-site Unix user is created in Phase 3, not Phase 4** (every site needs an owner+docroot). Phase 4
  only adds the PHP-FPM pool/DB on top. This removes the undefined "shared docroot owner" seam: the
  agent creates `useradd -r -s /usr/sbin/nologin -m` home `/srv/sites/<site>` mode **0751**, docroot
  `/srv/sites/<site>/public` **`2750 <site>:www-data`** (setgid, group read+traverse) so nginx serves
  without write. Set the site user's umask so new files stay group-readable. (The home is **0751**, not
  0750 — the earlier "0750" phrasing was wrong; www-data needs `o+x` traverse, not read.)
- **State-aware probe:** in render **state 1 (no cert)** the probe is HTTP only (GET `/` on :80 +
  the ACME path returns 200/404, never 301); the HTTPS probe runs only in states 2/3. "Answers worse"
  = a 5xx or a connection failure; a 404/403 on an empty docroot is **not** a failure. Probe target
  `127.0.0.1` with the `Host`/SNI set; in CI/pebble, TLS verification is relaxed against the test CA.
- **One cert per site (SAN), not per domain:** `ssl.issue` requests **all of the site's domains** in
  one `ObtainRequest.Domains` (SAN cert) stored under `tls/<primary_fqdn>/`; `domains.cert_state`
  tracks coverage. Force-HTTPS is allowed once the SAN cert covers **every** domain on the site.
- **Renewal forces a reload:** `ssl.renew` swaps the `current` symlink (bytes unchanged) so it
  **always reloads nginx** (the skip-if-hash-unchanged rule does not apply to a cert swap). The F18
  scheduler runs renewal daily (within 30 days of expiry) and flips `cert_state`.
- **SSL is its own UI section, separate from Domains** (per the prototype). Three cert sources on a
  site (`sites.cert_source`): **automatic** (Let's Encrypt via `ssl.issue`/`ssl.renew`, auto-renew
  governed by `ssl.auto_set`), **manual** (`ssl.upload{cert_pem,key_pem}` — MFA; the agent validates
  the key matches the leaf and the dates are current (mismatch or expired → `invalid_argument`), then installs into the same versioned-dir +
  `current` symlink; the scheduler **never** auto-renews a manual cert), and **none**. **Force-HTTPS
  lives in the SSL section** and is the `site.update{force_https}` field, still gated on a valid cert.
- **Maintenance mode** renders a `return 503` + a static maintenance page served from
  `/var/www/novapanel/maintenance.html`; it is a `sites.maintenance` field set by `site.update`.
- **HSTS raise** is `site.update{hsts_max_age}` (owner), only in state 3; never auto-`preload`.
- **ACME config** (F16, all read from `Cfg`): `acme_directory_staging`/`acme_directory_prod`,
  `acme_email` (`ssl.issue` for prod refuses with `precondition_failed` when it is empty); separate
  account keys sealed as `acme:account:staging` and `acme:account:prod`; tests/dev default to staging,
  real issuance uses prod (chosen by a `staging` arg, defaulting to **`Cfg.acme_use_staging`**). A local
  issuance limiter (counter persisted in `settings`) self-throttles **before** LE's caps — **keyed per
  registered-domain**, windows mirroring LE (50 issued/7 d · 5 duplicate-set/7 d · 5 auth-failures/hour),
  each **reset** as its window rolls off; on exhaustion `ssl.issue`/`ssl.renew` return **`rate_limited`**
  (F3) without calling ACME.
- **Apply order (reconciles F8):** render to `sites-available/<site>.conf.tmp` → atomic-rename to the
  real `sites-available/<site>.conf` (symlinked into `sites-enabled`) → `nginx -t` (validates the full
  live set) → on pass reload; **on fail, atomic-rename the last-known-good copy back** (kept at
  `sites-available/<site>.conf.lkg`) and do not reload. First-ever render has no LKG → on `nginx -t`
  failure remove the new file.
- **ssl.issue/renew effect-specific test (F6):** issue a cert against pebble, assert the cert file
  pair appears under a new versioned dir and `current` points at it; a second `ssl.issue` within the
  renewal window is a no-op (does not re-hit ACME) — the effectful-verb test in place of "run twice =
  no change".

### Resolved contract details (Phase-3 rulings)
- **Reads are synchronous (01 wins).** `site.list`/`site.get`/`ssl.status`/`php.list` are sync per 01;
  the "reads are slow" phrase in earlier prose is wrong — do not enqueue reads.
- **Site lifecycle** is the new `sites.status ∈ {creating, active, failed, deleting}` (02). `site.create`'s
  `Run` persists `status='creating'`; the worker flips it to `active` on success or **`failed`** on
  error (a `failed` site is retry-able via `site.create` ensure-semantics or removable via `site.delete`);
  `site.delete` sets `deleting`. `site.list`/`get` return `status` (already in the 03 shape).
- **Lock keys** per F6: `site.create` → `site:<name>`; all other site/domain/redirect/ssl verbs →
  `site:<site_id>` resolved before enqueue.
- **`ssl.upload` key handling** per F6 "secret job input": the agent **seals `cert_pem`+`key_pem` before
  the job row is written**; `args_json` holds only the sealed blob; plaintext never lands in
  jobs/audit/events/web.
- **domain.add on a forced-HTTPS site** always adds the domain row, but the new FQDN is **not
  force-HTTPS-served until a cert covers it** (`domains.cert_state`): a `letsencrypt` site enqueues a
  cert expansion covering the full domain set; a `manual` cert that cannot auto-expand leaves the new
  domain at `cert_state='none'` (served HTTP; force-HTTPS applies per-covered-domain) until a covering
  cert is uploaded. `ssl.status` reports per-domain coverage.
- **domain.remove** of the **primary** domain → `precondition_failed` unless another domain is first
  promoted to primary (`site.update`); removing the **last** domain → `precondition_failed` (a site
  keeps ≥1 domain). Promotion uses the additive `site.update{primary_domain_id?:string}` contract
  in canonical 03, retaining existing-domain IDs, admin/sites:write/slow/non-effectful authorization,
  `site:<site_id>` locking and the owner-only HSTS gate. Admission and execution validate active
  lifecycle, same-site target, ≥1 domains and exactly one primary; the store checks atomically too.
  Same-primary is a no-op. HTTP-only promotion is implemented; TLS promotion remains fail-closed
  (`precondition_failed`) pending a journal that binds old/new primary, installed-pair coverage,
  preserved old path, nginx and SQLite. The ssl.upload journal binds an unchanged primary and cannot
  safely be reused unchanged. No SAN regeneration or metadata-only TLS promotion is permitted.
  The console selects an existing domain and calls site.update, never a new verb.
- **site.delete scope.** Retry-safe ensure-absent order: (1) remove the nginx config + reload (stop
  serving), (2) remove certs (files + `domains` cert state), (3) if `purge_files` remove the docroot/home
  **and** the site Unix user, else **keep** the files and the user dormant, (4) delete `redirects`/
  `domains`/`sites` rows last. Certs are always removed (regenerable). **Phase-4 DB rows are out of
  Phase-3 scope** — when Phase 4 lands it extends `site.delete` to handle associated databases (refuse
  or cascade per a flag); a Phase-3 `site.delete` does not touch DB rows (none exist yet).

## External refs (gathered — verified values to honor)
- **The 502 cause:** FPM socket default owner is the FPM run-user (site user), mode 0660 → nginx
  can't `connect()` → EACCES → 502. Fix is `listen.owner`/`listen.group` = **nginx user** (Phase 4
  pool), not the worker identity. Every parent dir of the socket must be nginx-traversable; on
  RHEL-family, SELinux `httpd_can_network_connect`.
- **`try_files $uri =404` in the `\.php$` block** is the mitigation for the `cgi.fix_pathinfo` RCE
  (upload `evil.jpg`, request `.../evil.jpg/x.php` → PHP executes the jpg). Pair with pool
  `cgi.fix_pathinfo=0` (Phase 4) as defense in depth. Side effect: it can clear `$fastcgi_path_info`
  for apps needing real PATH_INFO — capture with `set $path_info $fastcgi_path_info;` before
  `try_files` only where an app needs it (WordPress front-controller doesn't).
- **`SCRIPT_FILENAME`** must be `$document_root$fastcgi_script_name`; Debian's `fastcgi_params` does
  **not** set it (Debian's `fastcgi.conf` does) — set it explicitly, don't double it.
- **ACME HTTP-01:** port-80-only, LE follows ≤10 redirects to 80/443 (and does **not** validate the
  cert on an HTTPS redirect), but first issuance has no cert so the challenge must be answered on :80
  directly, above the redirect. **Rate limits (token-bucket):** 50 certs/registered-domain/7d; **5
  duplicate (exact identifier set)/7d, no override**; **5 auth failures/identifier/hour, no
  override** — so a broken config that keeps failing self-throttles. **Build and test every issuance
  path against staging first.**
- **Reload semantics:** `nginx -s reload` / `systemctl reload nginx` = SIGHUP, graceful (new workers
  on new config, old workers drain) — no dropped connections when the config is valid.

## Verification (the gate)
```
1.  Renderer unit tests: hostile server_name/site/path (spaces, `;`, `$`, `..`, empty) → render ERROR,
      never an empty/unquoted directive; output passes `nginx -t` for valid inputs.
2.  A valid static site: create → `nginx -t` passes → serves by Host header over HTTP (then HTTPS).
3.  Apply-and-revert: inject a bad-but-`-t`-passing config (e.g. wrong root) → post-reload probe
      fails → prior good config restored → site still serves. A `-t`-failing render never reloads.
4.  ACME (staging): issuance obtains a cert; the :80 ACME path returns 200/404 and is NOT 301'd;
      cert+key written atomically (no reload observes a half-written pair); force-HTTPS is REFUSED
      (code:"precondition_failed") until a valid cert is installed.
5.  Wildcard with **no DNS provider configured** → code:"unsupported"; once a provider exists
      (Phase 7) `ssl.issue` for a wildcard uses DNS-01 (this gate is rewritten in Phase 7, not failed).
6.  Every site/ssl verb is a job + audit event; concurrent site.update on the same site serializes.
```

## Definition of Done
- [ ] Site/domain model + verbs; all mutations are jobs, audited, per-site serialized.
- [ ] Structured renderer (no raw directives); hostile-input tests pass; no empty/unquoted interpolation.
- [ ] Full apply→`nginx -t`→atomic→reload→probe→revert loop with last-known-good restore.
- [ ] ACME HTTP-01 issuance/renew via jobs on staging+prod; challenge on :80, never redirected.
- [ ] Force-HTTPS + HSTS gated on a valid installed cert; wildcard refused until Phase 7.
- [ ] Cert+key writes are atomic; a reload never sees a mismatched pair.

## Guardrails (this phase)
- **No raw-directive passthrough.** Users set structured fields; the renderer owns the nginx syntax.
- **Never reload on a failed `nginx -t`;** never leave force-HTTPS/HSTS on without a valid cert
  (self-inflicted lockout — start HSTS `max-age` short, never auto-`preload`).
- **Serve the ACME challenge on :80, above the redirect** — a catch-all 301 that swallows it fails
  first issuance and burns the 5-failures/hour limit. Test against **staging** first.
- Pin the lego major version here before coding; the FPM pool itself is Phase 4 (this phase renders
  the `fastcgi_pass` to the expected socket path).
