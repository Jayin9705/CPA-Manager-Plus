# Native Edge Deployment

This package reproduces a native Linux deployment with the separated CPAMP
console/API, CLIProxyAPI, Redis, PostgreSQL, usage workers, Nginx, and a durable
backup timer. It is intended for hosts where these components run under
systemd instead of Docker Compose.

```text
Internet :80/:443
        |
        v
      Nginx
        +-- /separated-console/* ------------> static console
        +-- CPAMP management/API paths ------> 127.0.0.1:18318
        +-- model APIs, callbacks, fallback -> 127.0.0.1:8317
                                                    |
                                               CLIProxyAPI -> mihomo

CPA usage queue -> separated API/collector -> Redis Stream
                                              |
                                              v
                                       persist worker -> PostgreSQL
                                                            |
                                                   hourly/daily rollup
```

## Security boundaries

- Only Nginx listens publicly on TCP 80/443. CPA, CPAMP, Redis, and PostgreSQL
  remain on loopback or Unix sockets.
- `cpa-web-ingress-allow` exempts only TCP 80/443 from source-IP reject chains.
  It does not accept SSH or any other port. Keep Fail2ban or the host's SSH
  policy enabled.
- No real domain, OAuth JSON, API key, management key, data key, DSN, password,
  certificate, database dump, or runtime environment file belongs in Git.
- Put secrets under `/etc/cpa-manager-plus/secrets/`, mode `0600`, with the
  narrowest service ownership that can read them. The templates use `*_FILE`
  settings so secrets do not appear in unit command lines.
- `runtime-config.json` is public browser configuration. It may contain exact
  origins but never credentials or private database/Redis addresses.

## 1. Prerequisites

Install Nginx, iptables/ip6tables, Redis, PostgreSQL client tools, curl, Python
3, and systemd. Build or install these binaries separately:

```text
/opt/cliproxyapi/cli-proxy-api
/opt/cpa-manager-plus-separated/bin/cpa-manager-plus-api
/opt/cpa-manager-plus/cpamp-migration
```

Mihomo is expected at `/usr/local/bin/mihomo`. The checked-in Mihomo example is
only a safe local listener and direct-routing baseline. Add real proxy nodes or
provider subscriptions only to the private installed `/etc/mihomo/config.yaml`.

The examples assume these service users already exist:

```text
cliproxyapi
cpa-manager-plus
redis
postgres
```

Distribution PostgreSQL unit names vary. Replace `postgresql.service` in the
unit templates with the concrete unit on the host when necessary, for example
`postgresql@18-main.service`.

## 2. Install private configuration

Create directories without copying any existing credentials into the repo:

```bash
sudo install -d -o root -g cpa-manager-plus -m 0750 /etc/cpa-manager-plus-separated
sudo install -d -o root -g cpa-manager-plus -m 0750 /etc/cpa-manager-plus/secrets
sudo install -d -o cpa-manager-plus -g cpa-manager-plus -m 0700 /var/lib/cpa-manager-plus
sudo install -d -o redis -g redis -m 0750 /var/lib/redis/cpamp-usage
sudo install -d -o postgres -g postgres -m 0700 /var/backups/cpa-manager-plus/postgresql
```

Copy and edit the non-secret templates:

```bash
sudo install -m 0600 deploy/native-edge/config/api.env.example \
  /etc/cpa-manager-plus-separated/api.env
sudo install -m 0600 deploy/native-edge/config/worker.env.example \
  /etc/cpa-manager-plus-separated/worker.env
sudo install -m 0600 deploy/native-edge/config/backup.env.example \
  /etc/cpa-manager-plus-separated/backup.env
sudo install -o root -g cpa-manager-plus -m 0640 deploy/native-edge/config/config.json.example \
  /etc/cpa-manager-plus-separated/config.json
sudo install -o root -g redis -m 0640 deploy/native-edge/config/redis.conf.example \
  /etc/redis/cpamp-usage.conf
sudo install -o root -g cliproxyapi -m 0640 \
  deploy/native-edge/config/cliproxyapi.config.example.yaml \
  /etc/cliproxyapi/config.yaml
sudo install -d -o root -g mihomo -m 0750 /etc/mihomo
sudo install -o root -g mihomo -m 0640 deploy/native-edge/config/mihomo.config.example.yaml \
  /etc/mihomo/config.yaml
```

Replace `public.example.com` with the exact production HTTPS origin. Create the
secret files referenced by `api.env` and `worker.env`; DSN examples for a local
peer-authenticated PostgreSQL installation are:

```text
user=cpa-manager-plus dbname=cpamp_usage host=/var/run/postgresql
user=cpa-manager-plus dbname=sub2api host=/var/run/postgresql
```

Use the same existing Manager data key when attaching to an existing database.
Generating a different key makes encrypted configuration unreadable.
Create service-readable secrets as `cpa-manager-plus:cpa-manager-plus` with mode
`0600`; keep the environment files root-owned at mode `0600` because systemd
loads them before dropping privileges.

Replace both `CHANGE_ME` values in the private CLIProxyAPI config. Its global
proxy is the complete local Mihomo URL `http://127.0.0.1:7890`; auth files with
no override inherit it.

Build the separated console and install it under
`/opt/cpa-manager-plus-separated/console/`. Copy the edited public runtime
configuration there as `runtime-config.json`.

## 3. Install scripts and units

Review every path first, then install:

```bash
sudo install -m 0755 deploy/native-edge/scripts/cpa-web-ingress-allow \
  /usr/local/sbin/cpa-web-ingress-allow
sudo install -m 0755 deploy/native-edge/scripts/cpa-auth-proxy-audit \
  /usr/local/sbin/cpa-auth-proxy-audit
sudo install -m 0755 deploy/native-edge/scripts/cpamp-postgres-backup \
  /usr/local/sbin/cpamp-postgres-backup
sudo install -m 0755 deploy/native-edge/scripts/verify-native-edge \
  /usr/local/sbin/verify-cpamp-native-edge

sudo install -m 0644 deploy/native-edge/systemd/*.service /etc/systemd/system/
sudo install -m 0644 deploy/native-edge/systemd/*.timer /etc/systemd/system/
```

If CLIProxyAPI is already managed by a customized unit, merge the important
dependency instead of blindly replacing the unit:

```ini
After=network-online.target mihomo.service
Wants=network-online.target mihomo.service
```

Audit CPA auth files before restart. `--fix` removes only malformed non-empty
proxy overrides and changes JSON modes to `0600`; it never prints JSON values:

```bash
sudo /usr/local/sbin/cpa-auth-proxy-audit
sudo /usr/local/sbin/cpa-auth-proxy-audit --fix
```

An absent or empty account-level proxy override inherits CPA's valid global
proxy. Do not write placeholders such as `"proxy_url": "3"`; a non-empty
override must be a complete URL with a supported scheme and host.

## 4. Install Nginx routing

Copy the common proxy snippet, edit the site template's domain and certificate
paths, then enable it according to the distribution's Nginx layout:

```bash
sudo install -m 0644 deploy/native-edge/nginx/cpa-proxy-common.conf \
  /etc/nginx/snippets/cpa-proxy-common.conf
sudo install -m 0644 deploy/native-edge/nginx/cpa-native-edge.conf.example \
  /etc/nginx/sites-available/cpa-native-edge.conf
sudoedit /etc/nginx/sites-available/cpa-native-edge.conf
sudo ln -s /etc/nginx/sites-available/cpa-native-edge.conf \
  /etc/nginx/sites-enabled/cpa-native-edge.conf
sudo nginx -t
sudo systemctl reload nginx
```

The `$connection_upgrade` map sends `Connection: upgrade` only when the client
requested an upgrade. Ordinary `/v1` HTTP calls use `Connection: close` toward
the upstream instead of being falsely marked as WebSockets.

The final `location /` deliberately goes to CPA. It keeps model endpoints,
OAuth callbacks, WebSockets, and future CPA paths working without a brittle
path allowlist.

## 5. Cut over one data pipeline

Never run the old Manager collector/SQLite relay and the separated API against
the same live CPA usage queue/Redis Stream. Back up first, then stop and disable
the old units while retaining their files for rollback:

```bash
sudo systemctl disable --now cpa-manager-plus.service cpamp-usage-relay.service
```

Load units and start the production pipeline:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now cpamp-usage-redis.service
sudo systemctl enable --now mihomo.service
sudo systemctl enable --now cliproxyapi.service
sudo systemctl enable --now cpamp-usage-persist.service
sudo systemctl enable --now cpamp-usage-rollup-hourly.service
sudo systemctl enable --now cpamp-usage-rollup-daily.service
sudo systemctl enable --now cpa-manager-plus-separated-api.service
sudo systemctl enable --now cpa-web-ingress-allow.service
sudo systemctl enable --now cpa-web-ingress-allow.timer
sudo systemctl enable --now cpamp-postgres-backup.timer
```

Run one backup immediately and verify both configured databases produced valid
custom-format dumps:

```bash
sudo systemctl start cpamp-postgres-backup.service
sudo -u postgres pg_restore --list \
  /var/backups/cpa-manager-plus/postgresql/<database>.<timestamp>.dump >/dev/null
```

## 6. Verify

Pass the public origin only when testing the public path:

```bash
sudo CPAMP_PUBLIC_ORIGIN=https://public.example.com \
  /usr/local/sbin/verify-cpamp-native-edge
```

Then perform an authenticated `/v1/models` and a minimal `/v1/responses` request
from a trusted client. Do not put the key in shell history or CI logs. Expected
unauthenticated checks are:

```text
GET  /health             -> 200
OPTIONS /v1/responses    -> 204
GET  127.0.0.1:8317/v1/models without a key -> 401 (CPA is reachable)
```

Check that the IPv4 and IPv6 `INPUT` chains start with `CPA-WEB-INGRESS`. If a
cloud security agent has `YJ-FIREWALL-INPUT`, its first rule also accepts only
TCP 80/443 before source-IP rejects. SSH remains governed by the existing
Fail2ban/cloud policy.

## Rollback

Do not roll database state backward casually. For an entry-only rollback,
restore the previous Nginx/unit files from the backup taken before installation
and reload systemd/Nginx. To restore the old Manager pipeline, first stop the
separated API and new workers, confirm there is no live consumer, then enable
the old units. Never leave both collectors active.
