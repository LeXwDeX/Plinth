# hyper3d api-v2 对齐实测报告

- 官方 API：`https://api.hyper3d.com/api/v2`
- 核对日期：2026-10-29
- 官方请求数：54（其中付费：6）；fake 请求数：56
- 差异：0 项，其中未说明：0 项

只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。

## 步骤

| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |
|---|---|---|---|---|---|---|---|
| balance | `GET /check_balance` | Authorization: Bearer <key> | 200/200 | 2 |  |  | 0 (0) |
| balance-no-auth | `GET /check_balance` | none | 401/401 | 3 |  |  | 0 (0) |
| status-invalid | `POST /status` | Authorization: Bearer <key> | 400/400 | 5 |  |  | 0 (0) |
| download-invalid | `POST /download` | Authorization: Bearer <key> | 400/400 | 5 |  |  | 0 (0) |
| rodin-invalid | `POST /rodin` | Authorization: Bearer <key> | 201/201 | 3 |  |  | 0 (0) |
| rodin-no-auth | `POST /rodin` | none | 401/401 | 3 |  |  | 0 (0) |
| rodin-text | `POST /rodin` | Authorization: Bearer <key> | 201/201 | 10 |  |  | 0 (0) |
| rodin-text-status | `POST /status` | Authorization: Bearer <key> | 201/201 | 5 | Done |  | 0 (0) |
| rodin-text-download | `POST /download` | Authorization: Bearer <key> | 201/201 | 5 |  | list.0.url model/gltf-binary glb; list.1.url image/webp webp | 0 (0) |
| rodin-image | `POST /rodin` | Authorization: Bearer <key> | 201/201 | 10 |  |  | 0 (0) |
| rodin-image-status | `POST /status` | Authorization: Bearer <key> | 201/201 | 5 | Done |  | 0 (0) |
| rodin-image-download | `POST /download` | Authorization: Bearer <key> | 201/201 | 5 |  | list.0.url model/gltf-binary glb; list.1.url image/webp webp | 0 (0) |
| bang | `POST /bang` | Authorization: Bearer <key> | 201/201 | 9 |  |  | 0 (0) |
| bang-status | `POST /status` | Authorization: Bearer <key> | 201/201 | 5 | Generating > Waiting > Generating > Done |  | 0 (0) |
| bang-download | `POST /download` | Authorization: Bearer <key> | 201/201 | 5 |  | list.0.url model/gltf-binary glb | 0 (0) |
| texture | `POST /rodin_texture_only` | Authorization: Bearer <key> | 201/201 | 9 |  |  | 0 (0) |
| texture-status | `POST /status` | Authorization: Bearer <key> | 201/201 | 5 | Generating > Done |  | 0 (0) |
| texture-download | `POST /download` | Authorization: Bearer <key> | 201/201 | 5 |  | list.0.url model/gltf-binary glb | 0 (0) |

## 请求差异说明

- 为每个端点取最便宜的请求：Gen-2.5-Extreme-Low、texture_mode=extreme-low，0.5 credit；Plinth 实际使用 Gen-2.5-Extreme-High，同一端点、同一响应结构。
- 官方运行分三段，报告把它们合并：第一段提交 rodin（文生、图生）、bang、texture，对 HTTP 201 的轮询未被工具识别为轮询（已修正），因此 status/download 留到第二段；第二段用同一批 uuid 与 subscription_key 补轮询和下载，并重发 bang；第三段重发 texture。
- 第一段的 bang 在 Rodin 任务尚未完成时发出，官方以 HTTP 201 + `{"error":"ASSET_NOT_AVAILABLE"}` 拒绝（未扣费）；fake 复现该拒绝，第二段在任务完成后重发并成功。
- 第一段的 texture 用了过小的占位 GLB，三个子作业以 Failed 结束（status 响应带顶层 message `Invalid input.`），已计费；第三段改用 bang 产出的 GLB 重发，是新的输入而非重放。该 Failed 响应未入库为样本，fake 的 Failed 状态按它的结构构造。
- 官方在请求缺少提示词与图片时返回 HTTP 201 + `{"error":"INVALID_REQUEST"}`（不是 4xx）；fake 复现。

## 端点证据

`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。

| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |
|---|---|---|---|---|---|
| `GET /check_balance` | real | GET /api/v2/check_balance | https://docs.hyper3d.ai/en/api-specification/check-balance | 2026-10-29 |  |
| `POST /status` | real | POST /api/v2/status | https://docs.hyper3d.ai/en/api-specification/check-status | 2026-10-29 | Waiting, Generating and Done polls are real; the Failed poll (a texture of an unreadable model: the jobs all Failed and a top-level message) was observed once but not committed as a sample, so it is built from the Done poll |
| `POST /download` | real | POST /api/v2/download | https://docs.hyper3d.ai/en/api-specification/download-results | 2026-10-29 | a download asked before the jobs finished answers an empty list (observed) |
| `GET /files/{task_uuid}/{name}` | constructed-from-docs |  | https://docs.hyper3d.ai/en/api-specification/download-results | 2026-10-29 | the official file host is not part of the API; this route serves the fixed files the download list names (content types as observed) |
| `POST /rodin` | real | POST /api/v2/rodin | https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5 | 2026-10-29 | multipart request, HTTP 201 for a handled request; a rejected request is HTTP 201 with an error body (observed) |
| `POST /bang` | real | POST /api/v2/bang | https://docs.hyper3d.ai/en/api-specification/bang | 2026-10-29 | multipart request, HTTP 201 for a handled request; a rejected request is HTTP 201 with an error body (observed) |
| `POST /rodin_texture_only` | real | POST /api/v2/rodin_texture_only | https://docs.hyper3d.ai/en/api-specification/generate-texture | 2026-10-29 | multipart request, HTTP 201 for a handled request; a rejected request is HTTP 201 with an error body (observed) |

## 差异

无。
