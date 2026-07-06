# Prism API Reference (Machine-Readable)

> AI Gateway 统一接口文档。供 AI Agent / 自动化工具集成使用。

## 基础信息

- Base URL: `http://{host}:{port}`
- 响应格式: JSON
- 统一响应结构:

```json
{"code": 0, "message": "success", "data": {...}}  // 成功
{"code": 400, "message": "error reason"}           // 失败
```

---

## 认证方式

| 路由组 | 认证方式 | Header |
|--------|----------|--------|
| `/v1/*` | API Token | `Authorization: Bearer sk-prism-xxx` |
| `/api/*` (除 auth/public) | JWT | `Authorization: Bearer {jwt_token}` |
| `/api/admin/*` | JWT + Admin 角色 | 同上 |
| `/internal/*` | Callback 签名 | 内部使用 |

---

## 公开接口 (无需认证)

### POST /api/auth/register

注册用户。

```json
// Request
{"username": "string (3-50)", "password": "string (min 6)"}

// Response.data
{"id": 1, "username": "xxx", "role": "user"}
```

### POST /api/auth/login

```json
// Request
{"username": "string", "password": "string"}

// Response.data
{"token": "jwt_string", "user": {"id": 1, "username": "xxx", "role": "user", "balance": "0.00"}}
```

### POST /api/auth/logout

Header: `Authorization: Bearer {jwt}`。无请求体。

### GET /api/public/pricing

获取定价信息。无参数。

---

## V1 API (Token 认证，用于 AI 调用)

### POST /v1/chat/completions

OpenAI 兼容的 Chat Completions 接口。

```json
// Request
{
  "model": "string (required)",
  "messages": [
    {
      "role": "system|user|assistant|tool",
      "content": "string 或 ContentPart[]",
      "name": "string?",
      "tool_calls": [{"id": "str", "type": "function", "function": {"name": "str", "arguments": "json_str"}}],
      "tool_call_id": "string?",
      "reasoning_content": "string?"
    }
  ],
  "temperature": 0.0,
  "max_tokens": 4096,
  "top_p": 1.0,
  "frequency_penalty": 0.0,
  "presence_penalty": 0.0,
  "stop": ["string"],
  "stream": false,
  "tools": [{"type": "function", "function": {"name": "str", "description": "str", "parameters": {}}}],
  "tool_choice": "auto|none|required|{type,function:{name}}",
  "response_format": {"type": "text|json_object|json_schema", "json_schema": {}},
  "seed": 42,
  "user": "string",
  "conversation_id": "string?"
}
```

ContentPart 类型:
```json
{"type": "text", "text": "..."}
{"type": "image_url", "image_url": {"url": "https://... 或 data:image/png;base64,...", "detail": "auto|low|high"}}
{"type": "file_url", "file_url": {"url": "https://...", "content_type": "application/pdf"}}
```

- `stream: false` → 返回标准 OpenAI ChatCompletion 响应
- `stream: true` → SSE 流，`Content-Type: text/event-stream`

### GET /v1/models

列出可用模型。

```json
// Response (直接返回，非统一包装)
{
  "object": "list",
  "data": [
    {"id": "model-code", "object": "model", "created": 1700000000, "owned_by": "provider", "max_tokens": 8192, "features": ["vision","tools"]}
  ]
}
```

### GET /v1/models/:code

获取单个模型详情（含参数 schema）。

```json
// Response (直接返回，非统一包装)
{
  "id": "gpt-4o",
  "object": "model",
  "created": 1700000000,
  "owned_by": "openai",
  "name": "GPT-4o",
  "description": "最新多模态模型",
  "max_tokens": 128000,
  "features": ["vision", "tools", "json_mode"],
  "param_schema": {
    "temperature": {"type": "number", "min": 0, "max": 2, "default": 1},
    "max_tokens": {"type": "integer", "min": 1, "max": 128000}
  }
}
```

- 404: 模型不存在或已禁用

### GET /v1/channels

列出可用渠道。

### GET /v1/capabilities

列出可用能力（含参数 schema）。Query: `channel=string&type=string` (可选)

```json
// Response.data
[
  {
    "code": "text2img",
    "name": "文生图",
    "type": "image",
    "description": "文本生成图片",
    "param_schema": {
      "prompt": {"type": "string", "required": true},
      "size": {"type": "string", "enum": ["1024x1024", "512x512"], "default": "1024x1024"}
    },
    "channels": [
      {"channel_type": "midjourney", "channel_name": "MJ", "model": "mj-v6", "price": "0.10"}
    ]
  }
]
```

### POST /v1/capabilities/:capability

调用统一能力接口（图片生成、视频生成等）。

```json
// Request
{
  "model": "string",
  "channel": "string?",
  "callback_url": "string?",
  "...params": "any"
}

// Response.data
{"task_id": "xxx", "status": "pending|processing|completed|failed", "result": {}}
```

### POST /v1/images/generations

图片生成。**OpenAI 标准协议**，任何 OpenAI 图像客户端/SDK 可即插即用。
对外统一同步返图：同步渠道直接返回，异步渠道网关内部轮询等待（默认上限 300s）。

```json
// Request (OpenAI 标准，字段平铺在顶层)
{
  "model": "string (required)",
  "prompt": "string (required)",
  "n": 1,
  "size": "1024x1024",
  "quality": "auto|low|medium|high",
  "response_format": "url|b64_json",
  "output_format": "png|jpeg|webp",
  "output_compression": 80,
  "moderation": "auto|low",
  "style": "string?",
  "user": "string?"
}
```

响应**不套** `{code,data}` 外壳，直接返回 OpenAI 原生结构：

```json
// 成功 (HTTP 200)
{
  "created": 1700000000,
  "data": [
    {"url": "https://...", "revised_prompt": "..."}
  ]
}

// 失败 (HTTP 4xx/5xx，OpenAI 错误格式)
{"error": {"message": "reason", "type": "invalid_request_error|api_error|authentication_error"}}

// 超时降级 (HTTP 202，异步渠道超过等待上限仍未出图)
{"status": "processing", "task_id": "xxx", "message": "query via GET /v1/tasks/{task_id}"}
```

> `prompt` 之外的参数经渠道 `param_mapping`（含 `computed_params` 计算）映射后透传给上游。
> 超时上限内出图直接返，超限返 202 + task_id，客户端用 `GET /v1/tasks/{task_id}` 后续查询。

### POST /v1/images/generations/async

图片生成（旧版异步接口）。提交即返 task_id，不等待结果，需自行轮询/回调。

```json
// Request
{"model": "string (required)", "prompt": "string (required)", "params": {}, "callback_url": "string?"}

// Response.data
{"id": "task_id", "status": "pending"}
```

### POST /v1/videos/generations

视频生成（异步，调用 capabilities/text2video，返回 task_id）。

### GET /v1/tasks/:task_no

查询任务状态。

```json
// Response.data
{"task_id": "xxx", "status": "pending|processing|completed|failed", "progress": 50, "result": {}, "error": "", "cost": "0.10"}
```

### POST /v1/tasks/:task_no/cancel

取消任务。返回 `{"message": "task cancelled"}`。

---

## 控制台 API (JWT 认证)

### GET /api/user/me

获取当前用户信息。

### PUT /api/user/password

修改密码。

### Token 管理

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/tokens | 列出我的 Token |
| POST | /api/tokens | 创建 Token |
| GET | /api/tokens/:id | 获取 Token 详情 |
| PUT | /api/tokens/:id | 更新 Token |
| DELETE | /api/tokens/:id | 删除 Token |
| POST | /api/tokens/:id/recharge | Token 充值 |

```json
// POST /api/tokens Request
{"name": "string (required, max 50)", "balance": "0.00", "channel_priorities": [{"capability_code": "str", "channel_id": 1, "priority": 1}]}

// PUT /api/tokens/:id Request
{"name": "string (max 50)", "channel_priorities": [...]}

// POST /api/tokens/:id/recharge Request
{"amount": "10.00"}
```

### 仪表盘

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/dashboard/stats | 统计概览 (today/weekly_trend/capability_dist) |
| GET | /api/dashboard/chat-stats | Chat 统计 |
| GET | /api/tasks | 任务列表 |
| GET | /api/tasks/:task_no | 任务详情 |

### 对话记录

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/conversations | 对话列表 |
| GET | /api/conversations/:id/messages | 对话消息 |

### 查询

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/capability-channels | 能力渠道列表 |
| GET | /api/chat-model-channels | Chat 模型渠道映射 |
| GET | /api/docs/models | 模型文档 |

### Playground (通过 JWT + token_id 代理)

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/playground/:token_id/models | 模型列表 |
| GET | /api/playground/:token_id/capabilities | 能力列表 |
| POST | /api/playground/:token_id/chat/completions | Chat 代理 |
| POST | /api/playground/:token_id/upload | 文件上传 |
| POST | /api/playground/:token_id/capabilities/:capability | 能力调用 |
| GET | /api/playground/:token_id/conversations | 对话列表 |
| GET | /api/playground/:token_id/conversations/:conversation_id/messages | 对话消息 |
| GET | /api/playground/:token_id/debug/:request_log_id | 调试信息 |
| GET | /api/playground/:token_id/tasks | 任务列表 |
| GET | /api/playground/:token_id/tasks/:task_no | 任务详情 |
| POST | /api/playground/:token_id/tasks/:task_no/cancel | 取消任务 |

---

## 管理员 API (JWT + Admin)

### 用户管理

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/admin/users | 用户列表 |
| PUT | /api/admin/users/:id/role | 修改角色 `{"role": "admin|user"}` |
| PUT | /api/admin/users/:id/status | 修改状态 `{"status": 0|1}` |
| POST | /api/admin/users/:id/recharge | 充值 `{"amount": "10.00"}` |

### 渠道管理

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/admin/channels | 渠道列表 |
| GET | /api/admin/channels/:id | 渠道详情 |
| POST | /api/admin/channels | 创建渠道 |
| PUT | /api/admin/channels/:id | 更新渠道 |
| DELETE | /api/admin/channels/:id | 删除渠道 |

### 渠道账号管理

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/admin/channel-accounts | 账号列表 |
| GET | /api/admin/channel-accounts/:id | 账号详情 |
| POST | /api/admin/channel-accounts | 创建账号 |
| PUT | /api/admin/channel-accounts/:id | 更新账号 |
| DELETE | /api/admin/channel-accounts/:id | 删除账号 |

### 能力管理

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/admin/capabilities | 能力列表 |
| GET | /api/admin/capabilities/:code | 能力详情 |
| POST | /api/admin/capabilities | 创建能力 |
| PUT | /api/admin/capabilities/:code | 更新能力 |
| DELETE | /api/admin/capabilities/:code | 删除能力 |

### 渠道能力配置

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/admin/channel-capabilities | 列表 |
| GET | /api/admin/channel-capabilities/:id | 详情 |
| POST | /api/admin/channel-capabilities | 创建 |
| PUT | /api/admin/channel-capabilities/:id | 更新 |
| DELETE | /api/admin/channel-capabilities/:id | 删除 |

### 请求日志

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/admin/request-logs | 日志列表 |
| GET | /api/admin/request-logs/:id | 日志详情 |
| POST | /api/admin/request-logs/:id/retry | 重试请求 |

### Chat 模型管理

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/admin/chat-models | 模型列表 |
| GET | /api/admin/chat-models/presets | 预设模型 |
| POST | /api/admin/chat-models/quick-setup | 快速配置 |
| GET | /api/admin/chat-models/:code | 模型详情 |
| POST | /api/admin/chat-models | 创建模型 |
| PUT | /api/admin/chat-models/:code | 更新模型 |
| DELETE | /api/admin/chat-models/:code | 删除模型 |

### Chat 模型渠道映射

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/admin/chat-model-channels | 映射列表 |
| GET | /api/admin/chat-model-channels/:id | 映射详情 |
| POST | /api/admin/chat-model-channels | 创建映射 |
| PUT | /api/admin/chat-model-channels/:id | 更新映射 |
| DELETE | /api/admin/chat-model-channels/:id | 删除映射 |

### 模型发现

| Method | Path | 说明 |
|--------|------|------|
| POST | /api/admin/discovery/sync | 同步所有渠道模型 |
| POST | /api/admin/discovery/sync/:channel_id | 同步指定渠道 |
| GET | /api/admin/discovery/pending | 待审核模型列表 |
| POST | /api/admin/discovery/approve | 批准模型 |
| POST | /api/admin/discovery/reject | 拒绝模型 |
| GET | /api/admin/models/:code/meta | 模型元信息 |

---

## 内部接口

### POST /internal/callback/:channel_type

上游渠道回调（任务完成通知）。需 Callback 签名认证。

---

## 系统接口

| Method | Path | 说明 |
|--------|------|------|
| GET | /health | 健康检查，返回 `{"status": "ok|degraded", "db": true, "redis": true}` |
| GET | /metrics | Prometheus 指标 |

---

## 错误码

| code | 含义 |
|------|------|
| 0 | 成功 |
| 400 | 参数错误 |
| 401 | 未认证 / Token 无效 |
| 403 | 权限不足 |
| 404 | 资源不存在 |
| 429 | 请求频率超限 (含 Retry-After header) |
| 500 | 服务器内部错误 |

---

## 速率限制

- `/v1/*` 路由受 Token 级别速率限制
- 触发限流返回 HTTP 429 + `Retry-After` header (秒)

---

## 注意事项

- `/v1/chat/completions` 和 `/v1/models` 完全兼容 OpenAI API 格式
- 多模态支持: messages.content 可为 string 或 ContentPart 数组
- 流式响应为标准 SSE 格式 (`data: {...}\n\n`，结束 `data: [DONE]\n\n`)
- Token key 格式: `sk-prism-{random_hex}`，存储时 SHA256 哈希
- 金额字段使用 decimal 字符串 (如 `"10.00"`)
