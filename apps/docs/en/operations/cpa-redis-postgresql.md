# CPA + CPAMP + Redis + PostgreSQL Architecture And Installation

This page documents the PostgreSQL-only runtime. It applies when CPA /
CLIProxyAPI is already running and CPAMP uses Redis to transport usage events
while PostgreSQL stores durable core and usage data.

> Current mode: `CPAMP_PERSISTENCE_BACKEND=postgres`. SQLite is not an online
> source of truth. It is retained only for offline rollback or historical
> migration material. Redis provides queues, cache, and sessions; it is not the
> final durable database.

## 1. Architecture

```text
Client (Codex / Claude Code / OpenAI-compatible)
                    |
                    v
          CPA / CLIProxyAPI :8317
          - model routing and providers
          - Management API
          - usage queue
                    |
                    v
          CPAMP Manager/API :18317 or :18318
          - management APIs and automation
          - the single CPA usage consumer
          - usage sink = Redis Stream
          - PostgreSQL-backed reads
             |             |              |
             |             |              +--> Redis cache/session/rate-limit
             |             |
             |             +-----------------> PostgreSQL cpamp_core
             |
             +--> Redis Stream cpamp:usage:v1
                         |
                         v
              cpamp-migration persist worker
                         |
                         v
              PostgreSQL cpamp_usage raw events
                         |
                         +--> hourly rollup worker
                         +--> daily rollup worker

          PostgreSQL sub2api.cpamp_auth.active_admins
          (minimal read-only administrator view)
```

Clients send model requests to CPA. CPAMP is the management and observability
service; it is not a second model proxy.

## 2. Component Responsibilities

| Component          | Port/entry             | Responsibility                                                      | Source of truth                      |
| ------------------ | ---------------------- | ------------------------------------------------------------------- | ------------------------------------ |
| CPA / CLIProxyAPI  | `8317`                 | Model routing, providers, auth files, Management API, usage queue   | Model traffic                        |
| CPAMP API          | `18317` or `18318`     | Management, monitoring/analytics API, automation, and configuration | Core/usage read service              |
| Redis              | `127.0.0.1:6379`       | Usage Stream, short cache, administrator sessions, login limits     | No; Stream still needs persistence   |
| PostgreSQL core    | `cpamp_core` schema    | Settings, prices, aliases, inspections, cooldowns, dead letters     | Yes                                  |
| PostgreSQL usage   | `cpamp_usage` schema   | Raw events, identity ledger, rollups, migration state               | Yes                                  |
| PostgreSQL auth    | Existing `sub2api` DB  | `cpamp_auth.active_admins` read-only view                            | Administrator identity source        |
| SQLite             | No online connection   | Historical migration/offline rollback copy                          | No                                   |

Important Redis keys:

- Usage Stream: `cpamp:usage:v1`
- Consumer group: `cpamp-persist-v1`
- Monitoring cache prefix: `cpamp:monitoring:v1:`
- Administrator session prefix: `cpamp:auth:session:`

A Redis message is durable only after the PostgreSQL transaction commits and
the worker completes `XACK`. With `--delete-after-ack`, delete only the exact
acknowledged Stream ID; never delete pending entries.

## 3. Write And Read Paths

Write path:

1. CPA accepts a client request and creates a usage event.
2. One CPAMP collector reads the CPA usage queue.
3. The collector normalizes the event and uses `XADD` on the Redis Stream.
4. `cpamp-migration persist` consumes through a consumer group.
5. The worker inserts into `cpamp_usage.usage_events` in a transaction, using
   `event_hash` for idempotency.
6. Hourly and daily workers advance rollups and the identity ledger.

Do not start a second CPAMP collector for the same CPA. Do not run two
destructive queue consumers for shadow validation.

Dashboard, Usage, Monitoring, and model-price summaries read PostgreSQL. Redis
is a best-effort cache for hot Monitoring responses. PostgreSQL-only mode must
fail closed when PostgreSQL is unavailable; it must not silently fall back to
SQLite. Keep the original `data.key`, which decrypts the stored CPA Management
Key.

## 4. Prerequisites

Prepare:

1. CPA / CLIProxyAPI with a Management API reachable from CPAMP.
2. Redis bound to a private address or loopback.
3. PostgreSQL connections for core, usage, and administrator authentication.
4. A CPAMP service user that can read its secrets and write its data directory.
5. An HTTPS reverse proxy when the service is public.

CPA must expose remote management to the private CPAMP connection and publish
usage statistics:

```yaml
remote-management:
  allow-remote: true
  secret-key: '<CPA_MANAGEMENT_KEY>'

usage-statistics-enabled: true
```

Never commit the real key or include it in screenshots and public issues.

For the host-native separated deployment, use the checked-in
[Native Edge Deployment](../deployment/native-edge.md) package. It keeps only
Nginx on public `80/443` and provides systemd, ingress, proxy-audit, and backup
templates.

## 5. PostgreSQL Initialization

Create a dedicated role and database; substitute the password, host, and TLS
policy for the target environment:

```sql
CREATE ROLE cpamp_manager LOGIN PASSWORD '<strong-password>';
CREATE DATABASE cpamp_usage OWNER cpamp_manager;
```

CPAMP creates the core schema and core migration table at startup. The
`cpamp-migration prepare` command creates the usage schema.

PostgreSQL-only login reads a minimal view from the database selected by
`CPAMP_AUTH_PG_DSN`. The database owner must create a security-barrier view that
exposes only `id`, `email`, and `password_hash`:

```sql
CREATE SCHEMA IF NOT EXISTS cpamp_auth;

CREATE OR REPLACE VIEW cpamp_auth.active_admins
WITH (security_barrier = true)
AS
SELECT id, email, password_hash
FROM public.users
WHERE <active-admin-predicate>;

GRANT USAGE ON SCHEMA cpamp_auth TO cpamp_manager;
GRANT SELECT ON cpamp_auth.active_admins TO cpamp_manager;
```

Replace the predicate after reviewing the real user schema. Do not grant CPAMP
direct access to `public.users`.

## 6. Build The Binaries

```bash
cd CPA-Manager-Plus
npm ci
npm run build

cd apps/manager-server
CGO_ENABLED=0 go build -o /opt/cpa-manager-plus/cpa-manager-plus ./cmd/cpa-manager-plus
CGO_ENABLED=0 go build -o /opt/cpa-manager-plus/cpamp-migration ./cmd/cpamp-migration
CGO_ENABLED=0 go build -o /opt/cpa-manager-plus/cpamp-core-migration ./cmd/cpamp-core-migration
```

The API-only binary can be built through `Dockerfile.manager-server-api` or the
repository's API build tag. Redis, PostgreSQL, and persist/rollup workers remain
separate runtime dependencies.

## 7. PostgreSQL-Only Configuration

Prefer root-owned systemd environment files and service-readable secret files.
The application supports `*_FILE` variants, which keep DSNs and keys out of
unit command lines:

```dotenv
HTTP_ADDR=127.0.0.1:18318
USAGE_DATA_DIR=/var/lib/cpa-manager-plus
CPA_UPSTREAM_URL=http://127.0.0.1:8317
CPA_MANAGEMENT_KEY_FILE=/etc/cpa-manager-plus/secrets/cpa-management-key
CPA_MANAGER_DATA_KEY_FILE=/etc/cpa-manager-plus/secrets/data-key

CPAMP_PERSISTENCE_BACKEND=postgres
CPAMP_CORE_PG_DSN_FILE=/etc/cpa-manager-plus/secrets/core-pg-dsn
CPAMP_USAGE_PG_DSN_FILE=/etc/cpa-manager-plus/secrets/usage-pg-dsn
CPAMP_AUTH_PG_DSN_FILE=/etc/cpa-manager-plus/secrets/auth-pg-dsn

CPAMP_USAGE_SINK=redis
CPAMP_USAGE_REDIS_ADDR=127.0.0.1:6379
CPAMP_USAGE_REDIS_DB=0
CPAMP_USAGE_REDIS_STREAM=cpamp:usage:v1
CPAMP_USAGE_REDIS_GROUP=cpamp-persist-v1
CPAMP_AUTH_REDIS_ADDR=127.0.0.1:6379
CPAMP_AUTH_REDIS_DB=0
CPAMP_USAGE_SQLITE_HOURLY_ROLLUP_ENABLED=false
CPAMP_USAGE_SQLITE_ACCOUNT_ROLLUP_ENABLED=false
```

PostgreSQL-only mode requires all three PostgreSQL connections, Redis, a Redis
usage sink, and the `cpamp_auth.active_admins` view. A missing dependency must
stop startup instead of enabling a SQLite fallback.

## 8. Initialize Or Migrate Usage Data

Back up and stop every SQLite writer first. Use the same private PostgreSQL and
Redis configuration for all migration commands:

```bash
export CPAMP_USAGE_PG_DSN_FILE=/etc/cpa-manager-plus/secrets/usage-pg-dsn
export CPAMP_USAGE_REDIS_ADDR=127.0.0.1:6379

/opt/cpa-manager-plus/cpamp-migration prepare \
  --sqlite /var/lib/cpa-manager-plus/usage.sqlite

/opt/cpa-manager-plus/cpamp-migration backfill \
  --sqlite /var/lib/cpa-manager-plus/usage.sqlite \
  --batch-size 500 --max-batches 0

/opt/cpa-manager-plus/cpamp-migration init-stream

/opt/cpa-manager-plus/cpamp-migration verify \
  --sqlite /var/lib/cpa-manager-plus/usage.sqlite
```

Run `cpamp-core-migration sync --replace` only before PostgreSQL becomes the
live core source. Running it again after cutover can overwrite live writes.

## 9. systemd Ordering

The production dependency shape is:

```text
PostgreSQL / Redis / CPA
        |
        +--> persist worker
        +--> hourly rollup worker
        +--> daily rollup worker
        +--> CPAMP API
```

The persist and rollup commands can read `CPAMP_USAGE_PG_DSN_FILE`; do not put a
password-bearing `--pg-dsn` in `ExecStart`. Redis should use AOF or another
appropriate persistence policy, `noeviction`, and a capacity limit.

Only one collector may own the live CPA usage queue. During a separated-API
cutover, disable the old Manager collector and SQLite relay before enabling the
new API and workers.

## 10. Verification

```bash
curl -f http://127.0.0.1:18318/health
systemctl is-active cpa-manager-plus-separated-api \
  cpamp-usage-persist cpamp-usage-rollup-hourly cpamp-usage-rollup-daily

redis-cli -h 127.0.0.1 XINFO GROUPS cpamp:usage:v1
```

Confirm:

- The console loads and administrator email/password login succeeds.
- The persist group's pending count is zero or explainable.
- Raw event, distinct `event_hash`, and identity-ledger counts agree.
- Hourly and daily checkpoints keep advancing.
- New requests appear in Monitoring and Usage Analytics.
- The PostgreSQL-only process does not open `usage.sqlite`.
- Logs contain no fallback, backend error, panic, or duplicate collector.

## 11. Backup And Rollback

Back up PostgreSQL core/usage data, the original `data.key`, private CPA/CPAMP
configuration, and a pre-migration SQLite copy. Redis persistence is useful for
recovery continuity but is not the final source of truth.

For rollback, stop the new API and workers before enabling the old Manager.
Never leave both collectors active. Do not truncate PostgreSQL, reset the Redis
consumer group, or discard post-cutover events. Keep the new PostgreSQL state
for reconciliation after the incident is fixed.
