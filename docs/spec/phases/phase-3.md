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
3. **Static-site workflow:** create a static site end-to-end (user+docroot in Phase 4 owns the user;
   here a static site can use a shared docroot owner until Phase 4 lands per-site users — spec the
   seam, don't block).
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
        root /var/lib/novapanel/acme/{{site}};
        default_type "text/plain";
        try_files $uri =404;
    }
    location / { return 301 https://$host$request_uri; }
}
server {                                   # :443 — TLS
    listen 443 ssl; listen [::]:443 ssl;
    http2 on;                              # nginx ≥1.25.1 form (NOT `listen ... http2`)
    server_name {{domain}};
    root /srv/sites/{{site}}/public; index index.php index.html;
    ssl_certificate     /etc/novapanel/tls/{{domain}}/fullchain.pem;
    ssl_certificate_key /etc/novapanel/tls/{{domain}}/privkey.pem;
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

### ACME (lego as a library)
- **Library:** `github.com/go-acme/lego` — **pin the major version in this spec** (v4 is stable and
  the signatures below are verified against it; v5 exists — head confirms the pin before coding and
  the implementer verifies signatures on pkg.go.dev for the pinned version).
- HTTP-01 via the **webroot provider** pointed at `/var/lib/novapanel/acme/<site>` (same dir nginx's
  :80 ACME `root` serves). `ObtainRequest{Domains, Bundle:true}` → `res.Certificate` (fullchain),
  `res.PrivateKey`, `res.IssuerCertificate`, each PEM, written atomically.
- **Default to the staging directory** in dev/tests (`lego.LEDirectoryStaging`); prod only on real
  issuance. Prefer ARI-aware renewal (`Renew` is deprecated).
- **No wildcards via HTTP-01** (needs DNS-01 → gated behind Phase 7).

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
5.  Wildcard via HTTP-01 → code:"unsupported" (needs Phase 7 DNS-01).
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
