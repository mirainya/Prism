# Prism 下游生图接入指南

本文面向调用 Prism 的第三方应用。调用方只使用 Prism API 令牌，不接触上游密钥或内部路由。

## 基本信息

| 项目 | 值 |
| --- | --- |
| Base URL | `https://prism.mirainya.icu/v1` |
| 认证 | `Authorization: Bearer <PRISM_TOKEN>` |
| 备用认证头 | `X-API-Key: <PRISM_TOKEN>` |
| 同步请求超时 | `330` 秒以上 |
| 异步轮询间隔 | `2-5` 秒 |

## 查询可用模型

模型接口使用复数路径：

```bash
curl "https://prism.mirainya.icu/v1/models" \
  -H "Authorization: Bearer <PRISM_TOKEN>"
```

从响应的 `data` 中选择：

- `supported_operations` 包含 `images.generate`：支持图片生成。
- `supported_operations` 包含 `images.edit`：支持图片编辑。
- `supported_endpoints` 给出生成或编辑的基础路径；异步调用规则见下文“异步任务”。

不要根据模型名称猜测能力，也不要调用不存在的 `/v1/model` 单数路径。

### 价格与可用率

`/v1/models` 不返回价格。公开价格通过以下接口查询，无需登录：

```bash
curl "https://prism.mirainya.icu/api/public/pricing"
```

在响应的 `data` 中按 `model_code` 查找模型，再读取 `skus[].components[]`：

- `operation` 表示计费对应的操作，例如 `images.generate` 或 `images.edit`。
- `unit_price`、`unit_code` 和 `currency.code` 共同表示单价。
- 当前本文所列图片模型按张计费，公开价格以接口实时返回值为准。

模型存在有效观测样本时，`/v1/models` 的模型项会包含 `availability`，其中 `success_rate` 表示成功率。该字段缺失表示暂无有效样本，不能按 `0%` 处理。

## 图片生成

### 请求

```bash
curl --max-time 330 \
  -X POST "https://prism.mirainya.icu/v1/images/generations" \
  -H "Authorization: Bearer <PRISM_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<IMAGE_MODEL_CODE>",
    "prompt": "雨夜中的未来城市",
    "n": 1,
    "aspect_ratio": "16:9",
    "response_format": "url"
  }'
```

`/v1/images/generations` 只执行图片生成。带参考图的请求必须改用 `/v1/images/edits`。

### 常用字段

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `model` | 是 | `/v1/models` 返回的图片模型代码 |
| `prompt` | 是 | 生成提示词 |
| `n` | 否 | 图片数量，范围 `1-10` |
| `size` | 否 | 输出尺寸，例如 `1024x1024` |
| `aspect_ratio` | 否 | 宽高比，例如 `1:1`、`16:9` |
| `quality` | 否 | 模型支持的质量档位 |
| `response_format` | 否 | `url` 或 `b64_json`，默认 `url` |
| `output_format` | 否 | `png`、`jpeg` 或 `webp` |
| `output_compression` | 否 | `0-100` |
| `stream` | 否 | `true` 时返回 SSE |

`size` 与 `aspect_ratio` 只能传一个。具体选项以控制台和模型能力为准。

## 图片编辑

图片编辑固定调用：

```text
POST /v1/images/edits
```

### JSON URL 或 Base64

```bash
curl --max-time 330 \
  -X POST "https://prism.mirainya.icu/v1/images/edits" \
  -H "Authorization: Bearer <PRISM_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<IMAGE_MODEL_CODE>",
    "prompt": "将背景改成雪山",
    "image_urls": ["https://example.com/source.png"],
    "response_format": "url"
  }'
```

`image_urls` 支持公开 HTTP(S) URL、纯 Base64 和 Data URL。Prism 会在提交阶段导入参考图，因此 URL 必须在提交请求期间可访问；请求成功后无需保持到生成完成。

### 本地文件

```bash
curl --max-time 330 \
  -X POST "https://prism.mirainya.icu/v1/images/edits" \
  -H "Authorization: Bearer <PRISM_TOKEN>" \
  -F "model=<IMAGE_MODEL_CODE>" \
  -F "prompt=将背景改成雪山" \
  -F "image=@./source.png" \
  -F "response_format=url"
```

可重复提交 `image` 字段以提供多张参考图，也可以使用一个可选的 PNG `mask` 文件字段。

### 文件限制

- 参考图格式：PNG、JPEG、WebP；蒙版格式仅支持 PNG。
- 参考图最多 `16` 张，蒙版最多 `1` 张。
- 单个文件最大 `20 MiB`。
- 同类图片合计最大 `32 MiB`。
- 整个请求最大 `48 MiB`。

## 成功响应

URL 格式：

```json
{
  "created": 1785133411,
  "data": [
    {"url": "https://storage.example.com/generated.png"}
  ]
}
```

Base64 格式：

```json
{
  "created": 1785133411,
  "data": [
    {"b64_json": "iVBORw0KGgo..."}
  ]
}
```

调用记录创建成功后，响应头 `X-Prism-Call-ID` 是本次调用的唯一编号，排查问题时应一并记录。鉴权、参数校验或选路阶段提前失败时可能没有该响应头。

## 错误响应

```json
{
  "error": {
    "message": "error description",
    "type": "invalid_request_error"
  }
}
```

| HTTP 状态码 | 说明 |
| --- | --- |
| `400` | 参数错误、模型能力不匹配或余额不足 |
| `401` | Prism API 令牌无效或缺失 |
| `409` | `Idempotency-Key` 与已有请求冲突 |
| `413` | 请求体或上传文件超限 |
| `429` | 请求频率或并发受限 |
| `500` | Prism 内部执行错误 |
| `502` | 上游生成失败或结果读取失败 |
| `503` | 当前没有可执行路由 |
| `504` | 等待上游结果超时 |

## 异步任务

异步接口创建任务后立即返回，不等待图片生成完成。它适合服务端调用、长耗时模型和不希望维持长连接的场景。异步接口只接受具有任务型上游线路的模型；同一个模型可能只开放同步接口，不支持异步时提交接口会返回 `400`。

| 操作 | 提交接口 |
| --- | --- |
| 图片生成 | `POST /v1/images/generations/async` |
| 图片编辑 | `POST /v1/images/edits/async` |
| 查询任务 | `GET /v1/images/tasks/{id}` |

异步生成支持同步生成的非流式字段；`stream` 必须省略或设为 `false`，并且必须省略 `partial_images`。可用字段包括 `model`、`prompt`、`n`、`size`、`aspect_ratio`、`quality`、`response_format`、`output_format`、`output_compression`、`moderation`、`style`、`background` 和 `user`。模型线路允许幂等键时，建议为每次提交设置一个稳定且唯一的 `Idempotency-Key`，用于识别重复提交：

```bash
curl -i \
  -X POST "https://prism.mirainya.icu/v1/images/generations/async" \
  -H "Authorization: Bearer <PRISM_TOKEN>" \
  -H "Idempotency-Key: image-order-20260920-001" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<IMAGE_MODEL_CODE>",
    "prompt": "雨夜中的未来城市",
    "aspect_ratio": "16:9",
    "response_format": "url"
  }'
```

图片编辑将路径改为 `/v1/images/edits/async`，请求格式仍支持前文介绍的 JSON `image_urls` 和 multipart 本地文件。

### 异步编辑：JSON 参考图

```bash
curl -i \
  -X POST "https://prism.mirainya.icu/v1/images/edits/async" \
  -H "Authorization: Bearer <PRISM_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<IMAGE_MODEL_CODE>",
    "prompt": "将背景改成雪山",
    "image_urls": ["https://example.com/source.png"],
    "response_format": "url"
  }'
```

`image_urls` 也可以是纯 Base64 或 Data URL。提交阶段会先导入参考图，再创建任务；提交成功后不需要客户端持续保留原始 URL。仍应确保 URL 在提交期间可访问。

### 异步编辑：multipart 本地文件

```bash
curl -i \
  -X POST "https://prism.mirainya.icu/v1/images/edits/async" \
  -H "Authorization: Bearer <PRISM_TOKEN>" \
  -F "model=<IMAGE_MODEL_CODE>" \
  -F "prompt=将背景改成雪山" \
  -F "image=@./source.png" \
  -F "mask=@./mask.png" \
  -F "response_format=url"
```

`image` 可以重复提交，`mask` 最多提交一个 PNG。异步提交的文件限制与同步编辑相同。

### 提交响应

成功提交返回 `202 Accepted`。`X-Prism-Call-ID` 与 `data.id` 都是任务编号，`Location` 与 `data.location` 都指向任务查询地址，`Retry-After: 3` 表示建议首次等待 3 秒再查询：

```http
HTTP/1.1 202 Accepted
X-Prism-Call-ID: 5fbc13b6-8f07-43ae-81d3-2ca260e637a7
Location: /v1/images/tasks/5fbc13b6-8f07-43ae-81d3-2ca260e637a7
Retry-After: 3
```

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": "5fbc13b6-8f07-43ae-81d3-2ca260e637a7",
    "status": "queued",
    "operation": "images.generate",
    "location": "/v1/images/tasks/5fbc13b6-8f07-43ae-81d3-2ca260e637a7"
  }
}
```

以下幂等规则仅适用于异步图片生成：`Idempotency-Key` 最长 `128` 字节，可在首次提交后的 `24` 小时内让服务端识别同一 Token 的重复提交；相同键对应不同请求时返回 `409`。提交超时后，应在该窗口内使用原请求内容和原键重试，并以任务查询接口返回的状态为准。

重试时必须保持请求体、模型、操作路径和 `Idempotency-Key` 都不变。不要因为没有立即拿到响应就更换键重新提交，否则可能创建多个任务。

`Idempotency-Key` 是否为必填、可选或禁用取决于模型线路策略。请求头不符合线路策略时提交接口返回 `400`；上例适用于允许该请求头的线路。

异步图片编辑会先将参考图导入为内部媒体资产，因此重复提交同一原始图片请求不保证复用原任务。编辑请求不要依赖 `Idempotency-Key` 去重；客户端应在获得任务 ID 后保存它，并只通过任务查询接口追踪结果。

### 查询任务

```bash
curl "https://prism.mirainya.icu/v1/images/tasks/<TASK_ID>" \
  -H "Authorization: Bearer <PRISM_TOKEN>"
```

任务只能由创建它的 Prism Token 查询。正常查询固定返回以下字段，终态再附加 `result` 或 `error`：

| 字段 | 说明 |
| --- | --- |
| `id` | 任务编号，与提交响应的 `data.id` 相同 |
| `status` | `queued`、`processing`、`completed` 或 `failed` |
| `progress` | `0-100` 的近似进度 |
| `model` | 提交时使用的公开模型代码 |
| `operation` | `images.generate` 或 `images.edit` |
| `created_at` | UTC 创建时间，RFC 3339 格式 |
| `updated_at` | UTC 最近更新时间，RFC 3339 格式 |
| `result` | 仅 `completed` 时出现，结构与同步图片响应相同 |
| `error` | 仅 `failed` 时出现，包含 `code` 和 `message` |

状态含义如下：

| `status` | 说明 | 是否终态 |
| --- | --- | --- |
| `queued` | 已创建，等待提交 | 否 |
| `processing` | 上游已接收或正在生成 | 否 |
| `completed` | 已完成，响应包含 `result` | 是 |
| `failed` | 执行失败，响应包含 `error` | 是 |

查询未结束的任务时，接口仍返回 HTTP `200`，只需根据 `data.status` 决定是否继续轮询。`progress` 是 `0-100` 的近似进度，部分上游只能提供 `0` 或 `100`，不能据此判断任务已经卡住。

处理中响应：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": "5fbc13b6-8f07-43ae-81d3-2ca260e637a7",
    "status": "processing",
    "progress": 42,
    "model": "<IMAGE_MODEL_CODE>",
    "operation": "images.generate",
    "created_at": "2026-09-20T11:20:00Z",
    "updated_at": "2026-09-20T11:20:09Z"
  }
}
```

### 轮询示例

下面的示例只在 `queued` 或 `processing` 时继续查询，终态为 `completed` 或 `failed`：

```python
import time
import requests

base_url = "https://prism.mirainya.icu"
token = "<PRISM_TOKEN>"
headers = {
    "Authorization": f"Bearer {token}",
    "Idempotency-Key": "image-order-20260920-001",
}

submit = requests.post(
    f"{base_url}/v1/images/generations/async",
    headers={**headers, "Content-Type": "application/json"},
    json={
        "model": "<IMAGE_MODEL_CODE>",
        "prompt": "雨夜中的未来城市",
        "response_format": "url",
    },
    timeout=30,
)
submit.raise_for_status()
task = submit.json()["data"]

while task["status"] in {"queued", "processing"}:
    time.sleep(3)
    query = requests.get(
        f"{base_url}{task['location']}",
        headers={"Authorization": f"Bearer {token}"},
        timeout=30,
    )
    query.raise_for_status()
    task = query.json()["data"]

if task["status"] == "completed":
    print(task["result"]["data"][0].get("url"))
else:
    raise RuntimeError(task.get("error", {}).get("message", "image task failed"))
```

首次查询建议等待提交响应中的 `Retry-After` 秒数（当前通常为 `3`），之后间隔 `2-5` 秒。生产客户端应设置最大轮询时长，并在超时后保留任务 ID，稍后继续查询。

完成响应：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": "5fbc13b6-8f07-43ae-81d3-2ca260e637a7",
    "status": "completed",
    "progress": 100,
    "model": "<IMAGE_MODEL_CODE>",
    "operation": "images.generate",
    "created_at": "2026-09-20T11:20:00Z",
    "updated_at": "2026-09-20T11:20:18Z",
    "result": {
      "created": 1789874418,
      "data": [{"url": "https://storage.example.com/generated.png"}]
    }
  }
}
```

结果 URL 可能带有效期，任务完成后应及时读取或转存。

失败响应仍为 HTTP `200`，通过任务状态表达执行结果：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": "5fbc13b6-8f07-43ae-81d3-2ca260e637a7",
    "status": "failed",
    "progress": 0,
    "model": "<IMAGE_MODEL_CODE>",
    "operation": "images.generate",
    "created_at": "2026-09-20T11:20:00Z",
    "updated_at": "2026-09-20T11:20:18Z",
    "error": {
      "code": "provider_request_failed",
      "message": "上游任务失败"
    }
  }
}
```

### 任务查询错误

任务本身执行失败时仍返回 HTTP `200` 和 `data.status: "failed"`。任务不存在、归属不符或结果读取失败时，查询处理器使用 Prism 错误结构：

```json
{
  "code": 404,
  "message": "image task not found"
}
```

鉴权、限流或网关就绪检查在进入查询处理器前执行，相关错误使用 OpenAI 错误结构：

```json
{
  "error": {
    "message": "rate limit exceeded, please try again later",
    "type": "rate_limit_error",
    "param": null,
    "code": "rate_limit_exceeded"
  }
}
```

| HTTP 状态码 | 说明 |
| --- | --- |
| `401` | Token 无效或缺失 |
| `404` | 任务不存在、不是图片任务，或不是由当前 Token 创建 |
| `429` | 请求频率超限 |
| `500` | 查询、失败信息或最终结果读取异常 |
| `503` | 网关运行时尚未就绪 |

异步接口仅适用于具有任务型上游线路的模型；不支持时返回 `400`。异步请求的 `stream` 必须省略或设为 `false`，不能使用 `stream: true`，且不能提交 `partial_images`。目前也没有图片任务取消或下游回调接口。建议异步任务使用 `response_format: "url"`，避免轮询响应携带较大的 Base64 内容。任务查询必须使用创建任务的同一个 Prism Token，其他 Token 查询会得到 `404`。

## 流式响应

同步接口传入 `"stream": true` 时返回 SSE。这是流式响应，不是异步任务。每条消息使用 `data:` 行；其中 JSON 的 `type` 字段在完成时为 `image_generation.completed`，失败时为 `image_generation.failed`，最后发送 `[DONE]`。

## Python OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(
    api_key="<PRISM_TOKEN>",
    base_url="https://prism.mirainya.icu/v1",
    timeout=330.0,
)

response = client.images.generate(
    model="<IMAGE_MODEL_CODE>",
    prompt="雨夜中的未来城市",
    response_format="url",
)

print(response.data[0].url)
```

生产调用应保存 `X-Prism-Call-ID`、HTTP 状态码和错误体；不要记录 Prism 令牌、上游地址或上游密钥。
