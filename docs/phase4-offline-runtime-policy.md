# Bounded offline PHP runtime dependency

This local ruling exercises the missing runtime dependency in phase-4.md lines
166 and 168. It does not open a production installer or assert account quiescence.
The production queue remains closed at the existing stage/config boundary.

## Chosen mechanism before implementation

The lab runs only retained canonical WP-CLI and WordPress bytes. A fixed lab
bootstrap requires the unchanged PHAR and registers its documented
`http_request_options` hook with a narrowly defined Requests transport. The
transport accepts only GET of the exact pinned official checksum URL and returns
an HTTP 200 envelope containing the exact digest-checked official response bytes.
Every other request, redirects and multi-request operation refuse. No upstream
file, TLS verification setting, proxy, host CA, host key or network changes.
This adapter lives under tools, not the production WordPress package. It is not
an approved production offline transport or a download cache implementation.
Verified archive extraction replaces acquisition only in this dependency lab;
`core download` remains unaccepted.

The lab PHP invocation uses `-n` plus a fixed reviewed list of `-d` directives.
It loads no host/default ini, scan directory, prepend/append script, user ini,
packages or project YAML. Only compiled-in extensions are allowed. The pinned
PHP 8.3 image supplies the binary; its path differs from Ubuntu's production
`/usr/bin/php8.3`. The bootstrap selects the pinned PHAR, never a site bootstrap.
The unchanged official `core verify-checksums` command runs before WordPress
loads, with explicit version, locale and derived lab path. A separate typed
config data check may use only the existing fixed config composer, not wp-config
or plugins. Core install and arbitrary PHP/site code remain forbidden.

All execution takes place in a uniquely owned network-none, read-only-root
container with all capabilities dropped, no-new-privileges, no host bind mounts,
no ports/devices and bounded memory/CPU/PIDs. The scratch filesystem alone is
writable. Files are copied before starting the nonroot runtime. The Go process
boundary is `internal/exec.Real`; inside this nonroot lab it inherits the already
selected numeric site UID with an explicitly empty environment, rather than
pretending to demonstrate privileged setuid from a capless process. The final VM
must separately prove root-to-persisted-site UID/GID/groups execution.

## Acceptance required

Run unchanged official checksum verification successfully, then change one core
file and require failure. Prove hostile ambient ini, project YAML, wp-config and
mu-plugin files cannot execute. Prove wrong URL and response bytes refuse. Keep
child output bounded and discarded by the tool; only fixed nonsecret statuses
leave it. Bind retained inputs and exact source commit to evidence. Do not move
the installation journal beyond `db_applied`, publish claims or fabricate an
isolation callback. Live transfer, physical UID/FPM quiescence, config execution,
DB orchestration and install remain source gaps, not deferred VM-only checks.

## Launcher cancellation and exact ownership

The launcher installs SIGTERM/SIGINT handlers before commands and records its
unique name and ownership label in fsynced `owner.json` before Docker effects.
Signals request cancellation rather than raising inside subprocess setup. A
build/create/start already admitted drains to completion within its 180-second
command deadline, so cleanup can inspect a resource even if cancellation arrived
before its ID was returned. Other commands are killed and reaped on cancellation.
Cleanup has a shared 20-second budget, survives repeated handled signals, checks
the exact name and label, removes by immutable ID, and reads back ID absence.
Container failure does not suppress the image cleanup attempt. Errors are retained
in `cleanup-errors.log` and `cleanup.json`; signal exits are 143/130. No global
prune, image search, foreign artifact removal or host service changes are used.
The COPY-only build uses `--force-rm`; the pinned base must already exist locally.

SIGKILL, power loss and a daemon that outlives a timed-out Docker mutation cannot
be guaranteed to run cleanup or settle within the client deadline. After that
mutation has settled, explicitly retry only the retained record with
`python3 tools/phase4_wp_offline_lab.py --reap /exact/evidence/owner.json`.
This validates the recorded name and current ownership labels, then uses the same
bounded ID-based cleanup. It never scans other launches or automatically reaps a
possibly active run. The evidence directory must be trusted local input. A
missing resource at one instant is not proof that an unsettled daemon operation
will not create it later. This limitation is not a power-loss cleanup guarantee.

`make ci` runs the non-Docker lifecycle failure/ownership regressions. With the
pinned image retained, `NOVAPANEL_WP_DOCKER_TEST=1 python3 -m unittest discover
-s tools -p test_phase4_wp_offline_lab.py -v` also exercises real local images and
containers at post-build, post-create pre-ID, post-start and running boundaries,
SIGTERM/SIGINT repeated cancellation, and explicit SIGKILL record reaping. These
fixtures do not execute the official PHP verification; the unchanged official
lab remains a separate exact-commit acceptance command.
