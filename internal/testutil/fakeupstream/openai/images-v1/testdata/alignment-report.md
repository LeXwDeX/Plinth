# openai images-v1 对齐实测报告

- 官方 API：`dev-test OpenAI-compatible image gateway /v1`
- 核对日期：2026-10-29
- 官方请求数：5（其中付费：2）；fake 请求数：5
- 差异：0 项，其中未说明：0 项

只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。

## 步骤

| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |
|---|---|---|---|---|---|---|---|
| generations-no-auth | `POST /images/generations` | none | 401/401 | 0 |  |  | 0 (0) |
| generations-invalid | `POST /images/generations` | Authorization: Bearer <key> | 400/400 | 7 |  |  | 0 (0) |
| generations | `POST /images/generations` | Authorization: Bearer <key> | 200/200 | 19 |  |  | 0 (0) |
| edits-invalid | `POST /images/edits` | Authorization: Bearer <key> | 400/400 | 7 |  |  | 0 (0) |
| edits | `POST /images/edits` | Authorization: Bearer <key> | 200/200 | 19 |  |  | 0 (0) |

## 请求差异说明

- 网关为 gpt-image-2 的 OpenAI 兼容中转（Plinth 的 OPENAI_BASE_URL）；为压低费用每个付费端点只发一次，取 1024x1024、quality=low、n=1：Plinth 默认 2048x2048、quality=max，响应结构相同，只有 usage 的数值不同。
- edits 的参考图在付费运行里用的是 `image[]` 分片名（Plinth 的真实写法，网关返回 200）；wirefixture 的键规则不接受方括号，样本把分片名写成 `image`，fake 两种写法都接受。
- 内容审核拒绝（400 moderation_blocked）、402、429、5xx 没有在官方网关上触发，fake 沿用真实 400 信封并换成文档里的 code 与 message。

## 端点证据

`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。

| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |
|---|---|---|---|---|---|
| `POST /images/generations` | real | POST /v1/images/generations | https://platform.openai.com/docs/api-reference/images/create | 2026-10-29 | JSON request, one inline base64 picture per request; 400 missing_required_parameter and 401 text/plain are real samples. A content-policy refusal (400 moderation_blocked) and 402/429/5xx are not observed: they reuse the real 400 envelope with the documented code or message |
| `POST /images/edits` | real | POST /v1/images/edits | https://platform.openai.com/docs/api-reference/images/createEdit | 2026-10-29 | multipart request with the reference picture in the image part (Plinth sends image[], the committed sample says image because the sample key rule rejects brackets; the fake accepts both) |

## 差异

无。
