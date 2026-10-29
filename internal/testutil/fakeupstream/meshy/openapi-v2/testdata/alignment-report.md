# meshy openapi-v2 对齐实测报告

- 官方 API：`https://api.meshy.ai/openapi/v2`
- 核对日期：2026-10-29
- 官方请求数：22（其中付费：2）；fake 请求数：13
- 差异：0 项，其中未说明：0 项

只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。

## 步骤

| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |
|---|---|---|---|---|---|---|---|
| task-not-found | `GET /text-to-3d/01a18514-059e-73f7-a0ce-0f88a12c9300` | Authorization: Bearer <key> | 404/404 | 2 |  |  | 0 (0) |
| task-no-auth | `GET /text-to-3d/01a18514-059e-73f7-a0ce-0f88a12c9300` | none | 401/401 | 2 |  |  | 0 (0) |
| create-invalid | `POST /text-to-3d` | Authorization: Bearer <key> | 400/400 | 2 |  |  | 0 (0) |
| preview | `POST /text-to-3d` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| preview-poll | `GET /text-to-3d/{preview}` | Authorization: Bearer <key> | 200/200 | 21 | IN_PROGRESS > SUCCEEDED | model_urls.glb model/gltf-binary glb; thumbnail_url image/png png | 0 (0) |
| refine | `POST /text-to-3d` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| refine-poll | `GET /text-to-3d/{refine}` | Authorization: Bearer <key> | 200/200 | 23 | IN_PROGRESS > SUCCEEDED | model_urls.glb model/gltf-binary glb; thumbnail_url image/png png; texture_urls.0.base_color image/png png | 0 (0) |
| delete-preview | `DELETE /text-to-3d/{preview}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-refine | `DELETE /text-to-3d/{refine}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |

## 请求差异说明

- preview 与 refine 各发一次，取最便宜的参数（meshy-7.1、standard 几何、1 万面三角形、2k 无 PBR 贴图）；refine 引用 preview 任务。
- 官方 API 的 v2 路由按 id 查询任一类型的任务（用 v2 路由读到了 rigging 任务），fake 只按路由所属的任务族服务，不模拟这种跨族读取。
- v1 的处理类端点可把 v2 的 preview/refine 任务当 `input_task_id`；fake 的 v1 与 v2 共用任务库，行为一致。

## 端点证据

`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。

| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |
|---|---|---|---|---|---|
| `POST /text-to-3d` | real | POST /openapi/v2/text-to-3d | https://docs.meshy.ai/en/api/text-to-3d | 2026-10-29 | preview and refine are one route; the task type follows mode |
| `GET /text-to-3d/{id}` | real | GET /openapi/v2/text-to-3d/{id} | https://docs.meshy.ai/en/api/text-to-3d | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real for both types; PENDING and FAILED reuse the in-progress structure (a failed one carries the task_error object observed on a failed rigging task) |
| `DELETE /text-to-3d/{id}` | real | DELETE /openapi/v2/text-to-3d/{id} | https://docs.meshy.ai/en/api/text-to-3d | 2026-10-29 |  |
| `GET /assets/{task_id}/{name}` | constructed-from-docs |  | https://docs.meshy.ai/en/api/text-to-3d | 2026-10-29 | the official CDN host is not part of the API; this route serves the fixed files the task's model_urls and thumbnail_url name (content types as observed) |

## 差异

无。
