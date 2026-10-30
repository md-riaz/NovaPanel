# Packaging reference (systemd units, tmpfiles, sysusers)

The concrete templates the corpus previously referenced as "BHPanel session notes." These are the
files the implementer writes in `packaging/` (Phase 1 deliverable). Values match F0a (Ubuntu 24.04).

## sysusers — create the unprivileged web user/group

`packaging/sysusers/novapanel.conf` (installed to `/usr/lib/sysusers.d/`):
```
# type name      id   gecos
u     novapanel  -    "NovaPanel web"
```
The agent runs as **root** (no sysusers entry); the `novapanel` system user owns `panel-web` and is
the group on the agent socket (F1).

## tmpfiles — runtime & state dirs (not unit RuntimeDirectory=)

`packaging/tmpfiles/novapanel.conf` (→ `/usr/lib/tmpfiles.d/`):
```
d /run/novapanel      0755 root root      -
d /var/lib/novapanel  0700 root root      -
d /var/log/novapanel  0750 root novapanel -
d /var/www/acme       0755 root root      -     # world-traversable ACME webroot (B-D8)
```
`/run/novapanel` is here (not a unit `RuntimeDirectory=`) because systemd deletes a RuntimeDirectory
on unit stop; `/var/lib/novapanel` is here (not `StateDirectory=`) to avoid the double-mount trap; the
agent also re-creates any missing dir at start and sets `chattr +a` on `audit.log` itself (so no
`LogsDirectory=`).

## novapanel-agent.service

```ini
[Unit]
Description=NovaPanel privileged agent
After=network.target
Before=novapanel-web.service
StartLimitIntervalSec=300
StartLimitBurst=20

[Service]
Type=simple
ExecStart=/usr/sbin/novapanel-agent
Restart=on-failure
RestartSec=2s
# Root by design (the ONLY root component). NoNewPrivileges=no: it must create users, write
# /etc units, manage the firewall.
NoNewPrivileges=no
ProtectKernelTunables=yes
ProtectKernelModules=yes
RestrictNamespaces=yes
RestrictRealtime=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service @privileged
SystemCallFilter=~@obsolete @swap
# NO StateDirectory / LogsDirectory / RuntimeDirectory — see tmpfiles note above.
CapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE CAP_FOWNER CAP_SETUID CAP_SETGID CAP_KILL CAP_LINUX_IMMUTABLE CAP_NET_ADMIN CAP_NET_BIND_SERVICE
TimeoutStopSec=30s

[Install]
WantedBy=multi-user.target
```
`CAP_LINUX_IMMUTABLE` is required for `chattr +a` on the audit log; `CAP_NET_ADMIN` for firewall
management; `CAP_SETUID/SETGID` to drop a worker to a site user.

## novapanel-web.service

```ini
[Unit]
Description=NovaPanel web UI + API
After=network.target novapanel-agent.service
Wants=novapanel-agent.service
StartLimitIntervalSec=300
StartLimitBurst=20

[Service]
Type=simple
ExecStart=/usr/bin/novapanel-web
Restart=on-failure
RestartSec=2s
User=novapanel
# Never root. Stateless; talks to the agent over the socket only.
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadWritePaths=/run/novapanel
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
RestrictSUIDSGID=yes
LockPersonality=yes
SystemCallArchitectures=native
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
TimeoutStopSec=15s

[Install]
WantedBy=multi-user.target
```
`systemd-analyze verify packaging/systemd/*.service` must pass (Tier-2 CI). Per-site units
(`novapanel-cron-<site>-<id>.{service,timer}`, the firewall revert transient unit) are rendered at
runtime, not shipped.
