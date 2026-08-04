# CPA Manager Plus 前后端完全分离与 UI 重构设计

> 状态：Phase 1 implemented（并行部署验证中）
>
> 日期：2026-08-01
>
> 适用范围：当前 CPA + CPA Manager Plus + Redis + PostgreSQL（PostgreSQL-only）完整模式
>
> 核心约束：后端继续使用 Go；Redis/PostgreSQL 的职责、数据模型和事实源不变；现有功能不得丢失。

本仓库已经落地第一阶段实现：`Dockerfile.manager-server-api` 使用 `api_only` 构建纯 Go
API，`Dockerfile.web` 构建独立 console，前端通过 `runtime-config.json` 读取 API origin，
后端提供精确 Origin/CORS、Cookie session、CSRF、request ID 和 v1 兼容别名。旧的
`Dockerfile.manager-server`、内嵌面板和现有线上服务保持不变；当前主机没有执行停止、重启或
数据迁移。独立 API 现在通过 `/readyz` 区分进程存活与 PostgreSQL 认证依赖就绪；OpenAPI 全量生成 client、OAuth transaction、E2E/视觉回归和最终删除旧面板仍属于
后续阶段，见第 12/13 节的验收矩阵。

## 1. 结论与关键决策

目标不是简单地把 `management.html` 挪到另一个目录，而是建立可以独立构建、部署、升级和回滚的前后端边界。

| 维度     | 当前状态                                                   | 目标状态                                                               |
| -------- | ---------------------------------------------------------- | ---------------------------------------------------------------------- |
| 前端交付 | Vite 产物复制/嵌入 Go，或由 `PANEL_PATH` 提供              | 独立静态站点/CDN/Nginx 镜像                                            |
| 后端交付 | Node 构建 + Go 构建合成一个产物                            | 纯 Go API/worker 产物，不含 UI                                         |
| 访问入口 | 页面和 API 同一个 Manager Server 域名                      | `console.<domain>` 与 `api.<domain>` 独立                              |
| API 地址 | 从 `window.location` 推断，兼容直连 CPA                    | 由无密钥的运行时配置明确指定                                           |
| 认证     | 同源 HttpOnly session；部分模式在浏览器保存 Management Key | 完整模式只使用 API 域 HttpOnly session + CSRF；Management Key 留在后端 |
| 契约     | TypeScript 类型和 Go handler 分散维护                      | OpenAPI 为唯一跨端契约，生成前端 client/types                          |
| 发布     | 前后端同版本、同流水线                                     | 独立镜像、独立版本、兼容窗口和独立回滚                                 |
| Redis/PG | usage Stream、缓存/session、core/usage/auth 事实源         | 完全保持当前职责和数据链路                                             |

推荐保留单一 Git 仓库，但建立严格物理边界和独立流水线。是否拆成两个 Git 仓库不决定前后端是否分离；是否存在跨目录源码复制、联合镜像和共同发布才决定运行时耦合。完成本设计后，如团队需要，前端目录可以无功能变更地迁到单独仓库。

## 2. 目标与非目标

### 2.1 目标

1. 前端可在后端不重新构建的情况下独立发布和回滚。
2. 后端镜像/原生包不再包含 Node、HTML、CSS、JS 或前端静态资源。
3. 完整模式前端只访问 CPAMP Go API，不直接访问 Redis、PostgreSQL 或 CPA Management API。
4. 所有现有页面、管理操作、分析、监控、巡检、插件、导入导出和兼容模式都有明确迁移归宿。
5. 认证、CORS、CSRF、OAuth、插件 iframe 和文件下载在跨域后仍安全、可用。
6. UI 按 `ui-ux-pro-max` 的 Data-Dense、Drill-Down、Real-Time Monitoring 方向统一重构，并达到 WCAG 2.2 AA。
7. 切换过程不修改 Redis Stream、consumer group、PostgreSQL schema 或事实源，不中断用量采集。

### 2.2 非目标

- 不把 CPAMP 变成模型代理；模型请求仍只经过 CPA。
- 不重写 Go 后端业务逻辑，不更换 Redis/PostgreSQL，不引入新的事实源。
- 不在本次迁移中拆分 collector、automation、API 为多个 Go 微服务；主 Go 服务先保持现有职责，避免扩大风险。
- 不强制更换 ECharts、React Router、i18n 或已工作的业务算法。
- 不在切换当天删除旧页面入口；旧入口只在稳定观察和回滚窗口结束后移除。

## 3. “完全分离”的验收定义

以下条件必须全部满足，不能只以“目录里有 `web` 和 `server`”作为完成标准。

| 编号   | 验收条件                                                                                    |
| ------ | ------------------------------------------------------------------------------------------- |
| SEP-01 | 前端和后端分别生成独立、可部署、可回滚的版本化产物。                                        |
| SEP-02 | 后端 Dockerfile/原生包构建不安装 Node，不复制 `dist`，没有 `go:embed web/...`。             |
| SEP-03 | Go router 不再提供 `/management.html`、`/management-assets/*` 或 SPA fallback。             |
| SEP-04 | 前端站点故障不影响 API/collector/persist/rollup；API 故障时前端仍能加载并展示明确离线状态。 |
| SEP-05 | 完整模式前端只连接配置的 API origin；CPA Management Key 不进入浏览器。                      |
| SEP-06 | Redis、PostgreSQL 和 CPA 只在私网/本机可达，浏览器不可直连。                                |
| SEP-07 | 前后端通过版本化 OpenAPI 契约交互，契约破坏性变更受到 CI 阻断。                             |
| SEP-08 | 前后端可独立升级，后端至少兼容当前和前一个前端次版本。                                      |
| SEP-09 | 页面深链、OAuth 回调、插件资源、导入导出和跨域 cookie 均有自动化端到端测试。                |
| SEP-10 | 功能保留矩阵全部通过后，才允许删除嵌入式面板代码。                                          |

## 4. 目标运行架构

```text
                         Internet
                            |
              +-------------+-------------+
              |                           |
              v                           v
   console.<domain>                 api.<domain>
   Frontend CDN/Nginx               Go CPAMP API :18317
   - index.html                     - JSON/file/API only
   - hashed JS/CSS                  - auth/session
   - runtime-config.json            - management proxy
   - independent release            - monitoring/analytics
              |                     - collector/automation
              | HTTPS + cookie/CSRF        |
              +----------------------------+
                                            |
                    +-----------------------+-----------------------+
                    |                       |                       |
                    v                       v                       v
              CPA :8317              Redis :6379             PostgreSQL
              Management API         - usage Stream          - cpamp_core
              usage queue            - cache                 - cpamp_usage
                                     - session/rate limit    - auth view
                                            |
                                            v
                                 cpamp-migration persist
                                            |
                                            v
                              hourly/daily rollup workers
```

### 4.1 浏览器请求流

1. 浏览器从 `console.<domain>` 加载静态 SPA 和 `runtime-config.json`。
2. SPA 从运行时配置取得 `apiOrigin`，不再从页面地址猜测 API。
3. 登录请求发送到 `api.<domain>`，Go API 设置仅属于 API host 的 HttpOnly cookie。
4. SPA 后续请求使用 `credentials: include`，修改请求同时携带 CSRF header。
5. Go API读取 PostgreSQL/Redis，或使用保存在后端并已加密的 Management Key 访问 CPA。
6. API 返回结构化、脱敏数据；浏览器永远不直接获得后端 Management Key、PG DSN 或 Redis 凭证。

### 4.2 数据写入和读取流保持不变

- CPA usage queue → CPAMP 唯一 collector → Redis Stream `cpamp:usage:v1`。
- `cpamp-migration persist` → PostgreSQL `cpamp_usage.usage_events`，继续按 `event_hash` 幂等。
- hourly/daily rollup worker 继续推进 PostgreSQL 汇总。
- Dashboard、Usage、Monitoring、model-price usage summary 继续以 PostgreSQL 为事实源。
- Redis 继续承担短缓存、管理员 session、登录限流和 usage Stream，不升级为事实源。
- PostgreSQL-only 模式依赖失败时继续 fail closed，不允许回退 SQLite。

## 5. 代码仓库与构建边界

### 5.1 推荐目标目录

```text
CPA-Manager-Plus/
├── frontend/
│   ├── package.json
│   ├── package-lock.json
│   ├── Dockerfile
│   ├── nginx.conf
│   ├── public/runtime-config.json.example
│   ├── src/
│   └── tests/
├── backend/
│   ├── go.mod
│   ├── go.sum
│   ├── Dockerfile
│   ├── cmd/
│   │   ├── cpamp-api/
│   │   ├── cpamp-migration/
│   │   └── cpamp-core-migration/
│   ├── internal/
│   └── tests/
├── contracts/
│   └── openapi/cpamp-v1.yaml
├── deploy/
│   ├── frontend/
│   ├── backend/
│   └── compose/
├── design-system/
└── docs/
```

实际迁移时可以先保留 `apps/web` 和 `apps/manager-server` 名称；重要的是满足以下边界：

- 前端拥有自己的 package manifest、lockfile、测试和镜像。
- 后端拥有自己的 Go module、测试和纯 Go 镜像。
- 后端不得读取/复制前端目录；前端不得导入 Go 内部文件。
- 唯一允许共享的机器接口是 `contracts/openapi/cpamp-v1.yaml`。
- 文档、设计系统和部署编排可以留在仓库根目录。

### 5.2 独立产物

前端产物：

- `cpamp-console:<web-version>` 静态镜像，或等价的对象存储/CDN bundle。
- `index.html`：`Cache-Control: no-cache`。
- `/assets/<content-hash>.*`：`Cache-Control: public,max-age=31536000,immutable`。
- `runtime-config.json`：`Cache-Control: no-store`。

后端产物：

- `cpamp-api:<api-version>`，仅包含 Go 二进制和 CA/tzdata。
- `cpamp-migration`、`cpamp-core-migration` 保持 Go 构建。
- 不包含 `management.html`、`management-assets`、Node/npm 或 Vite。

### 5.3 前端运行时配置

同一个前端镜像应能被提升到测试/生产环境，无需为域名重新编译：

```json
{
  "mode": "full",
  "apiOrigin": "https://api.example.com",
  "oauthReturnOrigin": "https://console.example.com",
  "pluginRuntimeOrigin": "https://plugins.example.com",
  "environmentName": "production"
}
```

运行时配置只能包含公开信息，不得包含管理密钥、数据库地址、Redis 密码或 bearer token。配置解析失败时前端进入“部署配置错误”页面，不能静默回退到 `localhost:8317`。

### 5.4 保留轻量面板和 Demo

“功能不丢失”包含当前轻量面板和演示能力，但它们不再混入完整模式运行时：

- `full`：独立 console，email/password + CPAMP session，只访问 Manager API。
- `lightweight`：单独构建产物，继续可由 CPA 托管并使用 CPA Management Key。
- `demo`：单独静态构建，只读取虚构 fixture，禁止连接真实 API。

三种模式复用 UI feature，但有独立入口、运行时策略和测试。完整模式不再显示 CPA 地址/Management Key 作为常规登录字段。

## 6. API 契约设计

### 6.1 版本策略

- 新的 CPAMP-owned API 使用 `/api/v1`。
- CPA 原生代理面在第一阶段继续保留 `/v0/management/*`，确保配置、Provider、Auth Files、OAuth、插件等操作零差异。
- 当前 `/usage-service/*` 和 Plus-owned `/v0/management/*` 通过兼容路由映射到新的 service/controller；至少保留两个稳定版本。
- 新前端最终只调用 OpenAPI 中声明的路径，不拼接未知路径。
- 后端通过 `/api/v1/meta/capabilities` 返回：API 版本、最小/最大 console 版本、feature flags、支持的 provider/plugin 能力。

第一阶段只改变 origin，不改变接口语义；第二阶段再把 Plus-owned API 迁入 `/api/v1`。这样可以把“跨域风险”和“API 重命名风险”分开验证。

### 6.2 资源分组

| 领域         | Canonical API 方向                                    | 保留能力                                                  |
| ------------ | ----------------------------------------------------- | --------------------------------------------------------- | ------ | ----- | --------------------------------------------- |
| 认证         | `/api/v1/auth/login                                   | session                                                   | logout | csrf` | email/password、remember、Redis session、限流 |
| 元数据       | `/healthz`、`/readyz`、`/api/v1/meta/*`               | 版本、build、能力、配置状态                               |
| 首次配置     | `/api/v1/setup`                                       | CPA 地址、Management Key、collector 初始化                |
| Manager 配置 | `/api/v1/config`、`/api/v1/account-processing-policy` | collector、巡检计划、自动化、CPA usage 设置               |
| 仪表盘       | `/api/v1/dashboard/summary`                           | 状态、请求、成本、健康、最近失败                          |
| Usage        | `/api/v1/usage/*`                                     | 查询、分析、事件分页、JSONL import/export、分块 session   |
| Monitoring   | `/api/v1/monitoring/*`                                | analytics、header snapshots、实时表、筛选                 |
| 模型价格     | `/api/v1/model-prices/*`                              | CRUD、LiteLLM/OpenRouter sync、usage summary              |
| 巡检/自动化  | `/api/v1/inspections/*`、`/api/v1/account-actions/*`  | 本地/服务端巡检、执行/停止、候选处理、冷却                |
| CPA 管理代理 | `/v0/management/*`（兼容）                            | Provider、config、API Key、Auth Files、OAuth、quota、logs |
| 插件         | `/api/v1/plugins/*` + compatibility resource proxy    | 列表、商店、安装、启停、资源页                            |
| Playground   | `/api/v1/playground/*`                                | model list、chat、image request，继续由 Manager 控制代理  |
| 系统/日志    | `/api/v1/system/*`、`/api/v1/logs/*`                  | 状态、版本、文件日志、错误日志、更新检查                  |

迁移前必须从当前前端 service 和 Go router 生成一份完整 endpoint inventory；OpenAPI 覆盖率达到 100% 后才能切换 generated client。

### 6.3 统一响应和错误

成功响应保留业务字段；错误统一为：

```json
{
  "error": {
    "code": "usage_import_session_conflict",
    "message": "The upload offset does not match the current session.",
    "details": {},
    "requestId": "01J..."
  }
}
```

规则：

- `code` 稳定、可本地化；`message` 可读但不作为前端逻辑判断依据。
- 不返回原始 provider failure body、token、Authorization、Cookie、DSN 或未脱敏配置。
- 每个响应带 `X-Request-ID`；前端错误详情可复制 request ID。
- 列表使用 cursor pagination；排序字段和 filter schema 写入 OpenAPI。
- 配置更新使用 `ETag`/`If-Match`，冲突返回 `409` 和可比较的新版本。
- 可安全重试的创建/导入操作支持 `Idempotency-Key`。

### 6.4 请求策略

- GET 网络错误或 `502/503` 最多自动重试一次，并使用短抖动退避。
- POST/PUT/PATCH/DELETE 默认不自动重试；只有带幂等键且契约声明安全时允许。
- 搜索和筛选取消旧请求，输入 debounce 150–250ms。
- `401` 触发一次 session recheck，再进入登录；不得出现多个并发登录弹窗。
- `403` 显示权限/能力原因；`409` 显示冲突解决；`429` 遵循 `Retry-After`。
- 文件下载必须暴露 `Content-Disposition`、`Content-Length`；分块上传允许 `Content-Range`、`Range` 和幂等 header。

## 7. 跨域认证与安全

### 7.1 推荐域名模型

推荐使用同一 registrable domain 的两个子域：

- UI：`https://console.example.com`
- API：`https://api.example.com`

它们是不同 origin，但属于同一 site，能使用 `SameSite=Lax` 并避免依赖 third-party cookie。不要把生产 UI 放在与 API 完全无关的公共域名上。

### 7.2 Session cookie

完整模式使用：

```text
Name: __Host-cpamp_session
Domain: 不设置
Path: /
HttpOnly: true
Secure: true
SameSite: Lax
```

- token 继续由 Redis session 保存和验证；管理员来源继续读取 PostgreSQL auth view。
- 登录后轮换 session；logout 删除 Redis session 并清 cookie。
- 浏览器内不保存 session token；前端请求统一 `credentials: include`。
- Bearer session 仅保留给受控的非浏览器客户端，不成为 SPA 的默认认证方式。

### 7.3 CSRF 和 Origin

当前 cookie 跨 origin 后，所有状态修改请求必须同时满足：

1. `Origin` 必须精确匹配允许的 console origin。
2. 带 `X-CSRF-Token`，token 与 Redis session 绑定并可轮换。
3. cookie session 有效。

`GET/HEAD/OPTIONS` 不改变状态。前端从 session/csrf endpoint 取得 token并只保存在内存。退出、过期或账号变化后清除。

### 7.4 CORS

- 生产环境禁止 `*` 或“反射任意 Origin”。
- `Access-Control-Allow-Origin` 只返回精确 console origin。
- `Access-Control-Allow-Credentials: true`，并设置 `Vary: Origin`。
- Allow headers 至少包含：`Content-Type`、`Authorization`、`X-CSRF-Token`、`X-Request-ID`、`Idempotency-Key`、`Content-Range`、`If-Match`。
- Expose headers 至少包含：版本/build 元数据、`X-Request-ID`、`ETag`、`Retry-After`、`Content-Disposition`、`Content-Length`、`Content-Range`。
- preflight 可设置短期缓存，但不能绕过 origin 校验。

### 7.5 OAuth

跨域后 OAuth callback 固定落在 API 域：

1. 前端请求创建 OAuth transaction。
2. API 将 `state`、PKCE/一次性上下文放入 Redis，返回 provider authorization URL。
3. provider 回调 `api.<domain>/api/v1/oauth/callback/...`。
4. API 完成凭证处理后 302 到 console 的固定 allowlist 路径，只携带不透明 transaction ID，不携带 token/错误原文。
5. console 用 transaction ID 查询结果并清理 URL。

需要逐个验证 Codex、Claude、Gemini/Vertex、xAI、Antigravity/Kimi 和插件 OAuth 回调，不允许用一个 provider 的成功代替全量验收。

### 7.6 插件页面

插件资源是完全分离中最容易遗漏的兼容点。当前 iframe 可能依赖与主页面同源的 DOM/style bridge；改到 API 域后不能再直接操作 iframe DOM。

目标协议：

- 插件 iframe 使用独立 plugin runtime origin 和严格 `sandbox`。
- 主题、locale、尺寸、导航和受限操作通过版本化 `postMessage` 协议传递。
- 插件不获得完整管理员 session；需要后台能力时经 parent RPC 或短期、限权 capability token 调用。
- `frame-src`、`connect-src` 和消息 origin 使用 manifest allowlist。
- 插件安装页展示来源、版本、所需能力和信任状态。

为了不丢功能，迁移期保留兼容 resource proxy；全部已启用插件通过新 bridge 验证后，才移除旧的同源 style bridge。

### 7.7 安全响应头

前端站点至少设置：

- CSP：`default-src 'self'`，精确设置 `connect-src`、`frame-src`、`img-src`；禁止 `unsafe-eval`。
- `frame-ancestors 'none'`（插件 iframe 本身使用单独策略）。
- `object-src 'none'`、`base-uri 'self'`、`form-action 'self' <api-origin>`。
- HSTS、`X-Content-Type-Options: nosniff`、严格 Referrer-Policy、Permissions-Policy。

## 8. Go 后端目标设计

### 8.1 保留内容

- Go `net/http` 或现有 router 可继续使用，不为分离而强制更换框架。
- collector、automation、quota cooldown、inspection、CPA proxy 和现有 repository/service 逻辑保留。
- `cpamp-migration persist/rollup` 和 `cpamp-core-migration` 保留。
- PostgreSQL-only 启动校验、Redis Stream/group 校验和 fail-closed 行为保留。

### 8.2 删除内容（最终阶段）

- panel controller/service。
- `/management.html`、`/management-assets/*` 和 `/` 到页面的 redirect。
- `PANEL_PATH` 配置。
- `internal/httpapi/web` 和 `go:embed` 面板资源。
- Dockerfile 的 Node build stage 和前端 COPY。
- native release 中复制 `management.html`/assets 的逻辑。

### 8.3 API 层职责

- 统一 auth/CORS/CSRF/request ID/security headers/recovery/logging middleware。
- controller 只负责 HTTP binding；service 保持业务规则；repository 负责 Redis/PG/CPA adapter。
- OpenAPI request/response schema 与 handler contract test 同步。
- readiness 检查关键依赖但不得泄露 DSN；liveness 只判断进程是否可服务。
- 管理 API继续脱敏日志和失败证据。

### 8.4 推荐环境变量

保留现有 PG/Redis 变量，新增/收紧：

```dotenv
HTTP_ADDR=127.0.0.1:18317
CPAMP_CONSOLE_ORIGINS=https://console.example.com
CPAMP_PUBLIC_API_ORIGIN=https://api.example.com
CPAMP_PUBLIC_CONSOLE_ORIGIN=https://console.example.com
CPAMP_COOKIE_SECURE=true
CPAMP_CSRF_ENABLED=true
CPAMP_API_COMPAT_MIN_CONSOLE_VERSION=<version>
```

删除目标：`PANEL_PATH`。生产 CORS 不再使用 `USAGE_CORS_ORIGINS=*`。

## 9. React 前端目标设计

### 9.1 技术栈

- 保留 React 19、TypeScript、Vite、React Router、i18next、ECharts、CodeMirror。
- 保留 Zustand 管理纯客户端状态：主题、语言、导航、短期草稿和 UI preference。
- 推荐引入 TanStack Query 管理 server state：缓存、取消、重试、失效、并发去重。
- 从 OpenAPI 生成 TypeScript types/client；禁止页面直接维护重复 DTO。
- route-level lazy loading 保留，重型图表/编辑器继续按需加载。
- API client 只有一个 origin 和统一 middleware；完整模式不再在多个 axios client 中混合 cookie/key 认证。

### 9.2 推荐前端分层

```text
src/
├── app/             # bootstrap, runtime config, providers, router, error boundary
├── routes/          # route composition and page-level loaders
├── features/        # business workflows
├── entities/        # typed domain models and presentation adapters
├── shared/
│   ├── api/         # generated client wrapper only
│   ├── ui/          # design-system components
│   ├── i18n/
│   ├── lib/
│   └── styles/
└── testing/         # fixtures, MSW, a11y helpers
```

页面不能直接调用 axios/fetch；只能调用 feature query/mutation。feature 不能依赖另一个 feature 的内部实现。

### 9.3 启动和失败模型

启动顺序：

1. 加载/校验 runtime config。
2. 加载 locale/theme，不等待业务 API。
3. 请求 session + capabilities。
4. 根据状态进入 login、setup、upgrade-required、authenticated 或 API-offline 页面。
5. authenticated shell 按 route 懒加载。

API 不可用时 SPA 仍能加载，并显示最后成功时间、重试和诊断 request ID。不得把“API 不可用”显示为“没有数据”。

### 9.4 兼容模式隔离

现有 `manager_embedded` 应重命名为语义明确的 `full_session`；`external_panel` 作为独立 lightweight build 保留。两种模式不在同一登录页自动探测并互相回退，避免地址误判和把 Management Key写入完整模式浏览器存储。

## 10. UI/UX 设计

详细 token、组件、无障碍和性能规则见：

- `design-system/cpa-manager-plus/MASTER.md`
- `design-system/cpa-manager-plus/pages/dashboard.md`
- `design-system/cpa-manager-plus/pages/monitoring.md`
- `design-system/cpa-manager-plus/pages/usage-analytics.md`
- `design-system/cpa-manager-plus/pages/configuration.md`
- `design-system/cpa-manager-plus/pages/credentials-automation.md`
- `design-system/cpa-manager-plus/pages/login.md`

### 10.1 视觉方向

- 使用 Data-Dense Dashboard、Drill-Down Analytics、Real-Time Monitoring。
- 浅/深主题同等支持；主色为可信蓝，状态色只表达状态。
- 中文 UI 以 Noto/system CJK Sans 为主；Fira Code 仅用于 ID、时间、Token、价格等数据。
- 4px 基础网格；普通卡片主要靠边框分层，不使用大面积玻璃、渐变和漂浮阴影。
- 交互动画 120–240ms，只用来表达状态关系；尊重 reduced-motion。
- Phosphor regular outline 作为统一图标语言；导航图标必须同时有文字标签。

### 10.2 信息架构

将当前偏平的长导航重组为五组，旧 URL 保持可用：

| 分组         | 页面/能力                                     |
| ------------ | --------------------------------------------- |
| 总览         | 仪表盘                                        |
| 观察与分析   | 用量分析、请求监控、模型价格                  |
| 网关配置     | 配置面板、AI 提供商、插件管理、动态插件页     |
| 凭证与自动化 | 认证文件、OAuth、配额、凭证巡检、账号处理队列 |
| 工具与系统   | Playground、日志、系统信息                    |

桌面使用可折叠分组侧栏；移动端使用顶部栏 + 分组抽屉。目的地超过五个，不使用拥挤的底部导航。

### 10.3 桌面框架线框

```text
┌──────────────────┬────────────────────────────────────────────────────┐
│ Brand / Env      │ Page title · breadcrumb       Search  Theme  User │
├──────────────────┼────────────────────────────────────────────────────┤
│ 总览             │ Context/status bar                                  │
│  仪表盘          ├────────────────────────────────────────────────────┤
│ 观察与分析       │ Page primary content                                │
│  用量 / 监控     │ - sticky query/filter bar                           │
│ 网关配置         │ - KPI or task summary                               │
│  配置 / Provider │ - table/chart/editor                                │
│ 凭证与自动化     │ - detail drawer when selected                       │
│ 工具与系统       │                                                      │
└──────────────────┴────────────────────────────────────────────────────┘
```

侧栏 240px，可收至 64px；顶部栏 56px。页面最大内容宽度 1600px，数据页允许全宽，表单页限制可读宽度。

### 10.4 移动端框架

```text
┌────────────────────────────┐
│ Menu  Page title   Actions │
├────────────────────────────┤
│ Status / stale / refresh   │
├────────────────────────────┤
│ Primary KPI (2 columns)    │
├────────────────────────────┤
│ Priority content           │
│ table row summaries        │
│ -> tap opens detail sheet  │
└────────────────────────────┘
```

页面本身禁止横向滚动。宽表在移动端转为优先列摘要 + detail sheet；高级筛选进入抽屉，并始终显示活动筛选数量。

### 10.5 页面级改造摘要

#### 仪表盘

- 首屏先回答服务是否健康，再展示流量和成本。
- 主 KPI 限为请求、成功率、成本、P95 延迟；Token 细分下沉。
- 活跃告警按严重度排序并链接到已过滤的目标页。
- 版本卡移到系统页，仅有可操作升级时回到仪表盘提示。

#### 用量分析

- 总览、趋势、模型、客户端 Key、凭证和热力图保持深链 tab。
- 全 tab 共用 URL 驱动的 sticky filter bar。
- 趋势使用 line、排名使用 sorted horizontal bar、小时/天使用 heatmap。
- 所有图表有表格/CSV替代，轴明确标注 request、token、currency 和时间粒度。

#### 请求监控

- 事件表是主角，过滤/KPI不能把表格推到两屏以下。
- 筛选 token、保存上下文、显示 refresh cadence/last success/pause。
- 行详情进入右侧 drawer，集中展示 trace、token、latency、cost、脱敏失败证据。
- 100+ 行虚拟化，继续使用 PostgreSQL cursor 分页。

#### 配置与 Provider

- master-detail，复杂配置按 Basics/Auth/Models/Advanced 分段。
- raw YAML 与 visual editor 共用一个 draft，保存前展示 diff。
- 显示 unchanged/dirty/validating/saving/saved/conflict。
- secret 字段区分“保持不变”和“替换”，不能回显真实 secret。

#### 认证、OAuth、配额、巡检

- disabled、cooldown、expired、reauth、manual-disabled 明确区分。
- quota 同时显示文本、进度、reset，unknown 不映射成 0 或健康。
- 巡检使用 config → progress → result → reviewed action 流程，历史 run 可追溯。
- “浏览器本地巡检”的调度和结果整理可以继续在浏览器执行，但所有网络探测必须通过 Go API 的受控 probe/api-call 接口；完整模式不得从浏览器直连 Provider 或携带上游凭证。
- bulk action 先显示 selected/eligible/skipped，再确认执行。

#### 插件、日志、Playground、系统

- 插件页显示来源、版本、权限和信任状态；资源页使用 sandbox + message bridge。
- 日志虚拟化，支持 pause、filter、copy/export，保留文件日志可用性 gate。
- Playground 保留 chat/image/model list，request/response 分栏，敏感字段始终脱敏。
- 系统页汇总 API、CPA、collector、Redis/PG 可用性和 build；不暴露 DSN/密码。

## 11. 部署设计

### 11.1 Frontend host

```nginx
server {
    listen 443 ssl http2;
    server_name console.example.com;
    root /usr/share/nginx/html;

    location = /runtime-config.json {
        add_header Cache-Control "no-store";
        try_files $uri =404;
    }

    location /assets/ {
        add_header Cache-Control "public, max-age=31536000, immutable";
        try_files $uri =404;
    }

    location / {
        add_header Cache-Control "no-cache";
        try_files $uri $uri/ /index.html;
    }
}
```

该 host 不反向代理普通业务 API；SPA 直接访问独立 API origin，以便真实验证 CORS 和独立故障域。

### 11.2 API host

```nginx
server {
    listen 443 ssl http2;
    server_name api.example.com;

    location / {
        proxy_pass http://127.0.0.1:18317;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 3600s;
    }
}
```

API host 不提供 SPA、前端 assets 或 fallback 页面。Redis、PG、CPA 端口不暴露公网。

### 11.3 CI/CD

前端流水线只由 frontend/design-system/contract 相关变更触发：

1. npm clean install。
2. type-check、lint、unit、a11y component tests。
3. OpenAPI client generation drift check。
4. production build、bundle budget、CSP/static header test。
5. Playwright smoke + visual regression。
6. 发布不可变 web image/bundle。

后端流水线只由 backend/contract 相关变更触发：

1. `go test ./...`、race、vet、security/static checks。
2. OpenAPI handler/response contract tests。
3. Redis/PG integration tests。
4. 构建纯 Go API/migration binaries。
5. 发布不可变 API image/native package。

部署按 web/API 各自版本记录组合，例如：

```text
production = web 2.0.3 + api 2.1.1 + contract v1.7
```

## 12. 功能保留矩阵

任何一项没有明确自动化验收，都视为尚未完成，而不是“应该还在”。

| 模块            | 当前能力                                           | 分离后归宿                                | 必测场景                                    |
| --------------- | -------------------------------------------------- | ----------------------------------------- | ------------------------------------------- |
| 登录/session    | email/password、remember、logout、rate limit       | API cookie + Redis session + CSRF         | 登录、刷新、过期、退出、限流、API 故障      |
| 首次 setup      | CPA 地址/Management Key/collector                  | `/api/v1/setup`                           | 未配置、无效 key、env lock、成功初始化      |
| Dashboard       | 状态、KPI、流量、健康、失败、collector             | React dashboard + Go summary API          | 空/有数据、stale、部分依赖故障              |
| Usage Analytics | 多维筛选、成本/token/延迟、热力图                  | React analytics + PG API                  | 所有 tab、时区、filter、导出                |
| Monitoring      | 账号/API Key/事件、自动刷新、失败证据              | React table/drawer + PG/cache API         | cursor、pause、filter、失败脱敏             |
| Import/export   | JSONL、小/大文件、分块 session、取消/恢复          | cross-origin file API                     | Content-Range、断点、冲突、取消、下载文件名 |
| Model prices    | CRUD、sync、匹配候选、usage summary                | API + React editor                        | 两个源、代理、覆盖、冲突                    |
| Config          | raw/visual、CPA config、Manager config、automation | API + draft/diff UI                       | 读写、验证、ETag 冲突、env lock             |
| Providers       | Gemini/Codex/Claude/Vertex/xAI/OpenAI-compatible   | CPA proxy + React editors                 | 每种 Provider CRUD、模型、key test          |
| Auth files      | 列表、上传/下载、字段、禁用、别名、粘贴            | CPA proxy + React credential UI           | 大文件、脱敏、model alias、prefix proxy     |
| OAuth           | 多 Provider 登录和 callback                        | API transaction/callback + console status | 每种 Provider、取消、超时、重复 callback    |
| Quota           | Provider quota、reset、cooldown                    | API + credential/quota UI                 | unknown、多个窗口、冷却恢复                 |
| Inspection      | 浏览器本地、服务端 run、停止、结果、日志、action   | React workflow + Go inspection API        | 两种模式、重登/禁用/删除、历史恢复          |
| Account actions | pending、ignore、resolve、delete、auto-disable     | Go API + review UI                        | eligibility、并发、回滚失败                 |
| Plugins         | 列表/store/install/config/enable/resource pages    | API + sandbox plugin runtime              | 官方/第三方、启停、页面、主题、升级         |
| Playground      | models/chat/images                                 | Go controlled proxy + React split view    | 流式/非流式、错误、脱敏                     |
| Logs            | 文件日志、错误日志、过滤/刷新                      | API + virtualized viewer                  | gate、暂停、下载、超大日志                  |
| System          | status/version/update/theme/language               | API meta + React system UI                | build headers、更新失败、四语言             |
| Demo            | 全静态虚构数据                                     | 独立 demo build                           | 不产生真实网络调用                          |
| Lightweight     | CPA Management Key 直连                            | 独立 lightweight build                    | 不依赖 Manager API，不影响 full 模式        |
| Deep links      | 所有当前 route/redirect                            | 保持 route 或明确 301/client redirect     | 刷新、前进后退、旧书签                      |

## 13. 零停机迁移计划

### Phase 0：冻结事实和基线

- 冻结当前页面/route/API/feature inventory。
- 为上表所有能力补 characterization tests 和关键 Playwright E2E。
- 记录当前前后端性能、错误率和 bundle/API p95 基线。
- 不修改 Redis、PG 或 collector。

退出条件：功能矩阵有负责人、测试或明确测试缺口；OpenAPI 覆盖现有前端实际调用。

### Phase 1：后端跨域就绪，旧 UI 仍运行

- 新增 exact-origin CORS、credentials、CSRF、request ID 和 expose headers。
- 新增 capabilities/runtime compatibility endpoint。
- OAuth callback 增加 console return allowlist。
- 插件和文件 API完成跨域测试。
- 旧 `/management.html` 仍同源工作，作为实时回滚入口。

退出条件：旧 UI 无回归；独立 test console 可完成登录和核心读写。

### Phase 2：前端物理拆分和 UI 基础层

- 独立 package/lockfile/Dockerfile/runtime config。
- 建立 semantic tokens、shared UI、统一 API client、server-state 层。
- 按页面逐个迁移，不做一次性全量 rewrite；旧/new page 使用同一 API contract。
- 每迁一页补视觉回归、a11y 和功能 E2E。

退出条件：功能矩阵在独立 console 全部通过，且 full 模式不再需要 Management Key进入浏览器。

### Phase 3：并行运行和 canary

- 部署 `console.<domain>`，后端仍保留 embedded panel。
- 内部用户和小比例入口使用新 console；比较请求成功率、API latency、前端错误和业务结果。
- 至少覆盖 OAuth、插件、长文件导入、实时监控和移动端，而不仅是 Dashboard。
- Redis Stream/group、PG row/checkpoint 和 collector 单例持续按现有方式监控。

退出条件：连续观察窗口内无 P0/P1 功能缺失，无数据差异，无安全阻断。

### Phase 4：正式切换

- 主管理入口跳转到独立 console。
- 旧 `/management.html` 保留为受限 rollback URL，不公开导航。
- 前端和后端独立部署、独立版本记录、独立告警。
- 不重启 Redis/PG，不迁移 schema，不重置 consumer group。

回滚：只回切 DNS/入口或 web image；API、Redis、PG、collector、persist/rollup 均不回滚数据。

### Phase 5：完成分离

- 至少两个稳定发布/约定观察窗后，移除 Go panel route/embed/PANEL_PATH/Node build stage。
- 删除旧同源 CORS 特例和前端自动 origin 猜测。
- 将 compatibility endpoints 标记弃用并按版本计划移除。
- 更新安装、备份、升级和反向代理文档。

退出条件：SEP-01 至 SEP-10 全部通过。

## 14. 测试与发布门禁

### 14.1 功能与契约

- Go handler contract tests：状态码、schema、error code、headers。
- 前端 MSW/unit tests：所有 loading/empty/error/partial/stale 状态。
- Playwright：完整功能矩阵的 critical path。
- OpenAPI breaking-change check：删除/收紧字段、状态码和 enum 必须显式版本化。
- API/current-and-previous-console compatibility matrix。

### 14.2 安全

- 非 allowlist Origin、缺 CSRF、错误 CSRF、无 cookie 的修改请求全部拒绝。
- cookie flags、session rotation、logout revocation、rate limit 自动测试。
- OAuth state/PKCE/replay/return URL allowlist 测试。
- 插件 message origin、sandbox 和 capability 测试。
- CSP、XSS sink、secret-in-DOM/URL/localStorage/log 扫描。
- 导出/日志/失败证据脱敏回归。

### 14.3 UI/可访问性

- axe 自动检查 + 关键流程人工键盘/屏幕阅读器检查。
- 375/768/1024/1440、mobile landscape、200% zoom。
- light/dark/system、四种现有语言、长文案和大数字。
- reduced-motion、focus order、modal/drawer focus return。
- 图表 summary/table、颜色+线型、键盘 tooltip。

### 14.4 性能

- 首屏 shell ≤250KB gzip（lazy ECharts/editor 不计入 shell）。
- route chunk 目标 ≤200KB gzip；超限需说明。
- LCP <2.5s、CLS <0.1、INP <200ms（约定生产 profile 的 p75）。
- 100+ 行虚拟化；1,000+ chart points downsample/Canvas。
- API latency 不因分离增加业务查询；跨域 preflight 使用安全短缓存。

### 14.5 数据链路

- collector 仍是唯一 CPA usage consumer。
- Redis pending 在可解释范围，persist checkpoint 持续推进。
- PostgreSQL raw/distinct event_hash/identity ledger 保持一致。
- hourly/daily checkpoint 持续推进。
- 主 Go 进程不打开 SQLite，Redis/PG失败不静默 fallback。

## 15. 主要风险与对策

| 风险                            | 影响                            | 对策                                                                    |
| ------------------------------- | ------------------------------- | ----------------------------------------------------------------------- |
| CORS wildcard + credential      | 任意站点可借用户 session 调 API | exact allowlist + CSRF + Origin validation                              |
| Cookie 在不同站点被浏览器阻止   | 登录循环                        | console/API 使用同一 registrable domain；避免 third-party cookie        |
| OAuth redirect 仍指旧页面       | 登录失败或 token 暴露           | API callback + 固定 console allowlist + opaque transaction ID           |
| 插件 iframe 失去同源 DOM 权限   | 插件页面主题/交互失效           | versioned postMessage bridge + compatibility proxy +逐插件验收          |
| 大文件跨域 header 不完整        | 导入/导出失败                   | allow/expose Range、Content-Range、Content-Disposition，并做断点 E2E    |
| 一次性重写 UI                   | 隐性功能丢失                    | route-by-route strangler migration + characterization tests             |
| API 与 web 独立升级不兼容       | 页面运行时报错                  | capabilities + version window + contract CI + upgrade-required state    |
| 完整模式仍保留直连 CPA fallback | Management Key泄露到浏览器      | full/lightweight 分开构建，full 模式禁止 fallback                       |
| 多个 API 实例带出多个 collector | usage 被 destructive 多消费     | 本阶段保持单 Manager 实例；未来拆 worker 前先建立 leader/singleton 机制 |
| UI 切换误触数据迁移             | 产生不可逆数据差异              | UI 分离不做 PG/Redis schema 变更，回滚只切 web/入口                     |

## 16. 推荐实施工作包

1. **Contract baseline**：endpoint inventory、OpenAPI、error/header contract、capabilities。
2. **Cross-origin security**：cookie、CORS、CSRF、Origin、OAuth、file headers。
3. **Independent builds**：web runtime config/image，Go-only image/native package，独立 CI。
4. **Frontend foundation**：design tokens、shell、generated client、query layer、a11y primitives。
5. **Read-only pages**：Dashboard、Usage Analytics、Monitoring、System。
6. **Mutation pages**：Config、Providers、Auth Files、Quota、Model Prices。
7. **Complex flows**：OAuth、inspection/account actions、import/export、plugins、Playground/logs。
8. **Canary/cutover**：并行入口、全矩阵 E2E、指标比较、正式切换。
9. **De-embed cleanup**：移除 Go panel、PANEL_PATH、Node build stage 和 compatibility glue。

每个工作包都应是可独立合并、可独立回滚的增量；禁止以“全部完成后才能测试”为前提。

## 17. Definition of Done

完成标准不是“新页面已经能打开”，而是：

- SEP-01 至 SEP-10 全部满足。
- 功能保留矩阵全部自动化或有经签字的人工验收记录。
- Redis/PostgreSQL 数据链路和线上 checkpoint 无变化。
- 完整模式浏览器中没有 CPA Management Key/session token 的持久化副本。
- 前端、API 可独立升级和回滚，组合版本可追踪。
- Go 后端不再包含任何应用 UI 静态资源。
- UI 通过设计系统、WCAG 2.2 AA、响应式和性能门禁。
- 文档、安装脚本、Nginx/systemd/Compose、备份与回滚手册均切换到新架构。
