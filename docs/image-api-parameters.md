# 图片生成 API 参数与 Schema 接入

核对日期：2026-09-27。范围为 catalog 已登记的五个图片模型、文生图和图生图共十个 Offering。新增模型仍需配置模型身份、接线及可用性，不能由通用协议执行器推断。

2026-10-09 更新（产品设计宗旨「最新 API、最高质量」，见 `provider-schema-architecture.md`）：质量参数改为后端固定（`fixed`），用户与 Agent 不能选择（即梦 `size` 除外，见本段后文）。GPT 固定 `quality=max`、`output_format=png`；Gemini 固定 `image_size=4K`；Seedream `size` 可选比例，每个比例按最高分辨率出图（低档由 `forced_when` 升档，原选项与像素约束保留）；Meshy `nano-banana-2` 两个 Offering 曾停用，2026-10-10 已恢复启用（上游无质量参数）。下文 2026-09-27 及更早的探针记录保留为历史证据，其中的默认值（如 Gemini 1K、GPT WebP、Seedream 2K）已不是现行配置。

当前启用身份（OpenAI 与 Google 在两类能力上各只有一个活跃模型；Meshy 两类能力仍各有两个活跃身份）：

- OpenAI：`gpt-image-2.5-sunburst`（官方指南中的高精度编辑档位；`gpt-image-2` 仅保留在历史任务快照与展示映射中）。
- Google：仅 `gemini-3-pro-image`。`gemini-3.1-flash-image` 曾以禁用 Offering 登记，因内网 llms 网关对该模型名返回 `model not supported by any target`（2026-09-27 用 gemini 密钥逐名实测），已从目录移除，不再保留占位。
- Offering ID 保持不变（如 `openai/gpt-image-2/image-to-image`），是历史任务与既有客户端提交的稳定路由键；模型身份来自 `model_id`。

## 参数覆盖

| 系列 | 当前协议 | 尺寸 / 比例 | 其他生成参数 |
| --- | --- | --- | --- |
| Meshy Nano Banana Pro / 2 | OpenAPI v1 text-to-image / image-to-image | 1:1、4:3、3:4、16:9、9:16；多视图时省略比例 | 多视图、去背景、文生图姿态、最多 5 张参考图 |
| GPT Image 2.5 Sunburst | 文生图 JSON `images/generations`；图生图 multipart `images/edits` | auto、预设与自定义 WxH；边长为 16 倍数、最长 3840、比例至 3:1、总像素 655360–8294400 | quality（含 xhigh / max）、n、PNG/JPEG/WebP、压缩、背景、moderation、16 张参考图、PNG 蒙版 |
| Gemini 3 Pro Image | 网宿 Chat Completions | 10 种比例；1K / 2K / 4K；已实测 9:16 / 2K | messages 文生图/参考图输入、data URI 图片返回；最多 14 张参考图 |
| Gemini 3.1 Flash Image | 网宿 Chat Completions | 渠道声明 10 种比例；1K / 2K / 4K | 同上；保持禁用状态，未做该模型的付费验收 |
| Seedream 4.5 | Ark v3 images/generations | 2K / 4K 与自定义 WxH；总像素 3686400–16777216 | 连续组图、max_images、水印、URL/base64 响应、standard 提示优化、14 张参考图；参考图与输出图合计不超过 15 |

尺寸范围与选项以 `internal/catalog/default_catalog.json` 为执行真源。GPT Image 2.5 超过 2560×1440 的输出属于实验尺寸（catalog `max_pixels` 仍按官方上限 8294400 声明，超过 3686400 视为实验）。PNG 下省略压缩参数；JPEG 与透明背景的组合在服务端拒绝。Meshy 不再发送文档未声明的 negative_prompt。Seedream 4.5 不声明旧模型 seed 或新版其他模型才有的格式选项。

## 数据路径

1. Offering 参数声明类型、选项、默认值、数字/图片大小约束与条件禁用。
2. `/api/providers?projection=extended` 投影展示和校验元数据；旧投影保留既有兼容规则。
3. `SchemaParameters` 根据参数类型生成控件，页面提交声明的参数，保留数字和布尔类型。自定义尺寸不被预设枚举强制替换。
4. generation 统一校验。补充资产字段（如 mask）通过兼容入口持久化，再走统一资产所有权解析。
5. `imagewire` 消费 `request_template`、`wire_name`、`wire_template`、`wire_values`、`omit_when`。`request_encoding: "multipart"` 时改为扁平表单编码：标量参数按 `wire_name` 写为字段，资产参数写为文件分片（GPT 图生图为 `image[]` 与 `mask`），本地资产读取与大小限制在连接上游前完成；默认（空或 `json`）沿用嵌套 JSON 构造。
6. `schemaimages` 使用配置的端点、认证和响应路径提取图片；编码方式只来自 Schema 声明，不按模型或能力分支。Meshy 保留异步任务生命周期，复用请求投影。
7. Chat 面板把 Offering 声明的参数（旧版固定控件之外）渲染为通用参数面板，提交 `parameters` 映射；后端按声明过滤后进入统一提交校验，Data URI 资产参数经归属解析后再写入任务输入。

添加现有协议的新标量/选项/图片参数时只改 Schema，不增加 Handler、页面或 Adapter 的模型判断。新增不同的传输协议仍需实现协议解析。

## 声明与实测边界

2026-09-27 完成的 `gpt-image-2.5-sunburst` 全声明参数付费探针（7 次请求全部 200，直打 llms 网关的 `/v1`，key 为 dev-test 的 `OPENAI_API_KEY`；除 quality 档位探针外均为最小成本组合 `size=1024x640` + `quality=low`）：

- G1 文生图 JSON：`size=1024x640`（自定义非预设值）+ `quality=low` + 注入默认值（`n=1`/`output_format=png`/`background=auto`/`moderation=auto`）→ 200，20.2s。
- G2：`output_format=webp` + `output_compression=80` + `background=transparent` → 200（透明+webp 组合合法）。
- G3：`output_format=jpeg` + `background=opaque` + `moderation=low` + `n=2` → 200，**返回 2 张图**（n 生效）。
- G4：`quality=xhigh` → 200，71.6s，output_tokens 1700。
- G5：`quality=max` → 200，127.0s，output_tokens 3824。token 阶梯 low(107) < xhigh(1700) < max(3824)，与质量档位单调一致。
- E1 图生图 multipart + `mask`（与首图同尺寸 64×64 PNG，半透明区域为编辑区）→ 200。
- E2 图生图 multipart + **16 张参考图** → 200（声明上限 16 张被代理接受）。

同日 App 端到端抽查（dev-test 生产同构测试环境，镜像 `a5e941e`）：原生 `POST /api/v1/tasks` 提交 `{size:1024x640, quality:low, output_format:webp, background:transparent}` → SUCCEEDED（12.3s），产物 VP8L WebP 实际解码 1024×640 与请求一致（任务 `task_b0909958e9da1209299bd224ebf82610`）。探针脚本与结果位于 dev-test `/tmp/probe2d/`（未入库；driver 不落盘密钥）。

结论修订（取代 2026-09-17 的「均未对代理逐项验证」）：

- mask、n（=2）、size（自定义值）、quality（low/xhigh/max，medium/high 同枚举族未单测）、output_format（png/webp/jpeg）、output_compression（=80）、background（auto/opaque/transparent）、moderation（auto/low）、16 张参考图上限**均已实测被代理接受**。
- `normalizeInput` 注入的默认值组合已随 G1 及 App 端到端任务经过线上验证。
- 仍未实测：单图 50MB 上限、n=10 上限、`quality=medium/high` 单项、自定义尺寸的 max_pixels/max_edge 拒绝边界（属于本地校验层职责）；输出尺寸规则不变——自动输出可以不是 1024 类预设，不能用实测输出反推输入约束。
- GPT 响应不回显模型身份的既有边界说明继续有效。

历史探针存档：2026-09-17 的 S1/S2/S3/S4 最小契约探针（S1 文生图 JSON `model+prompt`、S2 图生图 multipart `model+prompt+image[]`、S3/S4 Gemini）仍有效，产物位于 `.opencode/workflow-reports/provider-live-parameters/selection-20260917/`。

边界说明：

- 官方文档声明 ≠ 代理实测：截至 2026-09-27，GPT Image 2.5 Sunburst 的全部公开声明参数已完成最小组合实测（见上），但单参数上限边界（50MB、n=10、medium/high）仍以本地校验层为准，未经代理拒绝路径验证。
- GPT 响应不回显模型身份，只能证明目标路由返回了图片，不能独立证明上游模型身份；Gemini 响应回显 `model`。
- Gemini 保持代理渠道的 `eca_image_config` 契约（`aspect_ratio` / `image_size`），不是 Google 原生 Interactions/Images 契约；streaming、Responses API 与 Interactions 不在当前支持范围。
- 图生图编码曾以 JSON `images[].image_url` 运行，但未在本期复现；当前实现以 S2 的 multipart 契约为准。

## 接线与验证边界

Gemini 当前按已验证的网宿渠道配置：Base URL 含 `/v1/`，相对端点为 `chat/completions`，Bearer 认证。提示词位于 `messages.0.content.0.text`，参考图追加到同一 content 数组，使用 `image_url.url` data URI；返回图片从 `choices.*.message.content.*.image_url.url` 解码。请求与响应路径均由 Schema 声明，通用适配器无需按模型分支。HTTP 200 只返回文字仍判定为协议失败，不会标记任务成功。

这不是 Google 原生 Interactions 契约。网宿当前的 Interactions 路径返回 404，Images 路径返回 `model_unsupported`；管理台的 OpenAI 类型测试走聊天接口，不能证明 Images 路径可用。平台固定非流式响应，不做自动协议回退。Interactions 专用的搜索 grounding 和 JPEG 格式开关不再在该渠道声明为可用参数。

2026-09-09 根据网宿渠道文档修正尺寸映射：`eca_image_config.aspect_ratio` 和 `eca_image_config.image_size`。文档参数表字段为 `aspect_ratio`，示例中的尾随空格是排版错误，不应发送。真实请求 9:16 / 2K 返回 1536×2752 PNG（5,241,208 字节），符合模型像素对齐后的竖向 2K 输出。比例控件和分辨率控件继续由 Schema 自动投影、传参。档位不代表固定宽高，边长随比例及模型像素对齐变化。

此前 `image_config`、`generationConfig`、`generation_config`、`extra_body.google.image_config` 和顶层 `size` 探针均未生效，原因是没有采用网宿约定的 `eca_image_config`，不能据此断言上游不支持尺寸。已撤下页面中这一过早结论。该渠道文档只声明 10 种比例及 1K/2K/4K，因此不在当前 Gemini Schema 暴露未声明的超宽比例和 512 档位；Flash 保持禁用，未冒充已完成该模型实测。

2026-09-17 再次按上述网关文档手工验证文生图：`model=gemini-3-pro-image`、`stream=false`、`modalities=["text","image"]`、`eca_image_config={"aspect_ratio":"16:9","image_size":"2K"}`，单次请求返回 200，47.234 秒，PNG 实际解码为 2752×1536。响应图片位于 `choices[0].message.content[0].image_url.url`。输出按模型像素对齐，不因比例存在小幅偏差而判失败；此记录不代表所有选项均已逐项实测，也不解释此前图生图未提取到图片的失败。

启用的 Gemini Pro 文生图和图生图共用十种比例选项及 1K/2K/4K 档位，默认 1:1/1K。工作台经 `useProviders` 读取 extended 投影，由 `SchemaParameters` 展示并提交 `aspect_ratio`、`image_size`；Chat/Canvas 共用 `ChatInput` 与浏览器客户端，同名选项经工具的 Schema override 映射进入 generation。`imagewire` 最后将这两个字段写入 `eca_image_config`，UI 不直接提交上游私有信封。`modalities` 与 `stream` 是协议固定字段，不作为用户调参控件。

此前 GPT Image 2 文生图和图生图、Seedream 4.5、Meshy Nano Banana Pro/2 已各完成两次真实生成并校验落盘哈希。GPT 实际为 1536×1024 WebP / 1024×1536 PNG；Seedream 为 2560×1440 / 2048×2048。探针与部署后站点任务验收分别记录，不将直接请求网关的成功冒充为站点任务成功。dev-test 脱敏结果位于 `production/reports/live-images-20260909/`。2026-09-17 的 2.5 Sunburst / Gemini Pro 最小探针见上节「声明与实测边界」。旧站点在 2026-09-09 记录的 GPT 图生图走 JSON `images[].image_url`，与本期线上契约（multipart）不一致；本期按 S2 实测改为 multipart，不把历史记录当作现行协议依据。

回归测试覆盖 Chat 请求路径/认证、1/3/14 张参考图、文字与图片混合返回、data URI 解码、文字响应不得成为图片成功，以及拒绝请求不自动回退。Schema 驱动的参数回归还覆盖：新增声明参数无需改 Handler/Adapter 即进入上游请求体、GPT 图生图 multipart 的 `image[]` 与 `mask` 分片、质量 xhigh / max 校验、声明资产大小限制、历史任务快照模型身份读取，以及 Chat `parameters` 映射的解析与拒绝。OpenAI 和 Seedream 的现有协议保持各自 Schema 声明。

## 旧模型名兼容决策（2026-09-27）

`gpt-image-2` → `gpt-image-2.5-sunburst` 的决策为**不做别名映射，明确以新模型名为准**：

- **Offering ID 不含模型名变更**：`openai/gpt-image-2/text-to-image` / `image-to-image` 是历史沿用的供给项 ID（非模型名），升级前后保持不变；以 offering_id 提交的调用方不受影响。
- **`ai_model` 提交旧名的实际行为（dev-test 实测）**：chat 消息带 `ai_model=gpt-image-2` 返回 202；agent 解析不到该模型时**不报错、不静默换模型**，而是回退到「未选模型」引导路径——`list_offerings` 列出有效模型并请用户选择，同时正常进入积分同意流程。生图不会被旧模型名触发。
- **历史任务展示不受影响**：任务快照保留创建时的模型身份，旧任务仍显示原名（`legacy_task_snapshot_compat_test` 覆盖）。
- **provider 发现**：legacy 投影与 extended 投影均只声明 `gpt-image-2.5-sunburst`。
- **对外说明**：如有第三方脚本以 `ai_model=gpt-image-2` 提交，需改为 `gpt-image-2.5-sunburst`；不改则只会得到模型选择引导而不会生成。若未来收到真实集成方的兼容诉求，再评估是否在 `legacy_models` 加带兼容规则的别名，本期不做。

## 接口依据

- [网宿 Chat Completions 与 eca_image_config](http://doc.model-store.ai/ai-gateway/model/api-detail?code=api-chat-completions1)
- [Meshy 文生图](https://docs.meshy.ai/en/api/text-to-image)、[图生图](https://docs.meshy.ai/en/api/image-to-image)
- [OpenAI 图片生成指南](https://platform.openai.com/docs/guides/image-generation)、[Images 创建](https://developers.openai.com/api/reference/resources/images/methods/generate)、[编辑](https://developers.openai.com/api/reference/resources/images/methods/edit)
- [Gemini 3 Pro Image（企业代理平台）](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/gemini/3-pro-image)、[Gemini 3.1 Flash Image](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/gemini/3-1-flash-image)
- [Gemini 图片生成](https://ai.google.dev/gemini-api/docs/image-generation)、[Interactions API](https://ai.google.dev/api/interactions-api-v1)
- [Seedream 图片生成 API](https://docs.byteplus.com/api/docs/ModelArk/1824121)
