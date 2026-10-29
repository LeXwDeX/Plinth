# meshy openapi-v1 对齐实测报告

- 官方 API：`https://api.meshy.ai/openapi/v1`
- 核对日期：2026-10-29
- 官方请求数：181（其中付费：16）；fake 请求数：88
- 差异：0 项，其中未说明：0 项

只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。

## 步骤

| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |
|---|---|---|---|---|---|---|---|
| balance | `GET /balance` | Authorization: Bearer <key> | 200/200 | 5 |  |  | 0 (0) |
| balance-no-auth | `GET /balance` | none | 401/401 | 2 |  |  | 0 (0) |
| task-not-found | `GET /image-to-3d/01a18514-059e-73f7-a0ce-0f88a12c9300` | Authorization: Bearer <key> | 404/404 | 2 |  |  | 0 (0) |
| task-bad-id | `GET /image-to-3d/not-an-id` | Authorization: Bearer <key> | 400/400 | 2 |  |  | 0 (0) |
| create-invalid | `POST /image-to-3d` | Authorization: Bearer <key> | 400/400 | 2 |  |  | 0 (0) |
| create-invalid-remesh | `POST /remesh` | Authorization: Bearer <key> | 400/400 | 2 |  |  | 0 (0) |
| text-to-image | `POST /text-to-image` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| text-to-image-poll | `GET /text-to-image/{t2i}` | Authorization: Bearer <key> | 200/200 | 14 | IN_PROGRESS > SUCCEEDED | image_urls.0 image/png png | 0 (0) |
| text-to-image-url | `GET /text-to-image/{t2i}` | Authorization: Bearer <key> | 200/200 | 14 |  |  | 0 (0) |
| image-to-image | `POST /image-to-image` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| image-to-image-poll | `GET /image-to-image/{i2i}` | Authorization: Bearer <key> | 200/200 | 14 | IN_PROGRESS > SUCCEEDED | image_urls.0 image/png png | 0 (0) |
| image-to-3d | `POST /image-to-3d` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| image-to-3d-poll | `GET /image-to-3d/{i23d}` | Authorization: Bearer <key> | 200/200 | 27 | IN_PROGRESS > SUCCEEDED | model_urls.glb model/gltf-binary glb; thumbnail_url image/png png; texture_urls.0.base_color image/png png | 0 (0) |
| image-to-3d-model | `GET /image-to-3d/{i23d}` | Authorization: Bearer <key> | 200/200 | 27 |  |  | 0 (0) |
| multi-image-to-3d | `POST /multi-image-to-3d` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| multi-image-to-3d-poll | `GET /multi-image-to-3d/{mi23d}` | Authorization: Bearer <key> | 200/200 | 26 | IN_PROGRESS > SUCCEEDED | model_urls.glb model/gltf-binary glb; thumbnail_url image/png png | 0 (0) |
| remesh | `POST /remesh` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| remesh-poll | `GET /remesh/{remesh}` | Authorization: Bearer <key> | 200/200 | 31 | IN_PROGRESS > SUCCEEDED | model_urls.glb model/gltf-binary glb; thumbnail_url image/png png | 0 (0) |
| convert | `POST /convert` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| convert-poll | `GET /convert/{convert}` | Authorization: Bearer <key> | 200/200 | 26 | IN_PROGRESS > SUCCEEDED | model_urls.glb model/gltf-binary glb; model_urls.stl model/stl stl-binary | 0 (0) |
| retexture | `POST /retexture` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| retexture-poll | `GET /retexture/{retexture}` | Authorization: Bearer <key> | 200/200 | 27 | IN_PROGRESS > SUCCEEDED | model_urls.glb model/gltf-binary glb; thumbnail_url image/png png; texture_urls.0.base_color image/png png | 0 (0) |
| rigging | `POST /rigging` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| rigging-poll | `GET /rigging/{rig}` | Authorization: Bearer <key> | 200/200 | 22 | IN_PROGRESS > SUCCEEDED | result.basic_animations.running_fbx_url application/octet-stream fbx-binary; result.basic_animations.running_armature_glb_url model/gltf-binary glb; result.basic_animations.walking_glb_url model/gltf-binary glb; result.basic_animations.walking_fbx_url application/octet-stream fbx-binary; result.basic_animations.walking_armature_glb_url model/gltf-binary glb; result.basic_animations.running_glb_url model/gltf-binary glb; result.rigged_character_fbx_url application/octet-stream fbx-binary; result.rigged_character_glb_url model/gltf-binary glb | 0 (0) |
| animation | `POST /animations` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| animation-poll | `GET /animations/{anim}` | Authorization: Bearer <key> | 200/200 | 18 | IN_PROGRESS > SUCCEEDED | result.animation_glb_url model/gltf-binary glb; result.animation_fbx_url application/octet-stream fbx-binary | 0 (0) |
| remesh-quad | `POST /remesh` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| remesh-quad-poll | `GET /remesh/{remeshquad}` | Authorization: Bearer <key> | 200/200 | 31 | IN_PROGRESS > SUCCEEDED | model_urls.glb model/gltf-binary glb | 0 (0) |
| rigging-oversized | `POST /rigging` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| rigging-oversized-poll | `GET /rigging/{rigbig}` | Authorization: Bearer <key> | 200/200 | 20 | IN_PROGRESS > FAILED |  | 0 (0) |
| uv-unwrap | `POST /uv-unwrap` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| uv-unwrap-poll | `GET /uv-unwrap/{uv}` | Authorization: Bearer <key> | 200/200 | 25 | IN_PROGRESS > SUCCEEDED | model_urls.glb model/gltf-binary glb; thumbnail_url image/png png | 0 (0) |
| resize | `POST /resize` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| resize-poll | `GET /resize/{resize}` | Authorization: Bearer <key> | 200/200 | 25 | IN_PROGRESS > SUCCEEDED | model_urls.glb model/gltf-binary glb | 0 (0) |
| print-analyze | `POST /print/analyze` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| print-analyze-poll | `GET /print/analyze/{analyze}` | Authorization: Bearer <key> | 200/200 | 35 | IN_PROGRESS > SUCCEEDED |  | 0 (0) |
| print-multi-color | `POST /print/multi-color` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| print-multi-color-poll | `GET /print/multi-color/{multicolor}` | Authorization: Bearer <key> | 200/200 | 25 | IN_PROGRESS > SUCCEEDED | model_urls.3mf model/3mf zip | 0 (0) |
| text-to-motion | `POST /text-to-motion` | Authorization: Bearer <key> | 202/202 | 2 |  |  | 0 (0) |
| text-to-motion-poll | `GET /text-to-motion/{motion}` | Authorization: Bearer <key> | 200/200 | 17 | IN_PROGRESS > SUCCEEDED | result.motion_url application/octet-stream fbx-binary | 0 (0) |
| delete-text-to-image | `DELETE /text-to-image/{t2i}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-image-to-3d | `DELETE /image-to-3d/{i23d}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-remesh | `DELETE /remesh/{remesh}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-text-to-motion | `DELETE /text-to-motion/{motion}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-image-to-image | `DELETE /image-to-image/{i2i}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-multi-image-to-3d | `DELETE /multi-image-to-3d/{mi23d}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-remesh-quad | `DELETE /remesh/{remeshquad}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-convert | `DELETE /convert/{convert}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-retexture | `DELETE /retexture/{retexture}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-rigging | `DELETE /rigging/{rig}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-rigging-oversized | `DELETE /rigging/{rigbig}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-animation | `DELETE /animations/{anim}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-uv-unwrap | `DELETE /uv-unwrap/{uv}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-resize | `DELETE /resize/{resize}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-print-analyze | `DELETE /print/analyze/{analyze}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |
| delete-print-multi-color | `DELETE /print/multi-color/{multicolor}` | Authorization: Bearer <key> | 200/200 | 0 |  |  | 0 (0) |

## 请求差异说明

- 每个付费请求都取该端点最便宜的参数（nano-banana-2、meshy-7.1、standard 几何、2k 贴图、1 万面三角形、无 PBR），每个端点只发一次，没有重放已消费的请求。
- 处理类端点（remesh、convert、retexture、rigging、animations）经 `input_task_id` 引用前面步骤的任务；uv-unwrap、resize、print/analyze、print/multi-color 与 image-to-3d 的图片经 `model_url`/`image_url` 引用前面步骤产物的公开 URL。Plinth 对本地来源改发 data URI（`inline_model_input`），端点与响应结构相同，只有该字段的值不同。
- 官方 API 对 `rigging` 的输入面数上限（32 万）在提交时不返回 400：300000 面 quad 的 Remesh 来源（约 60 万三角面）被接受（202），任务在运行 10% 处以 FAILED、`task_error.type=invalid_input` 结束。fake 按此复现（见 `rigging-oversized` 两步）。
- 16 个删除步骤中，4 个随主运行发出，其余 12 个在主运行结束后用同一批任务 id 补发（两段官方请求按步骤合并到报告）；删除不收费。
- 官方 DELETE 任务返回 200 且无响应体；任务在 IN_PROGRESS 时删除按官方文档返回 409，实测运行只删除了已结束任务，该情形按文档构造。

## 端点证据

`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。

| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |
|---|---|---|---|---|---|
| `GET /balance` | real | GET /openapi/v1/balance | https://docs.meshy.ai/en/api/balance | 2026-10-29 |  |
| `GET /assets/{task_id}/{name}` | constructed-from-docs |  | https://docs.meshy.ai/en/api/remesh | 2026-10-29 | the official CDN host is not part of the API; this route serves the fixed files a task's URLs name. Content types of glb, png, fbx, stl and 3mf are as observed; obj, mtl and usdz were not requested in the run and follow the Meshy adapter's notes |
| `POST /text-to-image` | real | POST /openapi/v1/text-to-image | https://docs.meshy.ai/en/api/text-to-image | 2026-10-29 |  |
| `GET /text-to-image/{id}` | real | GET /openapi/v1/text-to-image/{id} | https://docs.meshy.ai/en/api/text-to-image | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /text-to-image/{id}` | real | DELETE /openapi/v1/text-to-image/{id} | https://docs.meshy.ai/en/api/text-to-image | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /image-to-image` | real | POST /openapi/v1/image-to-image | https://docs.meshy.ai/en/api/image-to-image | 2026-10-29 |  |
| `GET /image-to-image/{id}` | real | GET /openapi/v1/image-to-image/{id} | https://docs.meshy.ai/en/api/image-to-image | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /image-to-image/{id}` | real | DELETE /openapi/v1/image-to-image/{id} | https://docs.meshy.ai/en/api/image-to-image | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /image-to-3d` | real | POST /openapi/v1/image-to-3d | https://docs.meshy.ai/en/api/image-to-3d | 2026-10-29 |  |
| `GET /image-to-3d/{id}` | real | GET /openapi/v1/image-to-3d/{id} | https://docs.meshy.ai/en/api/image-to-3d | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /image-to-3d/{id}` | real | DELETE /openapi/v1/image-to-3d/{id} | https://docs.meshy.ai/en/api/image-to-3d | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /multi-image-to-3d` | real | POST /openapi/v1/multi-image-to-3d | https://docs.meshy.ai/en/api/multi-image-to-3d | 2026-10-29 |  |
| `GET /multi-image-to-3d/{id}` | real | GET /openapi/v1/multi-image-to-3d/{id} | https://docs.meshy.ai/en/api/multi-image-to-3d | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /multi-image-to-3d/{id}` | real | DELETE /openapi/v1/multi-image-to-3d/{id} | https://docs.meshy.ai/en/api/multi-image-to-3d | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /remesh` | real | POST /openapi/v1/remesh | https://docs.meshy.ai/en/api/remesh | 2026-10-29 |  |
| `GET /remesh/{id}` | real | GET /openapi/v1/remesh/{id} | https://docs.meshy.ai/en/api/remesh | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /remesh/{id}` | real | DELETE /openapi/v1/remesh/{id} | https://docs.meshy.ai/en/api/remesh | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /convert` | real | POST /openapi/v1/convert | https://docs.meshy.ai/en/api/convert | 2026-10-29 |  |
| `GET /convert/{id}` | real | GET /openapi/v1/convert/{id} | https://docs.meshy.ai/en/api/convert | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /convert/{id}` | real | DELETE /openapi/v1/convert/{id} | https://docs.meshy.ai/en/api/convert | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /retexture` | real | POST /openapi/v1/retexture | https://docs.meshy.ai/en/api/retexture | 2026-10-29 |  |
| `GET /retexture/{id}` | real | GET /openapi/v1/retexture/{id} | https://docs.meshy.ai/en/api/retexture | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /retexture/{id}` | real | DELETE /openapi/v1/retexture/{id} | https://docs.meshy.ai/en/api/retexture | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /rigging` | real | POST /openapi/v1/rigging | https://docs.meshy.ai/en/api/rigging-and-animation | 2026-10-29 | an input over 320000 faces is accepted (202) and the task ends FAILED with an invalid_input task_error (observed with a 300000-face quad remesh); the documentation says 400, the official API did not answer it at submit |
| `GET /rigging/{id}` | real | GET /openapi/v1/rigging/{id} | https://docs.meshy.ai/en/api/rigging-and-animation | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /rigging/{id}` | real | DELETE /openapi/v1/rigging/{id} | https://docs.meshy.ai/en/api/rigging-and-animation | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /animations` | real | POST /openapi/v1/animations | https://docs.meshy.ai/en/api/rigging-and-animation | 2026-10-29 |  |
| `GET /animations/{id}` | real | GET /openapi/v1/animations/{id} | https://docs.meshy.ai/en/api/rigging-and-animation | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /animations/{id}` | real | DELETE /openapi/v1/animations/{id} | https://docs.meshy.ai/en/api/rigging-and-animation | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /uv-unwrap` | real | POST /openapi/v1/uv-unwrap | https://docs.meshy.ai/en/api/uv-unwrap | 2026-10-29 |  |
| `GET /uv-unwrap/{id}` | real | GET /openapi/v1/uv-unwrap/{id} | https://docs.meshy.ai/en/api/uv-unwrap | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /uv-unwrap/{id}` | real | DELETE /openapi/v1/uv-unwrap/{id} | https://docs.meshy.ai/en/api/uv-unwrap | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /resize` | real | POST /openapi/v1/resize | https://docs.meshy.ai/en/api/resize | 2026-10-29 |  |
| `GET /resize/{id}` | real | GET /openapi/v1/resize/{id} | https://docs.meshy.ai/en/api/resize | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /resize/{id}` | real | DELETE /openapi/v1/resize/{id} | https://docs.meshy.ai/en/api/resize | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /print/analyze` | real | POST /openapi/v1/print/analyze | https://docs.meshy.ai/en/api/print | 2026-10-29 | the report's printability._version key cannot be held by the sample sanitizer; the fake adds it back |
| `GET /print/analyze/{id}` | real | GET /openapi/v1/print/analyze/{id} | https://docs.meshy.ai/en/api/print | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /print/analyze/{id}` | real | DELETE /openapi/v1/print/analyze/{id} | https://docs.meshy.ai/en/api/print | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /print/multi-color` | real | POST /openapi/v1/print/multi-color | https://docs.meshy.ai/en/api/print | 2026-10-29 |  |
| `GET /print/multi-color/{id}` | real | GET /openapi/v1/print/multi-color/{id} | https://docs.meshy.ai/en/api/print | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /print/multi-color/{id}` | real | DELETE /openapi/v1/print/multi-color/{id} | https://docs.meshy.ai/en/api/print | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |
| `POST /text-to-motion` | real | POST /openapi/v1/text-to-motion | https://docs.meshy.ai/en/api/text-to-motion | 2026-10-29 |  |
| `GET /text-to-motion/{id}` | real | GET /openapi/v1/text-to-motion/{id} | https://docs.meshy.ai/en/api/text-to-motion | 2026-10-29 | IN_PROGRESS and SUCCEEDED polls are real; PENDING reuses the in-progress structure; FAILED is real for rigging and reuses it elsewhere |
| `DELETE /text-to-motion/{id}` | real | DELETE /openapi/v1/text-to-motion/{id} | https://docs.meshy.ai/en/api/text-to-motion | 2026-10-29 | a sample exists for the routes the alignment run deleted (text-to-image, image-to-3d, remesh, text-to-motion); the others answer the same |

## 差异

无。
