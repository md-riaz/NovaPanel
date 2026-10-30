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
`make integration` gate runs real binaries on a scratch socket and requires root
in a disposable environment. It does not install or start host services.

The implementation contract is [Phase 0](docs/spec/phases/phase-0.md), with
shared contracts in [foundations](docs/spec/00-foundations.md).

## Legacy PHP application

The frozen PHP application, its installation instructions and feature documentation
are preserved at [`php-legacy`](https://github.com/md-riaz/NovaPanel/tree/php-legacy).
Those instructions apply only to that revision, not this Go branch. In particular,
`install.sh`, Composer commands and PHP configuration are not part of this branch.

## License

See [LICENSE](LICENSE). This status update does not change the existing license;
any proprietary licensing decision requires separate authorization.
