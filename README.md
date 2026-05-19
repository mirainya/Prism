<p align="center">
  <img src="console/assets/logo.svg" width="120" alt="Prism Logo" />
</p>

<h1 align="center">Prism</h1>

<p align="center">
  多渠道聚合、统一鉴权、可视化运营的 AI Gateway
</p>

---

Prism 是一个基于 Go + React 的 AI Gateway，提供统一的 OpenAI 兼容接口，支持多渠道路由、异步任务调度、计费管理和完整的管理后台。单二进制部署，前端嵌入后端。

## 核心能力

| 能力 | 说明 |
|------|------|
| **统一 Chat 接口** | `/v1/chat/completions`、`/v1/models`，完全兼容 OpenAI 格式 |
| **多渠道路由** | 同一模型映射多个渠道，支持优先级与负载均衡 |
| **多提供商** | OpenAI / Anthropic / Google Gemini / DeepSeek / 通义千问 / Moonshot / 火山引擎 |
| **异步任务** | 图片/视频生成等能力，支持提交、轮询、回调、取消 |
| **Playground** | 控制台内置调试台，支持流式对话、文件上传、能力调用 |
| **API 文档** | 动态生成，内置右侧抽屉试用面板（表单/JSON 双模式） |
| **模型发现** | 自动同步上游渠道模型列表，审批后上线 |
| **计费系统** | Token 余额体系，按模型定价，用量统计 |
| **请求日志** | 完整记录请求/响应/错误/用量，支持重试 |
| **配置热重载** | Viper 监听配置文件变更，无需重启 |

## 技术栈

**后端**：Go 1.25 · Gin · GORM · MySQL · Redis · Asynq · Viper · Zap · Prometheus

**前端**：React 19 · TypeScript · Vite · Tailwind CSS · Recharts · Lucide Icons

## 项目结构

```
Prism/
├── cmd/server/              # 服务入口
├── configs/                 # 配置文件
├── console/                 # React 前端（构建后嵌入二进制）
│   └── pages/               # 页面组件
├── internal/
│   ├── api/
│   │   ├── admin/           # 管理员 API（渠道/模型/用户/日志）
│   │   ├── console/         # 控制台 API（Token/仪表盘/Playground）
│   │   ├── open/            # 公开 API（Chat/能力/任务）
│   │   ├── callback/        # 上游回调接收
│   │   └── middleware/      # JWT/Token鉴权/限流/日志
│   ├── model/               # 数据模型
│   ├── domain/              # 领域接口
│   ├── repository/          # 数据访问层
│   ├── provider/
│   │   ├── chat/            # Chat 提供商适配（OpenAI/Anthropic/Google）
│   │   └── mapping/         # 参数/响应映射规则引擎
│   ├── service/             # 业务逻辑
│   └── worker/              # 异步任务（提交/轮询/超时/回调/模型发现）
├── pkg/                     # 公共组件（config/database/cache/auth/logger/queue）
├── Dockerfile               # 多阶段构建
├── docker-compose.yml       # 一键部署（含 MySQL + Redis）
└── build.bat                # Windows 构建 Linux 二进制
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

### 公开接口（Token 鉴权）

```
POST   /v1/chat/completions          # 对话补全（流式/非流式）
GET    /v1/models                     # 模型列表
GET    /v1/capabilities               # 可用能力列表
POST   /v1/capabilities/:capability   # 调用能力（图片/视频生成等）
GET    /v1/tasks/:task_no             # 查询任务状态
POST   /v1/tasks/:task_no/cancel      # 取消任务
```

认证方式：`Authorization: YOUR_TOKEN`

### 管理后台

| 模块 | 说明 |
|------|------|
| 仪表盘 | 请求量、用量、费用统计与趋势图 |
| 渠道管理 | 添加/编辑上游渠道与账号 |
| 模型管理 | Chat 模型配置、渠道映射、模型发现 |
| 能力管理 | 异步能力配置、参数 Schema、渠道绑定 |
| Token 管理 | 创建/充值/禁用 API Token |
| 请求日志 | 查看请求详情、错误信息、重试 |
| Playground | 在线调试对话和能力调用 |
| API 文档 | 动态生成的接口文档，内置试用面板 |
| 用户管理 | 角色/状态/余额管理 |

## 配置说明

`configs/config.yaml` 主要配置项：

```yaml
server:
  port: 23523
  jwt_secret: "your-secret"

database:
  host: localhost
  port: 3306
  user: prism
  password: ""
  dbname: prism

redis:
  addr: localhost:6379
  password: ""

worker:
  concurrency: 10
  poll_interval: 5s

rate_limit:
  enabled: false
  requests_per_min: 60
```

## License

MIT
