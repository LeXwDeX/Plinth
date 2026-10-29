# Provider 与 Product Schema 边界

更新：2026-10-22，补充「Meshy 官方合同基线」（#329）；2026-10-09，补充「最新 API 与最高质量」设计宗旨与 `fixed` 参数合同，以及 Meshy 文生 3D 自动精细化；2026-10-08 补充 Issue #134/#135 的 PC Skill 界面与可选分类合同。
源码基线为主线 `5bb885e453c0b7951bcd47fe9e3cb26089954020`；新版本的精确提交和部署验收以发布回执为准。

本文只维护该源码基线的边界。宏观依赖和逐层职责见 [架构总览](architecture.md)。2026-09-17 起的特征化、提案及整改过程已移出仓库，只保留在内部 Git 历史中（原历史档案与 `docs/provider-schema-implementation-plan.md`）；其中旧行号、ACCEPT、待办和“当前”均有时点，不是本次运行验收。

## Meshy 官方合同基线（2026-10-22 核对，#329）

核对方式：逐页读取 Meshy 官方文档的 Markdown 版（`https://docs.meshy.ai/api/<页面>.md`，与下表链接同内容）、[Changelog](https://docs.meshy.ai/en/api/changelog) 和 [Pricing](https://docs.meshy.ai/en/api/pricing)；在 dev-test 只读调用一次免费的 `GET /openapi/v1/balance`，确认返回 `balance`、`monthly_credits`、`permanent_credits`、`monthly_resets_at`。没有发起任何付费请求，下表的请求体形状由本机 HTTP 边界测试验证，真实交付仍待合并后的 dev-test 验收。以后复核时按同一张表逐行对照；Schema 中 `application.provider_deprecations[meshy].verified_at` 记同一日期。

| 能力 | 路径（API 版本） | 可用模型（官方） | 已弃用 / 退役 | 2026-08 起新增、与本平台相关 | 官方价格（积分） | 平台 Offering（本次之后） |
|---|---|---|---|---|---|---|
| [Text to 3D](https://docs.meshy.ai/en/api/text-to-3d) | `POST /openapi/v2/text-to-3d`（preview 与 refine 同端点；无 `/openapi` 前缀的 `/v2/...` 于 09-29 弃用，本平台未用） | `meshy-7.1`（= `latest`）、`meshy-6`、`meshy-6-lite`；Smart Topology：`meshy-t2.5`（默认）、`meshy-t2` | `meshy-7`（09-18 弃用，改发 `meshy-7.1`）；`meshy-5`（10-10 退役）；`ultra_mode`、`symmetry_mode`、`is_a_t_pose`、`art_style`、refine 的 `hd_texture`；`model_type: lowpoly`（10-30 退役） | `geometry_resolution`（`standard`/`2k`/`4k`，`4k` 需 7.1，仅 preview）；`model_type: smart-topology` + `target_polycount`（t2.5：100–30000，默认 15000）；`prompt`/`texture_prompt` 800 字 | preview 20（6-lite、t2、t2.5 为 5）；Ultra +5；refine 2K/4K 10、8K 15 | `meshy/meshy-7.1/text-to-3d`：20+5+15=40；新增 `meshy/meshy-t2.5/text-to-3d`：5+10=15；`meshy/meshy-7/text-to-3d` 保留为停用的历史记录 |
| [Image to 3D](https://docs.meshy.ai/en/api/image-to-3d) | `POST /openapi/v1/image-to-3d` | 同上 | 同上，另有 `surface_mode`、`hd_texture` | 同上；`webp` 输入（10-10）；`multi_view_thumbnails`；Smart Topology 下 `image_enhancement`、`remove_lighting` 返回 400 | 7.1：白模 20、2K/4K 30、8K 35，Ultra +5；t2/t2.5：5/15/15/20 | `meshy/meshy-7.1/image-to-3d`：35+5=40，输入加 WebP；参考图改为 1 张，只发 `image_url`（此前最多 4 张，多于 1 张时发送文档没列的 `image_urls`）；新增 `meshy/meshy-t2.5/image-to-3d`：8K 20；多张不同角度的图走 Multi-Image |
| [Multi-Image to 3D](https://docs.meshy.ai/en/api/multi-image-to-3d) | `POST /openapi/v1/multi-image-to-3d` | `meshy-7.1`（= `latest`）、`meshy-6`、`meshy-6-lite`；不支持 t2.x | `meshy-7`、`ultra_mode`、`symmetry_mode`、`is_a_t_pose`、`hd_texture` | `geometry_resolution` 只有 `standard`/`2k`；`texture_image_urls`（1–4 张，只引导纹理，需 7.1，不能与 `texture_image_url`/`texture_prompt` 同发） | 同 Image to 3D 的 7.1 行 | `meshy/meshy-7.1-multiview/image-to-3d` 原生报价不变（20+10+5+5=40）；新增可选 `texture_assets` → `texture_image_urls` |
| [Retexture](https://docs.meshy.ai/en/api/retexture) | `POST /openapi/v1/retexture` | `meshy-7`（= `latest`，**拒收 `meshy-7.1`**）、`meshy-6`、`meshy-6-lite` | `hd_texture`、`meshy-5` | `model_url` 400 MB（URL）/ 100 MB（data URI，10-14）；`image_style_url` 支持 WebP、20 MB；`text_style_prompt` 800 字；`multiview_image_urls`（需显式 `meshy-7`） | 2K/4K 10、8K 15 | `meshy/processing/retexture`：显式 `ai_model: meshy-7`（不再依赖 `latest`）；8K 15；风格图上限 20 MB；去掉文档没列的 `negative_prompt` |
| [Remesh](https://docs.meshy.ai/en/api/remesh) | `POST /openapi/v1/remesh` | 无 `ai_model` | `resize_height`、`resize_longest_side`、`auto_size`、`origin_at`（改用 [Resize](https://docs.meshy.ai/en/api/resize)）；`convert_format_only`（改用 [Convert](https://docs.meshy.ai/en/api/convert)） | `model_url` 400 MB / 100 MB；`target_formats` 可含 `blend` | 5 | 删掉 resize 三项；#330 起不再发 `convert_format_only`，「补齐格式」改用 Convert（下一行），Remesh 只做重拓扑 |
| [Convert](https://docs.meshy.ai/en/api/convert)（2026-10-22 核对，#330） | `POST /openapi/v1/convert`，`GET /openapi/v1/convert/:id` | 无 `ai_model` | — | `input_task_id`（须为 SUCCEEDED 任务，优先）或 `model_url`（GLB、glTF、OBJ、FBX、STL；400 MB / 100 MB）；`target_formats` 必填非空，可选 glb、fbx、obj、usdz、blend、stl、3mf；任务对象只有 `model_urls`（每个所请求格式一个），没有 `texture_urls`、`thumbnail_url`、`expires_at`；任一格式失败则整个任务失败；浮雕任务返回 403 | 1（失败为 0） | 新增 `meshy/processing/convert`「Meshy Convert」：一次固定请求 glb、fbx、obj、usdz、stl、3mf（不请求 blend），按 1 积分报价；原任务类型与 Remesh 相同，本地文件上限 70 MiB；OBJ 的贴图形式文档未写；真实 OBJ 带 `mtllib temp.mtl` 与 UV，上游不提供 MTL 和贴图（#330 note 99526） |
| [Rigging](https://docs.meshy.ai/en/api/rigging) | `POST /openapi/v1/rigging` | 无 `ai_model` | — | `model_url` 仅 GLB，400 MB / 100 MB；模型 ≤320,000 面；`input_task_id` 不列任务类型 | 5 | 本地模型文件上限 70 MiB（见下）；原任务类型见下 |
| [Animation](https://docs.meshy.ai/en/api/animation) | `POST /openapi/v1/animations` | 无 | — | `action_ids`（一次 1–10 个动作合成一份文件，09-02）；`motion_task_id`（套用 Text to Motion，08-26）；`rig_task_id` 只收 Rigging 任务 | 每个动作 3 | 仍只用 `action_id`；来源只收 `meshy/processing/rigging` |
| [Creative Lab](https://docs.meshy.ai/en/api/creative-lab-keychain)（各模板页） | `POST /openapi/creative-lab/<模板>/v1/prototype` 与 `/build`；折叠解压玩具为单步 `/fidget-collapsible/v1`（不在 OpenAPI 规范内） | 无 | — | `export_resolution` 上限 512（10-19）；钥匙扣 `name_text`（09-07）；原型 `remove_background`（08-20）；灯罩改为原型出模型（09-03） | 钥匙扣/冰箱贴/手办/潮玩/积木人偶/像素拼图 6→30；键帽 12→50；灯罩 30→6；折叠解压 6 | 17 个阶段 Offering 的参数与价格已一致，未改 |
| [Text to Image](https://docs.meshy.ai/en/api/text-to-image) / [Image to Image](https://docs.meshy.ai/en/api/image-to-image) | `POST /openapi/v1/text-to-image`、`/image-to-image` | `nano-banana`、`nano-banana-2`、`nano-banana-2-lite`、`nano-banana-pro`、`gpt-image-2`、`gpt-image-2-5-flare`、`gpt-image-2-5-sunburst` | — | `remove_background`；图生图 `input_task_id`（09-14）；GPT 系列 4:3、3:4 | nano-banana-pro 9/9、nano-banana-2 6/6、GPT 9/12 | `nano-banana-pro` 估算由 6 改为 9；其余未改 |
| [Balance](https://docs.meshy.ai/en/api/balance) | `GET /openapi/v1/balance`（免费） | — | — | 新增 `monthly_credits`、`permanent_credits`、`monthly_resets_at`（10-10），`balance` 不变 | 0 | 平台只读 `balance`，未改 |
| [Asset Retention](https://docs.meshy.ai/en/api/asset-retention) | — | — | — | 结果最多保留 3 天；签名 URL 到 `expires_at` 失效；过期后 `input_task_id` 返回 404 | — | `upstream_job_max_age_seconds` 仍为 172800（2 天，留余量），过期后改用已保存文件（#325/#326） |

UV 展开、尺寸调整、打印三端点与 Text to Motion 本次复读后无参数变化，见下文「Meshy 六类扩展步骤」；它们的 `model_url` 同样升到 400 MB / 100 MB，但平台入口是 `static-glb-v1` 的 10 MiB 上传合同（#184 资格边界），不随之放宽。

**模型的选择。** 生成默认 `meshy-7.1`，Offering 写明具体模型；`latest` 不作为对外选项，也不能写进 Offering：官方说明 `latest` 随新版本改指向，而且不同端点含义不同（Retexture 的 `latest` 是 `meshy-7`），冻结报价、冻结执行身份和历史复现都要求请求体里的模型是确定的。`meshy-t2.5` 以独立 Offering 呈现（`model_id` `meshy-t2.5`，「Meshy T2.5 智能拓扑」，非默认、非推荐），而不是 7.1 Offering 上的一个参数：两者可用的字段、面数范围、价格和不兼容组合都不同（Smart Topology 不收 `should_remesh`、`topology: quad`、`geometry_resolution`、`image_enhancement`、`remove_lighting`），做成参数要在一个 Offering 里写一套按模型切换的条件，报价也会变成区间。文生 3D 的 T2.5 精细化不发 `ai_model`（官方精细化没有 `meshy-t2.5` 值，省略时沿用预览的模型），纹理固定 2K：官方只对 `meshy-6`、`meshy-7.1` 写明 4K/8K 精细化，没写 Smart Topology 预览能否用。

**弃用清单与校验（`application.provider_deprecations`）。** Schema 按 Provider 登记官方已弃用或退役的模型（`models`，可按 `provider_options.endpoint` 限定，例如 `meshy-7` 只在三个生成端点弃用）、字段（`parameters`，例如 Remesh 的 resize 字段只在 `remesh` 端点弃用）和取值（`values`，例如 `model_type=lowpoly`），以及核对日期和来源链接。`catalog.Load` 检查该 Provider 的每个 Offering 可能发出的内容：参数的上游字段名（`wire_name` 或名称）与默认值、选项、`wire_values`，以及 `provider_options` 的 `model_parameter`/`model_parameter_value`、`request_template`、`refine_after_preview.set`、`input_paths`；发现任一项就拒绝加载。`history_offerings` 列出只为历史保留的停用 Offering（`meshy/meshy-7/*`：旧 Next 任务的缩略图回填映射与冻结快照的展示仍用它们），它们不受检查，但必须保持停用。`exemptions` 允许某个 Offering 暂时保留某个弃用字段，必须写明跟踪 Issue；当前没有例外：Remesh 的 `convert_format_only` 已在 #330 去掉，弃用登记保留，Catalog 校验仍拒绝任何 Offering 再发它。该清单只在服务端，不进公开 Schema。

**单图与多图。** Meshy 7.1 与 T2.5 的 Image to 3D 只收一张参考图（`reference_assets` 的 `max_items` 为 1，`wire_name` 为 `image_url`）；Adapter 对单图端点按 Schema 声明的字段名只发 `image_url`，请求带多于一张图时在发送前按 `invalid_input` 拒绝（包括此前允许 4 张的旧冻结快照的重试），`image_urls` 只由声明了它的 Multi-Image Offering 发送。工作区没有带 `max_items` 的生成面板上传控件，参考图只从对话输入区附加。`runtime.generation_messages.reference_limit` 与 `reference_limit_one` 由 `ExecutionMessages.ReferenceLimitText(count)` 选取，供任务 API 和能证明普通前缀的服务端结果使用（#329 note 99647）。#80 后输入区按下一轮 3D 执行 Offering 的 `max_items` 使用公开 Schema 的 `reference_capacity` 说明容量和附图数，不预测具体图片，不禁止附图或换模型；任务 API 的 `POST tasks/prepare` 仍冻结前 `max_items` 张并在应答 `reference_note` 写明，其他准备入口（含旧快照重试）仍在发送前拒绝。Agent 先做三视图替换与去重再截取，结果只按冻结的来源事实声明；替换或去重后的中性说明及历史操作边界见下方 #80 段。截取的张数记在 Agent 操作上（`reference_count`/`reference_total`），等待批准的工具结果、批准卡（`reference_note`）与批准后的结果消息（`chat.pending_approvals.messages.succeeded_with_note`，只含 `{note}`，模型名由备注写出）按冻结来源事实使用一致说明；工具说明按回合所选模型的 `max_items` 接上 `reference_capacity_note` 的 `single` 或 `multiple`：`multiple` 只陈述该模型的上限，不推断其他模型，也不再断言多张图是同一物体的多个视角；单图模型不再被说成会把多张图当作多个角度。这是给 Agent 的指导，不改变默认截取与 `preserve` 的拒绝语义；用户明确要求全部图片且超过上限时，Agent 不自行挑选或丢弃，只说明上限并请用户减少图片或改选模型；工具拒绝不等于提交，实际使用张数只以成功或待批准结果的 `reference_note` 为准。工具说明中的模型名用回合冻结的 `TurnOptions.Locale` 本地化（本地化缺失时保留 Schema 默认文案）；`list_offerings`、`reference_image_max` 与 `reference_note` 只供 Agent 查询，不得出现在给用户的回复里，用户可见说明用平实语言描述参考图上限和实际使用张数。这段文案对真实 LLM 的效果以 GitLab #356/#358/#363 的原生验收记录为准，本条不声明线上通过。#356 起，Agent 内置 `list_offerings` 与 MCP `mcp_list_offerings` 共用的投影另有只读标量 `reference_image_max`：只取 `semantic: reference-image`、`asset-array` 参数的第一个正 `max_items`（Rodin 5、多视图 4、Meshy 7.1/T2.5/Tripo 1）；没有正上限时字段缺席，不能读作 1，其他 `asset-array`（如 `texture_assets`）不算参考容量。它不是可调参数，不新增资产输入，不改变可用性过滤、截取/拒绝语义、费用或批准；比较未选模型的上限前须查询该字段，不能从所选模型推断或自行改选。`list_offerings` 描述与 `reference_capacity_note` 的 `single`/`multiple` 写明这条查询规则。Catalog revision `2026-10-24.issue356-agent-wording.1`。实际部署与真实 LLM 验收证据以 GitLab #356/#358/#363 的原生记录为准，本条不声明线上通过。Retexture 不再声明 `negative_prompt`：Agent 的 `retexture_model` 若带这个参数，按处理工具的既有规则拒绝并要求去掉；会话或回合里遗留的参数只在 Offering 声明时才会进入请求，所以被忽略；会话「功能」里固定过它的设置随 Offering 修订号变化而失效，需要重新保存（面板保存时只保留当前声明的参数）。历史任务的冻结快照不改。

**图生 3D 来源事实与发送前容量文案（#80，2026-10-27；源码合同，未部署验收）。** 三视图替换、去重与模型参考图上限的顺序不变：先把三视图视图替换为该组正面，再按首次出现去重，最后截取到 Offering 的 `max_items`。最终来源由 `generationtool.ReferenceSourceFact` 记录，并随 Agent Operation 与同一次 `PreparedInputID`/`PreparedInputHash` 绑定；事实只接受冻结副本或同 owner 派生证明，不保存 URL，也不按当前分组或相同 SHA 补猜。最终只剩一个已验证正面时结果/卡片使用 `chat.three_view.model_source_note`；确实是原始前缀时使用现有 `reference_limit`/`reference_limit_one`；替换或去重后不再是前缀时使用新增 `runtime.generation_messages.reference_selection_note` 中性数量说明；历史图生 3D 操作没有事实时不声明来源。发送前输入区只读公开 `runtime.generation_messages.reference_capacity`，`reference_capacity_note.single` 不再声称第一张会被使用；任务 API 的“前几张”契约不变。批准卡图片输入从冻结输入副本的 `asset_id` 经 `asset_item` 读取，原句柄只在 `asset_id` 精确相同时显示，不把输入副本 `asset_id` 与来源 `task_id` 结合。Provider 请求字段、上传路径、报价、ExecutionGrant、公开 Task 状态机和历史冻结输入均不变。离线验证见 `docs/architecture.md` 的 #80 条目；真实模型、浏览器与部署验收尚未执行。

**同步输入拒绝（#358 后续，2026-10-25；源码合同，未部署验收）。** Tripo（Smart Segmentation、Rig Check、Splat、Multiview、原生处理）与 Meshy（image/model 输入）在 `ValidateInput` 中，对来源本身可操作的拒绝选择 `runtime.generation_messages.input_source_mismatch`、`input_source_unavailable`、`input_source_too_large`、`input_source_media_type`、`input_source_dimensions`、`input_source_invalid`、`input_source_rig_required`、`input_source_pixels`、`input_source_task_expired`、`input_source_task_unsupported` 之一，并把大小、格式、尺寸/像素等值交给 `DetailValues`；`schema.ExecutionMessages.InputRefusal` 只做模板填充，不复制整张消息表。参数拒绝使用公共 `Parameter.Label`（`input_parameter_invalid`），没有 label 时使用 `input_parameters_invalid`；内核 Task 来源拒绝使用 `input_source_task_expired` / `input_source_task_unsupported`，只写公共 `Parameter.Label`，保留过期与不支持的区分。内部参数名、wire 名和原始原因只留在内部 `Cause`，不进入 `SafeDetail` 或 Agent 文本。像素超限使用像素专用文案，不伪造最小宽高。`input_source_rig_required` 只说明受支持的供应商原生 3D 生成任务，不声称所有同供应商任务都可用。声明和协议错误保留 Provider 诊断；参数/标量的内部原因留在 Cause，公开文本使用 Schema 参数文案或中性参数文案，不转成“重新选择来源”的文案；内部缺失 service/owner 声明按协议错误处理。Meshy 模型输入保留原有 `ErrInvalidModelInput`/`ErrUnsupportedModelInput`/`ErrModelInputLimit`，工具结果使用现有 `error.*` Schema 文案；`ErrConflict`（冻结字节变化）仍是 409，不降为 400。`generation.inputValidationError` 保留 Provider 原始诊断作为 `Detail`/`SafeDetail`；`generationtool` 只把 `UserFacing` 的 `ErrUnavailable` 当作输入拒绝，`refusalDetail` 与 `processingOutcome` 不把非 `UserFacing` 的 kernel `Detail`、参数名或 wire 名暴露给 Agent，只接受显式安全的 `SafeDetail`、模型 `error.*` 文案或公开的 invalid-parameters 回退；没有 localizer 时模型输入拒绝回退 `chat.presentation.messages.invalid_parameters`，不回退内部 detail。异步 `Start`/`Resume`/检查点中的不一致不新增“未提交、不扣费”文案，保持原有内部诊断。`ProviderInputRefusalFailure` 保留原始 Provider Failure 元数据与 Cause，`errors.Is` 可追溯原始读取错误；references 的生成参考图超限带 `UserFacing` 与 size/limit 模板。默认 Catalog 声明上述可选 key；使用缺少这些 key 的旧 `CATALOG_FILE` 外部目录时 `InputRefusal` 返回 nil，来源/参数拒绝没有 UserFacing 文案；信任边界仍隐藏非 `UserFacing` 的 kernel `Detail`，工具回退公开的 invalid-parameters 文案；参数拒绝的回退诊断是中性的 `input parameter invalid`；任务来源的过期/不支持区分在旧 Catalog 缺少两条新 key 时不可用；Meshy 模型输入仍使用既有 `error.*`。新增文案不进入公开 Schema，新增字段也不进入公开投影。Catalog revision `2026-10-26.issue363-validator-and-issue358-input-reasons.1`。本机验证覆盖真实 Tripo `ValidateInput` 经 Generation wrapper、Meshy `readImageInput`、实际工具入口的模型输入代码、参数拒绝与不可用原因、中英文模板与值保留、尺寸取整与 `ErrConflict`；未做部署验收。以上证据来自本机测试，不声明 dev-test 或 formal-test 已通过。

**拒绝理由（#329 notes 99868、99888、100006，2026-10-23；源码合同，未部署验收）。** `runtime.generation_messages.reference_limit_exceeded`（`{label}`、`{max}`、`{actual}`）是 preserve 策略下参考图超过 `max_items` 的唯一拒绝文案；`catalog` 只填公开参数 label 与数量，拒绝发生在 PreparedInput、Grant、Task 和 Provider 派发之前，不把超限列表截成上限来让请求通过。`runtime.generation_messages.extra_credits_consent_required` 与 `extra_credits_over_limit` 是原生报价缺少/超出额外积分授权时的文案；有 `cost_policy.messages` 的 Offering 仍以该 Offering 的文案优先。两者产生的 `schema.Error` 标记 `UserFacing` 并带 `DetailTemplate`/`DetailValues`，由普通与兼容 HTTP 按请求语言填写；Agent 工具结果使用同一模板并保持 `NoSubmission`。它们只描述本人输入的公开 label、数量和同意要求，不进入公开 Schema，也不改变价格、上限、批准规则或校验强度；缺文案时仍回退到内部诊断措辞，不冒充公开理由。

轮次内 consent 在核验 owner、session、Grant、PreparedInput hash 和 Offering 修订后按冻结来源事实本地化来源说明；脱离轮次的批准列表也按请求语言生成说明，并为资产输入投影冻结的可选 `media_type`。冻结输入资产使用 owner 鉴权的 `asset_item`，已知非图片不请求图片预览；旧响应没有媒体类型时保留既有行为，Task 预览不携带源模型的媒体类型。无句柄时显示 Schema 参数名或通用附件名及序号，不据相同 SHA 猜测图片来源。以上为源码合同，浏览器、真实 Agent 与部署验收待完成。

**图生 3D 的 Agent 能力说明（#65，2026-10-28；源码合同，待部署验收）。** 本次复核 [Meshy Multi-Image to 3D 官方文档](https://docs.meshy.ai/en/api/multi-image-to-3d)：`POST /openapi/v1/multi-image-to-3d` 接受 1–4 张图片，官方建议分别使用同一物体不同角度的图；`texture_prompt` 只引导纹理，不是描述模型几何的生成提示词。平台当前多图 Offering 没有声明 `semantic: prompt` 参数，Rodin Gen-2.5 图生 3D Offering 则声明了该参数。Agent 工具说明按所选 Offering 的参数语义附加 Schema `reference_capacity_note.no_prompt`，提醒对应图生 3D 步骤不会发送工具文字；独立声明的参数仍可能提供控制，不应推断只有修改参考图才能影响结果，推荐设置须有所选 Offering 的事实支持，而非一般 Provider 知识。`single` 与 `multiple` 指导 Agent 说明生成模型须由用户在工作区选择器改选，聊天确认不允许 Agent 切换；比较费用须有同币种同单位的实际报价，不直接等同不同 Provider 的积分；未提交或拒绝某步不证明对话免费；后续加工建议须以声明的输入兼容事实为依据。多图说明指出拼贴是一张输入图，不预测必然损失或产出，也不把拼贴承诺为分别传入各原图，除非输入契约明确支持等价。工具说明还附带所选 Offering 声明的用户可见设置清单（Schema `reference_capacity_note.settings`，由参数计算，不含隐藏、固定、资产与 prompt 参数），Agent 只能从中推荐设置（源码合同，待部署验收）。Catalog revision `2026-10-29.issue65-declared-settings.1`。Harness 使用冻结 Offering 身份与 revision，不按其他工作区模型推断；回落与未知 Offering 的边界见[架构总览](architecture.md)。这不改上游请求、参考图字节、报价或批准，也不证明真实 LLM 已遵守说明；真实回复仍须部署后验收。

**已确认的取舍（2026-10-22 主会话）。** 不开放 `latest`；T2.5 作为单独启用、非默认的模型；Rigging 沿用 Retexture/Remesh 的原任务列表并排除多图。`texture_image_urls` 的面板入口暂不做；`convert_format_only` 迁到 Convert 已由 #330 完成（见下文）。

**停用模型的回落（`runtime.frontend.retired_generation_models`）。** 公开 Schema 登记 `{provider_id, model_id, model_name, successor_model_id}`，当前是 `meshy-7 → meshy-7.1`。Catalog 校验要求停用模型确有 Offering 且全部停用、`model_name` 与之一致、后继模型对停用模型有过的每种能力都有启用的 Offering。工作区本机保存的模型、或打开会话时按 #318 恢复的模型是停用模型时，换成后继模型，并在输入区下方显示 Schema 文案 `ui.studio.picker.model_retired`，直到用户自己选模型。已冻结的任务、批准和历史结果仍保持原模型，不改写。 服务端同样处理（#329 后续 C）：回合开始时把 `ai_model`/`domain_models` 里的停用模型换成后继并记下切换，生成工具结果以 `model_note`（`chat.presentation.messages.retired_model_fallback`）说明；会话读取把记录的停用模型读成后继并以 `generation_models_retired` 标出；直接用停用模型的 Offering 发起时，Catalog 的 Offering 选择在冻结与上游提交前以 `invalid_input` 拒绝，理由取 `runtime.generation_messages.retired_model`（`UserFacing`）。登记了停用模型时这两条文案必须含 `{from}`、`{to}`。

**加工步骤接受的原任务类型。** Retexture、Remesh 的文档列出 Text to 3D、Image to 3D、Remesh、Retexture 任务作 `input_task_id`；Rigging 不列类型，Animation 只收 Rigging。按 2026-10-22 定下的保守规则，文档没列出的原任务类型（例如 Multi-Image to 3D）改用平台已保存的模型文件，服务收不了文件就明确说做不了，不付费去试。Schema 用 `source_task.task_offerings` 精确列出可以引用原任务的 Offering：Retexture、Remesh、Rigging 为 `meshy-7.1`/`meshy-t2.5`/历史 `meshy-7` 的文生、图生，以及 Remesh、Retexture；Animation 只有 `meshy/processing/rigging`。`generation.BindModelSource` 已按 `task_offerings` 判断（#326 统一的来源规则），面板（`chooseProcessingSource`）、Agent 工具、选择卡与计划共用这一判断，所以四处一致。Rigging 的文档不列类型，这里沿用 Retexture/Remesh 的列表并排除多图：这些类型此前已在 dev-test 真实绑骨通过，多图没有验证。Convert（#330）的文档只写「须为 SUCCEEDED 任务」、浮雕返回 403，同样沿用这份列表并排除多图。

**模型文件的大小上限。** 平台把已保存的模型文件以 data URI 内联发给 Meshy（内网无公网地址）。文档写的是 http(s) URL 400 MB、data URI 100 MB；Base64 会让体积变成 4/3，所以 Retexture、Remesh、Rigging、Convert 的 `source_asset` 与 `inline_model_input.max_bytes` 取 70 MiB（73,400,320 字节），编码后约 97.9 MB，仍在 100 MB 以内（此前为 60 MiB）。

**验证。** Go：`catalog/meshy_upgrade_329_test.go`（只出现当前模型、T2.5 合同、价格、提示词上限、Remesh/Retexture 字段、70 MiB、原任务类型的绑定）、`catalog/provider_deprecations_test.go`（各类弃用写法与例外的拒绝、停用模型回落的校验）、`providers/meshy/meshy_upgrade_329_test.go`（T2.5 文生的预览与精细化请求体、T2.5 图生、多图 `texture_image_urls`、Retexture/Remesh 请求体、默认生成 Offering 只发 `meshy-7.1`/`meshy-t2.5`）。前端：`retired-models.test.ts`、`session-models.test.ts`、`workspace-store.test.ts`。浏览器：`scripts/test-meshy-upgrade-329.mjs`（本机 Mock，Chromium/WebKit × 1366×768/1920×1080：3D 模型选择只列 Meshy 7.1 与 Meshy T2.5、本机选过 Meshy 7 的回落到 Meshy 7.1 并显示提示、改选后提示消失、用过 Meshy 7 的会话在新浏览器里恢复为 Meshy 7.1 并提示、Meshy 7.1 与 T2.5 经公开任务接口和对话生成成功）。Mock 的 `[mock:large_model]` 由约 70 MiB 改为约 80 MiB（`mock.LargeModelBytes`），以保持高于新的 70 MiB 上限，#321/#326 的浏览器脚本里「大模型 Meshy 收不了」的场景不变；`TestMeshy329MockLargeModelStaysAboveMeshyBound` 守住这一点。

**补齐格式改用 Meshy Convert（#330，2026-10-22）。** 呈现方式：Meshy Convert 是普通的「格式转换」Offering（`meshy/processing/convert`，`priority` 90），能以本地文件（data URI，70 MiB 内）或 Meshy 原任务输入的来源都可以用；它与已有转换服务并列出现在加工面板、Agent 的 `convert_model_format` 与会话模型的下一步里，列出即可提交，判断都是 `generation.BindModelSource`。已有默认不变：用户或会话选择优先，没有选择时仍用原来的默认转换服务（面板默认仍是本地转换，Convert 排在最后）。`application.model_delivery.conversions` 中 Remesh 的 `convert_format_only` 路线换成 Convert，排在本地路线之后；旧条目改为 `retired: true`，只用来识别 #330 之前建的补齐计划（已交付的文件照常列出、未完成的计划照常锁定格式），不再规划新的计划，Agent 也不会选它。`convert_model_format` 先试用户点名的服务，再按 Schema 顺序；Convert 一次产出全部六种格式，所请求的格式不作为它的入参。下载包 README 的「转换得到的 OBJ」说明按 `obj_package.converted_offerings` 覆盖 Convert。#330 第二轮：`model_delivery.reasons` 新增 `conversion_failed`（转换失败，区别于从未转换的 `not_converted`）与 `source_expired`（只能用原任务而原任务已过保留期）；`model_delivery.formats[].download_media_type` 声明单个文件下载的 Content-Type（IANA `model/*` 类型，FBX 为 `application/octet-stream`，Catalog 校验可解析）；`model_delivery.conversions` 同时供版本链识别格式转换 Task（并入来源版本，不单独编号）。真实 Convert 的 OBJ 引用了不随结果提供的 `temp.mtl`（dev-test note 99526），README 用 `readme.converted_obj_reference_note` 说明，OBJ 字节不改写；Mock Provider 的 Convert 只产出六个模型文件，OBJ 与真实一致写 `mtllib temp.mtl`（#330 后续）。
验证：Go `catalog/meshy_convert_330_test.go`（文档合同、默认不变、Remesh 不再发 `convert_format_only` 且无例外、退役路线的校验、README 说明）、`providers/meshy/convert_wire_330_test.go`（任务与文件两种请求体、Retrieve 的六个 `model_urls`）、`modeldelivery/meshy_convert_330_test.go`（不规划退役路线、旧计划的文件与锁定、README）、`chat/generationtool/processing_convert_330_test.go`（路由、大小写、点名优先、70 MiB 不列出）、`providers/mock/convert_330_test.go`；前端 `processing-source-convert.test.ts`；浏览器 `scripts/test-meshy-convert-330.mjs`（本机 Mock，Chromium/WebKit × 1366×768/1920×1080：加工面板的候选与默认、Meshy 结果经原任务提交 Convert、格式页按 1 积分补齐、重拓扑选择卡不列 Convert）。以上都是本机证据；真实 Convert 的 OBJ 带 `mtllib temp.mtl` 与 UV，上游不提供 MTL 和贴图（#330 note 99526）。

## Tripo 四视角图片组候选（#191）

当前官方 v3 的 `files`、`generation/image-to-multiview`、`generation/edit-multiview` 与 `tasks/{task_id}` 分别用于私有上传、创建和观察；这两个 Offering 于 2026-10-20 按用户选定的路线 B 在默认 Schema 启用（revision `2026-10-20.1`）：先启用、发布后在开发测试环境做一次公开付费验收，验收不通过即回退为禁用，验收结果见 #191。生成请求把上传得到的 file token 置于 `input`，编辑请求把来源成功 Task 的 Tripo remote ID 置于 `input`，把非空标量 prompt 按 front、left、back、right 的固定顺序组装为 `{view,prompt}` 数组。该窄 wire 转换与派发意图/确认回执属 Adapter，不建立另一个公开 Task 状态机；不确定 POST 不能盲重发。当前官方文档的 flat `output` 示例与 2026-10-17 脱敏实测的 `output.generate_multiview_image` 均作有界解析，不递归搜 URL。实测单 front 编辑只返回 front；四视角编辑返回四张。图片 CDN 曾以 `application/octet-stream` 响应 JPEG，因此按实际图片字节识别 MIME 后仍交给内核物化检查。

Schema 声明四个 `image`/variant 槽位、每个编辑 prompt 的 `present` 费用及对应 output `required_when`，并以输入规则限制至少一个 prompt。Catalog 限定条件只作用于精确必需输出；Generation 在 Task 冻结 Offering 中将未选槽的最低数量设为零，恢复和交付不得重新评估活的 Schema。编辑来源使用原生 `source_task`/`upstream_job` 依赖与 owner/session/Offering 白名单，不信任浏览器提供的上游 ID。`application.capabilities[].image_result_group` 仅提供公开图片组顺序、标签及可选整组来源，不能代替资源交付组或按 UI 补造缺失 Asset。Agent 的两个 2D 工具和手动/自动批准能力由 Schema 声明，通用执行器使用公开 Task ID；运行可用性仍要求注册 Adapter 与密钥。`image_result_group.partial_message` 声明该能力的结果可以只交付部分槽位及其文案；带条件图片输出的 Offering 所属分组必须声明它，整组可选来源不能声明它。本地 Mock Provider 只为完整四视角 `image-to-multiview` 来源实现只读依赖绑定投影（其他 provider 绑定来源仍拒绝），并按冻结 Offering 跳过 `minimum` 为 0 的条件必需输出，只用于本地栈复现这条来源与部分交付合同，不代表上游行为已验收。候选本地验证与剩余真实公开交付见 #191。

## 已有产物的本地恢复边界（#207）

Product Schema 声明 `asset-recovery` 与 `local/existing-assets/recover`，使用闭合 `local-recovery` 协议。连接禁止 URL、鉴权、运行凭据及上游状态/错误映射；Offering 必须零积分、非计量、幂等，不能附加费用、后置工作或远端资产主机。回执身份和摘要是两个必需隐藏参数，不接受图片/模型上传或 Task dependency。来源 Offering ID/修订及每个原槽位的映射位于服务端冻结 provider_options，不建立前端 Provider 表。

当前窄合同仅允许历史 `tripo/tripo-v3.1-full/text-to-3d@2026-09-30.1`，保留其七个必需槽位与一张缩略图：原 model 和 converted.gltf 都映射为各自独立的 GLB 槽位，FBX、3MF 分别使用当前 `binary-fbx-v1`、`3mf-package-v1`，OBJ/STL/USDZ 与缩略图保持对应实际格式。原 GLB 别名不等于新获得 JSON glTF。扩展来源或槽位必须独立修改 Schema 并重新验收，不能从文件名、下载 URL 或当前 Catalog 猜测旧契约。

可信 CLI 以实际冻结来源和预先审定包摘要生成不可变回执；外部 HTTP 不暴露回执创建或文件导入 API。Generation 读取恢复资格时先校验 owner、失败来源/会话、回执、当前 Offering/Adapter 与冻结输入，再查固定幂等身份。已绑定 Task 只有会话、Offering ID 和完整输入均匹配才返回链接；此导航不重新打开暂存字节，不写入新的准备、批准或任务记录。仅明确未绑定时执行完整 Provider 输入/字节校验，准备新步骤、提交与物化检查保持原合同。Generation 接收回执绑定的 PreparedInput、既有批准与唯一幂等身份；本地 Adapter 只检查和读取已验证字节，输出普通私有 Claim，不运行上传、转换或付费私有子作业。公开成功仍由 Generation 在所有必需产物通过物化检查后决定，旧失败记录和历史费用保留。持久化、GC 与公开投影详见 [架构总览的恢复协议](architecture.md#已有产物恢复协议207)。该合同的源码与本机 fixture 不能代替 #207 的真实历史恢复及测试栈验收记录。

## 上游结果重新取回声明（#295）

Provider 连接可以声明私有运行配置 `result_refetch: {mode: "observe", retention_hours}`，表示该连接已受理的上游作业在 Task 因入库失败而终态后，仍可经 Adapter 的 `Observe` 只读取回结果。Catalog 只允许有上游作业的协议声明它（`meshy`、`tripo`、`hyper3d.rodin`、`matting`），拒绝 `image-sync`、本地与对话协议、未知 mode 与越界保留期；声明时 `runtime.generation_messages` 必须带 `refetch_source_unavailable` 与 `refetch_result_unavailable`。`generation_messages.upstream_job_conflict`（可选，缺省时用 `submission_unknown`）是上游返回已被其他任务记录的 Job ID 时的失败文案（#298）。当前 Schema：Meshy `retention_hours: 72`（官方《Asset Retention》：API 结果最多保留 3 天），Tripo、Hyper3D、Matting 不写保留期（官方未写明，以上游回答为准）；三个同步图片连接不声明（结果是一次性的内联字节）。

这不新增 Adapter 方法或 Provider 私有分支：内核用来源冻结 Offering、输入、Job 与 Opaque 调既有 `Observe`（拒绝型派发检查点），再用既有 `OpenAsset` 下载；Adapter 看到的仍是来源 Task/Attempt 身份。重新取回 Task 的冻结 Offering 去掉计费与成功后观察声明，只用于展示与输出契约。逐 Provider 能否重取、依据与不确定性见 `docs/architecture.md`「上游结果重新取回（#295）」。

公开面：Core 路由 `task_refetch`（`GET` 资格、`POST` 发起，参数只有 `taskID`），文案 `ui.task.refetch.*`（中英文）；Agent 控制工具 `recover_task_result`（`task.refetch`、`SESSION_WRITE`）与 `chat.agent.refetch_started_content`、`refetch_unavailable_content`（六个原因必须齐全：`not_applicable`、`not_supported`、`expired`、`provider_unavailable`、`repeated_failure`、`delivered`；`repeated_failure` 必须含 `{cause}`，由公开失败类别文案 `failure.<class>` 填入；`delivered`（#330 第二轮：已有成功的重新取回，资格为 `eligible:false`）必须含 `{task_id}`，填入那次取回的 Task）。重新取回沿用来源冻结的交付声明，不改用当前 Catalog；快照声明有误导致取回以同样的不可重试完整性失败结束时，来源以 `repeated_failure` 关闭，判定规则见 `docs/architecture.md`。

## Tripo Rig Check 边界

`tripo/processing/rig-check` 已启用（`enabled:true`，revision `2026-10-16.1`），仅新增 `rig-check` 能力，并登记 legacy model `tripo-rig-check`。来源参数由 Schema 固定为同 Provider 原生 `upstream_job` Task，并精确允许当前两个 full 文生/图生 Offering；不接上传、URL、Texture/Rig/Retarget 或报告任务。实际 wire 为 v3 的 `animations/rig-check` 与 `tasks/{task_id}`，单次 Attempt、无幂等提交声明，结果未知不能盲重发。现有会话入口的来源校验保持，底层 owner 范围不额外收窄。

Schema 声明零积分估算、按积分计量及唯一必需 `other/report` 槽位：32 KiB、`application/vnd.art-ai.rig-check-report+json`。Adapter 只解析对应 wire 并重建系统 DTO `art-ai.rig-check-report.v1`，不传原始响应/remote ID/token/自由文本；真实 `riggable:false` 保留，推荐类型必须是声明的七类之一；缺失、空值、null 或未知类型均拒绝。公开 Task 仍由 Generation 推进，以既有 Inline Claim 持久化与鉴权资产交付，不新增任意 result 字段。报告不是模型，不作为 Rig/Retarget 来源；检查结论不会自动执行绑骨。独立 Schema 展示块与 typed viewer 只消费本系统报告，打印报告协议保持独立。隔离资格（2026-10-15 单次真实执行 + 2026-10-16 禁用 Provider 复验）之后，2026-10-16 已在 dev-test revision `31b80f2da3164935b4a6b829c0133a47cecd86bb` 完成人形来源的必需报告交付、鉴权下载、隔离与 PC 详情验收，范围见 #186 记录（#186 note 94376）。这不证明 Texture、Rig、Retarget 或上传链可用。

## Tripo Texture / Rig / Retarget 原生处理

2026-10-17 的最新 [Rig 官方合同](https://developers.tripo3d.ai/en/docs/animations-rig) 明列 `v3.0-20260909`：需显式指定，双足质量/速度提升，非双足仍沿 v2.5 的质量/速度。Schema 新增独立 `tripo/processing/rigging-v3`，由冻结 `provider_options.model` 组装 wire；候选固定 `biped`/`tripo`/`glb`、精确原生 Task 来源、35积分及必需 GLB+FBX 骨架，`enabled:false` 且没有 legacy model。已验收的 v2.5 Rig 继续可用，等待新版真实 Task/Grant、转换、骨架字节和鉴权交付资格后才讨论切换。官方新增 `others` 与 `actorcore`/`unity`/`unreal`/`vrm` 并列文件 token/URL 输入（GLB/FBX/OBJ/STL，150MB），但本候选不开放这些未验来源、骨架组合或后处理参数；本地 wire 测试不证明真实上游保真。端点版本、模型版本及已部署旧任务冻结修订各自独立。

Rig 默认 `enabled:true`，登记 legacy model `tripo-rigging`。Rig v3（revision `2026-10-20.1`）、Texture（`2026-10-20.1`）与 Retarget（`2026-10-20.2`）也已 `enabled:true`，分别登记 legacy model `tripo-rigging-v3`、`tripo-texture`、`tripo-animation`（#186 路线 B，2026-10-20 用户决定：先在 Schema 启用，再于 dev-test 精确发布后做公开 Prepare→Grant→Task 验收；未通过的项以回退 MR 改回 `enabled:false`）。v2.5 Rig 保持原样。Texture/Rig 的 `source_task` 只允许两个原生 full 生成 Offering；Retarget 只允许精确 `tripo/processing/rigging-v3`，并以 `task_source_binding:"provider"` 冻结来源配置：Tripo 的纯 binding resolver 从来源冻结 Offering 与已入库必需资产投影 Task/Attempt/remote、Offering ID/修订/摘要及 `model`/`rig_type`/`spec`，Retarget Adapter 在派发许可前与 `provider_options.source_rig`（`v3.0-20260909`、`biped`、`tripo`）逐项比较。v2.5 Rig、生成模型、上传与跨 Provider 来源都在准备阶段拒绝，不产生 Grant 或 POST。上传、URL 参数以及 Texture→Rig Task 来源未声明支持。Rig 的启用依据为同版独立 App 的完整公开 Task/Grant、GLB+FBX 骨架交付和禁用真实 Provider 的严格鉴权复验；单格式 wire 资格不代替公开交付。Texture 的公开 Task 在原生阶段遇上游2018超时（只读复核：上游创建到失败恰20分钟，本地3600秒超时未触发，同参数直接 wire 约2.5分钟成功，费用未知）；Retarget 的1004只出现在 v2.5 Rig 来源，v3.0 Rig 来源直接资格成功。这三项的启用是路线 B 的先启用后验收，不是已通过公开验收的结论；最终部署验收或回退以 #186 精确回执为准。

[Texture](https://developers.tripo3d.ai/en/docs/models-texture) 使用 v3 `models/texture`、模型 `v3.5-20260815`，固定 extreme 8K/PBR/bake/delight；文本写入 `texture_prompt.text`。已启用的旧 Rig 明确指定 `v2.5-20260210`、双足与 GLB，已取得一次真实双足 GLB 与其 FBX 转换的字节及骨架资格，不据此推及 v3.0 或所有来源。[Retarget](https://developers.tripo3d.ai/en/docs/animations-retarget) 使用来源 Rig 的版本，请求不带 `model`；固定 GLB、保留几何、非原地播放，不再发送官方已弃用并忽略的 `bake_animation`。官方动作表只列 v2.5/v1.0 Rig 的预设，未明示 v3.0 兼容，因此动作只开放已在 v3.0 Rig 来源上直接资格成功的 `preset:walk`，其余预设待逐项资格；真实失败及未报告的费用不能改记成功或零费用。所有操作单次 Attempt，无上游幂等承诺；准备与派发重新校验冻结依赖，结果未知不重发。

Texture 声明原生 GLB 及五个必需导出（FBX、OBJ、STL、USDZ、3MF），Rig 声明原生 GLB 与必需 FBX，Rig 的这两个槽位均检查 `skeleton`。固定 `output_formats` 与 [转换端点](https://developers.tripo3d.ai/en/docs/models-convert) 高级参数由 Schema 控制：8192、PNG、bake、blender preset，以及 Rig 保留动画/Texture 不保留动画。按[价目表](https://developers.tripo3d.ai/en/pricing)冻结 Texture 30+50、Rig 25+10；全部费用在首个原生 POST 前纳入 PreparedInput/Grant。上游小数积分与缺失情况仅留私有 checkpoint，估算不等于实际结算。Retarget 只声明必需的带动画 GLB（`animation` 保真），与 Meshy 动画只交付动作文件一致，不追加私有 FBX 导出；补 FBX 须另行决定并重新验收。

原生私有导出不用未获证实的处理 Task ID 转换支持，而使用处理结果 URL；原 GLB 保留。Adapter 通过匿名、禁止代理/重定向的 HTTPS 客户端，限制公开 CDN 解析地址并固定拨号，按150MB实际字节上限冻结 SHA256/大小。原 GLB claim 的私有 Metadata 同时绑定该摘要；最终入库流也校验冻结字节，不能让同 URL 的替换内容成为 READY。刷新不得重置绑定。上游只接 URL，因此本地核验不证明上游实际取到相同字节。

Adapter 将每个私有导出的请求意图、确认身份、状态及积分存入 Opaque；派发回执由 Generation 校验，并与子作业身份、阶段在同次提交中确认。未知派发不重发。基础处理映射0–80，确认导出阶段推进81–98；相同阶段新增积分沿既有 checkpoint CAS 保存。全部必需产物齐备后才交内核物化，失败仍由统一 Task 生命周期收口。无新增公开状态、数据库表或前端 Provider 配置。

Catalog 对 `skeleton`/`animation` 的 FBX 支持仅限精确 variant、唯一 MIME 与 `binary-fbx-v1`，仍执行真实二进制结构与属性检查。转换中的连续 IDAT PNG 沿共享有界流式检查，保留 CRC、顺序和预算；不由 Provider 绕过。Legacy 的字段、别名、multipart 与结果可声明 Offering 适用范围，空范围保留既有语义；原生 Rig 的 GET/SSE 结果使用明确 variant 别名，不按 MIME 猜测。旧付费 POST 仍拒绝，输入 helper 的单测不能当作旧 POST 端到端验收。Harness 仅为显示估价补齐缺失的固定费用选择，不改实际保存输入或批准状态。完整边界见 `docs/architecture.md`，精确资格与部署范围见 #186。

## Tripo 几何候选（#185 第一批）

**启用（2026-10-22）。** 下文 Decimate 三角、Segment、Complete、Quick Cap、Smart Segmentation 图片/模型六个 Offering（OBJ 改按 ZIP 声明后 revision `2026-10-22.185.5`，Quick Cap 重新启用为 `2026-10-22.185.6`）在默认 Schema 为 `enabled:true`、`default:false`，并在 `provider_discovery.legacy_models` 登记同名模型（启用模型与 legacy 登记必须一一对应）；Decimate 四边形（上游 `quad=true` 返回 FBX 而非声明的 GLB，官方页未写四边形输出格式）资格未通过，保持停用；Quick Cap 首次因 3MF 导出遇 429 未完成，原生导出改为对明确的 429 按 Retry-After 有限次重试同一子作业后，在 `f58a8b6b` 上重验通过。Tripo Texture 与七个几何 Offering 的原生 OBJ 导出按实际字节声明为 `application/zip`，格式清单列为已交付的 OBJ。`model_delivery.capabilities` 同时覆盖 `mesh-segmentation` 与 `mesh-completion`。当日重读各端点官方页与价格表：端点、模型版本（Decimate `v3.0`、Segment `v2.0-20260430` Beta、Complete `v1.0-20250506`）、wire 字段、固定值与积分均与 Schema 一致，Adapter 不变；Refine/Stylize 仍只见于迁移表、没有端点页与价格行，继续不登记；bbox/P2 局部编辑有 Mesh Local Edit 端点页但无价格行，同样不登记。默认 Catalog 的启用声明由 `TestGeometryCatalogOfferingsSendOfficialWire` 在 HTTP 边界逐字核对原生与导出请求体。第二阶段隔离公开 Task 资格（费用、全部必需字节、鉴权、PC 浏览器）结果见 `docs/architecture.md` 与 #185；以下各批记录保留当时的停用合同。部件选择传播（`part_names`）未实现且不在 1.0 范围，补全作用于全部部件、减面作用于整个模型，「部件」标签只用于高亮和单独导出。细节见 `docs/architecture.md`「Tripo 几何与分件的启用」。

`tripo/geometry/{decimate-triangle,decimate-quad,segment,complete,quick-cap}` 均保持禁用，不登记 legacy model。`remesh`、`mesh-segmentation`、`mesh-completion` 通过同一原生处理协议执行：Scalar 参数及 wire 名来自冻结 Offering，未知参数在派发许可前拒绝；完整 Offering/输入/来源摘要、未知 POST 停发、确认 ID 后仅 GET、私有格式导出及最终 GLB 字节绑定沿用既有实现。Generation 仍独占公开 Task 生命周期，无新表、公开状态或前端私有配置。

`tripo/geometry/smartsegment-image` 与 `smartsegment-model` 是独立的禁用候选，使用 `provider_options.protocol=smartsegment` 进入专属 wire Adapter：前者以 `image-to-3d` 消费本人 PNG/JPEG/WebP 单图，后者以 `mesh-segmentation` 消费本人已保存或上传的 GLB。两者由同一 PreparedInput/Grant/Task/Attempt 接纳，输入资产的 ID、大小、SHA-256、MIME 和 owner 在准备与上传前核对；PreparedInput 不记录 Task ID，模型处理计划另记录 `source_task_id` 与 `source_asset_id`，已保存 Task 资产仍经 owner 与成功来源校验，直接上传模型不伪造公开来源 Task。Schema 声明 `mesh/smartsegment`、`files`、`tasks/{task_id}`、固定 `seg_type`、`granularity`、`hint` 及模型单位 `transform`；Adapter 只执行协议和私有上传检查点。当前[官方 Files 页](https://developers.tripo3d.ai/en/docs/files)仅列 JPEG/PNG 图片，所以 WebP 来源先在本地无缩放转为 PNG，冻结来源与实际上传的 MIME、长度、SHA-256，超过 20,000,000 字节或项目 16,000,000 解码像素上限则在上传前拒绝；GLB 限 150,000,000 字节。两个字节上限按 Files 文档的 MB 作保守十进制解释，像素上限为项目资源限制；平台通用直接上传与 `static-glb-v1` 原生上传 profile 上限仍为 10,485,760 字节，但已成功 Task 的物化 GLB 经 owner/成功状态投影后走本候选 150,000,000 字节上限及 Adapter 实字节校验。请求体不携带 `model`，Smart Segmentation 的 `input` 只用 file token，不能复用只接受原生上游 task 来源的旧 `nativeProcessingDeclaration`。模型单位矩阵依据官方 identity 示例，仅适用于原始资产坐标，不推断画布坐标。

官方 [Smart Segmentation](https://developers.tripo3d.ai/zh/docs/mesh-smartsegment) 现行 v3 文档（2026-10-17 核查）指定图片85、模型55积分、`seg_type=image|model`、`granularity=coarse|medium|fine`，共享结果示例列出 `mask_url`、`seg_model_url`、`seg_task_id` 与 `model_task_id`。后二者为私有子作业 ID。第二批候选把 PNG mask 与分件 GLB 设为必需交付，并要求精确 `smartsegment_image|smartsegment_model` 任务类型。本账号一次图片和一次模型真实请求分别消耗 85、55 积分，两者均成功并返回这四个字段；四个产物的完整下载字节均通过格式检查，其中模型分件 GLB 为 59,817,996 字节、mask PNG 为 2,152,861 字节。该证据只证明当次上游协议与产物，不证明 Plinth 的公开 Task、Grant、owner 鉴权交付、UI 或 dev-test 部署验收，因此候选仍禁用。UI 的图片 `reference-image` 与模型处理 `source-asset` 路径可消费这些 Schema 语义，但禁用项当前不可执行。此批不提供稳定部件身份、P2/bbox 或局部编辑合同。

**第三批（2026-10-21，revision `2026-10-21.185.1`，仍停用）。** Smart Segmentation 改为与其他几何候选相同的完整交付：分件 GLB 加五个必需导出 FBX/OBJ/STL/USDZ/3MF，导出用 `seg_model_url` 作 `models/convert` 的 URL 输入，参数由 Schema 固定8192/PNG/bake/blender/无动画；`cost` 为85/55加5×10，完整估算135/105，`authorize_extra_credits` 在私有上传前必须为真。mask 是独立 `mask/png` 槽位：图片模式必需；模型模式 `minimum:0`，返回则必须通过检查，缺失不使交付失败（官方仍无模型结果示例）。Adapter 复用原生处理的私有导出 ledger（`nativeConversionLedger`/`nativeConversionRun`），所以导出意图先持久化、未知不重发、分件 GLB 物化按冻结字节校验。旧 2026-10-17 形状只为已冻结快照保留。上游结果 URL 在任务完成24小时后到期，导出必须在同一 Attempt 内完成。2026-10-21 一次模型模式分件（55）与一次 FBX 导出（10）直接资格成功，FBX 保留6个 `tripo_part_*` 名称，三个文件都通过资产交付检查；其余四种导出与公开交付未验收。Refine/Stylize 的当前迁移表列出 v3 端点，但 ComfyUI 2.0.0 已停用对应节点；精确 wire、费用与账号资格尚未核实，继续不开放，不能记为“没有公开端点”。bbox/局部编辑也没有本项目已核实的执行合同。

当前官方合同分别为 [Decimate](https://developers.tripo3d.ai/en/docs/mesh-decimate) `mesh/decimate`/`v3.0`， [Segment](https://developers.tripo3d.ai/en/docs/mesh-segment) `mesh/segment`/`v2.0-20260430`， [Complete](https://developers.tripo3d.ai/en/docs/mesh-complete) `mesh/complete`/`v1.0-20250506`。前两类只取两个精确 full 生成 Offering；Complete 必须是精确 Segment Offering 且能力为 `mesh-segmentation` 的同 Provider 成功原生来源。内核校验 owner/成功状态，Adapter 再验证解析依赖与声明一致。AI补全与快速封闭分别固定操作模式，省略 `part_names` 代表官方全部部件行为，不接受自造部件名、参考图、文件或 URL。

几何候选的 quad 与 completion_mode 区分操作身份，output_formats 及私有高级导出参数限定完整交付，继续作为不可变常量。bake、smart_uv 与 split_by_connectivity 当前只限定待资格协议组合；将其开放为质量建议或可调创作参数之前，须逐端点核实产物、组合与费用合同并补齐理由/冻结回归，不能从生成能力的 advised 政策推导处理接口自动可调。

Complete/Quick Cap 的 revision `2026-10-21.185.2` 在 `source_task` 声明 `same_session:true`，与既有 owner、同 Provider、精确成功 Segment Offering 约束一起冻结；同 owner 异会话也在 Prepare 拒绝，全部部件补全语义不变。

五项全部要求原始 GLB 加 FBX/OBJ/STL/USDZ/3MF。高级导出参数由 Schema 固定8192/PNG/bake/blender preset/无动画，不将 Decimate `quad` 透传给导出。基础35/40/40/50/30加五次高级导出50，在首个原生 POST 前纳入同一个冻结报价和 Grant；总85/90/90/100/80仅为估算。不能缺格式降可选、重试换模式或把 disabled 配置测试当作真实交付。参数边界、来源、费用、逐格式重启等本地回归与真实上游/全部实际字节/鉴权/UI资格分开记录。稳定部件选择与 Smart Segmentation 的公开交付仍待 #185 验收；P2/bbox 未获可执行合同；Refine/Stylize 的端点存在于[官方迁移表](https://developers.tripo3d.ai/en/docs/migration-v2-to-v3)，而[官方 ComfyUI 节点](https://developers.tripo3d.ai/en/docs/comfyui-nodes)已停用，精确合同与账号资格仍待核实，不开放；file-token/import 形式的 Tripo Segment/Decimate 与现有本地模型入口重复，暂不登记；真实交付未验收，见 #185；完整架构见 `docs/architecture.md`。

## 1. 公开任务与私有子作业

`internal/generation` 是公开 Task/Attempt 生命周期的唯一写入者。格式转换作为独立公开 Offering 时，也是普通 Generation Task。

**3D 舞台只读 GLB（2026-10-09 产品约束保留）。** 旧 `tripo/tripo-v3.1/format-conversion` 仍为 `enabled: false`，其六种命名目标与生成时已有导出重叠；不能据目标名 GLTF 推断实际字节一定为 JSON，已知上游结果也可能是 GLB。新入口只覆盖静态 GLB↔自包含 glTF 的免费本地转换，以及原生上传 GLB→STL 的独立收费步骤。舞台仍只预览 `model/gltf-binary`；其他格式以真实 MIME、变体和本地文件供下载，没有 GLB 时显示既有“暂不支持预览”文案。旧 Adapter 与显式启用的契约 fixture 保留。

为履行单个公开步骤而运行的上游上传、转换、导出等私有子作业由协议 Adapter 管理。其 remote ID 和 Opaque 检查点只留服务端。内核存储并回传不透明检查点，接收 Observation 和 ArtifactClaim；HTTP、Agent、Processing Plan 不解析检查点，不另建公开状态机。

同阶段/进度的新私有证据可由 Adapter 显式携带 `Observation.CheckpointBaseSHA256`（不序列化）保存。Generation 只接受当前租约的轮询、运行中的 Task/Attempt、同 Job 与无序号/时间的 Accepted/Running 观察，且不能附带派发、失败、产物或资源副作用；匹配旧 Opaque 的摘要、不同的有效 JSON 与去重键后，沿原有版本和租约 CAS 更新私有检查点及轮询时间。生命周期、进度、上游顺序和公开事件不变；正常存储版本/更新时间仍更新。原有陈旧判定不放宽，Webhook、回退、终态和过期租约不能使用此路径。它只证明本地基线匹配，不能伪称上游时序；Tripo 原生处理用它保留同进度补回的小数积分。

公开投影只能提供安全进度、事件和本地资产。账户原始上游响应、签名 URL、remote ID、密钥、私有检查点不能进入公开 JSON/SSE/日志文案或交付字节。

### 已交付依赖引用（2026-10-13，#169；已发布，真实 Remesh 来源通过）

来源 Adapter 可实现 Generation 定义的纯 `DependencyJobResolver`，从来源冻结 Offering、Job 和 Opaque 返回已交付的 `UpstreamJob`；没有网络或付费副作用。Generation 先校验来源 owner、成功状态、能力与 Provider，再查找来源 Adapter，并校验返回 Job 身份。不支持解析端口的单阶段 Adapter 保持原 Job；缺少来源 Adapter 不能退回猜测身份。

Meshy 的自动 preview/refine 链仍保留 preview 提交身份。对象式或历史布尔 `true` 精细化声明要求检查点具有有效 `refine_task_id`；严格 JSON 解码失败、缺失 ID、待派发意图均拒绝，不回退 preview，也不从最新 Catalog 推断旧任务含义。只有 Adapter 解释该私有结构。内核不改写来源成功终态、Job 或 Opaque，不把 refine ID 加入公开 API。

`TaskDependency.UpstreamJob` 与 PreparedInput 原有 remote/provider 字段冻结的是结果引用；不增加序列化字段或改变历史哈希形状。旧冻结 preview 在 SubmitPrepared 重解析时产生冲突，需要新的准备和确认。已接受的 QUEUED/重试任务在 Start 前经 `TaskAdmissionStore` 读取 owner/task 对应 bound grant 的 PreparedInput，校验哈希并比较依赖。已确认的变化、缺失或哈希损坏以不可重试 `invalid_input` / `attempt.task_dependency_changed` 停止；暂时读取失败、取消或缺少核验端口只返回不可用错误，保留 Task/Grant 供租约恢复后重试。两类失败都不能先派发或申请付费派发许可。SQLite 复用 execution_grants，无迁移；不使用 `json:-` 的内存 Admission 代替持久依据。无 grant 的历史提交保留兼容行为，已启动作业只观察旧检查点，历史终态不改写。mock HTTP 内核/真实 Adapter 回归已覆盖 Remesh、Retexture、Rigging 的 refine 来源请求，以及批准/冻结记录读失败后同一 Task 沿原 Grant 恢复；这些本机证据不能代替部署和真实链验收，后者的实际结果见下文 MR !111 回执。

依赖和批准读取在新 Attempt 写入 `SUBMITTING` 之前完成，避免暂时读失败被恢复逻辑误判为未知付费提交。已经处于 `SUBMITTING` 的不确定提交仍保留原防重发规则。

### Remesh 请求合同修正（2026-10-13，#170；已发布，真实 Remesh 成功）

2026-10-13 获取的 [官方 Remesh API 文档](https://docs.meshy.ai/en/api/remesh) 没有 `ai_model` 字段。本次只移除 Remesh 的 `provider_options.model_parameter`；application revision 为 `102`，该 Offering revision 为 `2026-10-13.1`。路由仍为 `openapi/v1/remesh`，内部模型身份仍为 `meshy-processing`，其他生成 Offering 的模型参数不变。新请求保留 Schema 声明的输入任务/模型 URL、最高质量参数和全部格式；不重写历史 Offering 或已批准输入。

MR !111 已合入并发布 revision `c29c6fcdfa82be552dc28e7a07e438d7db4ff8c6`。真实 Agent 计划的 Remesh、Rigging、Animation 均成功；冻结依赖只读核对证明 Remesh 消费已交付 refine，冻结 Offering 无 `model_parameter`，每步一个 Attempt。原始上游 HTTP 请求未抓取，不用数据库重建冒充抓包。Retexture 的 refine wire 引用、旧批准冲突和暂时读故障恢复仍以本机契约测试为证，本轮未追加这些付费或故障路径。精确任务、产物、浏览器范围和剩余项见 #162、#166。

## 2. Schema 与执行代码

| 归属 | 内容 |
|---|---|
| Product Schema | Provider/Model/Capability/Offering 身份、修订、参数、默认值、约束、输入/输出、费用、UI/文案、路由、运行配置引用、可声明的 wire/状态/错误映射 |
| 协议 Adapter | 上游 DTO 解析、上传握手、协议特有私有子作业及不可约的协议实现；消费目标 Offering 的声明 |
| Generation / 业务服务 | 状态转换、owner、幂等、租约、冻结输入、同意和必需资源门槛等通用算法与不变量 |
| App 装配 | 读取实际运行配置，按协议构造 Adapter，按 AdapterKey 分配 Offering，登记实际可用能力 |

Catalog 只加载、校验、投影声明，不导入执行模块。SkillRun 配方的声明转执行映射由 `internal/adapters/catalogrecipes` 实现其 RecipeResolver 端口。

### 功能选择的 Schema 与执行边界

`application.chat.harness` 声明语义分组、操作能力、模型适用范围、默认启用集合、合法前置路径及工具接线。公开投影仅含人可读分组/操作、能力与前置关系，不含 `tool_name`、`tool_arguments`、基础工具清单、模型上下文模板或运行配置。运行 Offering 发现仍来自原 Catalog/App，不增加 UI Provider 配置表或上游 Adapter 分支。

服务端 resolver 按具体 Offering 参数构造本轮动态工具合同：固定值为 const，Agent 只能填写声明允许的参数，来源和精确 Offering/revision 由服务器注入并校验。通用操作按能力和可接受来源匹配，不绑定左侧生成模型；不可用 Offering 明示不可用，不静默换模型。API 路由、右侧标签及文案仍由 Product Schema 声明。旧兼容 Offering/discovery 保留原结构，新增会话选择路由承担 CAS，不把执行密钥或私有工具接线混入首屏投影。

`Harness` binding 是 Grant/编排执行身份的一部分，不改变 Provider wire 请求或 PreparedInput 内容哈希。每个实际步骤仍经 Generation，Provider 私有子作业、资产完整性交付和终态规则不变。缺输入时可返回的前置方案仅做只读校验，没有独立执行器、accept API 或第二套任务状态机。前置路径可见性和 proposal 共用完整候选链解析及参数准备，从实际来源资产逐段验证 Generation 绑定合同并固定精确目标 Offering/revision，过滤不兼容的跨 Provider 或输出链；缺参仍列待补，参数已齐但规范化失败时继续检查后续候选；这些预测不替代执行时的真实资产检查。持久与恢复机制详见 `docs/architecture.md` 的“会话功能选择与冻结执行身份”；最终部署验收记录见 #181。

### Agent 可选对话来源（#212）

Harness 保留 `task_asset`/`input_asset` 类别，通过公开 `task_source_modes` 与 `default_task_source_mode` 声明对话模型的 Agent/固定子模式；当前默认 Agent。`selection_mode:"agent"` 必须显式保存且不带具体 Task/Asset ID；此模式可用 `parameter:"*"` 授权目标声明的全部模型来源参数，也可保留具体参数窄权限，固定/上传模式拒绝通配符，只有这种新权限可补来源；省略模式的历史具体来源不改 digest、不迁移 Grant。上传仍须固定本人原生资产，不能重标为会话 Task。

resolver 在新 turn 的验证后选择上下文中，只读冻结有界、本人本会话成功并符合目标输入合同的候选 pair、binder 值与指纹；与 Selection binding 分开持久化。工具的 `source` 对象只接受冻结 pair，固定来源完全禁止此参数。Agent 可根据明确语义选择多候选中的一个；无显式选择时当前已验证对象优先，其次完整范围的唯一候选，否则返回 Schema 澄清且不准备付费操作。截断不能证明唯一；Agent 未解析来源不获得前置执行或任意参数权限。

模型只见安全的平台来源/模型名/创建时间/variant 与选中提示，HTTP Harness 投影不透出私有候选指纹，工具和上下文模板仍私有。具体来源继续在 Prepare/Submit 复核，恢复不重选。pending approval 用必需装配的只读来源复核端口读取原 turn scope 后，仍调用原 Generation.SubmitPrepared；缺少原身份失败关闭，Bound 原任务观察不回退。没有新增 Provider 配置表、数据库迁移或公开生命周期。前端按公开模式声明初始化新服务草稿，旧固定来源不迁移；显式 Agent 模式不传具体 ID，固定与上传选择保持原合同和预览。前端不推断 Provider 路由或根据版本列表扩大候选，内部 `*` 不展示；保存不执行，也不扩大普通参数填充权限。源码、本机契约与挂载交互验证不证明发布或真实模型链已验收，完整交付见 #212。

### 上传模型的公开输入合同

`application.runtime.input_assets.model_profile` 是可公开的上传格式和资源预算声明；`application.runtime.configuration` 及 Provider 连接、密钥仍留服务端。参数的 `uploaded_model` 包含 `profile_id`、`require_textures`、`native_only` 和人可读 `requirements`，只允许声明在模型来源 `asset` 参数上。声明该合同的参数若引用已成功 Task 的产物，只接受 `role=model`：Splat、贴图与转换产物不是模型来源，按不支持的模型输入拒绝。Catalog 校验 profile 引用、资产类型、允许扩展/MIME、上传与参数大小限制及纹理条件文案；公开 Schema 与扩展 discovery 保留这份合同，前端不维护第二份 Provider 接受列表。

默认 profile 是 10 MiB 内的自包含静态 GLB。Meshy 重拓扑、换贴图、条件式绑骨及新增本地正向/上传 STL 转换在 `source_asset` 声明入口；绑骨要求模型带纹理并满足上游人物形态与朝向条件，结构检查不能证明这些语义条件。动画、其他格式转换和 Tripo 任务 ID 等未声明入口不能因文件上传成功而启用。`native_only` 只允许真实上传资产，拒绝已有 Task 输出的兼容回退；不改变普通 Task 输入合同。Task 产物的既有输入大小、格式和冻结历史合同不随新上传 profile 改写。 已受理的旧上传任务在调度时按持久 Task 的冻结参数保留无 profile 的旧合同；这条兼容入口不用于新准备或新提交。

上传预览的格式来自同一 profile 的可选 `preview_media_types`（必须是 `media_types` 子集），路径来自可选 `runtime.http.routes.asset_model_preview`。两者缺失即关闭预览；前端不推导后缀规则或回退普通下载。独立预览路由只接原生 owner 上传，由 `modelsource.OpenPreview` 按当前输入合同校验一次读取的实际 GLB 缓冲区与原记录摘要/大小，再交付同一缓冲区。此查看不会产生 Offering 执行、来源 Task 或付费授权，也不替代目标参数快照在 Prepare/Submit 时的重新验证。

新增的 `input_asset` Harness 来源经原生上传身份与实际字节检查后，仍冻结为 Generation 输入资产，再由对应 Adapter 读取实际字节；不伪造来源 Task 或 remote ID，也不增加独立公开生命周期。Meshy 沿用内联协议，Tripo 上传转换的私有文件准备另见下文。上传、选来源和保存均不等于付费执行授权。细节见架构文档“独立模型上传与来源身份”；端点实际调用、冻结输入与产物验收以 #182 为准。

`uploaded_model.profile_revision`、`profile_limits` 是 Catalog 生成的公开快照，源 Schema 不得自行填写；参数和 Provider 的私有 profile 限制保持一致。新上传使用当前全局限制，已冻结的 Generation/Harness 输入按参数快照重新验证实际字节。只有两字段均缺失的旧记录采用原兼容路径，残缺快照不能回退。`require_single_base_color_texture` 是可公开的额外来源条件；为 true 时，所有 primitive（含未引用 mesh）必须共享同一基础色 texture 引用及有效 UV，不能只依赖 `require_textures`。

### Creative Lab 的声明式协议扩展

**2026-10-29（#100）起 17 个 Creative Lab Offering 一律 `enabled:false`，不对用户和 Agent 提供，定义与 wire 映射保留；下文的“全部启用”是 2026-10-22 的历史状态，现行状态见 `docs/architecture.md`「Creative Lab 不对外提供」。**Creative Lab 使用单独声明的 `creative-lab-prototype`（图片域）和 `creative-lab-build`（3D 域）能力。九个模板共 17 个阶段，2026-10-22 分两批经隔离公开链路资格后全部启用（第一批：冰箱贴、手办、潮玩公仔、积木人偶与折叠解压玩具；第二批：钥匙扣、灯罩、像素解压拼图、键帽）；原型与构建是两个公开 Offering，各有冻结输入、报价和 Grant。折叠解压玩具是单阶段。该实现不创建第二套任务生命周期，Provider 只做协议投影、同账户原型读取和产物声明。两项能力都在 `model_delivery.capabilities` 内，成品的格式清单按通用格式匹配项列出已交付文件；键帽的 `bundle/obj_zip` 由 OBJ 格式的匹配项认领（#189）。

`provider_options.input_paths` 是从公开参数名到 2–8 段 JSON 路径的私有映射，不能直接用包含点号的 `wire_name` 代替。资产、任务和标量按既有规则解析后再放入嵌套对象。声明必须引用已存在参数；目标重复、前缀覆盖、扁平别名冲突与原始容器注入均拒绝。`task_type` 可选地绑定观察结果的精确类型与 remote ID，不改变旧快照解析。

来源参数的可选 `same_session` 只适用于 task；这八类构建全部开启，但不改变省略字段的旧来源合同。`task_source_binding:"provider"` 仅允许必需、同 Provider、精确单 Offering 的 upstream-job task 来源。键帽构建使用该选项，私有 `value_field` / `wire_name` 只能引用 Prepared/Grant 已冻结的候选；禁止覆盖公开参数或嵌套根，也禁止从当前 GET 猜选历史未冻结候选。

键帽原型的私有 `result_identity` 指定候选字段、唯一预览字段和 exact output；完成观察将配对证据绑定到该 claim。对应 output 的 `retain_dependency_identity` 必须是受支持 Meshy 声明、精确 required/minimum=1 槽，不兼容前缀、派生变体或 post-success。它只保留版本化 Metadata 与本轮实际 StoredAsset，移除 locator/inline/expiry，和成功状态原子写入现有 claims 表。纯 DependencyBindingResolver 复核 claim、StoredAsset 与 delivered Asset 的完整身份；Generation 在准备/提交/启动时验证会话和资产字节，将有界 canonical binding 纳入私有 Prepared dependency/hash。新字段缺省省略以保持历史字节；旧原型缺 envelope 对新键帽构建 fail closed。其他 `source_result_bindings` 仍用同一 Adapter 连接 GET 验证成功、精确阶段和唯一预览，均在许可前。该机制不是外部 ID 输入或公开凭据，公开发现只暴露约束，不暴露实例候选、remote 或 URL。

参数 `image_input` 用于实际来源图的最小宽高、最大像素，以及 #276 新增的可选 `max_aspect_ratio`（长边/短边上限），只有声明时生效；Catalog 与 Meshy 校验器均拒绝无效冻结合同。#276 起输入资产服务对所有协议按文件头执行该约束（`asset-array` 也可声明，Seedream 用它声明 3600 万像素、宽高比 1/16–16、边长 >14px）；Meshy 协议仍只允许单资产参数声明，因为其 Adapter 另行核对冻结字节。参数级 `max_total_encoded_bytes` 限定同一参数全部图片 base64 编码后的总量（Gemini 整次内联请求受限），单张 `max_bytes` 编码后不得超过它。超限图片的压缩策略在服务端专用的 `runtime.input_assets.image_fitting` 声明，不进入公开投影；规则见 `docs/architecture.md`「参考图的大小、像素与压缩」。键帽的图片字段使用该约束及 20,000,000 字节上限；其他端点只采用各自已核实的声明，不从一个端点外推像素限制。来源校验保留已批准资产的实际摘要与字节一致性，Start 内联同一缓冲区。公开 Schema、扩展 discovery 与 Catalog 的独立副本保留此字段，Provider 私有路径和候选 ID 不进入公开投影。

参数 `alpha_flatten`（#271）声明上游不接受透明像素：`{background_color: "#rrggbb", max_pixels, keep_when}`。`keep_when` 是可选的参数条件列表（格式同 `omit_when`），命中时本次保留 alpha。只允许用在 `image-to-image` Offering 的图片 asset 或 asset-array 参数上，由共用的 `imagewire` 在 wire 投影阶段执行。源图有非不透明像素时，合成到声明色上，以不透明 PNG 发出；源图不透明时原样发送存储字节；无法解码或超过像素上限时，任务失败。冻结资产、摘要和 Grant 不变。未声明的参数保留 alpha。当前 Gemini、Seedream、Meshy nano-banana 和 GPT Image 声明白底；GPT Image 在 `background=transparent` 时保留透明，因为实测不铺底时它把透明区输出成黑底。3D 类保留透明。蒙版参数不能声明。各 Offering 的实测结论记在 #271。扩展 discovery 不投影该字段。#276 的参考图压缩遇到超限的透明图时读取同一声明：未命中 `keep_when` 且不超过 `max_pixels` 才按同色铺底后转 JPEG（与 wire 共用 `imagefit.Flatten`），否则保留透明、只做无损一步，规则见 `docs/architecture.md`「参考图的大小、像素与压缩」。

每阶段价格按当前官方价格页登记；例如钥匙扣、冰箱贴、三类手办与像素拼图为 6 + 30，灯罩 30 + 6，键帽 12 + 50，折叠解压玩具 6。响应示例中的旧 `consumed_credits` 不替代报价。

钥匙扣/冰箱贴的导出分辨率按 2026-10-21 复核的当前官方 [Keychain](https://docs.meshy.ai/api/creative-lab-keychain.md) 与 [Fridge Magnet](https://docs.meshy.ai/api/creative-lab-fridge-magnet.md) 构建合同声明：整数 64–512、默认 512（几何来自 512 像素深度图），候选默认即最高档，Offering revision 升为 `2026-10-21.issue189.2`。早先记录的 64–2048 已不是当前合同；超过 512 的输入在规范化时拒绝、不发上游。2026-10-21 Fridge Magnet 创建回执 HTTP 400（`ExportResolution must be 512 or less`）与此一致；runner 名 `r512` 与请求体摘要仍不能证明当时实际发送的分辨率。钥匙扣 2026-10-16/17 两次以 2048 创建得到 202 后 `service_unavailable`，与分辨率的关系未证实。两类仍为停用候选，待公开链路验收。

**Creative Lab 的 0 默认与文案边界（#363，2026-10-24，Catalog revision `2026-10-24.issue363-copy-contract.1`）。** 2026-10-24 复核的官方 [Keychain](https://docs.meshy.ai/api/creative-lab-keychain.md) 与 [Fridge Magnet](https://docs.meshy.ai/api/creative-lab-fridge-magnet.md) 构建页把 `options.size_mm`（上限 400）、`curve_param`（上限 10）与 `relief_scale`（上限 10）的 0 定义为“使用默认值”。Schema 保留非零默认值与 0–最大值范围，不再用 `input_rules` 拒绝 0，也不声明排他最小值；负数与超最大值仍拒绝。参数说明用用户标签解释 0 值，`Catalog` 加载通用检查用户可见 Offering 文案不得含内部规则字段名或参数 wire 名，`offering_texts` 不保留对应旧译文。Seedream 画面比例与 Tripo 纹理种子说明按此改写。

钥匙扣/冰箱贴构建默认 `glb`、可选 `zip`；每次只返回所选格式的一个 `model_urls` 键。2026-10-17 冰箱贴 ZIP 样本只含 OBJ、MTL、PNG 和 `metrics.json`，因此可直接预览的默认格式改为 GLB。2026-10-21 成功构建对应 GLB 为 17,115,392 字节，glTF 2、两个 mesh/primitive、UV、内嵌 PNG，包围尺寸约 60 × 6.838 × 59.697 mm。下载响应为 `application/octet-stream`，按声明的 `model/gltf-binary` 校验实际字节。该文件资格不证明浏览器或公开 Task/Grant/owner 交付，所有候选仍为 `enabled:false`。

两个格式槽用 select 参数的 `required_when` 精确绑定，Generation 提交时只冻结所选格式为必需；Catalog 只接受 text 参数的 `present` 或 select 等于声明选项的 `equals`。灯罩构建按当前官方默认（2026-10-22 复核 [Lamp](https://docs.meshy.ai/api/creative-lab-lamp.md)）固定 `output.format=stl`：`model_urls.lamp_stl`（灯罩连同带灯座孔的底板，或预设为 `none` 时的单个封闭灯罩）登记为必需槽 `model/lamp_stl`，`model_urls.base_stl`（灯座支架）登记为 `model/base_stl`，以 `required_when: light_source_preset equals bambu_mh001_60mm` 只在有灯座预设时必需；两者均为 `model/stl`。打包 ZIP 与只能放在 ZIP 里的 `result.json`（原 `include_result_json` 参数）不再登记：ZIP 只是同一批 STL 的打包，分件 STL 可以逐个字节校验，而 ZIP 内的条件部件不能仅凭包地址验收。格式包内清单、条件部件与多对象字节仍需实际资格，不能以 ZIP 槽位或 3MF 文件头替代。官方端点和验收记录见 #189。

### Meshy 六类扩展步骤

这些步骤继续使用 `openapi/v1` 的各自端点。按端点核对当前文档，不从 Meshy 生成模型版本推断接口版本。默认 Catalog 开放 UV、尺寸调整、分析、Bambu 打印文件包和文本动作五条已通过资格的路由；打印修复尚未完成当前账号的完整交付资格，仍停用。资格记录不替代最终 main/13333 的新任务与实际下载验收；完整记录见 #184。

| 能力与官方合同 | 必需交付 | 入口限制与基础估算 |
|---|---|---|
| [UV 展开](https://docs.meshy.ai/en/api/uv-unwrap) | GLB、PNG 缩略图 | 静态 GLB，按节点实例不超过 40,000 三角；5 积分 |
| [尺寸调整](https://docs.meshy.ai/en/api/resize) | GLB | 高度或最长边恰一项，本入口最小 1 mm，显式选择底部/中心原点，不启用自动尺寸；1 积分 |
| [可打印性分析](https://docs.meshy.ai/en/api/analyze-printability) | 归一报告 | 静态 GLB；0 积分，仍是独立公开 Task |
| [打印修复](https://docs.meshy.ai/en/api/repair-printability) | 修复 GLB、归一报告 | 尚未通过完整账号交付资格，停用；详情可明确标记上游未报告，不伪造修复结论；10 积分 |
| [多色打印](https://docs.meshy.ai/en/api/multi-color-print) | `print-archive` 角色的 3MF | 固定为已验收的 Bambu、`realistic` 和最高网格深度；所有部件共用同一基础色贴图引用及 UV；颜色数是上限，不保证实际种数；10 积分 |
| [文本动作](https://docs.meshy.ai/en/api/text-to-motion) | `clip` 角色的 FBX | `prime` 固定模式，2–10 秒、0.5 秒步进；独立动作，不是角色模型或模型应用结果；10 积分 |

Bambu 的参数只公开该品牌，`required + fixed + readonly` 沿现有规范化合同将调用方输入固定为 `bambu`，不改变旧固定参数规则。真实资格只验证固定样本的单色来源一致性，未声明其他打印机或任意多色分区已获验收。动作时长仍以数值 wire 值提交；3 秒资格样本为 90 个 30 fps 均匀采样，关键帧跨度 89/30 秒，3 秒覆盖使用末帧保持一帧的项目验收约定，连续运动 3 秒并未证明（`continuous_target_duration_proven=false`）。Provider 的 3000 ms 另列为元数据，不能替代实际 FBX 曲线检查。

前五项输入只走 `source_asset` 静态 GLB 合同和 `model_input_profile` 实际字节检查。`inline_model_input.media_type` 明确 data URI 的端点要求：UV/尺寸使用 `application/octet-stream`，打印三端点使用 `model/gltf-binary`；未声明值的旧 Offering 保留原 wire 行为。Start 校验并内联同一份有界缓冲区。节点实例数是 UV 专用约束，不改变旧 profile 的三角定义预算。数值 select 的 wire 值来自 `WireValues`，不会将动作时长作为 JSON 字符串直接发送。

标量 `result_assets.media_type` 可声明一个与精确 role/variant 输出槽位一致的 MIME；启动与恢复投影都检查标量模式、唯一槽位及精确类型。新 FBX、3MF 分别声明 `model/fbx`、`model/3mf`，避免依赖 URL 后缀或操作系统 MIME 表。未声明值的历史 Offering 继续原推断。通用 CDN 类型可以保留已声明的类型，具体不兼容响应仍被物化拒绝；声明 MIME 不替代资产实际字节检查与端点产物属性验收。

Meshy 多图 Offering 的六个必需模型槽位改为 `model_urls.<format>` 标量投影，分别声明 `model/gltf-binary`、`model/obj`、`model/fbx`、`model/stl`、`model/vnd.usdz+zip`、`model/3mf`；可选 `mtl` 仍是标量投影但不声明 MIME。多图 wire 校验接受历史的 `model_urls` map 投影或精确标量槽位：标量形式必须在启动时覆盖每个必需模型槽位、path 与 variant 一致且声明该槽位的唯一 MIME，并与 map 形式互斥。2026-10-18 的一次真实公开任务中，上游已成功并返回全部七个 `model_urls`、字节有效，但仅靠 URL 后缀推断时 FBX claim 为 `application/octet-stream`、OBJ 响应被 CDN 标为 `application/x-tgif`、USDZ/3MF claim 依赖宿主机 MIME 表而为空，公开 Task 因此在物化阶段以 `integrity_failure` 失败。对该上游作业七个预签名 URL 的只读探测显示：`3mf`、`fbx`、`glb`、`mtl`、`usdz` 返回 `application/octet-stream`，`obj` 返回 `application/x-tgif`，`stl` 返回 `model/stl`。Meshy Adapter 现在只在声明类型为 `model/*` 且响应基础类型为 `application/x-tgif` 时保留声明类型（该标签来自 CDN 自身的 mime.types，不携带模型格式信息）；`application/octet-stream` 仍由内核保留声明，`text/html` 等具体响应继续原样参与契约校验。

文本动作和多色打印的输出还分别声明 `delivery_profile=binary-fbx-v1`、`3mf-package-v1`，绑定精确 variant/MIME 和大小上限。Generation 从已冻结的 Task 输出传递该合同并验证存储返回身份；SQLite 0052 保存 profile，恢复、复用和下载继续检查实际字节。二进制 FBX 使用完整有界解析，节点上限独立为 100,000，并验证 Blender 与 Assimp 两种精确页脚布局，保留属性、深度及共享工作预算；3MF 要求 OPC 起始关系、内容类型和模型根文档一致，并保留全包隔离检查。空 profile 兼容旧资产，未知值或漏存不能降级。该 profile 只证明格式身份与隔离；实际动作、几何、颜色数和可打印性仍属于端点验收，不能用结构通过代替。

**无 profile 的 STL/3MF 槽位（#297；已验收（#297，revision `b1d00405`，note 97469））。** 2026-10-21 的本机隔离验收（#264 note 97403）发现，Meshy 换贴图的可选 STL 槽位接收了上游以 200 返回的 HTML 错误页，可选 3MF 槽位接收了不含模型部件的 ZIP，两者都被标成“已有”，挡住了补齐入口。现在交付检查按冻结输出的 variant 或 MIME 识别 STL/3MF 槽位，不分 Offering、不分必需或可选：STL 须是长度一致的二进制布局或完整的 ASCII `solid/facet/endsolid` 结构，3MF 须是含 `[Content_Types].xml` 和 `model` 根元素模型部件的 ZIP。覆盖范围：Meshy 7.1 文生/图生、Remesh、Retexture、多图的 STL 和 3MF，Tripo 各处理与几何 Offering 的 `stl`，Tripo v3.1 full 的 `converted.stl`、`converted.3mf`，v3.1 按需展开的 `converted.<format>`，上传 GLB→STL、本地恢复和 Creative Lab 的 STL。仍未覆盖的只有已停用的 `tripo/tripo-v3.1/format-conversion`：它的槽位 variant 是 `converted`，格式由参数决定，交付检查拿不到格式身份。

这次没有给这些输出补 `delivery_profile`，原因有三：profile 要求精确 variant 和唯一 MIME，表达不了 `converted.stl` 和按需展开的槽位；Meshy CDN 对 3MF 返回 `application/octet-stream`，旧 Offering 的 claim 类型靠宿主机 MIME 表推断，收窄 `media_types` 会让真实交付失败；结构身份本来就属于交付检查的通用规则，不是产品配置。所以 Catalog 和 revision 都不变。已声明 `3mf-package-v1` 的输出仍走原来更严格的 OPC 检查。

`result_reports` 只声明分析/修复的归一报告投影，原始协议解析留在 Adapter。报告通过既有 Inline Claim 持久化及资产鉴权交付，不将 JSON URL 当报告，也不引入通用任意 JSON 资产例外。Task 仍需所有必需槽位 READY 才能成功。这六个声明都限制一次 Attempt、非幂等提交；发送结果不明不自动重发。Schema 估算和平台累计估算不能写成上游实扣。

### Tripo Image-to-Splat 候选合同

`tripo/image-to-splat` 是独立 `image-to-splat` capability，默认启用并仍受运行配置可用性约束；不是网格 `image-to-3d` 的质量/格式选项。Schema 声明 `generation/image-to-splat`、`files`、`tasks/{task_id}` 相对路径，沿 Tripo 连接的已配置基础地址组合；不能由该端点推断其他端点 API 版本。输入只接受一个本人 PNG/JPEG/WebP 资产，按端点20,000,000 bytes上限和冻结输入校验；可选 `model_seed` 是安全范围内整数。Adapter 在准备与上传前核对 owner、ID、大小、SHA-256和实际图片类型，上传同一缓冲区；创建 wire 只含上传 token `input` 与可选 seed，禁止网格 `model`、格式、重拓扑等字段。

上传与创建各自通过 Generation 的私有持久派发许可。上传回执先冻结 token，后续恢复才派发收费创建；检查点绑定 Task、Attempt、submission key、Offering/修订、输入摘要与来源身份。`upload_dispatching` 或 `create_dispatching` 的结果不明时不重发；每项任务仅一次 Attempt、无自动收费重试。轮询绑定返回 task_id，若上游提供 type 则必须为 `image_to_splat`，唯一产物来自 `output.model_url`；30 Tripo积分是 Schema 估算，实际 consumed credit 只留私有检查点，不能把估算写成实扣。

必需槽位为 `gaussian-splat` / `splat` / `gaussian-splat-32-v1` / `application/vnd.art-ai.gaussian-splat`，32 MiB应用上限。profile 的完整数值、字节与读写约束见 `architecture.md`「资产检查与原子写入」。内核在该槽位真实READY前不能成功，Splat不进入已有网格来源列表。Schema 同时登记公开 discovery 的模型投影；独立图片动作按实际可用性展示，普通网格选择器仍排除该能力。源码和直接上游样本资格均不证明公开产品交付完成；交付验收须覆盖真实 Grant→Task→物化→鉴权下载、PC查看器和精确版本测试栈。

### 失败转换的本地交付完成（#330 第三轮后续，2026-10-22；源码契约；验证范围与交付回执见 #330）

Schema 的 `application.model_delivery.conversions` 是“失败转换可否本地完成”的唯一产品配置。App 在装配 Generation 时把它注入为只读 `DeliveryConversionRule`；Generation 从不在代码中写 Offering/Provider 白名单，也不根据当前 Catalog 猜测历史转换。只有 Schema 识别为转换、B 真实成功且关系精确（refetch binding 或同一步 Plan retry 的 bound Grant/确定性键）时，Generation 才在原失败 Task 上新增 `DELIVERY_COMPLETED` Attempt，并从 B 的不可变 CAS 字节注册新资产。两条路径的失败前提不同：refetch 要求原 Attempt 已观察到上游 `COMPLETED`；Plan retry 可发生在原转换的任何失败阶段（包括本地 Start/运行阶段的 transport 失败、无 claims、未产出）。每个 Grant 用它自己的 PreparedInput 自校验；不同 Schema revision 下两个 PreparedInput 的 hash 不同是允许的。该路径不调用 Provider、Adapter、`Start`/`Observe` 或 Provider 私有子作业，不产生 Grant、计费事实或 usage 行，也不改变普通 `FAILED → SUCCEEDED` 状态机；Provider/Schema 没有新增 wire 字段或产品开关。provenance、事务、历史编号折叠与格式页结果投影见 `docs/architecture.md` 的“失败转换的本地交付完成”。

### 本地转换与上传准备合同

`local-model` 是 App 中闭合的新本地协议，不读取连接凭据或上游 URL；各 Offering 的方向、profile/修订、大小与工作预算均由 Schema 声明，完整 profile 限制在 Catalog 加载时冻结进 Offering。#264（2026-10-20；已验收（#264，revision `3edf8f62`，note 96368））新增第三个方向 `glb-to-gltf-package`：只接受本人成功 Task 已保存的模型，冻结服务端专用的 `generated_model_profile`（`generated-glb-v1`），必须声明 `max_concurrent`，输出 `application/zip` 的 glTF 文件包；上传方向只能冻结上传 `model_profile`，两者不可互换。该方向在 Start 中把文件包流式写入资产存储并返回 `StoredAsset` claim，Generation 仍负责核验与物化；只读校验结论按来源摘要在进程内缓存。`internal/assets/modelconvert` 只改 GLB/glTF 容器表达，保留模型语义和逻辑二进制内容；`internal/providers/localmodel` 通过普通 Observation/ArtifactClaim 交付。mock 模式仍用真实本地转换端口。两个本地 Offering 声明幂等提交，纯计算结果提交前的崩溃可以在原 Attempt 安全重算；Tripo 保持非幂等保护。Generation 的只读 `ProviderInputValidator` 在 Prepare/Submit 前检查实际本地输入，避免把不支持的来源冻结成可执行任务。 `ValidateOfferingInput` 以精确 Offering 修订和 owner 解析真实字节，让格式查询和补齐动作在创建计划前复用此检查；不产生 PreparedInput、Task、Grant 或 Plan，只在单次分析内去重，后续请求重新校验。已存在的计划继续保留其冻结身份和恢复操作。对话工具 `convert_model_format` 经 Schema 字段 `processing.delivery_format_argument` 复用同一顺序与同一检查：按 `model_delivery.conversions` 顺序挑第一个可用、来源可绑定且 `ValidateOfferingInput` 通过的转换，平台生成的 GLB 因此在对话里也先走 glTF 文件包路线（#264 后续；已部署，未验收，见 #264：note 97106 未覆盖对话里的 glTF 选路）。

反向转换的 task 参数使用 `task_source_kind=assets` 和 `task_offerings`，只接受明确来源 Offering 的成功 Task 与唯一兼容 READY 资产。内核负责 owner、实际字节、冻结依赖和重新校验；不需要 remote Job。`upstream_job` 是未声明该字段时的原语义，原来的 Provider 限制与依赖解析保留。这些公开参数进入扩展 discovery，运行配置与私有冻结 options 仍不公开。 Processing/Harness 的后续步骤通过声明级 `BindProjectedModelSource` 预测来源，首步仍绑定真实来源；前端消费相同公开字段。不创建虚构的 Task/Asset，声明预测也不替代每步 Prepare 的真实字节校验。assets 型 Task 参数的投影要求唯一必需且 variant/MIME/大小兼容的模型输出声明，旧 upstream-job 路径继续保留原 Provider/能力语义。

`upstream_job` 型 task 参数可声明 `upstream_job_max_age_seconds`（#325）：上游保留所引用 Job 的时长，从来源 Task 创建时起算；只允许在 `type: task` 且非 `assets` 的参数上，取正整数。过了这个年龄，`generation.BindModelSource` 与前端镜像不再走该参数，Offering 有 `source-asset` 参数时改用已保存的模型文件，否则以 `ErrSourceExpired` 明示原因；内核在准备、提交与派发时同样拒绝该依赖，不调用上游。它是公开 Offering 参数（前端需要提前解释可用性），与连接私有的 `result_refetch.retention_hours` 分开：后者只决定失败后能否只读取回结果。当前 Meshy 的 14 个上游任务引用声明 172800（官方最多保留 3 天，留一天余量）。Meshy Adapter 对带上游任务依赖的创建请求收到 404 时归为封闭失败分类新增的 `source_expired`（不可重试，`failure.source_expired` 为必需文案），处理计划记 `plan_source_expired` 且不提供重试。依据、取舍与验证见 `docs/architecture.md`「上游原任务过期后的来源」。

模型来源选文件时，必需产物先于附带产物（#326，`generation.PrimaryModelAsset`）：Offering `outputs` 里最低数为 0 的前缀槽位（如 Meshy Rigging 的 `basic_animations.`）交付的文件不会被当作结果的主模型，即使上游把它们排在前面；变体名不在 `source_variant_preference` 里的 GLB（如 `rigged_character_glb_url`）按 `glb` 排名。处理计划从第二步起从上一步结果选文件也用这份 `source_variant_preference`（`catalog.ProcessingSourcePreference`，#334），不再按上游排在前面的文件（如 Meshy 绑骨的 FBX）绑定。Provider 的结果字段名只留在资产变体里，Agent 工具结果与说明文字只用 `model_delivery` 的公开格式 ID（`ModelDeliverySchema.FormatOf`）。上游作业的年龄从持有该作业的任务算起：#295 重新取回的结果按其来源任务的创建时间；一个点名某任务的后续步骤以 `source_expired` 失败后，该任务的作业视为已失效（#326）。规则与验证见 `docs/architecture.md`「结果的主模型与做不了的前置步骤」。

上传 GLB→STL 是独立窄 Offering：`native_only=true`、固定 STL、基础估算 5 积分、不开放高级选项、最多一次 Attempt。按当前官方 [文件接口](https://developers.tripo3d.ai/en/docs/files) 上传实际字节后，Adapter 使用私有 token 请求转换；官方 [定价](https://developers.tripo3d.ai/en/pricing) 的基本转换与高级选项计价不同，Schema 估算不冒充预报价或实际扣款。 自由模式转换工具通过 `cost.format_parameter` 与参数映射识别目标格式；如果调用者给出的格式与所选 Offering 的固定值不一致，在 Prepare 前拒绝，不静默替换，也不改选收费入口。

连接 Base URL 含 API 版本路径（默认 `/v3`），三个 Offering 端点分别声明 `files`、`models/convert`、`tasks/{task_id}`。端点只表达相对路径；版本或网关前缀由连接提供。回归直接加载默认 Catalog，再以带 `/v3` 的 Base URL 验证真实请求路径与上传字节，避免手写测试 Offering 掩盖配置拼接错误。Offering 修订变更只用于新准备，旧 Task 的冻结端点及失败证据保留。

上传 GLB→STL 的 `asset_hosts` 显式沿用既有 Tripo 两域 `tripo3d.com`、`tripo3d.ai`，覆盖已观察 CDN 子域；`Adapter.OpenAsset` 只消费冻结声明，不从输出 URL 自动扩白名单。初始 URL 和重定向仍经过统一 HTTPS/域边界检查，域外及伪后缀主机不发起读取。离线默认 Catalog 回归不替代新修订的实际产物验收；既有空白名单任务及其失败、扣款证据保持原状。

Catalog 对已有 URL 映射词汇执行统一的加载期检查：`result_assets`、`response_aliases` 非空或 `response.url_field` 已声明时，`asset_hosts` 至少有一个经现有下载器去空白/边界点号规则处理后非空的值。此约束同样覆盖停用但已声明 URL 映射的 Offering；只有内联报告、Base64 或本地输出时不要求域名。新域名继续只改 Schema，不新增 Provider 名或域名常量表。检查不归一化冻结值，也不替代实际域名正确性、下载隔离和真实交付验收。

Start 的文件上传和 PreparationResumer 的转换提交各自申请一个派发许可。`UpstreamPreparing` 只保存无 Job/零进度的 token-ready 检查点；内核原子消费对应回执后再允许后续提交，真实 Job 落盘后仅轮询。发送结果不明保留 M2 禁止盲重试；旧二进制不支持恢复 token-ready phase，回滚前需核对活动任务并保留原数据。这些检查点不增加公开生命周期。详见 [架构总览](architecture.md#有界模型容器转换与私有上传准备)；真实部署、账号资格和产物证据以 #183 为准。

### 3D 交付声明（`application.model_delivery`）

3D 格式覆盖、补齐与 ZIP 由 `internal/modeldelivery` 消费该 Schema 块，不进入 Adapter，也不是新的 Provider 能力。块内的转换条目只引用已存在的 Offering（`offering_id`、固定输入、`provides`、`source_formats`）；Catalog 加载时校验引用，缺失则拒绝。`model_delivery.capabilities` 也限定来源任务是否进入格式覆盖分析；Meshy UV 展开和尺寸调整任务仅在其成功结果有 READY 模型 GLB 时显示该已有格式，不因此新增转换供给项或改变 Offering 可用性。补齐执行仍是 Processing Plan 中的普通 Offering 步骤，经 Generation 的冻结输入、批准策略与 `BindModelSource`；跨供应商只在目标 Offering 输入合同允许 `source_asset` 或明确的 `assets` Task 来源时发生。该块只在服务端使用，公开 Schema 不含转换参数。细节见 `docs/architecture.md` 的“3D 交付投影与一键 ZIP”。已验收（#166，revision `95ebc30e`）。

**付费转换的共享准入（#330 终审 P1，2026-10-22；源码契约，本机聚焦测试，未部署验收）。** `application.model_delivery.conversions` 同时是“哪些 Task 是付费格式转换、它的精确来源和目标格式是什么”的唯一产品配置：App 把同一份 Schema 注入 Generation 的 `DeliveryConversionRule`，`Module` 用冻结 Offering 与输入计算 `ConversionIntent`，SQLite 在 Task 创建事务内按 owner/session/来源/格式交集拒绝第二个活动转换。来源身份带标签：生成 Task 的输出与 `source_task` 归一化为来源 Task（`generation_assets.task_id`）；上传/派生的 `source_asset` 必须是同一 owner 的 `generation_input_assets`，以稳定 input asset ID 作为身份；两者不可互换，解析不到即 fail closed。待批准的 Agent 审批遇到这一拒绝时保留 pending approval，冲突结束后的 sweep 再提交，不创建 Task、不计费。Generation 与 Repository 都不含 Offering 或 Provider 白名单；新增退役路线或改换转换 Offering 只改 Schema。该检查不改变 Provider wire、Offering availability、Grant 或 Task 状态机，不新增数据库列或迁移。读取失败（active 列表、来源资产归属、规则缺失）一律在付费提交前失败，不静默放行。残余边界：Plan 行尚未提交步骤时没有活动 Task，不占准入；同一来源已成功的转换不占准入，用户显式 Resume 旧 Plan 仍可发起新的付费转换。无 session 的本地转换仍可准备；公开付费提交的 Grant 身份要求 session，因此不存在可执行的无 session 付费转换。实现与测试见 `docs/architecture.md` 的“付费格式转换的共享准入”。

### 本批新增 Schema 声明（2026-10-12；部分已验收（#162，revision `95ebc30e`），含 #160；#263 范围已验收（revision `9d9b687f`）；其余见 #265）

| 声明 | 消费者 | 边界 |
|---|---|---|
| `offerings[].outputs[].fidelity`：`skeleton` / `animation`（#165） | Generation 经 `AssetFidelityChecker` 端口调用本地资产检查器 | 只允许 GLB/glTF 媒体类型；不合格的必需资产使 Task 以不可重试的 `integrity_failure` 结束。Adapter 不参与判断 |
| `application.chat.tools[].processing.plan`（#162）与 `chat.agent_tools` 的 `processing.read` | `generationtool` 建立和读取 Processing Plan | Chat 不提交 Task、不推进步骤；执行仍由 `internal/processing` 经 Generation 完成 |
| `application.chat.tools[].processing.choice` 与 `application.chat.choice`（#321） | 处理工具在两个及以上兼容 Offering、用户未指定时建选择卡；`chat.Service` 校验卡片答复，编排器把所选参数交给 Agent | 只按 Schema 顺序与公开来源合同判断兼容，不按 Provider 分支；卡片不含私有配置；选定后的批准照会话批准方式 |
| `runtime.http.routes.processing_plan_reconcile`（#160） | `processing.Service.Reconcile` | 只读内核、不提交；路由缺失时不注册 Handler |
| `chat.agent.thinking.model_profiles`（2026-10-19，Chat LLM 思考参数） | `internal/app` 按 `llm_model` 小写前缀选 profile，`openai-compatible` Adapter 发送其 `request_fields` 并按 `reasoning_passback` 回传 `reasoning_content` | 只描述对话 LLM 的 wire 字段，不是 Provider Offering；服务端私有、不进公开投影；不得覆盖 model/messages/tools/stream/temperature；未命中 profile 时只发标准字段，`dsml_tool_calls` 决定是否解析 DSML（#253）；详见 `docs/architecture.md`「对话 LLM 的思考参数与思考内容回传」。#322（2026-10-22）起 profile 另声明 `effort_field`/`effort_levels`/`default_effort`（会话可选的官方档位与默认档，三者同时出现、默认档必须在档位内）、`context_window_tokens`（正整数窗口，省略即未声明）与可选 `stream_include_usage`；按前缀取第一个命中的 profile，被更早更短前缀覆盖的前缀被拒；`stream_options` 也成为保留字段；agent 级 `stream_include_usage` 与 `context_window_setting`（运行设置 `llm_context_window_tokens`）。B 线复用同一 profile 定义：管理员开放的档位只能是 `effort_levels` 的子集，profile 按每个请求（每个连接）的模型名选取，`dsml_tool_calls` 也按轮次模型生效 |
| `chat.context_compaction`（2026-10-22，会话上下文压缩，#322） | `internal/chat` 的 `compactHistory`（手动 `POST …/context/compaction` 与轮次开始前的自动压缩），摘要经同一 Agent Adapter 的 `Complete`（`Purpose: summary`）生成 | 不是 Provider Offering；声明开关、自动阈值比例（0.8）与警示比例（0.7）、保留最近轮数（5）、token 无法判断时的条数兜底 `auto_message_threshold`（200，压缩开启时请求不再受 `chat.context.maximum_messages` 截断，note 98983）、超时、摘要长度上限、记录条数上限、总结提示词与各模板、摘要标记、被压缩句柄提示与 Mock 摘要模板；Catalog 校验比例范围、正数、占位符与标记；公开投影只含 `enabled`、两个比例与保留轮数；详见 `docs/architecture.md`「会话思考强度、上下文用量与压缩」 |
| `llm_settings`（2026-10-22，管理员 LLM 设置与对话模型连接，#322 B 线） | `internal/app` 按 `connections` 从运行环境读取连接（默认 `LLM_*`，另由 `LLM_CONNECTIONS` 列出 `<ID>`，各读 `<ID>_BASE_URL`/`_API_KEY`/`_MODEL`/`_CONTEXT_WINDOW_TOKENS`），每连接一个 `openai-compatible` Adapter 经 `agents.ConnectionRouter` 分派；`internal/llmsettings` 管理有版本的设置并按 `probe` 测试连接；`chat.ModelPolicy.Source` 给会话可用模型 | 不是 Provider Offering；连接只读自己的变量、无别名或回落，`default_id`+后缀必须等于 `chat.agent` 的设置变量名（Catalog 校验），额外连接推导的变量名不得与已声明运行设置重名（启动校验）；设置里只有连接 ID、开关、档位（必须是该模型 profile `effort_levels` 的子集）与默认模型；公开投影只含 `routes` 与 `ui`，`connections`、`probe` 与任何连接值不出服务端；会话所选模型由 `runtime.http.routes.chat_session_model` 读写。详见 `docs/architecture.md`「管理员 LLM 设置与会话对话模型」 |
| `runtime.http.lineage_page_size`（#69） | 版本链分页 | 不超过 `maximum_lineage_tasks` 与 `maximum_page_size` |

局部重绘（#161）不新增声明：前端只按已有参数 `semantic`（`prompt`、`reference-image`、`mask`）定位参数名。

### 工作区模型选择与个人 Skill

工作区的模型面板按 `(provider_id, model_id)` 合并 Offering。聚合输入只包括运行时 `available` Offering；每个模型保留完整 Offering 列表，并从每个 Offering 的 `capability` 去重得到能力标签。相同模型名不能代替能力声明。图生图也不能自动解释为局部蒙版编辑；只有目标 Offering 实际声明 mask 参数时，UI 才展示对应标签。执行时仍要使用具体目标 Capability 和 Offering，不能用模型卡的聚合列表替代提交选择。

公开 Product Schema 提供 capability 显示标签、旧 Skill 类别元数据和个人 Skill 创建契约。`chat.skills.creation` 声明同源路由、`max_name_characters` 和 `recommended_size` 默认值；类别由同级 `chat.skills.categories` 声明。当前默认值为 `auto`；Catalog 校验它属于声明 Skill feature 的 Offering 所支持的尺寸。创建界面不提供尺寸选择控件。该元数据不覆盖用户后续生成时的执行参数。Skill 创建 DTO 没有图片上传或资源 URL 字段。

默认 Skill Catalog 为空。旧内建条目只保留 ID、完整内容 fingerprint 与同源缩略图路径，用于启动迁移兼容。默认目录模式下，AgentConfiguration 服务逐条按 fingerprint 比较最新持久配置；仅完整匹配的未修改内建 Skill 被从新版本中剔除。修改或克隆内容保留。迁移通过追加配置 revision 完成，不改写旧版本或已冻结的 Chat checkpoint。外部 `SKILL_CATALOG_FILE` 不运行内建退役迁移。

个人 Skill 是独立数据域，不进入共享 Product Schema 内容或管理员配置版本。认证 Principal 提供 owner ID；客户端不能指定 owner。SQLite migration 0042 将文本 Skill JSON 与 owner ID 存入 `personal_skills`，并按 owner/创建顺序建立索引。读写都按当前 Principal 的 owner 查询。新 Chat turn 将共享配置与本人的 Skill 合并后冻结到 checkpoint；个人 Skill 集合摘要进入本次配置 revision。其他用户无法读取该记录，历史版本与既有 checkpoint 不被改写。

PC 创建界面只填写名称和提示词，选择界面不提供类别筛选或类别标签。创建请求省略 `category`；服务端生成 Skill ID，保存空分类，并按 Schema 校验名称/提示词限制和推荐尺寸。旧请求仍可提供已声明的类别；未知非空类别继续拒绝。`SkillSchema.category` 保留为兼容元数据，未改写历史记录或 checkpoint，也未新增迁移。当前界面从 Schema 读取固定默认推荐尺寸，不展示尺寸选择。该接口不处理图片上传，也没有资源 URL 字段。个人 Skill 不是 Provider Offering，不声明或改变生成能力、运行接线、费用和授权规则。

### 最新 API 与最高质量（2026-10-16 #213 参数策略修订）

最新 API、最新模型和最高质量仍为默认。#213 只修订文生图、图生图、文生 3D、图生 3D 四类生成 Offering 的公开参数：原公开 `readonly` 项保留最高档 `default`、完整枚举、范围、条件、强制值和产物槽位，以 `adjustment_policy: advised` 和 `adjustment_reason` 声明“不建议更改”；不再以 `fixed` 覆盖 Agent 的合法选择。用户设置端口不能调整此类参数；Agent 必须给偏离建议值的参数附理由，仍须通过 Catalog 输入校验。

`fixed` 仍表示服务端强制值，协议内部 `hidden` 项和后处理 Offering 不随本次改变。建议值不是放宽协议约束：`input_rules[].forbidden_when`、`forced_when`、格式列表最少/最多项数、必需 outputs 与全部交付检查不变。比如 Meshy 8K + 四边面仍拒绝，完整导出格式的子集仍拒绝；条件强制形成的有效值不是 Agent 的主动降档，不要求额外理由。Agent 提交新输入时，实际值、有效建议值、理由及公开说明一起冻结，并参与 PreparedInput hash；费用按归一化后的实际输入计算。既有会话批准策略决定本次是否需要人工批准，没有新增一律人工批准要求。

公开 `/api/v1/offerings`、Product Schema 和 extended Provider discovery 保留建议策略/原因；extended 把此类项标为 readonly，旧表单发现仍过滤它们。Agent Offering 投影提供相同建议默认值及合法选项/边界。运行接线、私有配置、密钥和 hidden 参数不公开。没有迁移历史 Task、PreparedInput 或 Grant；旧冻结输入继续恢复，重试沿原输入和原冻结理由重新 Prepare 并取得新 Grant。界面与真实运行验收记录以 #213 为准。

以下表格是 2026-10-09 的历史核对，不代表 #213 的参数权限或本次 Provider 资格证据。

上游格式对照（2026-10-09 按官方文档核对；Meshy Retexture 与 refine 文档页无法打开，Tripo 转换格式来自二手资料；L4 六格式真实调用已验收（#167，revision `c8ee41b`），另见 #182；其余已验收（#264，revision `464d8cd1`，note 97106））：

| Offering | 上游能给的格式 | 本平台请求并保存 |
|---|---|---|
| Meshy 7.1 文生/图生 3D | `model_urls`：glb、fbx、obj、mtl、usdz、stl、3mf（3mf 须显式请求）；`texture_urls`；`thumbnail_url`、`alpha_thumbnail_url`；图生 3D 开重拓扑时另有 `pre_remeshed_glb` | 全部六种模型格式加 mtl、贴图、两种缩略图；`should_remesh` 固定为 false，故没有 `pre_remeshed_glb`；`video_url` 已被上游弃用，不取 |
| Meshy Remesh | glb、fbx、obj、usdz、stl、3mf | 全部六种（2026-10-18 起不再请求 BLEND，#221） |
| Meshy Convert（#330） | `model_urls`：所请求的格式（glb、fbx、obj、usdz、blend、stl、3mf） | 固定六种（不含 blend）；只有模型文件，没有 MTL、贴图和缩略图；OBJ 引用 `temp.mtl`（不提供），UV 完整（dev-test note 99526） |
| Meshy Retexture | glb、fbx、obj、usdz、stl、3mf（按 Schema 现有声明）；`thumbnail_url` | 全部六种；stl、3mf 为后台可选；缩略图（#324 起，`background`、可选；依据 #187 note 98985 实测上游返回 `thumbnail_url`，官方文档页当时无法打开） |
| Meshy Rigging | `rigged_character_fbx_url`、`rigged_character_glb_url`、`basic_animations` | `result` 整棵树展开，全部保存 |
| Meshy Animation | `animation_glb_url`、`animation_fbx_url`；带 `post_process` 时另有 usdz/骨架/改帧率 FBX | glb、fbx 全部保存；`post_process` 一次只能选一种操作，未请求 |
| Tripo v3.1 文生/图生 3D | 基础 GLB（`pbr_model`/`base_model`/`model`）；`rendered_image`、`generated_image`；转换可得 GLTF、USDZ、FBX、OBJ、STL、3MF | GLB 加全部六种转换格式；`rendered_image` 与 `generated_image` 都作为缩略图保存（后者为未声明的额外资产） |
| 图片（GPT、Gemini、即梦、Meshy Nano Banana、去背景） | 每次请求返回一张图片，格式由请求参数或上游决定 | GPT 固定无损 PNG；其他按上游原样保存 |

Meshy Animation 的 `action_id` 选项（#265，2026-10-20）按官方 [Animation Library](https://docs.meshy.ai/en/api/animation-library)（由 [Animation API](https://docs.meshy.ai/en/api/animation) 链接）重建：值是官方 `action_id`，标签是中文说明加括号内的官方名称，后者也是上游交付的剪辑名（例如 4 为 `Attack`，交付 `Armature|Attack|baselayer`）；分组为官方 category。2026-10-20 的官方库共 656 个动作，编号 0–696 不连续，退役编号不列出且会被 Catalog 拒绝。旧表的 37 个标签（如 1 Sitting、4 Waving）与官方编号不符，已全部替换；Agent 的 `list_offerings` 只收到紧凑的 `options_compact`（`4:Attack` 形式，见 `docs/architecture.md`「3D 处理工具」）；Offering revision 与 wire 映射不变，旧任务冻结的编号仍按官方含义执行。官方库会增删动作，`GET /openapi/v1/animations/library` 未接入运行时同步，变更时须重新核对文档并更新 Schema 与 `meshy_animation_actions_test.go`。改版后尚未在测试栈做付费复验。

按 Lex 的隔离定义（2026-10-09）：Provider 层把上游给的文件全部取回，存进任务层缓存，内网直接访问；前端和 API 决定显示什么。字节原样落盘（只算 SHA256，不转码不剥元数据）。尚未保存的只有文字类响应字段（OpenAI `revised_prompt`/`usage`、Gemini 文字部分、即梦 `size`、Meshy 已弃用的 `video_url`）。表中只列启用的 Offering；已停用的 `meshy-7` 与 `tripo-v3.1`（非 full）仍保留旧的可选格式声明，重新启用前须重新核实本节的完整产物与参数合同。

下段 #167 的历史记录限定于 `c8ee41b` 发布时；完整 Agent 链随后由 #162 验收（revision `c29c6fcd` / `95ebc30e`），#169/#170 的发布见上文 MR !111 回执；不将新证据回填到旧版本。

USDZ/USDC 数值弱校验（2026-10-13 用户确认，#167；本机回归通过，已发布至 dev-test revision `c8ee41b1579776848f71b62af4e116d6bdb0268b`，验证范围见下文）：资产层统一接受结构与隔离检查通过的 USDC 原始数值字节，包括 NaN/±Infinity；规则同时适用于 USDZ 成员与独立 USDC，不按供应商或属性名分支。结构、CRC、框架、索引、长度、字节范围认领、地址隔离、本地依赖和资源预算不放宽，其他格式的数值检查不变。没有新增 Schema 配置或运行开关，Adapter 仍只提供上游观察和产物，Generation 仍是公开 Task 的唯一写入者；必需 USDZ 仍须本地 READY，历史失败终态不改写，也不自动重新提交付费操作。通过交付检查不代表数值或渲染正确，不证明上游 NaN 原因或修复。部署后 L0/L1/L2 通过，独立 L4 的 Meshy 与 Tripo 真实任务均成功，各自六种必需格式及附件经两次下载核对哈希与大小；历史 NaN 样本的通过证据仅来自本机最终检查器。该 revision 的回执未覆盖 preview/refine 来源绑定（#169）与重建请求中的未文档化 `ai_model` 注入（#170）；后续 !111 已交付这些修复及一次 Agent 成功链，剩余验收见 #162/#166，不回填早期回执。实现与验证边界见 `docs/architecture.md` §12，详细验收记录见 历史文件 `docs/agent-workspace-implementation-ledger.md`（已移出仓库，见内部 Git 历史 `b43d4018`）。

`fixed` 的执行语义（`internal/schema/parameters.go`、`internal/catalog/input_normalization.go`）：

- 服务端规范化时，fixed 参数总是取 `default`，覆盖调用方传入的值（HTTP、Chat、Agent、Skill 覆盖、旧版兼容 API 都一样）。随后 `forced_when` 仍可覆盖，例如 Tripo 部件分割强制 `geometry_quality=detailed`、关闭纹理。
- `legacy_values`（#267，2026-10-19）：参数可声明已下线取值到替代值的映射，例如 GPT `size` 的 `auto → 2048x2048`、Seedream `size` 的 `2K/4K → 4096x4096`。映射在 `ApplyForcedValues` 入口、`forced_when` 与逐参数校验之前应用，所有规范化、计价、Harness 与参数调整路径一致；键不能是现行选项，替代值须通过该参数校验。它只用于让旧调用和会话里已保存的取值落到明确值，不作为可选项投影，也不改写已冻结的输入。
- 画布比例像素表（#246，2026-10-19）：2D Offering 的 `semantic=output-size` 参数若用比例选项（`16:9` 等，Gemini、Nano Banana），每个比例都必须在 `application.canvas.frontend.artboard.ratio_pixels` 声明像素，Catalog 加载时校验（`validateCanvasRatioCoverage`），新增比例选项须同时补表。带分辨率档位参数的 Offering（Gemini `image_size` 1K/2K/4K）另由 `artboard.ratio_pixel_tiers.<参数名>.<档位>` 给出每档每个比例的像素，校验要求每个档位选项都覆盖全部比例；数值取自 2026-10-19 核对的 Gemini 官方图片生成文档。这张表只决定画布尺寸与尺寸冲突提示，不进入上游请求；发给上游的仍是该 Offering 声明的比例字段。
- 画布轮次的输出尺寸（#272，2026-10-20）：以画布为来源的轮次里，`canvas_source: bind` 的工具把 Offering 中用户可选的 `semantic=output-size` 参数设为画板尺寸（服务端从快照资产读出宽高）；参数接受该宽×高时直接使用，否则取长宽比、其次面积最接近的已声明选项或按 `image_size.multiple` 取整的尺寸，比例选项取最接近的比例。`fixed`/隐藏的尺寸参数不受影响，之后照常应用 `legacy_values` 与 `forced_when`。像素约束检查由 `schema.ImageSizeConstraint.Check` 统一提供，输入规范化与此规则共用，所以选出的尺寸不会在规范化时被拒。新增比例或像素选项无需改代码；工具结果的说明文案在 `chat.context.canvas_output_size_template` / `canvas_output_size_nearest_template`，结果键由工具的 `result_fields.size_note` 声明。
- Provider 连接的环境变量契约（#252，2026-10-19）：连接引用的 `base_url_setting` / `api_key_setting` 首先读取设置名的大写形式（`<ID>_BASE_URL`、`<ID>_API_KEY`），其余来源必须标 `deprecated`，API key 的别名只能是同一 Provider 前缀的旧名，不能借用其他 Provider 或 Agent 的 key；Catalog 校验强制。用到弃用别名时 `Config.DeprecatedEnvironment` 记录 `别名 -> 规范名`，启动时记 WARN。对外清单见 `docs/provider-configuration.md`，`internal/app/provider_environment_doc_test.go` 比对文档、`.env.example` 与 Schema。
- Catalog 加载时校验：声明 fixed 的参数必须有 `default` 且 `surface` 为 `hidden` 或 `readonly`；`readonly` 必须有 `fixed` 或合法的 `adjustment_policy: advised`；后者必须带默认值和非空公开原因，不能同时声明 fixed 或 hidden。
- 固定不删声明（2026-10-10 审核返工）：参数的 `options`、`image_size` 像素约束、`presentation.visible_when`/`disabled_when`/`step` 与 `forced_when` 都按 22d2e1d 基线保留，固定值只用 `fixed`、`default` 及明确的 `hidden`/`readonly` 展示声明表达。确需改值的只有组合本身，例如 GPT 非透明背景的强制格式由 webp 改为 png。`fixed_parameter_test.go` 验证去掉 Seedream 尺寸的低档 `forced_when` 后回到原选择范围、Meshy 8K + 四边面仍被 `input_rules` 拒绝。
- 旧版兼容 API（`provider_discovery.legacy_models`）的默认值投影同样保留：Meshy 7.1 图生 3D `target_polycount` 默认 30000，Tripo v3.1 full 的 `texture_quality`、`geometry_quality` 默认 `standard`。参数 hidden 时不进入投影，这些条目不生效；去掉 `fixed` 并显示控件后自动恢复（`TestLegacyProjectionsOfFixedParametersArePreserved`）。重贴图原有的 `resolution` 默认 `1024` 投影未恢复：`resolution` 不是 Meshy 参数（实际参数是 `texture_resolution`，取值 2k/4k/8k），Catalog 校验会以“引用未知参数”拒绝加载。
- hidden 参数不进任何 discovery 投影和任务详情；处理类 Offering 的 Agent 输入视图也跳过 fixed 参数。
- 已存任务输入重复规范化结果不变（幂等）。
- 前端只认 Schema 的 `fixed`（`src/lib/agent-offerings.ts` 的 `fixedParameterValue`）。只有一个选项的 select、必须全选的列表不再被前端自动当作固定值；需要固定的参数一律在 Schema 标 `fixed`（例如即梦 `response_format`、`prompt_optimization`）。
- Agent 工具的参数表不因固定而删减：`retexture_model` 保留 `resolution`（映射到 Offering 的 `texture_resolution`）和 `enable_pbr`，被 Offering 固定的参数，Agent 传什么都按 `default` 规范化。`process_model` 原有的 `convert_format_only` 已在 #330 删除（Remesh 不再声明它，补齐格式改走 Meshy Convert）。

### 二选一布尔参数（2026-10-10）

带 `options` 的 `boolean` 参数表示二选一的模式，例如 Tripo 的“生成模式：常规 / 分部件”（`generate_parts`）。
Catalog 加载时校验：`options` 必须恰为 `"false"` 与 `"true"` 两项，且每项有 `label`。
提交值仍是真实布尔值，不是字符串；`options` 只管显示。
通用参数渲染器（`schema-parameters.tsx` 的 `isBooleanChoice`）把它画成两个单选标签，没有 `options` 的布尔仍是复选框。
任务详情（`task-detail.ts`）对声明了 `options` 的布尔显示选项标签，如“分部件”，不显示“是/否”。
说明和提示由 `description` 与 `presentation.notices` 声明，前端不出现参数名或 Provider 名。
旧版兼容 API 的参数标签（`provider_discovery.labels`）同步为“生成模式”。
已验收（#264，revision `464d8cd1`，note 97106）。

2026-10-09 现状（官方文档核对；真实上游调用：GPT Image 已验收（#166，revision `b43d4018`），Meshy 7.1 已验收（#166，revision `98c4c998`），Tripo 已验收（#218，revision `80f8b4d8`）；其余已验收（#264，revision `464d8cd1`，note 97106），Hyper3D 见 #188）：

| Provider / Offering | 模型与路由 | 后端固定的最高质量 |
|---|---|---|
| OpenAI GPT Image | `gpt-image-2.5-sunburst`（官方建议的 2.5 高精度档） | `quality=max`，`output_format=png`（无损，不再输出 WebP）。按背景强制格式的 `forced_when` 保留：透明背景仍为 png，非透明背景的值由 webp 改为 png；`output_compression` 的 `step`、`disabled_when` 保留 |
| Google Gemini 图片 | `gemini-3-pro-image`（Nano Banana Pro，官方定位最高质量） | `image_size=4K` |
| 即梦 Seedream | `doubao-seedream-4-5-251128` | `size` 可见，用户选画面比例，默认 `4096x4096`（#267 起只发明确宽×高，旧 `2K`/`4K` 经 `legacy_values` 落到 `4096x4096`）。每个比例都按最高分辨率出图：低档选项由 `forced_when` 换成同比例最高档（`2048x2048`→`4096x4096`、`2560x1440`→`5504x3040`、`1440x2560`→`3040x5504`、`2304x1728`→`4704x3520`、`1728x2304`→`3520x4704`，2026-10-10 Lex 选“可选比例”）。选项与 `min_pixels=3686400` 原样保留，删掉这几条 `forced_when` 即回到原选择范围。Schema 仍逐项声明全部选项；参数面板（`agent-offerings.ts` 的 `selectableOptions`，2026-10-20）不列出被本参数 `forced_when` 无条件替换掉的低档，所以每个比例只出现最高档那一项，与画布尺寸提示一致，旧会话里保存的低档值经 `forced_when` 显示为同比例最高档；自定义宽x高（API/Agent）仍按像素约束校验，不自动升档 |
| Meshy 文生图/图生图 | `nano-banana-pro`、`nano-banana-2` 都启用（2026-10-10 恢复 `nano-banana-2`：上游没有质量参数，停用它是删掉一个模型选项，不是改参数组合；旧版兼容 API 的 `legacy_models` 和 `legacy_constraint_option_labels` 随之恢复，Catalog 校验要求这两处与启用模型一一对应） | 上游无质量参数 |
| Meshy 3D | `meshy-7.1`；文生 3D `/openapi/v2/text-to-3d`，图生 3D `/openapi/v1/image-to-3d`（另有非默认的 `meshy-t2.5` 智能拓扑，按面数出三角网格，见上文「Meshy 官方合同基线」） | `geometry_resolution=4k`，`texture_resolution=8k`，`topology=triangle`（Meshy 的 8K 纹理不支持四边面，dev-test 实测返回 400；该约束以 `input_rules` 声明），`enable_pbr=true`，`should_remesh=false`（保留原始高精度网格；需要拓扑控制时走独立的 Remesh），图生 3D 另固定 `should_texture=true`、`image_enhancement=true`；文生 3D 固定 `target_polycount=300000` 和 `mode=preview` 并自动精细化（见下）；重拓扑 Offering 保留面数与拓扑选择，那是它的用途 |
| Meshy Retexture | `meshy-7`（Retexture 的最新模型；2026-10-22 起显式发送，不再依赖 `latest`，#329） | `texture_resolution=8k`（此前误用不存在的 `resolution` 字段，上游实际按默认 2K 输出），`enable_pbr=true` |
| Tripo H 3D | 几何 `v3.1-20260211`、独立纹理 `v3.5-20260815`（2026-10-17 官方当前版本） | `texture_quality=extreme`（最高纹理档；官方标称 8K，2026-10-19 在 dev-test 实测原生 GLB 及 OBJ/USDZ 导出贴图均为 4096²，产品文案不再承诺 8K，#264）、`geometry_quality=detailed`（本端点最高且合法）、`face_limit=2000000`、`texture=true`、`pbr=true`、`export_uv=true`、`quad=false`、`smart_low_poly=false`；图生 3D 另固定 `enable_image_autofix=true` |
| Hyper3D Rodin（2026-10-20 按路线 B 启用，同日开发测试环境公开验收通过，见 #188） | Gen-2.5 | `tier=Gen-2.5-Extreme-High`、`texture_mode=extreme-high`（6K 贴图，不加 HighPack）、PBR、Raw/2,000,000 面 |

**Tripo H 文生/图生合同校正（#218，2026-10-18 完成 dev-test 测试栈验收）。** [官方文生 H](https://developers.tripo3d.ai/en/docs/generation-text-to-model/standard)、[图生 H](https://developers.tripo3d.ai/en/docs/generation-image-to-model/standard) 的 `geometry_quality` 枚举只有 `standard`/`detailed`；旧 Catalog 默认 `extreme` 被 Adapter 原样发送，但没有证据证明旧请求实际被拒绝或造成某次历史故障。纹理模型需用独立 `texture_version` 明确 pin 到 `v3.5-20260815`，否则 v3.x 几何默认纹理模型是 `v3.0-20250812`。四个 full/legacy Offering 改用上述新修订；最高默认几何为 `detailed`，`texture_quality=extreme` 仍为最高纹理档（实际分辨率见上表，#264）。`texture=false` 会同步强制 `pbr=false`，分部件继续强制关闭纹理/PBR/四边面并使用详细几何。合法 advised 降档仍可由 Agent 带理由选择，冻结输入和报价同时反映有效值。

[官方 H 价格](https://developers.tripo3d.ai/en/pricing) 当日列文生/图生无纹理 10/20、标准纹理 20/30、HD 纹理 +10、8K 纹理 +20、HD 几何 +20、Quad +5、Smart Low-poly +10、Generate Parts +20；基本格式转换每项 +5。新 Schema `native_pricing` 以无纹理价为基础，`when_all` 将纹理是否启用与纹理质量合取，`selection` 对冻结的六种格式逐项计价（full 必须六项，disabled legacy 可在已声明范围内选子集）；默认完整六格式报价为文生 90、图生 100 Tripo 积分。`max_authorized_extra_amount=125` 是按所有已声明加项求和的保守上限，并非能同时触发的默认金额。Catalog 规范化、私有转换规划、公开 Offering 报价和获准 Grant 使用同一冻结声明；旧 `Cost` 快照仍按原路径继续，不能把新价追溯写入历史 Task。兼容 provider discovery 只为 `legacy_models.native_pricing:true` 明确登记的 Tripo full 模型输出按 capability 的 `nativeQuotes`，不输出虚假的整数零价；其他原生候选仍不进入兼容模型列表。本机合同测试覆盖两种 POST wire、六转换选择、报价和 Grant 修订。2026-10-18 在测试栈 revision `80f8b4d8`、Offering revision `2026-10-17.218.1` 完成真实公开验收：文生冻结报价 90、图生 100 Tripo 积分，均经人工 Grant 批准；上游主作业 `credits_consumed` 分别为 60/70，六个格式转换各 5，合计与冻结报价一致；两 Task 均 SUCCEEDED，`model` 和六个必需转换资产全部通过鉴权下载、实际字节大小、SHA-256 与格式头检查。

**Meshy 文生 3D 自动精细化（2026-10-13 已核真实冻结输入、preview/refine 检查点与产物；非原始上游 HTTP 抓包）。** Meshy 文生 3D 的预览只产出白模，带贴图的模型需要第二个 refine 任务。为满足“永远最高质量、不让用户选择”，`meshy/meshy-7.1/text-to-3d` 这样声明：

- `mode` 为 `fixed: true`、hidden，默认 `preview`。调用方传 `refine` 也会被规范化为 `preview`。`options` 仍保留 `refine`，因为 `texture_resolution` 的 `omit_when` 引用它，而且精细化阶段由 Adapter 用同一套 Schema 投影生成请求体。
- `preview_task_id` 在 `mode=preview` 时省略：调用方经 `/api/v1/tasks` 传入 `mode=refine` 加 `preview_task_id` 时，`mode` 被规范化为 `preview`，传入的预览任务不进入上游请求，结果是一个新的完整任务（预览加精细化）。旧 `/api/meshy/*` 写入路由不是准入路径（返回 403，见 `legacy_admission_regression_test.go`），没有单独的 refine 兼容入口。
- `provider_options.refine_after_preview` 打开 Adapter 的两阶段私有子作业（`internal/providers/meshy/refine.go`）。精细化请求的覆盖值和预览任务引用都由它声明：`{"set": {"mode": "refine"}, "source_parameter": "preview_task_id"}`，Adapter 不写死 Meshy 参数名。旧任务快照里的布尔形式 `true` 仍按同样含义读取。
- `estimated_credits` 为 40：预览 20 + 4K 几何 5 + 8K 精细化 15。

一个公开 Task 内的执行顺序：

1. `Start` 照常提交预览，公开 Attempt 的上游作业就是这个预览任务。
2. `Observe` 轮询预览。进度按 0–50 显示。
3. 预览成功后，Adapter 先经私有派发许可把意图写入 Opaque 检查点，再提交 refine（`mode=refine`、`preview_task_id`、`texture_resolution=8k`、`enable_pbr=true`，不带 `geometry_resolution`）。返回的 refine remote ID 只存在 Opaque 检查点里。
4. 之后 `Observe` 轮询 refine，进度按 50–100 显示。refine 成功时的产物就是公开任务的产物；refine 失败即公开任务失败。
5. 如果检查点停在“已写意图、未记录 refine ID”（付费 POST 结果不明），Adapter 让任务失败，绝不重发付费请求。
6. 成功后观察期（`PostSuccess`）不会补交 refine。

该 Offering 的 Meshy webhook 返回 `generation.ErrWebhookNotApplicable`。内核确认收到并丢弃，不改任务状态，因为预览 webhook 不能完成两阶段任务，也不带私有检查点。两阶段任务只由轮询推进。删除上游任务时只删公开 Attempt 记录的预览任务；refine 任务留在 Meshy 账户。

未固定、留给用户的处理参数：Remesh 的拓扑与面数（这是该处理的目的）、去背景的透明物体模式。旧六格式 Tripo Offering 保持停用；新增上传转换固定 STL，本地转换方向也固定，不提供质量降档选项。

待定：Seedream 5.0 Pro（`doubao-seedream-5-0-pro-260628`）已发布，但第三方资料显示它只有 1K/2K 档（约 4.2MP 上限），低于 4.5 的 4K（16.7MP）；且内网 llms 网关是否支持该模型名尚未核实。升级前需在 dev-test 做网关和付费探针。Gemini 官方另有更新的 `gemini-nano-banana-2.1`，属于 Flash 档，不替换 Pro。

## 3. Provider 插槽

生成端口保持 `Start / Observe / OpenAsset`，上游返回归一为 Observation。同步和异步协议使用同一公开任务内核。账户查询、聊天 LLM、Provider Webhook 有各自端口与 Schema 声明，不能塞进生成接口形成万能 Provider。

同协议接入以 Schema 新连接、Offering、映射及运行凭据完成，复用已实现协议 Adapter。新增不可约 wire 协议才增加隔离 Adapter、支持协议注册与契约测试。`protocol` 是已编译支持边界；`provider_id` 是供应商身份；`model_id` 是模型身份；`adapter_key` 选择已装配的端口，不能混用。

缺少凭据、能力前置条件或接线时明示不可用。显式选择无效时返回错误，不静默换模型。不同 AdapterKey 的配置隔离；Tripo 的历史快照路由回填也只使用所属连接声明。

### 测试采集与离线协议回放

App 的私有 `assemblyOverrides.httpTransport` 将连接身份传给测试装饰器，覆盖已装配的远程 Provider 与 Chat LLM；账户余额 reader 不经过该接缝。普通运行入口不启用采集，也不记录原始请求/响应载荷。Tripo 的 `Config.NativeSourceTransport` 只用于装饰独立匿名来源 transport，默认安全客户端先构造，钩子返回 nil 时构造失败，不能降级为普通联网客户端。

账户余额的 `provider_accounts` 目前登记 Meshy、Tripo、Hyper3D（#91，`/check_balance`，`balance`，`hyper3d.credit`），三者共用 `http-json` 余额读取器，新增账户只改 Schema；未做真实 Rodin 调用与部署验收。

测试二进制的 `TestWireCaptureServer` 将清单选定连接直接接到 `Recorder.Wrap(connection, base)`，不替换原 transport、不修改 `http.DefaultTransport`，也不改变 Provider/Generation 的批准、重试和终态规则。入口只接受 live Provider/Agent 配置，拒绝未知、重复、无凭据、非 HTTPS、带 query/userinfo/fragment 或未实际装配的选定连接；Tripo 原生来源须单独列出 `<connectionID>.native-source`。清单和 Catalog 字节在隔离目录冻结，App revision 为编译时链接的完整 SHA，Catalog revision 为实际字节 SHA-256，API/请求模型/返回模型/上游 build 无单独证据时保持 `unknown`，不从模型名或 URL 猜版本。采集时间为本轮启动时间，端点和协议由 recorder 逐交互填充。

注册策略必须明确字段、允许值和终止别名，通过离线预检仍不等于隐私审查或真实端点全覆盖。未知动态字段、inline 图片、未注册的 query key、截断和其他不支持的内容使完整导出失败，不能丢弃这些交互后声称完整；已注册 query key 只保留键名，值不进入样本。支持的二进制可流式写入 `PrivateStore`，导出时须显式替换为固定合成 recipe 并标记 derived/substituted；`WriteCandidate` 仍拒绝直接依赖私有字节的公开导出。正常关闭后逐项核对完整性和选定连接覆盖，失败只影响采集资格，不改变业务结果。当前仅有离线实现验证，真实 corpus、浏览器与付费验收仍以 #365 原生记录为准。

`internal/testutil/wirefixture` 的公开候选必须通过显式字段/文本/数字/终止别名策略及隐私复核。完整采集须在停止新请求后通过 `Recorder.AssertComplete()`，私有证据不可读或描述不符同样失败；导出重新检查元数据与实际端点/协议一致、序号唯一、正文唯一编码及安全值，并递归复制头、正文、媒体与 multipart 描述快照。实际 HTTP 交互的脱敏副本、官方示例、派生材料和合成负控分别标注来源；SSE 仅支持 delta 的 `role`、`content`、`reasoning_content`、`tool_calls`，分片合并后的保存表示明确标记规范化，未知版本保留未知。

字段登记先于凭据脱敏，未知敏感字段不能靠 redaction 绕过 `Policy.Keys`。测试包另有显式 opt-in 的 `NewDeferredRecorder`，在内存中用整轮字节与交互数预算限制在途和 pending 副本，单 body 仍限 8 MiB。它只观察业务 Read，不在 Close 排空；静止后才以审核过的精确 Policy 调用 `Finalize`，复用同一严格检查，不增加 Provider 分支或自动放行动态值。成功前没有公开 Exchange；任何终止失败都会释放原文并禁止导出，业务透传不受影响。采集服务器清单现支持可选 `deferred_limits`，省略仍走即时模式。延迟出口只在正常停机后读取私有根内固定的有界 0600 `final-policy.json`；Keys、PublicHosts、Projection、QueryKeys 和原登记不可改写，body 上限不得增加，新增精确 Allowed/Aliases 仍须严格预检与定稿，未知值失败关闭，不是自动批准入口。公开回放策略从已验证 candidate 的安全终端值及原公开登记重建，不含私有 Aliases keys 或活动 Projection，并再次 Validate；策略与 candidate 均在工作树外独占创建，失败不留下本轮新 candidate。延迟模式经 `NewDeferredRecorderWithEvidence` 将二进制响应流式写入私有媒体存储，单文件 2 GiB、整轮 4 GiB，不占 8 MiB 原文池；`Projection` 只对登记的非敏感 `id`/`url` 字段做固定公开 token，签名 query 值不保留。生命周期、文件安全、预算口径和释放约束见 `docs/architecture.md`「可控验收装配边界」。当前 App 接线与回放有合成 JSON、SSE 和媒体导出的离线回归，不证明真实 golden 或完整大媒体链；实际浏览器采集与真实 corpus 尚未验收。

multipart 文件请求与二进制响应只以类型化描述符进入 `Exchange`，不保存原始文件名、私有路径、摘要、私有 ID 或二进制/编码内容。multipart 的 boundary 前置内容必须是完整首分隔符；相同前缀后接其他字符不算分隔符，Go reader 的 preamble 跳过不能绕过这一检查。结束分隔符后只允许空行，第二个 close delimiter 等非空 epilogue 一律拒绝。no-byte payload 不能代表 multipart；空 multipart 只有显式结构化且至少一个 part 时才有意义。实际字节由工作树外 0700 目录中的 0600 独占私有文件承载；目录用 `os.Root` 固定句柄，创建时复核路径、inode 身份和双向 inode 祖先；文件系统把大小写或 Unicode 等价路径解析到同一 inode 时同样拒绝，Linux 还拒绝位于非根 bind mount 下的路径；该 bind mount 布局只有解析器回归，未实际创建验证。文件读取使用固定句柄、no-follow 和 non-blocking 标志，每次复核普通文件、0600、长度与 digest；写入或校验失败只使采集无效。同用户或特权进程在检查后的替换/移动竞态仍是残余风险。导出与私有存储错误只返回固定原因，不拼接路径；`PrivateSource`/`MediaEvidence` 格式化脱敏。回放必须显式提供私有或固定合成 recipe 解析器，二者不可互换；`MediaResolver` 含未导出方法，外部自定义实现不能绕过 digest/MIME/source 校验；私有证据只允许 captured provenance，合成字节在运行时生成，recipe 名称/类型/长度/digest 固定为 version 1 并自检，不能声称是原始捕获。回放经 `Replay.WithConnection` 保留连接身份，要求请求 `Host` override 与 URL host 一致（大小写不敏感），随后再做公开主机别名规范化；首次不匹配或耗尽后的额外请求使整轮失败，没有真实网络兜底。采集时已注册并丢弃值的 query 可接受，公开 URL 与回放请求不含 query；仍不支持重定向、transport 错误回放、inline 二进制请求、multipart 响应或未登记媒体类型。支持的媒体类型包括 PNG/JPEG/GLB/glTF JSON/OBJ/STL/FBX/3MF/USDZ/ZIP/octet-stream/x-tgif/text；固定保留头之外的协议字段也不在回放验证范围，不能替代完整上传、下载、资产字节和真实付费验收；真实样本覆盖以 #365 原生记录为准。

上述 no-follow/non-blocking 文件读取保证仅适用于 Darwin/Linux；候选目录不得使用 bind mount 是调用方前提，导出器本身不读取 mountinfo。证据与候选导出依赖 POSIX 0700/0600 权限语义，无法提供时失败关闭。无正文但带媒体 Content-Type 的 HEAD/204 等响应仍不支持。调用方负责关闭私有存储和保留/清理证据目录；失败不会自动清理已写出的私有证据。

## 4. 输入与资源合同

跨 Provider 输入以目标 Offering 合同、owner、格式和来源限制为准。目标接受的本地 source_asset 可以互通；要求原供应商 task 的入口不能透传别家的 remote ID。新提交不能用无法冻结字节的 source_url 代替本地资源。

支持本地资源输入是公共功能归类的充分条件，涵盖图片、模型、纹理、动作。只由一家 Provider 提供仍属公共功能；格式、大小、前置条件、运行可用性及费用另行校验。没有本地输入时，仍可按共同语义归为公共功能。当前接线与 UI 分组不能仅据 API 文档或品牌推定。

**上游输入一律内联（零互联网下游；未验收，见 #264：测试栈不记录出站请求，拿不到原始请求，只有入站日志等间接证据）。** 下游浏览器所在内网没有互联网，服务端也不能假定上游可回拉本平台。Provider 输入图片/模型因此不再依赖 `PUBLIC_BASE_URL` 与签名公网 URL：图片线协议（OpenAI/Gemini/Jimeng，`imagewire`）按 `wire_template`/`$data_uri` 或 multipart 内联；Meshy 的 `asset-array` 参考图固定编码为 data URI，单个 `asset` 参数由 `provider_options.inline_model_input.max_bytes` 声明内联上限，声明后即使资产网关已配置也不发布签名 URL，未声明的 `asset` 参数在 `meshy.ValidateOffering` 启动校验中被拒绝；声明了内联上限时，每个 `asset` 参数的 `max_bytes` 必须为正且不超过该上限（2026-10-09 起；remesh/retexture/rigging 的 `source_asset` 均为 70 MiB（#329 前为 60 MiB）；两处上限都按资产原始字节比较，与 Adapter 在 base64 编码前的检查一致），这样超限来源在 `generation.BindModelSource` 绑定时就判为不兼容：补齐格式把该格式标为 `source_incompatible`，不再显示“可获取”后提交失败（例如 66 MB 的 Tripo GLB 补齐 USDZ）；Tripo 经其文件上传接口、Hyper3D 与 Matting 经 multipart 直传字节。没有任何 Offering 需要上游回拉本平台 URL。图片响应的 `url_field` 必须配套非空 `asset_hosts`（Catalog 校验），只返回 base64 的 Offering（如 GPT image）不声明 `url_field`。

**上传文件名按已核验格式命名（#187 缺陷 C）。** Hyper3D、Tripo 等上游从 multipart 文件名读取格式。`imagewire`、Matting 与 Tripo 的上传文件名统一由 `internal/providers/uploadname` 的显式媒体类型→扩展名表确定，并且必须落在参数 Schema 声明的 `extensions` 内：原文件名扩展名与已核验类型一致时保留原名，否则沿用原名主干并换成核验扩展名；没有原文件名的任务产物命名为 `upload.<ext>`。`application/octet-stream` 只在资产 variant 或文件名带有该参数已声明的扩展名时接受。不读宿主或容器的 mime 表，也不回落 `upload.bin`/`.bin`/默认 `.png`：没有可靠映射时，在打开资产、取得派发许可或发出任何上游请求之前拒绝，失败原因写明媒体类型与缺失的扩展名。

上游完成后先物化；每份必需资源本地校验为 READY 才能公开成功。模型和递归依赖都须缓存、检查并安全改写，交付不能含外部上游引用。已返回必需资源中任何一份失败，都不能借最低数量条件清空 claim 并宣告成功。

### Meshy 独立多图协议与公开引用策略（#191）

2026-10-17 核对 [Multi-Image to 3D](https://docs.meshy.ai/en/api/multi-image-to-3d) 与 [pricing](https://docs.meshy.ai/en/api/pricing)：`POST/GET /openapi/v1/multi-image-to-3d` 接受 1–4 张 PNG/JPEG/WebP 的 image_urls；第一张作为正面主视角。此端点不支持 4K 几何，最高 2K Ultra + 8K 纹理对应 35+5=40 积分。target_formats 必须明确包含 3mf 才返回它。候选保留全部六种模型产物为 required，FBX 与 3MF 分别冻结 binary-fbx-v1、3mf-package-v1；原容器、外部引用、像素和实际字节校验不放宽。

业务 model_id 使用 meshy-7.1-multiview，provider_options 的 model_parameter=ai_model、model_parameter_value=meshy-7.1 映射官方模型身份；endpoint/api_version/result_assets 继续走现有 Adapter 和私有派发许可。无可靠创建响应按 submission_unknown 处理，不自动重复付费 POST，不另建公开生命周期。当前源码 Catalog 启用 owned reference_assets 输入，运行可用性仍由 Adapter 配置裁决；Adapter 中 input_task_id 的独立 wire 分支只为未来严格 same-provider、capability、exact Offering allowlist 资格保留，不等于已对外支持。两种来源互斥，本系统主动拒绝混用，而不采用上游 input_task_id 优先丢图行为。

八项公开只读参数采用 #213 advised 和具体 adjustment_reason，保留端点最高 2K/8K/PBR 建议。用户只读，Agent 合法偏离须理由；4K 几何、8K 与 quad 的非法组合及格式子集仍拒绝，六个 required 输出不变。无纹理时强制把不生效的 texture_resolution 规范化为 2K，避免无效的 8K 加价。原有输入归属、实际字节、顺序冻结、Grant 和物化校验不降级。

`native_pricing` 使用 `meshy.credit` 原生积分单位，以20基础、10纹理、5八K和5Ultra组成报价，默认40，规范化后的合法组合为20–40；隐藏授权字段与既有原生额外金额批准链复用。实际价格和调整原因冻结在 PreparedInput/Grant/Task 中，不把最高档40外推到全部组合，也不等同于实际账单。主界面及 Agent 使用 Offering API 的原生价格投影；旧 provider discovery 继续排除 native 模型，legacy 映射校验也排除它们，不额外广告不理解原生金额的兼容字段。运行账户 Schema 为 Meshy 声明 `meshy.credit` 及独立字符串 mock 金额，Catalog 校验与启用 Offering 单位一致；通用 http-json Reader 直接按原始 JSON 数字或字符串解析精确余额，旧 float Balance 不作为原生数据来源。[官方余额接口](https://docs.meshy.ai/en/api/balance)的 balance 字段声明为整数 credits。未知或无法精确表示的值保持失败，不显示零。

`reference_selection_policy` 是随 Task Offering 冻结的公开标量，空值保持旧兼容，preserve 仅用于有界 ImageTo3D 图片数组。Catalog → 原生 Offering DTO → TS OfferingWire 传递同一值，客户端不推断 Provider 路由；Agent 保留所选身份、顺序和重复引用，不执行历史 front 替换或截断。

这些是源码候选合同；真实账号资格、已发布 revision、dev-test Agent/UI、六格式完整字节与鉴权交付仍须在关联 Issue/MR 记录验收，不能据本地启用宣称 #191 已完成。

### 服务端内容检查

`internal/assets/local/isolation.go` 在新入库和历史交付时调用同一内容边界。
格式解析与共享检查分别由 `isolation_*` 文件负责。
ZIP 成员、glTF 内联或关联 buffer、图片元数据及其解码内容均须核对。
只有已验证的字段与精确字节范围可获得对应的数值或标准公开标识解释。
未知内容、嵌套引用及未消费尾字节保留地址检查。
多个字段和容器成员共享工作预算，限制解码和重复检查消耗。

来源资产还须匹配来源 Task 已冻结的输出合同。
`internal/generation/asset_output_contract.go` 复用物化时的角色、格式和媒体匹配规则。
未声明格式拒绝通过，历史合同不使用当前 Catalog 替换。

物化失败事件、失败次数和重试或终失败状态在同一仓储事务提交。
公开事件仍按 Schema 白名单投影，不因内部保留格式而暴露私有字段。
这部分加固现有物化与交付路径，没有增加 Provider 或第二套公开 Task 状态机。

### 尺寸按参数或提示词传递（`prompt_delivery`）

输出尺寸只由用户在“生成参数”里选的值决定。默认按参数上线。

模型没有对应的上游字段时，在该参数上声明 `prompt_delivery.template`。参数照常展示和选择，但不上线。

输入规范化在校验之后，把模板里的 `{value}` 换成所选值，另起一行追加到提示词参数末尾。追加后会重新校验提示词长度。该参数仍留在规范化输入里，因为准备输入和提交会再次规范化并比较结果，所以规范化必须幂等：提示词已以该行结尾时不再追加。`internal/generation` 把输入交给 Provider 前调用 `schema.WireInput` 去掉这类参数，所以它不会上线。

加载时校验：每个 Offering 最多一个参数声明它；模板必须含 `{value}`；该参数不能是提示词或资产、任务类型；Offering 必须恰有一个 `text` 类型的提示词参数。

当前默认目录没有 Offering 声明它，因为所有图像模型都有尺寸线参数：openai 与 jimeng 用 `size`，gemini 与 meshy 用 `aspect_ratio`，gemini 另有 `image_size`。

## 5. 成本与恢复

准备输入、报价、hash 和待决请求不等于批准。每个新增付费公开步骤都绑定冻结输入及精确的 ExecutionGrant；预期内的私有转换成本必须进入提交前预算，额外费用另行取得同意。

ExecutionGrant 按会话批准模式签发（规则见 `docs/product.md`）。自动模式自动授权。手工模式下，文生 3D、图生 3D、换贴图须由真实用户决定，其余步骤自动授权。冻结输入、Grant 精确绑定冻结输入和幂等身份、不盲目重发、历史视图不创建付费任务，这些规则不变。

**现状**：代表性人工批准/拒绝、策略 bound Grant、计划中间失败及结果不明恢复已验收（#162，revision `95ebc30e`）；故障分支为隔离进程 127.0.0.1:18333 同 SHA 验收。机制见 `docs/architecture.md` 第 7 节。

提交结果不明时沿原幂等身份只读核对；不能换键重发。历史读取不创建收费任务。测试脚本代用户批准仅是测试环境操作。

### 积分展示声明（`credits_metered`）

每个 Offering 用 `provider_short_label` 声明供应商的紧凑简称（如 openai 为 `GPT`、tripo 为 `TRIPO`、meshy `MESHY`、gemini `GEMINI`、jimeng `JIMENG`、matting `MATTING`、hyper3d `HYPER3D`），必填、非空、不超过 8 个字符，同一 `provider_id` 下取值必须一致（加载期校验）；它经 `/api/v1/offerings` 的 `provider_short_label` 进入公开投影，输入区模型选择器只显示它，完整模型名放在悬停提示和下拉表里。前端不内置供应商简称表。

**供应商排序（#100）。** 模型选择表按供应商分组，组序由 Offering 的 `provider_order`（正整数，升序）声明：同一 `provider_id` 的值必须一致，不同供应商不得共用同一个值（加载期校验）；一个供应商只要在任一 Offering 上声明一次，其余 Offering 继承该值，所以新增 Offering 不需要重复填写。当前顺序为 meshy 1、openai 2、gemini 3、jimeng 4、tripo 5、hyper3d 6、matting 7、local 8。值经 `/api/v1/offerings` 的 `provider_order` 公开；未声明的供应商排在已声明的之后。前端 `buildModelPickerTable` 先按 `provider_order` 排，组内可用模型在前、其余保持目录原有顺序，不内置供应商顺序。“推荐”标记与选中行为不变。

每个 Offering 用 `credits_metered` 声明其 Provider 是否按积分计费。同一 `provider_id` 下的所有 Offering 必须取值一致，加载期不一致会报错。当前 meshy、tripo 与 hyper3d 为 `true`（hyper3d 的报价是 `native_pricing` 原生单位，不折算平台整数积分），openai、gemini、jimeng、matting 均不声明。

`/api/v1/offerings` 公开该字段。模型选择表中，不计积分的模型在积分列显示“/”，计积分但没有估算的显示“—”。发送按钮不显示不计积分模型的预计积分。

其他界面按同一规则显示积分。前端用 `offeringShowsCredits` 判断。

- 局部重绘弹窗：不计积分时，不显示费用提示。
- 去背景确认：不计积分时，使用 `ui.canvas.bgremoval.confirm_unmetered`，这句文案不提积分。
- 3D 处理面板：不计积分的步骤显示“预计积分: /”，并且不计入总积分。所选步骤都不计积分时，总计显示“预计积分: /”。这种步骤的重试和继续按钮不显示积分。

`estimated_credits` 对所有 Offering 保持不变，批准、额外积分和估算逻辑仍使用它。该字段只决定是否向用户展示积分。

### 三视图合同与恢复

`create_three_view` 保持 `implementation_key: skill.prepare`。
本批将 `effects` 修正为 `PAID_TASK`，将 `cancellation` 修正为 `task`。
`consent: human` 和必需幂等保留。工具声明不能替代用户对具体冻结输入的同意。

持久 SkillRun 与精确 Grant 绑定 owner/session/turn、冻结输入、Offering 修订和步骤身份。
已提交 Task 按原幂等身份优先核对；丢响应或重启不能触发换键重发。
首步提交与恢复不表示 Agent 已自动执行完整三视图流程。其余两张视图由用户逐张继续，失败的一张可单独作为该步的下一次尝试重试（步骤身份含尝试序号，第 0 次与原身份相同）；三张视图以 `chat.three_view` 声明为一个分组，用它做 3D 时只使用正面图。2026-10-13 已验普通主体三张正常逐次继续及完整分组；失败单步重试和以该组三视图启动后续 3D 未由这次正常流程覆盖。

`image-three-view` 默认配方的主体限制修正（#174，2026-10-13 已发布并完成限定实图验收）位于 Product Schema：recipe revision `2026-10-13.1`、application revision `2026-07-29.103`。三个模板分别改变正面、右侧面、背面视角，保持参考主体的类别、形状/几何、比例、颜色、材质与表面标记的位置；仅在参考图已经是角色时保留既有角色特征，不为普通物体添加角色身体、脸、肢体或衣物。缺少唯一参考图的拒绝文案由 `chat.three_view.messages.reference_required` 声明并校验，执行器读取 Schema，不持有第二份角色专用文案。

边界保持：`catalogrecipes` 按所选 Offering 的参数语义映射模板与原图到 `StepSpec`，SkillRun 持久冻结 recipe revision/hash 与各步规格，Generation 冻结 PreparedInput。目录更新不会重写旧 Run；已准备和以后才准备的旧步骤仍使用该 Run 的原规格。`create_three_view` 当前只接收参考图句柄/序号，用户任意附加指令没有进入 CreateCommand/配方输入，本次不新增该能力。Provider Adapter、输出合同、逐张继续与批准/幂等机制没有改变。2026-10-13 在部署 revision `00dea7c52af622eaf62f1c8fabc6a6d10a33b8d0` 上，普通蓝色方块三张逐次继续、唯一 Task、完整分组和实际 PNG 字节均通过，逐图检查未新增角色特征；背面仍有透视，不据此保证严格正交。既有角色仅生成正面图并确认身份与服装保留，侧面和背面未启动。旧 Run 发布前后的冻结模板、步骤与 PreparedInput 摘要一致。批准策略的手工/自动模式分别有 bound Grant 回读。完整证据与范围见 [本日验收回执](acceptance-2026-10-13-processing162-166.md)，不把源码或冻结测试当实图。

冻结回归只在同一持久库上替换 Catalog 解析器与 SkillRun Runtime，复用 SchemaVersion 未设置的测试 Generation，验证旧 Run 读取及准备的规格不变。它不覆盖 application revision `.102` → `.103` 的旧输入提交兼容性：实际 Generation 绑定 application revision，未接受的旧 PreparedInput 版本不一致时由 `SubmitPrepared` 拒绝，仍须重新准备并遵守既有批准合同。

### 可控故障验收

同包 `_test.go` 验收服务器通过 `internal/app` 私有装配端口包裹真实 Provider/Processing Gateway，保留 Meshy 的依赖解析等可选端口。故障只作用于清单绑定的执行身份，Start 预算及一次性故障消费写在独立目录；普通发布路径不读取这些故障配置。图片余额不足样本由真实 image-sync Adapter 解析隔离本地端点的 `insufficient_quota` 响应，必须标注为 wire 样本，不描述为真实上游余额耗尽。Catalog A/B 及恢复使用私有副本，不改共享 Provider 连接、公开产品 Schema 或冻结任务记录。该方式不放宽输出检查、必需槽位、ExecutionGrant 或唯一 Task 生命周期合同。

## 6. 验证

本地门禁以 `dev_test.md` §2 为唯一真源。现有 Acme 同步/异步声明扩展、协议契约、内核分层和前端 Schema 变异测试继续保留；`internal/architecture/boundaries_test.go` 递归扫描直接 import，防止声明层反向依赖及业务层导入宿主/协议实现。它不证明所有标识符级耦合或完整运行正确性。

改动还须按影响链验证公开投影、discovery、历史、冻结输入、同意与本地资产；发布到 dev-test 后执行 L0/L1 和所需 L2/L4。精确提交及验收证据记入交付报告，不从历史状态推断。

本批结构调整与验收范围见 [架构总览](architecture.md)。
本文随本次运行改动同步更新。后续边界变化须按根 AGENTS.md 在同一交付同步本文与架构总览。

### 双域参数展示与兼容投影（#179；交付见 MR !118）

历史 #179 引入 `presentation.surface=readonly` 时只允许有默认值的固定参数使用；#213 的 advised 扩展见上文。公开 Offering DTO 保留 `fixed/default/forced_when/presentation/options`，前端据此展示有效固定值；标签、选项、条件和排列沿用 Offering 声明。`hidden` 不因不可编辑而自动公开。当时旧 `/api/providers` 表单及其 extended 表单投影均过滤 fixed/readonly，避免旧控件把固定配置显示成可编辑项；完整只读信息由 `/api/v1/offerings` 提供。Catalog 的 fixed/forced 规范化及费用、审批、已冻结输入规则不变。本次不更换 Provider API、模型或质量默认值。


### 原生计价合同（可选，旧整数路径保持）

Offering 可声明 `native_pricing` v1：稳定 unit/公开 unit_label、字符串 base_amount、最多八个 `{id,label,amount,when?}` 组件，以及有组件时的服务器 consent_parameter/max_authorized_extra_amount。组件相加，不复用旧 `cost.extra_when` 的 OR 计一次语义；固定最高档组件可省略 when。未知金额不可通过旧 estimated_credits=0 表示可执行免费。条件须是同 Offering 合法公开标量参数且有默认值；来源、同意、hidden/fixed 参数不得作为条件绕过权限（固定产品档位用无条件组件）。数量乘数与旧 Cost/CostPolicy 不能混入 v1。金额为非负有界十进制字符串，最多六位小数；未知版本、格式、溢出、重复组件、上限不足与非法条件均拒绝。

实际冻结报价形状是 `native_quote:{version,unit,unit_label,base_amount,extra_amount,total_amount,components:[{id,label,amount}]}`，金额 canonical，组件按 ID 排序。Schema evaluator 是唯一金额公式；Adapter 消费已冻结的合法输入和原派发许可，不持有第二份价目或批准策略。任何价格条件都必须与真实 wire 参数一致，不能以业务模型别名猜测上游费用。公开投影只给默认/上界或实际冻结报价，不公开私有定价规则。

Prepared hash、Task 0055 持久列与同事务 Grant 绑定共同冻结报价。新 Task/Prepared 的独立 required 标志防止缺报价被读成历史记录；nil 旧数据不回填、不改变 hash。native 报价与旧整数价格键互斥投影；多步骤和统计按 unit 分组，精确余额未提供时显示未知。旧不支持该合同的 discovery 入口不宣传 native 为免费，执行仍受统一 Generation 准入约束。Rodin/Bang/Texture 的最终价格声明、协议能力与 enabled 资格由对应 Issue 的实际文档和上游验收决定；新增通用价格类型不等于启用这些能力。

原生报价的消费合同包括账户汇总可省略旧 `by_type`、三视图每槽冻结报价与明确默认估算的区分，以及 `task_registered.native_quote` 经严格解析保留到实时消息费用说明；缺失或损坏的新模式报价不得回退为旧整数零值。


### 私有离线保留策略

`application.runtime.task_retention` 声明离线维护的启用状态、最小保留秒数、
终态集合、Task/Input 时间字段和 `preserve_all` 引用策略。默认禁用且不提供
保留期；该配置仅供 `cmd/art-ai-retention` 装配 Generation 维护服务，Catalog
校验与防御性复制覆盖这段声明。它不进入 PublicApplicationSchema、Provider
discovery、前端控件或 Offering 执行配置，不新增 Provider Adapter 分支。
运行镜像分发独立 `art-ai-retention` 二进制，不由服务器或 Provider worker 调用；
分发不启用策略、不产生清单，也不替代每次人工审批。
Schema 启用不代表人类批准具体删除清单；每次 apply 和恢复 cleanup 仍须独立
人工确认完整 manifest SHA。事务、备份、引用闭包与恢复边界见
[架构说明](architecture.md#离线任务保留边界)和[维护合同](storage-maintenance.md#独立的任务保留命令)。

### Hyper3D 接线（Rodin 2026-10-20 启用，#188；Bang/Texture 2026-10-21 启用，#187）

两个 Rodin Gen-2.5 Offering（修订 `2026-10-20.188.1`）用固定 wire 档位和精确 `hyper3d.credit` 报价：`Gen-2.5-Extreme-High`、Raw/2,000,000 目标面数、PBR、`texture_mode=extreme-high`，基础 0.5 + 几何 0.5 + 纹理 2 = 3。`authorize_extra_credits` 只参与现有批准边界，不发送上游。价格依据当前[独立端点文档](https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5#pricing)：未列出的参数（含 HighPack、UHD）不加价，不引用营销页换算或把 0.5 四舍五入。2026-10-20 按路线 B 启用（用户为通过资格的候选选定），同日在开发测试环境通过公开 Task 验收：文生、单图、双图各一次，每次实扣 3 credits，与冻结报价一致（#188）。

**输出合同（2026-10-20，Agent 按用户授权的技术决定）。** 必需产物是一份带 PBR 贴图的 GLB（下载列表里以 `_pbr.glb` 结尾的 pack 文件，2026-10-16 实际名为 `base_high_pbr.glb`），另以 `background` 槽位保存 `preview.webp` 缩略图。理由与边界：

- 贴图分辨率由 `texture_mode`、`uhd_texture`、`addons` 共同决定（端点文档的分辨率表）。`extreme-high` 不加 HighPack 是 6K，加 HighPack 是 12K。2026-10-16 的 12K 样本是 164,780,252 字节（三张 PNG 共约 108 MB，最大一张约 85 MB），超过平台生成模型转换 profile `generated-glb-v1` 的 128 MiB 与单张 8192² 像素上限，免费本地 glTF 补齐不可用，浏览器预览也要整份下载。6K 保留最高纹理生成档和同样的 3 credits 报价。2026-10-20 在开发测试环境实测三个公开 Task（文生、单图、双图）：主 GLB 为 100,005,836–110,770,224 字节，base color 为 6144²，另外两张是 2048²，100–122 万顶点、200 万三角面，都在该 profile 内；对文生 Task 实际做了免费 glTF 补齐（110,769,633 字节 ZIP）。验收证据见 #188。
- `Gen-2.5-Extreme-High` 另附一份最多 1000 万面的网格（2026-10-16 实际为 `model_export_raw.glb`，12,165,688 字节，888 万三角面、只有 POSITION、无材质贴图、`KHR_draco_mesh_compression` 必需）。它不是可直接使用的成品，平台本地转换 profile 也拒绝压缩扩展，所以不进入公开产物。Adapter 原先对每个 `.glb` 都生成 `model/glb` claim，下载列表把它排在 PBR 包之前时，它会占据主模型序号；现在由 Offering `provider_options.download_files` 声明只取哪些文件。2026-10-20 用当前 main 的 `Store.Put/Open` 离线复验，这份网格已能通过有界 Draco 检查（之前以 `external_address` 被拒），但仍不交付；以后要交付需另开独立角色并验收查看器、ZIP 与来源选择。
- `download_files` 每项恰好声明 `name` 或 `name_suffix`，只匹配文件名，扩展名必须是 Adapter 识别的类型，并且至少选中一个已声明的必需模型；每条规则只取第一个匹配的文件。没有选中必需模型时按“文件尚未就绪”继续轮询，不会把未声明的 GLB 填进必需槽位；如果全部子作业已完成、下载列表也不为空，却连续 10 次都没有选中的模型（例如上游改了文件名），Attempt 以 `protocol` 失败结束（“上游未返回声明的模型文件”，不可重试），不会一直轮询到 1 小时的作业超时。必需模型的判断只认会阻断成功的槽位，background/on-demand 模型槽不算。预览槽位只收 `image/webp`，上限 10 MiB。自 2026-10-21 起 Bang 与 Texture 候选也声明 `download_files`（见下文）；没有声明的 Offering 保持原来按扩展名识别的行为。
- 其他格式：glTF 可以经现有免费本地 `local/generated-glb/to-gltf` 补齐（受 128 MiB 等 profile 上限约束）。FBX、OBJ、USDZ、STL、3MF 只能走 Meshy Convert（`source_asset` 内联上限 70 MiB，#329 前为 60 MiB）；实测主 GLB 约 100–111 MB，这些格式如实显示为不可用（`source_incompatible`）。Rodin 自己的 `geometry_file_format` 是单选，每多一种格式就要再付一次生成费，所以不在同一 Task 内私有补齐。

**下载 host。** `asset_hosts` 只登记 `file.hyper3d.com`（含其子域，`httpjson.OpenClaim` 的既有匹配规则；不登记 `hyper3d.com` 等父域）。依据是 2026-10-16 一次真实 `POST /download` 回包：三份文件都在该 host 的 HTTPS 签名 URL（TOS 签名，7 天有效）下；2026-10-20 对这三份已有签名 URL 做只读 Range 请求，都直接返回 206 和正确的 `Content-Type`，没有重定向。官方下载文档的示例仍是 `example.invalid`，不足以作为 allowlist 依据。签名 URL 只作为 Attempt 内的 claim locator 使用，不写日志、事件、公开投影或 Issue。

Bang 的两个独立候选均为原生 0.5：`rodin-task` 只接受精确 Rodin 文生/图生 Offering 的同 Provider、已成功来源，以其私有顶层 UUID 映射 `asset_id`；`local-model` 只接收本人已验证的静态 GLB 和一张必需材质参考图，二者作为 multipart 文件，允许符合合同的跨 Provider 本地字节。候选固定 PBR/High、Raw/2,000,000、strength 5，不用业务模型别名推断 wire 版本；私有 ID 不进入公开响应。Schema 可要求比上游条件更窄的必需参考图，Adapter 在派发前复核来源、文件及互斥输入。[Bang 文档](https://docs.hyper3d.ai/en/api-specification/bang)。

**Bang 的语义。** 官方端点说明 `strength` 越大“拆出的部件越多”，2026-10-21 两次真实结果都是一份按部件拆开的 GLB（6 或 8 个 mesh/材质），三角面被重建到 `quality_override` 目标（本地 7.6 万面的输入输出 200 万面），材质也重新生成。它不是“重拓扑”或任意局部几何替换。两个候选现在归入既有 `mesh-segmentation` 能力；本地文件候选修订号为 `2026-10-21.187.2`，Rodin 来源候选为 `2026-10-21.187.3`，以 `same_session:true` 在 Prepare 阶段拒绝同 owner 的异会话来源，并冻结来源 session；连接声明同步替换原先没有其他 Offering 使用的 `remesh`，Adapter 拒绝旧归类。中英文名称均写明“部件拆分与重建”，2026-10-21 启用，非默认。归入分件能力不使其成为 Tripo Complete 的合法来源；该来源仍由精确 Offering、Provider、owner 与 session 约束。

**Texture（`hyper3d/texture/local-model`，修订 `2026-10-21.187.2`，2026-10-21 启用）。** 端点 `rodin_texture_only`，能力 `retexture`，只收本人已验证的静态 GLB（`static-glb-v1`，10 MiB）和一张必需参考图，可选提示词；不接受任务来源或远程 UUID（端点只收文件）。wire 固定 `texture_mode=extreme-high`、`resolution=High`（官方分辨率表为 12K）、`hd_texture=true`、PBR、GLB，遵循最高质量规则。旧修订的 `Basic` 是 6K；#188 Rodin 的路线 B 取舍不扩大到这个独立端点。12K 若超出 `generated-glb-v1` 的单图 8192² 上限，免费转换应如实不可用，不放宽安全检查或降低生成质量。报价为 2 Hyper3D credits：官方价格表列基础 0.5 与 extreme-high 2.0，未明说相加；2026-10-21 本修订（High）的公开 Task 提交回执 `consumed=2`，按任务标识对上的用量记录也是 2，与冻结报价一致（较早的 Basic 直接样本同样扣 2）。[Texture 文档](https://docs.hyper3d.ai/en/api-specification/generate-texture#texture-resolution)。

**输出合同与历史样本（2026-10-21 直接资格记录）。** Bang 与 Texture 候选都只声明一个必需 GLB 槽位，`download_files` 只取 `*_pbr.glb`，`asset_hosts` 只登记 `file.hyper3d.com`。依据是三次真实提交（各一次，均为新提交，没有重发）：

| 用例 | 扣费 | 下载列表 | 必需 GLB | 结构 |
|---|---|---|---|---|
| Bang `asset_id`（来源为 #188 文生 Rodin 任务） | 0.5 | 仅 `base_high_pbr.glb` | 213,315,892 B，SHA-256 `433dce281425…` | 6 个部件，200 万三角面；base color 12288²，另两张 4096² |
| Bang 本地模型（1.8 MB 无贴图静态 GLB + 一张参考图） | 0.5 | 仅 `base_high_pbr.glb` | 86,902,156 B，`dee4c67f0786…` | 8 个部件，200 万三角面；三张 4096² |
| Texture（同一模型和参考图） | 2 | 仅 `base_basic_pbr.glb` | 23,913,940 B，`580d6eed9658…` | 1 个 mesh，三角面不变（76,412）；base color 6144²，另两张 2048² |

三份文件都在 `file.hyper3d.com` 的 HTTPS 签名 URL 下，直接 200、`Content-Type: model/gltf-binary`、没有重定向，没有预览图；GLB 2.0、`extensionsRequired` 为空，用当前 main 的 `Store.Put/Open` 离线检查都通过且读回字节一致。`*_pbr.glb` 规则同时挡住 `material=All` 之类设置可能多出的 shaded 包或预览图被填进模型槽位。Bang `asset_id` 结果的 base color 是 12K、整份 213 MB：超出 `generated-glb-v1` 的 128 MiB 与单图上限，glTF 免费补齐和 Meshy Convert（70 MiB）都不可用，只交付 GLB；Bang 端点的 `resolution` 只有 Basic（2K）/High（4K）两档（与 Texture 端点按 `texture_mode` 分档的表不同），这张 12K 贴图不由它决定，本次没有为压低体积再付费试 Basic。

**公开 Task 验收与启用（2026-10-21，#187）。** 用隔离独立资格机制（`scripts/smoke/README.md`「独立候选资格」）：测试二进制在 dev-test 主机 loopback 端口运行，数据库与资产是 dev-test 的私有快照，私有 Catalog 与默认 Schema 只差这三项的 `enabled`，每项一个 `standalone` 场景、最多一次 Start。三项各一次公开 Prepare → Grant → Task，全部 SUCCEEDED：

| Offering（修订） | 输入 | 冻结报价 / 回执 `consumed` / 用量记录 | 必需 GLB | 结构 |
|---|---|---|---|---|
| Bang 本地模型（187.2） | 1,832,284 B 静态 GLB（76,412 三角面）+ 一张 PNG 参考图 | 0.5 / 0.5 / 0.5 | 84,339,612 B，`069a40a4077a…` | 7 个 mesh/材质，200 万三角面，三张 4096² |
| Bang Rodin 来源（187.3） | #188 文生 Task（同会话） | 0.5 / 0.5 / 0.5 | 188,047,300 B，`8f4ed8b92c5d…` | 7 个 mesh/材质，200 万三角面，base color 12288² |
| Texture（187.2） | 同一 GLB 与参考图 | 2 / 2 / 2 | 77,176,140 B，`52e82983b54b…` | 1 个 mesh，三角面不变，base color 12288² |

owner 两次下载字节与资产记录一致；公开 Task JSON 不含绝对地址、Hyper3D 域名或订阅键；GLB 2.0、`extensionsRequired` 为空。提交前负路径全部 400 且没有上游调用：异会话 Rodin 来源、加工结果来源、不存在的 Task、缺参考图、PNG 冒充模型、不存在的资产；14 MB GLB 在上传时就被拒（`model_input_limit`）。随后把同一快照复制为严格 session 副本（正常二进制、session 鉴权、不注入 Hyper3D Key）：匿名 401，另一普通用户 404 且与不存在的 ID 响应相同，对方的补齐格式、重试、取消、重新获取都是 404 且 Task 不变。启用改动只翻转这三项的 `enabled`，修订号不变。

**2026-10-21 官方文档复核（#187/#188 第一阶段，未付费）。** 逐端点重读 [Gen-2.5](https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5)、[Bang](https://docs.hyper3d.ai/en/api-specification/bang)、[Generate Texture](https://docs.hyper3d.ai/en/api-specification/generate-texture)、[Check Status](https://docs.hyper3d.ai/en/api-specification/check-status)、[Download Results](https://docs.hyper3d.ai/en/api-specification/download-results)、[Check Balance](https://docs.hyper3d.ai/en/api-specification/check-balance)、[Errors](https://docs.hyper3d.ai/en/api-specification/errors) 与 [Changelog](https://docs.hyper3d.ai/en/get-started/changelog)（API 均为 `/api/v2`，文档不标模型版本号）。与现有 wire、Schema 和最高档的对照：

- Rodin：Gen-2.5 参数、Extreme-High 档、纹理分辨率表与逐项相加的价格（0.5 + 0.5 + 2 = 3）与现行 Offering 一致，无差异。
- Bang：两种输入互斥、`asset_id` 模式必须省略 model/image/prompt、`strength` 1–12、`quality_override` 500–2,000,000、`resolution` 最高 High（4K）、每次 0.5，均与候选一致。文档内部有一处不一致：`asset_id` 模式的“可选参数”清单不含 `mesh_mode`、`quality_override`，但参数说明写 `quality_override` 覆盖来源资产设置，Changelog（2026-09）也记载为 Bang 新增了这两个参数；2026-10-21 两次直接样本的输出都是 200 万三角面，与按候选模板发送 `quality_override=2000000` 相符，但没有保留原始请求，不能证明当时实际发送了哪些字段（历史直接样本，非公开验收）。候选保持发送，公开验收时核对输出面数。新参数 `instruction`、`escore`、`reference_scale` 未开放，按默认值。
- Texture：分辨率表与候选的 `extreme-high + High`（12K）一致。价格表只列“基础 0.5”和“`texture_mode=extreme-high` 2.0”，没有 Gen-2.5 页那样写明“相加”；本仓库较早的调研摘要称该页“说明相加”，与当前页面不符。因此 High 修订的实际价格仍是 2 或 2.5 两种读法之一，冻结估算保持 2；验收时以提交回执 `consumed` 与按 uuid 归属的用量行核对，若为 2.5 则先改报价再启用。2026-10-21 公开 Task 的结果是 2。
- 失败与费用：错误页规定提交、状态、下载的业务错误都以 HTTP 201 加 body `error` 返回；官方建议对 429/5xx 退避重试，本项目对提交的 5xx/超时更严格地按“结果未知”停止、不重发。官方没有说明失败子作业（`Failed`）或 201 业务错误是否退款，这一点仍记为未知。
- 真实观察（只读、零费用）：2026-10-21 用开发测试环境 Key 以随机标识各调一次 `POST /status` 与 `POST /download`，都返回 HTTP 201 与 `{"error":"NO_SUCH_TASK"}`，调用前后余额都是 208。Adapter 对这一形状按 Schema 错误映射以不可重试、非结果未知的 `rejected` 结束 Attempt（离线回归 `TestObserveNoSuchTaskBodyErrorFailsWithoutResubmission`，追补覆盖）。同日 `check_balance?usage_page=1` 的用量行确实带 `task_uuid`；早前“无法归属”的三行（0.5、2、0.5）缺的是本地回执，而不是上游字段。

旧 remesh 兼容 API 的字段、Task alias 和 multipart 显式限定原 Meshy Offering，不把 Hyper3D 原生报价或不同文件合同投影到旧入口。

旧 retexture 兼容 API 的 `input_task_id` 别名同样显式限定 `meshy/processing/retexture` 与 `tripo/processing/texture`（这两个原本就是该别名的全部目标），不把只收文件的 Hyper3D Texture 投影到旧入口。Agentic 与完整多图来源构建也没有据此完成资格；Adapter 对 1/3/5 张图保序的 wire 测试不替代端到端 Agent 来源验收。


### 数量控件与报价的边界

数量控件、参数摘要读取有效会话参数及公开参数约束，不读取 `estimateCredits.count` 作为参数值。是否计积分、原生报价是否可用只影响费用文字；不改变数量或参数写入规则。步长、范围、整数约束和默认值仍来自 Offering，未声明数量参数的模型不补造控件。`disabled_when` 表示不能编辑，不能推导为固定数量 1；`omit_when` 仍按 Provider wire 合同省略字段。只读项与冻结执行授权保持原边界，禁用原因和边界提示文案由 Product Schema 声明。


## 无 Provider 凭据时的正式栈投影（#232）

正式栈使用独立且初始无 Provider Key 的运行环境。此部署状态不改变 Product Schema/API 的公开可用性合同：每个 `provider_id != local` 的远程 Provider Offering 都须明确投影为不可用，不得静默切换模型；`local` Offering 仍按本地能力投影；服务端 `application.runtime.configuration` 及密钥不进入公开 Schema、Provider discovery 或前端。formal 使用 `SESSION_COOKIE_NAME=art_ai_formal_session` 隔离会话 Cookie；该配置只在服务端运行环境中，公开 Schema 不公开 cookie 名、秘密或运行时配置。未设置时沿用现有默认 cookie 名，保持测试栈兼容。正式 smoke 只读取得 formal 登录时签发的真实 Cookie，先向测试栈重放并要求 `/api/auth/session` 返回匿名、`isAdmin=false`，`/api/v1/tasks?limit=1` 返回 401；以同一秘密改用测试栈默认 Cookie 名重放也必须拒绝。formal 登出后，再用独立 opener 向 formal 重放登录时捕获的原 Cookie，确认它已撤销（匿名 session 和 401 tasks）。该验证不登录测试栈、不读测试凭据、不发送状态变更请求。正式栈实际部署与线上验证状态以 #232 Issue 回执为准。

## Offering 文案的多语言（#255，第三期，源码已实现，未部署验收）

Offering 的面向用户文案（模型名、Provider 名、参数标签与说明、选项与选项分组、输入规则提示、费用项与费用说明、Adapter 声明的消息）按**默认语言原文**翻译，译文写在 Product Schema `application.localization.bundles.<locale>.offering_texts`（原文 → 译文），不按路径寻址，不新增 `label_key`，也不在前端另建翻译表。原因有三：同一批标签在几十个 Offering 里重复（2273 处中文只有 1053 种）；输入规则和值为空的选项没有身份字段，无法按 `[字段=值]` 稳定寻址；新增 Offering 若复用已有标签，不必再写译文。`coverage` 写 `offerings` 时，Catalog 加载要求每段中文 Offering 文案都有译文；某条译文的原文已没有 Offering 使用时，按过期译文报错。这项检查在 Offering 自身校验通过后进行，所以 Offering 本身有错时，报的是 Offering 自己的错误。

按语言投影只发生在读取层：`/api/v1/offerings`、Provider discovery、模型格式、三视图分组、待批准列表、聊天工具调用、任务投影、管理后台生成记录与用户资产页里的冻结报价、Agent 待批准投影。费用项改名后，旧报价按组件 id 回退到冻结 Offering（或同模型的当前 Offering）当前声明的译文。这些接口按请求语言把 Offering 文案换成译文，响应带 `Vary: Accept-Language, Cookie`。Provider Adapter、wire 映射、冻结的 Offering 修订、PreparedInput、NativeQuote 与公开 Task 的存储都不因语言改变；报价里的费用项名称只在读取时翻译。机制见 `docs/architecture.md` §5.1。

**当前限制（LEGACY，独立后续工作）：** 上述 `offering_texts` 机制以默认语言文案为中文原文：原文键、`coverage` 缺译检查和读取替换都依赖中文字符识别。默认语言不是中文的 Catalog 尚无 Offering 翻译支持；通用源语言声明与检测未实现，不纳入当前产品验收，也不把现行中文源机制表述为已完成的多语言支持。界面 `messages`/`fields` 投影不依赖该假设，但结构化字段的覆盖缺译检查同样按中文字符识别，非中文默认语言时不会强制这些译文。
