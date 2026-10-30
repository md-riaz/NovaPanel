# NovaPanel

## Go rewrite status

This branch implements the Phase 1 job/store foundation, Phase 2 authentication
and web shell, and Phase 3 static-site/domain/nginx/TLS workflows. It is a
review candidate, not a production-deployment-ready VPS control panel.

- The root agent owns authorization, SQLite state, persistent audit, queued jobs,
  per-site serialization and local Unix-socket RPC. `novapanelctl` is a local client.
- `panel-web` provides login, MFA and a static-site console with job polling,
  domains, redirects, maintenance and SSL controls. It does not own session authority.
- TLS supports sealed manual input, durable certificate publication/recovery,
  ACME HTTP01, environment-separated accounts and persistent issuance budgets.
  Automatic renewal uses persisted daily schedules. Wildcard issuance is unsupported.
- PHP/FPM, MariaDB and WordPress are Phase 4 work, not implemented site types.
  Dashboard metrics remain placeholders for later agent verbs.

The implementation contracts are [foundations](docs/spec/00-foundations.md),
[Phase 1](docs/spec/phases/phase-1.md), [Phase 2](docs/spec/phases/phase-2.md)
and [Phase 3](docs/spec/phases/phase-3.md). Read the
[acceptance map](docs/phase3-acceptance-map.md) for evidence and remaining gates.
Local passes do not replace privileged final-VM acceptance for real account
ownership/isolation, systemd/boot, append-only audit, production paths and ports,
or crash/power-loss behavior. Public CA acceptance needs separate authorization.

## Development setup and checks

Use the Go version declared in [go.mod](go.mod). Install `staticcheck` and
`govulncheck` in the development toolchain for `make ci`. Python 3 and systemd's
unit verifier are also used by the architecture and packaging checks.

```sh
make build
make ci
go test -race -count=1 -json ./... > race.jsonl
make packaging-test
```

Use a disposable root runner for `make integration`. It launches real CLI/web
binaries against a test daemon with the production lifecycle and an injected
command runner. Persistent paths stay in scratch. It exercises Unix-socket peer
credentials, jobs and shutdown, but does not execute real service or `chattr`
commands. Do not start `panel-agent` against its default production paths to run
these checks. Runtime configuration is defined in `internal/config` and F16 of
the foundations contract, not by the legacy PHP installation instructions.

Browser and asset tests use scratch-only npm dependencies, not production Node:

```sh
# Install jsdom@26.1.0, jsqr@1.4.0 and playwright@1.58.2 in a scratch npm prefix.
export NODE_PATH=/path/to/scratch/node_modules
node internal/web/assets_test.cjs
NOVAPANEL_BROWSER_NODE="$(command -v node)" go test ./internal/web/testdata/browser -count=1 -v
NOVAPANEL_BROWSER_NODE="$(command -v node)" go test ./tests/integration -run '^TestRealBackendConsoleBrowser$' -count=1 -v
```

See [web checks](internal/web/README.md) and
[real-backend console acceptance](docs/real-backend-console-browser.md) for Chrome
selection and test boundaries. Confined nginx 1.24/modern acceptance uses
`tools/real_nginx_lab.py`; genuine private-CA acceptance uses
`tools/pebble_nginx_lab.py` with `NOVAPANEL_PEBBLE_BINARY`. The scheduler matrix
selector is `NOVAPANEL_PEBBLE_TEST='^TestRealPebbleSchedulerMutationAcceptance$'`.
These opt-in labs use pinned images, no network except container loopback, no
host mounts or published ports, and adapted 8080/8443 listeners. They are not
production installation tests. See [scheduler evidence](docs/evidence/phase3-scheduler-audit/README.md).

## Release staging, not installation

```sh
make release-stage OUT=/absolute/scratch/release-stage
```

This creates `panel-agent`, `panel-web`, `novapanelctl` and `maintenance.tar`.
The maintenance archive contains the committed maintenance page; staging does not
install it into production or start services.
There is no Go installer in this branch. Review the
[next-phase dependency map](docs/phase4-dependency-map.md) before authorizing
Phase 4 implementation. Phase 3 review readiness is a prerequisite, not implied
by a successful build.

## Legacy PHP application

The frozen PHP application, its installation instructions and feature documentation
are preserved at [`php-legacy`](https://github.com/md-riaz/NovaPanel/tree/php-legacy).
Those instructions apply only to that revision, not this Go branch. In particular,
`install.sh`, Composer commands, old phpMyAdmin guides and the legacy sudoers setup
are not Go release instructions.

## License

This branch does not include a license file. This status update does not grant
or change licensing terms; any licensing decision requires separate authorization.
