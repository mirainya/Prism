# Prism 项目地图

> Prism 是一个轻量级 AI Gateway 平台，统一管理多渠道 AI 服务（Midjourney、Runway、Sora 等），提供标准化 API、智能路由、异步任务管理、计费系统和 Web 管理后台。

## 技术栈

| 层级 | 技术 |
|------|------|
| 后端框架 | Go 1.25 + Gin |
| ORM | GORM + MySQL |
| 缓存/队列 | Redis + Asynq |
| 认证 | JWT (控制台) + API Key (外部接口) |
| 日志 | Zap |
| 配置 | Viper (YAML) |
| 对象存储 | 腾讯云 COS |
| 前端框架 | React 19 + TypeScript |
| 构建工具 | Vite |
| 样式 | Tailwind CSS 4 |
| 图表 | Recharts |
| 部署 | 单二进制（前端 embed 到 Go 中） |

---

## 目录结构总览

```
Prism/
├── cmd/server/              # 应用入口
├── internal/                # 核心业务逻辑
│   ├── api/                 #   路由 & HTTP 处理
│   │   ├── middleware/      #     中间件
│   │   └── v1/              #     API 处理器
│   ├── model/               #   数据库模型
│   ├── service/             #   业务逻辑层
│   ├── provider/            #   AI 供应商适配器
│   │   └── chat/            #     Chat 模型适配器
│   └── worker/              #   异步任务处理
├── pkg/                     # 可复用工具包
│   ├── auth/                #   JWT & 密码
│   ├── cache/               #   Redis 缓存
│   ├── config/              #   配置管理
│   ├── database/            #   数据库连接
│   ├── errors/              #   错误定义
│   ├── httputil/            #   HTTP 客户端
│   ├── logger/              #   日志
│   ├── metrics/             #   指标采集
│   ├── queue/               #   任务队列
│   └── storage/             #   对象存储
├── console/                 # React 前端
│   ├── components/          #   公共组件
│   ├── pages/               #   页面组件
│   ├── services/            #   API 客户端
│   └── assets/              #   静态资源
├── configs/                 # 配置文件
├── docs/                    # 文档
└── dist/                    # 构建产物
```

---

## 根目录文件

| 文件 | 说明 |
|------|------|
| `go.mod` / `go.sum` | Go 模块定义和依赖锁定 |
| `build.bat` | 构建脚本：编译前端 → 编译 Go 二进制（Linux AMD64）→ 输出到 dist/ |
| `README.md` | 项目介绍、功能特性、部署说明 |
| `.gitignore` | Git 忽略规则 |

---

## 后端文件清单

### cmd/ — 应用入口

| 文件 | 说明 |
|------|------|
| `cmd/server/main.go` | 主入口：初始化配置、数据库、缓存、队列、Worker、定时器、HTTP 服务，支持优雅关闭 |

### console/ — 前端嵌入

| 文件 | 说明 |
|------|------|
| `console/embed.go` | 使用 `//go:embed` 将前端 dist 嵌入 Go 二进制 |

### internal/api/ — HTTP 层

| 文件 | 说明 |
|------|------|
| `api/router.go` | Gin 路由注册：auth、public、console、admin、v1、internal、SPA 静态文件 |
| `api/response.go` | 统一响应工具函数（Success、Error、BadRequest、NotFound 等） |

### internal/api/middleware/ — 中间件

| 文件 | 说明 |
|------|------|
| `middleware/jwt_auth.go` | JWT 认证：解析 Bearer Token、校验缓存、提取 UserID/Role |
| `middleware/auth.go` | API Key 认证：SHA256 哈希匹配、Redis 缓存、Token 失效管理 |
| `middleware/callback_auth.go` | Webhook 回调签名验证（HMAC-SHA256） |
| `middleware/cors.go` | CORS 跨域头设置 |
| `middleware/logger.go` | 请求日志：记录 Method、Path、Status、Latency、Body |
| `middleware/rate_limit.go` | 固定窗口限流：按 Token ID 或 IP，基于 Redis |
| `middleware/request_id.go` | 请求 ID 生成与传播 |

### internal/api/v1/ — API 处理器

| 文件 | 说明 |
|------|------|
| `v1/auth.go` | 注册、登录、登出、修改密码 |
| `v1/user.go` | 管理员用户管理：列表、角色/状态变更、充值 |
| `v1/channel.go` | 管理员渠道 & 账号 CRUD |
| `v1/capability_admin.go` | 管理员能力 & 渠道能力配置 CRUD |
| `v1/capability.go` | 列出可用渠道和能力（含渠道支持信息） |
| `v1/chat.go` | Chat 补全接口 + 公开模型列表 |
| `v1/chat_admin.go` | 管理员 Chat 模型 & 模型-渠道映射 CRUD |
| `v1/generation.go` | 图片/视频生成接口：计费、创建任务、调用供应商 |
| `v1/task.go` | 按任务编号查询任务状态 |
| `v1/token_manage.go` | Token CRUD：列表、创建、更新、删除、充值、渠道优先级 |
| `v1/conversation.go` | 对话和消息列表（分页、筛选） |
| `v1/dashboard.go` | 仪表盘统计：今日/昨日数据、趋势、Top 渠道、错误率 |
| `v1/pricing.go` | 公开定价信息接口 |
| `v1/request_log.go` | 管理员请求日志：列表、详情、重试 |
| `v1/callback.go` | Webhook 回调处理：解析并匹配任务结果 |
| `v1/response.go` | v1 API 响应辅助函数 |

### internal/model/ — 数据模型

| 文件 | 说明 |
|------|------|
| `model/base.go` | 基础模型（ID、时间戳、软删除）、数据库连接管理、AutoMigrate |
| `model/user.go` | 用户：用户名、密码、角色（admin/user）、余额、状态 |
| `model/token.go` | API Token：Key（SHA256）、余额、已用额度、限流配置、状态 |
| `model/channel.go` | 渠道：类型、名称、BaseURL、回调密钥、配置 JSON |
| `model/channel_account.go` | 渠道账号：API Key、权重、并发上限（max_tasks）、当前任务数 |
| `model/capability.go` | 能力定义：code、名称、类型（image/video/chat）、标准参数/响应 |
| `model/channel_capability.go` | 渠道能力实现：定价、请求/轮询/回调配置、认证、参数映射 |
| `model/chat_model.go` | Chat 模型：code、名称、供应商、描述 |
| `model/chat_model_channel.go` | 模型-渠道映射：定价（input/output per 1M tokens）、请求配置 |
| `model/conversation.go` | 对话：用户、Token、标题、模型、系统提示词、消息数 |
| `model/message.go` | 消息：角色、内容、Token 用量、延迟、费用 |
| `model/task.go` | 任务：编号、状态、参数、供应商响应、结果、回调 |
| `model/billing_log.go` | 计费流水：幂等键、扣费/退款、金额、状态 |
| `model/channel_request_log.go` | 请求日志：submit/poll/callback/chat、请求头/体、响应、耗时 |
| `model/token_channel_priority.go` | Token 渠道优先级配置 |

### internal/service/ — 业务逻辑

| 文件 | 说明 |
|------|------|
| `service/user_service.go` | 注册、登录、登出、改密、角色/状态变更 |
| `service/channel_service.go` | 渠道 & 账号 CRUD |
| `service/capability_service.go` | 能力调用：渠道选择、参数映射、响应解析、文件上传 |
| `service/chat_service.go` | Chat 补全：模型/渠道选择、Provider 路由、Token 计数、计费 |
| `service/task_service.go` | 任务 CRUD 和状态流转（pending → processing → success/failed） |
| `service/billing_service.go` | 扣费/退款（带幂等键），事务保证一致性 |
| `service/strategy_service.go` | 路由策略：加权随机选渠道能力、负载均衡选账号（行锁） |
| `service/conversation_service.go` | 对话和消息列表（分页、筛选、费用聚合） |
| `service/request_log_service.go` | 请求日志：异步写入（带重试）、列表、详情、重试 |
| `service/param_mapper.go` | 参数映射：字段/值映射、类型转换、计算参数 |
| `service/response_mapper.go` | 响应映射：字段提取、值映射、数组处理、成功条件判断 |

### internal/provider/ — AI 供应商适配

| 文件 | 说明 |
|------|------|
| `provider/provider.go` | Provider 接口定义（Submit、GetProgress、ParseCallback）和结果类型 |
| `provider/base_provider.go` | 基础 HTTP Provider：认证、请求构建、提交/进度/回调解析 |
| `provider/factory.go` | Provider 工厂：根据渠道/账号/能力配置创建 BaseProvider |
| `provider/converter.go` | 参数转换：字段映射、值映射、嵌套路径支持 |
| `provider/parser.go` | 响应解析：用 gjson 提取任务 ID、状态、进度、URL、错误 |
| `provider/chat/provider.go` | Chat Provider 接口和统一请求/响应结构 |
| `provider/chat/registry.go` | Chat Provider 注册表和工厂 |
| `provider/chat/openai.go` | OpenAI API 适配器 |
| `provider/chat/anthropic.go` | Anthropic (Claude) API 适配器 |
| `provider/chat/google.go` | Google (Gemini) API 适配器 |

### internal/worker/ — 异步任务

| 文件 | 说明 |
|------|------|
| `worker/worker.go` | Worker Handler 注册 |
| `worker/types.go` | 任务载荷类型（submit、poll、upload、notify） |
| `worker/submit_worker.go` | 任务提交：调用 Provider、参数映射 |
| `worker/poll_worker.go` | 轮询任务状态：可配置间隔和最大次数 |
| `worker/callback_handler.go` | 回调结果处理：路由到上传/失败/进度更新 |
| `worker/upload_worker.go` | 文件下载并上传到 COS，构建结果 |
| `worker/notify_worker.go` | 任务完成通知（带重试） |
| `worker/timeout_checker.go` | 定时检测超时任务（30+ 分钟未完成） |

### pkg/ — 工具包

| 文件 | 说明 |
|------|------|
| `pkg/auth/jwt.go` | JWT 生成和解析（HS256） |
| `pkg/auth/password.go` | bcrypt 密码哈希和校验 |
| `pkg/cache/cache.go` | Redis 客户端初始化、登录 Token 管理 |
| `pkg/config/config.go` | YAML 配置加载（server、database、redis、storage、worker、http、rate_limit） |
| `pkg/database/database.go` | MySQL 连接池配置 |
| `pkg/errors/errors.go` | 自定义错误类型（code + message） |
| `pkg/httputil/client.go` | HTTP 客户端工具（文件下载等） |
| `pkg/logger/logger.go` | Zap 结构化日志 |
| `pkg/metrics/metrics.go` | 指标采集 |
| `pkg/queue/queue.go` | Asynq 队列客户端初始化 |
| `pkg/storage/init.go` | 存储初始化 |
| `pkg/storage/storage.go` | 存储接口和上传功能 |
| `pkg/storage/tencent_cos.go` | 腾讯云 COS 实现 |

---

## 前端文件清单

### 配置文件

| 文件 | 说明 |
|------|------|
| `package.json` | 依赖定义：React 19、React Router、Recharts、Lucide Icons |
| `tsconfig.json` | TypeScript 配置（ES2022、路径别名） |
| `vite.config.ts` | Vite 构建配置：React 插件、API 代理到 localhost:23523 |
| `vite-env.d.ts` | Vite 客户端类型声明 |
| `metadata.json` | 应用元数据：名称「棱镜」、描述 |

### 核心文件

| 文件 | 说明 |
|------|------|
| `index.tsx` | React DOM 入口，挂载 App 到 root |
| `App.tsx` | 主组件：认证流程、路由、Layout、ErrorBoundary |
| `types.ts` | 全部 TypeScript 接口定义 |
| `constants.tsx` | 导航路由、状态颜色/标签映射、UI 常量 |

### 公共组件

| 文件 | 说明 |
|------|------|
| `components/Layout.tsx` | 主布局：侧边栏导航、顶栏用户信息/余额、面包屑 |
| `components/ErrorBoundary.tsx` | 错误边界：捕获渲染错误，显示友好提示和重试按钮 |

### API 服务

| 文件 | 说明 |
|------|------|
| `services/api.ts` | 统一 API 客户端：认证、用户、渠道、能力、Token、任务、日志、Chat 模型、对话 |

### 页面 — 管理员

| 文件 | 说明 |
|------|------|
| `pages/Channels.tsx` | 渠道管理：渠道/账号 CRUD，JSON 配置编辑，并发数设置 |
| `pages/Capabilities.tsx` | 能力配置：标准参数、响应映射、渠道能力（sync/poll/callback） |
| `pages/Users.tsx` | 用户管理：角色分配、搜索、余额充值 |
| `pages/ChatModels.tsx` | Chat 模型管理：创建/编辑模型，选择供应商 |
| `pages/ChatModelChannels.tsx` | 模型-渠道映射：定价、优先级配置 |
| `pages/RequestLogs.tsx` | 渠道请求日志：筛选、重试、请求/响应详情查看 |

### 页面 — 通用

| 文件 | 说明 |
|------|------|
| `pages/Dashboard.tsx` | 仪表盘：今日统计、周趋势图、能力分布饼图 |
| `pages/Tokens.tsx` | Token 管理：创建、充值、渠道优先级、Key 复制 |
| `pages/Logs.tsx` | 任务日志：按能力/状态筛选、分页、任务详情 |
| `pages/ChatLogs.tsx` | 对话记录：会话列表、消息详情、Token 用量、费用 |
| `pages/ApiDocs.tsx` | API 文档：接口定义、请求/响应示例、错误码、认证说明 |
| `pages/ChangePassword.tsx` | 修改密码表单 |

### 页面 — 公开

| 文件 | 说明 |
|------|------|
| `pages/Home.tsx` | 落地页：功能展示、能力列表 |
| `pages/Pricing.tsx` | 公开定价页：能力和渠道定价详情 |

---

## 配置文件

| 文件 | 说明 |
|------|------|
| `configs/config.example.yaml` | 配置模板：服务端口(23523)、JWT 密钥、数据库、Redis |
| `configs/config.yaml` | 实际配置（不提交 Git） |
| `configs/config.docker.yaml` | Docker 环境配置 |
| `docs/init.sql` | 数据库初始化 SQL（创建管理员账号） |

---

## API 层级

| 路径前缀 | 认证方式 | 用途 |
|----------|---------|------|
| `/api/auth/*` | 无 | 注册、登录 |
| `/api/*` | JWT | 控制台用户接口 |
| `/api/admin/*` | JWT + Admin 角色 | 管理员接口 |
| `/v1/*` | API Key | 外部 AI 服务接口 |
| `/internal/callback/*` | HMAC 签名 | 供应商回调 |

## 核心数据流

```
用户请求 → Token 认证 → 限流 → 路由策略（选渠道+选账号）
    → 参数映射 → Provider 调用 → 异步任务
    → 轮询/回调 → 响应映射 → 文件上传(COS) → 返回结果
    → 计费扣款
```

## 构建 & 部署

```bash
# build.bat 执行流程：
cd console && npm install && npm run build   # 编译前端
go build -o dist/prism cmd/server/main.go    # 编译后端（前端 embed）
cp configs/config.example.yaml dist/configs/ # 复制配置模板

# 产物：dist/prism（单二进制，包含前端静态文件）
```
