# Real-backend console browser acceptance

Run on a disposable root runner with Go, Node, Playwright and Chromium installed:

```sh
NOVAPANEL_BROWSER_NODE="$(command -v node)" go test ./tests/integration -run '^TestRealBackendConsoleBrowser$' -v -count=1
```

`NODE_PATH` must resolve Playwright. `NOVAPANEL_CHROME` optionally overrides `/usr/bin/google-chrome`. All persistent paths use the runner's scratch `TMPDIR`. The launcher compiles the real panel-web binary and the panel-agent test binary, starts the production daemon lifecycle, and drops panel-web to UID/GID 65534 before serving. A loopback TLS reverse proxy supplies the browser origin. Self-signed proxy TLS handshake warnings are expected.

The test uses real authentication, UID-gated Unix RPC, dispatcher, queue workers, SQLite and durable audit. Every privileged command and the nginx state probe are injected by the test-only fixture. It never contacts a live CA or host nginx/systemd service. This does not replace real nginx or VM acceptance.

Coverage includes login, queued site creation and polling, maintenance, domains, redirects, HTTP and TLS primary promotion, manual upload and deletion MFA refusal, MFA enrollment and backup-code step-up, successful sealed upload, force HTTPS, browser storage, mobile width, deletion and retained non-purged files. After clean daemon shutdown releases the exclusive SQLite lifetime lock, independent readback checks completed jobs, canonical upload result, destroyed sealed input, deleted site/domain/TLS state and mirrored secret-free audit events.

## Upload diagnosis

The original browser run admitted upload with HTTP 202 but the worker returned `invalid_argument`. The fixture had not created `TLSDir`. `internal/tls.openPublication` requires an existing absolute symlink-free private agent-owned root, and rejects a missing root before staging. Creating only the fixture's scratch TLS root with mode 0700 fixes publication without changing production validation. The fixture certificate covers both persisted domains, including the promoted primary. Manual upload preserves the stored auto-renew preference, so browser assertions must not assume upload disables it.
