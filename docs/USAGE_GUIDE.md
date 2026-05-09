# Prism 使用教程

> 本文档是 Prism AI Gateway 平台的完整使用教程，涵盖部署、配置、管理后台操作和 API 调用。

---

## 目录

1. [环境准备](#1-环境准备)
2. [安装部署](#2-安装部署)
3. [配置说明](#3-配置说明)
4. [首次启动](#4-首次启动)
5. [管理后台使用](#5-管理后台使用)
   - 5.1 [登录与用户管理](#51-登录与用户管理)
   - 5.2 [渠道管理](#52-渠道管理)
   - 5.3 [能力管理](#53-能力管理)
   - 5.4 [Chat 模型管理](#54-chat-模型管理)
   - 5.5 [Token 管理](#55-token-管理)
   - 5.6 [日志与监控](#56-日志与监控)
6. [API 调用指南](#6-api-调用指南)
   - 6.1 [认证方式](#61-认证方式)
   - 6.2 [图片生成](#62-图片生成)
   - 6.3 [视频生成](#63-视频生成)
   - 6.4 [任务查询](#64-任务查询)
   - 6.5 [Chat 对话](#65-chat-对话)
   - 6.6 [定价查询](#66-定价查询)
7. [路由策略与负载均衡](#7-路由策略与负载均衡)
8. [计费系统](#8-计费系统)
9. [异步任务流程](#9-异步任务流程)
10. [常见问题](#10-常见问题)

---

## 1. 环境准备

### 必需组件

| 组件 | 版本要求 | 用途 |
|------|---------|------|
| MySQL | 5.7+ | 数据存储 |
| Redis | 6.0+ | 缓存、队列、限流 |

### 可选组件

| 组件 | 用途 |
|------|------|
| 腾讯云 COS | 生成结果文件存储（图片/视频上传） |

### 开发环境额外需要

| 组件 | 版本要求 |
|------|---------|
| Go | 1.25+ |
| Node.js | 18+ |
| npm | 9+ |

---

## 2. 安装部署

### 方式一：使用编译好的二进制（推荐）

```bash
# 1. 将 dist/ 目录上传到服务器
scp -r dist/ user@server:/opt/prism/

# 2. 进入目录
cd /opt/prism

# 3. 复制并修改配置
cp configs/config.example.yaml configs/config.yaml
vim configs/config.yaml

# 4. 初始化数据库（首次部署）
# 创建数据库
mysql -u root -p -e "CREATE DATABASE prism CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
# 导入初始数据（创建管理员账号）
mysql -u root -p prism < configs/init.sql

# 5. 启动服务
chmod +x prism
./prism
```

### 方式二：从源码构建

```bash
# 1. 克隆项目
git clone https://github.com/mirainya/Prism.git
cd prism

# 2. Windows 下构建（输出 Linux AMD64 二进制）
build.bat

# 3. 或手动构建
cd console && npm install && npm run build && cd ..
set GOOS=linux
set GOARCH=amd64
go build -o dist/prism cmd/server/main.go
```

### 方式三：Docker 部署

```bash
# 1. 复制 Docker 配置
cp configs/config.docker.yaml configs/config.yaml

# 2. 启动（需自行准备 docker-compose.yml）
docker-compose up -d
```

> Docker 配置中 MySQL 主机名为 `mysql`，Redis 主机名为 `redis`，对应 Docker 服务名。

---

## 3. 配置说明

配置文件位于 `configs/config.yaml`，以下是完整配置项：

```yaml
# 服务配置
server:
  port: 23523                    # HTTP 监听端口
  jwt_secret: "your-secret-key"  # JWT 签名密钥（务必修改！）

# 数据库配置
database:
  host: 127.0.0.1
  port: 3306
  user: root
  password: 123456
  dbname: prism
  max_open_conns: 50             # 最大打开连接数
  max_idle_conns: 10             # 最大空闲连接数
  conn_max_lifetime: 3600        # 连接最大存活时间（秒）
  conn_max_idle_time: 300        # 空闲连接最大存活时间（秒）
  log_level: warn                # SQL 日志级别：info/warn/error/silent

# Redis 配置
redis:
  addr: 127.0.0.1:6379
  password: ""
  db: 0
  pool_size: 20                  # 连接池大小
  min_idle_conns: 5
  dial_timeout: 5                # 连接超时（秒）
  read_timeout: 3
  write_timeout: 3

# 对象存储（可选，用于保存生成的图片/视频）
storage:
  type: ""                       # 留空则不启用
  tencent:
    secret_id: ""
    secret_key: ""
    region: ""                   # 如 ap-guangzhou
    bucket: ""                   # 如 my-bucket-1250000000
    cdn: ""                      # CDN 域名（可选）

# 异步 Worker 配置
worker:
  concurrency: 10                # 并发 Worker 数
  poll_interval: 5s              # 轮询间隔
  max_retry: 3                   # 最大重试次数

# HTTP 客户端配置（调用上游 API 时使用）
http_client:
  timeout: 30                    # 请求超时（秒）
  max_idle_conns: 100
  max_idle_conns_per_host: 20
  idle_conn_timeout: 90

# 限流配置
rate_limit:
  enabled: false                 # 是否启用
  requests_per_min: 60           # 每分钟请求上限（按 Token）
```

### 关键配置提醒

- `jwt_secret`：生产环境必须修改为随机字符串
- `database`：确保 MySQL 已创建对应数据库
- `storage`：如果需要将生成结果上传到云存储，需配置腾讯云 COS
- `rate_limit`：建议生产环境开启，防止 API 滥用

---

## 4. 首次启动

### 启动流程

服务启动时会自动执行以下初始化：

1. 加载配置文件 `configs/config.yaml`
2. 初始化日志系统
3. 连接 MySQL 并自动建表（AutoMigrate）
4. 连接 Redis
5. 初始化任务队列（Asynq）
6. 启动异步 Worker
7. 启动定时任务（超时检测，每 5 分钟）
8. 启动 HTTP 服务

### 默认管理员账号

```
用户名：admin
密码：123456
```

> 首次登录后请立即修改密码！

### 验证启动

```bash
# 检查服务是否正常
curl http://localhost:23523/api/auth/login \
  -X POST \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"123456"}'
```

成功返回：
```json
{
  "code": 0,
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIs...",
    "user": {
      "id": 1,
      "username": "admin",
      "role": "admin"
    }
  }
}
```

---

## 5. 管理后台使用

浏览器访问 `http://your-server:23523` 进入管理后台。

### 5.1 登录与用户管理

#### 登录

在首页点击「登录」，输入用户名和密码。

#### 用户注册

支持自助注册，新用户默认角色为 `user`，需要管理员分配余额后才能使用 API。

#### 管理员操作

进入「用户管理」页面，管理员可以：

- **搜索用户**：按用户名搜索
- **修改角色**：将用户提升为管理员或降级为普通用户
- **启用/禁用**：禁用后用户无法登录
- **充值余额**：为用户账户充值，用于 API 调用扣费

#### 修改密码

点击右上角用户头像 → 「修改密码」，输入旧密码和新密码（至少 6 位）。

---

### 5.2 渠道管理

渠道代表一个外部 AI 服务提供商（如 Midjourney、Runway、Sora 等）。

#### 创建渠道

进入「渠道管理」页面，点击「新建渠道」：

| 字段 | 说明 | 示例 |
|------|------|------|
| 渠道类型 | 唯一标识，不可重复 | `midjourney`、`runway`、`sora` |
| 渠道名称 | 显示名称 | `Midjourney 官方` |
| Base URL | 上游 API 地址 | `https://api.midjourney.com` |
| 配置 (JSON) | 额外配置 | `{}` |

#### 管理渠道账号

每个渠道下可以添加多个账号，用于负载均衡。

点击渠道行展开 → 「账号列表」→ 「添加」：

| 字段 | 说明 | 示例 |
|------|------|------|
| 账号名称 | 标识名 | `主账号` |
| API Key | 上游服务的密钥 | `sk-xxx...` |
| 权重 | 负载均衡权重（1-100） | `10` |
| 最大并发数 | 同时处理的任务上限，0=不限制 | `5` |
| 账号配置 (JSON) | 额外配置 | `{}` |

#### 账号状态说明

- 账号列表显示格式：`并发: 当前/上限`，如 `并发: 3/5`
- 上限为 0 时显示 `并发: 0/∞`（不限制）
- 当并发达到上限时，文字变红色，该账号暂时不会被选中

---

### 5.3 能力管理

能力（Capability）是 Prism 的核心概念，代表一种标准化的 AI 功能。

#### 创建能力

进入「能力管理」页面，点击「新建能力」：

| 字段 | 说明 | 示例 |
|------|------|------|
| 能力代码 | 唯一标识 | `text-to-image` |
| 能力名称 | 显示名称 | `文生图` |
| 类型 | image / video / chat / other | `image` |
| 描述 | 功能说明 | `根据文本描述生成图片` |
| 标准参数 (JSON) | 定义该能力接受的参数格式 | 见下方 |
| 标准响应 (JSON) | 定义该能力返回的结果格式 | 见下方 |

标准参数示例：
```json
{
  "prompt": { "type": "string", "required": true, "description": "生成提示词" },
  "width": { "type": "integer", "default": 1024 },
  "height": { "type": "integer", "default": 1024 }
}
```

标准响应示例：
```json
{
  "image_url": { "type": "string", "description": "生成的图片地址" }
}
```

#### 配置渠道能力

能力本身只是定义，需要将能力绑定到具体渠道才能使用。

在能力管理页面，点击能力行展开 → 「渠道能力」→ 「添加」：

| 字段 | 说明 | 示例 |
|------|------|------|
| 渠道 | 选择已创建的渠道 | `Midjourney 官方` |
| 模型 | 上游模型标识 | `midjourney-v6` |
| 名称 | 显示名称 | `MJ V6` |
| 单价 | 每次调用费用 | `0.1` |
| 计价单位 | 费用单位 | `次` |
| 结果模式 | sync / poll / callback | 见下方说明 |

**结果模式说明：**

| 模式 | 说明 | 适用场景 |
|------|------|---------|
| `sync` | 同步返回结果 | 响应快的接口（<30s） |
| `poll` | 提交后轮询状态 | Midjourney、Sora 等异步生成 |
| `callback` | 提交后等待回调 | 支持 Webhook 的服务 |

**轮询模式额外配置：**

| 字段 | 说明 | 示例 |
|------|------|------|
| 轮询路径 | 查询任务状态的 API 路径 | `/v1/tasks/{task_id}` |
| 轮询方法 | HTTP 方法 | `GET` |
| 轮询间隔 | 每次轮询间隔（秒） | `5` |
| 最大轮询次数 | 超过则标记超时 | `120` |
| 轮询参数映射 | 轮询请求的参数映射 | `{}` |
| 轮询响应映射 | 轮询响应的字段映射 | `{}` |

**认证配置：**

| 字段 | 说明 | 示例 |
|------|------|------|
| 认证位置 | header / body / query | `header` |
| 认证 Key | 认证字段名 | `Authorization` |
| 认证值前缀 | 拼接在 API Key 前 | `Bearer ` |

**参数映射 (JSON)：**

参数映射定义了如何将 Prism 标准参数转换为上游 API 的参数格式：

```json
{
  "field_mapping": {
    "prompt": "input.prompt",
    "width": "input.image_size.width",
    "height": "input.image_size.height"
  },
  "value_mapping": {
    "model": {
      "sd-xl": "stable-diffusion-xl-1024-v1-0"
    }
  },
  "computed": {
    "size": "{width}x{height}"
  }
}
```

**响应映射 (JSON)：**

响应映射定义了如何从上游 API 响应中提取结果：

```json
{
  "task_id": "data.task_id",
  "status": "data.status",
  "progress": "data.progress",
  "image_url": "data.output.image_url",
  "status_mapping": {
    "PENDING": "pending",
    "PROCESSING": "processing",
    "SUCCESS": "success",
    "FAILED": "failed"
  },
  "success_condition": "status == success"
}
```

---

### 5.4 Chat 模型管理

Chat 模型管理用于配置 OpenAI 兼容的对话接口。

#### 创建 Chat 模型

进入「Chat 模型」页面，点击「新建模型」：

| 字段 | 说明 | 示例 |
|------|------|------|
| 模型代码 | 唯一标识，用户调用时使用 | `gpt-4o` |
| 模型名称 | 显示名称 | `GPT-4o` |
| 供应商 | 模型提供商 | `OpenAI` |
| 描述 | 模型说明 | `最新的 GPT-4o 模型` |

支持的供应商：OpenAI、Anthropic (Claude)、Google (Gemini)、DeepSeek、通义千问、Moonshot

#### 配置模型-渠道映射

进入「模型渠道」页面，点击「新建映射」：

| 字段 | 说明 | 示例 |
|------|------|------|
| 模型 | 选择已创建的 Chat 模型 | `gpt-4o` |
| 渠道 | 选择已创建的渠道 | `OpenAI 官方` |
| 上游模型名 | 实际传给上游的模型名 | `gpt-4o-2024-08-06` |
| 优先级 | 数字越大优先级越高 | `10` |
| 计价模式 | token（按量）/ request（按次） | `token` |
| 输入价格 | 每百万 Token 价格 | `2.50` |
| 输出价格 | 每百万 Token 价格 | `10.00` |
| 请求路径 | 上游 API 路径 | `/v1/chat/completions` |
| 超时时间 | 请求超时（秒） | `60` |
| 额外请求头 (JSON) | 附加的 HTTP 头 | `{}` |
| 额外配置 (JSON) | 其他配置 | `{}` |

> 同一个模型可以映射到多个渠道，系统会按优先级选择可用渠道。

---

### 5.5 Token 管理

Token 是用户调用 API 的凭证，每个 Token 有独立的余额和权限。

#### 创建 Token

进入「Token 管理」页面，点击「新建 Token」：

| 字段 | 说明 | 示例 |
|------|------|------|
| 名称 | Token 标识名 | `生产环境 Key` |

创建后会生成一个 `sk-prism-xxx` 格式的 API Key，**只显示一次**，请妥善保存。

#### Token 充值

点击 Token 行的「充值」按钮，输入金额即可。Token 余额和用户余额独立计算，调用 API 时两者都会扣费。

#### 渠道优先级配置

点击 Token 行的「优先级」按钮，可以为该 Token 配置特定能力的渠道优先级：

```
能力: text-to-image
  渠道 A → 优先级 10（优先使用）
  渠道 B → 优先级 5（备选）
```

> 如果不配置优先级，系统使用默认的加权随机策略。

---

### 5.6 日志与监控

#### 仪表盘

「仪表盘」页面展示：

- **今日统计**：总请求数、总费用、成功/失败数、错误率
- **趋势对比**：与昨日的请求量和费用对比
- **周趋势图**：最近 7 天的请求量、费用、错误数折线图
- **能力分布**：各能力的调用占比饼图

#### 调用日志

「调用日志」页面展示所有任务的执行记录：

- 按能力、状态筛选
- 按任务编号搜索（Enter 触发）
- 点击任务查看详情：原始参数、供应商响应、最终结果

#### 对话记录

「对话记录」页面展示所有 Chat 对话：

- 按模型筛选
- 查看每条消息的 Token 用量、延迟、费用
- 管理员可查看所有用户的对话

#### 渠道请求日志

「请求日志」页面（管理员）展示底层 HTTP 请求详情：

- 按渠道、能力、请求类型（submit/poll/callback/chat）筛选
- 查看完整的请求头、请求体、响应体
- 支持「重试」功能：对失败的请求重新发送

---

## 6. API 调用指南

### 6.1 认证方式

所有 `/v1/*` 接口使用 API Key 认证：

```bash
Authorization: Bearer sk-prism-your-api-key
```

认证流程：
1. API Key 经 SHA256 哈希后与数据库匹配
2. Token 信息缓存在 Redis（5 分钟 TTL）
3. Token 必须为启用状态且余额充足

错误响应：
```json
{
  "code": 401,
  "message": "invalid token"
}
```

### 限流

启用限流后，每个 Token 有独立的请求频率限制（默认 60 次/分钟）。

响应头：
- `X-RateLimit-Limit`：最大请求数
- `X-RateLimit-Remaining`：剩余请求数

超限返回：
```json
{
  "code": 429,
  "message": "rate limit exceeded, please try again later"
}
```

---

### 6.2 图片生成

```bash
curl http://your-server:23523/v1/images/generations \
  -X POST \
  -H "Authorization: Bearer sk-prism-your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "capability": "text-to-image",
    "params": {
      "prompt": "a beautiful sunset over the ocean",
      "width": 1024,
      "height": 1024
    },
    "callback_url": "https://your-server.com/callback"
  }'
```

成功响应：
```json
{
  "code": 0,
  "data": {
    "task_no": "task_1709123456_a1b2c3d4",
    "status": "pending"
  }
}
```

> `callback_url` 可选。如果不提供，需要主动轮询任务状态。

---

### 6.3 视频生成

```bash
curl http://your-server:23523/v1/videos/generations \
  -X POST \
  -H "Authorization: Bearer sk-prism-your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "capability": "text-to-video",
    "params": {
      "prompt": "a cat playing piano",
      "duration": 5
    }
  }'
```

响应格式与图片生成相同。

---

### 6.4 任务查询

提交任务后，通过任务编号查询状态：

```bash
curl http://your-server:23523/v1/tasks/task_1709123456_a1b2c3d4 \
  -H "Authorization: Bearer sk-prism-your-api-key"
```

响应：
```json
{
  "code": 0,
  "data": {
    "task_no": "task_1709123456_a1b2c3d4",
    "status": "success",
    "progress": 100,
    "result": {
      "image_url": "https://cdn.example.com/output/image.png"
    },
    "cost": "0.10",
    "created_at": "2026-03-13T10:00:00Z",
    "completed_at": "2026-03-13T10:00:30Z"
  }
}
```

**任务状态流转：**

```
pending → processing → success
                    → failed（自动退款）
         → cancelled
```

| 状态 | 说明 |
|------|------|
| `pending` | 已创建，等待处理 |
| `processing` | 正在处理中 |
| `success` | 处理成功，结果已就绪 |
| `failed` | 处理失败，费用自动退还 |
| `cancelled` | 用户取消 |

---

### 6.5 Chat 对话

Prism 提供 OpenAI 兼容的 Chat 接口：

```bash
curl http://your-server:23523/v1/chat/completions \
  -X POST \
  -H "Authorization: Bearer sk-prism-your-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "Hello!"}
    ],
    "temperature": 0.7,
    "max_tokens": 1000,
    "stream": false
  }'
```

响应（OpenAI 兼容格式）：
```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "model": "gpt-4o",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello! How can I help you today?"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 20,
    "completion_tokens": 10,
    "total_tokens": 30
  }
}
```

**支持流式输出：**

设置 `"stream": true`，响应为 SSE 格式：

```
data: {"id":"chatcmpl-xxx","choices":[{"delta":{"content":"Hello"}}]}
data: {"id":"chatcmpl-xxx","choices":[{"delta":{"content":"!"}}]}
data: [DONE]
```

**查询可用模型：**

```bash
curl http://your-server:23523/v1/models \
  -H "Authorization: Bearer sk-prism-your-api-key"
```

---

### 6.6 定价查询

公开接口，无需认证：

```bash
curl http://your-server:23523/api/public/pricing
```

返回所有能力及其渠道定价信息。

---

## 7. 路由策略与负载均衡

Prism 使用两级路由策略：

### 第一级：选择渠道能力

当用户请求某个能力时，系统从所有启用的渠道能力中选择：

1. 检查 Token 是否配置了渠道优先级
2. 如果有优先级配置，按优先级排序选择
3. 如果没有，使用**加权随机**算法：权重越高，被选中概率越大

### 第二级：选择账号

选定渠道后，从该渠道的账号中选择：

1. 过滤条件：
   - 账号状态为启用
   - 未达到并发上限（`max_tasks = 0` 或 `current_tasks < max_tasks`）
2. 选择策略：选择当前任务数最少的账号（负载最低）
3. 使用**行锁**保证并发安全，避免多个请求同时选中同一账号
4. 选中后 `current_tasks + 1`，任务完成后 `current_tasks - 1`

### 示例场景

```
能力: text-to-image
├── 渠道 A（权重 10）
│   ├── 账号 A1（并发: 3/5）
│   └── 账号 A2（并发: 1/5）  ← 优先选择（负载最低）
└── 渠道 B（权重 5）
    └── 账号 B1（并发: 0/∞）
```

渠道 A 被选中的概率是渠道 B 的 2 倍（10:5）。选中渠道 A 后，账号 A2 会被优先选择（当前任务数更少）。

---

## 8. 计费系统

### 扣费流程

1. 用户发起请求
2. 系统根据渠道能力的单价计算费用
3. **预扣费**：同时扣减 Token 余额和用户余额
4. 任务执行
5. 如果任务失败 → **自动退款**

### 幂等保证

- 每次扣费/退款携带幂等键（基于任务 ID）
- 数据库唯一索引防止重复扣费
- 事务保证 Token 余额和用户余额一致性

### 余额关系

```
用户余额 ≥ Token 余额（逻辑上）
调用 API → 同时扣减 Token 余额 + 用户余额
Token 充值 → 只增加 Token 余额（不影响用户余额）
用户充值 → 只增加用户余额（不影响 Token 余额）
```

> Token 余额不足或用户余额不足都会导致请求失败。

### Chat 计费

Chat 接口按 Token 用量计费：

```
费用 = (输入 Token 数 × 输入价格 + 输出 Token 数 × 输出价格) / 1,000,000
```

---

## 9. 异步任务流程

图片/视频生成是异步的，完整流程如下：

```
┌─────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐
│ 用户请求 │ ──→ │ 预扣费    │ ──→ │ 创建任务  │ ──→ │ 入队列   │
└─────────┘     └──────────┘     └──────────┘     └──────────┘
                                                        │
                                                        ▼
┌─────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐
│ 返回结果 │ ←── │ 文件上传  │ ←── │ 结果解析  │ ←── │ Worker   │
│ (或回调) │     │ (COS)    │     │          │     │ 提交任务  │
└─────────┘     └──────────┘     └──────────┘     └──────────┘
                                                        │
                                                   ┌────┴────┐
                                                   │ 轮询/回调 │
                                                   └─────────┘
```

### 详细步骤

1. **用户提交请求** → 认证、限流、参数校验
2. **路由选择** → 选渠道能力 + 选账号
3. **参数映射** → 将标准参数转换为上游格式
4. **预扣费** → 扣减 Token 和用户余额
5. **创建任务** → 状态 `pending`，入 Asynq 队列
6. **Worker 提交** → 调用上游 API，状态变为 `processing`
7. **等待结果**：
   - **轮询模式**：Worker 定期查询上游状态
   - **回调模式**：等待上游 Webhook 通知
8. **结果处理** → 响应映射提取结果
9. **文件上传** → 下载生成文件并上传到 COS（如果配置了）
10. **完成** → 状态变为 `success`，通知用户（如果有 callback_url）
11. **失败处理** → 状态变为 `failed`，自动退款

### 超时检测

系统每 5 分钟检查一次，将超过 30 分钟仍在 `processing` 状态的任务标记为失败并退款。

---

## 10. 常见问题

### Q: 启动报错 "connect to database failed"

检查 `configs/config.yaml` 中的数据库配置：
- MySQL 是否已启动
- 数据库是否已创建
- 用户名密码是否正确
- 如果是 Docker 部署，主机名应为服务名（如 `mysql`）

### Q: 启动报错 "connect to redis failed"

检查 Redis 配置：
- Redis 是否已启动
- 地址和端口是否正确
- 如果有密码，是否已配置

### Q: API 调用返回 401

- 检查 Token 是否已创建且状态为启用
- 检查 Authorization 头格式：`Bearer sk-prism-xxx`
- 检查 Token 余额是否充足

### Q: API 调用返回 "no available channel"

- 检查是否已创建渠道并添加账号
- 检查渠道和账号是否为启用状态
- 检查是否已配置渠道能力
- 检查账号是否达到并发上限

### Q: 任务一直处于 pending 状态

- 检查 Redis 是否正常（Asynq 依赖 Redis）
- 检查日志中是否有 Worker 错误
- 确认 `worker.concurrency` 配置大于 0

### Q: 任务失败但没有退款

- 检查日志中是否有 "refund failed" 错误
- 确认 Token 和用户记录未被删除
- 可在数据库 `billing_logs` 表中查看计费流水

### Q: 生成的图片/视频链接无法访问

- 如果配置了 COS，检查存储配置是否正确
- 如果未配置 COS，返回的是上游原始链接，可能有时效性
- 建议生产环境配置对象存储

### Q: Chat 接口返回 "model not found"

- 检查是否已创建 Chat 模型
- 检查是否已配置模型-渠道映射
- 检查映射的渠道和账号是否启用
- 确认请求中的 `model` 字段与模型代码一致

### Q: 如何对接新的 AI 服务？

1. 创建渠道（填写 Base URL）
2. 添加账号（填写 API Key）
3. 创建能力（定义标准参数和响应）
4. 配置渠道能力（设置参数映射、响应映射、认证方式）
5. 测试调用

关键在于参数映射和响应映射的配置，它们决定了 Prism 如何与上游 API 交互。
