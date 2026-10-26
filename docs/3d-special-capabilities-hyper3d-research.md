# Hyper3D 特殊能力与 API 接入调研

**调研日期：2026-09-30**  
**范围：**仅核对 Hyper3D 官方网站、`docs.hyper3d.ai` API 文档、Hyper3D 官方发布公告及 DeemosTech 官方 GitHub 资料。本文是供应商能力调研，不是本项目代码实现或运行验收记录。

## 结论摘要

- Hyper3D 标准云端 API 的正式文档基址为 `https://api.hyper3d.com/api/v2`。公开目录列出 Rodin Gen-1/1.5、Gen-2、Gen-2.5（共用 `/rodin`）、Rodin Agentic（Beta）、Bang、Generate Texture、Check Status、Download Results、Check Balance 等端点。
- Rodin 生成、Bang 与 Generate Texture 的文件输入使用 `multipart/form-data`。公开文档没有列出通用的预上传、上传 token、远程 URL 输入或跨供应商 task ID 读取接口。Bang 的 `asset_id` 明确是本账号已完成 Rodin 生成响应的顶层 `uuid`；外部模型应以本地文件 multipart 上传。
- Gen-2/Gen-2.5、Bang、Texture Generation 的格式应在任务提交时选择；没有发现标准云端 API 的通用格式转换端点。Hyper3D 网页格式转换工具不能据此视作服务端转换 API。
- ChatAvatar、WorldGen、网页 OmniCraft Partial Edit、HDRI Generator、Mesh Editor、Smart Low-Poly 等产品或网页能力有官方页面/公告，但未在标准公开 API 目录中找到对应调用契约。企业定制能力须向 Hyper3D 洽谈，不能推定存在可直接调用的端点。
- BBox ControlNet 是公开 Rodin 生成参数；Voxel 与 Point Cloud ControlNet 见于面向特定客户的 on-premises 文档，不应归类为标准云端公开 API。
- 下文价格均为官方文档/官网公布的价格信息，不是本账户余额、实际扣费、API key 权限或成功调用的实测结果。提交前仍须按账户实际可用性及响应中的 `consumed` 核对。

## 标准云端 API 目录

所有公开 v2 API 使用 `https://api.hyper3d.com/api/v2` 基址；受保护接口使用 `Authorization: Bearer <API key>`。API 文档称文件式生成请求使用 multipart，响应为 JSON。公开 API access 在官网计划比较中列为 Business 的 Full API access；Enterprise 提供定制 usage/deployment/support 条款。计划说明不是账户授权实测。

| 产品/操作 | 方法与路径 | 可确认输入与内部 ID | 文件、输出与收费说明（文档价） | 官方依据 |
|---|---|---|---|---|
| Rodin Gen-1/1.5 | `POST /rodin` | multipart。图生 3D：1–5 张 `images`，可选 `prompt`；文生 3D：`prompt` 必填并省略 images。无 `asset_id`、外部 task ID 或 URL 输入。通过 `tier=Sketch/Regular/Detail/Smooth` 选此代模型。 | 输出格式可选 GLB/USDZ/FBX/OBJ/STL；材质支持 PBR/Shaded/All 等。文档基础费用 0.5 credits。文档未注明此 API 的图像 MIME 枚举或每图大小上限。 | [Gen-1/1.5 API](https://docs.hyper3d.ai/en/api-specification/rodin-gen1-1-5)、[API Features](https://docs.hyper3d.ai/en/get-started/features) |
| Rodin Gen-2 | `POST /rodin` | multipart。1–5 张 `images` 或 `prompt` 文生；必须明确 `tier=Gen-2`，否则文档称会回退至 Regular/Gen-1.5。无资产 ID / URL 输入。 | 输出格式 GLB/USDZ/FBX/OBJ/STL；Raw/Quad 网格、材质、质量选项。Gen-2 基础 0.5 credits；HighPack 等附加项需按官方价格另计。文档未注明通用图像大小上限或 API MIME 白名单。 | [Gen-2 API](https://docs.hyper3d.ai/en/api-specification/rodin-gen2) |
| Rodin Gen-2.5 | `POST /rodin` | multipart。图生 3D 为 1–5 张图，`prompt` 可选；文档列出 `image_label`、`is_symmetric`、`bbox_condition`、`TAPose`、`use_original_alpha`、`geometry_instruct_mode`、`texture_mode`、`uhd_texture` 等参数。无外部 asset/task ID 或 URL 输入。 | GLB/USDZ/FBX/OBJ/STL；Raw/Quad；PBR/Shaded/Hybrid/All/None；质量预设及面数可配置；HighPack、预览图等选项。详见下方价格解释。文档没有说明一般图像格式枚举/单图大小上限。 | [Gen-2.5 API](https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5) |
| Rodin Agentic（Beta） | `POST /rodin_agentic` | multipart。1–5 张参考图，每张最多 25 MiB；prompt 可省略，服务可按图片生成提示；tier 为 `basic`、`standard`、`pro`；无上游资产 ID/URL 输入。 | 导出 GLB/USDZ/FBX/STL。文档列 basic 0.5、standard 1.0、pro 2.0 credits。API 明确标为 Beta，参数与结果可能不确定。 | [Rodin Agentic API](https://docs.hyper3d.ai/en/api-specification/rodin-agentic)、[Changelog](https://docs.hyper3d.ai/en/get-started/changelog) |
| Bang（BANG） | `POST /bang` | multipart，两种互斥模式：A. `asset_id` 为已完成 Rodin generation 响应的顶层 `uuid`；此模式必须省略 model、image、prompt。B. 上传 `model`；可带参考图、prompt、instruction。不能将外部 provider task ID 当作 `asset_id`。 | 输入模型扩展名为 OBJ、GLB、STL、FBX、USD、USDA、USDZ、USDC。输出格式 GLB/USDZ/FBX/OBJ/STL；`strength` 1–12，`instruction` 非空时启用 instruction mode。固定 0.5 credits/请求（无论 Rodin asset 或上传模型）。文档未给上传大小上限或参考图 MIME/大小枚举。 | [Bang API](https://docs.hyper3d.ai/en/api-specification/bang) |
| Generate Texture | `POST /rodin_texture_only` | multipart，必须直接上传一张 `image` 与一个 `model`；文档未列资产 ID、task ID 或 URL 输入。 | 图片支持 JPEG、PNG、WebP、TIFF、GIF、AVIF、HEIC、HEIF、BMP、JPEG 2000；模型支持 OBJ、STL、glTF/GLB、FBX、USDZ。材质 PBR/Shaded/All；几何导出 GLB/USDZ/FBX/OBJ/STL；分辨率 Basic 2K 或 High 4K，`texture_mode=extreme-high` 对应更高纹理矩阵。文档基础费 0.5 credits，极高纹理档按下方逐项表额外计费。没有公开上传字节大小上限。 | [Generate Texture API](https://docs.hyper3d.ai/en/api-specification/generate-texture) |
| Check Status | `POST /status` | JSON 使用生成响应中的 `jobs.subscription_key`；不是 task UUID。 | 轮询状态；Quick Start 指出等至少 5 秒再首次请求并遵循 `Retry-After`。文档未列独立 credits 收费。 | [Quick Start](https://docs.hyper3d.ai/en/get-started/quick-start)、[Errors](https://docs.hyper3d.ai/en/api-specification/errors) |
| Download Results | `POST /download` | JSON：`task_uuid` 使用生成响应顶层 `uuid`，不是 subscription key。任务完成后调用。 | 返回文件名及下载 URL；URL 会过期，应及时取回文件。文档未列独立 credits 收费。 | [Download Results API](https://docs.hyper3d.ai/en/api-specification/download-results) |
| Check Balance | API 文档目录提供 Check Balance；Changelog 记录可选 `usage_page` 及 `usage` 响应字段。 | 查询已认证账户余额/可选用量；不是费用预留或提交授权。 | 文档未列独立 credits 收费。具体字段应使用当前 API 文档。 | [Check Balance API](https://docs.hyper3d.ai/en/api-specification/check-balance)、[Changelog](https://docs.hyper3d.ai/en/get-started/changelog) |

### Gen-2.5 与纹理任务的价格解释

Gen-2.5 API 页的逐项 Pricing 表将基础 generation 列为 0.5 credits、`tier=Gen-2.5-Extreme-High` 列为 `+0.5 credits`、`texture_mode=extreme-high` 列为 `+2.0 credits`，并说明 **Charges are additive**。因此按该 API 表的字面口径，Extreme-High 几何档是基础费之上的增量，不是单独总价；极高 texture 也是在适用基础费用之上的增量。最终实际扣费以提交响应 `consumed` 为准，不用官网宣传卡片的“from”价覆盖详细 API 价格表。

Generate Texture 页同样把 Base texture generation 列为 0.5 credits，把 `texture_mode=extreme-high` 列为 `2.0 credits`。（2026-10-21 更正：当前该页没有 Gen-2.5 页那样的“Charges are additive”说明，是否相加未写明，见 `provider-schema-architecture.md` 的 Hyper3D 复核记录。）HighPack 的官网/Gen2.5说明是纹理包分辨率升级，Gen-2.5 API页的纹理矩阵还受 `texture_mode`、`uhd_texture` 和 `addons` 影响；不要把各种高分辨率能力简化成相同可叠加规则。请按所选具体 endpoint 的当前 Pricing/Texture Resolution 表核价。

- [Gen-2.5 价格与分辨率表](https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5#pricing)
- [Generate Texture 价格与分辨率表](https://docs.hyper3d.ai/en/api-specification/generate-texture)
- [官网 API 介绍与公开功能表](https://hyper3d.ai/features/api)

### 本地上传、ID 与跨供应商输入边界

1. **Rodin Generation**：公开合同为 multipart 图片文件或 text prompt。未找到公开的预上传 ID、token、远程 URL、外部 provider task ID 字段。Rodin 生成结果获得平台 UUID 与内部 job subscription key；二者分别用于下载、状态查询。
2. **Bang**：可传本地上传模型，也可传 Hyper3D 自己 Rodin generation 完成后返回的顶层 task UUID 作为 `asset_id`。官方文档明确这两种互斥。外部 provider 的 task ID 不满足文档定义；需要先由调用方取得并验证模型文件，再以 multipart 上传。Bang 上传模式另需参考图的条件见端点文档。
3. **Generate Texture**：模型与参考图都必须 multipart 上传本地文件；没有文档化的 `asset_id`/URL 输入。适合作为本地缓存资产的纹理重生成 API 候选，不表示任意模型都经实际兼容验收。
4. **下载回交**：Hyper3D `/download` 返回临时签名 URL。API 文档要求调用方下载生成文件；文档没有承诺该 URL 能重新传回其他 API 或可永久保存。
5. **格式转换兜底**：Rodin/Bang/Texture 可以在生成/处理提交时选支持的输出格式。未在标准公开 API 目录找到 generic convert endpoint。网页 Converter/Mesh Editor 不能替代该缺失的服务端契约。
6. **供应商/格式失败边界**：如果目标是跨 provider 或需要 Hyper3D 未列出的输入格式，现有公开契约只支持调用方本地取得字节后测试 multipart 上传；不支持把远程 URL、任务 ID 当作自动桥接。Bang 与 Texture 对模型扩展名的差异必须按各自 API 分别校验。

## 官网能力与公开 API 状态

| 官网产品/能力 | 官方已说明的产品能力 | 公开 API 状态、输入/输出与不确定性 | 官方依据 |
|---|---|---|---|
| ChatAvatar（头像/脸模设计） | 从文字或参考肖像生成完整 avatar；页面介绍细节脸、匹配身体网格、PBR 与 facial controls、blendshapes、ARKit-compatible expressions、visemes/lip-sync 等。独立于 Rodin 普通物件生成。页面 FAQ 称 Enterprise 可联系讨论 API access、custom workflows、pipeline integration。网页应用曾显示 V0.7 (Beta)。 | 未在标准 API 目录或公开 SDK 证据中找到 endpoint、请求字段、图像格式/大小、输出格式、task/asset ID 规则或按调用价格。状态：网页产品有官方证据；公开 API 合同未证实。不能用 Rodin `/rodin` 推断或伪装 Avatar API。 | [ChatAvatar 产品页](https://hyper3d.ai/features/chatavatar)、[ChatAvatar 网页应用](https://hyper3d.ai/chatavatar/8dd42f33-f2d6-40f9-9706-9360f6f1617e?lang=null)、[Pricing](https://hyper3d.ai/pricing) |
| WorldGen（世界/场景生成） | Hyper3D 官方发布公告称从单张图生成含独立、可编辑、可交互物件的场景；Rodin 产生对象 mesh，背景使用 3D Gaussian Splatting，并估计对象空间关系、位姿及部分物理属性。 | 未在标准 Rodin API 目录或官方公开 SDK 中找到 WorldGen endpoint、上传/任务/下载协议、场景包格式、成本或 API 权限。公告中提到已有项目投入生产不等于开放通用 API。状态：官方发布的产品/研究能力有证据；公开 API 未证实。 | [Hyper3D 官方 PR Newswire 公告](https://www.prnewswire.com/news-releases/hyper3d-launches-worldgen-to-turn-single-images-into-editable-3d-scenes-302873750.html) |
| OmniCraft Partial Edit / 网页“局部编辑” | 官网称可以编辑 3D asset 选定区域而无需重建整体；主站将其与 BANG 等能力并列展示。 | 未找到 Partial Edit endpoint、区域 mask/范围输入 DTO 或输出/价格契约。Bang 文档中的 `instruction` 用于按指令切分/处理模型；其文档没有区域选区字段，不能当成网页 Partial Edit 的等价 API。状态：网页宣传已证，API 未证实。 | [Hyper3D 首页](https://hyper3d.ai/)、[Bang API](https://docs.hyper3d.ai/en/api-specification/bang) |
| 3D ControlNet：Bounding Box | Rodin Gen-2/2.5 schema 提供 `bbox_condition` 参数，以三个整数表示 X 宽、Y 高、Z 长；属于生成端点的可控输入，不是独立任务端点。 | **公开云端参数**：`bbox_condition` 数组长度为3；公开 BBOX 文档给范围 1–2048。实际 generation tier/端点仍受其自身契约约束。 | [BBOX Control API说明](https://docs.hyper3d.ai/en/api-specification/bbox-control)、[Gen-2.5 API](https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5) |
| 3D ControlNet：Voxel / Point Cloud | 官网展示Voxel与Point Cloud 控制能力。 | 详细字段仅见于 Hyper3D on-premises docs，并明确 ControlNet API 限 specific customers。文档描述 `voxel_condition` Base64 数据和 `pcd_condition` 点坐标及权重；未确认这些字段适用于标准云端 `/api/v2/rodin`。应归类为客户/部署专属，不纳入普通云端接口。 | [On-premises ControlNet](https://on-premises.docs.hyper3d.ai/all-in-one/controlnet)、[On-premises Rodin Mesh](https://on-premises.docs.hyper3d.ai/mesh-generation/rodin-mesh) |
| OmniCraft 3D Mesh Editor（含 mask、雕刻、paint、remesh） | 网页工具可导入模型或从基础几何开始；雕刻、PBR paint、mask regions、重建拓扑/remesh；支持整场景/选中mesh导出；官网声称本地浏览器处理、Nothing uploaded、无需登录，最高导入模型200 MB。 | 未发现可提交服务器任务的公开 API。该工具的本地处理说明意味着不能把它当服务端批量处理或项目 backend API。FAQ列出OBJ、PLY、STL、SGL输出；页面转格式建议先导OBJ再使用网页转换工具。 | [3D Mesh Editor](https://hyper3d.ai/features/omnicraft/3d-mesh-editor) |
| OmniCraft HDRI Generator（360环境贴图） | prompt或最多10 MB参考图生成360° skybox；网页称preview约30秒，8K包含 `hdr_high.png`、`hdr.exr`、`depth.png`；预览/regenerate免费，解锁订阅者免费、普通会员1 credit。 | 未找到标准 API 端点或 request schema。它生成的是HDRI/skybox与深度图，不是WorldGen完整多对象3D场景。状态：网页产品有证据，公开 API 未证实。 | [AI HDRI Generator](https://hyper3d.ai/features/omnicraft/ai-hdri-generator) |
| Smart Low-Poly | 官网价格计划将其列为Creator及以上Rodin能力，营销页描述为优化低面数版本。 | 未在公开 API 目录找到专门对既有模型执行 Smart Low-Poly 的端点/字段。Gen 的 face-count、mesh_mode 参数不等于已证明同一网页 Smart Low-Poly workflow。 | [Pricing](https://hyper3d.ai/pricing)、[首页](https://hyper3d.ai/) |
| OmniCraft Image Remix | 网页支持多参考图 remix、会话式图片编辑和为 Rodin 准备转面参考图。某官方功能页称 Gen mode最多3图，JPG/JPEG/PNG/WebP，压缩后每图3 MB。 | 未找到对应标准 API endpoint/合同；不计作3D模型端点或可复用公开API。 | [AI Image Remix](https://hyper3d.ai/features/omnicraft/ai-image-remix) |
| 其他 OmniCraft：Image Enhancer、AI Texture网页工具、Model Search、SVG-to-3D、Format Converter | 官网 Features 矩阵列出多种网页工具。Generate Texture API另有独立、明确端点，不能将其泛化为所有 OmniCraft 工具的 endpoint。 | 除本文件公开 API 表中 `rodin_texture_only` 外，未在 Rodin API spec 找到上述工具的标准端点、登录/上传合同及计费。状态：逐项公开 API 未证实。 | [Features Matrix](https://hyper3d.ai/features)、[Generate Texture API](https://docs.hyper3d.ai/en/api-specification/generate-texture) |

## 官方文档与 SDK/版本注意点

- API 参考明确说明自己是 Rodin API 文档，且所有 v2 endpoints 使用统一基址；导航目录没有列出 ChatAvatar、WorldGen、OmniCraft 专用服务端API。
- 2026-09 的官方 API changelog 增加 Rodin Agentic 文档与 Bang 参数；历史更新列出 Texture endpoint、Gen-2.5 参数与 ControlNet等信息。应以当前端点文档字段为准，不从旧示例拼接口。
- DeemosTech 官方组织公开 `rodin3d-skills`、`rodin3d-bang-skills`、ComfyUI-Rodin 等集成，证明 Rodin/Bang 的公开集成示例；没有从这些项目推断 ChatAvatar/WorldGen/OmniCraft API。
- API 提交响应可能 HTTP 201 但 body 中携带 `error`。按官方 Errors 文档还须检查 `error` 与有效顶层 `uuid`；不可仅凭 HTTP status 判接受成功。
- 没有公开资料的输入大小、账号权限、格式实际兼容和最终输出文件集合，一律留为“待供应商确认/待账户实测”，不能用网页 UI 限制或营销文案补齐。

## 官方来源索引

- [Hyper3D API Introduction](https://docs.hyper3d.ai/en)：标准云端 API 基址、认证与文档范围。
- [API Features](https://docs.hyper3d.ai/en/get-started/features)：Rodin API generation families 与异步状态流程。
- [API Changelog](https://docs.hyper3d.ai/en/get-started/changelog)：端点/参数版本沿革。
- [API Errors](https://docs.hyper3d.ai/en/api-specification/errors)：API 业务错误、状态/下载 ID 差异及重试规则。
- [Official API workflow / API feature overview](https://hyper3d.ai/features/api)：官网列出的 API 功能与访问计划。
- [Official DeemosTech GitHub organization](https://github.com/DeemosTech)：官方公开集成代码目录。
