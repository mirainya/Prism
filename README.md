<p align="center">
  <img src="console/assets/logo.svg" width="120" alt="Prism Logo" />
</p>

<h1 align="center">Prism v2.0.0</h1>

<p align="center">统一目录、协议转换、精确计费与异步执行的 AI Gateway</p>

Prism 使用 Go + React 构建。公开 API 位于 `/v1`；“Gateway V2”是内部架构名称，不代表存在 `/v2` HTTP API。前端构建产物嵌入 Go 二进制，可作为单个服务部署。

## 架构

```text
客户端
  -> Gin Handler（认证、限流、协议校验）
  -> Downstream Codec
  -> Canonical Request / Event
  -> Gateway Engine（路由、调用、尝试、预授权、结算）
  -> Product Transport / Adapter
  -> 上游 API
```

统一目录固定一次执行所使用的发布版、产品、SKU、线路、Offering、成本方案、凭据及凭据版本。图片使用统一同步能力生命周期；后台 Responses 和视频由 MySQL 中的持久 Outbox 与租约 Worker 执行，不使用 Redis 任务队列。

## 公开 API

| 方法 | 路径 | 用途 |
|---|---|---|
| POST | `/v1/chat/completions` | OpenAI Chat Completions |
| POST | `/v1/messages` | Anthropic Messages |
| POST | `/v1/responses` | OpenAI Responses |
| GET / DELETE / POST | `/v1/responses/:id` 相关路由 | 查询、删除、取消后台 Response、读取输入项 |
| POST / GET / DELETE | `/v1/files` 相关路由 | 文件上传、列表、详情、内容与删除 |
| POST | `/v1/images/generations` | OpenAI 风格图片生成 |
| POST | `/v1/images/edits` | OpenAI 风格图片编辑 |
| POST | `/v1/videos/generations` | 视频生成 |
| POST | `/v1/videos/estimate` | 视频价格估算 |
| GET | `/v1/videos/generations` | 视频列表 |
| GET | `/v1/videos/generations/:id` | 视频详情 |
| GET | `/v1/videos/generations/:id/queue` | 单任务队列状态 |
| GET | `/v1/videos/queue` | 当前 Token 的活动视频任务 |
| POST / GET / DELETE | `/v1/videos/assets` 相关路由 | 视频输入素材 |
| GET | `/v1/models`、`/v1/models/:code` | 公开模型目录 |

所有 `/v1/*` 路由使用 API Token：

```http
Authorization: Bearer sk-prism-...
```

也兼容 `x-api-key`。不存在 `/v1/channels`、`/v1/capabilities`、通用 `/v1/tasks` 或视频取消接口。

完整行为见 [API_REFERENCE.md](API_REFERENCE.md)。

## 统一数据模型

所有新执行事实使用 `gw_*` 表：

| 领域 | 主要数据 |
|---|---|
| 目录 | 发布版、模型、产品、SKU、Operation Contract、Transport、Route、Offering |
| 凭据 | 渠道、凭据池、用途授权、加密凭据版本、请求/任务名额 |
| 执行 | Call、Attempt、请求日志、加密 Payload、资源、异步执行、Outbox |
| 计费 | 售价、成本方案、预授权、结算事件、上游成本证据与事件 |
| 交付 | 媒体资产、结果交付、来源、状态事件、客户端回调投递与尝试 |
| 控制面 | 目录发现、审核、部署代次、成员就绪证明、运行状态 |

旧 `api_calls`、`tasks`、`ai_responses` 等表只作为迁移源或兼容数据存在，不再是新调用的执行事实源。历史清理必须通过深度审计，不能直接删除。

## 运行时 Worker

服务进程按数据库状态启动以下 SQL Worker：

- 目录发现 Worker；
- 异步提交、查询与恢复 Worker；
- 后台 Responses Attempt Worker；
- 上游回调消费 Worker；
- 客户端回调投递 Worker；
- 结果交付到期与恢复 Worker；
- 能力调用恢复、敏感正文清理和媒体资产清理 Worker。

Worker 使用数据库租约认领任务。未知提交结果会保留为待恢复或人工核验，不会擅自重复生成。

## 快速开始

### 环境

- Go 1.26.6+
- Node.js `^20.19.0` 或 `>=22.12.0`
- Oracle MySQL 8.0.16+
- Redis 7+，仅用于缓存和限流等运行依赖

### 构建

```bash
git clone https://github.com/mirainya/Prism.git
cd Prism
cp configs/config.example.yaml configs/config.yaml

cd console
npm ci
npm run build
cd ..

VERSION=2.0.0
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
SOURCE_REVISION="$(git rev-parse HEAD)"
go build -trimpath -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X github.com/mirainya/Prism/internal/gateway/adapter.BuildRevision=${SOURCE_REVISION}" -o prism ./cmd/server
./prism migrate up
./prism
```

Windows 可运行 `build.bat` 生成 Linux AMD64 软件包。版本可用 `./prism version` 检查。

Docker 本地体验：

```bash
docker compose up -d --build
```

示例 Compose 包含开发凭据并公开 MySQL、Redis 端口，不可直接用于公网生产。

## 数据库迁移

`database/migrations/` 是唯一生产结构来源，服务不会执行 GORM AutoMigrate。服务发现旧库、失败迁移或待执行迁移时会拒绝启动。

```bash
./prism migrate status
./prism migrate up
./prism migrate audit
./prism migrate audit-deep
```

基线前旧库应停服、备份，应用旧迁移至 `20260718_120000_add_conversation_turn_context_mode.sql`，然后执行：

```bash
./prism migrate adopt
./prism migrate up
```

统一网关迁移还提供 `import-legacy`、`import-runtime`、`import-video-assets`、`import-ai-files`、`verify-crypto` 和 `cleanup-legacy`。具体顺序见 [统一网关运行与发布](docs/unified_gateway_operations.md)。

## 加密密钥

存在统一目录或执行数据时，生产环境必须提供四个独立的 32 字节 Base64 密钥：

```text
PRISM_GATEWAY_KEK_B64
PRISM_GATEWAY_HMAC_B64
PRISM_GATEWAY_PAYLOAD_KEK_B64
PRISM_GATEWAY_PAYLOAD_HMAC_B64
```

前两个保护渠道凭据，后两个保护请求、结果及回调资料。升级时不得替换已有凭据 KEK，否则历史密文将无法解密。

## 管理控制台

控制台包括仪表盘、Playground、Token、统一网关渠道与凭据池、目录发布版、目录来源与发现、币种与费率证据、统一调用、只读视频任务、访问日志、审计事件、余额流水和对话记录。

旧“能力渠道”“视频渠道”和独立能力配置页面不再是 v2.0.0 的配置入口。图片、视频和对话均从统一目录选路。

首次安装不会创建默认管理员。注册用户后，由数据库管理员将该用户的 `users.role` 设置为 `admin`。

## 健康与检查

- `GET /health`：MySQL 与 Redis 健康状态
- `GET /metrics`：Prometheus 指标

```bash
go test ./...
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 '-checks=all,-ST*' ./...

cd console
npm test
npx tsc --noEmit
npm run build
```

## 文档

- [API 参考](API_REFERENCE.md)
- [使用教程](docs/USAGE_GUIDE.md)
- [项目地图](docs/PROJECT_MAP.md)
- [视频架构](docs/VIDEO_ARCHITECTURE.md)
- [统一网关运行与发布](docs/unified_gateway_operations.md)
- [v2.0.0 验收状态](docs/unified_gateway_acceptance.md)

## License

MIT
