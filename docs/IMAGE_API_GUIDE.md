# Sub2API 生图接口文档

客户端只调用 Sub2API。Sub2API 将请求转发到 Prism，再由 Prism 调用实际生图渠道。

## 基本信息

| 项目 | 值 |
| --- | --- |
| Base URL | `https://sub2api.mirainya.com/v1` |
| API Key | `<SUB2API_KEY>` |
| 认证 | `Authorization: Bearer <SUB2API_KEY>` |
| 对外模型 | `gpt-image-2` |
| 建议超时 | `330` 秒以上 |

必须使用 Sub2API 用户 Key，不要使用 Prism Key，也不要使用内部模型名 `gpt-image-2-duomi`。

## 接口选择

| 场景 | 接口 | Content-Type |
| --- | --- | --- |
| 文生图 | `POST /images/generations` | `application/json` |
| URL 或 Base64 图生图 | `POST /images/generations` | `application/json` |
| 上传本地图片进行图生图 | `POST /images/edits` | `multipart/form-data` |

## 文生图

### 请求

```bash
curl --max-time 330 \
  -X POST "https://sub2api.mirainya.com/v1/images/generations" \
  -H "Authorization: Bearer <SUB2API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-image-2",
    "prompt": "Generate a photorealistic orange cat astronaut.",
    "n": 1,
    "aspect_ratio": "1:1",
    "response_format": "url"
  }'
```

## JSON 图生图

调用路径仍为 `/images/generations`。只要 `image_urls` 非空，系统就按图生图记录和处理。

### 使用公开 URL

```bash
curl --max-time 330 \
  -X POST "https://sub2api.mirainya.com/v1/images/generations" \
  -H "Authorization: Bearer <SUB2API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-image-2",
    "prompt": "Restore and enhance the supplied old photo while preserving identity and composition.",
    "image_urls": [
      "https://example.com/reference.png"
    ],
    "aspect_ratio": "16:9",
    "response_format": "url"
  }'
```

URL 必须能被服务器公开访问。临时签名 URL 必须在任务完成前保持有效。

### 使用 Base64

`image_urls` 的元素支持纯 Base64 或 Data URL：

```json
{
  "model": "gpt-image-2",
  "prompt": "Restore this photo.",
  "image_urls": [
    "data:image/png;base64,<BASE64_IMAGE>"
  ],
  "aspect_ratio": "16:9",
  "response_format": "b64_json"
}
```

Prism 会在创建内部异步任务前将 Base64 图片转存为 URL，任务参数不会保存完整 Base64。

## Images Edit

`/images/edits` 兼容 OpenAI Images Edit 的 multipart 上传方式，适合直接上传本地文件。

### cURL

```bash
curl --max-time 330 \
  -X POST "https://sub2api.mirainya.com/v1/images/edits" \
  -H "Authorization: Bearer <SUB2API_KEY>" \
  -F "model=gpt-image-2" \
  -F "prompt=Restore and enhance this old photo while preserving the same people and composition." \
  -F "image=@input.png" \
  -F "aspect_ratio=16:9" \
  -F "response_format=b64_json"
```

Windows PowerShell 中请使用 `curl.exe`，避免调用 PowerShell 的 `curl` 别名。

### Python OpenAI SDK

```python
import base64
from pathlib import Path

from openai import OpenAI

client = OpenAI(
    api_key="<SUB2API_KEY>",
    base_url="https://sub2api.mirainya.com/v1",
    timeout=330.0,
)

with Path("input.png").open("rb") as image:
    response = client.images.edit(
        model="gpt-image-2",
        prompt="Restore and enhance this old photo.",
        image=image,
        response_format="b64_json",
        extra_body={"aspect_ratio": "16:9"},
    )

Path("edited.png").write_bytes(
    base64.b64decode(response.data[0].b64_json)
)
```

### 多图与蒙版

可以重复提交 `image` 字段上传多张参考图，也可以提交可选的 `mask`：

```bash
curl --max-time 330 \
  -X POST "https://sub2api.mirainya.com/v1/images/edits" \
  -H "Authorization: Bearer <SUB2API_KEY>" \
  -F "model=gpt-image-2" \
  -F "prompt=Combine the references into one realistic image." \
  -F "image=@reference-1.png" \
  -F "image=@reference-2.png" \
  -F "mask=@mask.png" \
  -F "aspect_ratio=16:9" \
  -F "response_format=url"
```

接口支持多文件和 `mask`，具体效果取决于上游模型能力。

## 请求字段

### `/images/generations`

| 字段 | 必填 | 类型 | 说明 |
| --- | --- | --- | --- |
| `model` | 是 | string | 固定使用 `gpt-image-2` |
| `prompt` | 是 | string | 生图或编辑提示词 |
| `image_urls` | 否 | string[] | 非空时按图生图处理；支持 HTTP(S) URL、纯 Base64、Data URL |
| `n` | 否 | integer | 生成数量，正整数 |
| `aspect_ratio` | 否 | string | 输出宽高比，例如 `1:1`、`16:9`、`9:16` |
| `size` | 否 | string | 输出尺寸；使用 Duomi 时推荐传 `aspect_ratio` |
| `quality` | 否 | string | `auto`、`low`、`medium`、`high` |
| `response_format` | 否 | string | `url` 或 `b64_json`，默认 `url` |
| `output_format` | 否 | string | `png`、`jpeg`、`webp` |
| `output_compression` | 否 | integer | 输出压缩率，取值 `0` 至 `100` |
| `moderation` | 否 | string | 内容审核配置，是否生效取决于上游模型 |
| `style` | 否 | string | 风格配置，是否生效取决于上游模型 |

建议只传 `aspect_ratio` 或 `size` 中的一个。当前 Duomi 模型优先使用 `aspect_ratio`。

### `/images/edits`

| 字段 | 必填 | 类型 | 说明 |
| --- | --- | --- | --- |
| `model` | 是 | text | 固定使用 `gpt-image-2` |
| `prompt` | 是 | text | 图片编辑提示词 |
| `image` | 是 | file | 参考图，可重复提交；支持 PNG、JPEG、WebP |
| `mask` | 否 | file | 蒙版图，可重复提交；是否生效取决于上游模型 |
| `n` | 否 | text | 生成数量，正整数 |
| `aspect_ratio` | 否 | text | 输出宽高比，例如 `16:9` |
| `size` | 否 | text | 输出尺寸 |
| `quality` | 否 | text | 质量配置 |
| `response_format` | 否 | text | `url` 或 `b64_json`，默认 `url` |
| `output_format` | 否 | text | `png`、`jpeg`、`webp` |
| `output_compression` | 否 | text | `0` 至 `100` |
| `moderation` | 否 | text | 内容审核配置 |
| `style` | 否 | text | 风格配置 |

## 文件限制

- 支持格式：PNG、JPEG、WebP。
- 单个图片文件最大 `20 MiB`。
- 同类图片文件合计最大 `32 MiB`。
- multipart 请求整体最大 `40 MiB`。
- JSON 请求整体最大 `40 MiB`。
- Base64 会增加请求体积，较大的本地图片推荐使用 `/images/edits`。

## 成功响应

### URL 格式

请求传入 `"response_format": "url"`，或不传该字段：

```json
{
  "created": 1785133411,
  "data": [
    {
      "url": "https://example.com/generated.png"
    }
  ]
}
```

返回 URL 可能带有效期，客户端应及时下载并保存图片。

### Base64 格式

请求传入 `"response_format": "b64_json"`：

```json
{
  "created": 1785133411,
  "data": [
    {
      "b64_json": "iVBORw0KGgo..."
    }
  ]
}
```

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
| `400` | 请求参数错误或余额不足 |
| `401` | API Key 无效或缺失 |
| `413` | 请求体或上传文件超限 |
| `500` | 内部调用失败 |
| `502` | 上游生成失败或结果读取失败 |
| `504` | 等待生图结果超时 |

## 同步与异步说明

Sub2API 对外的 `/images/generations` 和 `/images/edits` 都是同步接口。Prism 内部可以调用异步渠道，但会等待任务结束后再按 OpenAI 格式返回最终图片。

调用方无需查询 Prism 任务状态，也不能通过 Sub2API 查询 Prism 原生异步任务。请将客户端和反向代理超时设置为 `330` 秒以上。
