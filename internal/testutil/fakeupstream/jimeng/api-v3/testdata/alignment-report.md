# jimeng api-v3 对齐实测报告

- 官方 API：`https://ark.cn-beijing.volces.com/api/v3`
- 核对日期：2026-10-29
- 官方请求数：5（其中付费：3）；fake 请求数：5
- 差异：0 项，其中未说明：0 项

只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。

## 步骤

| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |
|---|---|---|---|---|---|---|---|
| generations-no-auth | `POST /images/generations` | none | 401/401 | 6 |  |  | 0 (0) |
| generations-invalid | `POST /images/generations` | Authorization: Bearer <key> | 400/400 | 6 |  |  | 0 (0) |
| generations-b64 | `POST /images/generations` | Authorization: Bearer <key> | 200/200 | 11 |  |  | 0 (0) |
| generations-url | `POST /images/generations` | Authorization: Bearer <key> | 200/200 | 11 |  | data.0.url image/jpeg jpeg | 0 (0) |
| edits-b64 | `POST /images/generations` | Authorization: Bearer <key> | 200/200 | 11 |  |  | 0 (0) |

## 请求差异说明

- 每个付费请求只发一次：2048x2048（模型接受的最小尺寸）；b64_json 与 url 两种响应形态各一次，图生图一次（b64_json）。Plinth 的 Offering 把 response_format 固定为 b64_json，url 形态由 Adapter 支持但目前不被 Schema 使用。
- 图生图的 `image`（data URI 数组）在样本里是不透明占位 ok（wirefixture 的标识符投影拒绝 data URI），fake 只检查提示词。
- 官方签名 URL 用 TOS V4 签名（X-Tos-Expires=86400）；样本把它投影成占位。已过期 URL 的响应不是自然过期观测，而是把 X-Tos-Date 改到过去后由官方主机返回的 403 JSON（Code=AccessDenied、Message=Request has expired）。
- 内容审核拒绝、402、429、5xx 没有在官方 API 上触发，fake 沿用真实 400 信封并换成文档里的 code。

## 端点证据

`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。

| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |
|---|---|---|---|---|---|
| `POST /images/generations` | real | POST /api/v3/images/generations | https://www.volcengine.com/docs/82379/1541523 | 2026-10-29 | response_format b64_json answers an inline picture, url a signed TOS URL (both real samples); 401 AuthenticationError and 400 MissingParameter are real samples. A content-policy refusal, 402/429/5xx are not observed and reuse the real error envelope |
| `GET /files/{name}` | constructed-from-docs |  | https://www.volcengine.com/docs/82379/1541523 | 2026-10-29 | the signed picture host is not part of the API; this route serves the fixed JPEG a response_format=url answer names. An expired URL answers the 403 JSON body the official TOS host returned for a URL past its X-Tos-Expires (observed 2026-10-29 by moving X-Tos-Date into the past) |

## 差异

无。
