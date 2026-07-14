<p align="center">
  <img src="console/assets/logo.svg" width="120" alt="Prism Logo" />
</p>

<h1 align="center">Prism</h1>

<p align="center">
  多协议转换、能力感知路由和可视化管理的 AI Gateway
</p>

Prism 使用 Go + React 构建，对外提供 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages 接口，并根据请求内容选择兼容的上游协议。前端构建产物嵌入 Go 二进制，可作为单个服务部署。

> Gateway V2 是内部执行架构名称；公开 API 仍位于 `/v1`，没有 `/v2` HTTP 接口。

## 架构

```text
客户端
  -> Gin Handler（鉴权、校验、协议错误格式）
  -> Downstream Codec（Chat / Responses / Messages）
  -> Canonical Request / Response / Event
  -> Engine（执行计划、选路、重试、计费、日志、流生命周期）
  -> Router（语义能力、Transport、并发、熔断、权重）
  -> Upstream Transport
  -> 上游 API
```

| 层 | 职责 |
|---|---|
| `gateway/codec` | 解码下游请求，并把响应或 SSE 编码为下游协议 |
| `gateway/canonical` | 表示消息、多模态内容、工具、推理、usage 和流事件 |
| `gateway/engine` | 生成执行计划，管理重试、计费预授权、请求日志和流终态 |
| `gateway/routing` | 从 `gw_*` 数据中选择能力与 Transport，管理并发和熔断 |
| `gateway/transport` | 处理上游 URL、鉴权、请求编码、HTTP/SSE 和响应解码 |
| `gateway/responses` | 管理 Responses 存储、续话、幂等、后台任务、取消和恢复 |

图片、视频等异步能力目前仍由 `api/open -> service -> worker` 执行，与对话 Gateway V2 并存。

## 协议支持

### 下游接口

| 接口 | 用途 |
|---|---|
| `POST /v1/chat/completions` | OpenAI Chat Completions，支持流式与非流式 |
| `POST /v1/responses` | OpenAI Responses，支持流式、存储、续话和后台执行 |
| `POST /v1/messages` | Anthropic Messages，支持流式与非流式 |
| `/v1/files` | 按 Token 隔离的文件上传、查询、下载和删除 |

### 上游 Transport

| Transport ID | 上游协议 |
|---|---|
| `openai_chat` | `POST /v1/chat/completions` |
| `openai_responses` | `POST /v1/responses` |
| `anthropic_messages` | `POST /v1/messages` |
| `google_generate_content` | Gemini `generateContent` / `streamGenerateContent` |
| `volcengine_responses_v3` | 火山方舟 `POST /api/v3/responses` |

每个 Transport 会针对当前 canonical 请求返回 `exact`、`converted` 或 `unsupported`。Engine 优先选择原生协议，仅在字段和能力可表达时转换；无法兼容的请求返回 `400 unsupported_model_capability`，临时没有可用路由时返回 `503 model_unavailable`。

Chat、Responses 和 Messages 并不与某个模型固定绑定。一个公开模型能使用哪些下游协议，由其语义能力、已启用 Transport 和当前请求共同决定。

火山方舟的 `thinking`、`caching`、`session`、`context_management` 等扩展只会交给原生 v3 Transport。Responses 转 Chat 时允许忽略 `include: ["reasoning.encrypted_content"]`，但不会伪造加密推理内容。

## 路由模型

| 数据表 | 作用 |
|---|---|
| `gw_channels` | 上游 Base URL、协议、附加请求头和渠道配置 |
| `gw_channel_keys` | API Key、权重、状态和最大并发 |
| `gw_abilities` | 公开模型到上游模型的映射、能力、优先级和价格 |
| `gw_ability_transports` | 为每条能力显式声明可用 Transport |
| `gw_route_states` | 按 Key、公开模型和 Transport 记录临时熔断状态 |
| `gw_model_meta` | 展示名、分组、排序、思考档和最大输出元数据，不参与选路 |

路由不会根据渠道类型猜测上游端点。原生 Transport 优先，其后比较 Ability 优先级，同档 Key 按权重选择。一次请求最多尝试 3 个可用路由，并在请求结束后释放并发占用。

## 主要能力

- 文本、图片、文件、音频、视频、函数工具、推理和结构化输出的 canonical 表达；实际支持范围由 Transport 与 Ability 能力共同校验。
- Responses 使用 Prism `resp_` ID，支持 `Idempotency-Key`、`previous_response_id`、`GET`、`DELETE`、取消和 `input_items`。
- 上游执行前预授权计费，终态 usage 到达后按实际用量结算；未指定输出上限时的 `4096` 仅用于预授权估算，不代表模型最大输出。
- 每次真实上游尝试写入请求日志，记录 Transport、路径、模型、状态、耗时和 usage，并对凭据及大型 data URL 脱敏。
- Playground 复用同一个 Gateway Engine，支持 Chat、Responses、Messages、多模态粘贴/拖放、能力调用、历史记录和调试信息。
- 图片与视频生成支持统一能力接口、同步/轮询/回调渠道及后台任务。
- `/health` 检查 MySQL 与 Redis，`/metrics` 暴露 Prometheus 指标。

普通 `/v1/chat/completions` 调用会记录请求日志，但不会自动创建 Conversation；对话记录由 Playground 保存。Responses 状态单独存储在 `ai_responses`。

## 项目结构

```text
Prism/
├── cmd/server/                    # 服务入口与依赖初始化
├── configs/                       # 示例、Docker 与本地配置
├── console/                       # React 管理端，dist 嵌入 Go 二进制
├── database/migrations/           # 按时间排序的数据迁移
├── internal/
│   ├── api/                       # Console、Admin、V1 与回调路由
│   ├── gateway/
│   │   ├── canonical/             # 协议中立模型和事件
│   │   ├── codec/                 # 三种下游协议编解码
│   │   ├── engine/                # 执行、重试、计费与日志
│   │   ├── handler/               # Chat、Responses、Messages、Files
│   │   ├── pipeline/              # Chat 边界编排与 Playground 复用
│   │   ├── responses/             # Responses 生命周期
│   │   ├── routing/               # 选路、并发和熔断
│   │   └── transport/             # 五种上游 Transport
│   ├── model/                     # GORM 数据模型
│   ├── service/                   # 管理、计费与异步能力业务
│   └── worker/                    # Asynq 后台任务
├── pkg/                           # 配置、数据库、缓存、日志、指标等组件
├── API_REFERENCE.md               # 静态 API 参考
├── Dockerfile
└── docker-compose.yml
```

## 快速开始

### 环境要求

- Go 1.25.6+
- Node.js `^20.19.0` 或 `>=22.12.0`
- MySQL 8+
- Redis 7+

### Docker Compose（本地体验）

```bash
git clone https://github.com/mirainya/Prism.git
cd Prism
docker compose up -d --build
```

访问 `http://localhost:23523/`。Compose 文件包含固定的示例数据库密码、JWT 密钥，并公开 MySQL 与 Redis 端口，不应直接用于公网生产环境；生产部署前必须修改凭据、限制端口并提供独立配置。

### 手动构建

```bash
git clone https://github.com/mirainya/Prism.git
cd Prism
cp configs/config.example.yaml configs/config.yaml

cd console
npm ci
npm run build
cd ..

go build -trimpath -ldflags="-s -w" -o prism ./cmd/server
./prism
```

`console/dist` 未提交到仓库，构建后端前必须先构建前端。Windows 可运行 `build.bat` 生成 Linux AMD64 二进制。

首次启动会执行 GORM AutoMigrate 创建或补充表结构。已有实例升级时，还需备份数据库并按文件名顺序执行 `database/migrations/` 中尚未应用的 SQL；这些数据迁移不会由 AutoMigrate 自动执行。

当前版本没有管理员自举命令。首次部署需先注册用户，再由数据库管理员将该用户的 `users.role` 提升为 `admin`，才能配置网关。

### 开发模式

```bash
# 后端：http://localhost:23523
go run ./cmd/server

# 前端：http://localhost:3001
cd console
npm ci
npm run dev
```

前端开发服务器默认把 `/api` 转发到 `http://localhost:23523`；调试 `/v1` 时直接使用后端端口。

## API 概览

V1 接口支持以下两种认证头：

```http
Authorization: Bearer sk-prism-...
```

```http
x-api-key: sk-prism-...
```

除三个对话入口和 Files API 外，V1 还提供：

- `GET /v1/models`、`GET /v1/models/:code`
- `GET /v1/channels`、`GET /v1/capabilities`
- `POST /v1/capabilities/:capability`
- `POST /v1/images/generations`、`POST /v1/videos/generations`
- `GET /v1/tasks/:task_no`、`POST /v1/tasks/:task_no/cancel`

Responses 资源接口包括：

- `GET /v1/responses/:id`
- `DELETE /v1/responses/:id`
- `POST /v1/responses/:id/cancel`
- `GET /v1/responses/:id/input_items`

完整请求示例见 [`API_REFERENCE.md`](API_REFERENCE.md)。登录控制台后可访问 `/#/api-docs` 使用在线文档与试用面板。

## 管理后台

- 网关渠道：管理 Gateway V2 渠道、Key、模型发现与导入。
- 对话模型：管理 Ability、Transport 探测、公开模型元数据、分组与排序。
- 能力渠道与能力配置：管理图片、视频等异步能力的渠道、账号和参数。
- Playground：测试 Chat、Responses、Messages 和异步能力，查看历史与调试信息。
- 用户与令牌：管理用户角色、余额、API Token、限流和充值。
- 日志：查看调用日志、对话记录及管理员请求详情与重试。
- 仪表盘与 API 文档：查看用量统计，并在登录状态下试用接口。

## 配置

复制 [`configs/config.example.yaml`](configs/config.example.yaml) 为 `configs/config.yaml`。重点配置包括：

- `server.port`、`server.jwt_secret`
- `server.reset_gateway_concurrency_on_start`：多实例共用数据库时必须设为 `false`
- `database.*`、`redis.*`
- `worker.*`、`http_client.*`
- `file_storage.max_total_size_mb`
- `rate_limit.*`

数据库、Redis、Worker、HTTP Client 和监听端口都在启动时初始化，修改这些配置后需要重启服务。

## 检查

```bash
go test ./...
go vet ./...

cd console
npm run build
```

## License

MIT
