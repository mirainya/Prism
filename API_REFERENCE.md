# Prism API Reference (Machine-Readable)

> AI Gateway 统一接口文档。供 AI Agent / 自动化工具集成使用。

## 基础信息

- Base URL: `http://{host}:{port}`
- `/api/*` 管理接口使用统一 JSON 包装:

```json
{"code": 0, "message": "success", "data": {...}}  // 成功
{"code": 400, "message": "error reason"}           // 失败
```

- `/v1/*` 兼容接口直接返回对应协议结构，不使用 `{code,data}` 包装。
- `/v1/responses`、`/v1/files` 的错误使用 OpenAI Error 结构：`{"error":{"message":"...","type":"...","code":"..."}}`。

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

### POST /v1/messages

Anthropic Messages 兼容接口。支持文本、图片、文档、工具调用及 Anthropic SSE 事件格式。

```json
{
  "model": "claude-sonnet-4-20250514",
  "max_tokens": 1024,
  "messages": [{"role": "user", "content": [{"type": "text", "text": "你好"}]}],
  "system": "string 或 ContentBlock[]",
  "tools": [{"name": "tool_name", "description": "...", "input_schema": {"type": "object"}}],
  "tool_choice": {"type": "auto"},
  "stream": false
}
```

认证可使用 `Authorization: Bearer sk-prism-xxx` 或 Anthropic SDK 的 `x-api-key: sk-prism-xxx`。

### POST /v1/responses

对下游开放的 OpenAI Responses 兼容接口。支持多模态输入、函数工具、流式输出、续话、后台执行、存储和幂等请求。

上游调用方式由每个模型能力的 `gw_ability_transports` 配置决定：

| Transport | 上游路径 | 处理方式 |
|-----------|----------|----------|
| `openai_chat` | `/v1/chat/completions` | 无损范围内转换 |
| `openai_responses` | `/v1/responses` | 原生 Responses |
| `anthropic_messages` | `/v1/messages` | 无损范围内转换 |
| `google_generate_content` | Gemini GenerateContent | 无损范围内转换 |
| `volcengine_responses_v3` | `/api/v3/responses` | 火山方舟原生 Responses v3 |

> Prism 会管理公开的 `resp_` ID、存储和后台任务。无法无损转换的字段会返回 HTTP 400，不会静默丢弃。`file_search` 暂不支持。

Header：

| Header | 必填 | 说明 |
|--------|------|------|
| `Authorization: Bearer sk-prism-xxx` | 是 | API Token |
| `Idempotency-Key` | 否 | 相同 Token、Key 与请求体返回同一响应；同 Key 不同请求体返回 400 |

```json
// Request
{
  "model": "string (required)",
  "input": "string 或 InputItem[] (required)",
  "instructions": "string?",
  "stream": false,
  "store": true,
  "background": false,
  "previous_response_id": "resp_xxx?",
  "tools": [],
  "tool_choice": "auto|none|required|object",
  "parallel_tool_calls": true,
  "max_output_tokens": 4096,
  "max_tool_calls": 10,
  "temperature": 0.7,
  "top_p": 1.0,
  "top_logprobs": 0,
  "reasoning": {},
  "thinking": {},
  "caching": {},
  "text": {},
  "conversation": null,
  "prompt": null,
  "stream_options": null,
  "context_management": null,
  "metadata": {},
  "include": [],
  "truncation": "auto",
  "service_tier": "auto",
  "prompt_cache_key": "string?",
  "prompt_cache_retention": "string?",
  "safety_identifier": "string?",
  "user": "string?",
  "expire_at": 1900000000,
  "session": {"id": "session_xxx"}
}
```

多模态输入示例：

```json
{
  "model": "doubao-seed-2-0-pro",
  "input": [{
    "role": "user",
    "content": [
      {"type": "input_text", "text": "描述这张图片和附件"},
      {"type": "input_image", "image_url": "https://example.com/image.jpg"},
      {"type": "input_video", "video_url": "https://example.com/video.mp4", "fps": 1},
      {"type": "input_file", "file_id": "file_xxx"}
    ]
  }],
  "store": true
}
```

- `stream: true`：返回 Responses SSE 事件流。
- `background: true`：立即返回 `queued`；通过 `GET /v1/responses/:id` 查询。不能同时启用 `stream`，且 `store` 必须为 `true`。
- `previous_response_id`：必须使用同一 Token 创建且已保存的 Prism `resp_` ID。
- `file_id`：必须属于当前 Token；Prism 会在调用上游前解析文件内容。
- 火山扩展：支持 `thinking`、`caching`、`expire_at`、`session`，以及 `function`、`web_search`、`image_process`、`mcp`、`knowledge_search`、`doubao_app` 工具。
- 火山不支持 `conversation`、`prompt`、`stream_options`、`top_logprobs`、`metadata`、`truncation`、`prompt_cache_retention`、`user`；选择火山通道时这些字段返回 400。
- 火山请求、响应和 `usage` 的未知扩展字段会原样保留，不会在 Prism 重新编码时丢失。

### Responses 资源接口

| Method | Path | 说明 |
|--------|------|------|
| GET | `/v1/responses/:id` | 查询已保存响应 |
| DELETE | `/v1/responses/:id` | 删除响应 |
| POST | `/v1/responses/:id/cancel` | 取消后台响应 |
| GET | `/v1/responses/:id/input_items` | 获取输入项；支持 `limit=1..100`、`order=asc|desc`、`after` |

### POST /v1/files

上传供 Responses 多模态输入使用的文件。请求类型必须为 `multipart/form-data`。

```bash
curl "$BASE_URL/v1/files" \
  -H "Authorization: Bearer sk-prism-xxx" \
  -F "purpose=vision" \
  -F "file=@./image.png"
```

```json
// Response
{
  "id": "file_xxx",
  "object": "file",
  "bytes": 1024,
  "created_at": 1700000000,
  "filename": "image.png",
  "purpose": "vision",
  "status": "processed"
}
```

- 单文件最大 `64 MiB`。
- 默认每个 Token 总配额 `1 GiB`，可通过服务端配置调整。
- `purpose` 支持 `assistants`、`batch`、`evals`、`fine-tune`、`user_data`、`vision`；默认 `assistants`。
- 文件仅能由创建它的 API Token 查询、下载、引用或删除。

### Files 资源接口

| Method | Path | 说明 |
|--------|------|------|
| GET | `/v1/files` | 文件列表；支持 `purpose`、`limit=1..10000`、`order=asc|desc`、`after` |
| GET | `/v1/files/:id` | 获取文件信息 |
| GET | `/v1/files/:id/content` | 下载文件内容 |
| DELETE | `/v1/files/:id` | 删除文件 |

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

- `/v1/chat/completions`、`/v1/responses`、`/v1/messages` 和 `/v1/models` 分别返回对应协议的兼容格式
- 多模态支持: messages.content 可为 string 或 ContentPart 数组
- 流式响应为标准 SSE 格式 (`data: {...}\n\n`，结束 `data: [DONE]\n\n`)
- Token key 格式: `sk-prism-{random_hex}`，存储时 SHA256 哈希
- 金额字段使用 decimal 字符串 (如 `"10.00"`)
