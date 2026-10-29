# tripo v3 对齐实测报告

- 官方 API：`https://openapi.tripo3d.ai/v3`
- 核对日期：2026-10-29
- 官方请求数：65（其中付费：8）；fake 请求数：42
- 差异：0 项，其中未说明：0 项

只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。

## 步骤

| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |
|---|---|---|---|---|---|---|---|
| balance | `GET /account/balance` | Authorization: Bearer <key> | 200/200 | 6 |  |  | 0 (0) |
| balance-no-auth | `GET /account/balance` | none | 401/401 | 5 |  |  | 0 (0) |
| task-not-found | `GET /tasks/00000000-0000-0000-0000-000000000000` | Authorization: Bearer <key> | 404/404 | 6 |  |  | 0 (0) |
| create-invalid | `POST /generation/text-to-model` | Authorization: Bearer <key> | 400/400 | 6 |  |  | 0 (0) |
| upload | `POST /files` | Authorization: Bearer <key> | 200/200 | 5 |  |  | 0 (0) |
| text-to-model | `POST /generation/text-to-model` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| text-to-model-poll | `GET /tasks/{gen}` | Authorization: Bearer <key> | 200/200 | 24 | running > success | data.output.rendered_image_url binary/octet-stream png; data.output.generated_image_url binary/octet-stream jpeg; data.output.model_url binary/octet-stream glb | 0 (0) |
| rig-check | `POST /animations/rig-check` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| rig-check-poll | `GET /tasks/{rigcheck}` | Authorization: Bearer <key> | 200/200 | 16 | queued > success |  | 0 (0) |
| rig | `POST /animations/rig` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| rig-poll | `GET /tasks/{rig}` | Authorization: Bearer <key> | 200/200 | 19 | queued > running > success | data.output.model_url binary/octet-stream glb; data.output.rendered_image_url binary/octet-stream png | 0 (0) |
| retarget | `POST /animations/retarget` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| retarget-poll | `GET /tasks/{retarget}` | Authorization: Bearer <key> | 200/200 | 19 | running > failed |  | 0 (0) |
| segment | `POST /mesh/segment` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| segment-poll | `GET /tasks/{seg}` | Authorization: Bearer <key> | 200/200 | 19 | running > success | data.output.rendered_image_url binary/octet-stream png; data.output.model_url binary/octet-stream glb | 0 (0) |
| complete | `POST /mesh/complete` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| complete-poll | `GET /tasks/{complete}` | Authorization: Bearer <key> | 200/200 | 16 | running > success | data.output.model_url binary/octet-stream glb | 0 (0) |
| rig-v3 | `POST /animations/rig` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| rig-v3-poll | `GET /tasks/{rigv3}` | Authorization: Bearer <key> | 200/200 | 19 | queued > success | data.output.model_url binary/octet-stream glb; data.output.rendered_image_url binary/octet-stream png | 0 (0) |
| retarget-v3 | `POST /animations/retarget` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| retarget-v3-poll | `GET /tasks/{retargetv3}` | Authorization: Bearer <key> | 200/200 | 19 | running > success | data.output.model_url binary/octet-stream glb | 0 (0) |
| segment-model | `GET /tasks/{seg}` | Authorization: Bearer <key> | 200/200 | 19 |  |  | 0 (0) |
| convert | `POST /models/convert` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| convert-poll | `GET /tasks/{convert}` | Authorization: Bearer <key> | 200/200 | 21 | queued > running > success | data.output.model_url binary/octet-stream stl-binary | 0 (0) |

## 端点证据

`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。

| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |
|---|---|---|---|---|---|
| `GET /account/balance` | real | GET /v3/account/balance | https://developers.tripo3d.ai/en/docs/introduction | 2026-10-29 |  |
| `POST /files` | real | POST /v3/files | https://developers.tripo3d.ai/en/docs/files | 2026-10-29 | the sample's multipart reference picture is replaced by a fixed recipe; the response is real |
| `GET /tasks/{task_id}` | real | GET /v3/tasks/{id} | https://developers.tripo3d.ai/en/docs/task-query | 2026-10-29 | queued, running, success and failed poll bodies are real for the real kinds |
| `GET /artifacts/{task_id}/{name}` | constructed-from-docs |  | https://developers.tripo3d.ai/en/docs/task-query | 2026-09-30 | the official artifact CDN host is not part of the API; this route serves the fixed files its task output URLs name (content type binary/octet-stream as observed) |
| `POST /generation/text-to-model` | real | POST /v3/generation/text-to-model | https://developers.tripo3d.ai/en/docs/generation-text-to-model/standard | 2026-10-29 |  |
| `POST /animations/rig-check` | real | POST /v3/animations/rig-check | https://developers.tripo3d.ai/en/docs/animations-rig-check | 2026-10-29 |  |
| `POST /animations/rig` | real | POST /v3/animations/rig | https://developers.tripo3d.ai/en/docs/animations-rig | 2026-10-29 |  |
| `POST /animations/retarget` | real | POST /v3/animations/retarget | https://developers.tripo3d.ai/en/docs/animations-retarget | 2026-10-29 |  |
| `POST /mesh/segment` | real | POST /v3/mesh/segment | https://developers.tripo3d.ai/en/docs/mesh-segment | 2026-10-29 |  |
| `POST /mesh/complete` | real | POST /v3/mesh/complete | https://developers.tripo3d.ai/en/docs/mesh-complete | 2026-10-29 |  |
| `POST /models/convert` | real | POST /v3/models/convert | https://developers.tripo3d.ai/en/docs/models-convert | 2026-10-29 |  |
| `POST /generation/image-to-model` | constructed-from-docs |  | https://developers.tripo3d.ai/en/docs/generation-image-to-model/standard | 2026-09-30 | no real sample (not requested to save credits): the create envelope is the real one of text-to-model; task type image_to_model and the output structure of text_to_model are assumptions to verify on first real use; the documentation was researched on 2026-09-30 and could not be fetched on 2026-10-29 |
| `POST /generation/image-to-splat` | constructed-from-docs |  | https://developers.tripo3d.ai/en/docs/generation-image-to-splat | 2026-09-30 | no real sample (not requested to save credits): the create envelope is the real one of text-to-model; task type image_to_splat and the output structure of text_to_model are assumptions to verify on first real use; the documentation was researched on 2026-09-30 and could not be fetched on 2026-10-29 |
| `POST /generation/image-to-multiview` | constructed-from-docs |  | https://developers.tripo3d.ai/en/docs/generation-image-to-multiview | 2026-09-30 | no real sample (not requested to save credits): the create envelope is the real one of text-to-model; task type image_to_multiview and the output structure of text_to_model are assumptions to verify on first real use; the documentation was researched on 2026-09-30 and could not be fetched on 2026-10-29 |
| `POST /generation/edit-multiview` | constructed-from-docs |  | https://developers.tripo3d.ai/en/docs/generation-edit-multiview | 2026-09-30 | no real sample (not requested to save credits): the create envelope is the real one of text-to-model; task type edit_multiview and the output structure of text_to_model are assumptions to verify on first real use; the documentation was researched on 2026-09-30 and could not be fetched on 2026-10-29 |
| `POST /models/texture` | constructed-from-docs |  | https://developers.tripo3d.ai/en/docs/models-texture | 2026-09-30 | no real sample (not requested to save credits): the create envelope is the real one of text-to-model; task type texture_model and the output structure of text_to_model are assumptions to verify on first real use; the documentation was researched on 2026-09-30 and could not be fetched on 2026-10-29 |
| `POST /mesh/decimate` | constructed-from-docs |  | https://developers.tripo3d.ai/en/docs/mesh-decimate | 2026-09-30 | no real sample (not requested to save credits): the create envelope is the real one of text-to-model; task type mesh_decimation and the output structure of mesh_completion are assumptions to verify on first real use; the documentation was researched on 2026-09-30 and could not be fetched on 2026-10-29 |
| `POST /mesh/smartsegment` | constructed-from-docs |  | https://developers.tripo3d.ai/en/docs/mesh-smartsegment | 2026-09-30 | no real sample (not requested to save credits): the create envelope is the real one of text-to-model; task type mesh_smart_segmentation and the output structure of mesh_segmentation are assumptions to verify on first real use; the documentation was researched on 2026-09-30 and could not be fetched on 2026-10-29 |

## 差异

无。
