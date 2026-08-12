---
title: 原生 systemd 边缘部署
description: 使用 Nginx、分离式 CPAMP API、CLIProxyAPI、Mihomo、Redis、PostgreSQL 和 systemd 部署完整生产链路。
---

# 原生 systemd 边缘部署

该方案适合已经使用 systemd 管理服务、由宿主机 Nginx 终止 TLS 的 Linux
服务器。公网只开放 `80/443`；CPA `8317`、分离 API `18318`、Mihomo `7890`、
Redis 和 PostgreSQL 均保持本机监听。

仓库中的 [`deploy/native-edge/`](https://github.com/Jayin9705/CPA-Manager-Plus/blob/main/deploy/native-edge/README_CN.md)
提供完整可复制模板：

- Nginx 同域 Console、管理 API 和模型 API 分流；
- 仅在客户端请求 Upgrade 时设置 WebSocket `Connection` 头；
- CLIProxyAPI 对 Mihomo 的 systemd 启动依赖；
- IPv4/IPv6 TCP 80/443 在云安全来源 IP 拒绝链之前放行；
- OAuth JSON 代理覆盖审计和 0600 权限修复；
- Redis、persist、hourly/daily rollup 与分离 API unit；
- `cpamp_usage`、`sub2api` 双数据库定时备份；
- 不输出密钥的运行状态验证脚本。

## 适用边界

这不是 Docker 完整栈安装器。只有在二进制和数据库已经准备好、需要复现原生
生产拓扑时使用。首次部署请先阅读
[CPA + Redis + PostgreSQL 架构与安装](../operations/cpa-redis-postgresql.md)。

切换时只允许一个 collector 消费 live CPA usage queue。启用分离 API 和新 worker
之前，必须停用旧 `cpa-manager-plus.service` 和 `cpamp-usage-relay.service`。

## 安全要求

不要把真实 OAuth JSON、API Key、Management Key、data key、DSN、Mihomo 订阅、
证书或数据库 dump 复制进 Git。部署模板使用 `*_FILE` 指向 0600 私密文件。

Web 放行脚本只接受 80/443，不改变 SSH；继续保持 Fail2ban 和云安全 SSH 策略。

## 验证结果

部署后应验证：

```text
GET /health                  -> 200
OPTIONS /v1/responses        -> 204
GET /v1/models（普通 API Key）-> 200
最小 /v1/responses           -> 200 completed
```

完整安装、备份、切换、验证和回滚命令见
[`deploy/native-edge/README_CN.md`](https://github.com/Jayin9705/CPA-Manager-Plus/blob/main/deploy/native-edge/README_CN.md)。
