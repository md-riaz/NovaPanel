# Nginx 1.24 and maintenance packaging acceptance

This bounded follow-up starts at `40cbb3bc4d508e7fc83dad299799ed8dca2884cf`.
It does not change production modules, RPC or renderer behavior.

## Repeat the checks

From a clean committed worktree, with Go, Python, Docker and TMPDIR set to task scratch:

```
make arch packaging-test
NOVAPANEL_NGINX_IMAGE=1.24 python3 tools/real_nginx_lab.py
NOVAPANEL_NGINX_IMAGE=modern python3 tools/real_nginx_lab.py
make package-maintenance OUT="$TMPDIR/novapanel-maintenance.tar"
```

The launcher selects only these immutable images:

- `1.24`: `nginx@sha256:77e5d4a6ad906c5d3793764085706577fa705b1dc6f244ea0241c4b5e2155385`
- `modern`: `nginx@sha256:1d13701a5f9f3fb01aaa88cef2344d65b6b5bf6b7d9fa4cf0dca557a8d7702ba`

The test logs the actual nginx version and asserts the directive shape before continuing with real validation, reload and HTTP/TLS probes. The 1.24 run uses `listen ... ssl http2;`; the modern run uses `http2 on;`. Version detection here belongs only to the lab, not production startup. The job executor continues to exercise its production-selected legacy shape.

Each run retains `source-sha.txt`, `source.tar`, `image.json`, `binary-sha256.txt`, `commands.log`, `acceptance.log`, before/after/teardown inspections and confirmed removal in the printed scratch evidence directory. The binary runs as UID/GID 65534 with no capabilities, no-new-privileges, network none, no mounts or published ports, 512 MiB memory, one CPU and 64 PIDs. The test archive contains only tracked committed files.

## Missing maintenance asset

The base renderer referenced `/var/www/novapanel/maintenance.html`, but the repository shipped neither that page nor a packaging step for it. The minimal static page now lives at `packaging/rootfs/var/www/novapanel/maintenance.html`. No external resources or scripts are needed.

`tools/package_maintenance.py` stages a tar artifact only. Its entries are root:root, directories `0755`, and the regular page `0644`. `TestMaintenancePackage` reads the generated archive and verifies the exact destination, parent entries, ownership, modes, file type and content. The real nginx test now serves this committed asset instead of an invented maintenance fixture. The specification pins the path and 503 behavior, not exact page wording; the plain wording is an implementation detail.

No installer exists in this slice and none is invented. The release packaging owner must consume this tar or the rootfs asset with those permissions. No archive is extracted into host system paths.

## Coverage limits

These Alpine images establish nginx-version compatibility, not full Ubuntu 24.04 acceptance. Listeners remain internal 8080/8443 rather than literal 80/443. Reload is SIGHUP, not systemd. No public CA is contacted. Actual package installation, systemd boot, per-site UID transitions and `site:www-data` ownership, firewall, append-only enforcement and reboot/power-loss acceptance remain VM-deferred. Do not add capabilities or host mounts to force them. This is not whole Phase 3 readiness.
