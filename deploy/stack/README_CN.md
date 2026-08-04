# CPA + Manager Plus + Redis + PostgreSQL 部署

这套 Compose 编排复现当前的四层运行架构，并把控制台、持久化 worker 和聚合 worker 一并纳入启动流程：

```text
浏览器 -> CPAMP Console -> CPAMP API -> CPA
                              |
CPA usage queue -> collector -> Redis Stream -> persist worker -> PostgreSQL
                                                        |
                                             hourly/daily rollup
```

PostgreSQL 是业务配置和用量记录的持久数据源；Redis 只承担用量流、监控缓存、登录会话和限流。Redis 与 PostgreSQL 不暴露宿主机端口。

Operations 页面通过只读挂载的 Linux 聚合指标文件读取宿主机 CPU、内存、负载、运行时间、网络、主机名和系统版本；不会挂载宿主机进程目录、命令行或环境变量。磁盘容量取 Manager 数据卷所在的宿主机文件系统。

## 首次启动

需要 Linux、Docker Engine 和 Docker Compose v2。克隆后执行：

```bash
git clone https://github.com/Jayin9705/CPA-Manager-Plus.git
cd CPA-Manager-Plus
./bin/bootstrap-stack.sh
```

脚本会在 `deploy/stack/runtime/` 生成本机配置和高强度随机密钥，然后构建并启动全部服务。它不会在终端显示任何密钥。默认只监听 `127.0.0.1`，入口为：

- 控制台：`http://localhost:18080`
- Manager API：`http://localhost:18318`
- CPA：`http://localhost:8317`

管理员邮箱来自 `deploy/stack/runtime/.env`，默认是 `admin@localhost`。管理员密码和演示 client key 分别保存在：

```text
deploy/stack/runtime/secrets/cpamp-admin-password
deploy/stack/runtime/secrets/cpa-demo-client-key
```

需要查看时只在可信终端读取这些本机文件。不要把其内容粘贴到 issue、日志或提交中。

如果先修改域名、端口或 HTTPS 设置，可执行：

```bash
./bin/bootstrap-stack.sh --init-only
```

编辑 `deploy/stack/runtime/.env` 后再次运行 `./bin/bootstrap-stack.sh`。远程访问建议保留 loopback 监听并在前面配置 HTTPS 反向代理；使用 HTTPS 时把控制台/API origin 改成真实的精确 origin，并设置 `CPAMP_COOKIE_SECURE=true`。只有明确了解暴露风险时才把 bind address 改成 `0.0.0.0`。

## 日常操作

```bash
./bin/stack.sh ps
./bin/stack.sh logs -f cpamp-api cpamp-usage-persist
./bin/stack.sh up -d --build
./bin/stack.sh down
```

`down` 不会删除 PostgreSQL、Redis 和 Manager 数据卷。不要使用 `down -v`，除非已经确认要永久删除数据。

## 敏感信息边界

Git 仓库只保存模板和初始化逻辑，不保存以下内容：

- CPA management key、client API key 和 OAuth/provider 凭据；
- CPAMP 管理员密码、兼容 admin key、数据加密 key；
- PostgreSQL/Redis 密码及带密码的 DSN；
- CPA auth 文件、运行日志、数据库数据、导入文件和备份。

这些内容全部位于被 `.gitignore` 排除的 `deploy/stack/runtime/`。Docker 构建上下文也会通过 `.dockerignore` 排除该目录。

数据备份与密钥备份必须分开、安全保存。数据库备份示例：

```bash
mkdir -p deploy/stack/runtime/backups
./bin/stack.sh exec -T postgres pg_dump -U cpamp -d cpamp -Fc \
  > deploy/stack/runtime/backups/cpamp.dump
```

同时离线备份 `deploy/stack/runtime/secrets/` 和 `deploy/stack/runtime/cliproxyapi/auths/`；没有原数据加密 key，将无法恢复 PostgreSQL 中的加密 Manager 配置。备份目录同样不会进入 Git。

## 从现网切换

不要让这套 stack 与现有 Manager collector 同时消费同一套 CPA usage queue/Redis Stream，否则会产生双消费者竞争。现网切换前应先备份数据，再停止原生的 Manager、persist 和 rollup worker，确认端口及数据迁移方案后才启动新 stack。

本部署不会把现网数据库或密钥上传 GitHub，也不会自动复制现网敏感数据。GitHub 负责保存“可重建的程序与编排”；运行数据应走独立的加密备份/恢复流程。
