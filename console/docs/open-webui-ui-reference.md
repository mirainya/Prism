# Open WebUI 参考文档：流程设计 + UI 设计

> 来源: `F:\free\PythonProject\open-webui` (Svelte + Tailwind CSS)

---

# 第一部分：流程设计（核心参考）

## 0. 架构总览：两层模型体系

Open WebUI 最精妙的设计是**两层模型体系**：

```
┌─────────────────────────────────────────────────────────────┐
│                    用户看到的模型列表                          │
│  (经过权限过滤、排序、隐藏后的最终列表)                        │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐     │
│  │ Base Model  │    │ Base Model  │    │ Workspace   │     │
│  │ (直接来自   │    │ (被 DB 覆盖 │    │ Model       │     │
│  │  Provider)  │    │  了配置)    │    │ (用户创建)  │     │
│  └─────────────┘    └─────────────┘    └─────────────┘     │
│        ↑                   ↑                  ↑             │
│        │                   │                  │             │
├────────┼───────────────────┼──────────────────┼─────────────┤
│        │                   │                  │             │
│  ┌─────┴─────┐       ┌────┴────┐       ┌────┴────┐        │
│  │ Provider  │       │   DB    │       │   DB    │        │
│  │ /models   │       │ Model   │       │ Model   │        │
│  │ API 自动  │       │ (same   │       │ (has    │        │
│  │ 发现      │       │  id,    │       │  base_  │        │
│  │           │       │  null   │       │  model_ │        │
│  │           │       │  base)  │       │  id)    │        │
│  └───────────┘       └─────────┘       └─────────┘        │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 关键概念

| 概念 | 说明 |
|------|------|
| **Base Model** | 从 Provider (OpenAI/Ollama) 的 `/models` API 自动发现的模型 |
| **Custom Model (override)** | DB 中 `base_model_id = NULL` 的记录，ID 与 base model 相同，用于覆盖其名称/配置 |
| **Workspace Model (preset)** | DB 中 `base_model_id != NULL` 的记录，是用户创建的"模型预设"，指向某个 base model |

---

## 1. 完整流程：从添加连接到使用模型

### Step 1: 添加 Provider 连接

**位置**: Admin > Settings > Connections

```
Admin 添加一个 OpenAI-compatible 连接:
  - URL: https://api.openai.com/v1
  - API Key: sk-xxx
  - Config: { enable: true, model_ids: [], prefix_id: "" }
```

**精妙之处**:
- 支持多个连接（数组），每个连接有独立的 URL + Key + Config
- `model_ids`: 可手动指定模型列表（不调 /models API）
- `prefix_id`: 给模型 ID 加前缀避免冲突（如 `azure.gpt-4`）
- `enable`: 可临时禁用某个连接
- 连接验证: `verifyOpenAIConnection()` 调用 `/models` 确认可达

### Step 2: 自动发现模型

**触发时机**: 页面加载 / 手动刷新 / 连接变更后

```python
# backend: utils/models.py
async def get_all_base_models():
    # 并发请求所有 Provider
    openai_models = await fetch_openai_models()   # 调 /models API
    ollama_models = await fetch_ollama_models()   # 调 Ollama API
    function_models = await get_function_models() # Pipeline 函数模型
    return merge(openai_models, ollama_models, function_models)
```

### Step 3: DB 模型覆盖/创建

```python
async def get_all_models():
    base_models = await get_all_base_models()
    db_models = await Models.get_all_models()
    
    for db_model in db_models:
        if db_model.base_model_id:
            # Workspace Model: 作为独立模型加入列表
            merged_list.append(build_workspace_model(db_model, base_models))
        else:
            # Override: 找到同 ID 的 base model，覆盖其配置
            override_base_model(base_models, db_model)
```

### Step 4: 权限过滤

```python
# 最终用户看到的模型 = 过滤后的列表
visible_models = [m for m in all_models if check_access(user, m)]
```

**权限规则**:
- Admin: 看到所有
- User: 看到 `access_control.read.group_ids` 包含自己组的 + 公开的

---

## 2. 模型管理 UI 流程

### 创建 Workspace Model

```
ModelEditor.svelte:
  1. 输入 Name → 自动生成 ID (kebab-case)
  2. 选择 Base Model (从已发现的模型列表)
  3. 配置:
     - System Prompt
     - Advanced Params (temperature, top_p, etc.)
     - Knowledge (RAG 文件/集合)
     - Tools (勾选可用工具)
     - Filters (inlet/outlet 函数)
     - Capabilities (vision, web_search, etc.)
     - Access Control
  4. Save → POST /api/v1/models/create
```

### 编辑已有模型

```
ModelEditor.svelte (edit mode):
  - 加载现有配置
  - ID 不可修改
  - 其余同创建流程
  - Save → POST /api/v1/models/update
```

---

## 3. 连接管理流程

### 数据结构

```typescript
// 前端 store
OPENAI_API_BASE_URLS: string[]     // 多个连接 URL
OPENAI_API_KEYS: string[]          // 对应的 Key
OPENAI_API_CONFIGS: Record<string, {
  enable?: boolean
  model_ids?: string[]
  prefix_id?: string
}>
```

### 连接验证流程

```
用户输入 URL + Key
  → verifyOpenAIConnection(url, key)
  → GET {url}/models (带 Authorization header)
  → 成功: 显示绿色勾 + 模型数量
  → 失败: 显示红色叉 + 错误信息
```

---

## 4. 模型发现与同步

### 触发时机

1. 应用启动时
2. Admin 修改连接配置后
3. 用户手动刷新模型列表
4. 定时刷新（可配置）

### 同步策略

```python
# 每次同步都是全量替换内存缓存
app.state.MODELS = await get_all_models()
# 不持久化 base model 到 DB（除非用户主动 override）
```

---

## 5. 权限控制设计

### 模型级权限

```python
class AccessControl:
    read:
        group_ids: list[str]   # 哪些组可以看到/使用这个模型
        user_ids: list[str]    # 哪些用户可以看到/使用
    write:
        group_ids: list[str]   # 哪些组可以编辑
        user_ids: list[str]    # 哪些用户可以编辑
```

### 检查逻辑

```python
def has_access(user, model):
    if user.role == 'admin': return True
    if not model.access_control: return True  # 无限制 = 公开
    return user.id in read.user_ids or user.group in read.group_ids
```

---

## 6. 与 Prism 的对照

| 维度 | Open WebUI | Prism 当前 |
|------|-----------|-----------|
| 模型来源 | Provider API 自动发现 + DB 覆盖 | DB Model 表 + Provider ListModels |
| 模型配置 | DB 存 params/meta JSON blob | Model 表只有 Features/ParamSchema |
| 连接管理 | 多连接数组，每个有独立配置 | Channel + ChannelAccount |
| 路由 | urlIdx (连接索引) | Endpoint 优先级 + Account 加权 |
| 权限 | AccessGrants (group/user 级) | Token + 用户角色 |
| 模型预设 | Workspace Model (base_model_id) | 无此概念 |

### Prism 可借鉴的

1. **模型预设概念**: 允许管理员基于同一个 base model 创建多个预设（不同 system prompt + 参数）
2. **自动发现 + DB 覆盖**: 从 Channel 自动拉取模型列表，管理员可覆盖名称/配置
3. **模型级参数默认值**: 在 Model 表存 default_params，请求时自动合并

---

# 第二部分：UI 设计参考

## 7. 整体布局

```
┌──────────────────────────────────────────────────────────┐
│ Sidebar (可折叠)  │  Main Content                        │
│                   │                                      │
│ ┌───────────────┐ │  ┌────────────────────────────────┐  │
│ │ Logo + Name   │ │  │  Navbar (模型选择 + 操作)      │  │
│ ├───────────────┤ │  ├────────────────────────────────┤  │
│ │ New Chat      │ │  │                                │  │
│ ├───────────────┤ │  │  Messages Area                 │  │
│ │ Chat History  │ │  │  (flex-1, overflow-y-auto)     │  │
│ │ - Today       │ │  │                                │  │
│ │ - Yesterday   │ │  │                                │  │
│ │ - Last 7 days │ │  ├────────────────────────────────┤  │
│ │ ...           │ │  │  Message Input                 │  │
│ ├───────────────┤ │  │  (底部固定)                    │  │
│ │ User Menu     │ │  └────────────────────────────────┘  │
│ └───────────────┘ │                                      │
└──────────────────────────────────────────────────────────┘
```

## 8. 配色方案

```css
/* 核心: 黑白为主，极简 */
--bg-primary: white / #1a1a2e (dark)
--text-primary: #111 / #e0e0e0 (dark)
--border: rgba(0,0,0,0.05) / rgba(255,255,255,0.05)

/* 特点: 边框用极低透明度 */
border-color: rgba(0,0,0,0.1)  /* 而非实色 */
```

## 9. 组件风格

### 卡片/容器
```css
/* 大容器 */
.container { @apply rounded-3xl border border-gray-100/30 dark:border-gray-850/30; }

/* 列表项 */
.list-item { @apply px-3 py-2 hover:bg-black/5 dark:hover:bg-white/5 rounded-xl transition; }

/* Modal */
.modal { @apply rounded-2xl backdrop-blur-sm; }
```

### 按钮
```css
/* 主按钮: 黑白反转 */
.btn-primary { @apply bg-black text-white dark:bg-white dark:text-black hover:bg-gray-900 dark:hover:bg-gray-100 rounded-lg; }

/* 次要按钮 */
.btn-secondary { @apply bg-gray-100 dark:bg-gray-800 rounded-lg; }

/* 图标按钮 */
.btn-icon { @apply p-1.5 rounded-lg hover:bg-black/5 dark:hover:bg-white/5 transition; }
```

### 输入框
```css
/* 搜索框: 无边框，嵌入容器 */
.search { @apply bg-transparent outline-none placeholder-gray-400; }

/* 表单输入 */
.input { @apply bg-transparent border-b border-gray-200 dark:border-gray-700 outline-none; }
```

## 10. 列表设计模式

```svelte
<!-- 典型列表页结构 -->
<div class="rounded-3xl border border-gray-100/30">
  <!-- 搜索栏: 嵌入容器顶部 -->
  <div class="px-4 py-3 border-b border-gray-100/30">
    <input placeholder="Search..." class="bg-transparent outline-none w-full" />
  </div>
  
  <!-- 列表 -->
  <div class="overflow-y-auto max-h-[60vh]">
    {#each items as item}
      <div class="px-4 py-3 hover:bg-black/5 flex items-center justify-between">
        <div class="flex items-center gap-3">
          <img src={item.avatar} class="size-8 rounded-full" />
          <div>
            <div class="font-medium text-sm">{item.name}</div>
            <div class="text-xs text-gray-500">{item.description}</div>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <!-- 操作按钮 -->
        </div>
      </div>
    {/each}
  </div>
</div>
```

## 11. 状态指示

```svelte
<!-- Switch 开关 (替代图标按钮) -->
<Switch bind:state={item.enabled} />

<!-- Badge -->
<span class="px-2 py-0.5 text-xs rounded-full bg-green-500/20 text-green-700">Active</span>

<!-- 加载状态 -->
<Spinner className="size-4" />  <!-- SVG 旋转动画 -->
```

## 12. 响应式设计

```css
/* 移动端: 侧边栏变 overlay */
@media (max-width: 768px) {
  .sidebar { @apply fixed inset-y-0 left-0 z-50 transform -translate-x-full; }
  .sidebar.open { @apply translate-x-0; }
}

/* 桌面端: 侧边栏固定 */
@media (min-width: 768px) {
  .sidebar { @apply relative w-[260px] shrink-0; }
}
```

## 13. 动画

```css
/* 过渡 */
.transition-all { transition: all 150ms ease; }

/* 淡入 */
{#if show}
  <div transition:fade={{ duration: 100 }}>...</div>
{/if}

/* 滑入 */
{#if show}
  <div transition:slide={{ duration: 200 }}>...</div>
{/if}
```

## 14. 图标使用

- 使用内联 SVG（不依赖图标库）
- 统一 `size-4` / `size-5` 尺寸
- `strokeWidth="1.5"` 线条粗细
- 颜色继承父元素 `currentColor`

## 15. 表单设计

```svelte
<!-- 典型表单布局 -->
<div class="space-y-4">
  <div>
    <label class="text-xs font-medium text-gray-500 mb-1">{label}</label>
    <input class="w-full bg-transparent outline-none text-sm" />
  </div>
  
  <!-- 三态切换 -->
  <div class="flex justify-between items-center">
    <span class="text-xs">{label}</span>
    <button on:click={cycleState}>
      {#if value === true}On
      {:else if value === false}Off
      {:else}Default
      {/if}
    </button>
  </div>
</div>
```

## 16. 暗色模式

```css
/* 策略: CSS 变量 + dark: 前缀 */
.card {
  @apply bg-white dark:bg-gray-900;
  @apply border-gray-100 dark:border-gray-800;
  @apply text-gray-900 dark:text-gray-100;
}

/* 滚动条样式 */
::-webkit-scrollbar { height: 0.45rem; width: 0.45rem; }
::-webkit-scrollbar-thumb {
  background-color: rgba(215, 215, 215, 0.6);
  border-radius: 9999px;
}
```

---

## 17. 设计原则总结

| 原则 | Open WebUI 做法 | Prism 当前做法 |
|------|----------------|---------------|
| 圆角 | 4xl(modal) > 3xl(容器) > 2xl(卡片) > xl(按钮/项) | 统一 2xl |
| 边框 | 极淡 `/30` 透明度 | 实色 border-soft |
| 背景 | 同色 + border 区分层级 | 不同色区分 |
| 按钮 | 黑白反转 (主) / 灰底 (次) | 品牌色 primary |
| 状态 | Switch 开关 | 图标按钮 |
| 统计 | 无统计卡片，数字内联在标题旁 | 顶部统计卡片 |
| 搜索 | 无边框，嵌入容器 | 独立带边框 |
| 操作 | 右侧内联 (编辑+菜单+开关) | 右侧图标按钮组 |
| 空状态 | emoji + 文字居中 | 简单文字 |
| 加载 | Spinner SVG 动画 | animate-pulse 骨架 |

---

## 18. 可直接应用到 Prism 的改进

1. **去掉统计卡片** → 数字放标题旁 `Models {count}`
2. **列表容器** → 用 `rounded-3xl border border-[var(--border-soft)]/30` 包裹搜索+列表
3. **搜索框** → 无边框，嵌入容器顶部
4. **列表项** → `px-3 py-2 hover:bg-black/5 dark:hover:bg-white/5 rounded-xl`
5. **状态切换** → 用 Switch 替代 Power 图标
6. **Badge** → 用透明度背景 `bg-xxx-500/20`
7. **按钮** → 主按钮用黑白反转，次要用灰底
8. **Modal** → 加 `backdrop-blur-sm` + 更大圆角
9. **操作菜单** → 用 Dropdown 替代多个图标按钮
10. **空状态** → 加 emoji + 引导文字

---

# 第三部分：LLM 配置 + 对话 + 能力（对照 Prism 实际架构）

## 0. Prism 当前架构速览

```
Channel (渠道: openai/anthropic/google...)
  └── ChannelAccount (账号: API Key + 权重 + 并发限制)

Model (统一模型表)
  ├── Code, Name, Type(chat/image/video/audio/embedding)
  ├── Features: ["streaming","tools","vision","extended_thinking"]
  ├── ParamSchema: JSON (能力参数定义)
  └── MaxTokens

Endpoint (端点: 模型×渠道 的具体配置)
  ├── ModelCode → Model.Code
  ├── ChannelID → Channel.ID
  ├── Protocol (openai/anthropic/google/custom)
  ├── VendorModel (上游实际模型名)
  ├── InteractionMode (sync/stream/poll/callback)
  ├── PriceMode + InputPrice/OutputPrice
  ├── ParamMapping / ResponseMapping
  └── ExtraHeaders / ExtraConfig / Timeout / Priority

对话: Conversation → Message (含 ReasoningContent, Cost, LatencyMs)
能力: Model(type!=chat) → Endpoint → Provider.Submit() → Task (异步/同步)
```

## 1. 模型参数配置对比

### Open WebUI 的做法

四层参数覆盖：全局默认 → 模型级 (model.params JSON) → 会话级 (Chat Controls) → 请求级

模型级参数存在 `model.params` JSON blob 里，包含 system prompt + temperature + max_tokens 等。
有"专有参数"概念（stream_response, function_calling），发送前剥离不传给 LLM。

### Prism 当前的做法

**没有模型级参数配置**。参数完全由调用方在请求时传入：

```go
// internal/api/open/chat.go
req struct {
    Model, Messages, Temperature, MaxTokens, TopP,
    FrequencyPenalty, PresencePenalty, Stop, Stream,
    Tools, ToolChoice, ResponseFormat, Seed, User, ConversationID
}
```

Playground 前端 (ChatTab.tsx) 有完整参数面板，但这些参数不持久化到模型配置。

### 可借鉴的改进

| 改进 | 做法 |
|------|------|
| Model 加 `DefaultParams JSON` | 存 system_prompt + temperature 等默认值 |
| 请求时参数合并 | 请求参数 > 模型默认 > 全局默认，零值不覆盖 |
| Features 驱动前端 | 根据 Model.Features 动态显示/隐藏 Playground 参数项 |

## 2. 对话调用链对比

### Open WebUI

```
前端 → WebSocket → /api/chat/completions
  → 中间件管道 (Filter inlet → Knowledge RAG → Tools 注入 → Files 处理)
  → 路由到 Provider
  → 流式响应 → WebSocket 推送
  → Filter outlet
```

核心特点：中间件管道式处理，每步可修改请求；工具本地执行+循环调用。

### Prism

```
前端/API → POST /v1/chat/completions
  → Token 认证
  → UnifiedService.Route(tokenID, modelCode):
      查 Endpoint (按 priority) → 查 Channel → selectAccount (加权随机)
  → 流式: StreamComplete() → 直接代理上游 SSE
  → 非流式: Complete() → 转发 → 返回 JSON
  → 计费 + 日志 + Conversation/Message 持久化
```

核心特点：路由是核心逻辑；直接代理上游响应，不做中间处理。

### 关键差异

| 维度 | Open WebUI | Prism |
|------|-----------|-------|
| 请求处理 | 管道式中间件，层层修改 | 直接转发 |
| 工具调用 | 本地执行 Python 模块 + FC 循环 | 透传给上游 LLM |
| 对话持久化 | 完整消息树 (支持分支) | 线性 Conversation → Message |
| 路由 | urlIdx (连接索引) | Endpoint 优先级 + Account 加权随机 |
| 计费 | 无 | Token 余额扣费 |

### 可借鉴的改进

| 改进 | 做法 |
|------|------|
| 模型级 System Prompt | Route 后、发送前注入 Model.DefaultParams.system |
| 模型级参数覆盖 | Model 配了 temperature 默认值，请求没传则用默认 |

## 3. 能力系统对比

### Open WebUI 的"工具"

- 本地 Python 模块，绑定到模型 (meta.toolIds)
- 两种模式：Prompt 注入 / 原生 Function Calling
- 内置 40+ 工具 (web_search, memory, code_interpreter, calendar...)
- Capabilities 开关绑定模型 (vision, file_upload, web_search...)

### Prism 的"能力"

完全不同的概念——Prism 的 Capability 是**非对话类异步任务**：

```
POST /v1/capabilities/:capability (如 image_gen, video_gen)
  → findEndpointForCapability() 路由
  → 同步: Provider.Submit() 直接返回
  → 异步: 创建 Task → 入队 → Worker 执行 → 回调/轮询
```

Prism 的对话 Tools 是**直接透传**给上游 LLM，不做本地执行。

### 对照

| 概念 | Open WebUI | Prism |
|------|-----------|-------|
| 对话工具 | 本地执行 + 循环 | 透传上游 |
| 非对话能力 | 无 | Capability 异步任务 |
| 工具绑定模型 | meta.toolIds | 无 |
| 能力开关 | Capabilities 勾选 | Model.Features 标签 |

## 4. Playground 前端对比

### Prism 已经做得好的

1. **调试面板** — 能看到路由到哪个 channel/account/vendor_model + 延迟
2. **能力调用 Tab** — 独立处理非对话任务（图片/视频生成等）
3. **会话续接** — conversation_id 多轮对话
4. **文件上传** — image_url + file_url content parts
5. **流式 + 非流式** — 根据模型 supports_stream 自动切换

### 可从 Open WebUI 借鉴的

| 改进 | 具体做法 |
|------|---------|
| 模型级预设 | 管理员给模型配默认 system prompt + 参数，Playground 自动填充 |
| Features 驱动 UI | model.features 含 "vision" 时显示图片上传，含 "tools" 时显示 tools 输入 |
| 参数三态 | Default/Custom/Off 三态切换（而非简单数值输入） |
| 模型搜索+分类 | 模型多了之后需要搜索和标签过滤 |
