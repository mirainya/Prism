# Seedance 满血官逆接入设计

日期：2026-08-27

## 1. 目标

在 Prism 现有视频引擎中接入 Seedance 满血官逆渠道，首期覆盖文生视频、首帧、首尾帧和多模态参考生成，并保留生成音频、联网增强和返回尾帧能力。

本次只接官逆，不改火山方舟直连 `seedance` Adapter，也不混入 H 渠道或即梦渠道规则。

## 2. 实现结论

采用现有 `generic` Adapter，协议档案使用 `json_task_v1`。渠道差异全部写入 `video_channels.extra_config.adapter`，不新增 Seedance 官逆专用 Go Adapter。

原因：官逆仍是标准 JSON 异步任务协议，提交、查询、取消、能力发现和字段映射均可由 Generic Adapter 完整表达。

## 3. 当前基线

项目已有以下可复用能力：

- Prism V1 视频入口：`POST /v1/videos/generations`
- 统一素材接口：`POST /v1/videos/assets`
- Generic Adapter 的提交、轮询、取消、能力发现和响应字段提取
- `presigned_upload` 素材上传与上游对象引用缓存
- `X-Request-ID` 幂等请求头
- `video_tasks`、`api_calls`、`api_call_attempts` 和调用正文日志
- 渠道与 Key 的优先级、权重、并发及熔断
- 按渠道开关的视频结果持久化

历史迁移中已有“官满血-Seedance”配置，但需要重新按当前代码生成新迁移，不能修改已执行迁移。

当前配置还缺少 `camera_fixed` 参数声明。Generic Adapter 会拒绝未声明参数，因此本次必须补齐。

历史迁移还直接开启了官逆的上游取消，但仓库中没有真实上游契约或联调记录证明该接口可用。现有取消测试使用模拟服务器，不能作为上游支持证据。因此新配置必须默认关闭上游取消。

## 4. 渠道定义

| 项目 | 配置 |
|---|---|
| 渠道名称 | `官满血-Seedance` |
| Adapter | `generic` |
| Profile | `json_task_v1` |
| 鉴权 | `Authorization: Bearer <API_KEY>` |
| 素材解析器 | `presigned_upload` |
| 结果持久化 | 默认关闭 |
| 上游取消 | 默认关闭，取得真实上游证明后再启用 |
| 本地取消 | 仅限尚未提交上游的 `queued` 任务 |
| 路由优先级 | 高于 H 渠道，具体值沿用生产配置 |
| 模型发现 | 启用，静态配置作为能力上限 |

模型使用公开名到上游名映射。首期公开模型保持：

| Prism 模型 | 上游模型 |
|---|---|
| `seedance-2.0` | `seedance-2.0` |
| `seedance-2.0-fast` | `seedance-2.0-fast` |

上游能力接口未返回的模型不得展示或参与选路。

## 5. 能力上限

| 能力 | `seedance-2.0` | `seedance-2.0-fast` |
|---|---|---|
| 时长 | 4-15 秒 | 4-15 秒 |
| 分辨率 | 480p、720p、1080p、4k | 480p、720p |
| 比例 | adaptive、16:9、4:3、1:1、3:4、9:16、21:9 | 同左 |
| 文生视频 | 支持 | 支持 |
| 首帧 / 首尾帧 | 支持 | 支持 |
| 多模态参考 | 支持 | 支持 |
| 生成音频 | 支持 | 支持 |
| 联网增强 | 支持 | 支持 |
| 返回尾帧 | 支持 | 支持 |
| 固定镜头 | 支持 | 支持 |
| 上游取消 | 未证实，默认关闭 | 未证实，默认关闭 |

素材上限：图片 9 个、视频 3 个、音频 3 个、总素材 15 个；单个及同类视频/音频总时长不超过 15 秒。能力发现只能缩小这些范围，不能扩大静态上限。

## 6. Prism 对外请求

客户端继续调用统一接口，不直接提交官逆字段：

```json
{
  "model": "seedance-2.0",
  "prompt": "镜头缓慢推进，海边城市进入夜晚",
  "duration": 5,
  "resolution": "1080p",
  "aspect_ratio": "16:9",
  "generate_audio": true,
  "references": [
    {
      "type": "image",
      "role": "first_frame",
      "asset_id": "asset_xxx"
    }
  ],
  "provider_options": {
    "seedance": {
      "camera_fixed": false,
      "return_last_frame": true,
      "web_search": true
    }
  },
  "callback_url": "https://example.com/video-callback"
}
```

任务类型由 `references[].role` 推导，不允许客户端提交旧字段 `task_mode`、`content`、`params` 或 `ratio`。

## 7. 官逆上游协议

| 操作 | 请求 |
|---|---|
| 能力发现 | `GET /v1/video-generations/capabilities` |
| 创建任务 | `POST /v1/video-generations` |
| 查询任务 | `GET /v1/video-generations/{task_id}` |
| 取消任务 | 首期不调用；只有上游文档与联调均确认后才配置 |

创建请求映射：

| Prism 语义 | 官逆字段 |
|---|---|
| 上游模型 | `model` |
| 提示词 | `prompt` |
| 分辨率 | `resolution` |
| 画面比例 | `ratio` |
| 时长 | `duration` |
| 生成音频 | `generate_audio` |
| 任务模式 | `task_mode` |
| 素材 | `content[]` |
| 幂等 ID | `X-Request-ID` 请求头 |

`provider_options.seedance` 映射为顶层 `camera_fixed`、`return_last_frame`、`web_search`。三项均必须声明为布尔参数，未知扩展参数直接拒绝。

素材项只发送 `type`、`role`、`url` 或 `storage_object_id`、`duration_seconds`。禁止向上游请求或数据库写入 Base64/Data URL。

## 8. 响应与状态

兼容直接响应和 `{code, data}` 包装。任务 ID 从 `data.id` 或 `id` 读取；结果 URL 从 `data.result.video_url`、`data.content.video_url` 等已配置路径读取。

| 上游状态 | Prism 状态 |
|---|---|
| queued、created、submitted、pending、waiting | submitted |
| running、processing、in_progress | tracking |
| succeeded、success、completed | completed |
| failed、error、expired | failed |
| canceled、cancelled | cancelled；仅用于上游确实返回该状态的兼容处理 |

未知状态保持 `tracking` 并继续轮询，不得误判成功。

## 9. 素材与结果策略

- 用户上传素材先保存为 `video_asset`，官逆渠道再通过 `presigned_upload` 获取上游对象引用。
- 相同素材复用未过期的上游引用，避免重复上传。
- 请求日志保存 URL、对象 ID 和结构化参数，不保存二进制正文。
- `extra_config.result_storage.enabled` 默认 `false`，完成后直接保存上游视频 URL。
- 仅当上游结果 URL 短时失效且管理员显式开启持久化时，Prism 才转存结果。
- 转存时只允许配置的可信结果域名，失败不能把任务错误标记为生成失败。

## 10. 计费、日志与可靠性

- 创建前估价并预扣；无上游估价接口时使用渠道固定价格。
- 任务成功后结算，失败或取消后退款。
- `queued` 任务可在 Prism 本地取消；取得上游任务 ID 后，官逆渠道默认返回“不支持取消”。
- 创建、轮询和取消均写入调用记录及上游尝试。
- 保存实际上游请求 JSON、响应 JSON、HTTP 状态和耗时，密钥必须脱敏。
- 429、超时和 5xx 可重试；确定的 4xx 直接失败。
- 提交结果不明确时依赖 `X-Request-ID` 与 checkpoint 恢复，禁止盲目重复创建。
- 任务保存不可变 RoutePlan，渠道后续修改不影响运行中的任务。

## 11. 实施内容

1. 新建迁移，更新或创建官满血渠道，不修改历史迁移。
2. 配置模型映射、能力上限、能力发现及请求/响应映射；保持上游取消关闭。
3. 为两个模型声明 `camera_fixed`、`return_last_frame`、`web_search` 参数。
4. 保持 `result_storage.enabled=false`。
5. 补充 Generic Adapter 配置测试、模型能力接口测试及完整任务生命周期测试。
6. 在控制台确认渠道、模型、任务类型、素材角色和扩展参数均按能力动态展示。

## 12. 验收清单

- 文生视频成功，任务 ID、状态、进度、结果 URL 正常。
- 首帧、首尾帧、参考图、参考视频、参考音频分别成功。
- 1080p 与 4k 仅在 `seedance-2.0` 展示和通过校验。
- `camera_fixed`、`return_last_frame`、`web_search` 能到达上游请求。
- 超素材数、超时长、错误比例、Fast 选择 1080p/4k 均在提交前拒绝。
- 上游 400、401、429、500、超时及未知状态处理符合规则。
- `queued` 状态可本地取消并退款；`submitted`、`tracking` 状态拒绝取消；退款仅执行一次。
- 数据库与调用日志不存在 Base64/Data URL；密钥不出现在日志中。
- 结果持久化关闭时保存原始上游 URL，不发生自动转存。
- 重启 Worker 后任务可继续轮询，不重复提交、不重复扣费。

## 13. 上游接入前置资料

开始联调前需确认官逆 Base URL、测试 Key、能力接口样例、创建/查询的成功与失败响应样例、结果 URL 有效期、并发限制和计费规则。上游取消必须同时具备接口文档、允许状态和真实联调结果才可启用。所有能力以实测响应为准，再写入新迁移。
