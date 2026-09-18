<p align="center">
  <img src="console/assets/logo.svg" width="112" alt="Prism Logo" />
</p>

<h1 align="center">Prism V2</h1>

<p align="center">统一管理 LLM、图片与视频模型的 AI API 网关</p>

Prism 将不同上游的模型、协议、API Key、参数映射和计费规则集中到一个网关中，并通过统一 API 对外提供服务。后端使用 Go，管理控制台使用 React；前端产物会嵌入服务端二进制，可作为单个服务部署。

> V2 是当前分支的产品与架构版本。公开接口继续使用 `/v1/*`，不会因仓库分支名称改变。

## 核心能力

- **统一模型目录**：按 LLM、图片、视频分类管理模型、规格、上下游名称和公开信息。
- **多协议接入**：支持 OpenAI Chat Completions、Responses、Anthropic Messages、OpenAI Images 与视频生成接口。
- **上游与 Key 管理**：直接维护上游地址、协议、Key 池、可用模型、权重和并发限制。
- **参数映射**：LLM 使用标准协议转换；图片和视频可配置请求、状态与结果的 JSON 映射。
- **统一计费**：支持按 Token、次、张、秒等单位定价，也支持组合计费表达式。
- **异步任务**：视频与后台 Responses 通过 MySQL Outbox、租约和恢复 Worker 执行，支持轮询与回调。
- **结果转存**：每个 Prism Token 可绑定独立 XFileStorage Key，调用创建后固定本次任务使用的 Key。
- **运行诊断**：提供调用记录、上游请求日志、异步任务、熔断状态、审计事件和账务流水。

## 使用流程

管理员在运维台完成一条模型线路的配置：

```text
模型 -> 上游接口与协议 -> API Key -> 参数映射 -> 规格与价格 -> 路由状态 -> 在线验证
```

调用方创建 Prism Token 后，通过 `/v1/models` 获取可用模型，再使用同一个 Token 调用对话、图片或视频接口。配置更新直接作用于后续请求，不需要创建草稿、版本快照或发布批次。

## 环境要求

| 组件 | 最低要求 | 用途 |
|---|---:|---|
| Go | 1.26.6 | 后端构建 |
| Node.js | 20.19 或 22.12 | 控制台构建 |
| MySQL | Oracle MySQL 8.0.16 | 业务数据、计费、异步任务与租约 |
| Redis | 7 | 缓存与限流 |

Prism 当前不支持 PostgreSQL、MariaDB 或 TiDB。数据库结构只由 `database/migrations/` 中的迁移文件管理。

## 快速开始

### 1. 准备配置

```bash
git clone --branch v2 https://github.com/mirainya/Prism.git
cd Prism
cp configs/config.example.yaml configs/config.yaml
```

至少需要修改以下配置：

- `server.jwt_secret`：不少于 32 字节的随机值；
- `database.*`：MySQL 连接信息；
- `redis.*`：Redis 连接信息；
- `file_storage.*`：使用 XFileStorage 时填写。

生产环境还需要两个独立的 32 字节 Base64 密钥：

```text
PRISM_GATEWAY_PAYLOAD_KEK_B64
PRISM_GATEWAY_PAYLOAD_HMAC_B64
```

它们用于保护请求正文、结果、回调目标和敏感证据。上游 API Key 当前以明文保存在 `gw_credentials.secret`，应依靠数据库访问控制和主机权限保护。

### 2. 构建

```bash
cd console
npm ci
npm run build
cd ..

go build -trimpath -o prism ./cmd/server
```

Windows 也可以运行 `build.bat`，生成 Linux AMD64 部署包到 `dist/`。

### 3. 初始化并启动

```bash
./prism migrate status
./prism migrate up
./prism
```

默认地址：

- 控制台：`http://localhost:23523`
- 健康检查：`http://localhost:23523/health`
- Prometheus 指标：`http://localhost:23523/metrics`

首次安装不会创建默认管理员。先注册普通用户，再由数据库管理员将该用户的 `users.role` 设置为 `admin`。

### Docker Compose

```bash
docker compose up -d --build
```

仓库中的 Compose 配置仅供本地开发，包含示例数据库密码并公开 MySQL、Redis 端口，不可直接用于公网环境。

## 公开 API

所有 `/v1/*` 请求使用 Prism Token：

```http
Authorization: Bearer sk-prism-...
```

也可以使用 `x-api-key` 请求头。

| 方法 | 路径 | 用途 |
|---|---|---|
| `GET` | `/v1/models` | 获取可用模型与公开信息 |
| `GET` | `/v1/models/:code` | 获取单个模型详情 |
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions |
| `POST` | `/v1/messages` | Anthropic Messages |
| `POST` | `/v1/responses` | OpenAI Responses |
| `POST` | `/v1/images/generations` | 图片生成 |
| `POST` | `/v1/images/edits` | 图片编辑 |
| `POST` | `/v1/videos/generations` | 视频生成 |
| `POST` | `/v1/videos/estimate` | 视频价格估算 |
| `GET` | `/v1/videos/generations/:id` | 查询视频任务与结果 |
| `POST` | `/v1/files` | 上传文件 |

完整请求格式与状态码见 [API_REFERENCE.md](API_REFERENCE.md)。

## 架构概览

```text
Client
  -> Gin Handler（认证、限流、协议校验）
  -> Codec / Adapter（标准协议与 JSON 映射）
  -> Gateway Engine（选路、预授权、调用、结算）
  -> Upstream API

MySQL
  -> Outbox / Lease Worker
  -> Submit / Poll / Callback / Recover
  -> Result Delivery / XFileStorage
```

请求开始时会固定模型、规格、线路、价格、凭据和文件存储配置，避免任务执行期间的配置修改影响既有调用。新调用事实统一写入 `gw_*` 表；旧表只用于迁移或历史数据读取。

## 数据库迁移

```bash
./prism migrate status
./prism migrate up
./prism migrate audit
./prism migrate audit-deep
```

已在任何环境执行过的迁移文件不得修改。升级旧数据库、导入旧执行记录或清理旧表时，按 [统一网关运行手册](docs/unified_gateway_operations.md) 操作。

## 开发检查

```bash
go test ./...
go vet ./...

cd console
npm test
npx tsc --noEmit
npm run build
```

## 文档

- [API 参考](API_REFERENCE.md)
- [使用教程](docs/USAGE_GUIDE.md)
- [模型配置手册](docs/MODEL_ONBOARDING.md)
- [项目地图](docs/PROJECT_MAP.md)
- [视频架构](docs/VIDEO_ARCHITECTURE.md)
- [统一网关运行手册](docs/unified_gateway_operations.md)
- [V2 验收状态](docs/unified_gateway_acceptance.md)

## License

[MIT](LICENSE)
