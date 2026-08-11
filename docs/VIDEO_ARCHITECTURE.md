# 视频架构

本文描述 Prism 当前实际使用的视频生成路径。

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
