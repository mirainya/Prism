<p align="center">
  <img src="console/assets/logo.svg" width="120" alt="Prism Logo" />
</p>

<h1 align="center">Prism</h1>

<p align="center">
  多渠道聚合、统一鉴权、可视化运营的 AI Gateway
</p>

---

Prism 是一个基于 Go + React 的 AI Gateway，提供统一的 OpenAI 兼容接口，支持多渠道路由、异步任务调度、计费管理和完整的管理后台。单二进制部署，前端嵌入后端。

```
客户端 ──→ Prism（鉴权 → 路由 → 计费 → 日志）──→ 上游 API
                                                    ├── OpenAI
                                                    ├── Anthropic Claude
                                                    ├── Google Gemini
                                                    └── 火山方舟
```

## 核心能力

| 能力 | 说明 |
|------|------|
| **统一对话接口** | `/v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1/files`、`/v1/models` |
| **多渠道路由** | 同一模型映射多个渠道，支持优先级与负载均衡，失败自动切换，渠道级图片 Base64 转换 |
| **多提供商** | OpenAI / Anthropic Claude / Google Gemini / 火山方舟；火山 Responses 使用 `/api/v3/responses` |
| **对话管理** | 自动归并对话、流式聚合 usage、消息历史追溯、附件显示 |
| **异步任务** | 图片/视频生成等能力，支持同步/轮询/回调三种交互模式，支持取消 |
| **Playground** | 内置调试台：流式对话、文件上传、能力调用、请求调试面板 |
| **API 文档** | 动态生成，渠道参数按交互模式分组显示，内置右侧抽屉试用面板（表单/JSON 双模式） |
| **模型发现** | 自动同步上游渠道模型列表，审批后上线，支持快速配置 |
| **计费系统** | Token 余额体系，按模型定价，用量实时扣费 |
| **请求日志** | 完整记录请求/响应/用量/延迟/错误，支持重试 |
| **对话日志** | 按 Token 查看对话列表、消息详情、输入输出 token |
| **统计仪表盘** | 今日概览 + 7 天趋势 + 模型/渠道分布图 |
| **配置热重载** | Viper 监听 config.yaml 变更，无需重启 |
| **监控** | Prometheus 指标 + `/health` 健康检查（DB + Redis） |
| **安全** | JWT + Token 双鉴权、CORS 可配置、Token 级限流、渠道能力更新白名单校验 |

## 技术栈

**后端**：Go 1.25 · Gin · GORM · MySQL 8 · Redis 7 · Asynq · Viper · Zap · Prometheus

**前端**：React 19 · TypeScript · Vite 6 · Tailwind CSS 4 · Recharts 3 · Lucide Icons

## 项目结构

```
Prism/
├── cmd/server/                  # 服务入口
├── configs/                     # 配置文件
├── console/                     # React 前端（构建后嵌入二进制）
│   └── pages/                   # 页面（Dashboard/Channels/ChatModels/Tokens/...）
├── internal/
│   ├── api/
│   │   ├── admin/               # 管理员 API（渠道/模型/用户/日志/发现）
│   │   ├── console/             # 控制台 API（Token/仪表盘/Playground/对话/文档）
│   │   ├── open/                # 公开 API（Chat/能力/任务/模型）
│   │   ├── callback/            # 上游回调接收
│   │   └── middleware/          # JWT/Token鉴权/限流/CORS/日志/错误处理
│   ├── model/                   # 数据模型（User/Channel/Token/Endpoint/Task/Log/Conversation）
│   ├── domain/                  # 领域接口
│   ├── repository/              # 数据访问层
│   ├── provider/
│   │   ├── chat/                # Chat 提供商适配（OpenAI/Anthropic/Google）
│   │   └── mapping/             # 参数/响应映射规则引擎
│   ├── service/                 # 业务逻辑（路由/计费/对话/日志/统计/模型管理）
│   └── worker/                  # 异步任务（提交/轮询/超时/回调/模型发现/上传）
├── pkg/                         # 公共组件
│   ├── auth/                    # JWT + 密码加密
│   ├── cache/                   # Redis 客户端
│   ├── config/                  # Viper 配置加载
│   ├── database/                # GORM 数据库
│   ├── httputil/                # HTTP 客户端（普通 + 流式）
│   ├── logger/                  # Zap 日志
│   ├── metrics/                 # Prometheus 指标
│   ├── queue/                   # Asynq 任务队列
│   └── filestorage/             # 文件存储
├── Dockerfile                   # 多阶段构建
├── docker-compose.yml           # 一键部署（Prism + MySQL + Redis）
└── build.bat                    # Windows 构建 Linux 二进制
```

## 快速开始

### 环境要求

- Go 1.25+
- Node.js 18+
- MySQL 8+
- Redis 6+

### Docker 部署（推荐）

```bash
git clone https://github.com/mirainya/Prism.git
cd Prism
docker compose up -d
```

服务启动后访问 `http://localhost:23523`。

### 手动部署

```bash
# 1. 克隆
git clone https://github.com/mirainya/Prism.git
cd Prism

# 2. 配置
cp configs/config.example.yaml configs/config.yaml
# 编辑 config.yaml 填写数据库和 Redis 连接信息

# 3. 构建前端
cd console && npm install && npm run build && cd ..

# 4. 构建后端（Linux）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o prism ./cmd/server

# 5. 部署
# 上传 prism + configs/ 到服务器，运行 ./prism
```

Windows 下可直接执行 `build.bat` 一键构建。

### 开发模式

```bash
# 后端
go run ./cmd/server

# 前端（独立开发服务器）
cd console && npm run dev
```

## API 概览

### 公开接口（`/v1`，Token 鉴权）

```
POST   /v1/chat/completions            # 对话补全（流式/非流式）
POST   /v1/messages                    # Anthropic Messages 兼容接口
POST   /v1/responses                   # OpenAI Responses 兼容接口
GET    /v1/responses/:id               # 查询已保存响应
DELETE /v1/responses/:id               # 删除响应
POST   /v1/responses/:id/cancel        # 取消后台响应
GET    /v1/responses/:id/input_items   # 获取响应输入项
POST   /v1/files                       # 上传多模态文件
GET    /v1/files                       # 文件列表
GET    /v1/files/:id                   # 文件信息
GET    /v1/files/:id/content           # 下载文件
DELETE /v1/files/:id                   # 删除文件
GET    /v1/models                       # 模型列表
GET    /v1/models/:code                 # 模型详情
GET    /v1/channels                      # 可用渠道列表
GET    /v1/capabilities                 # 可用能力列表
POST   /v1/capabilities/:capability     # 调用能力（图片/视频生成等）
GET    /v1/tasks/:task_no               # 查询任务状态
POST   /v1/tasks/:task_no/cancel        # 取消任务
POST   /v1/images/generations           # 图片生成（兼容接口）
POST   /v1/videos/generations           # 视频生成（兼容接口）
```

认证方式：`Authorization: YOUR_TOKEN`

### 管理接口（`/api/admin`，JWT + 管理员权限）

| 模块 | 接口 |
|------|------|
| 用户管理 | 列表、角色变更、状态变更、充值 |
| 渠道管理 | CRUD 渠道 + 渠道账号 |
| 能力管理 | CRUD 能力 + 渠道能力绑定 |
| 模型管理 | CRUD Chat 模型 + 渠道映射、预设模板、快速配置 |
| 模型发现 | 同步上游模型列表、审批/拒绝待上线模型 |
| 请求日志 | 列表、详情、重试 |

### 控制台接口（`/api`，JWT 认证）

| 模块 | 接口 |
|------|------|
| 认证 | 注册、登录、登出 |
| 用户 | 个人信息、修改密码 |
| Token | CRUD + 充值 |
| 仪表盘 | 请求/用量/费用统计、Chat 统计 |
| 对话 | 对话列表、消息详情 |
| Playground | 对话补全、能力调用、任务管理、调试信息、文件上传 |
| 文档 | 模型列表（用于 API 文档展示） |

## 管理后台

| 模块 | 说明 |
|------|------|
| 仪表盘 | 请求量、用量、费用统计与趋势图 |
| 渠道管理 | 添加/编辑上游渠道与账号，支持多种提供商 |
| 模型管理 | Chat 模型配置、渠道映射、预设模板、模型发现 |
| 能力管理 | 异步能力配置、参数 Schema、渠道绑定 |
| Token 管理 | 创建/充值/禁用 API Token |
| 用户管理 | 角色/状态/余额管理 |
| 请求日志 | 查看请求详情、错误信息、用量、支持重试 |
| 对话日志 | 按 Token 查看对话列表、消息内容、token 用量 |
| 定价管理 | 按模型设置输入/输出 token 单价 |
| Playground | 在线调试对话和能力调用，带调试面板 |
| API 文档 | 动态生成的接口文档，内置试用面板 |

## 配置说明

`configs/config.yaml` 主要配置项：

```yaml
server:
  port: 23523                     # 监听端口
  jwt_secret: "your-secret"       # JWT 密钥

database:
  host: localhost
  port: 3306
  user: prism
  password: ""
  dbname: prism
  max_open_conns: 50              # 最大连接数
  max_idle_conns: 10              # 最大空闲连接
  conn_max_lifetime: 3600         # 连接存活时间(秒)
  log_level: warn                 # info/warn/error/silent

redis:
  addr: localhost:6379
  password: ""
  db: 0
  pool_size: 20                   # 连接池大小

worker:
  concurrency: 10                 # 异步任务并发数
  poll_interval: 5s               # 轮询间隔
  max_retry: 3                    # 最大重试次数

http_client:
  timeout: 30                     # 请求超时(秒)
  max_idle_conns: 100             # 最大空闲连接
  idle_conn_timeout: 90           # 空闲连接超时(秒)

rate_limit:
  enabled: false                  # 是否启用限流
  requests_per_min: 60            # 每分钟请求上限(按 Token)
```

## License

MIT
