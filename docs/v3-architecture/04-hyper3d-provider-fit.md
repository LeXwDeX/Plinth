# Hyper3D / Rodin Gen-2.5：Provider 扩展性验证

状态：**2026-10-20 起 Rodin 文生/图生 3D 按路线 B 启用，同日在开发测试环境通过公开验收（#188）**；Bang/Texture 仍禁用。下面除最后一节外都是历史记录。本页其余“无 Key”与首版档位是 2026-09-29 的历史边界，不能作为当前运行配置或最终协议真源；原生计价的新增合同见下文。2026-09-29 查阅 [Hyper3D 官方概览](https://docs.hyper3d.ai/en)、[Quick Start](https://docs.hyper3d.ai/en/get-started/quick-start)、[Rodin Gen-2.5](https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5)、[状态](https://docs.hyper3d.ai/en/api-specification/check-status)、[下载](https://docs.hyper3d.ai/en/api-specification/download-results)、[错误](https://docs.hyper3d.ai/en/api-specification/errors)及[Features](https://docs.hyper3d.ai/en/get-started/features)。当时没有 Hyper3D API Key，历史试验不构成「真实生成可用」证据；当前凭据只按运行配置核验，不从这段历史推断。

## 兼容性结论

| 扩展面 | 现有抽象是否足够 | 接入选择 |
|---|---|---|
| Provider/Model/Offering、参数、默认值、输入数、路由、状态词表、运行设置 | 足够，由 Product Schema 声明并由 Catalog 校验 | 新增一个 `hyper3d.rodin` connection；文生/图生 3D 各一个 Offering，保留 Offering ID/revision 快照 |
| 同协议新 Provider | 仅已有协议可纯配置 | Rodin 不是现有 `image-sync`、Meshy 或 Tripo 协议；不能把它伪装成任一适配器 |
| 上游线协议 | 需一个隔离补丁 | 新 Adapter 只实现 `Start`/`Observe`/`OpenAsset`，不写公开 Task、页面或第二套状态机 |
| 积分展示与同意 | 现有 `estimated_credits` 是整数；官方基础价为 0.5 Hyper3D credit，Extreme-High 与极高纹理另加价 | 不把 0.5 四舍五入成「1 积分」，也不将供应商 credit 偷换为平台 credit；在单位/换算与现有人工同意语义确定前保持 Offering `enabled:false`、预估值 0 |
| 产物域名 | 官方下载文档示例使用 `example.invalid`，没有可作为生产 allowlist 的真实 CDN 域名 | `asset_hosts` 暂为空，真实签名 URL 不得全网任意抓取；获得 Key 后以实际回包和官方/账户支持证据登记精确 host，再做 SSRF、重定向与过期验收 |

因此这次试验验证的是**原有 Provider Port 能承载新异步协议，产品参数与运行接线继续由 Schema 控制**，同时明确「零代码」只适用于相同协议。架构不应为了零代码口号把任意异步上游协议塞进配置解释器。每种不可约 wire 形状是一个小 Adapter；平台任务与资产事实保持统一。

## 协议与状态约束

Rodin 基地址为 `https://api.hyper3d.com/api/v2`，Bearer 鉴权。`POST /rodin` 是 multipart；文生 3D 要求 prompt，图生 3D 接受 1—5 张 `images`。必须显式传 `Gen-2.5-*` tier，省略会落到较旧模型；首版固定 `Gen-2.5-Medium`，并显式指定 `geometry_file_format=glb`，由 Offering `provider_options.request_template` 给出。首版的必需模型只接受 `model/gltf-binary`，Adapter 在取回后核对 GLB 文件头、版本、JSON 块与实际字节总数；即使上游分块下载而没有 `Content-Length`，也不能把截断或多余字节的文件保存为成功模型。HTML/JSON 错误页同样不得作为模型。发布前再比对文档中的字段/价格。HTTP 201 只是请求被处理，响应中有应用层 `error` 时不能接受；缺顶层 `uuid` 或状态订阅键也不能建立可观察的任务。[来源：Rodin Gen-2.5](https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5)。

| 阶段 | Provider 私有数据 | 平台事实 |
|---|---|---|
| Start 成功 | 顶层 `uuid` 是下载用 task UUID；`jobs.subscription_key` 是状态用订阅键，需在 Attempt 检查点分开保存 | Generation 维护稳定平台 Task ID/Attempt ID；上游 ID 不替换 Task ID |
| Observe | `POST /status` 只用 `subscription_key`；所有 jobs 为 `Done` 才可请求下载，任一 `Failed` 即失败；首次至少 5 秒后轮询、退避与 Retry-After 遵循既有调度语义 | `QUEUED/RUNNING` 根据 Observation 推进，不在 Adapter 里另建公开状态机 |
| 下载 | `POST /download` 只用顶层 `uuid`；可能在提前请求时返回不完整列表，签名 URL 有效期有限 | 必需模型取回、格式/大小/hash 校验、本地 READY 后 Task 才能 `SUCCEEDED`；不可把签名 URL 当永久资产 |
| 结果不明 | 非幂等付费 `POST /rodin` 断网/超时不能盲重发；429/5xx 只按已知是否受理与现有检查点策略处理 | 未知状态保留原 Attempt 证据，停机对账；不能把「重试」变成重复扣费 |

[Quick Start](https://docs.hyper3d.ai/en/get-started/quick-start)和[状态 API](https://docs.hyper3d.ai/en/api-specification/check-status)要求先等全部 job 完成再下载，且状态轮询至少等待 5 秒。[下载 API](https://docs.hyper3d.ai/en/api-specification/download-results)明确两种 ID 的不同用途和签名 URL 过期风险。具体状态值 `Waiting / Generating / Done / Failed` 只在 connection `status_mapping` 声明，公开状态仍由 Generation 决定。

## 无 Key 验收与启用门槛

本分支可验证：Catalog 加载与公开投影不泄漏 Key 设置，两个 Offering 保持不可提交；Adapter 的 httptest 覆盖 multipart、1/5 张输入、两种 ID、状态聚合、body-level error、429/Retry-After、坏 URL/禁用 host、结果列表、GLB 内容负控和物化失败；原有 Meshy/Tripo、任务、资产与鉴权回归仍通过。无 Key 时测试环境也应返回 Hyper3D 不可用或不列出，不准借 mock 结果宣称真实付费链通过。

真正启用前必须补齐并留下同提交证据：真实账户 Key（仅运行时设置）、实际签名下载 host 的精确 allowlist、平台积分与 Hyper3D credit 的展示/计算/人工同意映射、真实 1 张与 5 张图片及文字任务、至少一个失败/超时/结果不明场景、产物本地 READY 与跨用户下载负控。启用必须修改 Offering `enabled` 与 revision，重新跑 Catalog/Adapter/Generation/API/页面和 dev-test 测试栈验收；`healthz` 或 Provider 列表不是生成验收。

性能待办：当前非幂等提交在预检 multipart 后，HTTP 层还会为可重放传输再落一次临时文件。5 张大图与并发提交会放大临时磁盘占用；启用或压测前，应在共享 HTTP 传输接口中复用已预检文件，同时保持派发检查点与未知结果不得盲重试的语义。


## 原生价格兼容性补充

新增可选 `native_pricing` / `native_quote`，使用精确十进制字符串、明确供应商单位和冻结 Prepared/Grant，总额不是旧整数积分的缩放。旧 int 估算与历史 hash 保持，混合统计按单位分组；0.5 不会变成 0 或 1。通用价格能力不替代 Hyper3D Adapter 的最终 wire 资格，也不意味着本页旧档位或未知 Texture 价格已被重新确认。真实启用仍需对应 Issue 的完整报价、必需产物字节、鉴权及测试栈证据；不应把历史“没有 Key”继续当作未实现的默认解释。


## 2026-10-16 原生候选更新（未启用）

当前 Schema 已取代上文历史零估值与 Medium 首版说明：两个 Rodin 候选固定当前 Gen-2.5 Extreme-High、Raw/2,000,000、PBR、extreme-high 纹理及 HighPack/UHD，按[当前端点价格](https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5#pricing)冻结基础0.5、几何0.5、纹理2，合计3原生单位。两个 Bang 候选分别限定已交付 Rodin Task 和已归属静态 GLB+单参考图，按[独立 Bang 端点](https://docs.hyper3d.ai/en/api-specification/bang#pricing)固定0.5；业务模型别名不作为上游版本判据。Texture 仅有私有协议测试，其最高档表价是否加总仍未知，不登记猜测报价。

全部候选仍禁用、host allowlist 为空；原生必需 GLB wire 不等于完整六格式交付，尤其3MF没有在这些端点获得证明。额外格式/转换、账户权限、实际字节、鉴权、真实费用和Agentic资格均未由本轮离线测试建立。1/3/5图顺序测试是 Adapter 层协议证据，不是完整 Agent 选源交付。最终可执行边界由精确 Offering 修订与对应原生 Issue 验收决定，不依赖本页历史 Key 状态。


## 2026-10-20 启用更新（#188）

Rodin 两个 Offering 改为启用，修订 `2026-10-20.188.1`。当前合同以 [Provider/Schema 合同的 Hyper3D 一节](../provider-schema-architecture.md)为准，这里只记与上文历史结论不同的地方：

- 下载 host 不再为空：只登记 `file.hyper3d.com`。依据是 2026-10-16 一次真实下载回包，以及 2026-10-20 对其中签名 URL 的只读 Range 请求（直接返回 206，没有重定向）。
- Extreme-High 一次返回三份文件：PBR pack GLB、附带的 Draco 原始网格和 `preview.webp`。`provider_options.download_files` 声明只取 PBR GLB 和预览图。原始网格现已能通过有界 Draco 检查（2026-10-20 用当前 main 离线复验），但无贴图，所以不交付。
- 纹理去掉 HighPack 与 UHD，`extreme-high` 为 6K，报价仍是 3。这样主 GLB 落在生成模型转换 profile 内，glTF 可以免费补齐；其他格式按现有转换途径的大小合同处理。
- 2026-10-20 文生、单图、双图公开 Task 均通过：主 GLB 100–111 MB，6K base color，每次实扣 3 credits；跨用户 404、真实失败/结果未知路径未覆盖，见 #188。上文的性能待办（multipart 临时文件复制两次）仍未处理。

