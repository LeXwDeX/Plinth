# Meshy 与 Tripo 公开 3D API 能力调研

调研日期：2026-09-30。范围：Meshy 官方 API 文档与 Meshy 官方产品页面；Tripo 官方开发者文档、官方 SDK 文档与官方产品/技术页面。本文是文档证据整理，不代表亲自调用、计费实测、账户权限已开通或输出质量验收。

## 核心结论

- Meshy 的多数独立处理接口使用 `https://api.meshy.ai/openapi/v1`；Text-to-3D 使用 `/openapi/v2/text-to-3d`，Creative Lab 另有 `/openapi/creative-lab/...` 路由。API 路由版本与模型版本分别核对；模型 ID 和账户分批开放能力会变化，应按 endpoint 文档固定版本并在目标账户核验。
- Tripo 当前公开 API 是 v3，Base URL 为 `https://openapi.tripo3d.ai/v3`。`/v2/openapi/task` 是旧版通用任务端点，v3 已改为按能力拆分的端点。
- 两家都公开了多种模型后处理 API。普通 text/image/multiview-to-model 是通用模型生成，不应描述成专用脸模/头像 API 或世界/场景生成 API。Tripo另有 `/mesh/edit`：按 P2 管线和模型空间 bbox 做局部网格编辑；Smart Segmentation 又是独立的端到端分割/自动建模处理，不得与 `/mesh/segment` 合并描述。
- Tripo v3 提供 multipart 文件上传、file token、预签名上传和显式模型导入。Meshy 本次核查到的模型输入方式是内部 API task ID，或 JSON `model_url` 的公开 URL/Data URI；未在官方 API 导航和接口文档中发现 multipart 模型上传、file token 或通用 import-to-task 接口。此结论是对所列官方文档的检索结果，不代表断言其私有网页流程不存在。
- URL 输入要求为可公开访问的 URL。文档没有明确承诺屏蔽/允许私网地址的规则；将“公网可访问”理解为不应依赖调用者内网地址，是依据文档文字的实现建议，而非官方 SSRF 策略说明。
- Tripo 官方 SDK 能力矩阵除 3D 生成外，还列出 `/generation/image-to-multiview` 与 `/generation/edit-multiview` 两个图像预处理路由；另列 `/generation/image-to-splat`、`/models/refine`、`/models/stylize`、`/models/import` 等。其锁定版本的 Java SDK 独有后四种中的 Splat/Refine/Stylize/Import 方法。SDK 有无 wrapper 不等同于 REST 端点是否存在；SDK DTO 也不一定暴露端点完整 schema。

## 文件输入、导入与转换兜底

| 平台/路线 | 输入办法 | 格式/大小与细节 | 输出与注意事项 | 官方证据 |
|---|---|---|---|---|
| Meshy 通用模型输入 | JSON 传 `model_url`；接受公开 URL 或 Base64 Data URI。Data URI 按具体接口要求使用 MIME `application/octet-stream`。 | 没有统一可确认的模型上传上限；每个接口的格式/大小限制不同。官方文档示例未要求文件名，不能推断文件名决定格式。URL 通常要求可下载且扩展名受支持；Data URI 一些接口明确绕过扩展名检查。 | 没查到官方 multipart 上传端点、`file_token` 或通用 Import Model API。外部本地资产需按 endpoint 支持情况编码为 Data URI，或由服务端提供公开 URL。 | [API 导航](https://docs.meshy.ai/en/api)、[Convert](https://docs.meshy.ai/en/api/convert)、[Remesh](https://docs.meshy.ai/en/api/remesh) |
| Meshy Convert | `POST /openapi/v1/convert`。仅其一：已成功 Meshy API task 的 `input_task_id`，或外部 `model_url`。 | 输入 `.glb/.gltf/.obj/.fbx/.stl`；URL 或 Data URI；至少指定一个 `target_formats`。 | 输出格式 `glb/fbx/obj/usdz/blend/stl/3mf`。每任务官方计价 1 credit；需以账户价格页和实际 Usage 为准。Task ID 必须为 SUCCEEDED。 | [Convert API](https://docs.meshy.ai/en/api/convert)、[Pricing](https://docs.meshy.ai/en/api/pricing) |
| Tripo 文件上传 | `POST /files` multipart/form-data 上传并拿 `file_token`。较大文件可 `POST /files/presign` 获取 presigned PUT URL，然后 HTTP PUT 到存储，再以 token 调 API。 | multipart：图片 JPEG/PNG ≤20 MB；模型 GLB/GLTF/FBX/OBJ/STL ≤150 MB。>60 MB 建议 presign。presigned 上传格式字段也列有图片 JPEG/JPG/PNG/WebP/BMP/TIFF，模型 GLB/GLTF/FBX/OBJ/STL/3MF/USDZ；URL 有效30分钟。 | 返回 token，后续 API 依其各自契约接收。Presign 页还以 `/models/import` 演示后续处理。 | [File Upload](https://developers.tripo3d.ai/en/docs/files)、[Large File Upload](https://developers.tripo3d.ai/en/docs/files-presign) |
| Tripo Import | `POST /models/import`，`input` 接 file token 或可公开访问 URL。 | GLB/GLTF/FBX/OBJ/STL ≤150 MB。 | 异步创建 Tripo task，供后续处理。官方 v3 专有路线，Meshy 未查到对等通用 import。 | [Import Model](https://developers.tripo3d.ai/en/docs/models-import) |
| Tripo Convert | `POST /models/convert`；`input` 接允许类型中的 task ID、file token 或公开 URL。 | 输入 GLB/GLTF/FBX/OBJ/STL，最大150 MB。 | 输出目标格式含 GLTF、FBX、USDZ、OBJ、STL；OBJ 不保动画，STL 仅几何。转换参数中可选网格处理/烘焙等，以该页为准。 | [Convert Format](https://developers.tripo3d.ai/en/docs/models-convert) |

## Meshy API 能力表（多数处理接口 `/openapi/v1`，例外按行注明）

费用栏只写官方文档明确的固定价或官方 Pricing 页信息；未列金额表示此次查到的 endpoint 页面未确认固定数值，不能从示例 task 的 `consumed_credits` 倒推价格。

| 能力/端点 | 允许输入与限制 | 输出/版本/费用/权限 | 官方 URL |
|---|---|---|---|
| Text to 3D | `POST /openapi/v2/text-to-3d`；文本 prompt；Preview 后 Refine 的两阶段常规生成流程。不是头像专用 API。 | API 文档动态模型 ID；`latest` 会移动。具体 endpoint 参数、模型、价格需针对目标 ai_model 查看。 | [Text to 3D](https://docs.meshy.ai/en/api/text-to-3d) |
| Image to 3D | 图片 URL 或 Data URI；task ID 可引用成功 Text-to-Image/Image-to-Image API 产物。 | 普通单图生成；支持不同几何/纹理选项，不能称专用 face API。 | [Image to 3D](https://docs.meshy.ai/en/api/image-to-3d) |
| Multi-Image to 3D | `POST /multi-image-to-3d`，1–4 张同一对象不同角度图片，URL或图片 Data URI；或受支持的成功图片生成 task。 | `meshy-7.1`/`latest`：首图作为正面视图，其余顺序无关；支持 standard/2k geometry_resolution（2k仅7.1/latest）；纹理参考视图有具体参数约束。不是场景拼接/世界生成。 | [Multi-Image to 3D](https://docs.meshy.ai/en/api/multi-image-to-3d) |
| Remesh | `POST /remesh`。task ID只支持 SUCCEEDED 的 Text 3D Preview/Refine、Image 3D、Retexture；或公开 URL/Data URI，GLB/GLTF/OBJ/FBX/STL；Data URI MIME `application/octet-stream`。 | 输出 GLB/FBX/OBJ/USDZ/Blend/STL/3MF；triangle/quad；target polycount范围依 tier（文档列 100–300,000，输出可偏离目标）。转换/resize类旧参数 deprecated，宜用 Convert/Resize。 | [Remesh](https://docs.meshy.ai/en/api/remesh) |
| Convert | `POST /convert`。成功 Meshy task ID（SUCCEEDED）或 URL/Data URI；输入五格式 GLB/GLTF/OBJ/FBX/STL。 | 输出 GLB/FBX/OBJ/USDZ/Blend/STL/3MF；至少一个格式；官方定价 1 credit/task。 | [Convert](https://docs.meshy.ai/en/api/convert) |
| Resize | `POST /resize`。成功 Meshy task ID，或 URL/Data URI 五格式 GLB/GLTF/OBJ/FBX/STL。Task 输入输出 GLB；URL 输入保留格式。 | 高度、最长边、AI 自动估尺寸三选一；原点 bottom/center；官方定价1 credit/task。 | [Resize](https://docs.meshy.ai/en/api/resize) |
| UV Unwrap | `POST /uv-unwrap`。成功 Meshy task 且产 GLB，或 URL/Data URI GLB。 | ≤40,000 faces，超限先 Remesh；输出单 GLB UV 白模，三角化 quad/ngon；5 credits/call；按 Statsig rollout 开通，账号可能 404。 | [UV Unwrap](https://docs.meshy.ai/en/api/uv-unwrap) |
| Retexture | `POST /retexture`。task ID只支持成功 Text 3D Preview/Refine、Image 3D、Remesh；或者 URL/Data URI，GLB/GLTF/OBJ/FBX/STL。 | 文字（≤800字符）、风格图片或同物1–4视图三类引导，视图互斥；多视图要求显式指定 Meshy 7（文档值处于渐进开放）；可开PBR、保UV、2K/4K/8K，输出多种格式。模型开放状态依账户。不要把它说成局部几何编辑。 | [Retexture](https://docs.meshy.ai/en/api/retexture) |
| Rigging | `POST /rigging`。成功 API task ID或 URL/Data URI。Task ID支持 textured humanoid，超过300k faces不支持；外部模型仅 textured humanoid GLB，脸朝 +Z；纹理图可 URL/Data URI PNG。 | 只适合结构清楚的双足 humanoid；无纹理/非人形/肢体不明确易失败。第三方 OBJ/FBX需先转 GLB。失败模式含 pose estimation failure。 | [Rigging](https://docs.meshy.ai/en/api/rigging) |
| Animation | `POST /animations`。只接受成功 Rigging task ID；`action_id`、`action_ids`（1–10）与 `motion_task_id` 三选一。 | 对 preset action 3 credits/action；Motion clip 要双足 rig，clip 资产保留3天。可后处理 FPS。 | [Animation](https://docs.meshy.ai/en/api/animation) |
| Text to Motion | `POST /text-to-motion`。文本描述≤400字符、时长2–10秒，步长0.5秒；无需输入角色模型。 | `prime`: 10 credits，FBX；`swift`: 3 credits，BVH。作为 motion_task_id 交给 Animation API 时须在3天资源保留期内，且目标角色要双足 rig。 | [Text to Motion](https://docs.meshy.ai/en/api/text-to-motion) |
| Print Analyze | `POST /print/analyze`。成功且归属当前用户的 Image/Multi-Image/Text-to-3D、Remesh、Retexture任务，且 Meshy 6+；或模型 URL/Data URI。URL格式 GLB/GLTF/OBJ/FBX/STL、≤100 MB；data URL可跳过扩展名校验。 | 输出 FDM 可打印性指标（watertight、volume、holes、non-manifold、degenerate faces等）；task例子和结果字段显示0 credits，任务可能复用缓存。 | [Analyze Printability](https://docs.meshy.ai/en/api/analyze-printability) |
| Print Repair | `POST /print/repair`。成功归属用户且有 GLB 资产的支持 task，或 URL/Data URI 3D模型；官方示例还支持直接 STL URL。文档错误说明模型最大100MB。 | 修补打印问题，输出保留输入格式（GLB task输入按GLB返回），不重烘焙纹理，`texture_urls`为空；成功10 credits。免费计划不可建任务（需付费方案）。 | [Repair Printability](https://docs.meshy.ai/en/api/repair-printability) |
| Auto Split | `POST /print/split`。只接 SUCCEEDED 的 Image-to-3D/Multi-Image/Text-to-3D Preview/Remesh/Convert/Resize task；模型须 Meshy 6/7系，不支持 low-poly/Smart Topology (meshy-t2)。此 endpoint 文档未提供直接 model_url 外部文件输入。 | `auto`、`by_parts`、`by_color`（需图像生成来源；多视图用首张图）；prompt可命名1–10部件/≤600字符；可加连接件；结果不保留纹理，改平面顶点色。格式 glb/obj/fbx/usdz/blend/3mf 保持各部件对象，STL会融合；任务费用/账户权限本次页面未确认。prompt parsing共享每账户12次/分；该模式可能临时503。 | [Auto Split](https://docs.meshy.ai/en/api/auto-split) |
| Multi-Color Print | `POST /print/multi-color`。成功任务仅 Image/Multi-Image/Text-to-3D/Remesh/Retexture；或 URL/Data URI，仅 GLB/FBX。 | 输出多色3MF，1–16色；输入需有颜色信息，realistic需要每个mesh有UV+单一base-color纹理，cartoon可用顶点色；打印机品牌可写入预设。价格/账户权限以 Pricing/实际Usage核验。 | [Multi-Color Print](https://docs.meshy.ai/en/api/multi-color-print) |
| Creative Lab 专用成品 API族 | 见下方“Creative Lab 逐产品端点表”。公开目录包含9类产品，绝大多数是图片 prototype→确认→build 两阶段，以产品内 task_id衔接；每款都有独立、带版本的路由。 | 这是特定印制成品/浅浮雕/摆件设计流水线，不是自由通用3D建模或头像端点。原料是用户照片，不是已有模型task。外部网页生成出的 prototype不能传给API build；产品build通常需同API账户创建的成功prototype。 | [Meshy API导览](https://docs.meshy.ai/en/api)、[Creative Lab Keychain](https://docs.meshy.ai/en/api/creative-lab-keychain)、[Pricing](https://docs.meshy.ai/en/api/pricing) |

### Meshy 打印能力额外项

Meshy API 导航还列有 Balance API：机器可读规范中确认为 `GET /v1/balance`（Base `https://api.meshy.ai/openapi`）；这是账户积分查询，不是3D资产处理能力。标准公开 OpenAPI JSON/YAML在 scope 说明中明确排除了 Creative Lab（partner-specific surface），所以 Creative Lab 必须按其官方网页文档另行纳入。

### Creative Lab 逐产品端点

共同输入模式：各产品 prototype 的 `image_url` 接公网可下载照片 URL 或图片 Data URI；常见格式 JPEG/JPG/PNG/WebP，Data URI示例是 `data:image/jpeg;base64,...`。图片上限和参数不完全一致；Keycap页给出明确上限：下载后≤20 MB、每边至少32px、总像素≤178,956,970，格式从字节解码识别而非信任扩展名；不能把该上限推广到所有 Creative Lab 产品。Prototype/build计价按官方 Pricing 页面所列名义 credits；API key、计划与内容 moderation 均适用各 endpoint 的失败条件。

| 产品/输出 | HTTP端点 | 精确链路及特点 | 费用（官方名义定价；非实测） | 官方文档 |
|---|---|---|---|---|
| Keychain 钥匙扣/浮雕章 | `POST /openapi/creative-lab/keychain/v1/prototype` → `.../build` | prototype接受 source photo；可刻≤10字符名字；build必须同API key已成功且恰有一个候选的 prototype。输出GLB、OBJ/MTL+texture zip或全部artifact zip；提供徽章形状、毫米尺寸、浮雕高度/曲线/厚度等控制。 | Prototype 6 + Build 30 | [Keychain](https://docs.meshy.ai/en/api/creative-lab-keychain) |
| Fridge Magnet 冰箱贴 | `POST /openapi/creative-lab/fridge-magnet/v1/prototype` → `.../build` | prototype是彩色概念图；build出圆角矩形、平背、深度浮雕模型；build专属参数见文档，外形不是任意建模。 | Prototype 6 + Build 30 | [Fridge Magnet](https://docs.meshy.ai/en/api/creative-lab-fridge-magnet) |
| Figure Q版手办 | `POST /openapi/creative-lab/figure/v1/prototype` → `.../build` | 从照片生成 stylized/chibi概念图再输出纹理模型，GLB与OBJ/MTL/texture；需要同产品成功prototype task。 | Prototype 6 + Build 30 | [Figure](https://docs.meshy.ai/en/api/creative-lab-figure) |
| Vinyl Figure 大头公仔 | `POST /openapi/creative-lab/vinyl-figure/v1/prototype` → `.../build` | 照片→大头vinyl概念图→纹理化3D收藏公仔；不等同通用可绑定头像API。 | Prototype 6 + Build 30 | [Vinyl Figure](https://docs.meshy.ai/en/api/creative-lab-vinyl-figure) |
| Brick Figure 积木小人 | `POST /openapi/creative-lab/brick-figure/v1/prototype` → `.../build` | 照片→积木风格minifigure概念图→纹理模型；输出GLB与OBJ/MTL/base-color。 | Prototype 6 + Build 30 | [Brick Figure](https://docs.meshy.ai/en/api/creative-lab-brick-figure) |
| Lamp 灯罩 | `POST /openapi/creative-lab/lamp/v1/prototype` → `.../build` | prototype图像可 `image_subject=character|landscape`，并直接返回中空GLB灯罩；build将模型按直径缩放、切平底、掏空、可加入灯座孔及mount，输出STL或ZIP。是对象照片转产品的专用生成流水线，image_subject=landscape不代表世界/场景生成。 | Prototype 30 + Build 6（定价页如此列，勿按示例 task 估算） | [Lamp](https://docs.meshy.ai/en/api/creative-lab-lamp) |
| Keycap 键帽 | `POST /openapi/creative-lab/keycap/v1/prototype` → `.../build` | prototype每请求恰一张完成态设计render并带一个candidate_id；build需要prototype `input_task_id` + `candidate_id`。标准 Cherry MX 1u base `cherry-mx-1x1-r1`；输出实尺纹理模型GLB/OBJ bundle；需付费订阅计划。 | Prototype 12 + Build 50 | [Keycap](https://docs.meshy.ai/en/api/creative-lab-keycap) |
| Collapsible Fidget 可收缩玩具 | `POST /openapi/creative-lab/fidget-collapsible/v1`（单步，不分prototype/build） | 照片silhouette变同一件打印件中的嵌套同心环；图片URL/Data URI；单一清晰封闭轮廓最合适，复杂背景/多主体/极细轮廓可能任务失败。页面称Web UI部分几何调节不开放给API，API使用固定服务端默认值。 | 6；需付费计划 | [Collapsible Fidget](https://docs.meshy.ai/en/api/creative-lab-fidget-collapsible) |
| Fidget Pixel 像素拼插板 | `POST /openapi/creative-lab/fidget-pixel/v1/prototype` → `.../build` | prototype可设 `type=person|other`；build须同账户成功prototype，16或32网格、square/hex部件、尺寸/色数/高度；输出单个带颜色对象的多色3MF。build task需让用户确认概念图后再提交付费步骤。 | Prototype 6 + Build 30；需付费计划 | [Fidget Pixel](https://docs.meshy.ai/en/api/creative-lab-fidget-pixel) |

官方定价也列有各产品 stage的名义价：[Meshy API Pricing](https://docs.meshy.ai/en/api/pricing)。对外集成应读取实际 `consumed_credits`、重试/失败退款策略与套餐限制，尤其区分 POST被拒未创建task、创建后FAILED、PENDING取消和IN_PROGRESS取消。

- [完整 Meshy API 导航](https://docs.meshy.ai/en/api)
- [Pricing](https://docs.meshy.ai/en/api/pricing)
- [Usage API](https://docs.meshy.ai/en/api/usage)

## Tripo API 能力表（官方 API v3）

Tripo v3 常用模型处理 `input` 支持 task ID/file token/公开 URL，但**具体端点允许的 task 类型、输入格式、大小要按行读**。生成接口的图片 task ID 通常只引用 Tripo 自身的图片生成任务，3D后处理的 task ID通常要求来自规定生成/上一步骤任务。H Series 当前包括 v3.x 等通用高质量建模版本；P1/P2 面向低多边形、干净拓扑与面数控制。P1 当前模型 ID 为 `P1-20260311`，P2 为 `P2-20260801`；P2 独有 `quad`，P2面数范围为三角形48–50,000、四边形48–25,000，P1范围50–20,000。P 系列已用于 text/image/multiview-to-model，参数不应跨版本盲传。价格会受到模型版本、纹理质量、附加项、套餐/折扣影响；下表只记 endpoint 文档明示值或定价页范围。

| 能力/端点 | 输入条件 | 输出/模型/费用/权限 | 官方 URL |
|---|---|---|---|
| Text to Model | `POST /generation/text-to-model`，文本 prompt；不需要模型文件。 | 当前文档模型版本推荐 Tripo v3.1；支持几何质量、面数、纹理/PBR等。通用生成，不是专门脸模/头像/世界接口。 | [H Series Text-to-3D](https://developers.tripo3d.ai/en/docs/generation-text-to-model/standard)、[Models & Versions](https://developers.tripo3d.ai/en/docs/models-and-versions) |
| Image to Model | `POST /generation/image-to-model`，file token、公开图片URL，或 Tripo text-to-image/image-to-image task ID；PNG/JPEG/WebP，≤20 MB，推荐≥256px且主体清楚少遮挡。 | H 系列面向通用高质量建模，参数含几何/纹理质量与面数等；P 系列官方描述为低多边形、拓扑较干净，当前页面列 P1 `P1-20260311`（`face_limit`）与 P2 `P2-20260801`（可选 quad）。P/P2 的具体面数范围和可用参数依模型而异，见各版本页。面数控制是网格能力，不是脸模专用 API。 | [Image-to-3D H Series](https://developers.tripo3d.ai/en/docs/generation-image-to-model)、[Image-to-3D P Series](https://developers.tripo3d.ai/en/docs/generation-image-to-model/p)、[Models & Versions](https://developers.tripo3d.ai/en/docs/models-and-versions) |
| Multiview to Model | `POST /generation/multiview-to-model`；H/P 都接受 `inputs` 中视图键、固定顺序 positional 或前一 image-to-multiview/edit-multiview task ID；至少两视图且必须 front，支持 file token/公开 URL。 | H Series 默认 v3.1 系列，几何/纹理等参数较多；P1/P2 强调低多边形/干净拓扑，并按 P 版本提供 `face_limit`/quad 等参数。官方 Pricing 当前列价不贴在此泛化行，因为模型/纹理/附加参数有差异，按价格页与实际 Usage核对。常规多视角同物生成，不是完整世界重建或多物体场景生成。 | [Multiview H Series](https://developers.tripo3d.ai/en/docs/generation-multiview-to-model/standard)、[Multiview P Series](https://developers.tripo3d.ai/en/docs/generation-multiview-to-model/p)、[Models & Versions](https://developers.tripo3d.ai/en/docs/models-and-versions) |
| Image to Multiview | `POST /generation/image-to-multiview`；输入单张图片 `input`，可为 `file_token` 或公开 URL，PNG/JPEG/WebP，主体应清楚、背景整洁。 | 异步返回 task ID；成功结果含 `front_view_url`、`left_view_url`、`back_view_url`、`right_view_url`，可作为 multiview-to-model 的 task 输入。官方 API Pricing 页当前列价 10 credits；API单次实际扣费以 `credits_consumed`/账户 Usage 为准。 | [Image to Multiview](https://developers.tripo3d.ai/en/docs/generation-image-to-multiview)、[API Pricing](https://developers.tripo3d.ai/en/pricing) |
| Edit Multiview | `POST /generation/edit-multiview`；输入 `input` 三选一：先前多视图生成的 task ID、file token、公开多视图图片 URL；还要求 `prompts[]`，每项包含编辑 `prompt` 与 `view`（front/left/back/right）。 | 对既有多视图的各指定视角做一致编辑，异步创建任务并返回 task ID；成功后的 task ID 可交给 multiview-to-model 复用四视图输出。Pricing 页当前列价 5 credits/edited image；具体单次任务扣费以 `credits_consumed`/账户 Usage 为准。不能把该图像编辑能力称为模型几何编辑。 | [Edit Multiview](https://developers.tripo3d.ai/zh/docs/generation-edit-multiview)、[API Pricing](https://developers.tripo3d.ai/en/pricing)、[Migration](https://developers.tripo3d.ai/en/docs/migration-v2-to-v3) |
| Image to Gaussian Splat | `POST /generation/image-to-splat`；单图 `input` 为 `file_token` 或公开 URL，二选一；PNG/JPEG/WebP，≤20 MB，推荐≥256×256，主体清晰、干净背景、少遮挡；可选 `model_seed`。 | 异步任务输出 `.splat` 的 `output.model_url`，非 mesh/贴图资产。官方文档说明固定 30 credits/次，约4分钟生成；API task example 也为30。适用于 Gaussian Splat 查看器/实时渲染与真实感重建，不是可编辑场景图。 | [Image to Gaussian Splat](https://developers.tripo3d.ai/en/docs/generation-image-to-splat)、[API Pricing](https://developers.tripo3d.ai/en/pricing) |
| Refine Draft | `POST /models/refine`。公开迁移表证实端点；官方 ComfyUI 文档描述只接受可 refine 的 draft model task context，不兼容输入会失败。 | SDK矩阵里 Java wrapper独有。完整REST参数/输入格式/费用页面本次未能核实，列为待逐页核对；不是局部编辑合同。 | [Migration](https://developers.tripo3d.ai/en/docs/migration-v2-to-v3)、[ComfyUI Nodes](https://developers.tripo3d.ai/en/docs/comfyui-nodes)、[SDK](https://developers.tripo3d.ai/en/docs/sdk) |
| Stylize Model | `POST /models/stylize`。公开迁移表证实端点；SDK矩阵显示 Java wrapper独有。 | 本次未确认可接受格式、版本、费用；不能推广成任意模型局部编辑。 | [Migration](https://developers.tripo3d.ai/en/docs/migration-v2-to-v3)、[SDK](https://developers.tripo3d.ai/en/docs/sdk) |
| Import Model | `POST /models/import`，file_token或公开URL；GLB/GLTF/FBX/OBJ/STL，≤150 MB。 | 新建 Tripo task，让外部模型进入后处理链。 | [Import Model](https://developers.tripo3d.ai/en/docs/models-import) |
| Texture | `POST /models/texture`；task/file_token/URL，GLB/GLTF/FBX/OBJ/STL ≤150 MB。 | 支持文字、风格图、单图、恰好四视图(front/left/back/right)纹理指引；v3.5-20260815支持fast/delight，extreme=8K并增加费用。指定 segmentation task部件可局部选部件重贴图，但这是按部件纹理，不是mesh局部几何编辑。 | [Texture](https://developers.tripo3d.ai/en/docs/models-texture) |
| Convert | `POST /models/convert`；task/file_token/URL，五格式≤150 MB。 | GLTF/FBX/USDZ/OBJ/STL；OBJ无动画、STL仅几何。官方 API Pricing 当前列 Basic 5、Advanced 10 credits；任一非默认的 quad、face_limit、flatten_bottom、flatten_bottom_threshold、texture_size、texture_format、pivot_to_center_bottom、scale_factor 会触发 Advanced价。 | [Convert](https://developers.tripo3d.ai/en/docs/models-convert) |
| Smart Segmentation（image/model pipeline） | `POST /mesh/smartsegment`；`seg_type=image` 输入 PNG/JPEG/WebP 图片，`seg_type=model` 输入仅 GLB，且此模式必填列主序4×4 `transform`（image 模式不需要）；`input` 为 file token（可由普通/预签名上传取得）或公开 URL。可选 `granularity` 取 coarse、medium 或 fine、`hint`，`face_limit` 可选（最小100）、`ref_image` 可选（file token或URL）、`compress=geometry` 可选。未发现该端点公开的输入 MB 上限。 | 与 `/mesh/segment` 不同：这是 end-to-end smart segmentation pipeline，官方页称含自动建模；普通 `/mesh/segment` 只分割已有模型。image 示例成功结果含 `mask_url`、`seg_model_url`、`seg_task_id`、`model_task_id` 与 prompt。模型流程页示例不全，模型结果字段与 task 限制须目标账户验证。Pricing 当前列 Smart Segmentation Image 85 / Model 55 credits。 | [Smart Segmentation](https://developers.tripo3d.ai/en/docs/mesh-smartsegment)、[API Pricing](https://developers.tripo3d.ai/en/pricing) |
| Mesh Local Edit | `POST /mesh/edit`；模型输入 `task_id`、file token 或 URL（模型文件格式依通用输入控件为 GLB/GLTF/FBX/OBJ/STL）；必填模型空间轴对齐 bbox（2×3）；可选 `model=P2-20260801`（唯一支持版本）、`face_limit`（提供时≥100）、参考图 `ref_image`（file token或URL）、compress。示例可从 Task ID 或模型 file token 提供文件。 | 端到端局部重试/精修 bbox 所框区域，是明确存在的局部几何/网格编辑能力。异步创建 Task，官方通用成功结果示例为 `output.model_url` 与 `rendered_image_url`，但专页未见 route-specific output schema。未找到独立公开固定价格表项，费用未知，不能借用 Smart Segmentation 或 Retopology 价格。 | [Mesh Local Edit](https://developers.tripo3d.ai/en/docs/mesh-edit) |
| Segment | `POST /mesh/segment`；task/file_token/URL，五格式≤150 MB。 | v1.0-20250506几何分割；v2.0-20260430 beta语义标签+几何。v2 granular 为 simple、balanced 或 detailed，并可设 connectivity、ref_image；一旦提供ref_image，granularity/connectivity被忽略。task 示例 `credits_consumed=40` 不是定价依据；官方 API Pricing 页当前另列 Segmentation 为40 credits。应区分公开价目表与账号实际扣费，并以目标账户 Usage 核对。 | [Segmentation](https://developers.tripo3d.ai/en/docs/mesh-segment) |
| Complete | `POST /mesh/complete`；必须输入 mesh/segment task ID，不能把任意本地文件直接传入该端点。 | 只 v1.0-20250506；选分割部件或全补；`ai_completion`生成缺失几何，`quick_cap`只封闭边界。API Pricing 当前列 Part Completion Default 50、Quick Cap 30 credits。 | [Mesh Complete](https://developers.tripo3d.ai/en/docs/mesh-complete) |
| Retopology/Decimate | `POST /mesh/decimate`；task/file_token/URL，五格式≤150 MB。 | v2.0 smart：30 credits，tri 500–20k/quad 500–10k，自适应可省face_limit，支持bake及part_names，输出GLB且不支持format；v1.0 basic：10 credits，face_limit必填，tri最高2M/quad150k，不支持bake/part_names。 | [Retopology](https://developers.tripo3d.ai/en/docs/mesh-decimate) |
| Rig Check | `POST /animations/rig-check`；task/file_token/URL，但输入只GLB ≤150 MB。 | 输出是否可rig及建议rig type；task 示例 `credits_consumed=0` 本身不能证明免费；官方 API Pricing 页当前明确列 Rig Check 为 Free。上线核验仍应读取目标账户 Usage/最新价目表。 | [Rig Check](https://developers.tripo3d.ai/en/docs/animations-rig-check) |
| Auto Rig | `POST /animations/rig`；task/file_token/URL，GLB/GLTF/FBX/OBJ/STL≤150MB。 | v1.0-20240301 humanoid/biped；v2.5-20260210支持非人形类型（quadruped/hexapod/octopod/avian/serpentine/aquatic）；Tripo/Mixamo骨名；GLB/FBX输出。task 示例 `credits_consumed=30` 不能当作固定价；官方 API Pricing 页当前列 Auto Rig 为25 credits，与端点示例数字冲突。公开固定价以价目表为准，个别账户/模型的实际扣费需查 Usage。 | [Auto Rig](https://developers.tripo3d.ai/en/docs/animations-rig) |
| Retarget | `POST /animations/retarget`；只 rigged model task ID。一次 `animation` 或 `animations`；输出GLB/FBX。 | preset动作；`bake_animation` 已被官方标为弃用并忽略（2026-10-20 核对），默认带geometry。API Pricing 当前列10 credits/animation。 | [Animation Retarget](https://developers.tripo3d.ai/en/docs/animations-retarget) |
| Task/files/account | `GET /tasks/{task_id}`、`POST /tasks/list`、`POST /files`、`POST /files/presign`、account balance/usage。 | 通用基础设施，不是创作能力。并发和速率限制按类别/账号执行。 | [Introduction](https://developers.tripo3d.ai/en/docs/introduction)、[Rate Limits](https://developers.tripo3d.ai/en/docs/rate-limits) |

## Meshy 本地文件 URL、Data URI 与公网限制

- 官方多项 Meshy 接口把模型文件放在 JSON 字段 `model_url`，并明确允许 Data URI；这是“直接携带二进制的编码值”，不是 multipart 上传，也不是 Meshy 资源 file token。
- 对 Remesh/Convert/Resize/Rigging/Retexture 等页面，模型 Data URI MIME 类型要求 `application/octet-stream`。打印分析/修补页面接受 `data:` URL，但调用方应照 endpoint 的描述组织数据。
- Meshy 的 retexture 风格图片通常是 `data:image/jpeg;base64,...` 或公开 JPEG/PNG URL；Multi-Image 输入逐张支持 JPEG/PNG Data URI。数据格式规定应以该端点为准，不能假设模型 Data URI 的 octet-stream规则也适用于图片。
- API task ID通常不等价于外部资产导入。各操作限定 source task类型；若持有任意模型文件，优先看端点是否支持外部 `model_url`，否则需要官方 Convert/Import之外的转换步骤。Meshy 未查到通用 Import task。
- 文档的 “publicly accessible URL” 明确要求服务方可以下载；这意味着 localhost、私有VPC地址或只在调用机器可达的文件路径不构成已证明可用的URL。官方页面没有对 RFC1918 拒绝策略、DNS重绑定或最大下载时长给出足够明确条文，因此不能声称它必定拒绝或允许内网地址。面向产品设计应避免传内网URL，直接使用平台服务端下载并以 Data URI/自家公网受控对象存储提供字节。
- 当前官方资料没有说明 Meshy 按 Data URI 的 filename 推断格式；文档强调 MIME 类型、扩展名与真实模型格式。不要依赖 filename inference。
- Tripo REST 文档列出的本地文件路径接入不是将本机路径直接写进 `input`：先用 `POST /files` multipart 上传（普通大小文件）或 `POST /files/presign` /预签名 PUT（大文件）取得 `file_token`，再按 endpoint 契约传 token；也可使用公开 URL，部分端点还接受规定类型的 Tripo task ID。已查到的端点契约没有普遍列出 Data URI。官方 Java SDK 锁定版本提供 `LocalFileInput` 抽象，但本地文件须先经上传/解析策略转为远程可引用输入；这不是服务端能够读取调用者本机路径。

## 特殊能力分类，避免宣传混淆

| 用户需求 | 官方 API 证据可支持的说法 | 不能外推的说法 |
|---|---|---|
| 脸模/头像 | 两家普通模型生成、image-to-model / multiview-to-model都可按 prompt 或图片生成对象；Meshy/Tripo 都有 humanoid rigging能力。 | 未找到本次核实范围内可明确命名为 face/head/avatar-only 的独立生成 endpoint，也未确认身份保持、可动画面部表情或脸部拓扑保证。产品网页示例/普通人物生成不能代替专用API契约。 |
| 3D局部编辑 | Tripo 已有 P2 bbox 驱动的 `/mesh/edit` 局部网格精修；另有 Smart Segmentation 自动建模、mesh segmentation/completion、按部件 texture/decimate；Meshy有 retexture 和打印分件处理。 | Mesh Local Edit 是明确的局部几何修改入口，但受 bbox 与 P2 模型管线约束；其他分割、贴图、补全也不自动等于通用任意笔刷雕刻。 |
| 世界/场景生成 | Tripo 有 image-to-Gaussian-splat API路线；两家有多图同一物件建模。 | Gaussian splat 或 Multi-Image-to-3D不自动等于文本到可编辑完整世界/场景、空间布局或场景图API。 |
| 3D打印 | Meshy公开分析、修复、自动分割、多色3MF，以及几何/格式处理，是实际专用API族。 | Meshy Auto Split 结果不保留原贴图；Multi-color 需要实际颜色源；Analyze/Repair的输入task类型、账户归属和格式受限。 |

## 费用与访问的核对规则

- 本文金额都是官方文档发布的名义 credit 值或示例，不是扣费实测，不等于美元换算，也不保证某账号已开通。以该用户的 API 账户 Pricing、提交结果 `credits_consumed`/`consumed_credits` 与 Usage API实际记录为准。
- Meshy 某些新端点（如 UV unwrap）按账户 feature flag rollout；部分模型值也渐进开放。Tripo 有账号类别并发池与请求速率限制，详见官方 Rate Limits。
- Tripo SDK文档表是SDK实现覆盖快照，不能作为 REST访问权限、产品功能灰度或未来 SDK 版本的长期保证。该官方 Java SDK 锁定源代码版本 [7b6a68d](https://github.com/VAST-AI-Research/tripo-java-sdk/tree/7b6a68d855e01a0b098775b1ee14ce7f3c8ac7d9)：`EndpointDescriptor` 确认路由；`TaskRequestPayload` 只把 `model`、`prompt`、`negative_prompt` 作为便利字段，并允许任意额外 Map，不是 per-endpoint schema；本地 `LocalFileInput` 须先经上传策略解析后再生成 payload。因此不从通用 DTO 推定 Refine/Splat/Stylize 的完整字段或本地格式限制。核查官方 [llms.txt](https://developers.tripo3d.ai/llms.txt)、[sitemap.xml](https://developers.tripo3d.ai/sitemap.xml) 时，目录提供 image-to-multiview/edit-multiview 详细页，但未提供 image-to-splat/refine/stylize 专页链接。后续已直接读取 [Image-to-Splat 专页](https://developers.tripo3d.ai/en/docs/generation-image-to-splat)，补齐其输入、输出和30-credit文档价。Refine、Stylize 的详细 REST 输入合同、文件限制与费用仍未找到可核实的公开资料；保留为待补证，不把缺少 SDK 型别或索引链接当作接口不存在。

## 主要官方索引

- [Meshy API Reference](https://docs.meshy.ai/en/api)
- [Meshy Changelog](https://docs.meshy.ai/en/api/changelog)
- [Meshy Pricing](https://docs.meshy.ai/en/api/pricing)
- [Tripo API Introduction](https://developers.tripo3d.ai/en/docs/introduction)
- [Tripo v2-to-v3 Migration](https://developers.tripo3d.ai/en/docs/migration-v2-to-v3)
- [Tripo SDK Integration](https://developers.tripo3d.ai/en/docs/sdk)
- [Tripo Models & Versions](https://developers.tripo3d.ai/en/docs/models-and-versions)
- [Tripo Image to Multiview](https://developers.tripo3d.ai/en/docs/generation-image-to-multiview)
- [Tripo Multiview to Model — P Series](https://developers.tripo3d.ai/en/docs/generation-multiview-to-model/p)
- [Tripo Image to Model — P Series](https://developers.tripo3d.ai/en/docs/generation-image-to-model/p)
- [Tripo Smart Segmentation](https://developers.tripo3d.ai/en/docs/mesh-smartsegment)
- [Tripo Mesh Local Edit](https://developers.tripo3d.ai/en/docs/mesh-edit)
- [Tripo Image to Gaussian Splat](https://developers.tripo3d.ai/en/docs/generation-image-to-splat)
- [Tripo Changelog (P2 release and version support)](https://developers.tripo3d.ai/en/docs/changelog)
- [Tripo Edit Multiview](https://developers.tripo3d.ai/zh/docs/generation-edit-multiview)
- [Tripo API Pricing](https://developers.tripo3d.ai/en/pricing)
- [Tripo Java SDK source at inspected revision](https://github.com/VAST-AI-Research/tripo-java-sdk/tree/7b6a68d855e01a0b098775b1ee14ce7f3c8ac7d9) ([EndpointDescriptor](https://github.com/VAST-AI-Research/tripo-java-sdk/blob/7b6a68d855e01a0b098775b1ee14ce7f3c8ac7d9/src/main/java/ai/tripo3d/sdk/internal/contract/EndpointDescriptor.java), [TaskRequestPayload](https://github.com/VAST-AI-Research/tripo-java-sdk/blob/7b6a68d855e01a0b098775b1ee14ce7f3c8ac7d9/src/main/java/ai/tripo3d/sdk/model/request/TaskRequestPayload.java), [LocalFileInput](https://github.com/VAST-AI-Research/tripo-java-sdk/blob/7b6a68d855e01a0b098775b1ee14ce7f3c8ac7d9/src/main/java/ai/tripo3d/sdk/input/LocalFileInput.java))
