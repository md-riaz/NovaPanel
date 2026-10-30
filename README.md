# NovaPanel

## Go rewrite status

This branch contains the Phase 0 Go foundation, not a deployable VPS control panel.
The agent exposes only `ping` and owner-only `agent.info` over a local Unix socket.
`panel-web` is a stub and `novapanelctl` is a thin local client. There is no web UI,
installer, job queue, database or persistent audit log in this phase. Do not deploy
this branch to manage production services.

Build and test with the Go version declared in [go.mod](go.mod):

```sh
make build
make test
```

`make ci` also requires `staticcheck` and `govulncheck`. The optional
`make integration` gate runs the real CLI and web binaries against a test-only daemon
that shares the production lifecycle with an injected command runner. All persistent
paths stay in scratch; real socket permissions, peer credentials, jobs and shutdown
are exercised. Root is required for socket ownership and peer UID probes. Run it in
a disposable environment. It never runs real `chattr` or service commands and does
not replace final VM acceptance for append-only enforcement and live services.

The implementation contract is [Phase 0](docs/spec/phases/phase-0.md), with
shared contracts in [foundations](docs/spec/00-foundations.md).

## Legacy PHP application

The frozen PHP application, its installation instructions and feature documentation
are preserved at [`php-legacy`](https://github.com/md-riaz/NovaPanel/tree/php-legacy).
Those instructions apply only to that revision, not this Go branch. In particular,
`install.sh`, Composer commands and PHP configuration are not part of this branch.

## License

This branch does not include a license file. This status update does not grant
or change licensing terms; any licensing decision requires separate authorization.
