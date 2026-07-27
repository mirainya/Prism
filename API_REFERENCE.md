# Prism API Reference (Machine-Readable)

> AI Gateway 统一接口文档。供 AI Agent / 自动化工具集成使用。

## 基础信息

- Base URL: `http://{host}:{port}`
- `/api/*` 管理接口使用统一 JSON 包装:

```json
{"code": 0, "message": "success", "data": {...}}  // 成功
{"code": 400, "message": "error reason"}           // 失败
```

- `/v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1/files`、`/v1/models` 与图像生成接口直接返回兼容协议结构。能力、任务、渠道及视频生成接口使用 `{code,data}` 包装。
- `/v1/responses`、`/v1/files` 的错误使用 OpenAI Error 结构：`{"error":{"message":"...","type":"...","code":"..."}}`。
- 所有请求响应都会返回 `X-Request-ID`。模型调用在建立调用账本后还会返回 `X-Prism-Call-ID`。

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

### 对话保存与续接

`/v1/chat/completions`、`/v1/messages` 和 `/v1/responses` 都会把 canonical 输入及已产生的输出投影到 Conversation，并通过 `call_id` 关联调用记录。该投影覆盖成功、失败、取消和客户端中断；Responses 的 `store: false` 不会关闭投影。

三个端点都接受请求头 `X-Prism-Conversation-ID: <正整数>`，用于显式续接当前 Token 的 Prism 对话。Chat 还接受请求体 `conversation_id`，两者同时提供时必须一致。显式 ID 有效时，响应头返回同一 `X-Prism-Conversation-ID`；未显式提供 ID 时，Prism 可按 `previous_response_id` 或唯一完整历史前缀识别续话，但隐式匹配或新建对话不会返回该响应头。

Conversation 保存的是结构化 canonical 内容，不是原始 HTTP 正文。下游及上游原始 HTTP 请求/响应正文仅在启用 `observability.retain_api_call_payloads` 时写入 `api_call_payloads`，并按脱敏、限长、加密和过期策略处理。

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

> Google/Gemini Transport 当前仅接受 `/v1/responses` 下游，包括纯文本请求。Gemini 3 的 `thoughtSignature` 可附着于推理、函数调用、普通文本或媒体 Part；Chat 与 Messages 没有可移植的 proof 字段。Google Transport 当前不映射 Responses `reasoning` 控制项，传入该字段会返回 HTTP 400。

> Prism 使用 Responses `reasoning.encrypted_content` 中的版本信封承载非 OpenAI proof。Gemini 函数调用 carrier 按全局唯一 `call_id` 恢复，Google 普通 Part carrier 按唯一 `TargetID` 恢复到原消息 Part，均不依赖相邻位置。Anthropic `redacted_thinking` 的 opaque data 也通过版本信封保存，并且只会回放给原签发 Provider。

> 为避免无状态客户端在续轮时丢失 Provider proof，Prism 的转换响应会固定返回所需 `encrypted_content` carrier；`include:["reasoning.encrypted_content"]` 可传但不是必需条件。

> Prism 会管理公开的 `resp_` ID、存储和后台任务。无法无损转换的字段会返回 HTTP 400，不会静默丢弃。`file_search` 暂不支持。

Header：

| Header | 必填 | 说明 |
|--------|------|------|
| `Authorization: Bearer sk-prism-xxx` | 是 | API Token |
| `Idempotency-Key` | 否 | 相同 Token、Key 与请求体在 24 小时内返回同一响应；同 Key 不同请求体返回 400 |

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
- `Idempotency-Key` 同时支持 `store: true` 与 `store: false`。`store: false` 的短期幂等缓存不允许通过 `GET /v1/responses/:id` 查询，但仍保存 canonical Conversation 投影。
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

`callback_url` 可省略；提供时必须是主机解析结果全部为公网地址的 HTTP/HTTPS URL。本机、内网、链路本地、多播地址及不安全重定向会被拒绝。

```json
// Request
{
  "model": "string",
  "channel": "string?",
  "callback_url": "string?",
  "...params": "any"
}

// Response.data
{"task_id": "xxx", "status": "pending|processing|success"}
```

### POST /v1/images/generations

图片生成。**OpenAI 标准协议**，任何 OpenAI 图像客户端/SDK 可即插即用。
对外统一同步返图：同步渠道直接返回，异步渠道网关内部轮询等待（默认上限 300s）。
超过等待上限时，Prism 会取消内部任务、退回预授权费用，并返回 OpenAI 格式的 HTTP 504，不返回异步 `task_id`。

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

// response_format=b64_json 时
{
  "created": 1700000000,
  "data": [
    {"b64_json": "iVBORw0KGgo...", "revised_prompt": "..."}
  ]
}

// 超时 (HTTP 504)
{"error": {"message": "image generation timed out", "type": "api_error"}}
```

> `response_format` 由 Prism 响应层处理，`user` 仅作协议兼容保留；其余生成参数经渠道 `param_mapping`（含 `computed_params` 计算）映射后透传给上游。
> `response_format` 省略时默认为 `url`。使用 `b64_json` 时，Prism 会安全下载生成结果并编码，单张图片最大 32 MiB。
> 对接 sub2api 时，请求模型使用 `gpt-image-<Prism 模型 code>`。Prism 会先查完整模型名，查不到时再按去掉 `gpt-image-` 前缀后的模型 `code` 或 `vendor_model` 路由，无需复制模型或端点。

### POST /v1/images/edits

图生图。使用 OpenAI Images Edits multipart/form-data 协议，同步返回结构与 `/v1/images/generations` 相同。

```bash
curl -X POST "https://prism.example/v1/images/edits" \
  -H "Authorization: Bearer <API_KEY>" \
  -F "model=gpt-image-example" \
  -F "prompt=Edit this image" \
  -F "image=@input.png" \
  -F "size=1024x1024" \
  -F "response_format=b64_json"
```

字段：`model`、`prompt`、`image` 必填；`mask`、`n`、`size`、`aspect_ratio`、`quality`、`response_format`、`output_format`、`output_compression`、`moderation`、`style` 可选。支持 PNG、JPEG、WebP，单文件最大 20 MiB，请求体最大 40 MiB。

端点配置位于“能力配置 → 渠道能力配置 → 请求配置 → 参考图输入适配”：

- 上游接收图片 URL：选择“图片 URL”，填写上游字段（如 `image_urls`）。Prism 会先上传客户端文件，再发送 URL 数组。
- 上游接收文件：选择“Multipart 文件”，填写图片字段及编辑路径。Prism 会按 multipart/form-data 调用上游。
- 旧端点未设置 `input_mode` 时按 Multipart 模式处理。

### POST /v1/videos/generations

视频生成（异步，调用 capabilities/text2video，返回 task_id）。

### GET /v1/tasks/:task_no

查询任务状态。

```json
// Response.data
{"task_id": "xxx", "status": "pending|processing|success|failed|cancelled", "progress": 50, "result": {}, "error": "", "cost": "0.10"}
```

### POST /v1/tasks/:task_no/cancel

取消任务。`Response.data` 为 `{"message": "task cancelled"}`。

`tasks` 仅表示图片、视频等异步能力资源。所有协议和能力调用的统一历史、usage、计费与重试事实位于 `api_calls`；创建任务的响应头和控制台任务详情可通过 `call_id` 定位对应调用。

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

`POST /api/tokens` 仅在创建响应中返回一次完整 Token。列表与详情只返回后四位提示，服务端仅保存 SHA256 哈希。

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
| GET | /api/tasks | 异步任务列表 |
| GET | /api/tasks/:task_no | 异步任务详情，包含 `call_id` |

`GET /api/tasks` 支持 `page`、`page_size`、`status`、`capability`、`keyword`、日期和 `snapshot_at`。首次查询返回 `snapshot_at`；后续翻页应原样传回，修改筛选条件或刷新时应丢弃旧快照。列表项和详情均可通过 `call_id` 进入 `/api/calls?call_id=...`。

### 对话记录

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/conversations | 对话列表 |
| GET | /api/conversations/:id/messages | 旧消息兼容投影，独立分页 |
| GET | /api/conversations/:id/turns | canonical 对话轮次，独立分页 |

这些接口展示 Playground 及 `/v1/chat/completions`、`/v1/messages`、`/v1/responses` 保存的对话，但不替代完整的 API 调用历史；全部调用仍以 `/api/calls` 为准。数据按 `conversations -> conversation_turns -> conversation_items` 保存；`conversation_items` 保留有序 canonical 多模态、推理和工具内容。`/turns` 是新记录主查询，`/messages` 保留旧数据兼容，两者各自使用 `page/page_size`。Turn、Item 的 64 位 ID 与轮次序号以十进制字符串返回；`call_id` 可用于定位 `/api/calls`。

### 查询

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/capability-channels | 能力渠道列表 |
| GET | /api/docs/models | 模型文档 |

### 调用、审计与流水

普通用户只能查看自己的记录；管理员可通过 `user_id`、`token_id` 进一步筛选。

| Method | Path | 说明 |
|--------|------|------|
| GET | `/api/calls` | 全调用事实源；支持 Call、Request、模型、状态、日期等筛选 |
| GET | `/api/calls/:id` | Call、上游 Attempt、计费与管理员可见正文详情 |
| GET | `/api/observability/access-logs` | API 访问元数据，不保存认证头或请求正文 |
| GET | `/api/observability/audit-events` | 登录、Token、文件、取消及控制台写操作审计 |
| GET | `/api/observability/balance-entries` | 用户与 Token 的初始额度、历史余额基线、扣费、退款、充值和结算流水 |

`/api/calls/:id` 中的下游与上游原始 HTTP 正文来自可选的 `api_call_payloads` 捕获；默认不保存，且与始终生成的 canonical Conversation 投影相互独立。

三个观测接口都支持 `page`、`page_size`、`start_date`、`end_date`。访问日志另支持 `request_id`、`call_id`、`method`、`path`、`status_code`、`error_code`；审计事件支持 `action`、`resource_type`、`outcome`；余额流水支持 `account_type`、`direction`、`category`、`call_id`。

余额流水的 `category` 包括 `initial_credit`、`opening_balance`、`recharge`、`deduction`、`reservation`、`settlement`、`refund`。`opening_balance` 是升级迁移为历史非零余额建立的幂等基线，不代表一次新充值。

### Playground (通过 JWT + token_id 代理)

| Method | Path | 说明 |
|--------|------|------|
| GET | /api/playground/:token_id/models | 模型列表 |
| GET | /api/playground/:token_id/capabilities | 能力列表 |
| POST | /api/playground/:token_id/chat/completions | Chat 代理 |
| POST | /api/playground/:token_id/responses | Responses 代理 |
| POST | /api/playground/:token_id/messages | Anthropic Messages 代理 |
| POST | /api/playground/:token_id/upload | 文件上传 |
| POST | /api/playground/:token_id/capabilities/:capability | 能力调用 |
| GET | /api/playground/:token_id/conversations | 对话列表 |
| GET | /api/playground/:token_id/conversations/:conversation_id/messages | 对话消息 |
| GET | /api/playground/:token_id/conversations/:conversation_id/turns | canonical 对话轮次 |
| GET | /api/playground/:token_id/debug/:request_log_id | 调试信息 |
| GET | /api/playground/:token_id/tasks | 任务列表 |
| GET | /api/playground/:token_id/tasks/:task_no | 任务详情 |
| POST | /api/playground/:token_id/tasks/:task_no/cancel | 取消任务 |

Playground 任务列表与 `/api/tasks` 使用相同的 `snapshot_at` 稳定分页语义。任务详情和对话轮次中的 `call_id` 可直接用于调用记录筛选。

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

### Gateway V2 管理

所有路径均以 `/api/admin/gw` 开头。

| 资源 | 主要接口 |
|------|----------|
| Channels | `GET/POST /channels`、`GET/PUT/DELETE /channels/:id`、`POST /channels/reorder` |
| Keys | `GET /channels/:id/keys`、`POST /keys`、`PUT/DELETE /keys/:id` |
| 模型发现 | `GET /keys/:id/discover`、`POST /keys/:id/import` |
| Abilities | `GET /abilities`、`PUT/DELETE /abilities/:id` |
| Transports | `GET/PUT /abilities/:id/transports`、`DELETE /abilities/:id/transports/:transport`、`POST .../:transport/check` |
| Models | `GET /models`、`POST /models/reorder`、`DELETE /models?name=...` |
| Model Meta | `GET /model-meta`、`PUT/DELETE /model-meta/:model_name` |

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
- 请求未指定输出上限时，`4096` 仅用于计费预授权估算；实际最大输出由模型元数据、请求参数和上游共同决定
- 同一个公开模型可通过不同下游协议调用，前提是当前请求能由已启用 Transport 完整表达
- Token key 格式: `sk-prism-{random_hex}`，存储时 SHA256 哈希
- 金额字段使用 8 位精度 decimal 字符串；所有余额变化都有独立流水
- 密码、角色或用户状态变更会使该用户的旧 JWT 失效
