# Phase 5 log viewer ruling

The canonical registry authorizes `log.tail` for Admin and above, without read-only admission, with the `sites:read` scope.

The allowlist covers the existing per-site PHP error log and the system nginx and PHP-FPM journal units. Per-site nginx access and error logs are deferred to a follow-up slice that establishes them in the renderer.
