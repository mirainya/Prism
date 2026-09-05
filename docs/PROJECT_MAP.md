# Prism 项目地图

Prism 是 Go + React 实现的 AI Gateway。公开接口位于 `/v1`；Gateway V2 是内部执行架构，不是 HTTP 版本号。

> 本文描述当前已实现结构。统一目录、凭据、异步执行与计费的目标架构以 `docs/specs/2026-09-04-unified-gateway-catalog-billing-architecture.md` 为准；两者冲突时不得把本文当作目标设计。

## 技术栈

| 层 | 技术 |
|---|---|
| HTTP | Gin |
| 数据库 | MySQL + GORM |
| 缓存与队列 | Redis + Asynq |
| 控制台 | React 19 + TypeScript + Vite + Tailwind CSS |
| 认证 | 控制台 JWT、V1 API Token、回调 HMAC |
| 可观测性 | Zap、Prometheus、调用账本、访问日志、审计事件 |

## 目录

```text
Prism/
├── cmd/server/                  # 依赖初始化、HTTP/Worker/Scheduler 生命周期
├── configs/                    # YAML 配置模板
├── console/                    # React 控制台与嵌入式 dist
├── database/migrations/        # 有序 SQL 迁移和历史回填
├── internal/
│   ├── api/                    # Console、Admin、V1、Callback 路由与中间件
│   ├── gateway/                # 对话协议统一执行架构
│   ├── model/                  # GORM 模型
│   ├── provider/               # 图片/视频等能力渠道 Provider
│   ├── service/                # 计费、任务、账本、管理业务
│   └── worker/                 # Asynq 后台执行与恢复
├── pkg/                        # 配置、认证、缓存、数据库、日志、队列等组件
├── README.md
└── API_REFERENCE.md
```

## HTTP 层

| 位置 | 职责 |
|---|---|
| `internal/api/router.go` | 全局中间件、路由组、静态控制台 |
| `internal/api/middleware/auth.go` | V1 API Token 鉴权与缓存 |
| `internal/api/middleware/jwt_auth.go` | JWT、用户状态、`SessionVersion` 校验 |
| `internal/api/middleware/request_id.go` | 生成并传播 `X-Request-ID` |
| `internal/api/middleware/logger.go` | 进程结构化日志，AI 正文不进入普通日志 |
| `internal/api/middleware/access_log.go` | 持久 API 访问日志与写操作审计 |
| `internal/api/console` | 用户控制台、Playground、Call 与观测查询 |
| `internal/api/admin` | 用户、能力渠道和 Gateway V2 管理 |
| `internal/api/open` | 图片、视频等统一能力接口 |

## Gateway V2

```text
HTTP Handler
  -> Downstream Codec
  -> Canonical Request
  -> Engine
  -> Router
  -> Upstream Transport
  -> Upstream API
```

| 模块 | 职责 |
|---|---|
| `gateway/codec` | Chat、Responses、Anthropic Messages 编解码 |
| `gateway/canonical` | 协议中立消息、多模态、工具、推理、usage 和事件 |
| `gateway/engine` | 执行计划、重试、计费、Call/Attempt、交付和流终态 |
| `gateway/routing` | 能力、Transport、Key、并发、权重与熔断选路 |
| `gateway/transport` | OpenAI Chat/Responses、Anthropic、Google、Volcengine v3 |
| `gateway/responses` | Responses 存储、续话、幂等、后台执行、租约与恢复 |
| `gateway/handler` | `/v1/chat/completions`、`/v1/messages`、`/v1/responses`、Files |

Transport 先判断请求是 `exact`、`converted` 还是 `unsupported`。路由优先原生协议，只执行能完整表达当前请求的转换。

## 能力任务

图片、视频等异步能力使用另一条执行链：

```text
api/open -> service/capability -> Task -> worker submit/poll/upload/notify -> provider
```

每次 Submit/Poll 都会写入独立 `api_call_attempts`，最终成功端点决定实际结算价格。

Task 行是持久的异步提交意图。Submit、Poll、Upload 和 Notify 使用由 Task ID/轮询序号派生的确定性 Asynq Task ID；服务启动时恢复待提交任务，5 分钟一次的超时检查也会重新投递缺失或陈旧队列项。`callback_url` 只接受解析到公网地址的 HTTP/HTTPS URL。

## 核心数据

| 表 | 作用 |
|---|---|
| `gw_channels` / `gw_channel_keys` | 对话上游与 API Key |
| `gw_abilities` / `gw_ability_transports` | 公开模型映射与可用 Transport |
| `gw_route_states` | Key + 模型 + Transport 熔断状态 |
| `api_calls` | 所有协议与能力调用的事实源，统一记录状态、usage、计费和资源关联 |
| `api_call_attempts` | 一次真实上游执行 |
| `api_call_payloads` | 可选的下游/上游原始 HTTP 正文；脱敏、限长、可加密并按期删除 |
| `channel_request_logs` | 上游 HTTP 路径、状态、耗时和 usage 摘要 |
| `ai_responses` | Responses 资源与后台执行检查点 |
| `ai_response_idempotency_cache` | 24 小时短期幂等结果 |
| `tasks` | 仅图片、视频等异步能力任务资源，通过 `call_id` 关联 `api_calls` |
| `billing_logs` | 模型调用计费阶段记录 |
| `balance_entries` | 用户与 Token 的追加式余额流水，历史非零余额以 `opening_balance` 建立迁移基线 |
| `api_access_logs` | 不含正文和凭据的 API 访问元数据 |
| `audit_events` | 控制台和资源状态变更审计 |
| `conversations` | Playground 与开放对话 API 的会话容器 |
| `conversation_turns` | 每轮请求的顺序、`call_id`、状态、usage 和费用投影 |
| `conversation_items` | 每轮有序的 canonical 输入/输出，包含多模态、推理、工具调用与结果 |
| `messages` | 旧消息结构与独立分页 `/messages` 查询的兼容投影；新记录使用 `/turns` |

## 一致性与恢复

- 计费预授权、结算、退款和余额流水在数据库事务内完成。
- 前台长请求使用 Call 租约与心跳，恢复任务只处理过期租约。
- 后台 Responses 在上游成功后先保存结果检查点，再完成响应与计费状态。
- `Idempotency-Key` 并发请求等待首个执行者；哈希冲突返回 400。
- 密码、角色或状态变更递增 `SessionVersion`，旧 JWT 随即失效。
- Conversation Turn、canonical items、旧消息投影及 Call/请求日志关联在同一事务保存。
- Chat、Messages、Responses 的终态调用都会生成 canonical Conversation 投影；`store: false` 不关闭该投影。
- `api_calls` 是调用执行事实，`tasks`、`ai_responses` 和 Conversation Turn 只保存各自资源或展示投影。
- 异步 Task Submit 使用确定性队列 ID，并在服务启动和定时检查时从 SQL 状态恢复。

## 数据保留

`observability` 配置控制：

- `api_call_payloads` 中的下游与上游原始 HTTP 正文默认不保留；启用后默认最多 256 KiB、保留 168 小时，可使用 AES-256-GCM 独立密钥。
- Call/Attempt 与上游请求日志元数据默认保留 90 天。
- 终态异步任务和闲置 Conversation、轮次、项目及旧消息默认保留 90 天。
- API 访问日志默认保留 30 天，审计事件默认 180 天，计费和余额流水默认 365 天。
- canonical Conversation 是独立的结构化投影，不受原始正文捕获开关或 Responses `store: false` 影响。

小时级清理任务使用小批次物理删除，避免单次处理大量历史数据。

## 控制台

| 页面 | 作用 |
|---|---|
| Dashboard | 用量与错误统计 |
| Gateway Channels / Models | 对话上游、Key、Ability、Transport 和模型元数据 |
| Channels / Capabilities | 图片、视频等能力渠道配置 |
| Playground | Chat、Responses、Messages、多模态与能力任务测试 |
| Call Logs | 全调用事实、Attempt、usage、计费和正文详情，支持 `call_id` 定位 |
| Observability | API 访问日志、审计事件、余额流水 |
| Async Tasks | 图片、视频等异步任务，使用 `snapshot_at` 稳定分页并链接 Call |
| Chat Logs | Playground 与开放 API 对话、轮次、附件和关联 Call |
| API Docs | 在线协议文档与试用 |

## 启动与构建

```bash
cd console
npm ci
npm run build
cd ..
go build ./cmd/server
```

数据库结构只由 `database/migrations` 管理。全新安装运行 `prism migrate up`；基线前的旧库完成历史迁移后运行 `prism migrate adopt`；服务启动只检查版本，不执行 GORM AutoMigrate。存在待执行或失败中的迁移时，服务拒绝启动。
