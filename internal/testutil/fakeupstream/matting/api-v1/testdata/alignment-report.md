# matting api-v1 对齐实测报告

- 官方 API：`dev-test matting service /api/v1`
- 核对日期：2026-10-29
- 官方请求数：33（其中付费：0）；fake 请求数：9
- 差异：0 项，其中未说明：0 项

只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。

## 步骤

| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |
|---|---|---|---|---|---|---|---|
| jobs-no-auth | `POST /jobs` | none | 401/401 | 2 |  |  | 0 (0) |
| jobs-invalid | `POST /jobs` | Authorization: Bearer <key> | 422/422 | 8 |  |  | 0 (0) |
| jobs | `POST /jobs` | Authorization: Bearer <key> | 200/200 | 16 |  |  | 0 (0) |
| batch | `GET /batches/{batch}` | Authorization: Bearer <key> | 200/200 | 16 | READY > RUNNING > SUCCESS |  | 0 (0) |
| batch-unknown | `GET /batches/00000000-0000-4000-8000-000000000000` | Authorization: Bearer <key> | 404/404 | 2 |  |  | 0 (0) |
| download | `GET /tasks/{task}/download` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| download-no-auth | `GET /tasks/{task}/download` | none | 401/401 | 2 |  |  | 0 (0) |

## 请求差异说明

- 官方服务是运营方自己的 GPU 服务，通过明文 HTTP 访问；wirefixture 只接受 https，提交的样本把请求 URL 规范为 https 并把主机别名成 example.test，响应字节不变。
- 服务没有按次计价，所有步骤都不计为付费；提交只发一次，轮询约 30 次直到批次 SUCCESS。
- 批次与任务 id（batch_xxx、task_xxx）与 download_path 在样本里是占位，fake 自行生成同形态的 id 和 `/api/v1/tasks/<id>/download` 路径。
- 没有观测到 FAILED 批次、未知任务的下载 404：fake 按已观测的字段结构构造（端点表标明）。

## 端点证据

`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。

| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |
|---|---|---|---|---|---|
| `POST /jobs` | real | POST /api/v1/jobs | internal matting service (FastAPI /docs) | 2026-10-29 | multipart request (files + pipeline_params_json); 401 {detail}, 422 validation list and the READY batch envelope are real samples |
| `GET /batches/{batch_id}` | real | GET /api/v1/batches/{id} | internal matting service (FastAPI /docs) | 2026-10-29 | READY, RUNNING and SUCCESS polls and the 404 {detail} are real. The FAILED batch (task FAILED with error_message) was never observed: it is built from the SUCCESS poll with the task failed and not downloadable |
| `GET /tasks/{task_id}/download` | real | GET /api/v1/tasks/{id}/download | internal matting service (FastAPI /docs) | 2026-10-29 | authenticated download of the PNG result (image/png); 401 {detail} without a key. The service has no signed URLs, so an expired-URL scenario does not apply to it |

## 差异

无。
