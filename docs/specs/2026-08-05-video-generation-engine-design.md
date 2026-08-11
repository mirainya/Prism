# 视频生成引擎设计文档

## 概述

重构 Prism 视频生成系统，从现有的"配置驱动 BaseProvider"单一模式，升级为统一媒体生成引擎 + 可插拔 Provider Adapter 架构。目标：

- **干净**：上传是上传，生成是生成，透传是透传，职责不混
- **优雅**：原生协议只实现 Codec，异步 HTTP 生命周期由通用执行器承担
- **灵活**：普通上游零代码配置接入；只有稳定、主流的官方协议进入原生协议层

## 系统边界声明

- `video_tasks`、`video_assets`、`video_channels`、`video_channel_keys` 保留视频资源与路由语义，不复用通用 `tasks` 状态机
- 所有视频请求必须接入统一 `api_calls`、`api_call_attempts`、计费流水和退款事务
- 复用 `pkg/filestorage` 管理素材公共 URL；`AssetResolver` 只负责生成上游可用引用
- Worker 租约采用与现有系统相同模式（Redis SET NX + TTL），独立实现于 video 包内

---

## 设计决策记录

| 决策 | 结论 | 理由 |
|------|------|------|
| API 风格 | Prism 自定义标准协议 | 不绑定任何上游，内部翻译 |
| 上传去重 | Prism 侧 SHA-256 去重 + AssetResolver 按需推上游 | 客户端只和 Prism 存储交互，上游推送由 Worker 内部完成 |
| 独家功能路由 | `/v1/videos/services/:service_name/**` | 按功能域命名，不暴露上游技术细节 |
| 计费模式 | 透传上游估价 × 加价系数 | 保证盈利，价格跟随上游 |
| 任务状态 | Prism 标准状态机，内部映射 | 对外可控 |
| Content 格式 | asset_id + URL 双模式，保留细分 role | 兼容方便 + 对齐官方语义 |
| 渠道区分 | 独立 video_channels 表，专属选路 | endpoint 只适合同步一进一出，视频多阶段+能力声明+透传绑定需要独立模型 |
| Adapter 定位 | 对标产品官方协议，中间商是 transport 细节 | 换通路不影响适配逻辑 |
| 原生协议边界 | Seedance 官方协议作为一等 Codec 保留 | 保留官方语义，但不复制 HTTP、轮询、重试和渠道代码 |
| 渠道接入原则 | 渠道优先绑定原生协议或 Generic Definition | 接入普通渠道不新增 Provider Go 包 |
| 新老共存 | adapter_type 字段区分，双轨并行 | 零停机迁移 |

---

## 1. 对外 API 接口

### 1.1 核心视频 API

```
POST   /v1/videos/generations            创建视频生成任务
GET    /v1/videos/generations             列表（按 token 分页）
GET    /v1/videos/generations/:id         查询任务状态/结果
POST   /v1/videos/generations/:id/cancel  取消任务
POST   /v1/videos/estimate               估价（不创建任务）
```

### 1.2 素材上传 API

```
POST   /v1/videos/assets                 上传文件或注册公网 URL
POST   /v1/videos/uploads                兼容别名
GET    /v1/videos/assets/:asset_id        查询素材状态
DELETE /v1/videos/assets/:asset_id        删除素材
```

客户端只和 Prism 存储交互，不感知上游渠道。素材推送给上游是 Submit Worker 内部的 AssetResolver 职责。

### 1.3 透传层 API

```
ANY    /v1/videos/services/:service_name/**   反代转发
```

### 1.4 创建任务请求体

```json
{
  "model": "seedance-2.0",
  "prompt": "使用图片1和视频1生成角色在海边转身的镜头",
  "resolution": "720p",
  "ratio": "16:9",
  "duration": 10,
  "generate_audio": true,
  "task_mode": "references",
  "content": [
    {"type": "image_url", "role": "first_frame", "asset_id": "vasset_xxx"},
    {"type": "image_url", "role": "reference_image", "url": "https://cdn.example.com/ref.png"},
    {"type": "video_url", "role": "reference_video", "asset_id": "vasset_yyy"},
    {"type": "audio_url", "role": "reference_audio", "asset_id": "vasset_zzz"}
  ],
  "callback_url": "https://your-site.com/callback"
}
```

`prompt` 与媒体素材至少提供一种。`task_mode` 可省略，Engine 自动推断：有非 text 类型 content → `references`，否则 → `text`。提示词按同类素材在 `content` 中的顺序使用 `图片1`、`视频1`、`音频1` 引用，不能使用 Asset ID 指代素材。

### 1.5 任务响应体

创建成功（HTTP 202）：

```json
{
  "id": "vtask_abc123",
  "status": "queued",
  "progress": 0,
  "model": "seedance-2.0",
  "estimated_cost": 1.25,
  "created_at": "2026-08-05T10:00:00Z"
}
```

生成完成：

```json
{
  "id": "vtask_abc123",
  "status": "completed",
  "progress": 100,
  "result": {
    "video_url": "https://prism.example.com/v1/videos/content/vtask_abc123",
    "duration": 10,
    "thumbnail_url": "..."
  },
  "cost": 1.25,
  "created_at": "...",
  "completed_at": "..."
}
```

### 1.6 标准状态枚举

```
queued      排队等待执行（Engine 创建 + 入队后立即设置）
submitted   已提交上游（Submit Worker 调 Adapter.Submit 成功后设置）
tracking    上游生成中（Submit 成功 → 自动进入；或 Poll 返回非终态）
completed   生成成功
failed      生成失败
cancelled   已取消
```

状态流转路径：
```
queued → submitted → tracking → completed
                              → failed
       → cancelled（未提交前可取消）
submitted → failed（提交失败直接终态）
tracking → cancelled（已提交但可调上游取消）
```

上游状态 → Prism 状态映射（Seedance Adapter）：
| Seedance status | Prism status | 说明 |
|----------------|--------------|------|
| queued | submitted | 已提交等待调度 |
| running | tracking | 生成进行中 |
| succeeded | completed | 成功，提取 result |
| failed | failed | 失败，提取 error |
| cancelled | cancelled | 被取消 |
| expired | failed | 上游任务过期 |

### 1.7 Content 类型与 Role 枚举

类型：`text` / `image_url` / `video_url` / `audio_url`

Role：
- `first_frame` — 首帧图（仅官满血）
- `last_frame` — 尾帧图（仅官满血）
- `reference_image` — 参考图
- `reference_video` — 参考视频
- `reference_audio` — 参考音频

素材引用方式（二选一）：
- `asset_id` — Prism 上传服务返回的素材 ID
- `url` — 公网可访问的直链 URL

Seedance 2.0 限制：图片最多 9 个、视频最多 3 个、音频最多 3 个；音频不能单独输入，必须同时提供图片或视频。

渠道能力差异：
| 渠道 | first/last_frame | reference_* | generate_audio | web_search | 取消 |
|------|-----------------|-------------|----------------|------------|------|
| 官满血 | 支持 | 支持 | 支持 | 支持 | 仅上游 queued 状态 |
| H渠道 | 不支持 | 支持 | 支持 | 不支持 | 仅本地队列阶段 |
| 2.5 | 不支持 | 支持（必须有） | 不支持 | 不支持 | 不支持 |

### 1.8 估价响应格式

成功：
```json
{
  "model": "seedance-2.0",
  "estimated_cost": 1.25,
  "currency": "credits"
}
```

### 1.9 错误响应格式

```json
{
  "error": {
    "code": "insufficient_balance",
    "message": "余额不足，预计费用 1.25"
  }
}
```

标准错误码：

| 错误码 | 含义 |
|--------|------|
| invalid_request | 参数校验失败 |
| model_not_found | 模型不存在/未启用 |
| asset_not_found | 素材不存在或已过期 |
| asset_not_ready | 素材尚未上传完成 |
| insufficient_balance | 余额不足 |
| estimate_failed | 估价失败 |
| generation_failed | 生成失败 |
| generation_timeout | 生成超时 |
| cancelled | 已取消 |
| service_unavailable | 无可用渠道 |

---

## 2. 内部架构

### 2.1 包结构

```
internal/video/
├── engine.go              核心引擎，驱动状态机
├── adapter.go             Adapter 接口定义
├── resolver.go            AssetResolver 接口定义
├── router.go              VideoRouter 选路
├── upload_service.go      客户端上传服务（Prism 存储）
├── estimate.go            估价逻辑
├── registry.go            Adapter 注册表
├── passthrough.go         透传反代
├── seedance/
│   └── adapter.go         Seedance 官方协议 Codec 与绑定配置
├── taskhttp/
│   ├── client.go          通用鉴权、HTTP、错误分类与响应限制
│   └── adapter.go         通用异步 submit/poll/cancel 执行器
├── generic/               配置驱动通用任务适配
│   ├── adapter.go         请求、响应和 HTTP 通信
│   ├── config.go          渠道配置解析与校验
│   └── validation.go      模型级请求约束
├── presigned_resolver.go  配置驱动的预签名上传
├── direct_url.go          公网 URL 直传
├── handler/
│   ├── generation.go      生成 HTTP handler
│   ├── upload.go          上传 HTTP handler
│   ├── estimate.go        估价 HTTP handler
│   └── passthrough.go     透传 HTTP handler
└── worker/
    ├── submit.go          提交 worker（含 AssetResolver 调用）
    ├── poll.go            轮询 worker
    └── notify.go          回调通知 worker
```

### 2.2 Adapter 接口

```go
type Adapter interface {
    BuildRequest(ctx context.Context, req *GenerateRequest) (*ProviderRequest, error)
    Submit(ctx context.Context, pr *ProviderRequest) (*SubmitResult, error)
    Poll(ctx context.Context, providerTaskID string) (*Progress, error)
}

// 可选能力
type Estimator interface {
    Estimate(ctx context.Context, req *GenerateRequest) (float64, error)
}

type Canceller interface {
    CanCancel(status TaskStatus) bool
    Cancel(ctx context.Context, providerTaskID string) error
}
```

`Adapter` 是 Engine 的运行时契约，不等同于“每个供应商一个 Go Adapter”。实现分为两类：

- 原生协议：官方协议 `Codec` + `taskhttp.Adapter`，Seedance 属于此类。
- 声明式协议：`generic.Adapter` 读取版本化映射配置。

原生协议 Codec 只负责可测试的 wire format：

```go
type Codec interface {
    BuildRequest(context.Context, *GenerateRequest) (*ProviderRequest, error)
    DecodeSubmit([]byte) (*SubmitResult, error)
    DecodePoll([]byte) (*Progress, error)
}
```

鉴权、URL、HTTP、响应大小限制、错误分类、submit/poll/cancel 和任务 ID 路径替换不得在原生协议包中重复实现。

### 2.3 核心数据结构

```go
type GenerateRequest struct {
    Model      string
    Prompt     string
    Resolution string
    Ratio      string
    Duration   int
    Audio      bool
    TaskMode   string // "text" / "references"（可省略，自动推断）
    Content    []ContentItem
    Params     map[string]any

    // Prism 内部上下文
    TaskID  string
    TokenID uint
    Channel *VideoChannel
    Key     *VideoChannelKey
}

type ContentItem struct {
    Type            string  // text / image_url / video_url / audio_url
    Role            string  // first_frame / last_frame / reference_image / reference_video / reference_audio
    Text            string
    AssetID         string  // Prism 内部素材 ID
    URL             string  // 公网直链（与 AssetID 二选一）
    DurationSeconds float64 // 可选素材元数据，不传给 Seedance 官方接口
}

type ProviderRequest struct {
    Body    map[string]any
    Headers map[string]string
}

type SubmitResult struct {
    ProviderTaskID string
    Status         TaskStatus
    Result         *GenerationResult // 同步模式直接返回结果
}

type Progress struct {
    Status  TaskStatus
    Percent int
    Result  *GenerationResult
    Error   string
}

type GenerationResult struct {
    VideoURL     string
    ThumbnailURL string
    Duration     float64
}
```

### 2.4 Adapter 注册表

```go
type AdapterRegistry struct {
    factories map[string]AdapterFactory
}

type AdapterFactory func(channel *VideoChannel, key *VideoChannelKey) Adapter

func (r *AdapterRegistry) Register(name string, f AdapterFactory)
func (r *AdapterRegistry) Get(adapterType string, ch *VideoChannel, key *VideoChannelKey) Adapter
```

启动时注册：

```go
registry.Register("generic", generic.NewAdapter)
registry.Register("seedance", seedance.NewAdapter)
```

### 2.5 Generic Adapter 设计

Generic Adapter 从渠道的 `extra_config` 配置动态生成行为，无需写代码：

```go
type GenericConfig struct {
    SubmitPath    string            // POST 路径模板: "/v1/video/generate"
    PollPath      string            // GET 路径模板: "/v1/video/{{.TaskID}}"
    CancelPath    string            // POST（可选）
    RequestMapping map[string]string // Prism 字段→上游字段: {"prompt":"text", "model":"model_name"}
    StatusMapping  map[string]string // 上游状态→Prism状态: {"success":"completed", "processing":"tracking"}
    ResultPath     string           // JSON Path 提取结果 URL: "data.video_url"
    ProgressPath   string           // JSON Path 提取进度: "data.progress"
}
```

与现有 BaseProvider 的区别在**执行模式**：BaseProvider 是同步/SSE 适合 Chat/Image，Generic Adapter 是异步三阶段（submit/poll/cancel），覆盖简单视频供应商（如 grok）。两者不互相替代。

`generic.Adapter` 与原生协议共用 `taskhttp.Client`。渠道密钥只在 HTTP 发出前注入，不进入 `ProviderRequest`，避免协议转换结果携带凭据。

新增渠道时先判断其 wire format：兼容 Seedance 官方格式则复用 Seedance Codec；普通 JSON 任务协议使用 Generic Definition。只有出现无法由现有执行原语表达、且可被多个上游复用的新机制时，才允许新增 Go 插件。

### 2.6 Engine 核心流程

```go
func (e *Engine) Execute(ctx context.Context, req *GenerateRequest) (string, error) {
    // 1. 校验 content 引用（asset_id 存在且 status=ready 且未过期）
    // 2. 选择渠道和 Key（VideoRouter：模型匹配→能力过滤→优先级→权重→熔断）
    // 3. 估价（Adapter 实现 Estimator 则调上游，否则读 channel.pricing.fixed_price）
    // 4. 预扣费（估价 × markup_ratio）
    // 5. 创建 video_tasks 记录（status=queued）
    // 6. 入队 video:submit
    return taskID, nil
}
```

### 2.7 取消流程

取消能力因渠道而异，由 Adapter 声明：

```go
// 可选接口：Adapter 不实现则该渠道完全不支持取消
type Canceller interface {
    // CanCancel 判断当前状态是否允许取消
    CanCancel(status TaskStatus) bool
    Cancel(ctx context.Context, providerTaskID string) error
}
```

Engine 取消逻辑：

```go
func (e *Engine) Cancel(ctx context.Context, taskID string) error {
    vTask := load(taskID)

    // 终态不可取消
    if vTask.Status.IsTerminal() {
        return ErrAlreadyTerminal
    }

    // Adapter 不实现 Canceller → 不支持取消
    c, ok := adapter.(Canceller)
    if !ok {
        return ErrCancelNotSupported
    }

    // Adapter 判断当前状态是否允许取消
    if !c.CanCancel(vTask.Status) {
        return ErrCancelNotAllowed
    }

    switch vTask.Status {
    case "queued":
        vTask.Status = "cancelled"
        refundFull(vTask)
    case "submitted":
        if err := c.Cancel(ctx, vTask.ProviderTaskID); err != nil {
            return err // 上游拒绝取消
        }
        vTask.Status = "cancelled"
        refundFull(vTask)
    }
}
```

### 2.8 VideoRouter 选路

独立于现有 QueryService，针对视频多阶段特性设计：

```go
type VideoRouter struct {
    db    *gorm.DB
    redis *redis.Client
}

func (r *VideoRouter) Select(ctx context.Context, model string, caps RequiredCaps) (*VideoChannel, *VideoChannelKey, error) {
    // 1. 按 model 过滤 active 渠道（channels.models JSON 包含该模型）
    // 2. 按 capabilities 过滤（请求需要 first_frame → 只留声明支持的渠道）
    // 3. 按 priority DESC 排序
    // 4. 同优先级内按 key weight 加权随机选 key
    // 5. 检查 key 并发（max_concurrency > 0 时 Redis INCR 检查）
    // 6. 熔断检查（Redis 记录连续失败次数，超阈值跳过）
}

type RequiredCaps struct {
    FirstFrame bool
    LastFrame  bool
    Cancel     bool
    Audio      bool
    WebSearch  bool
}
```

与现有 QueryService 的区别：
- 按 capabilities JSON 字段过滤，不是按 model_code 精确匹配
- 并发控制在 key 级别（非请求级别），用 Redis 原子计数
- 熔断粒度是 channel+key（不是 endpoint+account）

---

## 3. 上传服务

### 3.1 核心抽象：AssetResolver

上传服务的本质不是"上传文件"，而是"确保素材在上游可用"。不同渠道获取上游可用素材的方式完全不同：

```go
// AssetResolver — 负责"确保素材在目标渠道可用并返回引用标识"
type AssetResolver interface {
    // Prepare 在任务提交前调用，返回上游可用的素材引用
    // 输入：Prism 内部 asset（含本地元信息）
    // 输出：上游引用标识（object_id / URL / base64 等，由实现决定）
    Prepare(ctx context.Context, asset *VideoAsset) (*ResolvedAsset, error)
}

type ResolvedAsset struct {
    RefType string // "provider_object" / "url"
    RefValue string // 具体引用值
}
```

实现：

| 实现 | 适用渠道 | 工作方式 |
|------|----------|----------|
| PresignedUploadResolver | 需要先登记素材的渠道 | 配置驱动 apply/wait/upload/complete/abort，返回上游对象引用 |
| DirectURLResolver | 官方直连 / 通用 | 素材已有公网 URL，直接透传 |

### 3.2 客户端上传 API

上传 API 是 Prism 对外的统一入口，与上游无关。客户端上传素材到 Prism 管理的存储，拿到 `asset_id`：

```
POST   /v1/videos/uploads                申请上传
POST   /v1/videos/uploads/complete        确认上传完成
GET    /v1/videos/assets/:asset_id        查询素材状态
DELETE /v1/videos/assets/:asset_id        删除素材
```

关键设计：**上传与渠道解耦**。客户端上传素材时不需要知道最终走哪个渠道，Prism 先收到本地存储，任务创建时由 `AssetResolver` 按目标渠道的方式"推送"给上游。

### 3.3 上传流程（Prism 侧）

```
客户端                         Prism
  │ POST /v1/videos/uploads      │
  │  {sha256, size, kind, type}  │
  │─────────────────────────────>│ 本地去重：SHA-256 命中？
  │                              │  命中 → 返回已有 asset_id
  │                              │  未命中 → 签发 presigned URL（Prism 自建存储）
  │  {asset_id, upload_url}      │
  │<─────────────────────────────│
  │                              │
  │ PUT upload_url (直传存储)     │
  │─────────────────────────────>│ (Prism S3/OSS/本地磁盘)
  │                              │
  │ POST /v1/videos/uploads/     │
  │      complete                │
  │  {asset_id}                  │
  │─────────────────────────────>│ 校验 size + SHA-256，标记 ready
  │  {asset_id, status: ready}   │
  │<─────────────────────────────│
```

素材存在 **Prism 自己控制的存储**中。任务提交时 AssetResolver 按需推送给上游。

### 3.4 AssetResolver 实现：PresignedUploadResolver

部分渠道要求先登记素材，再通过预签名 URL 上传到对象存储。Prism 用 `presigned_upload` 插件实现，协议路径、鉴权头、重试和引用提取均由渠道配置提供；`disposition_v1` 约束状态机语义：

```go
type PresignedUploadResolver struct {
    baseURL string
    config  PresignedUploadConfig
    client  *http.Client
}

func (r *PresignedUploadResolver) Prepare(ctx context.Context, asset *VideoAsset) (*ResolvedAsset, error) {
    // 1. 按 channel + key + profile 检查上游引用缓存
    // 2. 无缓存 → 调配置的 apply_path：
    //    - owner: 从 Prism 存储读取素材 → PUT 预签名 URL → complete
    //    - waiting: 轮询 WaitUpload → 最终拿到 object_id
    //    - reused: 直接返回 object_id
    // 3. 按配置路径提取引用并缓存到 asset 记录
    return &ResolvedAsset{RefType: "provider_object", RefValue: providerReference}, nil
}
```

`disposition_v1` 细节（内部实现，不暴露给客户端）：
- **owner** — Prism 是上传者，下载本地素材 → PUT presigned URL → Complete
- **waiting** — 相同 SHA-256 正在被其他会话上传，轮询等待
- **reused** — 已存在，直接拿 object_id

### 3.5 AssetResolver 实现：DirectURLResolver

官方直连或通用渠道，素材用公网 URL 直接传递：

```go
type DirectURLResolver struct {
    cdnBase string // Prism 素材 CDN 前缀
}

func (r *DirectURLResolver) Prepare(ctx context.Context, asset *VideoAsset) (*ResolvedAsset, error) {
    // 素材已在 Prism 存储，生成公网可访问 URL
    url := r.cdnBase + "/" + asset.StoragePath
    return &ResolvedAsset{RefType: "url", RefValue: url}, nil
}
```

### 3.6 去重逻辑

同 TokenID + 同 SHA-256 + status=ready + 未过期 → 直接返回已有 asset_id，跳过上传。

PresignedUploadResolver 维护 `upstream_refs` 缓存：相同素材推送到同一渠道密钥后缓存上游引用，在引用有效期内免重传。

### 3.7 素材过期清理

- 定时任务（每小时扫描 `status=ready AND expires_at < NOW()`）
- 标记 `status=expired`（软删除，不立即删存储文件）
- 存储文件清理：标记 expired 后 24h 再删对象存储（给未完成任务缓冲）
- 任务创建时若引用 expired 素材 → 返回 `asset_not_found` 错误
- 任务入队后素材过期 → Submit Worker 在 BuildRequest 阶段检查，失败则 fail 任务

### 3.8 上传校验

`/uploads/complete` 接口校验逻辑：
- HeadObject 获取 Content-Length 校验 size
- SHA-256 校验：客户端上传时设置 `x-amz-checksum-sha256` 头（S3/R2 原生支持），后端 HeadObject 读 checksum 比对
- 若存储后端不支持 checksum 头：信任客户端声明（降级模式，配置开关）

---

## 4. 计费与结算

### 4.1 估价流程

```
请求 → 找 Adapter → 实现 Estimator？
  是 → 调上游估价 → 成本价
  否 → 读渠道 pricing.fixed_price → 成本价

成本价 × markup_ratio → 客户端预估价
```

### 4.2 计费配置

```go
type VideoPricing struct {
    Mode        string  // "upstream_estimate" | "fixed"
    FixedPrice  float64 // mode=fixed
    MarkupRatio float64 // 加价系数
}
```

配置层级：渠道默认 → 模型覆盖。

### 4.3 结算规则

| 场景 | 处理 |
|------|------|
| 任务完成 | 按实际成本 × markup 结算，多扣的退回 |
| 任务失败 + 上游退款 | Prism 全额退款 |
| 任务失败 + 上游扣了部分 | 按比例退 |
| 任务失败 + 无法确认上游 | 全退（宁亏不坑） |
| 任务取消（未提交上游） | 全额退款 |

---

## 5. 透传层

### 5.1 服务配置

透传服务配置内嵌于 `video_channels.passthrough` JSON 字段，不独立建表：

```go
// 从 video_channels 读取透传配置
type PassthroughConfig struct {
    LongStorage    bool // 是否支持长期素材库透传
    MaterialAssets bool // 是否支持真人素材库透传
}
```

透传请求使用渠道的 `base_url` + 对应 key，复用渠道鉴权。

### 5.2 Handler 逻辑

```go
func passthroughHandler(c *gin.Context) {
    serviceName := c.Param("service_name")
    path := c.Param("path")

    // 找支持该透传服务的渠道
    channel, key := findPassthroughChannel(serviceName)
    // 1. 验证 Prism token
    // 2. 构造上游请求：channel.BaseURL + path
    // 3. 注入上游鉴权头（key.ApiKey），移除客户端 Prism token
    // 4. 透传 body + query + 非敏感 headers
    // 5. 返回上游原始响应
}
```

### 5.3 安全边界

- 客户端 Prism Authorization 不转发给上游
- 透传层不经过生成引擎，不记 Task，不走计费
- 需要计费的透传接口（如长期素材库购买/续费）加轻量 billing hook

### 5.4 透传范围界定

以下功能**全部走透传层**，不进核心生成引擎：
- 长期素材库（long-storage/*）— 购买/续费/上传/管理/预览
- 真人素材库（material-assets/*）— 认证/上传/查询/绑定
- 能力查询（capabilities）— 纯转发
- 活跃计数 / WebSocket（active-count / ws/active-count）

以下功能**走核心引擎**：
- 任务创建 / 查询 / 取消 / 刷新 / 估价
- 素材上传三步流程（uploads → PUT → complete）
- 任务结果内容代理（content / last-frame）

---

## 6. 数据模型

### 6.1 video_assets 表

```sql
CREATE TABLE video_assets (
    id                  VARCHAR(32) PRIMARY KEY,
    token_id            BIGINT UNSIGNED NOT NULL,
    sha256              CHAR(64) NOT NULL,
    size_bytes          BIGINT NOT NULL,
    kind                VARCHAR(16) NOT NULL,        -- image/video/audio
    content_type        VARCHAR(64) NOT NULL,
    duration_seconds    DECIMAL(6,2),
    status              VARCHAR(16) NOT NULL,        -- uploading/ready/expired
    storage_path        VARCHAR(256),                -- Prism 存储路径（S3 key / 本地路径）
    upstream_refs       JSON,                        -- 按 resolver/channel/key/profile 缓存上游引用和有效期
    expires_at          DATETIME NOT NULL,
    created_at          DATETIME NOT NULL,
    INDEX idx_token_sha256 (token_id, sha256, status),
    INDEX idx_expires (status, expires_at)
);
```

状态说明：
- `uploading` — 已签发 presigned URL，等客户端 PUT + complete
- `ready` — 上传完成，可用于创建任务
- `expired` — 已过期，不可使用

`upstream_refs` 字段缓存 AssetResolver 推送成功后的引用标识，避免同素材多次推送同一渠道。

### 6.2 video_channels 表

```sql
CREATE TABLE video_channels (
    id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name            VARCHAR(64) NOT NULL,
    adapter_type    VARCHAR(32) NOT NULL,         -- seedance / generic
    base_url        VARCHAR(256) NOT NULL,        -- 上游地址（任务+透传）
    status          VARCHAR(16) DEFAULT 'active', -- active/disabled
    priority        INT DEFAULT 0,               -- 选路优先级（数值越大越优先）
    models          JSON NOT NULL,               -- 支持的模型列表 ["seedance-2.0","seedance-2.5"]
    capabilities    JSON,                        -- 能力声明 {"first_frame":true,"cancel":true,"audio":true,"web_search":true}
    pricing         JSON,                        -- {"mode":"fixed","markup_ratio":1.3,"fixed_price":1}
    asset_resolver  VARCHAR(32) DEFAULT 'direct_url', -- direct_url / presigned_upload
    passthrough     JSON,                        -- 透传服务配置 {"long_storage":true,"material_assets":false}
    extra_config    JSON,                        -- adapter 特有配置
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL
);
```

### 6.3 video_channel_keys 表

```sql
CREATE TABLE video_channel_keys (
    id           BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    channel_id   BIGINT UNSIGNED NOT NULL,
    api_key      VARCHAR(256) NOT NULL,
    label        VARCHAR(64),                    -- 人类可读标签
    weight       INT DEFAULT 1,                  -- 同渠道内权重
    max_concurrency INT DEFAULT 0,              -- 0=不限
    status       VARCHAR(16) DEFAULT 'active',   -- active/disabled/exhausted
    current_concurrency INT DEFAULT 0,
    total_calls  BIGINT DEFAULT 0,
    last_used_at DATETIME,
    created_at   DATETIME NOT NULL,
    INDEX idx_channel_status (channel_id, status),
    FOREIGN KEY (channel_id) REFERENCES video_channels(id)
);
```

### 6.4 video_tasks 表

```sql
CREATE TABLE video_tasks (
    id               VARCHAR(32) PRIMARY KEY,
    token_id         BIGINT UNSIGNED NOT NULL,
    model            VARCHAR(64) NOT NULL,
    status           VARCHAR(16) NOT NULL,
    progress         TINYINT UNSIGNED DEFAULT 0,
    task_mode        VARCHAR(16) NOT NULL,        -- text/references
    prompt           TEXT,
    resolution       VARCHAR(16),
    ratio            VARCHAR(16),
    duration         SMALLINT,
    generate_audio   BOOLEAN DEFAULT FALSE,
    content_json     JSON,
    params_json      JSON,
    channel_id       BIGINT UNSIGNED,
    key_id           BIGINT UNSIGNED,
    adapter_type     VARCHAR(32) NOT NULL,
    provider_task_id VARCHAR(128),
    provider_response JSON,
    estimated_cost   DECIMAL(10,4),
    markup_ratio     DECIMAL(4,2),
    final_cost       DECIMAL(10,4),
    billing_status   VARCHAR(16),                 -- pending/charged/refunded/partial_refund
    result_json      JSON,
    error_message    TEXT,
    worker_lease     VARCHAR(64),
    poll_count       INT DEFAULT 0,
    callback_url     VARCHAR(512),
    created_at       DATETIME NOT NULL,
    submitted_at     DATETIME,
    completed_at     DATETIME,
    INDEX idx_token_status (token_id, status, created_at DESC),
    INDEX idx_status_created (status, created_at),
    INDEX idx_channel_key (channel_id, key_id)
    -- 刻意不加 FK：渠道/Key 删除后历史任务仍需保留关联记录用于审计
);
```

### 6.5 与现有系统关系

- `video_channels` + `video_channel_keys` **完全独立于** `endpoints` + `endpoint_accounts`
- `video_tasks` 独立于现有 `tasks` 表（字段差异大，状态机不同，Worker 不同）
- 计费写入现有 `balance_entries` 流水表（复用）
- Token 鉴权复用现有 `api_tokens` 体系
- 透传服务配置内嵌于 `video_channels.passthrough`，不再需要独立 `video_services` 表

---

## 7. Worker 集成

### 7.1 Asynq Task 类型

```go
const (
    TypeVideoSubmit = "video:submit"
    TypeVideoPoll   = "video:poll"
    TypeVideoNotify = "video:notify"
)
```

### 7.2 Submit Worker

```go
func (w *VideoSubmitWorker) Handle(ctx, task) error {
    vTask := load(taskID)
    if vTask.Status != "queued" { return nil } // 幂等
    channel, key := loadChannelKey(vTask.ChannelID, vTask.KeyID)
    adapter := registry.Get(vTask.AdapterType, channel, key)
    resolver := resolverFactory.Get(channel.AssetResolver, channel, key)

    // 素材解析：将 Prism 存储中的素材推送到上游
    for _, asset := range vTask.Assets {
        resolved, err := resolver.Prepare(ctx, asset)
        if err != nil { return fail(vTask, err) }
        asset.SetResolved(resolved) // 写入 upstream_refs 缓存
    }

    // 构建请求（使用已解析的素材引用）
    pr, err := adapter.BuildRequest(ctx, toRequest(vTask))
    if err != nil { return fail(vTask, err) }

    // checkpoint: 标记 submitted（崩溃恢复点）
    vTask.Status = "submitted"
    save(vTask)

    // 提交上游
    result, err := adapter.Submit(ctx, pr)
    if err != nil { return fail(vTask, err) }

    // 同步完成？（简单供应商可能直接返回结果）
    if result.Result != nil {
        return complete(vTask, result.Result)
    }

    // 异步：进入轮询
    vTask.ProviderTaskID = result.ProviderTaskID
    vTask.Status = "tracking"
    save(vTask)
    enqueue(TypeVideoPoll, taskID, delay=5s)
    return nil
}
```

### 7.3 Poll Worker

```go
func (w *VideoPollWorker) Handle(ctx, task) error {
    vTask := load(taskID)
    channel, key := loadChannelKey(vTask.ChannelID, vTask.KeyID)
    adapter := registry.Get(vTask.AdapterType, channel, key)
    progress := adapter.Poll(ctx, vTask.ProviderTaskID)
    switch progress.Status {
    case Completed: complete(vTask, progress.Result)
    case Failed:    fail(vTask, progress.Error)
    default:        updateProgress(vTask); re-enqueue()
    }
}
```

### 7.4 Notify Worker

触发时机：任务到达终态（completed / failed / cancelled）且 `callback_url` 非空。

```go
func (w *VideoNotifyWorker) Handle(ctx, task) error {
    vTask := load(taskID)
    payload := buildCallbackPayload(vTask) // 与 GET /generations/:id 响应结构一致
    resp, err := httpClient.Post(vTask.CallbackURL, payload)
    if err != nil || resp.StatusCode >= 500 {
        return retryable(err) // Asynq 指数退避，最多重试 5 次
    }
    // 2xx/4xx 视为送达（4xx 说明对方不接受，重试无意义）
    return nil
}
```

### 7.5 超时保护

定时扫描 `status=tracking` 且 `submitted_at` 超阈值（默认 30 分钟）的任务，标记 failed + 退款。

### 7.6 Worker 租约机制

```
获取租约: Redis SET video:lease:{taskID} {workerID} NX EX 60
续期:    Worker 每 20s 续 TTL（长轮询 adapter 在等待时续期）
释放:    任务到终态后 DEL key
竞争保护: Handle 入口先 SET NX，失败则 return nil（其他实例已接管）
崩溃恢复: 60s TTL 到期后，Asynq retry 重新投递，新实例获取租约继续
```

与现有 `AcquireTaskWorkerLease` 模式相同，独立 key 前缀 `video:lease:` 避免冲突。

---

## 8. 错误处理与重试

### 8.1 错误分类

| 类型 | 条件 | 处理 |
|------|------|------|
| Retryable | 网络超时、5xx、429 | Asynq 指数退避重试 |
| Fatal | 4xx 客户端错误 | 直接 failed + 退款 |
| Ambiguous | 提交后网络断 | checkpoint 恢复，先查再决策 |

### 8.2 幂等保障

- video_task ID 基于请求内容哈希生成，防重复创建
- Submit 阶段 checkpoint：提交前写入，崩溃恢复时先查上游状态
- Seedance adapter 通过 X-Request-ID 头实现上游幂等

---

## 9. 路由分发与迁移

### 9.1 新老系统共存

```go
func VideoGenerationHandler(c *gin.Context) {
    // 优先查 video_channels 有无匹配该 model 的渠道
    channel, key, err := videoRouter.Select(ctx, model, caps)
    if err == nil {
        videoEngine.Execute(c, req)    // 新引擎
        return
    }
    // fallback: 走老链路（CapabilityService）
    capabilityService.Invoke(...)
}
```

### 9.2 迁移步骤

1. 部署新引擎代码（video_channels 表为空，全部走老链路）
2. 管理员创建 video_channel（adapter_type=seedance，绑 Key），新模型立即走新引擎
3. 验证稳定后，把 grok 等简单供应商创建 generic 类型渠道
4. 全部切完后下线老链路代码

---

## 10. 管理后台

### 10.1 新增接口

```
GET    /api/admin/video/channels            列出视频渠道
POST   /api/admin/video/channels            创建渠道
PUT    /api/admin/video/channels/:id        更新渠道
DELETE /api/admin/video/channels/:id        删除渠道
GET    /api/admin/video/channels/:id/keys   列出渠道 Key
POST   /api/admin/video/channels/:id/keys   添加 Key
PUT    /api/admin/video/keys/:id            更新 Key
DELETE /api/admin/video/keys/:id            删除 Key
GET    /api/admin/video/tasks               视频任务列表
GET    /api/admin/video/stats               统计
```

### 10.2 Seedance 接入配置示例

1. 创建渠道：name=`seedance-官方`，adapter_type=`seedance`，base_url=`https://ark.cn-beijing.volces.com`，models=`["seedance-2.0"]`，capabilities=`{"first_frame":true,"last_frame":true,"cancel":true,"audio":true}`，pricing=`{"mode":"fixed","fixed_price":1,"markup_ratio":1.3}`
2. 添加火山方舟 API Key，weight=1
3. `extra_config` 通常留空；适配器默认使用 `/api/v3/contents/generations/tasks` 创建、查询和删除任务
