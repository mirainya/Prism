# 视频架构

本文描述 Prism 当前实际使用的视频生成路径。

> 本文只说明统一网关迁移前的现行实现，不是后续扩展规范。统一目录、凭据、计费、异步状态和素材生命周期的目标设计以 [`specs/2026-09-04-unified-gateway-catalog-billing-architecture.md`](specs/2026-09-04-unified-gateway-catalog-billing-architecture.md) 为准；新功能不得继续复制本文的独立视频渠道、Redis 并发、渠道价格或旧取消流程。

## 对外接口

客户端统一使用视频 API：

- `POST /v1/videos/generations`
- `POST /v1/videos/estimate`
- `GET /v1/videos/generations/:id`
- `POST /v1/videos/generations/:id/cancel`
- `POST /v1/videos/assets`

新视频任务不经过能力 Endpoint。

## 请求流程

1. API 将输入转换为 `video.CreateTaskRequest`。
2. `video.Engine` 校验素材和请求能力。
3. `video.Router` 选择兼容的 `video_channel` 和 Key。
4. 素材解析器把 Prism 素材 ID 转换为上游引用。
5. 渠道 Adapter 构造并提交上游请求。
6. Worker 轮询上游任务并保存统一结果。
7. 引擎统一处理计费、回调、日志和并发。

## 配置边界

`video_channels` 保存路由数据：

- 支持的模型与能力
- Base URL 与协议类型
- 计费策略
- 素材交付策略
- Generic Adapter 的协议配置

`video_channel_keys` 保存凭据和并发设置。

每个渠道的模型使用公开名到上游名的映射：

```json
[
  {
    "model_name": "video-fast",
    "vendor_model": "seedance-2.0-fast"
  }
]
```

- `model_name` 是 Prism 对外公开的稳定名称，请求、查询和自动选路均使用它。
- `vendor_model` 是当前渠道实际接收的模型名称，仅在调用上游时使用。
- 历史字符串数组仍可读取，`"seedance-2.0"` 等价于公开名与上游名相同。

任务创建后保存 `RoutePlan`，其中包含公开模型、上游模型及渠道协议配置快照。渠道后续修改不会改变已创建任务的提交、轮询和取消行为。RoutePlan 只保存 Key ID，密钥仍从 `video_channel_keys` 实时读取；运行中任务引用的渠道或 Key 不允许删除。

## 协议

Prism 只有两种视频协议实现：

- `seedance`：内置的 Seedance 官方协议
- `generic`：可配置的 JSON 任务协议

第三方差异写入 `extra_config.adapter`。仅当稳定协议无法用 Generic Adapter 表达时，才新增 Go Adapter。

## 素材

- `direct_url`：直接把 Prism 存储 URL 交给上游。
- `presigned_upload`：上游必须使用自己的对象引用时，才向上游存储传输素材。

素材解析完成后，Adapter 只处理统一引用。

## 历史兼容

旧视频 Endpoint 已停止创建任务。旧任务的查询和取消暂时保留，避免历史任务失去访问能力。
