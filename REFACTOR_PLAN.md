# Prism 重构方案

## 一、现状问题总结

| 问题 | 影响 |
|---|---|
| God Object（chat_service 970行, capability_service 800+行） | 难以维护、测试、扩展 |
| 双重供应商系统（chat provider vs base provider）互不相通 | 新增供应商要改两处 |
| 无 Repository 抽象，所有层直接 `model.DB()` | 无法单测，强耦合 GORM |
| 重复实现（HTTP client、auth 构建、响应解析、轮询逻辑） | 改一处漏一处 |
| N+1 查询（渠道选择逐个查询） | 性能差 |
| 模型管理纯手动，无动态发现 | 运维负担重 |
| Service 无接口，实例化方式不统一 | 难以 mock 和替换 |

---

## 二、重构目标

1. **清晰的分层架构** — 每层职责单一，依赖方向明确
2. **统一供应商系统** — 一套接口覆盖 chat + capability
3. **可测试性** — 通过接口解耦，支持 mock
4. **动态模型发现** — 自动从上游 API 拉取可用模型
5. **消除重复** — 统一 HTTP client、路由策略、错误处理
6. **渐进式迁移** — 不一次性重写，分阶段推进

---

## 三、新架构设计

### 目录结构

```
cmd/server/main.go

internal/
  domain/                    ← 领域模型（纯结构体 + 接口，零外部依赖）
    model.go                 # Channel, Account, ChatModel, Token, Task...
    repository.go            # Repository 接口定义
    provider.go              # Provider 统一接口定义
    errors.go                # 领域错误

  repository/                ← 数据访问层（实现 domain 接口）
    channel_repo.go
    model_repo.go
    token_repo.go
    task_repo.go
    billing_repo.go
    conversation_repo.go
    log_repo.go

  provider/                  ← 统一供应商适配层
    registry.go              # 供应商注册表
    openai/                  # OpenAI 兼容 (含 deepseek/qwen/moonshot)
      provider.go
      models.go              # /v1/models 动态发现
    anthropic/
      provider.go
      models.go
    google/
      provider.go
      models.go
    generic/                 # 通用 HTTP 供应商（原 base_provider 能力）
      provider.go

  service/                   ← 业务逻辑层（依赖 domain 接口）
    chat.go                  # 对话补全（精简版，~200行）
    completion.go            # 流式/非流式执行逻辑
    routing.go               # 渠道选择 + 负载均衡（合并 strategy）
    billing.go               # 计费
    task.go                  # 异步任务编排
    discovery.go             # 模型动态发现 + 同步
    admin.go                 # 管理操作

  worker/                    ← 异步任务处理（精简）
    handler.go               # 统一 handler 注册
    submit.go
    poll.go
    notify.go
    timeout.go

  api/                       ← HTTP 层（薄 handler，只做参数绑定 + 调用 service）
    router.go
    middleware/
    v1/                      # 公开 AI API
      chat.go
      capability.go
      models.go              # GET /v1/models（动态）
    admin/                   # 管理 API
    console/                 # 前端 API

pkg/                         ← 纯工具（无业务逻辑）
  httpclient/                # 统一 HTTP client（合并两个）
  config/
  logger/
  cache/
  database/
  queue/
  crypto/                    # hash, jwt, password

console/                     ← React 前端
```

### 依赖方向（严格单向）

```
api → service → domain ← repository
                domain ← provider
pkg（被所有层使用，不依赖 internal）
```

---

## 四、统一 Provider 接口

```go
// internal/domain/provider.go

type Provider interface {
    // Chat 能力
    ChatComplete(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    ChatStream(ctx context.Context, req *ChatRequest) (io.ReadCloser, error)

    // 通用能力（图片/视频生成等）
    Execute(ctx context.Context, req *GenericRequest) (*GenericResponse, error)
    Poll(ctx context.Context, taskID string) (*GenericResponse, error)

    // 模型发现
    ListModels(ctx context.Context) ([]ModelInfo, error)

    // 元信息
    Name() string
    Capabilities() []string  // ["chat", "image", "video", "embedding"]
}

type ModelInfo struct {
    ID          string            // 上游模型 ID
    Name        string            // 显示名
    Provider    string            // 供应商标识
    Type        string            // chat/image/video/embedding
    MaxTokens   int               // 最大 token 数
    Features    []string          // streaming, tools, vision...
    Pricing     *ModelPricing     // 可选：上游公开的定价
    RawMeta     map[string]any    // 上游返回的原始元数据
}
```

不是所有供应商都实现全部方法 — 未实现的返回 `ErrNotSupported`。

---

## 五、动态模型发现系统

### 设计

```
┌─────────────┐     定时/手动触发      ┌──────────────┐
│  Discovery  │ ──────────────────────→ │   Provider   │
│   Service   │ ←────────────────────── │ .ListModels()│
└─────────────┘     []ModelInfo         └──────────────┘
       │
       │ 对比本地 DB
       ▼
┌─────────────┐
│  同步策略    │
│ - 新增模型 → 自动创建（默认禁用）
│ - 已删除   → 标记下线
│ - 已存在   → 更新元数据
└─────────────┘
```

### 工作流程

1. **定时拉取**：每 30 分钟（可配置）对所有启用的渠道调用 `ListModels()`
2. **手动触发**：管理员可通过 API `POST /api/admin/discovery/sync` 立即同步
3. **对比策略**：
   - 上游新增模型 → 写入 `chat_models` 表，`status=0`（默认禁用），通知管理员
   - 上游移除模型 → 标记 `deprecated`，不自动删除
   - 已存在模型 → 更新 `max_tokens`、`features` 等元数据
4. **管理员审核**：在前端看到新发现的模型列表，一键启用/配置定价

### 各供应商 ListModels 实现

| 供应商 | API | 说明 |
|---|---|---|
| OpenAI 兼容 | `GET /v1/models` | 标准接口，DeepSeek/Qwen/Moonshot 同样支持 |
| Anthropic | 硬编码 + 版本检测 | Anthropic 无公开 models API，维护已知列表 |
| Google | `GET /v1beta/models` | Gemini API 支持 |

### 配置

```yaml
discovery:
  enabled: true
  interval_minutes: 30
  auto_enable: false          # 新发现的模型是否自动启用
  providers:                  # 可按供应商覆盖
    openai:
      enabled: true
    anthropic:
      enabled: false          # 无 API，用硬编码列表
```

---

## 六、渠道路由重构

### 当前问题
- N+1 查询
- chat 和 capability 两套选择逻辑
- 无权重/优先级/故障转移

### 新设计

```go
// internal/service/routing.go

type Router struct {
    repo domain.ChannelRepository
}

// Route 统一路由入口
func (r *Router) Route(ctx context.Context, req RoutingRequest) (*RoutingResult, error)

type RoutingRequest struct {
    TokenID        uint
    ModelCode      string
    CapabilityCode string     // 可选，capability 路由时使用
    ExcludeIDs     []uint    // 重试时排除已失败的渠道
}

type RoutingResult struct {
    Channel    *domain.Channel
    Account    *domain.ChannelAccount
    Config     *domain.RouteConfig    // 合并后的路由配置
}
```

### 路由策略（单次 SQL）

```sql
SELECT cc.*, c.*, ca.*
FROM channel_capabilities cc
JOIN channels c ON cc.channel_id = c.id AND c.status = 1
JOIN channel_accounts ca ON ca.channel_id = c.id AND ca.status = 1
  AND (ca.max_tasks = 0 OR ca.current_tasks < ca.max_tasks)
LEFT JOIN token_channel_priorities tcp ON tcp.channel_id = c.id AND tcp.token_id = ?
WHERE cc.model = ? AND cc.status = 1
  AND cc.channel_id NOT IN (?)
ORDER BY tcp.priority ASC NULLS LAST, ca.current_tasks ASC, ca.weight DESC
LIMIT 1
FOR UPDATE OF ca
```

一次查询完成：渠道筛选 + 账号选择 + 并发控制。

---

## 七、分阶段实施计划

### Phase 1：基础重构（1-2 周）

1. 创建 `internal/domain/` — 定义领域模型和接口
2. 创建 `internal/repository/` — 从 service 中提取数据访问逻辑
3. 统一 HTTP client 到 `pkg/httpclient/`
4. Service 层改为依赖接口（构造函数注入）

**不破坏现有功能，纯内部重组。**

### Phase 2：Provider 统一（1 周）

1. 定义统一 `Provider` 接口
2. 重写 `provider/openai/`、`provider/anthropic/`、`provider/google/`
3. 将原 `base_provider` 的通用 HTTP 能力迁移到 `provider/generic/`
4. 删除旧的双重系统

### Phase 3：Service 拆分（1 周）

1. `chat_service.go` 拆为 `chat.go`（编排）+ `completion.go`（执行）+ `routing.go`（路由）
2. `capability_service.go` 拆分，复用 `routing.go` 和 `billing.go`
3. 消除重复的轮询/回调逻辑

### Phase 4：动态模型发现（1 周）

1. 实现各 Provider 的 `ListModels()`
2. 创建 `service/discovery.go`
3. 添加定时任务 + 管理 API
4. 前端：模型发现页面（新发现列表 + 一键启用）

### Phase 5：前端优化 + 新特性（1 周）

1. 模型管理页面增加"同步"按钮和发现状态
2. 渠道健康可视化增强
3. 路由策略可视化配置

---

## 八、新特性总结

### 管理员新特性

| 特性 | 说明 |
|---|---|
| 🔄 动态模型发现 | 自动从上游拉取可用模型，无需手动维护模型列表 |
| 📋 模型审核面板 | 新发现的模型集中展示，一键启用/配置 |
| 🏥 渠道健康大盘 | 实时查看每个渠道/账号的状态、延迟、错误率 |
| ⚡ 智能路由配置 | 可视化配置路由策略（优先级/权重/故障转移） |
| 📊 模型元数据同步 | 自动同步 max_tokens、支持的 features 等信息 |
| 🔔 变更通知 | 上游模型上下线时通知管理员 |

### 用户（API 调用者）新特性

| 特性 | 说明 |
|---|---|
| 📡 实时模型列表 | `GET /v1/models` 返回真实可用模型（含能力标签） |
| 🏷️ 模型能力标签 | 每个模型标注支持的功能（streaming, tools, vision, json_mode） |
| 🔀 自动故障转移 | 请求失败自动切换到备用渠道，用户无感知 |
| 📈 用量透明 | 响应中包含实际使用的模型和渠道信息（debug 模式） |

### 架构优势

| 优势 | 说明 |
|---|---|
| 可测试 | Repository 接口 + mock，service 层可独立单测 |
| 可扩展 | 新增供应商只需实现 Provider 接口，注册即用 |
| 性能 | 路由从 N+1 查询优化为单次 SQL |
| 可维护 | 每个文件 < 300 行，职责单一 |
| 渐进迁移 | 分 5 个 Phase，每个 Phase 独立可交付 |

---

## 九、关于动态模型发现的详细设计

### 数据模型扩展

```go
// chat_models 表新增字段
type ChatModel struct {
    Code         string         // 主键
    Name         string
    Provider     string
    Description  string
    Status       int            // 0=禁用 1=启用 2=已发现未审核
    // --- 新增 ---
    MaxTokens    int            // 从上游同步
    Features     JSON           // ["streaming","tools","vision"]
    DiscoveredAt *time.Time     // 首次发现时间
    LastSyncAt   *time.Time     // 最后同步时间
    Deprecated   bool           // 上游已下线
    RawMeta      JSON           // 上游原始元数据
}
```

### API 设计

```
# 管理员
POST   /api/admin/discovery/sync              # 手动触发全量同步
POST   /api/admin/discovery/sync/:channel_id  # 同步指定渠道
GET    /api/admin/discovery/pending           # 获取待审核模型列表
POST   /api/admin/discovery/approve           # 批量审核启用
GET    /api/admin/models/:code/meta           # 查看模型元数据

# 用户
GET    /v1/models                             # 返回所有已启用模型（含能力标签）
GET    /v1/models/:model_id                   # 单个模型详情
```

### /v1/models 响应示例

```json
{
  "object": "list",
  "data": [
    {
      "id": "gpt-4o",
      "object": "model",
      "created": 1715367049,
      "owned_by": "openai",
      "max_tokens": 128000,
      "features": ["streaming", "tools", "vision", "json_mode"],
      "available": true
    },
    {
      "id": "claude-sonnet-4-6",
      "object": "model",
      "created": 1715367049,
      "owned_by": "anthropic",
      "max_tokens": 200000,
      "features": ["streaming", "tools", "vision", "thinking"],
      "available": true
    }
  ]
}
```

---

## 十、前端美化方案 — 二次元柔和风格

### 当前状态

- Tailwind CDN（无构建优化）
- 标准 indigo 配色，白底灰边
- 无组件库，无设计系统
- 无动画/装饰，视觉平淡

### 目标风格

综合参考 5 个项目的设计语言：
- **nexus/console** — 薰衣草紫 + 樱花粉配色，玻璃卡片
- **meta-prompt/web** — 登录页装饰圆，渐变侧边栏
- **Infinite Canvas** — 紫色发光系统，登录页几何装饰动画（旋转环/万花筒/闪烁点），多主题管理面板
- **x-file-storage** — 5 主题运行时切换（樱花粉/薰衣草/薄荷/奶茶/天空蓝），hover 微上浮，ECharts 主题联动

最终目标：**多主题可切换 + 玻璃拟态 + 紫色发光 + 登录页装饰动画**的柔和二次元风格。

### 技术升级

| 项目 | 当前 | 目标 |
|---|---|---|
| Tailwind | CDN 引入 | Tailwind v4 + Vite 构建 |
| 组件 | 每页重复写 | 统一设计系统（共享组件） |
| 主题 | 硬编码 indigo | CSS 变量 + 可切换主题 |
| 动画 | 仅 fade-in | 入场动画 + hover 微交互 |
| 装饰 | 无 | 背景 mesh + 登录页装饰 |

### 色彩体系 — 多主题运行时切换

参考 x-file-storage 的主题引擎，实现 5 套主题运行时切换（localStorage 持久化）：

| 主题 | 主色 | 页面背景 | 风格 |
|---|---|---|---|
| 薰衣草（默认） | `#8b5cf6` | `#faf8ff` | 柔紫 |
| 樱花粉 | `#d4849a` | `#faf5f7` | 暖粉 |
| 薄荷绿 | `#7ab8a8` | `#f4faf8` | 清凉 |
| 天空蓝 | `#7aaac8` | `#f4f8fa` | 宁静 |
| 奶茶棕 | `#b8a08a` | `#faf7f4` | 温暖 |

每套主题定义完整的 CSS 变量集：

```typescript
// theme.ts — 主题引擎
interface Theme {
  name: string
  primary: string
  primaryLight: string
  primaryLighter: string
  accent: string          // 点缀色
  surface: string         // 页面背景
  surfaceCard: string     // 卡片背景 (rgba)
  borderSoft: string
  textPrimary: string
  textSecondary: string
  sidebarGradient: string // 侧边栏渐变
  loginGradient: string   // 登录页渐变
  glowColor: string       // 发光色 (rgba)
  chartColors: string[]   // 图表配色
}

const themes: Record<string, Theme> = {
  lavender: {
    name: '薰衣草',
    primary: '#8b5cf6',
    primaryLight: '#a78bfa',
    primaryLighter: '#f5f3ff',
    accent: '#ec4899',
    surface: '#faf8ff',
    surfaceCard: 'rgba(255, 255, 255, 0.82)',
    borderSoft: '#e8e0f4',
    textPrimary: '#3a3448',
    textSecondary: '#9690a8',
    sidebarGradient: 'linear-gradient(180deg, #f5f3ff 0%, #ede9fe 100%)',
    loginGradient: 'linear-gradient(135deg, #f5f3ff 0%, #ede9fe 50%, #c4b5fd 100%)',
    glowColor: 'rgba(139, 92, 246, 0.15)',
    chartColors: ['#8b5cf6', '#ec4899', '#6366f1', '#a78bfa', '#f472b6'],
  },
  sakura: { ... },
  mint: { ... },
  sky: { ... },
  milktea: { ... },
}
```

**主题切换器**：在 Header 右侧显示一排彩色圆点（20×20px），点击即切换，hover 时 `scale(1.2)`。

### 圆角规范

| 元素 | 圆角 |
|---|---|
| 按钮 | 12px (rounded-xl) |
| 卡片 | 16px (rounded-2xl) |
| 弹窗 | 20px |
| 输入框 | 12px |
| 标签/徽章 | 全圆 (rounded-full) |
| 侧边栏 | 右侧 24px |

### 玻璃拟态卡片

```css
.glass-card {
  background: rgba(255, 255, 255, 0.82);
  backdrop-filter: blur(20px);
  border: 1px solid rgba(255, 255, 255, 0.6);
  box-shadow: 0 8px 32px rgba(138, 92, 246, 0.06);
  border-radius: 16px;
}
```

### 渐变规范

```css
/* 主按钮 */
.btn-primary {
  background: linear-gradient(135deg, var(--prism-500), var(--prism-600));
  box-shadow: 0 4px 12px rgba(139, 92, 246, 0.25);
}
.btn-primary:hover {
  transform: translateY(-1px);
  box-shadow: 0 6px 20px rgba(139, 92, 246, 0.35);
}

/* 页面背景 mesh */
.bg-mesh {
  background:
    radial-gradient(at 20% 30%, rgba(139, 92, 246, 0.08) 0px, transparent 50%),
    radial-gradient(at 80% 10%, rgba(236, 72, 153, 0.06) 0px, transparent 50%),
    radial-gradient(at 50% 80%, rgba(167, 139, 250, 0.05) 0px, transparent 50%);
}

/* 登录页 */
.login-bg {
  background: linear-gradient(135deg, #f5f3ff 0%, #ede9fe 50%, #c4b5fd 100%);
}
```

### 动画系统

```css
/* 页面入场 */
@keyframes fade-slide-up {
  from { opacity: 0; transform: translateY(12px); }
  to { opacity: 1; transform: translateY(0); }
}
.page-enter { animation: fade-slide-up 0.3s ease-out; }

/* 卡片入场（交错） */
.card-enter {
  animation: fade-slide-up 0.4s ease-out backwards;
}
.card-enter:nth-child(1) { animation-delay: 0ms; }
.card-enter:nth-child(2) { animation-delay: 60ms; }
.card-enter:nth-child(3) { animation-delay: 120ms; }
.card-enter:nth-child(4) { animation-delay: 180ms; }

/* hover 微上浮 */
.hover-lift {
  transition: all 0.2s ease;
}
.hover-lift:hover {
  transform: translateY(-2px);
  box-shadow: 0 12px 24px rgba(138, 92, 246, 0.1);
}

/* 发光脉冲（状态指示） */
@keyframes glow-pulse {
  0%, 100% { box-shadow: 0 0 4px rgba(139, 92, 246, 0.4); }
  50% { box-shadow: 0 0 12px rgba(139, 92, 246, 0.6); }
}
```

### 装饰元素

#### 登录页 — 几何装饰动画 + 半透明圆

参考 Infinite Canvas 的登录页装饰层，实现旋转几何图形 + 发光粒子：

```jsx
<div className="login-bg min-h-screen relative overflow-hidden">
  {/* 背景 mesh 渐变 */}
  <div className="absolute inset-0 bg-mesh" />
  
  {/* 装饰层 — 旋转几何 */}
  <div className="login-art">
    {/* 大旋转环 (24s) */}
    <div className="absolute w-[500px] h-[500px] rounded-full 
                    border-2 border-primary/10 -top-[150px] -right-[150px]
                    animate-[login-spin_24s_linear_infinite]" />
    {/* 小虚线环 (反向 36s) */}
    <div className="absolute w-[200px] h-[200px] rounded-full 
                    border border-dashed border-primary/15 
                    bottom-[10%] left-[5%]
                    animate-[login-spin-reverse_36s_linear_infinite]" />
    {/* 渐变光斑 */}
    <div className="absolute w-[300px] h-[300px] rounded-full 
                    bg-gradient-to-br from-primary/8 to-accent/5
                    top-[30%] right-[10%] blur-[40px]
                    animate-[glow-pulse_6s_ease-in-out_infinite]" />
    {/* 闪烁点 */}
    <div className="sparkle-dots" />
  </div>

  {/* 半透明装饰圆 */}
  <div className="absolute w-[400px] h-[400px] rounded-full 
                  bg-white/15 -top-[100px] -right-[80px]" />
  <div className="absolute w-[300px] h-[300px] rounded-full 
                  bg-white/15 -bottom-[60px] -left-[60px]" />

  {/* 登录卡片 — 玻璃拟态 */}
  <div className="glass-card max-w-md mx-auto mt-[20vh] p-8
                  shadow-[0_20px_60px_rgba(0,0,0,0.08)]">
    <h1 className="text-2xl font-bold bg-gradient-to-r 
                   from-primary to-accent bg-clip-text text-transparent">
      Prism
    </h1>
    ...
  </div>
</div>
```

动画定义（参考 Infinite Canvas）：
```css
@keyframes login-spin { to { transform: rotate(360deg); } }
@keyframes login-spin-reverse { to { transform: rotate(-360deg); } }
@keyframes sparkle {
  0%, 100% { opacity: 0.2; }
  50% { opacity: 0.8; filter: drop-shadow(0 0 4px var(--primary)); }
}
```

#### 侧边栏 — 渐变背景 + 活跃态发光

```jsx
<aside className="bg-gradient-to-b from-prism-50 to-white border-r border-border-soft">
  {/* 活跃菜单项 */}
  <a className="bg-prism-100/60 text-prism-700 rounded-xl 
               shadow-sm shadow-prism-200/50 font-medium">
    <Icon className="text-prism-500" />
    <span>Dashboard</span>
  </a>
</aside>
```

#### 仪表盘 — 统计卡片渐变图标

```jsx
<div className="glass-card hover-lift p-6">
  <div className="w-12 h-12 rounded-xl bg-gradient-to-br 
                  from-prism-400 to-sakura-400 flex items-center justify-center">
    <Icon className="text-white" size={24} />
  </div>
  <div className="mt-4">
    <p className="text-text-secondary text-sm">今日请求</p>
    <p className="text-2xl font-bold text-text-primary">12,847</p>
  </div>
</div>
```

### 发光系统（参考 Infinite Canvas）

```css
:root {
  --glow: 0 0 20px var(--glow-color);
  --glow-strong: 0 0 32px var(--glow-color);
}

/* 活跃导航项 */
.nav-active {
  box-shadow: var(--glow);
  border-left: 3px solid var(--primary);
}

/* 状态指示灯 */
.status-online {
  animation: glow-pulse 2s ease-in-out infinite alternate;
}

/* 卡片 hover */
.glass-card:hover {
  box-shadow: var(--glow), 0 12px 24px rgba(0, 0, 0, 0.06);
}
```

### ECharts 主题联动（参考 x-file-storage）

图表颜色跟随当前主题的 `chartColors` 数组动态变化：

```typescript
// 切换主题时同步更新 ECharts 实例
const updateChartTheme = (theme: Theme) => {
  chartInstance.setOption({
    color: theme.chartColors,
    textStyle: { color: theme.textSecondary },
  })
}
```

仪表盘的 AreaChart 和 PieChart 自动适配当前主题色。

### 共享组件设计

```
console/src/
  theme/
    themes.ts              # 5 套主题定义
    ThemeProvider.tsx       # Context + localStorage 持久化
    ThemeSwitch.tsx         # 彩色圆点切换器
  components/
    ui/                    ← 基础组件（全部读取 CSS 变量）
      Button.tsx           # 主/次/危险/幽灵 4 种变体
      Card.tsx             # glass-card 封装 + hover-lift
      Input.tsx            # 统一输入框
      Badge.tsx            # 状态标签 + 发光
      Modal.tsx            # 统一弹窗（玻璃拟态）
      Table.tsx            # 统一表格（圆角 12px）
      Tooltip.tsx
      Spinner.tsx          # 加载动画
    layout/
      Sidebar.tsx          # 渐变侧边栏 + 发光活跃态
      Header.tsx           # 顶栏 + 主题切换器
      PageContainer.tsx    # 页面容器（入场动画）
    decorations/
      MeshBackground.tsx   # 背景 mesh 渐变
      LoginArt.tsx         # 登录页几何装饰动画
    charts/
      ThemedChart.tsx      # ECharts 主题联动包装
```

### 页面改造对照

| 页面 | 改造重点 |
|---|---|
| 登录/注册 | 渐变背景 + 旋转几何装饰 + 闪烁粒子 + 玻璃卡片 + 主题切换圆点 |
| Dashboard | 渐变图标卡片 + 交错入场 + ECharts 主题联动 + 发光统计数字 |
| 渠道管理 | 表格圆角 12px + 行 hover 高亮 + 状态徽章发光 + 展开动画 |
| 模型管理 | 新增"发现"标签页 + 模型卡片网格（auto-fill） + 能力标签 |
| Playground | 聊天气泡玻璃拟态 + 渐变发送按钮 + 打字动画 + 代码块主题色 |
| Token 管理 | 密钥遮罩美化 + 复制按钮微交互 + 余额渐变色 |
| 请求日志 | 时间线布局 + 状态码彩色标签 + 展开详情抽屉（slide-in） |

### 字体

```css
font-family: 'Nunito', 'Noto Sans SC', 'PingFang SC', system-ui, sans-serif;
```

Nunito（圆润西文字体，参考 x-file-storage）+ Noto Sans SC（中文），整体更柔和。

### 滚动条

```css
::-webkit-scrollbar { width: 5px; height: 5px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb {
  background: var(--primary-light);
  border-radius: 3px;
}
::-webkit-scrollbar-thumb:hover {
  background: var(--primary);
}
```

---

## 十一、风险与注意事项

1. **渐进迁移**：不要一次性重写。每个 Phase 结束后确保所有现有 API 行为不变。
2. **数据库兼容**：新增字段用 `ALTER TABLE ADD COLUMN`，不改已有字段语义。
3. **Provider ListModels 限流**：上游 API 有 rate limit，同步间隔不宜太短。
4. **Anthropic 特殊处理**：无公开 models API，需维护硬编码列表或爬取文档。
5. **测试覆盖**：Phase 1 引入 Repository 后，立即为核心路由逻辑写单测。
6. **前端迁移**：先升级 Tailwind CDN → Vite 构建，再逐页改造样式，避免一次性全改。

---

## 十二、完整实施时间线

| Phase | 内容 | 预估时间 | 交付物 |
|---|---|---|---|
| 1 | Domain + Repository 抽象 | 1-2 周 | 解耦数据库，可单测 |
| 2 | 统一 Provider 接口 | 1 周 | 一套接口覆盖所有供应商 |
| 3 | Service 拆分 | 1 周 | 消灭 God Object |
| 4 | 动态模型发现 | 1 周 | 自动同步上游模型 |
| 5 | 前端升级（Tailwind v4 + 组件系统） | 1 周 | 设计系统 + 共享组件 |
| 6 | 前端美化（逐页改造） | 1-2 周 | 二次元柔和风格全面落地 |

**总计：6-8 周**

---

## 十三、总结

### 重构后的核心优势

| 维度 | 改进 |
|---|---|
| **可维护性** | 每文件 < 300 行，职责单一，依赖清晰 |
| **可测试性** | Repository 接口 + mock，CI 可跑单测 |
| **可扩展性** | 新供应商只需实现 Provider 接口 |
| **性能** | N+1 → 单次 SQL，统一连接池 |
| **运维** | 动态模型发现，无需手动维护模型列表 |
| **用户体验** | 柔和美观的 UI + 实时模型列表 + 自动故障转移 |

### 对管理员的价值

- 不再手动添加模型 — 系统自动发现并提醒审核
- 渠道健康一目了然 — 实时状态 + 故障自动切换
- 路由策略可视化配置 — 不用改代码就能调整优先级

### 对 API 用户的价值

- `GET /v1/models` 返回真实可用模型（含能力标签）
- 请求失败无感知切换备用渠道
- 更丰富的模型元数据（max_tokens, features）
