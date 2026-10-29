# gemini images-v1 对齐实测报告

- 官方 API：`dev-test OpenAI-compatible Gemini image gateway /v1`
- 核对日期：2026-10-29
- 官方请求数：4（其中付费：2）；fake 请求数：4
- 差异：0 项，其中未说明：0 项

只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。

## 步骤

| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |
|---|---|---|---|---|---|---|---|
| generate-no-auth | `POST /chat/completions` | none | 401/401 | 0 |  |  | 0 (0) |
| generate-invalid | `POST /chat/completions` | Authorization: Bearer <key> | 400/400 | 6 |  |  | 0 (0) |
| generate | `POST /chat/completions` | Authorization: Bearer <key> | 200/200 | 29 |  |  | 0 (0) |
| edit | `POST /chat/completions` | Authorization: Bearer <key> | 200/200 | 32 |  |  | 0 (0) |

## 请求差异说明

- 网关是 Gemini 图片模型的 OpenAI 兼容中转（Plinth 的 GEMINI_BASE_URL）；每个付费请求只发一次，取 1K 分辨率与 1:1：Plinth 默认 4K，响应结构相同。
- 文生图的真实响应是 JPEG data URI，图生图是 PNG data URI（网关按模型输出而定）；fake 一律返回 PNG data URI，Plinth 按字节判断类型。
- 图生图响应多出顶层 service_tier 与 usage.prompt_tokens_details.image_tokens，fake 对带参考图的请求使用图生图样本的结构。
- 内容审核拒绝、402、429、5xx 没有在官方网关上触发，fake 沿用真实 400 信封并换成合理的 code 与 message。

## 端点证据

`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。

| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |
|---|---|---|---|---|---|
| `POST /chat/completions` | real | POST /v1/chat/completions | https://ai.google.dev/gemini-api/docs/image-generation | 2026-10-29 | the gateway's OpenAI-compatible chat completion with modalities [text,image]: the picture comes back as a data URI in choices[0].message.content[].image_url.url. The real text-to-image sample is a JPEG, the image-to-image one a PNG; the fake always sends a PNG. 400 invalid_text_request and 401 text/plain are real samples; a content-policy refusal and 402/429/5xx are not observed and reuse the real 400 envelope |

## 差异

无。
