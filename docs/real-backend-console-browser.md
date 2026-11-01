# Real-backend console browser acceptance

Run on a disposable root runner with Go, Node, Playwright and Chromium installed:

```sh
NOVAPANEL_BROWSER_NODE="$(command -v node)" go test ./tests/integration -run '^TestRealBackendConsoleBrowser$' -v -count=1
```

`NODE_PATH` must resolve Playwright. `NOVAPANEL_CHROME` optionally overrides `/usr/bin/google-chrome`. All persistent paths use the runner's scratch `TMPDIR`. The launcher compiles the real panel-web binary and the panel-agent test binary, starts the production daemon lifecycle, and drops panel-web to UID/GID 65534 before serving. A loopback TLS reverse proxy supplies the browser origin. Self-signed proxy TLS handshake warnings are expected.

The test uses real authentication, UID-gated Unix RPC, dispatcher, queue workers, SQLite and durable audit. Every privileged command and the nginx state probe are injected by the test-only fixture. It never contacts a live CA or host nginx/systemd service. This does not replace real nginx or VM acceptance.

Coverage includes login, queued site creation and polling, maintenance, domains, redirects, HTTP and TLS primary promotion, manual upload and deletion MFA refusal, MFA enrollment and backup-code step-up, successful sealed upload, force HTTPS, browser storage, mobile width, deletion and retained non-purged files. After clean daemon shutdown releases the exclusive SQLite lifetime lock, independent readback checks completed jobs, canonical upload result, destroyed sealed input, deleted site/domain/TLS state and mirrored secret-free audit events.

## Timeout diagnostics

The browser retains at most 80 allowlisted lifecycle records in memory and emits `[browser-diag]` on failure before closing Chromium. Records contain relative milliseconds, normalized route categories, HTTP method/status, canonical job ID, fixed job state, polling-marker presence/connection, completion/alert booleans, and fixed DOM/HTMX event names. The final snapshot is read-only. Go also emits `[daemon-diag]` with elapsed daemon time and a bounded read-only `job.list` snapshot containing only validated IDs/states and numeric creation/update timestamps, before scratch cleanup. This distinguishes a completed daemon job from a browser still displaying queued state without polling the job again or changing acceptance behavior.

No response/request bodies, headers, cookies, credentials, CSRF/session/MFA values, certificate/key PEM, free-form errors, screenshots, full DOM, or daemon logs are retained by diagnostics. Unknown routes become `other`; query strings are discarded. Persist Go output in the CI artifact store, not the deleted fixture directory:

```sh
NOVAPANEL_BROWSER_NODE="$(command -v node)" go test -race -count=3 -json ./tests/integration -run '^TestRealBackendConsole(Browser|Diagnostics)$' > browser-acceptance.jsonl
```

`TestRealBackendConsoleDiagnostics` runs explicit secret-negative serializer checks and an isolated Chromium fixture with no polling script. It verifies that the completion predicate still times out and the report includes HTTP 202, a queued job and connected poll marker without fixture secrets. The fixture's short timeout is confined to that test. The actual-backend browser retains its original 15000ms default, assertions and failure exit; there are no automatic retries. Diagnostics do not establish a root cause or fix the previously observed intermittent create/poll timeout. Passing repeats are review evidence, not release clearance for the unresolved flake.

## Upload diagnosis

The original browser fixture created `TLSDir` and masked missing production startup provisioning. `OpenState` now creates the configured root before accepting transport requests. It walks no-follow directory descriptors, accepts only root/agent-owned non-writable ancestors except root-owned sticky scratch ancestors, and requires an agent-owned exact 0700 leaf with no special bits. It never repairs unsafe existing ownership or permissions. Newly created shared configuration ancestors are exact 0755 so an unprivileged panel-web can traverse `/etc/novapanel` and treat absent optional `agent.toml` as missing rather than denied. Only newly created directories receive exact modes, independent of the service umask; existing safe administrator modes and ownership remain unchanged. The configured TLS leaf stays exact 0700, blocking web access to its directory and private keys. The fixture no longer creates this directory. The real browser upload therefore exercises clean startup provisioning. The fixture certificate covers both persisted domains, including the promoted primary. Manual upload preserves the stored auto-renew preference.
