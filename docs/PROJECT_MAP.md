# Prism v2.0.0 项目地图

Prism 是 Go + React AI Gateway。公开 API 位于 `/v1`；Gateway V2 是内部架构名称。

## 技术栈

| 层 | 技术 |
|---|---|
| HTTP | Gin |
| 数据库 | MySQL + GORM / database/sql |
| 缓存 | Redis |
| 后台执行 | MySQL Outbox、租约与恢复 Worker |
| 控制台 | React 19 + TypeScript + Vite + Tailwind CSS |
| 认证 | JWT、API Token、执行级回调令牌 |
| 安全 | AES-256-GCM、HMAC-SHA256、SSRF 主机校验 |
| 可观测性 | Zap、Prometheus、Call/Attempt、访问日志与审计事件 |

## 目录

```text
Prism/
├── cmd/server/                    # 服务、迁移命令、SQL Worker 生命周期
├── configs/                       # 配置模板
├── console/                       # React 控制台与嵌入式 dist
├── database/migrations/           # 唯一数据库结构来源
├── internal/
│   ├── api/                       # Console、Admin、V1 与内部回调路由
│   ├── gateway/
│   │   ├── adapter/               # 图片、视频等协议适配器
│   │   ├── billing/               # 定点计费与结算
│   │   ├── canonical/             # 协议中立请求、响应与事件
│   │   ├── catalog/               # 目录领域规则
│   │   ├── catalogsource/         # 目录来源发现 Worker
│   │   ├── codec/                 # 下游协议编解码
│   │   ├── credentials/           # 凭据与用途授权
│   │   ├── delivery/              # 结果交付策略
│   │   ├── engine/                # 同步执行、重试、计费与调用事实
│   │   ├── execution/             # 执行状态机
│   │   ├── handler/               # Chat、Messages、Responses、Files
│   │   ├── repository/            # gw_* 存储与事务边界
│   │   ├── responses/             # Responses 资源及后台执行
│   │   ├── routing/               # 目录选路、凭据与并发名额
│   │   ├── runtime/               # Outbox、租约、回调、恢复与清理 Worker
│   │   ├── security/              # 密钥、HMAC 与加密 Blob
│   │   └── transport/             # 上游对话协议 Transport
│   ├── migrate/                   # 迁移、导入、审计与清理命令
│   ├── model/                     # 用户等非统一外围模型与兼容模型
│   └── video/                     # 视频请求协议和统一媒体资产服务
└── pkg/                           # 配置、数据库、缓存、日志、指标等
```

## HTTP 入口

| 文件 | 职责 |
|---|---|
| `internal/api/router.go` | 全局中间件、认证分组、健康检查、静态控制台 |
| `internal/gateway/gateway.go` | Chat、Messages、Responses、Files |
| `internal/api/open/routes.go` | Images、Videos、Models |
| `internal/api/console/routes.go` | 登录用户控制台与 Playground |
| `internal/api/admin/routes.go` | 统一网关管理与用户管理 |
| `internal/api/callback` | 统一上游回调入口 |

公开路由不包含 Channels、Capabilities、通用 Tasks 或视频取消。

## 统一目录

```text
Active Catalog
  -> Product
  -> SKU
  -> Operation Contract
  -> Product Transport
  -> Route
  -> Offering
  -> Credential Pool / Credential
```

活动目录目前仍由状态为 `published` 的 `Catalog Release` 兼容行承载，但管理界面不再提供发布版生命周期操作。每次请求开始时固定 SKU、Operation、Transport、Offering、成本方案和凭据配置。模型公开名与上游模型名分别存储，不靠渠道类型猜测请求端点。

## 执行路径

同步对话与图片：

```text
Handler -> Codec/Adapter -> Engine -> Route -> Attempt -> Upstream -> Settle
```

后台 Responses 与视频：

```text
Handler
  -> 事务创建 Call + Attempt + AsyncExecution + Outbox + Reservation
  -> SQL Worker 租约认领
  -> Submit / Query / Recover
  -> 事务写入终态 + Result Delivery + Settlement
```

上游回调入口只写入验真证据，回调 Worker 再推进异步状态。客户端 `callback_url` 加密绑定 Call，终态事务产生不可变事件及内部 HMAC 完整性证据；投递 Worker 发送稳定事件 ID、内容 SHA256，并执行 SSRF 防护、超时和有限重试。

## gw_* 事实分类

| 分类 | 代表表 |
|---|---|
| 目录 | `gw_catalog_releases`、`gw_products`、`gw_skus`、`gw_operation_contracts`、`gw_offerings`、`gw_routes` |
| 凭据 | `gateway_channels`、`gw_credential_pools`、`gw_credentials`、`gw_credential_slots` |
| 执行 | `gw_api_calls`、`gw_api_call_attempts`、`gw_channel_request_logs`、`gw_async_executions`、`gw_async_outbox` |
| 资源 | `gw_api_resources`、`gw_ai_responses`、`gw_capability_tasks`、`gw_video_tasks`、`gw_file_resources` |
| 正文 | `gw_api_call_payloads` 及加密 Blob |
| 计费 | `gw_sell_rates`、`gw_cost_plans`、`gw_cost_rates`、预授权与结算事件、`gw_upstream_cost_*` |
| 交付 | `gw_media_assets`、`gw_result_deliveries`、`gw_callback_deliveries` |
| 控制面 | 目录发现、证据审核、运行状态和状态事件；就绪检查由进程内部完成 |

旧 `api_calls`、`tasks`、`ai_responses` 不是新调用的执行事实源。迁移完成前可保留为迁移源或兼容数据；删除必须经过深度审计。

## 运行门禁

服务先检查数据库迁移、活动目录、Payload 密钥及目录商业数据。仅当启用中或停止分配中的凭据仍依赖旧密文时，才检查旧凭据 KEK/HMAC。目录未配置时控制面仍可启动，统一数据面不接收调用；进程会持续复查运行条件。

服务启动的后台组件包括目录发现、异步 Outbox、Responses Attempt、上游回调、客户端回调、交付到期/恢复、能力恢复、敏感正文清理及媒体清理 Worker。它们都直接读取 SQL，不使用 Redis 任务投递。

## 迁移入口

`cmd/server/main.go` 提供：

```text
migrate up
migrate status
migrate adopt
migrate audit
migrate audit-deep
migrate import-legacy
migrate import-runtime
migrate import-video-assets
migrate import-ai-files
migrate verify-crypto
migrate cleanup-legacy
```

`database/migrations/` 是唯一生产结构来源，已应用迁移不可修改。
