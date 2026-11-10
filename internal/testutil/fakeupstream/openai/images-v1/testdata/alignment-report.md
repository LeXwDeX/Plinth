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
- 内容审核拒绝（400 moderation_blocked）与 402 没有在官方网关上触发，fake 沿用真实 400 信封并换成文档里的 code 与 message；quota-exceeded 429（error.quota 信封）与 forbidden 403（eca_access_denied）依据仓库内已记录的真实网关响应构造，service-unavailable 503 依据官方错误码指南构造（2026-11-08 核对）。

## 端点证据

`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。

| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |
|---|---|---|---|---|---|
| `POST /images/generations` | real | POST /v1/images/generations | https://platform.openai.com/docs/api-reference/images/create | 2026-10-29 | JSON request with inline base64 pictures; n is documented by the official Create image reference (1–10) and returns that many distinct inline candidates; the committed samples ask for n=1, so n>1 is constructed-from-docs. 400 missing_required_parameter and 401 text/plain are real samples. A content-policy refusal (400 moderation_blocked) and the 402/500 faults are not observed: they reuse the real 400 envelope with the documented code or message. The quota-exceeded 429 (the gateway's error.quota body), forbidden 403 (the gateway's eca_access_denied) and service-unavailable 503 (documented service_unavailable_error/server_is_overloaded) are constructed-from-docs answers built from the recorded gateway refusals and the error-code guide (https://developers.openai.com/api/docs/guides/error-codes, checked 2026-11-08) |
| `POST /images/edits` | real | POST /v1/images/edits | https://platform.openai.com/docs/api-reference/images/createEdit | 2026-10-29 | multipart request with the reference picture in the image part (Plinth sends image[], the committed sample says image because the sample key rule rejects brackets; the fake accepts both). model is optional on this route, per the official Create image edit reference (default gpt-image-2.5-sunburst, checked 2026-11-08); the same constructed-from-docs 403/429/503 faults as generations apply |

## 差异

无。
