# Prism v2.0.0 使用教程

本文说明当前仓库代码的安装、目录配置和 API 使用。生产发布步骤必须按运维文档执行。

## 1. 环境

| 组件 | 要求 | 用途 |
|---|---|---|
| Go | 1.26.6+ | 后端构建 |
| Node.js | `^20.19.0` 或 `>=22.12.0` | 控制台构建 |
| MySQL | Oracle MySQL 8.0.16+ | 目录、执行、计费、Outbox 与租约；不支持 MariaDB、TiDB 等兼容实现 |
| Redis | 7+ | 缓存和限流等运行依赖 |

Prism 的后台执行不使用 Redis 队列。异步任务事实与调度均保存在 MySQL。

## 2. 构建与启动

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

检查版本和健康状态：

```bash
./prism version
curl http://localhost:23523/health
```

开发模式：

```bash
go run ./cmd/server

cd console
npm ci
npm run dev
```

控制台开发服务器默认使用 `http://localhost:3001`，后端默认使用 `http://localhost:23523`。

## 3. 配置

复制 `configs/config.example.yaml` 后配置：

- `server.port`、`server.jwt_secret`；
- `database.*`；
- `redis.*`；
- `http_client.*`；
- `file_storage.base_url`、`api_key`、`upload_path`、单文件与 Token 总容量上限；
- `rate_limit.*`；
- `observability.*`。

后台执行没有 YAML 队列配置。SQL Worker 的并发和租约策略由当前实现管理。

Files API、图片编辑素材和 managed-copy 交付依赖 x-file-storage。启用这些能力时必须同时配置 `file_storage.base_url` 与 `file_storage.api_key`；两者为空表示明确禁用托管文件存储。

生产环境还必须提供四个独立的 32 字节 Base64 环境密钥：

```text
PRISM_GATEWAY_KEK_B64
PRISM_GATEWAY_HMAC_B64
PRISM_GATEWAY_PAYLOAD_KEK_B64
PRISM_GATEWAY_PAYLOAD_HMAC_B64
```

凭据 KEK/HMAC 用于渠道密钥，Payload KEK/HMAC 用于规范化请求、结果、回调目标及敏感证据。已有 KEK 不能直接替换。

## 4. 数据库初始化与升级

新库：

```bash
./prism migrate status
./prism migrate up
```

服务启动只检查迁移状态，不执行 AutoMigrate。存在旧库、待执行或失败迁移时，服务拒绝启动。

基线前实例需要停服和备份，补齐旧迁移至 `20260718_120000_add_conversation_turn_context_mode.sql`，然后执行：

```bash
./prism migrate adopt
./prism migrate up
```

旧数据转入统一结构时依次使用：

```bash
./prism migrate audit
./prism migrate audit-deep
./prism migrate import-legacy
./prism migrate import-runtime
./prism migrate import-video-assets
./prism migrate import-ai-files
./prism migrate verify-crypto
./prism migrate audit-deep
```

只有最终 `audit-deep` 明确 `ready_for_cleanup=true`，并完成备份与停机验证后，才能执行：

```bash
./prism migrate cleanup-legacy
```

迁移和任何生产数据库操作都应单独审核后执行。

## 5. 首次管理

Prism 不创建默认管理员：

1. 通过 `/api/auth/register` 注册用户；
2. 由数据库管理员将 `users.role` 设置为 `admin`；
3. 登录控制台配置统一网关。

统一网关配置顺序：

1. 创建结算币种；
2. 创建渠道、凭据池和加密凭据，授予用途；
3. 创建目录草稿；
4. 配置 Product、SKU、Operation、Transport、Route、Offering；
5. 配置售价、成本方案及费率证据；
6. 运行目录发现与审核；
7. 发布目录；
8. 创建部署代次，登记实例并提交目录与加密就绪证明；
9. 激活部署代次与目录发布版。

目录未就绪时，控制面仍可使用，统一数据面返回不可用，不会改走旧执行链。

旧“能力渠道”“视频渠道”和独立模型映射页面不再使用。所有模型与媒体能力都通过统一目录配置。

## 6. 调用 API

创建 API Token 后使用：

```http
Authorization: Bearer sk-prism-...
```

### 对话

```bash
curl "$BASE_URL/v1/chat/completions" \
  -H "Authorization: Bearer $PRISM_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model":"model-code","messages":[{"role":"user","content":"你好"}]}'
```

Messages 使用 `/v1/messages`，Responses 使用 `/v1/responses`。后台 Responses 可通过 `/v1/responses/:id` 查询，并且只有 Responses 资源提供取消操作。

### 图片

```bash
curl "$BASE_URL/v1/images/generations" \
  -H "Authorization: Bearer $PRISM_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model":"image-model","prompt":"糖果色街道","size":"1024x1024"}'
```

图片编辑使用 `/v1/images/edits`。图片输入会转为统一媒体资产引用，不把 Base64 任务参数作为长期执行事实保存。

### 视频

```bash
curl "$BASE_URL/v1/videos/generations" \
  -H "Authorization: Bearer $PRISM_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: video-demo-1" \
  -d '{"model":"video-model","prompt":"海边日落","duration":5,"resolution":"1080p","ratio":"16:9"}'
```

查询：

```bash
curl "$BASE_URL/v1/videos/generations/<id>" \
  -H "Authorization: Bearer $PRISM_TOKEN"
```

视频 API 不支持取消。`/v1/videos/estimate` 使用活动目录估价；`/v1/videos/queue` 返回当前 Token 的活动任务轻量视图。

图生视频素材先调用 `/v1/videos/assets` 上传或登记 URL，再在视频请求中引用素材 ID。素材按 Token 隔离。

## 7. 调用、计费与结果

- 每次调用创建统一 `gw_api_calls` 事实；
- 每次真实上游交换创建 `gw_api_call_attempts`；
- 调用在发送前按目录售价预授权，终态按实际计量结算；
- Attempt 固定成本方案，上游成本按同一方案产生证据与事件；
- 异步任务使用 SQL Outbox 与租约，提交结果不确定时进入恢复或人工核验；
- 结果 URL、托管复制和客户端回调是独立交付事实，不改变生成计费终态。

调用列表只读取有界元数据。敏感正文单独加密保存并受权限、完整性与保留期约束。

## 8. 控制台排查

- “统一调用”查看 Call、Attempt、请求状态、HTTP 状态、耗时、计费与资源关联；
- “视频任务”是 `gw_video_tasks` 的只读投影；
- “目录来源与发现”查看上游模型发现、候选价格及审核；
- “访问日志”“审计事件”“余额流水”分别查看请求、管理变更和资金变化；
- “Playground”使用同一统一网关，不使用独立路由或计费逻辑。

公开路由完整清单见 `API_REFERENCE.md`，生产切换见 `docs/unified_gateway_operations.md`。
