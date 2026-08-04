# CPA + CPAMP + Redis + PostgreSQL 当前架构与安装

本文记录当前 PostgreSQL-only 运行模式。适用于 CPA / CLIProxyAPI 已运行，CPAMP Manager Server 使用 Redis 传递 usage event、使用 PostgreSQL 保存 core 和 usage 数据的部署。

> 当前实现状态：`CPAMP_PERSISTENCE_BACKEND=postgres`。SQLite 不再是线上事实源，只保留为离线回滚或历史迁移材料。Redis 负责队列、缓存和会话，不作为最终持久化数据库。

## 1. 总体结构

```text
客户端（Codex / Claude Code / OpenAI-compatible）
                    |
                    v
          CPA / CLIProxyAPI :8317
          - 模型路由与上游 Provider
          - Management API
          - usage queue
                    |
                    | CPA 管理请求、usage queue
                    v
          CPAMP Manager Server :18317
          - management.html 与管理 API
          - 唯一 CPA usage consumer
          - usage sink = Redis Stream
          - PG 读路径、业务 API、自动化
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
          （只读管理员视图；登录密码仍由现有用户表校验）
```

客户端只访问 CPA 的模型接口；CPAMP 是管理和观测服务，不独立转发模型请求。

## 2. 组件职责

| 组件                 | 端口/入口             | 当前职责                                                          | 是否事实源                                    |
| -------------------- | --------------------- | ----------------------------------------------------------------- | --------------------------------------------- |
| CPA / CLIProxyAPI    | `8317`                | 模型路由、Provider、认证文件、Management API、usage queue         | 模型请求事实源                                |
| CPAMP Manager Server | `18317`               | 管理面板、CPA 代理、监控/分析 API、自动化和配置                   | core/usage 的读服务                           |
| Redis                | 默认 `127.0.0.1:6379` | usage Stream、监控短缓存、管理员 session、登录限流                | 否，必须可丢缓存；Stream 需有持久化和容量策略 |
| PostgreSQL core      | `cpamp_core` schema   | settings、model prices、API key alias、巡检、冷却、账号处理、死信 | 是                                            |
| PostgreSQL usage     | `cpamp_usage` schema  | raw usage events、identity ledger、小时/天汇总、迁移状态          | 是                                            |
| PostgreSQL auth      | 现有 `sub2api` 数据库 | `cpamp_auth.active_admins` 最小只读视图                           | 管理员账号来源                                |
| SQLite               | 无线上连接            | 历史迁移/离线回滚副本                                             | 否                                            |

### Redis key 与 Stream

- usage Stream：`cpamp:usage:v1`
- consumer group：`cpamp-persist-v1`
- 监控缓存前缀：`cpamp:monitoring:v1:`，默认短 TTL
- 管理 session 前缀：`cpamp:auth:session:`
- 登录限流使用 Redis 短 TTL key

Redis 消息只有在 PostgreSQL 事务提交并完成 `XACK` 后才算持久化成功。可选的 `--delete-after-ack` 会在 ACK 后删除已落库的精确 Stream ID；不得删除 pending 消息。

## 3. 事件流与读流

### 写入链路

1. 客户端请求进入 CPA，CPA 产生 usage event。
2. CPAMP 是唯一 CPA consumer，读取 CPA usage queue。
3. collector 将规范化 event `XADD` 到 Redis Stream。
4. `cpamp-migration persist` 以 consumer group 消费 Stream。
5. persist worker 在 PostgreSQL 事务中写入 `cpamp_usage.usage_events`，以 `event_hash` 幂等。
6. `cpamp-usage-rollup-hourly` 与 `cpamp-usage-rollup-daily` 推进汇总和 ledger。

不要为同一个 CPA 启动第二个 CPAMP collector，也不要用两个 destructive queue consumer 做影子迁移。

### 查询链路

- Dashboard、Usage、Monitoring 和 model-price usage summary 优先读取 PostgreSQL。
- Monitoring 的热点结果使用 Redis best-effort cache；缓存不可用时回源 PostgreSQL。
- Redis 故障不能改变 PostgreSQL 已保存的数据；PG 故障时 PostgreSQL-only 主进程应拒绝启动或停止提供服务，不回退到 SQLite。
- `data.key` 仍需作为本地 secret 保存，用于解密 CPAMP 保存的 CPA Management Key。

## 4. 安装前准备

准备以下资源：

1. 已运行的 CPA / CLIProxyAPI，并允许 CPAMP 访问 Management API。
2. 一个只监听内网或本机的 Redis 实例。
3. 一个 PostgreSQL 实例，以及两个 CPAMP 连接数据库（可以是同一数据库的不同 schema）：
   - core 和 usage 使用 `CPAMP_CORE_PG_DSN`、`CPAMP_USAGE_PG_DSN`；
   - 管理员认证使用现有 CPA 用户数据库 `CPAMP_AUTH_PG_DSN`，默认是 `sub2api`。
4. CPAMP 运行用户对数据目录、`data.key` 和 panel 文件有读写权限。
5. 反向代理（如果暴露公网）只转发 `18317`，并启用 HTTPS。

CPA 至少需要打开远程管理和 usage 统计，示例配置只展示结构：

```yaml
remote-management:
  allow-remote: true
  secret-key: '<CPA_MANAGEMENT_KEY>'

usage-statistics-enabled: true
```

不要把真实 key 写入仓库、Compose 文件、截图或公开 issue。

## 5. PostgreSQL 初始化

### 5.1 CPAMP core/usage 数据库

为 CPAMP 建立独立角色和数据库；密码、主机和 SSL 参数按实际环境替换：

```sql
CREATE ROLE cpamp_manager LOGIN PASSWORD '<strong-password>';
CREATE DATABASE cpamp_usage OWNER cpamp_manager;
```

`cpamp-manager-plus` 启动时会自动创建 `cpamp_core` schema 和 core schema migration 表。usage schema 由迁移工具的 `prepare` 阶段创建。

### 5.2 管理员只读视图

PG-only 登录不再使用旧 SQLite 中的管理员 key，而是从 `CPAMP_AUTH_PG_DSN` 指向的现有用户数据库读取 `cpamp_auth.active_admins`。视图必须由数据库 owner 创建，并只投影 `id`、`email`、`password_hash` 三列。

下面是模板；`<active-admin-predicate>` 必须根据实际 `public.users` schema 替换，不能未经检查直接复制到生产库：

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

不要向 CPAMP 角色授予 `public.users` 的直接 SELECT 权限。验证视图能够返回至少一个管理员，并确认归一化 email 不重复、密码字段是现有系统使用的 bcrypt hash。

## 6. 构建二进制

仓库包含三个相关程序：主服务、usage 迁移/后台 worker、core 数据迁移工具。

```bash
cd CPA-Manager-Plus
npm ci
npm run build

cd apps/manager-server
CGO_ENABLED=0 go build -o /opt/cpa-manager-plus/cpa-manager-plus ./cmd/cpa-manager-plus
CGO_ENABLED=0 go build -o /opt/cpa-manager-plus/cpamp-migration ./cmd/cpamp-migration
CGO_ENABLED=0 go build -o /opt/cpa-manager-plus/cpamp-core-migration ./cmd/cpamp-core-migration

install -m 0644 ../../apps/web/dist/index.html /var/lib/cpa-manager-plus/management.html
```

如果使用 Docker，`Dockerfile.manager-server` 会把前端内置到镜像；Redis、PostgreSQL 和 persist/rollup worker 仍需作为独立依赖或独立服务运行。

## 7. CPAMP 配置

建议使用 systemd `EnvironmentFile` 或 secret manager。下面只保留变量名和示例值：

```dotenv
HTTP_ADDR=127.0.0.1:18317
USAGE_DATA_DIR=/var/lib/cpa-manager-plus
PANEL_PATH=/var/lib/cpa-manager-plus/management.html
CPA_UPSTREAM_URL=http://127.0.0.1:8317
CPA_MANAGEMENT_KEY_FILE=/etc/cpa-manager-plus/secrets/management-key
CPA_MANAGER_DATA_KEY_PATH=/var/lib/cpa-manager-plus/data.key

CPAMP_PERSISTENCE_BACKEND=postgres
CPAMP_CORE_PG_DSN=postgresql://cpamp_manager:<password>@127.0.0.1:5432/cpamp_usage?sslmode=disable
CPAMP_USAGE_PG_DSN=postgresql://cpamp_manager:<password>@127.0.0.1:5432/cpamp_usage?sslmode=disable
CPAMP_AUTH_PG_DSN=postgresql://cpamp_manager:<password>@/sub2api?host=/var/run/postgresql

CPAMP_USAGE_SINK=redis
CPAMP_USAGE_REDIS_ADDR=127.0.0.1:6379
CPAMP_USAGE_REDIS_DB=0
CPAMP_USAGE_REDIS_STREAM=cpamp:usage:v1
CPAMP_USAGE_REDIS_GROUP=cpamp-persist-v1
CPAMP_AUTH_REDIS_ADDR=127.0.0.1:6379
CPAMP_AUTH_REDIS_DB=0
CPAMP_MONITORING_CACHE_ENABLED=true
CPAMP_MONITORING_CACHE_TTL_SECONDS=5
CPAMP_USAGE_SQLITE_HOURLY_ROLLUP_ENABLED=false
CPAMP_USAGE_SQLITE_ACCOUNT_ROLLUP_ENABLED=false
```

PG-only 模式的硬性要求：

- `CPAMP_CORE_PG_DSN`、`CPAMP_USAGE_PG_DSN`、`CPAMP_AUTH_PG_DSN` 和 Redis 必须可用；
- `CPAMP_USAGE_SINK` 必须是 `redis`；
- 两个 SQLite rollup 开关必须为 `false`；
- Redis Stream/group 必须可创建或已存在；
- `cpamp_auth.active_admins` 必须可读；
- 任一依赖不可用时，主服务不得悄悄切回 SQLite。

DSN 中含有特殊字符的密码应使用 URL 编码，或改用 PostgreSQL `.pgpass` / Unix socket / systemd credential，避免把密码直接写进 unit。

## 8. 初始化 usage schema 与迁移

### 已有 SQLite 数据迁移

先备份并停止会写 SQLite 的旧 CPAMP 实例。使用同一套 PG/Redis 配置执行：

```bash
export CPAMP_USAGE_PG_DSN='<usage-pg-dsn>'
export CPAMP_USAGE_REDIS_ADDR='127.0.0.1:6379'

/opt/cpa-manager-plus/cpamp-migration prepare \
  --sqlite /var/lib/cpa-manager-plus/usage.sqlite \
  --pg-dsn "$CPAMP_USAGE_PG_DSN"

/opt/cpa-manager-plus/cpamp-migration backfill \
  --sqlite /var/lib/cpa-manager-plus/usage.sqlite \
  --pg-dsn "$CPAMP_USAGE_PG_DSN" \
  --batch-size 500 --max-batches 0

/opt/cpa-manager-plus/cpamp-migration init-stream \
  --pg-dsn "$CPAMP_USAGE_PG_DSN" \
  --redis-addr 127.0.0.1:6379

/opt/cpa-manager-plus/cpamp-migration verify \
  --sqlite /var/lib/cpa-manager-plus/usage.sqlite \
  --pg-dsn "$CPAMP_USAGE_PG_DSN"
```

`cpamp-core-migration sync` 只用于把旧 SQLite 的 core durable tables 迁入 `cpamp_core`；执行前必须有一致性备份，并确认目标 PG 还未成为线上写入源：

```bash
/opt/cpa-manager-plus/cpamp-core-migration sync \
  --sqlite /var/lib/cpa-manager-plus/usage.sqlite \
  --pg-dsn '<core-pg-dsn>' \
  --replace

/opt/cpa-manager-plus/cpamp-core-migration verify \
  --sqlite /var/lib/cpa-manager-plus/usage.sqlite \
  --pg-dsn '<core-pg-dsn>'
```

已经完成 PG-only 切换后，禁止再次运行 `sync --replace`，否则可能覆盖线上 core 写入。切换前应在隔离环境验证迁移与回滚，并保留 PostgreSQL、Redis、数据加密 key 和 CPA auth 文件的独立备份。

## 9. systemd 服务顺序

生产环境至少需要以下服务：

```text
PostgreSQL / Redis
        |
        +--> cpamp-usage-persist.service
        +--> cpamp-usage-rollup-hourly.service
        +--> cpamp-usage-rollup-daily.service
        +--> cpa-manager-plus.service
```

persist worker 的核心命令如下；实际部署应放入 root-only unit 或 credential 文件：

```bash
/opt/cpa-manager-plus/cpamp-migration persist \
  --pg-dsn '<usage-pg-dsn>' \
  --redis-addr 127.0.0.1:6379 \
  --stream cpamp:usage:v1 \
  --group cpamp-persist-v1 \
  --consumer cpamp-persist-v1 \
  --batch-size 100 --max-batches 0 --follow \
  --claim-idle 30s --delete-after-ack
```

小时和天 rollup 分别执行：

```bash
/opt/cpa-manager-plus/cpamp-migration rollup --kind hourly \
  --pg-dsn '<usage-pg-dsn>' --batch-size 500 \
  --max-batches 0 --follow --poll-interval 1s

/opt/cpa-manager-plus/cpamp-migration rollup --kind daily \
  --pg-dsn '<usage-pg-dsn>' --batch-size 500 \
  --max-batches 0 --follow --poll-interval 5s
```

主服务应 `Requires` PostgreSQL 和 Redis，并在 persist/rollup 之后启动。Redis 要设置容量上限和 `noeviction`，同时为 PostgreSQL、Redis 和 `data.key` 配置周期备份与恢复演练。

## 10. 验证清单

```bash
curl -f http://127.0.0.1:18317/health
systemctl is-active cpa-manager-plus cpamp-usage-persist \
  cpamp-usage-rollup-hourly cpamp-usage-rollup-daily

/opt/cpa-manager-plus/cpamp-migration status --pg-dsn '<usage-pg-dsn>'
redis-cli -h 127.0.0.1 XINFO GROUPS cpamp:usage:v1
```

确认：

- CPAMP `/management.html` 可以打开并能用管理员 email/password 登录；
- `cpamp-persist-v1` 的 pending 持续为 0 或在可解释范围内；
- PostgreSQL raw event、distinct `event_hash`、identity ledger 计数一致；
- hourly/daily checkpoint 持续推进；
- 新请求能在 Monitoring 和 Usage Analytics 中出现；
- `lsof` 或进程命名空间检查确认 PG-only 主进程没有打开 `usage.sqlite`；
- 日志没有 SQLite fallback、Redis/PG backend error、panic 或重复 collector。

## 11. 备份与回滚

至少备份：

- PostgreSQL `cpamp_core` 和 `cpamp_usage` 的逻辑备份；
- Redis 持久化文件或可重建配置（Redis 不是最终事实源）；
- `/var/lib/cpa-manager-plus/data.key`；
- CPA/CPAMP secret 文件和 systemd 配置；
- 迁移前的 SQLite 离线副本。

PG-only 回滚只能作为短时应急：先停止新主服务，恢复经过验证的旧二进制和 SQLite 副本，再启动旧 SQLite 模式。不要 truncate/drop PostgreSQL 或 Redis，也不要重置 consumer group；切换后的新 PG 数据需要另行保留，以便修复后重新校验。
