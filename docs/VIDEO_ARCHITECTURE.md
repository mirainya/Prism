# Prism v2.0.0 视频架构

本文描述 v2.0.0 的统一视频路径。

## 对外接口

| 方法 | 路径 | 作用 |
|---|---|---|
| POST | `/v1/videos/generations` | 创建视频 |
| POST | `/v1/videos/estimate` | 按活动目录估价 |
| GET | `/v1/videos/generations` | 当前 Token 的视频列表 |
| GET | `/v1/videos/generations/:id` | 视频详情与结果 |
| GET | `/v1/videos/generations/:id/queue` | 单任务队列状态 |
| GET | `/v1/videos/queue` | 活动任务轻量列表 |
| POST | `/v1/videos/assets`、`/v1/videos/uploads` | 创建输入素材 |
| GET / DELETE | `/v1/videos/assets/:asset_id` | 查询或删除素材 |

`GET /v1/models` 与 `GET /v1/models/:code` 对视频模型返回 `video_options`，包含当前可执行线路的任务模式、素材上限、时长及分辨率等公开参数；不同线路约束不同时以 `variants` 分列。未声明的参数不推测为“全部支持”。

视频不提供取消接口。取消能力不能由前端配置，也不能在上游不支持时模拟。

## 创建流程

1. `prismv1` 解码并校验请求。
2. 统一路由按 `/v1/videos/generations`、公开模型和能力选择活动目录项。
3. 固定发布版、Operation、SKU、Route、Offering、成本方案、Transport、凭据与凭据版本。
4. 素材服务验证用户、Token、类型、状态和有效期，并生成允许上游访问的 URL。
5. 一个数据库事务创建加密请求、Call、Attempt、视频资源、异步执行、预授权、任务名额、可选客户端回调目标及 Outbox，并把 Token 当前的 XFileStorage Key 固定到 Call。
6. SQL Worker 使用租约认领 Outbox，通过固定版本 Adapter 提交上游。
7. 后续查询、回调或恢复推进状态；终态事务同时写结果、交付、结算和名额释放。

重复 `Idempotency-Key` 且请求摘要一致时返回原资源；同键不同正文会被拒绝。

## 配置模型

视频不读取旧 `video_channels`、`video_channel_keys` 或独立视频价格作为新任务事实。配置来自统一 `gw_*` 目录：

- Product / SKU 定义公开能力与规格；
- Operation Contract 对应 `POST /v1/videos/generations`；
- Product Transport 固定 Adapter 代码、版本和动作；
- Route / Offering 固定上游模型、主机、凭据池与成本方案；
- Sell Rate 与 Cost Plan 分别保存用户售价和上游成本；
- Service Tier、交付模式、幂等模式、任务名额范围和回调策略是正式字段。

当前实际异步视频适配器由 `internal/gateway/adapter` 注册。Worker 不根据 JSON 名称动态加载任意代码。

## 状态与恢复

持久事实位于：

- `gw_api_calls`：调用终态；
- `gw_api_call_attempts`：本次上游尝试及固定成本方案；
- `gw_video_tasks`：视频规格的只读资源投影；
- `gw_async_executions`：提交、运行、恢复和终态；
- `gw_async_outbox`：待执行动作及租约；
- `gw_channel_request_logs`：每次 HTTP 交换证据；
- `gw_result_deliveries`：结果交付状态。

每次提交或轮询 HTTP 交换使用渠道 Transport 超时；这不是视频任务总时限。只要上游持续返回可验证的排队或运行状态，任务可以持续轮询。提交结果无法确认时不重发；已获得上游任务 ID后，连续三次轮询通信失败会转为 `terminated_unknown`，保留请求日志和任务名额，不再假装处理中。可信的迟到终态回调仍可完成结算。

## 素材

`gw_media_assets` 是统一输入/输出媒体资源。输入素材必须属于当前用户和 Token，处于 active 且未过期，并与声明类型一致。

图生视频不把 Base64 作为长期任务参数：上传文件或 URL 经素材服务转存/登记后，视频请求只保存媒体资产关联及规范化规格。已被任务引用的素材不能删除。

结果交付支持：

- `reference`：加密保存经验证的上游来源，查询时按权限解析；
- `managed_copy`：保存私有持久定位符；查询任务详情时使用 Call 固定的 XFileStorage Key 生成新的临时签名 URL，不直接返回定位符。

Token 当前的 XFileStorage Key 只用于创建新 Call。有 Key 时图片和视频使用 `managed_copy`，无 Key 时使用 `reference`。更换或清除 Key 不影响在途任务和历史结果，读取、签名与清理始终使用任务所属 Call 固定的 Key。

生成终态与结果交付状态相互独立。转存失败只改变交付状态，不会把已成功的生成改为失败。远程 URL 的临时下载、上传或校验失败会保存加密来源，并由 `reconcile_delivery` 只重试转存；不会重新调用或查询上游生成。内联结果和永久失败不自动重试。

## 回调

上游回调使用：

```text
POST /internal/gateway/callback
X-Gateway-Callback-Token: ...
X-Gateway-Event-ID: ...
```

入口只接受 JSON，正文上限 1 MiB；回调路径不接受客户端 `scope`，事件作用域由服务端固定。入口按客户端地址限速并限制并发，持久化事件 HMAC、正文 HMAC 和限期加密正文后返回 `202`。回调 Worker 验真后推进异步状态；不能验证的证据进入人工核验，处理完成或过期后清除正文。

客户端 `callback_url` 在创建时经过公网地址校验并加密绑定 Call。终态事务生成不可变回调事件及内部 HMAC 完整性证据；结果载荷只包含交付 ID，不包含私有定位符或临时签名 URL，客户端通过任务查询接口获取新的签名 URL。投递 Worker 发送稳定事件 ID、内容 SHA256，并执行禁止重定向、SSRF 防护、请求超时和有限重试。

## 查询性能

视频列表、队列和管理员视频任务只读取有界结构化投影，不读取加密请求、结果正文或 Base64。详情按需解密单个结果来源，因此新增图生视频任务不会使列表加载完整输入正文。

## 历史迁移

- `prism migrate import-runtime` 导入可证明的历史调用与任务事实；
- `prism migrate import-video-assets` 导入有归属、完整性和受控存储证据的视频素材；
- 无法证明的行写入迁移问题并使导入失败；
- 深度审计完成前不得删除历史表。
