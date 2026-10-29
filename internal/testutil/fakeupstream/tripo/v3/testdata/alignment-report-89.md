# tripo v3 对齐实测报告

- 官方 API：`https://openapi.tripo3d.ai/v3`
- 核对日期：2026-10-29
- 官方请求数：208（其中付费：10）；fake 请求数：85
- 差异：10 项，其中未说明：0 项

只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。

## 步骤

| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |
|---|---|---|---|---|---|---|---|
| image-upload | `POST /files` | Authorization: Bearer <key> | 200/200 | 5 |  |  | 0 (0) |
| source-model | `POST /generation/text-to-model` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| source-model-poll | `GET /tasks/{source}` | Authorization: Bearer <key> | 200/200 | 24 | queued > running > success |  | 0 (0) |
| image-to-model | `POST /generation/image-to-model` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| image-to-model-poll | `GET /tasks/{i2m}` | Authorization: Bearer <key> | 200/200 | 27 | running > success | data.output.model_url binary/octet-stream glb; data.output.rendered_image_url binary/octet-stream png | 0 (0) |
| image-to-splat | `POST /generation/image-to-splat` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| image-to-splat-poll | `GET /tasks/{splat}` | Authorization: Bearer <key> | 200/200 | 21 | running > success | data.output.model_url binary/octet-stream binary | 0 (0) |
| image-to-multiview | `POST /generation/image-to-multiview` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| image-to-multiview-poll | `GET /tasks/{mv}` | Authorization: Bearer <key> | 200/200 | 24 | queued > running > success | data.output.generate_multiview_image.right_view_url binary/octet-stream jpeg; data.output.generate_multiview_image.front_view_url binary/octet-stream jpeg; data.output.generate_multiview_image.left_view_url binary/octet-stream jpeg; data.output.generate_multiview_image.back_view_url binary/octet-stream jpeg | 0 (0) |
| edit-multiview | `POST /generation/edit-multiview` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| edit-multiview-poll | `GET /tasks/{emv}` | Authorization: Bearer <key> | 200/200 | 21 | running > success | data.output.generate_multiview_image.front_view_url image/jpeg jpeg | 0 (0) |
| texture | `POST /models/texture` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| texture-poll | `GET /tasks/{texture}` | Authorization: Bearer <key> | 200/200 | 25 | running > success | data.output.rendered_image_url binary/octet-stream png; data.output.model_url binary/octet-stream glb | 0 (0) |
| decimate | `POST /mesh/decimate` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| decimate-poll | `GET /tasks/{decimate}` | Authorization: Bearer <key> | 200/200 | 19 | running > success | data.output.model_url binary/octet-stream glb; data.output.rendered_image_url binary/octet-stream png | 0 (0) |
| smartsegment-image | `POST /mesh/smartsegment` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| smartsegment-image-poll | `GET /tasks/{sseg}` | Authorization: Bearer <key> | 200/200 | 15 | running > failed |  | 10 (0) |
| figure-upload | `POST /files` | Authorization: Bearer <key> | 200/200 | 5 |  |  | 0 (0) |
| smartsegment-figure | `POST /mesh/smartsegment` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| smartsegment-figure-poll | `GET /tasks/{fseg}` | Authorization: Bearer <key> | 200/200 | 20 | queued > running > success | data.output.seg_model_url binary/octet-stream glb; data.output.mask_url image/png png | 0 (0) |
| model-upload | `POST /files` | Authorization: Bearer <key> | 200/200 | 5 |  |  | 0 (0) |
| smartsegment-model | `POST /mesh/smartsegment` | Authorization: Bearer <key> | 200/200 | 7 |  |  | 0 (0) |
| smartsegment-model-poll | `GET /tasks/{mseg}` | Authorization: Bearer <key> | 200/200 | 21 | queued > running > success | data.output.seg_model_url binary/octet-stream glb; data.output.mask_url image/png png | 0 (0) |

## 请求差异说明

- 第二段官方运行（#89，2026-10-29，标签 seven、seg2、mseg）补采七个原先按文档构造的端点：`image-to-model`、`image-to-splat`、`image-to-multiview`、`edit-multiview`、`models/texture`、`mesh/decimate`、`mesh/smartsegment`（image 与 model 两种 seg_type）。每个端点取最便宜且保留 Plinth 请求体字段的选项，只发一次；付费 10 次共 260 积分（余额 3010 → 2750）：text-to-model 源任务 10、image-to-model 20、image-to-splat 30、image-to-multiview 10、edit-multiview 5、texture 10、decimate 35、smartsegment-image 85、smartsegment-model 55；第一个 smartsegment-image 请求失败未计费。
- 七个端点的任务 type 有两处与原先的假设不同：mesh/decimate 是 `highpoly_to_lowpoly`（不是 `mesh_decimation`），mesh/smartsegment 按 seg_type 是 `smartsegment_image` 或 `smartsegment_model`（不是 `mesh_smart_segmentation`）；其余五个沿用相近类型的假设成立。输出字段与原假设也不同：image-to-model、texture 没有 `generated_image_url`，image-to-splat 只有 `model_url`，两个 multiview 的输出是嵌套对象 `generate_multiview_image`（edit 只含被编辑的视图），smartsegment 输出 `seg_model_url`、`mask_url`、`prompt`、`seg_task_id`、`model_task_id`；部分任务类型的部分状态没有 `progress`。fake 按样本改正。
- 第一个 smartsegment-image 请求（球体示意图）受理后约四分钟以 `failed`、error_code 1001、`Segmentation pipeline failed` 结束，未计费；换一张有头、身体、手臂和腿的图片的第二个请求成功。两次都入库为样本（`tripo-v3-smartsegment-image-failed`、`tripo-v3-smartsegment-image`）。第一次的失败是官方对该输入的观测，原因未证；报告里 `smartsegment-image-poll` 步骤的差异就是 fake 对同一请求返回成功。
- 产物 CDN 对不同文件返回不同的 content-type：模型、渲染图和 multiview 图片是 `binary/octet-stream`，分割遮罩 `mask.png` 是 `image/png`，edit-multiview 的视图是 `image/jpeg`；fake 照此返回。CDN 是 CloudFront 签名 URL，无效或过期都是 403 `text/xml` 的 AccessDenied。
- 这一段报告只含第二段官方运行的步骤：第一阶段的官方原始运行（余额、生成、绑骨、分割等）没有保留在工作树外，本次不能重新生成那份报告，它仍在 `alignment-report.md`。

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
| `POST /generation/image-to-model` | real | POST /v3/generation/image-to-model | https://developers.tripo3d.ai/en/docs/generation-image-to-model/standard | 2026-10-29 | captured: task type image_to_model, 20 credits for a geometry-only v3.1 model; the output has model_url and rendered_image_url (no generated_image_url, which only a text prompt produces) |
| `POST /generation/image-to-splat` | real | POST /v3/generation/image-to-splat | https://developers.tripo3d.ai/en/docs/generation-image-to-splat | 2026-10-29 | captured: task type image_to_splat, 30 credits; the only output is model_url (a .splat file) |
| `POST /generation/image-to-multiview` | real | POST /v3/generation/image-to-multiview | https://developers.tripo3d.ai/en/docs/generation-image-to-multiview | 2026-10-29 | captured: task type image_to_multiview, 10 credits; the output is the object generate_multiview_image with front/left/back/right_view_url (JPEG) |
| `POST /generation/edit-multiview` | real | POST /v3/generation/edit-multiview | https://developers.tripo3d.ai/en/docs/generation-edit-multiview | 2026-10-29 | captured: task type edit_multiview, 5 credits; input.original_task_id is the multiview task and the output carries only the edited views (one prompt, one front_view_url) |
| `POST /models/texture` | real | POST /v3/models/texture | https://developers.tripo3d.ai/en/docs/models-texture | 2026-10-29 | captured: task type texture_model, 10 credits at texture_quality standard without PBR; the output has model_url and rendered_image_url |
| `POST /mesh/decimate` | real | POST /v3/mesh/decimate | https://developers.tripo3d.ai/en/docs/mesh-decimate | 2026-10-29 | captured: the task type is highpoly_to_lowpoly (not mesh_decimation as assumed), 35 credits; the output has model_url and rendered_image_url |
| `POST /mesh/smartsegment` | real | POST /v3/mesh/smartsegment | https://developers.tripo3d.ai/en/docs/mesh-smartsegment | 2026-10-29 | captured for both seg_type values: smartsegment_image (85 credits; the first request, on a shaded sphere, ended failed with error 1001 "Segmentation pipeline failed" and was not charged, a second picture with distinct parts succeeded) and smartsegment_model (55 credits, a 36 KB GLB uploaded to /files); the output is seg_model_url, mask_url, prompt, seg_task_id and model_task_id |

## 差异

- `smartsegment-image-poll` / field-extra-in-fake / $.data.credits_consumed：官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本
- `smartsegment-image-poll` / field-extra-in-fake / $.data.output.mask_url：官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本
- `smartsegment-image-poll` / field-extra-in-fake / $.data.output.model_task_id：官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本
- `smartsegment-image-poll` / field-extra-in-fake / $.data.output.prompt：官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本
- `smartsegment-image-poll` / field-extra-in-fake / $.data.output.seg_model_url：官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本
- `smartsegment-image-poll` / field-extra-in-fake / $.data.output.seg_task_id：官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本
- `smartsegment-image-poll` / field-extra-in-fake / $.data.progress：官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本
- `smartsegment-image-poll` / field-missing-in-fake / $.data.error_code：官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本
- `smartsegment-image-poll` / field-missing-in-fake / $.data.error_message：官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本
- `smartsegment-image-poll` / task-status-flow / official running>failed, fake queued>running>success：官方的第一个 smartsegment-image 请求（球体示意图）以 failed 结束（error_code 1001，未计费）；fake 对同一请求返回成功，失败由 task-failed 场景单独注入，失败体的结构取自 `tripo-v3-smartsegment-image-failed` 样本
