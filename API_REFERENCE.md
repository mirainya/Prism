# Prism v2.0.0 API Reference

本文只记录当前代码注册的 HTTP 路由。公开 AI API 位于 `/v1`；控制台 API 位于 `/api`；内部上游回调位于 `/internal/gateway`。

实际可用模型与价格取决于当前实例已激活的目录发布版。

## 通用约定

### 基础地址

```text
https://prism.mirainya.icu
```

### 认证

| 路由 | 认证 |
|---|---|
| `/v1/*` | `Authorization: Bearer sk-prism-...` 或 `x-api-key` |
| `/api/*`（除 auth/public） | `Authorization: Bearer <JWT>` |
| `/api/admin/*` | JWT 且用户角色为 admin |
| `/internal/gateway/*` | 执行级回调令牌 |

所有响应带 `X-Request-ID`。建立统一 Call 后，调用响应还带 `X-Prism-Call-ID`。

### 响应格式

Chat、Messages、Responses、Files、Models 和 Images 使用各自兼容协议。Videos 与控制台 API 使用 Prism 包装：

```json
{"code":0,"message":"success","data":{}}
```

失败示例：

```json
{"code":400,"message":"invalid parameters"}
```

## 对话 API

### POST /v1/chat/completions

OpenAI Chat Completions 兼容接口，支持非流式和 SSE。主要字段：`model`、`messages`、`stream`、`tools`、`tool_choice`、`response_format`、采样参数及输出上限。

```bash
curl "$BASE_URL/v1/chat/completions" \
  -H "Authorization: Bearer $PRISM_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model":"model-code","messages":[{"role":"user","content":"你好"}]}'
```

### POST /v1/messages

Anthropic Messages 兼容接口，支持非流式和 SSE。使用 `x-api-key` 时仍由 Prism Token 鉴权。

```bash
curl "$BASE_URL/v1/messages" \
  -H "x-api-key: $PRISM_TOKEN" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"model-code","max_tokens":1024,"messages":[{"role":"user","content":"你好"}]}'
```

### POST /v1/responses

OpenAI Responses 兼容接口，支持 `stream`、`store`、`background`、`previous_response_id` 和 `Idempotency-Key`。

```bash
curl "$BASE_URL/v1/responses" \
  -H "Authorization: Bearer $PRISM_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: demo-1" \
  -d '{"model":"model-code","input":"你好"}'
```

Responses 资源路由：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/responses/:id` | 查询 Response |
| DELETE | `/v1/responses/:id` | 删除 Response |
| POST | `/v1/responses/:id/cancel` | 取消后台 Response |
| GET | `/v1/responses/:id/input_items` | 输入项分页 |

`cancel` 仅属于 Responses 资源，不代表图片或视频支持取消。

### Conversation 关联

Chat、Messages 和 Responses 接受 `X-Prism-Conversation-ID` 续接当前 Token 的 Conversation。Chat 还接受请求体 `conversation_id`，两处同时提供时必须一致。调用的 canonical 输入和已产生输出会投影为 Conversation；`store: false` 不关闭该投影。

## Files API

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/files` | `multipart/form-data` 上传 |
| GET | `/v1/files` | 当前 Token 文件列表 |
| GET | `/v1/files/:id` | 文件元数据 |
| GET | `/v1/files/:id/content` | 文件内容 |
| DELETE | `/v1/files/:id` | 删除文件 |

```bash
curl "$BASE_URL/v1/files" \
  -H "Authorization: Bearer $PRISM_TOKEN" \
  -F "purpose=vision" \
  -F "file=@./image.png"
```

文件只能由创建它的 Token 查询、下载、引用或删除。

## Images API

### POST /v1/images/generations

OpenAI 风格 JSON 接口。必填字段为 `model` 和 `prompt`。支持 `n`、`size`、`aspect_ratio`、`quality`、`response_format`、`output_format`、`stream` 等参数；`image_urls` 非空时按图片编辑操作选路。

```bash
curl "$BASE_URL/v1/images/generations" \
  -H "Authorization: Bearer $PRISM_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model":"image-model","prompt":"糖果色城市","size":"1024x1024","response_format":"url"}'
```

### POST /v1/images/edits

OpenAI 风格 `multipart/form-data` 图片编辑接口。输入图片会转为统一媒体资产引用，不把 Base64 请求作为长期任务参数保存。

图片结果返回 OpenAI `created` 与 `data` 结构；流式请求返回 SSE。

## Videos API

### POST /v1/videos/generations

创建视频。请求经过统一目录选路，固定发布版、SKU、Offering、成本方案、Transport 与凭据版本，并在事务中创建 Call、Attempt、异步执行和 SQL Outbox。

```json
{
  "model": "video-model",
  "prompt": "海边日落",
  "duration": 5,
  "resolution": "1080p",
  "ratio": "16:9",
  "service_tier": "standard",
  "callback_url": "https://example.com/prism-callback"
}
```

可使用 `Idempotency-Key`；目录策略可规定 required、optional 或 forbidden。成功创建：

```json
{"code":0,"message":"success","data":{"id":"uuid","status":"queued","service_tier":"standard"}}
```

### POST /v1/videos/estimate

使用当前活动目录估算费用，返回字符串金额及币种。估算不创建任务。

### 视频查询

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/videos/generations` | 当前 Token 的视频分页列表 |
| GET | `/v1/videos/generations/:id` | 视频详情与可用结果 |
| GET | `/v1/videos/generations/:id/queue` | 单任务异步状态 |
| GET | `/v1/videos/queue` | 最多 100 个活动任务的轻量投影 |

视频没有取消路由。任务进入提交未知状态时由恢复 Worker 查询或进入人工核验，不会自动重复提交。

### 视频素材

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/videos/assets` | 上传文件或登记受控 URL |
| POST | `/v1/videos/uploads` | 与素材创建相同的兼容入口 |
| GET | `/v1/videos/assets/:asset_id` | 素材详情 |
| DELETE | `/v1/videos/assets/:asset_id` | 将未占用素材标为过期 |

素材接口接受 `multipart/form-data` 或 `application/json`。素材按用户及 Token 隔离；已被任务引用时不能删除。

## Models API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/models` | 当前可用公开模型列表 |
| GET | `/v1/models/:code` | 模型详情与参数 Schema |

模型列表来自已激活的统一目录，不提供渠道或能力配置详情。

## 不存在的旧路由

以下路由未注册，返回 404：

- `/v1/channels`
- `/v1/capabilities`
- `/v1/capabilities/:capability`
- `/v1/tasks/:task_no`
- `/v1/tasks/:task_no/cancel`
- `/v1/videos/generations/:id/cancel`

## 控制台 API 概览

公开认证：`POST /api/auth/register`、`POST /api/auth/login`、`POST /api/auth/logout`、`GET /api/public/pricing`。

登录用户可管理自身资料和 Token，查看仪表盘、统一调用、只读任务投影、观测日志、Conversation、文档及 Playground。

管理员统一网关路由以 `/api/admin/unified-gateway/*` 为前缀，覆盖：

- overview、channels、pools、credentials；
- catalog、catalog-sources、discoveries、products、SKUs、rates；
- currencies、rate-evidence；
- deployments、members、catalog/crypto readiness；
- `GET /api/admin/unified-gateway/deployments/runtime-identity`：读取当前实例与二进制摘要；
- `POST /api/admin/unified-gateway/deployments/:id/prove-current`：校验当前目录、Adapter 与密钥并提交当前实例证明；
- calls、attempts、request logs 与按权限读取的 payload；
- offering credential validations。

管理员视频任务为只读投影：`GET /api/admin/video/tasks`、`GET /api/admin/video/tasks/:id`、`GET /api/admin/video/stats`。不存在旧能力渠道或视频渠道写接口。

## 内部回调

`POST /internal/gateway/callback` 接收上游回调。路径不再接受客户端提供的 `scope`；事件作用域由服务端固定为 `unified`。请求必须带 `Content-Type: application/json`（可带标准参数）和 `X-Gateway-Callback-Token`，可带 `X-Gateway-Event-ID`。正文上限为 1 MiB，入口按客户端地址限速并限制并发，只持久化短期回调证据后返回 `202`，业务状态由 SQL Worker 消费后更新。认证失败不会创建 Receipt；Receipt 到期或处理完成后会清除加密正文。
