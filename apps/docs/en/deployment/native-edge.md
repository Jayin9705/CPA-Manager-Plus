---
title: Native systemd Edge Deployment
description: Deploy the complete production path with Nginx, the separated CPAMP API, CLIProxyAPI, Mihomo, Redis, PostgreSQL, and systemd.
---

# Native systemd Edge Deployment

Use this topology on Linux hosts that already manage services with systemd and
terminate TLS in host Nginx. Only public `80/443` are exposed; CPA `8317`, the
separated API `18318`, Mihomo `7890`, Redis, and PostgreSQL remain local.

The checked-in [`deploy/native-edge/`](https://github.com/Jayin9705/CPA-Manager-Plus/blob/main/deploy/native-edge/README.md)
package provides reusable templates for:

- Same-domain routing across the console, management APIs, and model APIs.
- WebSocket `Connection` headers only when the client requested an upgrade.
- A systemd startup dependency from CLIProxyAPI to Mihomo.
- IPv4/IPv6 TCP 80/443 acceptance before cloud source-IP reject chains.
- OAuth JSON proxy-override auditing and mode-0600 repair.
- Redis, persist, hourly/daily rollup, and separated API units.
- Scheduled backups for both `cpamp_usage` and `sub2api`.
- Runtime verification without printing secrets.

## Scope

This is not the Docker full-stack installer. Use it after the binaries and
databases are prepared and you need to reproduce the native production
topology. Read
[CPA + Redis + PostgreSQL Architecture And Installation](../operations/cpa-redis-postgresql.md)
first for a new deployment.

Exactly one collector may consume the live CPA usage queue. Disable the old
`cpa-manager-plus.service` and `cpamp-usage-relay.service` before enabling the
separated API and new workers.

## Security Requirements

Never copy real OAuth JSON, API keys, Management Keys, data keys, DSNs, Mihomo
subscriptions, certificates, or database dumps into Git. The templates use
`*_FILE` settings for mode-0600 private files.

The web-ingress script accepts only 80/443 and does not alter SSH. Keep Fail2ban
and the cloud SSH policy enabled.

## Expected Verification

```text
GET /health                       -> 200
OPTIONS /v1/responses             -> 204
GET /v1/models (normal API key)   -> 200
Minimal /v1/responses             -> 200 completed
```

See [`deploy/native-edge/README.md`](https://github.com/Jayin9705/CPA-Manager-Plus/blob/main/deploy/native-edge/README.md)
for installation, backup, cutover, verification, and rollback commands.
