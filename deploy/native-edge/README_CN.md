# 原生边缘部署

本目录用于复现原生 Linux 生产架构：分离式 CPAMP Console/API、CLIProxyAPI、
Mihomo、Redis、PostgreSQL、用量 worker、Nginx 和定时备份均由 systemd 管理，
不依赖 Docker Compose。

```text
公网 :80/:443
      |
    Nginx
      +-- /separated-console/* ------------> 静态 Console
      +-- CPAMP 管理/API 路径 -------------> 127.0.0.1:18318
      +-- 模型 API、OAuth 回调及兜底 ------> 127.0.0.1:8317
                                                |
                                           CLIProxyAPI -> Mihomo

CPA usage queue -> 分离 API/collector -> Redis Stream
                                         |
                                  persist worker -> PostgreSQL
                                                       |
                                              hourly/daily rollup
```

## 安全边界

- 公网仅开放 Nginx 的 TCP 80/443；CPA、CPAMP、Redis 和 PostgreSQL 保持本机监听。
- `cpa-web-ingress-allow` 只让 80/443 绕过来源 IP 拒绝链，不会放开 SSH 或其他端口。
  SSH 继续由 Fail2ban 和云安全策略保护。
- Git 中不得保存真实域名专用密钥、OAuth JSON、API Key、Management Key、data key、
  DSN 密码、证书、数据库 dump 或运行环境文件。
- 私密值放在 `/etc/cpa-manager-plus/secrets/` 的 0600 文件中。模板使用 `*_FILE`
  参数，避免密钥出现在 unit 命令行。
- 浏览器可读的 `runtime-config.json` 只能包含公开 origin，不能包含任何凭据。

## 安装前提

服务器需要 Nginx、iptables/ip6tables、Redis、PostgreSQL 客户端工具、curl、
Python 3 和 systemd，并准备以下二进制：

```text
/opt/cliproxyapi/cli-proxy-api
/opt/cpa-manager-plus-separated/bin/cpa-manager-plus-api
/opt/cpa-manager-plus/cpamp-migration
/usr/local/bin/mihomo
```

默认服务用户为 `cliproxyapi`、`cpa-manager-plus`、`redis`、`postgres` 和
`mihomo`。不同发行版的 PostgreSQL unit 名称不同，安装前需要把模板中的
`postgresql.service` 改成实际名称，例如 `postgresql@18-main.service`。

## 配置目录与私密文件

```bash
sudo install -d -o root -g cpa-manager-plus -m 0750 /etc/cpa-manager-plus-separated
sudo install -d -o root -g cpa-manager-plus -m 0750 /etc/cpa-manager-plus/secrets
sudo install -d -o cpa-manager-plus -g cpa-manager-plus -m 0700 /var/lib/cpa-manager-plus
sudo install -d -o redis -g redis -m 0750 /var/lib/redis/cpamp-usage
sudo install -d -o postgres -g postgres -m 0700 /var/backups/cpa-manager-plus/postgresql
```

复制并修改示例配置：

```bash
sudo install -m 0600 deploy/native-edge/config/api.env.example \
  /etc/cpa-manager-plus-separated/api.env
sudo install -m 0600 deploy/native-edge/config/worker.env.example \
  /etc/cpa-manager-plus-separated/worker.env
sudo install -m 0600 deploy/native-edge/config/backup.env.example \
  /etc/cpa-manager-plus-separated/backup.env
sudo install -o root -g cpa-manager-plus -m 0640 \
  deploy/native-edge/config/config.json.example \
  /etc/cpa-manager-plus-separated/config.json
sudo install -o root -g redis -m 0640 \
  deploy/native-edge/config/redis.conf.example /etc/redis/cpamp-usage.conf
sudo install -o root -g cliproxyapi -m 0640 \
  deploy/native-edge/config/cliproxyapi.config.example.yaml \
  /etc/cliproxyapi/config.yaml
sudo install -d -o root -g mihomo -m 0750 /etc/mihomo
sudo install -o root -g mihomo -m 0640 \
  deploy/native-edge/config/mihomo.config.example.yaml /etc/mihomo/config.yaml
```

把 `public.example.com` 替换为精确的生产 HTTPS origin。由
`cpa-manager-plus` 读取的 secret 文件应为 `cpa-manager-plus:cpa-manager-plus`
且权限为 0600；环境文件由 systemd 在降权前读取，应保持 root:root 0600。

私有 CLIProxyAPI 配置中的两个 `CHANGE_ME` 必须替换。全局代理使用完整的本机
Mihomo URL `http://127.0.0.1:7890`；没有账号级覆盖的认证文件会继承该值。

本机 PostgreSQL peer 认证的 DSN 文件内容示例：

```text
user=cpa-manager-plus dbname=cpamp_usage host=/var/run/postgresql
user=cpa-manager-plus dbname=sub2api host=/var/run/postgresql
```

接入已有数据时必须沿用原 Manager 的 data key；生成新 key 会导致已有加密配置
无法解密。分离 Console 构建产物安装到
`/opt/cpa-manager-plus-separated/console/`，公开运行配置保存为
`runtime-config.json`。

Mihomo 示例仅提供本机 7890 监听和直连基线。真实节点、订阅地址及凭据只能写入
服务器私有的 `/etc/mihomo/config.yaml`，不得提交。

## 安装脚本与 systemd unit

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

如果 CLIProxyAPI 已有自定义 unit，不要直接覆盖，只合并以下依赖：

```ini
After=network-online.target mihomo.service
Wants=network-online.target mihomo.service
```

认证文件审计不会显示 JSON 内容或 token。`--fix` 只删除格式错误的非空代理覆盖，
并把 JSON 权限收紧为 0600：

```bash
sudo /usr/local/sbin/cpa-auth-proxy-audit
sudo /usr/local/sbin/cpa-auth-proxy-audit --fix
```

账号级代理字段缺失或为空时继承 CPA 的有效全局代理。非空值必须是含 scheme 和
host 的完整代理 URL，不能使用 `"proxy_url": "3"` 一类占位值。

## Nginx 同域分流

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

`$connection_upgrade` 只在客户端实际请求 Upgrade 时才发送升级头，普通 `/v1`
请求不会被固定伪装为 WebSocket。最终 `location /` 必须兜底到 CPA，以支持模型
接口、OAuth 回调、WebSocket 和未来新增路径。

## 单一数据链切换

旧 Manager collector/SQLite relay 与分离 API 不能同时消费同一套 usage queue 或
Redis Stream。先做备份，再停用旧单元；保留 unit 文件便于回滚：

```bash
sudo systemctl disable --now cpa-manager-plus.service cpamp-usage-relay.service
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

立即执行一次备份，并用 `pg_restore --list` 校验 `cpamp_usage` 和 `sub2api`
两个 dump。备份脚本默认保留 14 天。

## 验证

```bash
sudo systemctl start cpamp-postgres-backup.service
sudo CPAMP_PUBLIC_ORIGIN=https://public.example.com \
  /usr/local/sbin/verify-cpamp-native-edge
```

预期结果：`/health` 为 200、`OPTIONS /v1/responses` 为 204、本机未鉴权
`/v1/models` 为 401（表示 CPA 可达）。随后在可信客户端使用普通 CPA API Key
验证 `/v1/models` 和最小 `/v1/responses`，不要把密钥写进 shell 历史或 CI 日志。

IPv4/IPv6 `INPUT` 第一条应为 `CPA-WEB-INGRESS`。如果存在
`YJ-FIREWALL-INPUT`，其第一条也应只 ACCEPT TCP 80/443，后续来源 IP 拒绝规则
继续有效；SSH Fail2ban 不受影响。

## 回滚

入口回滚可恢复安装前备份的 Nginx/unit 并重载。若需恢复旧 Manager 数据链，
必须先停止分离 API 和新 worker，确认没有活跃消费者后再启用旧单元。禁止同时
运行两套 collector，也不要在没有一致性备份时回滚 PostgreSQL 数据。
