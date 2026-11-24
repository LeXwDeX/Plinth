# Fake 验证登记表

> 本文件由 `internal/testutil/fakeupstream/register/register.json` 生成，不要手改。改登记数据后重新生成：
>
> `UPDATE_FAKE_REGISTER=1 go test ./internal/testutil/fakeupstream/register -run Markdown`

每条记录一个 fake 行为（Provider / API / 端点 + 行为类别）以及它的依据：

- `real`：fake 的该行为与仓库内已提交的脱敏真实样本一致；列出样本文件、真实调用日期（样本 meta 的 `captured_at_utc`）、`app_revision` 与 `catalog_revision`（meta 未记录时写 `unknown`）。
- `constructed`：没有真实样本，按所列官方文档或未入库的观测构造，写明核对日期与没有真实样本的原因。`constructed` 不等于已对齐。
- `gap`：已知缺口或差异，写明计划与 Issue。

登记表与 wire coverage 矩阵（`internal/testutil/wirefixture/testdata/wire-coverage.generated.json`）并存：矩阵按端点记 Offering 有无真实样本，共享端点的 Offering 会继承该单元的证据；登记表按行为与模型记录，并在「按行为纠正 wire coverage」一节列出被共享端点误带成 real 的行为。矩阵的生成规则不因登记表改变。

测试（`internal/testutil/fakeupstream/register`）保证：每个 fake 版本的每个端点至少一条记录；`real` 条目引用的样本存在、能被 wirefixture 读取、日期与来源同样本 meta 一致，并且样本里确有该端点的交换；`constructed` 条目有依据与核对日期；`gap` 条目有计划与 Issue；本文件与数据一致。

## 汇总

| Provider / API | real | constructed | gap | 合计 | 最近一次真实调用 |
|---|---|---|---|---|---|
| `tripo/v3` | 22 | 19 | 1 | 42 | 2026-11-23 |
| `openai/v1` | 4 | 9 | 0 | 13 | 2026-11-23 |
| `meshy/openapi-v1` | 19 | 17 | 2 | 38 | 2026-11-23 |
| `meshy/openapi-v2` | 4 | 4 | 2 | 10 | 2026-11-22 |
| `hyper3d/api-v2` | 8 | 11 | 1 | 20 | 2026-11-19 |
| `openai/images-v1` | 3 | 4 | 1 | 8 | 2026-11-09 |
| `gemini/images-v1` | 2 | 3 | 0 | 5 | 2026-10-29 |
| `jimeng/api-v3` | 2 | 4 | 0 | 6 | 2026-10-29 |
| `matting/api-v1` | 3 | 6 | 0 | 9 | 2026-11-23 |
| 合计 | 67 | 77 | 7 | 151 | 2026-11-23 |

「最近一次真实调用」取该版本 `real` 条目所引样本中最新的 `captured_at_utc`；未入库的真实观测不计入。

## 按行为纠正 wire coverage

下列行为在 wire coverage 矩阵里因共享端点显示为有真实证据，登记表按行为记为非 `real`：

| 条目 | 矩阵单元 | Offering | 登记状态 | 原因 |
|---|---|---|---|---|
| `tripo-v3/text-to-model-p-series` | `tripo.v3\|text-to-3d\|POST /v3/generation/text-to-model` | `tripo/tripo-p2/text-to-3d` | constructed | 该单元的 real 来自 v3.1-20260211 的文生样本；P2 与 H 系列共用端点，P2 的请求值、拒绝与价格没有真实样本。矩阵的 Offering 行已是 partial，但单元仍把 P2 列在 real 证据下。 |
| `tripo-v3/image-to-model-p-series` | `tripo.v3\|image-to-3d\|POST /v3/generation/image-to-model` | `tripo/tripo-p2/image-to-3d` | constructed | 该单元的 real 来自 v3.1-20260211 的图生样本；P2 共用端点但没有真实样本。 |
| `tripo-v3/multiview-to-model-p-series` | `tripo.v3\|image-to-3d\|POST /v3/generation/multiview-to-model` | `tripo/tripo-p2-multiview/image-to-3d` | constructed | 该单元的 real 来自 v3.1-20260211 的多视图样本（evidence-386）；P2 多视图共用端点但没有真实样本。 |
| `meshy-openapi-v1/t25-image-to-3d` | `meshy.v2\|image-to-3d\|POST /openapi/v1/image-to-3d` | `meshy/meshy-t2.5/image-to-3d` | gap | 矩阵只比较请求体的 model 字段，Meshy 用 ai_model，所以 meshy-t2.5 继承了 meshy-7.1 样本的 real；在 #569 之前没有任何入库样本请求过 meshy-t2.5。 |
| `meshy-openapi-v2/t25-text-to-3d` | `meshy.v2\|text-to-3d\|POST /openapi/v2/text-to-3d` | `meshy/meshy-t2.5/text-to-3d` | gap | 该单元的 real 来自 meshy-7.1 的样本；meshy-t2.5 共用端点，在 #569 之前没有入库样本请求过它。 |

## 明细

### `tripo/v3`

#### `tripo-v3/balance` · response-shape · real

- 端点：`GET /account/balance`
- 行为：余额查询返回 {code:0, data:{balance, frozen}, status:success} 信封，值固定。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-account.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/evidence-305-rig-convert.candidate.json`（2026-11-16，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/auth-401` · error-code-and-text · real

- 端点：`GET /account/balance`
- 行为：缺少 Key 返回 401，错误信封为 code 2、status error、message 与 suggestion。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-account.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/files-upload` · response-shape · real

- 端点：`POST /files`
- 行为：multipart 上传图片或模型，返回 file_token 信封。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-upload-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-upload-model.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-multiview-2026-11-23.candidate.json`（2026-11-23，derived，app_revision `unknown`，catalog_revision `unknown`）
- 备注：上传的参考图在样本里替换为固定 recipe，响应为真实；2026-11-23 的样本 provenance 为 derived。

#### `tripo-v3/files-refusals` · error-code-and-text · constructed

- 端点：`POST /files`
- 行为：空文件 400/code 2003，不支持的类型 400/code 2004，沿用真实 400 信封。
- 依据：https://developers.tripo3d.ai/en/docs/files（Files）
- 依据：https://developers.tripo3d.ai/en/docs/error-handling（Error handling）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `tripo-v3/task-poll-states` · state-machine · real

- 端点：`GET /tasks/{task_id}`
- 行为：任务轮询 queued → running → success / failed，各状态的轮询体结构与样本一致。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-text-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-smartsegment-image-failed.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-multiview-2026-11-23.candidate.json`（2026-11-23，derived，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/task-cancelled-unknown` · state-machine · constructed

- 端点：`GET /tasks/{task_id}`
- 行为：task-canceled 与 task-unknown 场景的 cancelled / unknown 终态，由真实 failed 轮询去掉 error_code 与 error_message 派生；banned、expired 不出现。
- 依据：https://developers.tripo3d.ai/en/docs/task-lifecycle（Task lifecycle）
- 依据：https://developers.tripo3d.ai/en/docs/task-query（Task query）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `tripo-v3/task-404` · error-code-and-text · real

- 端点：`GET /tasks/{task_id}`
- 行为：未知或不属于本账号的任务返回 404/code 2001 信封。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-account.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/artifacts-route` · artifact-shape · constructed

- 端点：`GET /artifacts/{task_id}/{name}`
- 行为：产物 CDN 不属于 API；该路由提供任务输出 URL 指向的固定文件（content type 按观测为 binary/octet-stream）。
- 依据：https://developers.tripo3d.ai/en/docs/task-query（Task query：output 字段）
- 核对日期：2026-09-30
- 没有真实样本的原因：产物主机不是 API 的一部分，wire 样本只记 API 交换，媒体字节按 recipe 替换

#### `tripo-v3/artifact-expired` · error-code-and-text · constructed

- 端点：`GET /artifacts/{task_id}/{name}`
- 行为：artifact-expired 场景：过期签名 URL 返回 CloudFront 的 403 text/xml AccessDenied 体。
- 依据：官方产物 CDN 上的观测：把签名 URL 的有效期改到过去后请求（#295，2026-10-29）
- 核对日期：2026-10-29
- 没有真实样本的原因：真实调用观测到了，但原始请求与响应没有作为脱敏样本入库；且不是自然过期，只是同一条服务端分支

#### `tripo-v3/text-to-model-h-shape` · response-shape · real

- 端点：`POST /generation/text-to-model`
- 行为：H 系列（v3.1-20260211）文生模型的创建信封与任务对象结构；输出含 model_url、rendered_image_url 与 generated_image_url。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-text-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/create-400-1004` · error-code-and-text · real

- 端点：`POST /generation/text-to-model`
- 行为：缺少或非法参数的创建请求返回 400/code 1004 信封；其余创建端点的参数拒绝复用这一信封。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-account.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/text-to-model-p-series` · request-validation · constructed

- 端点：`POST /generation/text-to-model`
- 行为：P 系列（P2-20260801、P1-20260311）文生模型的请求值、拒绝与报价按官方 P Series 页面构造（generation.go）。
- 依据：https://developers.tripo3d.ai/en/docs/generation-text-to-model/standard（Text to model（P Series））
- 依据：https://developers.tripo3d.ai/en/pricing（Pricing）
- 核对日期：2026-11-24
- 没有真实样本的原因：P 系列从未以文生任务真实请求过；已提交的文生样本只用 H 系列 v3.1-20260211
- 纠正 wire coverage：单元 `tripo.v3|text-to-3d|POST /v3/generation/text-to-model` 下的 `tripo/tripo-p2/text-to-3d`。该单元的 real 来自 v3.1-20260211 的文生样本；P2 与 H 系列共用端点，P2 的请求值、拒绝与价格没有真实样本。矩阵的 Offering 行已是 partial，但单元仍把 P2 列在 real 证据下。

#### `tripo-v3/generation-request-contract` · request-validation · constructed

- 端点：`POST /generation/text-to-model`、`POST /generation/image-to-model`、`POST /generation/multiview-to-model`
- 行为：生成请求契约（#541）：各模型版本允许的参数、取值与来源任务按官方页校验，非法值答真实 400/code 1004 信封。
- 依据：https://developers.tripo3d.ai/en/docs/generation-text-to-model/standard（Text to model）
- 依据：https://developers.tripo3d.ai/en/docs/generation-image-to-model/standard（Image to model）
- 依据：https://developers.tripo3d.ai/en/docs/generation-multiview-to-model/standard（Multiview to model）
- 依据：https://developers.tripo3d.ai/en/docs/changelog（Changelog）
- 核对日期：2026-11-24
- 没有真实样本的原因：参数层面的拒绝没有真实请求过；样本只有一次合法请求与一次缺参 400

#### `tripo-v3/p-quad-fbx` · artifact-shape · constructed

- 端点：`POST /generation/text-to-model`、`POST /generation/image-to-model`、`POST /generation/multiview-to-model`
- 行为：P 系列 quad=true 时输出只有 FBX、没有 GLB（p2_quad.go）；multiview-to-model 上为推定。
- 依据：dev-test 真实运行观测（文生与图生）（#294，2026-11-12）
- 核对日期：2026-11-12
- 没有真实样本的原因：真实调用观测到了，但原始请求与响应没有作为脱敏样本入库；multiview-to-model 未观测，按同系列推定

#### `tripo-v3/image-to-model-h-shape` · response-shape · real

- 端点：`POST /generation/image-to-model`
- 行为：H 系列 v3.1 图生模型：任务类型 image_to_model，输出 model_url 与 rendered_image_url（没有 generated_image_url）。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-image-to-model.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/image-to-model-p-series` · request-validation · constructed

- 端点：`POST /generation/image-to-model`
- 行为：P 系列图生模型的请求值、拒绝与报价按官方 P Series 页面构造。
- 依据：https://developers.tripo3d.ai/en/docs/generation-image-to-model/p（Image to model（P Series））
- 依据：https://developers.tripo3d.ai/en/pricing（Pricing）
- 核对日期：2026-11-24
- 没有真实样本的原因：P 系列从未在图生端点真实请求过
- 纠正 wire coverage：单元 `tripo.v3|image-to-3d|POST /v3/generation/image-to-model` 下的 `tripo/tripo-p2/image-to-3d`。该单元的 real 来自 v3.1-20260211 的图生样本；P2 共用端点但没有真实样本。

#### `tripo-v3/multiview-to-model-shape` · response-shape · real

- 端点：`POST /generation/multiview-to-model`
- 行为：v3.1 多视图建模：创建信封、任务类型 multiview_to_model、queued → running → success，任务对象带 input、model_version 与四项 files，输出 model_url（GLB）与 rendered_image_url（WebP）。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/evidence-386-multiview-model.candidate.json`（2026-11-19，captured，app_revision `unknown`，catalog_revision `unknown`）
- 备注：该样本是对照证据，不是 fake 的模板；端点表仍按文档标 constructed。真实运行中 running 轮询在 progress 99 就给出渲染图，fake 不复现；请求省略参数时真实回显什么未知。

#### `tripo-v3/multiview-to-model-request` · request-validation · constructed

- 端点：`POST /generation/multiview-to-model`
- 行为：inputs 为单键视图对象数组（front 必需，2–4 张），代替 input。
- 依据：https://developers.tripo3d.ai/en/docs/generation-multiview-to-model/standard（Multiview to model）
- 核对日期：2026-11-24
- 没有真实样本的原因：只有一次合法的三视图请求入库（evidence-386），缺视图或越界的拒绝没有真实请求过

#### `tripo-v3/multiview-to-model-p-series` · request-validation · constructed

- 端点：`POST /generation/multiview-to-model`
- 行为：P 系列多视图建模的请求值与报价按官方 P Series 页面构造。
- 依据：https://developers.tripo3d.ai/en/docs/generation-multiview-to-model/standard（Multiview to model（P Series））
- 依据：https://developers.tripo3d.ai/en/pricing（Pricing）
- 核对日期：2026-11-24
- 没有真实样本的原因：P 系列从未在多视图端点真实请求过；唯一的样本 evidence-386 用 v3.1-20260211
- 纠正 wire coverage：单元 `tripo.v3|image-to-3d|POST /v3/generation/multiview-to-model` 下的 `tripo/tripo-p2-multiview/image-to-3d`。该单元的 real 来自 v3.1-20260211 的多视图样本（evidence-386）；P2 多视图共用端点但没有真实样本。

#### `tripo-v3/image-to-splat` · response-shape · real

- 端点：`POST /generation/image-to-splat`
- 行为：任务类型 image_to_splat，输出只有 model_url（.splat）。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-image-to-splat.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/image-to-multiview` · response-shape · real

- 端点：`POST /generation/image-to-multiview`
- 行为：任务类型 image_to_multiview，输出 generate_multiview_image 的四个视图 URL（JPEG）；running 轮询的 output 为 {}，任务回显 input.file.object。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-image-to-multiview.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-multiview-2026-11-20.candidate.json`（2026-11-20，derived，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-multiview-2026-11-23.candidate.json`（2026-11-23，derived，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/edit-multiview` · response-shape · real

- 端点：`POST /generation/edit-multiview`
- 行为：任务类型 edit_multiview，input.original_task_id 指向多视图任务，输出只含被编辑的视图。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-edit-multiview.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-multiview-2026-11-23.candidate.json`（2026-11-23，derived，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/rig-check` · response-shape · real

- 端点：`POST /animations/rig-check`
- 行为：rig-check 的创建与结果结构。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-rig-check.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/evidence-305-rig-convert.candidate.json`（2026-11-16，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/rig-shape` · response-shape · real

- 端点：`POST /animations/rig`
- 行为：rig（biped、spec tripo，v2.5 与 v3.0）的创建与结果；v3.0 运行中即显示 25 credits。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-rigging.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-rigging-v3.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/evidence-305-rig-convert.candidate.json`（2026-11-16，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/rig-request-values` · request-validation · constructed

- 端点：`POST /animations/rig`
- 行为：rig_type、spec、out_format 的取值与 v3.0 专有值按官方页校验，非法值答 400/code 1004；轮询回显 spec、out_format 与骨架类型。
- 依据：https://developers.tripo3d.ai/en/docs/animations-rig（Rig）
- 依据：https://developers.tripo3d.ai/en/docs/animations-rig-check（Rig check）
- 核对日期：2026-11-12
- 没有真实样本的原因：样本只有 biped + tripo 的请求，其余取值未真实请求过

#### `tripo-v3/retarget-shape` · response-shape · real

- 端点：`POST /animations/retarget`
- 行为：retarget（preset:walk，v3.0 biped 骨架）的创建与结果；v2 骨架的 retarget 以 failed、code 1004 结束。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-animation.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-rigging.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/retarget-request-values` · request-validation · constructed

- 端点：`POST /animations/retarget`
- 行为：animation 与 animations 二选一（至多 5 个）、11 个 v3.0 biped 预设、5 个生物预设与 v1.0 preset:biped: 名称；预设与骨架不符时以 failed/1004 结束（推定）。
- 依据：https://developers.tripo3d.ai/en/docs/animations-retarget（Retarget）
- 核对日期：2026-11-12
- 没有真实样本的原因：只真实请求过 preset:walk

#### `tripo-v3/segment` · response-shape · real

- 端点：`POST /mesh/segment`
- 行为：segment 的创建与结果结构。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-segment.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/complete` · response-shape · real

- 端点：`POST /mesh/complete`
- 行为：complete 的创建与结果结构。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-complete.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/smartsegment` · response-shape · real

- 端点：`POST /mesh/smartsegment`
- 行为：smartsegment_image 与 smartsegment_model：输出 seg_model_url、mask_url、prompt、seg_task_id、model_task_id；着色球体输入以 failed/1001 结束且不计费。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-smartsegment-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-smartsegment-image-failed.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-smartsegment-model.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 备注：两种 seg_type 各只观测到一次成功。

#### `tripo-v3/decimate-shape` · response-shape · real

- 端点：`POST /mesh/decimate`
- 行为：任务类型 highpoly_to_lowpoly，输出 model_url 与 rendered_image_url。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-decimate.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/decimate-request` · request-validation · constructed

- 端点：`POST /mesh/decimate`
- 行为：来源只能是文生、图生等生成任务，面数与四边面选项按官方页校验，回显默认值并按面数交付带贴图的 GLB 或四边面 FBX（decimate.go）。
- 依据：https://developers.tripo3d.ai/en/docs/mesh-decimate（Mesh decimate）
- 核对日期：2026-11-12
- 没有真实样本的原因：样本只有一次合法的 v3.0 请求

#### `tripo-v3/texture` · response-shape · real

- 端点：`POST /models/texture`
- 行为：任务类型 texture_model（standard、无 PBR），输出 model_url 与 rendered_image_url。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-texture.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/convert-shape` · response-shape · real

- 端点：`POST /models/convert`
- 行为：格式转换：任务在 input 下回显请求与 original_model_id；GLTF/FBX 结果带 normalize_scale 与 normalize_translation，3MF 结果只有 model_url。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-convert.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/evidence-305-rig-convert.candidate.json`（2026-11-16，captured，app_revision `unknown`，catalog_revision `unknown`）
- 备注：3MF 一轮的样本 evidence-386-convert-3mf 只含轮询，按任务 id 指定来源，见 convert-3mf-package。

#### `tripo-v3/convert-request` · request-validation · constructed

- 端点：`POST /models/convert`
- 行为：format 限 GLTF、FBX、USDZ、OBJ、STL、3MF；texture_format、export_orientation、fbx_preset、texture_packaging 取值与类型按官方页校验，texture_packaging 只给 FBX。
- 依据：https://developers.tripo3d.ai/en/docs/models-convert（Models convert）
- 核对日期：2026-11-12
- 没有真实样本的原因：取值层面的拒绝没有真实请求过

#### `tripo-v3/convert-3mf-package` · artifact-shape · constructed

- 端点：`POST /models/convert`
- 行为：3MF 交付是带 Tripo 供应商命名空间（AIGC 标签）的 ZIP64 包结构（tripo3mf.go），不是通用 3MF 夹具。
- 依据：dev-test 真实运行观测（多视图模型转 3MF，35,952,441 字节）（#386，2026-11-19）
- 核对日期：2026-11-19
- 没有真实样本的原因：API 交换已入库（evidence-386-convert-3mf），交付文件本身只有私有媒体证据，没有入库字节

#### `tripo-v3/convert-url-delay` · other · constructed

- 端点：`POST /models/convert`
- 行为：以 URL 为输入的转换创建延迟应答（真实 47–48 秒，fake 默认 10 ms，可配置）。
- 依据：dev-test 真实运行观测（#305，2026-11-16）
- 核对日期：2026-11-16
- 没有真实样本的原因：wire 样本不记录应答耗时

#### `tripo-v3/pricing-observed` · pricing · real

- 端点：`POST /generation/text-to-model`、`POST /generation/image-to-model`、`POST /generation/image-to-splat`、`POST /generation/image-to-multiview`、`POST /generation/edit-multiview`、`POST /models/texture`、`POST /mesh/decimate`、`POST /mesh/smartsegment`、`POST /models/convert`、`POST /animations/rig`
- 行为：已真实观测的扣费回显：H 系列图生（纯几何）20、splat 30、多视图 10、编辑多视图 5、texture 10、decimate 35、smartsegment 85/55、convert 10（GLTF/FBX）、rig（v3.0）25。
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-image-to-model.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-text-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-image-to-splat.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-image-to-multiview.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-edit-multiview.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-texture.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-decimate.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-smartsegment-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/tripo-v3-smartsegment-model.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/tripo/v3/testdata/evidence-305-rig-convert.candidate.json`（2026-11-16，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `tripo-v3/pricing-table` · pricing · constructed

- 端点：`POST /generation/text-to-model`、`POST /generation/image-to-model`、`POST /generation/multiview-to-model`、`POST /generation/image-to-splat`、`POST /generation/image-to-multiview`、`POST /generation/edit-multiview`、`POST /models/texture`、`POST /mesh/decimate`、`POST /models/convert`
- 行为：未观测的参数组合（P 系列、PBR、纹理档位、quad 等）按官方价格表计价（billing.go）。
- 依据：https://developers.tripo3d.ai/en/pricing（Pricing）
- 核对日期：2026-11-24
- 没有真实样本的原因：只有样本里那几种参数组合被真实扣费过

#### `tripo-v3/rate-limit-concurrency` · rate-limit-concurrency · constructed

- 端点：`POST /generation/text-to-model`、`POST /generation/image-to-model`、`POST /generation/multiview-to-model`、`POST /generation/image-to-splat`、`POST /generation/image-to-multiview`、`POST /generation/edit-multiview`、`POST /animations/rig-check`、`POST /animations/rig`、`POST /animations/retarget`、`POST /mesh/segment`、`POST /mesh/complete`、`POST /mesh/smartsegment`、`POST /mesh/decimate`、`POST /models/texture`、`POST /models/convert`
- 行为：请求限流 429/code 1007 带 X-RateLimit-* 头；分类并发池（H 10 / P 5、图像 1、动画 10、模型处理 5、网格 10）满时创建答 429/code 2000 与 Retry-After（#468）。
- 依据：https://developers.tripo3d.ai/en/docs/rate-limits（Rate limits）
- 依据：https://developers.tripo3d.ai/en/docs/error-handling（Error handling）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `tripo-v3/fault-scenarios` · error-code-and-text · constructed

- 端点：`POST /generation/text-to-model`、`POST /generation/image-to-model`、`POST /generation/multiview-to-model`、`POST /generation/image-to-splat`、`POST /generation/image-to-multiview`、`POST /generation/edit-multiview`、`POST /animations/rig-check`、`POST /animations/rig`、`POST /animations/retarget`、`POST /mesh/segment`、`POST /mesh/complete`、`POST /mesh/smartsegment`、`POST /mesh/decimate`、`POST /models/texture`、`POST /models/convert`
- 行为：余额不足、Key 额度、5xx、超时、丢响应、受理后 502、畸形请求体 400/code 1003、未知来源任务 404/code 2001 等故障场景，复用真实错误信封。
- 依据：https://developers.tripo3d.ai/en/docs/error-handling（Error handling）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `tripo-v3/real-round-567` · other · gap

- 端点：`POST /generation/text-to-model`、`POST /generation/image-to-model`、`POST /generation/multiview-to-model`、`POST /generation/image-to-splat`、`POST /generation/image-to-multiview`、`POST /generation/edit-multiview`、`POST /animations/rig-check`、`POST /animations/rig`、`POST /animations/retarget`、`POST /mesh/segment`、`POST /mesh/complete`、`POST /mesh/smartsegment`、`POST /mesh/decimate`、`POST /models/texture`、`POST /models/convert`
- 行为：2026-11-24 的 Tripo 公开任务 API 全功能真实一轮正在回灌，本表仍按其之前的样本与构造登记。
- 计划：回灌完成后按功能更新对应条目的状态、样本与日期（P 系列、请求契约、计价等 constructed 条目优先）。
- Issue：#567

### `openai/v1`

#### `openai-v1/models-list` · response-shape · real

- 端点：`GET /models`
- 行为：模型列表结构，以及网关对缺少 Key 的 text/plain 401。
- 样本：`internal/testutil/fakeupstream/openai/v1/testdata/openai-v1.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `openai-v1/models-invalid-key` · error-code-and-text · constructed

- 端点：`GET /models`、`POST /chat/completions`
- 行为：invalid-key 场景答 OpenAI ErrorResponse 形态的 401。
- 依据：https://github.com/openai/openai-openapi/blob/master/openapi.yaml（ErrorResponse schema）
- 依据：https://developers.openai.com/api/docs/guides/error-codes（Error codes）
- 核对日期：2026-11-08
- 没有真实样本的原因：网关对错误 Key 的应答只录到 text/plain 形态

#### `openai-v1/chat-shape` · response-shape · real

- 端点：`POST /chat/completions`
- 行为：非流式、SSE 流式、tool_calls 与 reasoning_content 的结构；2026-11-23 的五次补全是所有文本补全的模板（usage 带 prompt_tokens_details.text_tokens，没有 system_fingerprint）。
- 样本：`internal/testutil/fakeupstream/openai/v1/testdata/openai-v1.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/openai/v1/testdata/openai-v1-chat-2026-11-23.candidate.json`（2026-11-23，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `openai-v1/chat-model-required` · error-code-and-text · real

- 端点：`POST /chat/completions`
- 行为：缺 model 的 400 model_required（网关信封没有官方的 param，带 path）与缺 Key 的 401。
- 样本：`internal/testutil/fakeupstream/openai/v1/testdata/openai-v1.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `openai-v1/chat-fault-scenarios` · error-code-and-text · constructed

- 端点：`POST /chat/completions`
- 行为：403（text/plain JSON 文本）、404 model-not-found、503 带 Retry-After、429 带 error.quota、messages 为空的 400。
- 依据：https://platform.openai.com/docs/api-reference/chat/create（Chat create）
- 依据：https://developers.openai.com/api/docs/guides/error-codes（Error codes）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `openai-v1/chat-stream-faults` · state-machine · constructed

- 端点：`POST /chat/completions`
- 行为：[[fake:stream-error]] 以错误帧结束已开始的流，[[fake:stream-cut]] 在 finish_reason 与 [DONE] 前干净断开，[[fake:stream-plain]] 对流式请求答非流式对象（#509）。
- 依据：https://platform.openai.com/docs/api-reference/chat/create（Chat create（stream））
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `openai-v1/chat-model-constraints` · request-validation · constructed

- 端点：`POST /chat/completions`
- 行为：gpt-astra / gpt-6.2-astra 按模型规则拒绝 tools、temperature 与 reasoning_effort none（400 invalid_request_error，#401）。
- 依据：网关 2026-11-20 的日志观测（tools、temperature 拒绝）
- 依据：https://developers.openai.com/api/docs/guides/latest-model.md（Latest model guide）
- 依据：https://developers.openai.com/api/docs/models/gpt-6.2-astra.md（gpt-6.2-astra）
- 核对日期：2026-11-20
- 没有真实样本的原因：拒绝只在网关日志里看到，没有入库样本；reasoning_effort none 按文档构造

#### `openai-v1/chat-vendor-forms` · response-shape · constructed

- 端点：`POST /chat/completions`
- 行为：#536 的厂商形态：qwen 系列在 finish_reason 之后用空 choices 的末块报 usage，[[fake:dsml]] 把工具调用写成 DSML 文本，[[fake:inline-image]] 在 delta.images / message.images 里画图。
- 依据：Model Studio OpenAI 兼容文档（流式 usage）
- 核对日期：2026-11-24
- 没有真实样本的原因：这些形态没有入库样本（deepseek 的 usage 在 finish_reason 块上是已观测形态），待真实一轮

#### `openai-v1/chat-image-refusals` · error-code-and-text · constructed

- 端点：`POST /chat/completions`
- 行为：图片输入按模型规则拒绝：qwen 宽或高不大于 32、fake-text-only 不收图、[[fake:image-refusal:KIND]] 的尺寸/大小/格式/张数/不支持。
- 依据：2026-11-23 观测到的 qwen 拒绝体（宽高下限）
- 依据：#253 的不收图文案
- 核对日期：2026-11-23
- 没有真实样本的原因：qwen 的拒绝体只在观测中出现，未入库；大小、格式、张数按厂商风格构造

#### `openai-v1/chat-quota-and-reasoning` · error-code-and-text · constructed

- 端点：`POST /chat/completions`
- 行为：[[fake:quota:CODE]] 按连接 error_mapping 声明的配额码答 429；deepseek 拒绝 low/high/max 之外的 reasoning_effort，[[fake:strict-reasoning]] 切换 reasoning_content 的严格读法（未证实）。
- 依据：https://api-docs.deepseek.com/guides/thinking_mode（Thinking mode）
- 依据：https://developers.openai.com/api/docs/guides/error-codes（Error codes）
- 核对日期：2026-11-24
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `openai-v1/responses-shape` · response-shape · constructed

- 端点：`POST /responses`
- 行为：Responses API（#401）：流式工具调用、文本、并行调用、xhigh 推理项、input_image、非流式响应、strict false 的工具 schema、重放推理项，以及按字节对齐的 400。
- 依据：https://platform.openai.com/docs/api-reference/responses/create（Responses create）
- 依据：2026-11-20 两次网关探测的手工脱敏摘录（internal/testutil/fakeupstream/openai/v1/testdata/responses-2026-11-20.sample.json）
- 核对日期：2026-11-20
- 没有真实样本的原因：探测结果不是 wirefixture candidate（手工脱敏，部分由非流式响应补全），不能按 real 计

#### `openai-v1/responses-unsampled-events` · state-machine · constructed

- 端点：`POST /responses`
- 行为：response.incomplete、response.failed、流内 error 事件、response.refusal.delta、推理摘要事件、超长流行与推理项后无后继项的 400。
- 依据：https://platform.openai.com/docs/api-reference/responses/create（Responses streaming events）
- 核对日期：2026-11-20
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `openai-v1/responses-quota-429` · error-code-and-text · real

- 端点：`POST /responses`
- 行为：网关日配额耗尽答 HTTP 429：error.code 与 error.type 为 quota_exceeded，另带嵌套 quota 对象；quota-exceeded 场景复用此信封。
- 样本：`internal/testutil/fakeupstream/openai/v1/testdata/openai-v1-quota-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）

### `meshy/openapi-v1`

#### `meshy-openapi-v1/balance` · response-shape · real

- 端点：`GET /balance`
- 行为：余额查询结构，值固定。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-text-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/errors-400-401-404` · error-code-and-text · real

- 端点：`GET /balance`、`GET /image-to-3d/{id}`、`POST /image-to-3d`、`POST /remesh`
- 行为：错误体为 {message}：缺 Key 401、非法 id 400、未知任务 404、非法创建请求 400。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-text-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/assets-route` · artifact-shape · constructed

- 端点：`GET /assets/{task_id}/{name}`
- 行为：产物 CDN 不属于 API；该路由提供任务 URL 指向的固定文件，glb、png、fbx、stl、3mf、obj、mtl、usdz 的 content type 按 2026-11-20 窗口的观测（obj 为 model/obj，usdz 为 application/octet-stream）。
- 依据：https://docs.meshy.ai/en/api/remesh（Remesh：model_urls）
- 核对日期：2026-10-29
- 没有真实样本的原因：产物主机不在 API 样本里，媒体字节按 recipe 替换；content type 来自观测

#### `meshy-openapi-v1/text-to-image-shape` · response-shape · real

- 端点：`POST /text-to-image`、`GET /text-to-image/{id}`
- 行为：text-to-image 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-text-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-processing-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/image-to-image-shape` · response-shape · real

- 端点：`POST /image-to-image`、`GET /image-to-image/{id}`
- 行为：image-to-image 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-image-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-image-to-image-2026-11-23.candidate.json`（2026-11-23，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/image-to-3d-shape` · response-shape · real

- 端点：`POST /image-to-3d`、`GET /image-to-3d/{id}`
- 行为：image-to-3d 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-image-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-processing-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）
- 备注：2026-10-29 的样本用最便宜的贴图选项；2026-11-20 窗口按 Catalog 默认（贴图与 PBR 开）重采，PBR 贴图为真实。

#### `meshy-openapi-v1/multi-image-to-3d-shape` · response-shape · real

- 端点：`POST /multi-image-to-3d`、`GET /multi-image-to-3d/{id}`
- 行为：multi-image-to-3d 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-multi-image-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-multi-image-to-3d-2026-11-23.candidate.json`（2026-11-23，captured，app_revision `unknown`，catalog_revision `unknown`）
- 备注：2026-11-23 的样本带 PBR 贴图，轮询回显 geometry_resolution 与 ultra_mode。

#### `meshy-openapi-v1/remesh-shape` · response-shape · real

- 端点：`POST /remesh`、`GET /remesh/{id}`
- 行为：remesh 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-remesh.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-processing-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/convert-shape` · response-shape · real

- 端点：`POST /convert`、`GET /convert/{id}`
- 行为：convert 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-convert.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-processing-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/retexture-shape` · response-shape · real

- 端点：`POST /retexture`、`GET /retexture/{id}`
- 行为：retexture 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-retexture.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-processing-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）
- 备注：2026-11-20 窗口按 Catalog 默认重采，PBR 贴图为真实。

#### `meshy-openapi-v1/rigging-shape` · response-shape · real

- 端点：`POST /rigging`、`GET /rigging/{id}`
- 行为：rigging 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-rigging.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-processing-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）
- 备注：成功的 rig 回 result.basic_animations 六个片段（2026-11-20）。

#### `meshy-openapi-v1/animations-shape` · response-shape · real

- 端点：`POST /animations`、`GET /animations/{id}`
- 行为：animations 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-animation.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-processing-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/uv-unwrap-shape` · response-shape · real

- 端点：`POST /uv-unwrap`、`GET /uv-unwrap/{id}`
- 行为：uv-unwrap 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-uv-unwrap.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/resize-shape` · response-shape · real

- 端点：`POST /resize`、`GET /resize/{id}`
- 行为：resize 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-resize.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-processing-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/print-analyze-shape` · response-shape · real

- 端点：`POST /print/analyze`、`GET /print/analyze/{id}`
- 行为：print/analyze 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-print-analyze.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-processing-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）
- 备注：报告的 printability._version 键不能进样本（策略键须以字母或数字开头），fake 补回。

#### `meshy-openapi-v1/print-multi-color-shape` · response-shape · real

- 端点：`POST /print/multi-color`、`GET /print/multi-color/{id}`
- 行为：print/multi-color 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-print-multi-color.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/text-to-motion-shape` · response-shape · real

- 端点：`POST /text-to-motion`、`GET /text-to-motion/{id}`
- 行为：text-to-motion 的创建信封（202，{result}）与 IN_PROGRESS → SUCCEEDED 轮询体结构；引擎以最新样本为模板。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-text-to-motion.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/delete` · response-shape · real

- 端点：`DELETE /text-to-image/{id}`、`DELETE /image-to-image/{id}`、`DELETE /image-to-3d/{id}`、`DELETE /multi-image-to-3d/{id}`、`DELETE /remesh/{id}`、`DELETE /convert/{id}`、`DELETE /retexture/{id}`、`DELETE /rigging/{id}`、`DELETE /animations/{id}`、`DELETE /uv-unwrap/{id}`、`DELETE /resize/{id}`、`DELETE /print/analyze/{id}`、`DELETE /print/multi-color/{id}`、`DELETE /text-to-motion/{id}`
- 行为：删除任务答 HTTP 200，没有 JSON 体；14 个任务族都有真实删除样本。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-delete.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-text-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-image-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-text-to-motion.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/delete-running-409` · error-code-and-text · constructed

- 端点：`DELETE /text-to-image/{id}`、`DELETE /image-to-image/{id}`、`DELETE /image-to-3d/{id}`、`DELETE /multi-image-to-3d/{id}`、`DELETE /remesh/{id}`、`DELETE /convert/{id}`、`DELETE /retexture/{id}`、`DELETE /rigging/{id}`、`DELETE /animations/{id}`、`DELETE /uv-unwrap/{id}`、`DELETE /resize/{id}`、`DELETE /print/analyze/{id}`、`DELETE /print/multi-color/{id}`、`DELETE /text-to-motion/{id}`
- 行为：删除运行中任务的 409。
- 依据：https://docs.meshy.ai/en/api/errors（Errors）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `meshy-openapi-v1/poll-pending-canceled` · state-machine · constructed

- 端点：`GET /text-to-image/{id}`、`GET /image-to-image/{id}`、`GET /image-to-3d/{id}`、`GET /multi-image-to-3d/{id}`、`GET /remesh/{id}`、`GET /convert/{id}`、`GET /retexture/{id}`、`GET /rigging/{id}`、`GET /animations/{id}`、`GET /uv-unwrap/{id}`、`GET /resize/{id}`、`GET /print/analyze/{id}`、`GET /print/multi-color/{id}`、`GET /text-to-motion/{id}`
- 行为：PENDING（无 started/finished 时间戳，排队时带 preceding_tasks）与 task-canceled 场景的 CANCELED 轮询体。
- 依据：https://docs.meshy.ai/en/api/image-to-3d（各端点的任务对象字段说明）
- 核对日期：2026-11-08
- 没有真实样本的原因：真实轮询只观测到 IN_PROGRESS 与 SUCCEEDED（rigging 另有 FAILED）

#### `meshy-openapi-v1/rigging-failed-oversize` · state-machine · real

- 端点：`POST /rigging`、`GET /rigging/{id}`
- 行为：超过 32 万面的输入在提交时被受理（202，文档写 400），任务运行到约 10% 后以 FAILED、task_error.type=invalid_input 结束。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-rigging.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/poll-failed-other-kinds` · state-machine · constructed

- 端点：`GET /text-to-image/{id}`、`GET /image-to-image/{id}`、`GET /image-to-3d/{id}`、`GET /multi-image-to-3d/{id}`、`GET /remesh/{id}`、`GET /convert/{id}`、`GET /retexture/{id}`、`GET /rigging/{id}`、`GET /animations/{id}`、`GET /uv-unwrap/{id}`、`GET /resize/{id}`、`GET /print/analyze/{id}`、`GET /print/multi-color/{id}`、`GET /text-to-motion/{id}`
- 行为：rigging 之外各族的 FAILED 轮询体（task-failed 场景），由进行中结构加终态时间戳与 task_error 构造。
- 依据：https://docs.meshy.ai/en/api/errors（Errors）
- 核对日期：2026-11-08
- 没有真实样本的原因：只有 rigging 观测到过 FAILED

#### `meshy-openapi-v1/image-models-request` · request-validation · constructed

- 端点：`POST /text-to-image`、`POST /image-to-image`
- 行为：ai_model 必填且取十个之一，aspect_ratio 七值且 3:2、2:3 只给 GPT Image 模型，generate_multi_view 与 aspect_ratio 冲突；图生图接受 1–5 张 reference_image_urls 或成功任务的 input_task_id（任务优先）（#542）。
- 依据：https://docs.meshy.ai/en/api/text-to-image（Text to image）
- 依据：https://docs.meshy.ai/en/api/image-to-image（Image to image）
- 核对日期：2026-11-24
- 没有真实样本的原因：拒绝从未向真实 API 请求过，拒绝文案为构造

#### `meshy-openapi-v1/image-to-3d-request` · request-validation · constructed

- 端点：`POST /image-to-3d`、`POST /multi-image-to-3d`
- 行为：geometry_resolution 2k/4k 与模型的组合、texture_model、4k/8k 贴图与 meshy-6-lite/meshy-n1（定价表 NS 格读作 400）、meshy-n1 不配 PBR；多图 1–4 张、不收 4k、texture_image_urls 的互斥（core.ImageTo3DRequestError，#542）。
- 依据：https://docs.meshy.ai/en/api/image-to-3d（Image to 3D）
- 依据：https://docs.meshy.ai/en/api/multi-image-to-3d（Multi-image to 3D）
- 依据：https://docs.meshy.ai/en/api/pricing（Pricing）
- 核对日期：2026-11-24
- 没有真实样本的原因：组合拒绝没有真实请求过

#### `meshy-openapi-v1/image-to-3d-rich-success` · response-shape · constructed

- 端点：`GET /image-to-3d/{id}`、`GET /multi-image-to-3d/{id}`
- 行为：带多视图、PBR 与 8K 贴图的成功体按文档的贴图字段构造。
- 依据：https://docs.meshy.ai/en/api/image-to-3d（Image to 3D：texture_urls）
- 核对日期：2026-11-08
- 没有真实样本的原因：这些组合的成功体还没有真实运行替换

#### `meshy-openapi-v1/retexture-request` · request-validation · constructed

- 端点：`POST /retexture`
- 行为：text_style_prompt、image_style_url、multiview_image_urls 三选一，multiview 互斥且须显式 meshy-7 或 meshy-n1，风格文本不超过 800 字符；页面 ai_model 枚举外的值（如 meshy-7.1）答 400（core.RetextureRequestError，#542）。
- 依据：https://docs.meshy.ai/en/api/retexture（Retexture）
- 核对日期：2026-11-24
- 没有真实样本的原因：页面没写枚举外取值的结果，待零费用探测定稿

#### `meshy-openapi-v1/rigging-request` · request-validation · constructed

- 端点：`POST /rigging`
- 行为：skeleton_template（九种）、include_fingers（不与 texture_image_url 同用）、height_meters（非负）按官方页校验（riggingRequestError）；rig-non-humanoid 场景答文档的 422。
- 依据：https://docs.meshy.ai/en/api/rigging（Rigging）
- 依据：https://docs.meshy.ai/en/api/rigging-and-animation（Rigging and animation）
- 核对日期：2026-11-12
- 没有真实样本的原因：取值未真实请求过

#### `meshy-openapi-v1/rigging-quad-fingers` · error-code-and-text · constructed

- 端点：`POST /rigging`
- 行为：四边面拓扑来源任务带 include_fingers 时答 HTTP 400，正文只有 message（riggingSourceError）。
- 依据：dev-test 真实调用观测（#294，2026-11-12）
- 核对日期：2026-11-12
- 没有真实样本的原因：真实调用观测到了，但原始请求与响应没有作为脱敏样本入库

#### `meshy-openapi-v1/motion-request` · request-validation · constructed

- 端点：`POST /animations`、`POST /text-to-motion`
- 行为：动画三种动作来源恰好一个（action_ids 至多 10 个、不重复）；text-to-motion 的 prompt 至多 400 字符、mode 为 prime 或 swift、duration 2–10 秒半秒步进（#542）。
- 依据：https://docs.meshy.ai/en/api/animation（Animation）
- 依据：https://docs.meshy.ai/en/api/text-to-motion（Text to motion）
- 核对日期：2026-11-24
- 没有真实样本的原因：拒绝从未向真实 API 请求过

#### `meshy-openapi-v1/resize-request` · request-validation · constructed

- 端点：`POST /resize`
- 行为：resize_height、resize_longest_side、auto_size 恰好一个，否则答文档的 400（resizeRequestError）。
- 依据：https://docs.meshy.ai/en/api/resize（Resize）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `meshy-openapi-v1/pricing-observed` · pricing · real

- 端点：`GET /text-to-image/{id}`、`GET /image-to-3d/{id}`、`GET /multi-image-to-3d/{id}`、`GET /remesh/{id}`、`GET /convert/{id}`、`GET /retexture/{id}`、`GET /rigging/{id}`、`GET /animations/{id}`、`GET /resize/{id}`
- 行为：2026-11-20 至 23 的真实价格作为断言：nano-banana-pro 9、image-to-3d 4k 几何 2k 贴图 35、多图 2k 几何 2k 贴图 35、retexture 8k 15、remesh 5、rigging 5、动画 3、resize 与 convert 1；价格从第一次轮询起就带上。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-processing-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v1/testdata/meshy-openapi-v1-multi-image-to-3d-2026-11-23.candidate.json`（2026-11-23，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v1/pricing-table` · pricing · constructed

- 端点：`GET /text-to-image/{id}`、`GET /image-to-image/{id}`、`GET /image-to-3d/{id}`、`GET /multi-image-to-3d/{id}`、`GET /remesh/{id}`、`GET /convert/{id}`、`GET /retexture/{id}`、`GET /rigging/{id}`、`GET /animations/{id}`、`GET /uv-unwrap/{id}`、`GET /resize/{id}`、`GET /print/analyze/{id}`、`GET /print/multi-color/{id}`、`GET /text-to-motion/{id}`
- 行为：其余参数组合的 consumed_credits 按官方 Pricing 页随请求计算（core.Credits），失败任务为 0（#542）。
- 依据：https://docs.meshy.ai/en/api/pricing（Pricing）
- 核对日期：2026-11-24
- 没有真实样本的原因：只有上一条所列组合被真实扣费过

#### `meshy-openapi-v1/fault-scenarios` · error-code-and-text · constructed

- 端点：`POST /text-to-image`、`POST /image-to-image`、`POST /image-to-3d`、`POST /multi-image-to-3d`、`POST /remesh`、`POST /convert`、`POST /retexture`、`POST /rigging`、`POST /animations`、`POST /uv-unwrap`、`POST /resize`、`POST /print/analyze`、`POST /print/multi-color`、`POST /text-to-motion`
- 行为：403 forbidden（{message}）、余额不足（非 Key 额度）、429、5xx、超时与丢响应；402 API key credit limit reached 沿用 #73 的观测。
- 依据：https://docs.meshy.ai/en/api/errors（Errors）
- 依据：#73 的 402 Key 额度观测（未入库）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `meshy-openapi-v1/rate-limit` · rate-limit-concurrency · constructed

- 端点：`POST /text-to-image`、`POST /image-to-image`、`POST /image-to-3d`、`POST /multi-image-to-3d`、`POST /remesh`、`POST /convert`、`POST /retexture`、`POST /rigging`、`POST /animations`、`POST /uv-unwrap`、`POST /resize`、`POST /print/analyze`、`POST /print/multi-color`、`POST /text-to-motion`
- 行为：rate-limit 场景答 429 与 Retry-After；非终态轮询带 Retry-After: 5。
- 依据：https://docs.meshy.ai/en/api/rate-limits（Rate limits）
- 核对日期：2026-11-08
- 没有真实样本的原因：429 没有在官方 API 上触发；Retry-After: 5 在轮询中已观测，但未单独断言为样本

#### `meshy-openapi-v1/partial-formats` · other · constructed

- 端点：`GET /retexture/{id}`
- 行为：partial-formats 场景：完成的 retexture 不给 STL 与 3MF（D 类补齐恢复的起点）。
- 依据：Catalog：retexture 的 STL、3MF 为可选产物
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 从未被观测到少给格式；这是构造的故障

#### `meshy-openapi-v1/webhook-sender` · other · constructed

- 端点：`POST /text-to-image`、`POST /image-to-image`、`POST /image-to-3d`、`POST /multi-image-to-3d`、`POST /remesh`、`POST /convert`、`POST /retexture`、`POST /rigging`、`POST /animations`、`POST /uv-unwrap`、`POST /resize`、`POST /print/analyze`、`POST /print/multi-color`、`POST /text-to-motion`
- 行为：按账号登记的 webhook 收到全部 API 任务的状态更新，投递体为该任务 GET 端点的任务对象（webhook.go，#542）。
- 依据：https://docs.meshy.ai/en/api/webhooks（Webhooks）
- 核对日期：2026-11-24
- 没有真实样本的原因：真实投递需要公网 https 地址并会影响账号下其他应用，未做真实验证

#### `meshy-openapi-v1/t25-image-to-3d` · response-shape · gap

- 端点：`POST /image-to-3d`、`GET /image-to-3d/{id}`
- 行为：meshy-t2.5 图生 3D 的创建与轮询体按 meshy-7.1 样本的结构给出。
- 计划：#569 合并后，用 2026-11-24 真实一轮的 meshy-openapi-v1-real-round-2026-11-24 样本把本条改为 real 并补样本路径与日期。
- Issue：#568、#569
- 纠正 wire coverage：单元 `meshy.v2|image-to-3d|POST /openapi/v1/image-to-3d` 下的 `meshy/meshy-t2.5/image-to-3d`。矩阵只比较请求体的 model 字段，Meshy 用 ai_model，所以 meshy-t2.5 继承了 meshy-7.1 样本的 real；在 #569 之前没有任何入库样本请求过 meshy-t2.5。

#### `meshy-openapi-v1/real-round-568` · response-shape · gap

- 端点：`GET /image-to-3d/{id}`、`GET /multi-image-to-3d/{id}`、`GET /retexture/{id}`、`POST /retexture`
- 行为：2026-11-24 的 Meshy 15 功能真实一轮（#568）发现分辨率键的回显应按请求（2k 不回显、4k/8k 回显 texture_resolution 与 hd_texture），并确认了 15 个功能的价格；主线 fake 仍按模板回显。
- 计划：#569 合并后把分辨率回显、t2.5 与多图、retexture 的样本和价格登记为 real，并引用新样本路径。
- Issue：#568、#569

### `meshy/openapi-v2`

#### `meshy-openapi-v2/text-to-3d-shape` · response-shape · real

- 端点：`POST /text-to-3d`、`GET /text-to-3d/{id}`
- 行为：preview 与 refine 共用一个路由，任务类型随 mode；IN_PROGRESS 与 SUCCEEDED 轮询体两种类型均为真实，2026-11-20 的对象另有 input、expires_at、分辨率回显与 output。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v2/testdata/meshy-openapi-v2.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v2/testdata/meshy-openapi-v2-text-to-3d-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v2/testdata/meshy-openapi-v2-text-to-3d-2026-11-22.candidate.json`（2026-11-22，captured，app_revision `unknown`，catalog_revision `2026-11-22.456-460.1`）
- 样本：`internal/providers/meshy/testdata/meshy-text-to-3d.candidate.json`（2026-10-26，captured，app_revision `unknown`，catalog_revision `2026-10-26.issue363-validator-and-issue358-input-reasons.1`）
- 样本：`internal/providers/meshy/testdata/meshy-text-to-3d-2026-11-22.candidate.json`（2026-11-22，captured，app_revision `unknown`，catalog_revision `2026-11-22.456-460.1`）

#### `meshy-openapi-v2/errors` · error-code-and-text · real

- 端点：`POST /text-to-3d`、`GET /text-to-3d/{id}`
- 行为：缺 Key 401、未知任务 404 与非法创建 400，错误体为 {message}。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v2/testdata/meshy-openapi-v2.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v2/delete` · response-shape · real

- 端点：`DELETE /text-to-3d/{id}`
- 行为：删除任务的应答。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v2/testdata/meshy-openapi-v2.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `meshy-openapi-v2/poll-pending-failed-canceled` · state-machine · constructed

- 端点：`GET /text-to-3d/{id}`
- 行为：PENDING（started_at/finished_at 为 0、排队时带 preceding_tasks）、FAILED（task_error 与 expires_at null）与 CANCELED 的轮询体。
- 依据：https://docs.meshy.ai/en/api/text-to-3d（Text to 3D：任务对象）
- 核对日期：2026-11-08
- 没有真实样本的原因：真实轮询只观测到 IN_PROGRESS 与 SUCCEEDED

#### `meshy-openapi-v2/text-to-3d-request` · request-validation · constructed

- 端点：`POST /text-to-3d`
- 行为：preview 需要不超过 800 字符的 prompt，refine 需要指向本账号已成功 preview 的 preview_task_id（否则 400），2k/4k 几何与模型的组合，模型、mode、格式枚举（#542）；refine 的任意已知模型名都接受。
- 依据：https://docs.meshy.ai/en/api/text-to-3d（Text to 3D）
- 核对日期：2026-11-24
- 没有真实样本的原因：拒绝文案为构造，参数层面的拒绝没有真实请求过
- 备注：refine 带 meshy-7.1 被接受是 2026-11-20 的真实观测。

#### `meshy-openapi-v2/pricing-observed` · pricing · real

- 端点：`GET /text-to-3d/{id}`
- 行为：2026-11-20 与 22 的真实价格作为断言：7.1 preview 4k 几何 25、refine 2k 10、8k 15。
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v2/testdata/meshy-openapi-v2-text-to-3d-2026-11-20.candidate.json`（2026-11-20，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/meshy/openapi-v2/testdata/meshy-openapi-v2-text-to-3d-2026-11-22.candidate.json`（2026-11-22，captured，app_revision `unknown`，catalog_revision `2026-11-22.456-460.1`）

#### `meshy-openapi-v2/pricing-table` · pricing · constructed

- 端点：`GET /text-to-3d/{id}`
- 行为：其余组合按 Pricing 页计价：preview 20 或 5，2k/4k 几何加 5；refine 10，8k 为 15。
- 依据：https://docs.meshy.ai/en/api/pricing（Pricing）
- 核对日期：2026-11-24
- 没有真实样本的原因：只有上一条的组合被真实扣费过

#### `meshy-openapi-v2/assets-route` · artifact-shape · constructed

- 端点：`GET /assets/{task_id}/{name}`
- 行为：提供 model_urls 与 thumbnail_url 指向的固定文件；六种 Catalog 格式的 content type 沿用 v1 对齐的 CDN 观测。
- 依据：https://docs.meshy.ai/en/api/text-to-3d（Text to 3D：model_urls）
- 核对日期：2026-10-29
- 没有真实样本的原因：v2 对齐只请求了 glb；产物主机不在 API 样本里

#### `meshy-openapi-v2/t25-text-to-3d` · response-shape · gap

- 端点：`POST /text-to-3d`、`GET /text-to-3d/{id}`
- 行为：meshy-t2.5 文生 3D 的 preview 与 refine 按 meshy-7.1 样本的结构给出。
- 计划：#569 合并后，用 meshy-openapi-v2-real-round-2026-11-24 样本把本条改为 real 并补样本路径与日期。
- Issue：#568、#569
- 纠正 wire coverage：单元 `meshy.v2|text-to-3d|POST /openapi/v2/text-to-3d` 下的 `meshy/meshy-t2.5/text-to-3d`。该单元的 real 来自 meshy-7.1 的样本；meshy-t2.5 共用端点，在 #569 之前没有入库样本请求过它。

#### `meshy-openapi-v2/real-round-568` · response-shape · gap

- 端点：`POST /text-to-3d`、`GET /text-to-3d/{id}`
- 行为：2026-11-24 真实一轮（#568）的 t2.5 preview/refine 结构、价格与分辨率回显规则尚未合入主线 fake。
- 计划：#569 合并后登记为 real 并引用 meshy-openapi-v2-real-round-2026-11-24。
- Issue：#568、#569

### `hyper3d/api-v2`

#### `hyper3d-api-v2/check-balance` · response-shape · real

- 端点：`GET /check_balance`
- 行为：余额查询结构与缺 Key 的 401 UNAUTHORIZED。
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-text-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `hyper3d-api-v2/check-balance-usage` · response-shape · constructed

- 端点：`GET /check_balance`
- 行为：usage_page（从 1 起，每页至多 20 条，新的在前）返回 usage 列表；非法页码答 400 信封（#537）。
- 依据：https://docs.hyper3d.ai/en/api-specification/check-balance（Check balance）
- 核对日期：2026-11-24
- 没有真实样本的原因：带 usage_page 的真实请求待真实一轮

#### `hyper3d-api-v2/status-states` · state-machine · real

- 端点：`POST /status`
- 行为：子作业 Waiting → Generating → Done / Failed；400 校验信封；Waiting 的 queue_length 只出现在下一个排队的作业上（值 0）。
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-bang.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-texture-failed.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-text-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/providers/hyper3d/testdata/rodin-text-to-3d.candidate.json`（2026-10-26，captured，app_revision `unknown`，catalog_revision `2026-10-26.issue363-validator-and-issue358-input-reasons.1`）

#### `hyper3d-api-v2/status-queue-countdown` · state-machine · constructed

- 端点：`POST /status`
- 行为：排队期间第一个作业的 queue_length 逐次递减到 0。
- 依据：https://docs.hyper3d.ai/en/api-specification/check-status（Check status）
- 核对日期：2026-11-24
- 没有真实样本的原因：真实排队中的取值未观测到

#### `hyper3d-api-v2/no-such-task` · error-code-and-text · constructed

- 端点：`POST /status`、`POST /download`
- 行为：未知 subscription_key 或 task_uuid 答 HTTP 201 {"error":"NO_SUCH_TASK"}。
- 依据：真实观测（charge_evidence_test.go）（2026-10-21）
- 核对日期：2026-10-21
- 没有真实样本的原因：真实调用观测到了，但原始请求与响应没有作为脱敏样本入库

#### `hyper3d-api-v2/download` · response-shape · real

- 端点：`POST /download`
- 行为：下载列表（name、url）；400 校验信封；作业未完成时为空列表。
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-text-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-bang.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-texture.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/providers/hyper3d/testdata/rodin-image-to-3d.candidate.json`（2026-10-28，captured，app_revision `b29265603b4fbb7efc25ba967a537b161153e023`，catalog_revision `2026-10-29.issue92-93-reference-key-quota.2`）

#### `hyper3d-api-v2/files-route` · artifact-shape · constructed

- 端点：`GET /files/{task_uuid}/{name}`
- 行为：文件主机不属于 API；提供下载列表指向的固定夹具（content type 按观测），Gen-2.5 的 PBR 包随请求（三角面数、贴图边长，#303）。
- 依据：https://docs.hyper3d.ai/en/api-specification/download-results（Download results）
- 依据：https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5（Gen-2.5）
- 核对日期：2026-11-16
- 没有真实样本的原因：文件主机不在 API 样本里；PBR 包的面数与贴图边长按页面表与少数观测构造
- 备注：真实文件的体量与形状不复现：200 万面的展示请求交付的是 121,032 三角面上限的夹具，贴图是平涂。

#### `hyper3d-api-v2/rodin-shape` · response-shape · real

- 端点：`POST /rodin`
- 行为：multipart 提交，受理与拒绝都是 HTTP 201；文生、图生与 image_label 的回执结构。
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-text-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-image-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/evidence-376-rodin-labeled.candidate.json`（2026-11-19，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/providers/hyper3d/testdata/rodin-text-to-3d.candidate.json`（2026-10-26，captured，app_revision `unknown`，catalog_revision `2026-10-26.issue363-validator-and-issue358-input-reasons.1`）
- 样本：`internal/providers/hyper3d/testdata/rodin-image-to-3d.candidate.json`（2026-10-28，captured，app_revision `b29265603b4fbb7efc25ba967a537b161153e023`，catalog_revision `2026-10-29.issue92-93-reference-key-quota.2`）
- 备注：internal/providers/hyper3d 的两个样本里，图生的提交请求按 recipe 替换参考图（wire coverage 记为 derived）。

#### `hyper3d-api-v2/rodin-errors` · error-code-and-text · real

- 端点：`POST /rodin`
- 行为：201 加 error 的拒绝：INVALID_REQUEST、IMAGE_LABEL_LENGTH_TOO_LONG；缺 Key 401 UNAUTHORIZED。
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-text-to-3d.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/evidence-376-rodin-label-overflow.candidate.json`（2026-11-19，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `hyper3d-api-v2/submit-error-codes` · error-code-and-text · constructed

- 端点：`POST /rodin`、`POST /bang`、`POST /rodin_texture_only`
- 行为：API_NO_ACTIVE_SUBSCRIPTION（2026-11-12 观测到被拒，正文未保留）与文档列出的 API_PARALLELISM_LIMIT_REACHED、PERMISSION_DENIED、API_SUBSCRIPTION_PLAN_TOO_LOW、API_OBJECT_NOT_FOUND_ON_IMAGE、IMAGE_CONTENT_VIOLATION、API_UNKNOWN、NO_SUCH_TASK。
- 依据：https://docs.hyper3d.ai/en/api-specification/errors（Errors）
- 依据：#294 的真实提交被拒观测（2026-11-12）
- 核对日期：2026-11-08
- 没有真实样本的原因：这些码没有入库样本；API_NO_ACTIVE_SUBSCRIPTION 的真实正文未保留

#### `hyper3d-api-v2/rodin-gen25-values` · request-validation · constructed

- 端点：`POST /rodin`
- 行为：Gen-2.5 表单的取值规则（seed、mesh_mode、quality、texture_mode、quality_override 及 Quad 与档位上限、TAPose、quad_normal、image_label、参考图 1–5 张），越界答 INVALID_REQUEST。
- 依据：https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5（Gen-2.5）
- 核对日期：2026-11-16
- 没有真实样本的原因：拒绝值没有真实请求过；真实服务也可能答 HTTP 400 校验信封

#### `hyper3d-api-v2/rodin-pricing` · pricing · constructed

- 端点：`POST /rodin`
- 行为：回执的 consumed 按官方页价格表计（generationCharge）；Extreme-High 档多列额外网格。
- 依据：https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5（Gen-2.5：价格表）
- 依据：游戏角色真实付费运行（扣 1 credit）（#294 后续，2026-11-13）
- 核对日期：2026-11-16
- 没有真实样本的原因：2026-11-13 的运行没有录原始请求与响应

#### `hyper3d-api-v2/bang-shape` · response-shape · real

- 端点：`POST /bang`
- 行为：Bang 的提交回执与子作业流转（Generating > Waiting > Generating > Done）。
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-bang.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `hyper3d-api-v2/bang-input-modes` · request-validation · constructed

- 端点：`POST /bang`
- 行为：asset_id 与 model 恰好一个；asset_id 带 model/image/prompt 答 HTTP 400；model 缺 image 且 material 不是 None 答 INVALID_REQUEST；asset_id 指向 Bang 或 Texture 结果答 ASSET_NOT_AVAILABLE（#537）。
- 依据：https://docs.hyper3d.ai/en/api-specification/bang（Bang）
- 核对日期：2026-11-24
- 没有真实样本的原因：冲突与缺图的拒绝状态没有真实请求过；来源未完成的 ASSET_NOT_AVAILABLE 为已观测

#### `hyper3d-api-v2/texture-shape` · response-shape · real

- 端点：`POST /rodin_texture_only`
- 行为：Texture 的提交回执与完成后的下载列表。
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-texture.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `hyper3d-api-v2/texture-failed-tiny-model` · state-machine · real

- 端点：`POST /rodin_texture_only`
- 行为：模型是 400 字节单三角形 GLB 时官方受理（201），第一次轮询全部子作业 Failed，顶层 message 为 Invalid input.，下载为空列表。
- 样本：`internal/testutil/fakeupstream/hyper3d/api-v2/testdata/hyper3d-api-v2-texture-failed.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `hyper3d-api-v2/texture-request` · request-validation · constructed

- 端点：`POST /rodin_texture_only`
- 行为：参考图字段 images 与 image 都收，合计 1–5 张；image_label 与超过 1024 字符的 prompt 按 Gen-2.5 规则拒绝（#537）。
- 依据：https://docs.hyper3d.ai/en/api-specification/generate-texture（Generate texture）
- 核对日期：2026-11-24
- 没有真实样本的原因：单数 image 是否仍被容忍待真实一轮

#### `hyper3d-api-v2/texture-pricing` · pricing · constructed

- 端点：`POST /rodin_texture_only`
- 行为：extreme-high 回执 consumed 为 2，其余模式为基础 0.5。
- 依据：https://docs.hyper3d.ai/en/api-specification/generate-texture（Generate texture）
- 依据：#187 的真实扣费观测（Basic 与 High 分辨率各扣 2）
- 核对日期：2026-11-16
- 没有真实样本的原因：扣费只在观测中出现，没有入库样本

#### `hyper3d-api-v2/rate-limit-faults` · rate-limit-concurrency · constructed

- 端点：`POST /rodin`、`POST /bang`、`POST /rodin_texture_only`
- 行为：rate-limit、5xx、超时与丢响应场景；并行上限 API_PARALLELISM_LIMIT_REACHED。
- 依据：https://docs.hyper3d.ai/en/api-specification/errors（Errors）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `hyper3d-api-v2/real-round-568` · artifact-shape · gap

- 端点：`POST /rodin_texture_only`、`GET /files/{task_uuid}/{name}`
- 行为：2026-11-24 的 Rodin 真实一轮（#568）显示 Texture（extreme-high、High、hd_texture）交付 base_high_pbr.glb、贴图边长 12288；主线 fake 的 Texture 包仍是 Basic 命名的夹具。
- 计划：#569 合并后按分辨率命名 Texture 包并按表单给贴图边长，引用 evidence-568-processing-high 样本；Bang 子作业数等未回灌项另记。
- Issue：#568、#569

### `openai/images-v1`

#### `openai-images-v1/generations-shape` · response-shape · real

- 端点：`POST /images/generations`
- 行为：JSON 请求，成功体为内嵌 base64 的 data[0].b64_json 加 usage（1024x1024 PNG）。
- 样本：`internal/testutil/fakeupstream/openai/images-v1/testdata/openai-images-v1-text-to-image.candidate.json`（2026-11-09，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `openai-images-v1/generations-errors` · error-code-and-text · real

- 端点：`POST /images/generations`
- 行为：缺 prompt 的 400 empty_string 与 text/plain 的 401。
- 样本：`internal/testutil/fakeupstream/openai/images-v1/testdata/openai-images-v1-text-to-image.candidate.json`（2026-11-09，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `openai-images-v1/size-format-background` · artifact-shape · constructed

- 端点：`POST /images/generations`、`POST /images/edits`
- 行为：按 size（auto 为 1024x1024）与 output_format 出图并回显实际尺寸、格式与 background，transparent 给带 alpha 的 PNG（#543）。
- 依据：https://platform.openai.com/docs/api-reference/images/（Images create）
- 核对日期：2026-11-24
- 没有真实样本的原因：已提交样本只请求过 1024x1024 png

#### `openai-images-v1/parameter-contract` · request-validation · constructed

- 端点：`POST /images/generations`、`POST /images/edits`
- 行为：size（16 的倍数、最长边 3840、比例 3:1、65.5 万至 829.4 万像素）、quality、output_format、background、moderation、n（1–10，不截断）、output_compression、prompt 至多 32000 字符，非法值答沿用真实信封的 400（#543）。
- 依据：https://platform.openai.com/docs/api-reference/images/（Images create）
- 核对日期：2026-11-24
- 没有真实样本的原因：这些拒绝没有真实请求过

#### `openai-images-v1/fault-scenarios` · error-code-and-text · constructed

- 端点：`POST /images/generations`、`POST /images/edits`
- 行为：403、429、503 故障与内容审核拒绝。
- 依据：https://developers.openai.com/api/docs/guides/error-codes（Error codes）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `openai-images-v1/quality-max` · request-validation · gap

- 端点：`POST /images/generations`、`POST /images/edits`
- 行为：quality=max 仍被接受：官方页称 xhigh、max 只给 2.5 系列，网关 2026-11-09 对 2.6 观测为 200，两者冲突。
- 计划：在 2D 真实一轮里对账一次，按结果决定接受或拒绝。
- Issue：#540

#### `openai-images-v1/edits-shape` · response-shape · real

- 端点：`POST /images/edits`
- 行为：multipart 编辑（参考图在 image 分片，Plinth 写 image[]，fake 两种都收），成功体与生成相同；缺参数 400 missing_required_parameter。
- 样本：`internal/testutil/fakeupstream/openai/images-v1/testdata/openai-images-v1-image-to-image.candidate.json`（2026-11-09，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `openai-images-v1/edits-mask-and-count` · request-validation · constructed

- 端点：`POST /images/edits`
- 行为：至多 16 张图（array_above_max_length）；mask 须为带 alpha、与首图同尺寸的 PNG，否则 400 invalid_image；带 mask 只改透明像素并保持尺寸；model 可省略（默认 gpt-image-2.6-sunburst）。
- 依据：https://platform.openai.com/docs/api-reference/images/（Create image edit）
- 核对日期：2026-11-24
- 没有真实样本的原因：官方编辑参考没有 mask 规则，网关的措辞未记录

### `gemini/images-v1`

#### `gemini-images-v1/chat-image-shape` · response-shape · real

- 端点：`POST /chat/completions`
- 行为：modalities [text,image] 的补全，图片是 choices[0].message.content[].image_url.url 的 data URI；文生图 JPEG、图生图 PNG，图生图多出 service_tier 与图片 token。
- 样本：`internal/testutil/fakeupstream/gemini/images-v1/testdata/gemini-images-v1-text-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/gemini/images-v1/testdata/gemini-images-v1-image-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `gemini-images-v1/errors` · error-code-and-text · real

- 端点：`POST /chat/completions`
- 行为：缺 Key 401 与 400 invalid_text_request。
- 样本：`internal/testutil/fakeupstream/gemini/images-v1/testdata/gemini-images-v1-text-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/gemini/images-v1/testdata/gemini-images-v1-image-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `gemini-images-v1/size-table` · artifact-shape · constructed

- 端点：`POST /chat/completions`
- 行为：按 eca_image_config 的十种 aspect_ratio 与 1K/2K/4K 出图（默认 1:1 的 1K），像素表为构造值（pictureSizes）。
- 依据：https://doc.model-store.ai/ai-gateway/model/api-detail?code=api-chat-completions1（Chat completions（网关））
- 核对日期：2026-11-24
- 没有真实样本的原因：样本只请求过 1K 1:1

#### `gemini-images-v1/out-of-set-400` · request-validation · constructed

- 端点：`POST /chat/completions`
- 行为：比例或尺寸不在集合内、小写 1k、超过 14 张参考图答 400 invalid_argument。
- 依据：https://ai.google.dev/gemini-api/docs/api-errors（API errors）
- 核对日期：2026-11-24
- 没有真实样本的原因：真实网关是拒绝还是静默忽略没有记录；免费探针待做

#### `gemini-images-v1/fault-scenarios` · error-code-and-text · constructed

- 端点：`POST /chat/completions`
- 行为：内容审核拒绝、内容过滤（沿用成功信封改 finish_reason）、402/403/404/429/5xx。
- 依据：https://ai.google.dev/gemini-api/docs/api-errors（API errors）
- 核对日期：2026-10-29
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

### `jimeng/api-v3`

#### `jimeng-api-v3/generations-shape` · response-shape · real

- 端点：`POST /images/generations`
- 行为：response_format b64_json 答内嵌图片、url 答签名 TOS URL；图生图（b64_json）结构相同。
- 样本：`internal/testutil/fakeupstream/jimeng/api-v3/testdata/jimeng-api-v3-text-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/jimeng/api-v3/testdata/jimeng-api-v3-image-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `jimeng-api-v3/errors` · error-code-and-text · real

- 端点：`POST /images/generations`
- 行为：401 AuthenticationError 与 400 MissingParameter。
- 样本：`internal/testutil/fakeupstream/jimeng/api-v3/testdata/jimeng-api-v3-text-to-image.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `jimeng-api-v3/size-and-contract` · request-validation · constructed

- 端点：`POST /images/generations`
- 行为：size（WxH 总像素 3686400–16777216、比例至多 16，或 2K/4K）、response_format、sequential_image_generation、max_images 1–15、参考图至多 14 张与逐张尺寸，非法值答 400 InvalidParameter（param 指明字段）（#543）。
- 依据：https://www.volcengine.com/docs/82379/1541523（Seedream 图片生成 API）
- 核对日期：2026-11-24
- 没有真实样本的原因：样本只请求过 2048x2048；真实措辞未记录，免费探针待做

#### `jimeng-api-v3/documented-codes` · error-code-and-text · constructed

- 端点：`POST /images/generations`
- 行为：内容安全、风险检测、ContentSecurityDetectionError、模型访问码与 RequestBurstTooFast；429 类带 Retry-After；欠费与限流。
- 依据：https://docs.volcengine.com/docs/82379/1299023（错误码）
- 核对日期：2026-11-24
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `jimeng-api-v3/files-route` · artifact-shape · constructed

- 端点：`GET /files/{name}`
- 行为：签名图片主机不属于 API；提供 response_format=url 指向的固定 JPEG。
- 依据：https://www.volcengine.com/docs/82379/1541523（Seedream 图片生成 API）
- 核对日期：2026-10-29
- 没有真实样本的原因：文件主机不在 API 样本里，媒体字节按 recipe 替换

#### `jimeng-api-v3/files-expired` · error-code-and-text · constructed

- 端点：`GET /files/{name}`
- 行为：过期 URL 答 TOS 的 403 application/json（Code AccessDenied、Message Request has expired）。
- 依据：官方 TOS 主机观测：把 X-Tos-Date 改到过去后请求（2026-10-29）
- 核对日期：2026-10-29
- 没有真实样本的原因：真实调用观测到了，但原始请求与响应没有作为脱敏样本入库；不是自然过期

### `matting/api-v1`

#### `matting-api-v1/jobs-shape` · response-shape · real

- 端点：`POST /jobs`
- 行为：multipart 提交（files + pipeline_params_json），READY 批次信封；401 {detail} 与 422 校验列表。
- 样本：`internal/testutil/fakeupstream/matting/api-v1/testdata/matting-api-v1.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/matting/api-v1/testdata/matting-api-v1-batch-2026-11-23.candidate.json`（2026-11-23，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `matting-api-v1/jobs-multi-file` · response-shape · constructed

- 端点：`POST /jobs`
- 行为：每个 files 分片一个任务，REJECTED 任务与 PARTIAL_SUCCESS / 全部拒绝的批次。
- 依据：运营方抠图服务的 FastAPI /docs（SubmitBatchResponse、TaskStatusItem、BatchStatus 枚举）
- 核对日期：2026-11-08
- 没有真实样本的原因：样本每批只有一个被受理的文件

#### `matting-api-v1/jobs-process-res-422` · request-validation · constructed

- 端点：`POST /jobs`
- 行为：处理分辨率越界答 422（birefnet_process_res 512..4096、sdmatte_process_res 512..2048），正文为 pydantic 校验列表加 ctx。
- 依据：抠图服务源码 PipelineParams（取值范围）
- 核对日期：2026-11-12
- 没有真实样本的原因：越界值没有真实请求过，loc 前缀未观测

#### `matting-api-v1/batches-states` · state-machine · real

- 端点：`GET /batches/{batch_id}`
- 行为：READY → RUNNING → SUCCESS 轮询体、downloadable 随批次翻转、download_path 为 /api/v1/tasks/<task>/download；未知批次 404 {detail}。
- 样本：`internal/testutil/fakeupstream/matting/api-v1/testdata/matting-api-v1.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/matting/api-v1/testdata/matting-api-v1-batch-2026-11-20.candidate.json`（2026-11-21，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/matting/api-v1/testdata/matting-api-v1-batch-2026-11-23.candidate.json`（2026-11-23，captured，app_revision `unknown`，catalog_revision `unknown`）
- 备注：matting-api-v1-batch-2026-11-20 的 meta 记 captured_at_utc 为 2026-11-21（窗口跨过 UTC 零点），按 meta 登记。

#### `matting-api-v1/batches-failure-states` · state-machine · constructed

- 端点：`GET /batches/{batch_id}`
- 行为：FAILED 批次（任务 FAILED 带 error_message）、PARTIAL_SUCCESS、全部 REJECTED/FAILED 无产物的完成批次与 CANCELED 批次。
- 依据：运营方抠图服务的 FastAPI /docs（BatchStatus、TaskStatus 枚举）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `matting-api-v1/download` · artifact-shape · real

- 端点：`GET /tasks/{task_id}/download`
- 行为：鉴权下载 PNG 结果（image/png），没有 Key 时 401 {detail}；服务没有签名 URL。
- 样本：`internal/testutil/fakeupstream/matting/api-v1/testdata/matting-api-v1.candidate.json`（2026-10-29，captured，app_revision `unknown`，catalog_revision `unknown`）
- 样本：`internal/testutil/fakeupstream/matting/api-v1/testdata/matting-api-v1-batch-2026-11-23.candidate.json`（2026-11-23，captured，app_revision `unknown`，catalog_revision `unknown`）

#### `matting-api-v1/download-rgba-and-keys` · artifact-shape · constructed

- 端点：`GET /tasks/{task_id}/download`
- 行为：下载是源图像素尺寸（长边缩到 4096）的 RGBA PNG，背景透明、主体不透明；读不出头的文件给 64x64；服务不认识的 Key 答 401 Invalid authentication credentials 带 WWW-Authenticate: Bearer（#538）。
- 依据：运营方抠图服务的 FastAPI /docs
- 核对日期：2026-11-12
- 没有真实样本的原因：像素与 Key 规则待真实一轮定稿

#### `matting-api-v1/download-404` · error-code-and-text · constructed

- 端点：`GET /tasks/{task_id}/download`
- 行为：REJECTED 任务或文件级故障的任务没有产物，下载答 404。
- 依据：运营方抠图服务的 FastAPI /docs（TaskStatus 枚举）
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本

#### `matting-api-v1/fault-scenarios` · rate-limit-concurrency · constructed

- 端点：`POST /jobs`、`GET /batches/{batch_id}`、`GET /tasks/{task_id}/download`
- 行为：请求级 429、500 与超时故障在各路由上相同。
- 依据：运营方抠图服务的 FastAPI /docs
- 核对日期：2026-11-08
- 没有真实样本的原因：官方 API 上没有触发过这种情况，没有已提交的样本
