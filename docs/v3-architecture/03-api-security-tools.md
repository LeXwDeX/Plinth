# G1：API、安全与 Tool/Skill/MCP 边界

状态：G1 契约与历史基线；**不是全部产品代码或测试环境已验收的声明**。以下标为「现状」的段落专指固定提交 `a32a93799c08cf98c6f5e0481e233809233b1b7c`。2026-09-29 工作分支的增量状态以下表为准，最终证据记录在 [05](05-local-regression.md)。路径由 `internal/catalog/default_catalog.json` 声明，表内字面路径是历史快照值，运行时仍须以装配后的 Schema 为准。

## 1. API 身份与单一事实

### 本分支实现与验证状态

| 范围 | 源码状态 | 仍待验证 |
|---|---|---|
| API 身份与 owner | **代码与定向负控已接入**：原生/兼容读取受 owner 范围约束，Agent consent 由 session/turn 身份校验；仅 Bearer 身份不能作人类决定。 | 全 route/method 动态 owner/admin/吊销矩阵，以及同提交测试与部署读回。 |
| CSRF | **代码与定向负控已接入**：带 Cookie 的写请求在 Origin 与 Sec-Fetch-Site 同时缺失时拒绝；冒烟脚本改送同源 Origin。 | 目标环境开关读回、HTTPS 代理信任配置、真实 Cookie `Secure` 属性和逐路由浏览器/机器回调验证。 |
| Public Schema | **显式白名单代码与定向负控已接入**。 | 全公开入口 canary 检查，含 inline、discovery、兼容投影、ETag/304 的同提交结果。 |
| Hyper3D 运行配置 | **代码边界已声明；未启用**：无 Key 时关闭 Offering，不公开服务端设置。 | 没有真实 Key 与下载 host 证据前保持关闭，不可声称真实调用通过。 |
| 付费人工同意 | **代码已接入，待同提交与环境验收**：`PreparedInput` + 单次 `ExecutionGrant` 精确绑定 owner/session/Offering 修订/输入 hash/操作幂等键；Agent、原生任务、画布直提、Plan 共用；旧兼容写入口 403。`approved_credits` 产品字段已移除。 | 浏览器确认、网络未知结果、重启恢复、真实小额生成。按批准模式授权是已定目标，未实现（见 `docs/product.md`）。 |

**现状。** `internal/transport/httpapi/server.go:405-555` 注册原生 `/api/v1/*`、兼容 `/api/*`、管理、Chat/Canvas、Provider 回调；兼容能力路由还按 Catalog 中 `capabilities[].legacy_api` 逐项生成 POST、GET、DELETE 和可选资产 GET（`:559-589`）。HTTP 外层顺序为安全响应头、同源 CSRF、请求日志、panic 恢复、mux（`:377-389`）。原生 `withIdentity` / `withPrincipal` 验证身份；`taskReadScope` 只对拥有 `tasks.read.any` 的 principal 开放全局读，其余限制 owner（`:591-623`）。原生任务创建、读、事件、取消和产物分别调用 Generation 的 Submit/GetSummaryScoped/EventsScoped/Cancel/OpenAssetScoped（`:2210-2425`）。兼容层转换协议外形，但不能形成第二套 Task/Attempt/Asset 状态机。

**目标。** canonical 写与读事实是 Generation 的稳定 `TaskID`、`Attempt`、`AssetID`、状态与事件序号；Chat session、Processing Plan、Canvas 和旧 API 只能保存关联或投影。每个创建入口共用同一个应用服务校验：身份、session/asset owner、Offering ID 与 revision、输入版本、Schema 参数/依赖、幂等键及必需人工同意。兼容客户端可保留旧字段、错误外壳和 URL token，但映射回同一任务及已 READY 的资产；不能把上游 remote job ID 当平台任务 ID，不能从历史读触发付费 Submit。收费直连无法证明人审时先封闭。`request_consent` 只表达意图，实际授权必须在具体生成工具构造并冻结输入之后独立决定；3D 处理面板按步骤确认。不恢复数字报价批准功能。

**付费准入必须单独证明。** 实现中的授权身份绑定 `(owner, session, offering_id/revision, prepared_input_id/hash, operation_id, idempotency key, plan_step_id)`，首次提交前由真实用户独立决定；Task 创建与 Grant claim/bind 在同一 SQLite 事务。`PreparedInput` hash、Plan version 或费用数字本身均不是批准。逐入口矩阵包括 Chat、Canvas 直提、Processing Plan；旧兼容付费写入口暂时关闭。此机制落实现行的逐步人工同意边界，不是新增报价批准。

ExecutionGrant 按会话批准模式签发（规则见 `docs/product.md`）。自动模式自动授权。手工模式下，文生 3D、图生 3D、换贴图须由真实用户决定，其余步骤自动授权。冻结输入、Grant 精确绑定冻结输入和幂等身份、不盲目重发、历史视图不创建付费任务，这些规则不变。

**现状**：代码仍实现旧规则，即每个新增付费步骤都由真实用户独立决定。新规则是已定目标，未实现。

## 2. 路由、授权与 CSRF 矩阵

下表按权限相同的路由族分组，覆盖 `routes()`、`registerLegacyTaskContract`、Chat/Canvas 和管理注册函数的固定快照。G1 阶段须从当前注册与 Schema 只读生成每个实际路由的 method/path/handler/permission 清单并逐项审定；C 阶段再把该清单做成自动回归；条件注册的 Processing Plan、Schema 驱动的 capability 路径和 SSO/Provider 回调不能仅凭静态字面列表作穷尽断言。`C` 表示该浏览器 cookie 写操作应受 CSRF 保护：**现行 middleware 仅在 `CSRFOriginEnforced=true` 时检查，当前部署的配置值本轮未核；目标要求实际启用并验证**。`—` 表示 GET 只读或独立的 webhook 验签/令牌边界。权限列的「目标」若高于现状，不能误读为已实施。

| 路由族（快照） | 方法 | 现状主体与作用域 | 目标服务边界 | CSRF 适用 |
|---|---|---|---|---|
| `/`, `/healthz`, `/readyz`, `/api/health`, `/api/version`, `/api/v1/schema`, `/api/v1/chat/skills` | GET | 公开；健康/公开 Schema/Skill 投影 | 仅公开字段，健康不泄露接线；Schema 按第 5 节 | — |
| `/api/auth/login`, `/api/auth/mock`, `/api/auth/session`, `/api/auth/password` 及 SSO 重定向/回调/退出 | POST/GET/DELETE，依路由 | 会话服务；mock 受运行配置约束 | 登录/口令变更/退出按用户与 session 记录，回调校验 state；环境不开放 mock 时不可因旧路由存在而启用 | cookie 写请求 C；跨站 SSO 回调另有 state 边界 |
| `/api/v1/agent/params`, `/bind`, `/consent` | GET/PUT | 身份包装器；操作当前用户 Agent 设置与同意 | 不用客户端 `approved` 字段推断其它请求已获批准；同意仍需现行人工路径 | PUT C |
| `/api/v1/offerings`, `/api/providers`（含 `projection=extended`） | GET | 已认证；Catalog/运行可用性投影 | Public Offering 白名单；不可用显式 `available:false`，不泄露配置名/密钥/私有 URL | — |
| `/api/v1/assets`, `/api/v1/assets/{assetID}`, `/api/assets/upload`, `/api/assets/{assetID}` | POST/GET | 已认证；输入资产上传/下载经 owner 访问 | Asset 服务验证 owner、角色、大小与媒体类型；受控本地下载 URL | POST C |
| `/api/v1/model-captures`, `/{captureID}` | POST/GET | 工作分支新增；Cookie/owner 及来源 Task/模型 Asset/PNG 绑定 | 不接受跨 owner、不完整模型或客户端伪造来源；Capture 是可核验的客户端像素记录，不冒充服务端 renderer | POST C |
| `/api/v1/scenes/{sessionID}/objects`、`/{objectID}`、`/representations`、`/apply`、`/snapshot` | POST/PATCH/DELETE/GET | 工作分支新增；session owner 与 object CAS 校验 | Scene 服务保留版本/墓碑/候选，不写 Task 终态；候选应用是显式动作 | 写请求 C |
| `/api/v1/scenes/{sessionID}/render-manifests`、`/{manifestID}` | POST/GET | 工作分支新增；session owner 和 Scene 输入校验 | POST 只接受 frame；输出像素/hash 由服务端渲染，Generation 校验投递资产与 Manifest 完全一致 | POST C |
| `/api/v1/tasks`, `/api/v1/tasks/{taskID}`, `/cancel`, `/events`, `/assets/{assetID}` | POST/GET | 创建/取消 owner；读/事件/下载走 `taskReadScope` | 写入仅 owner；读由 owner 或 `tasks.read.any`，资产 READY/授权后打开；事件同任务作用域 | POST C |
| `/api/v1/skill-runs`、`/{runID}`、`/steps/{stepKey}`、`/review` | POST/GET | 工作分支新增；session owner、recipe/Offering revision、步骤身份与状态校验 | SkillRun 只编排，不自建 Task 状态；每个付费步骤经冻结输入与单独人工 Grant，输出 READY 后可审核下一步 | POST C |
| `/api/v1/processing-plans`, `/{planID}`, `/{planID}/retry`, `/{planID}/resume` | POST/GET | 仅在 processing 装配时注册；已认证，Plan 服务核验归属 | Plan 不自建任务；每个付费后续步骤经现行人审，否则停止；重试不得重提已成功步骤 | POST C |
| `capabilities[].legacy_api.path` 与可选下载路径、`/api/tasks`, `/api/meshy/stream/{type}/{taskID}`, `/api/gpt-images/{filename}` | POST/GET/DELETE | 兼容路由；多数任务读走 principal scope；旧生成图片另有兼容鉴权 | 与 canonical 同 Task/Asset/owner 事实，legacy token 只作可验证映射；不能返回未经物化的远端 URL | POST/DELETE C |
| `/api/chat/sessions`, `/{sessionID}`, `/messages`, `/{messageID}`, `/stream`, `/turn/cancel`; `/api/prompt-enhance` | GET/POST/PATCH/DELETE | 身份包装器；Chat 服务按 principal/session 归属处理 | session ID 是归属键，同名不串；LLM/工具输出不改变权限；流读取仍核验 owner | 写请求 C |
| `/api/canvas/projects`, `/{projectID}`, `/nodes`, `/api/canvas/merge-images` | GET/POST/PATCH/PUT/DELETE | 已认证；删除项目和 merge 显式 `IsAdmin`；其余走 Canvas 归属 | 项目/节点由服务层核 owner+session；Canvas 生成不能绕过统一 Submit 与成本门禁 | 写请求 C |
| `/api/meshy/balance`, `/api/cache-status`, `/api/meshy/proxy-asset` | GET | 分别有身份包装器/代理边界 | 区分 Provider 账户余额与用户预估用量；代理只接受声明的受控资产/目标，限制 SSRF 与缓存越权 | — |
| `/api/admin/users`, `/{userID}`, `/audit-logs`, `/generations`, `/{taskID}`, `/{taskID}/download`, `/usage`, `/balance-summary`, `/user-assets/{userID}`, `/backfill-status` | GET/POST/PATCH 等依注册 | 每组检查 Catalog 声明的管理 permission（`administration_*.go`）；Generation 下载独立服务 | 后台跨 owner 读必须有具体 permission；变更记录 actor、对象、结果与原因，敏感值不入审计 | 写请求 C |
| `/api/v1/provider-assets/{token}`、Provider webhook/status 回调 | GET/POST | 签名/secret/Provider 边界，非用户 cookie 的 owner API | token 有期限、受限用途/对象；webhook 验证并只投递内核观察，不直接置公开终态 | —；独立签名/secret 校验 |

**CSRF 限度与本轮修复。** 表中的 `C` 不能作为现行部署已启用 CSRF 的证据；先读回目标环境 `CSRFOriginEnforced` 的实际生效配置，再逐路由动态验证。基线 `security_middleware.go` 在 `Origin` 与 `Sec-Fetch-Site` 同时缺失时放行。本轮实现分支已令**携带 Cookie 的写请求**在双缺失时拒绝；合法同源浏览器请求和无 Cookie 的独立验签机器回调分别测试。进一步的目标是比较可信外部 scheme/host，明确非浏览器客户端的显式凭证与权限作用域，并逐路由验证 SSO callback、Provider webhook 的边界；不能单凭旧行为断言已存在可利用的 CSRF 漏洞。dev-test 当前入口为 HTTP，session Cookie 在 `secure_mode=auto` 且 public app URL 为空时不带 `Secure`，此环境限制须在 HTTPS 入口和真实 Set-Cookie 验收中单独关闭。

## 3. 会话、owner 与错误语义

**现状。** 会话 cookie 是签名的 versioned claims，含 session ID、签发/过期时间及用户；服务端会话记录可单条或用户级吊销，验证时比较用户、时间和吊销状态（`internal/authentication/session.go:14-100`）。旧 cookie 有固定截止的双读兼容。Cookie 的 HttpOnly/Secure/SameSite 由认证 Schema 与运行环境决定（`service.go:514-582`），不能只凭字段存在断言部署时具体值。当前 `writeDomainError` 映射 invalid→400、conflict→409、not_found→404、forbidden→403、unavailable→503、rate limit→429；不同兼容层有各自线协议外壳。

**目标。** 401 表示未认证、无效或已吊销 session；403 表示已认证但缺管理权限或不可执行操作；对按 ID 读取其它用户的 session/Task/Asset/Plan 统一返回 404，避免对象存在性探测。列表不显示他人资源。管理员跨 owner 读取只由明确 permission 授予，不因 UI 隐藏/显示菜单决定。写操作在应用服务再次校验 owner、角色与目标状态，工具、HTTP 和兼容入口共用边界；日志保留内部 cause，响应仅给稳定错误 code、Schema 声明的安全文案和可选重试提示，不回显 Provider 私有错误、key、路径或 opaque checkpoint。404 与 403 的具体资源规则写成测试矩阵，避免管理权限被普通 owner 规则意外收窄。

**事件/状态投影。** 公开 Task 状态只来自 Generation 状态机及 Product Schema `task_states`：`QUEUED → RUNNING → MATERIALIZING → SUCCEEDED` 或终态 `FAILED/CANCELED`。上游完成不等于 Asset READY。HTTP 列表、详情、兼容协议、SSE 和 Agent `get_task/observe_task` 使用同一版本/事件序号与公开错误白名单；可选资源失败单独表示，不回退成功 Task。SSE 连接建立和每次恢复都用 scope 校验；断线以 cursor 增量恢复，不把重复事件解释成重复提交。`internal/transport/httpapi/server.go:2718-2737` 已有 public event 字段选择，目标是在所有事件外壳中保持一致。

## 4. 资产下载、缓存和后台审计

**现状。** 原生和兼容任务产物 GET 都调用 `generation.OpenAssetScoped`：先按任务 scope 取 Task，再按 Asset ID 在该任务资产中查找（`module.go:842-873`；`server.go:1489-1563,2377-2425`）。两者记录下载审计、设置类型/长度/SHA256 ETag、`nosniff` 和 Schema 的 immutable cache-control；输入资产 GET 另用 private cache-control。后台 `/api/admin/generations/{taskID}/download` 先在管理路由按 `administration.generations.permission` 授权，再由独立的 `GenerationDownloadService` 读取详情与资产，单文件直接返回、多文件打 ZIP；个别文件读取失败会跳过，全部失败才报错（`administration/generation_download.go:160-243`）。这不是普通用户下载路径。

**目标。** 受 cookie 保护的私有资产不能被共享缓存跨用户复用：逻辑授权缓存键至少绑定 owner、Task/Asset ID、内容 hash 和访问语义；物理内容寻址存储可以跨 owner 去重，但每次读取仍须重新授权，不能以物理 hash 命中代替 owner 校验。明确区分静态公共构建资源、私有输入资产、私有任务产物与短期 Provider 能力 URL。审查当前 immutable cache-control 在 CDN/浏览器/反向代理中的具体效果后冻结 header；私有响应优先 `private`，权限变化或吊销后必须明确缓存行为。ETag 只能在授权之后比较，304 不能成为探测或免鉴权入口。下载文件名和 Content-Type 取安全投影并加 `nosniff`；`Content-Disposition` 不接受未清理的换行/引号。后台 ZIP 若部分失败，响应须显式带缺失文件清单/审计记录，不能让用户误以为完整包；具体兼容外壳待定。任何历史/预览 GET 都不能顺手触发上游付费转换。

审计最少包含 actor/session、动作、目标稳定 ID、owner 范围、结果码、时间、请求关联 ID、Offering ID/revision（提交类）和资产 ID（下载类）；不能记密钥、cookie、签名 URL、原始 prompt 中的敏感内容、Provider 私有 job claims。管理员的跨 owner 查看、批量下载、用户/角色修改单独标 action。读回审计时仍按管理 permission 过滤，审计写失败的处置策略需在代码阶段决定并测试。

## 5. Public Product Schema 白名单

**现状。** `ApplicationSchema.Public()` 复制较宽的 Runtime、Chat 等对象，再清除部分内部字段（`internal/catalog/catalog.go:258-295`）。`/api/v1/schema` 使用此投影并以公开内容形成 ETag；Web 首屏也把它注入 inline JSON（`server.go:700-716`、`webui/host.go:113-128`）。已有测试排除 configuration、provider connections 和若干字段，但这**只证明元数据投影过宽的风险，不证明密钥明文已泄漏**。当前 SPA 仍消费部分 Asset proxy 规则，不能突然删整块 runtime。

**目标字段白名单（新增字段默认不公开）：**

| 投影 | 允许的字段类别 | 禁止的字段类别 |
|---|---|---|
| `PublicProductSchema` | 公共 revision、品牌/导航/页面文案、能力与任务状态标签、公开路由标识、合法 UI 控件/顺序/选项、可展示价格与说明 | `application.runtime.configuration`、环境变量名、内部 coordinator/租约/队列、cache root/shards、Provider 账户接线、代理上游目标、管理内部配置、任意新增 runtime 字段 |
| `PublicOffering` / 扩展 discovery / 兼容 providers | Offering ID+revision、模型/能力、可用性及安全原因、参数类型/范围/默认值/选项/展示、输入输出格式/大小、可展示成本 | API key/base URL/credential setting、私有 Adapter wire、上游账户余额、内部重试与 checkpoint、Provider remote ID |
| `PublicChat` / Skill 目录 | Agent UI 所需文案、工具/Skill 的公开名称说明、输入表单和可信 recipe 版本/可用性 | system prompt、模型密钥设置、endpoint/auth header、worker queue、内部工具执行配置；Skill prompt 如需公开须单独审查其用途 |
| `PublicAssetDelivery` | 服务端生成的受控本地 Asset URL、媒体类型、大小、hash、READY/失败状态、下载文件名 | 缓存根路径、代理规则/上游 URL、签名 URL、密钥、远端 job claims |

前端迁移后只消费受控 URL，不重建 `provider-config.ts` 或第二份 Provider 表。白名单需同时覆盖 `/api/v1/schema`、首屏 inline JSON、`/api/v1/offerings`、`/api/providers` 默认/extended、兼容 Task/Asset 响应、SSE 和 ETag/304：向服务器内部结构插入 canary 字段，证明各公开面及浏览器可见网络响应均无 canary，且 ETag 只依公开字节变化。变更字段必须更新 Catalog 校验、投影合约和消费者，而非靠 `omitempty` 隐藏。

## 6. D05：ToolRegistry、Skill、MCP 信任边界

**现状。** Chat Agent 现有 `list_offerings`、`set_params`、`get_task`/`observe_task`、`request_consent` 和生成工具；执行器按工具名分派（`internal/chat/agenttools/executor.go:136-152`），付费任务最终仍经 Generation。Skill 有独立 Catalog/loader；当前源码证据不足以宣称开放 MCP connector 已存在。LLM 指令与用户内容不能授予权限。

**2026-09-29 工作分支增量。** Catalog 的工具执行声明编译为服务端 ToolRegistry，执行器按 domain/effect/capability 与可用 Offering 校验；`create_three_view` 使用版本化 recipe、持久 SkillRun 和独立的人类 ExecutionGrant，仅自动发起首步。待批准的 SkillRun 步骤由同一 Agent consent API 投影，批准/拒绝都需真实 Cookie 人类身份与当前 request ID。AgentPending Grant 持久绑定 turn，Task 创建事务与 Stop 状态原子校验。后两步由 SkillRun review/advance API 推进，工作区交互及真实输出仍待验收。开放 MCP connector 未实现，不能把 ToolRegistry 存在当作任意外部工具可用。

**目标 ToolRegistry 契约。** 每个可信工具登记稳定 implementation key、输入/输出 schema、effect、所需 principal permission、owner/session 作用域、是否付费、幂等与取消语义、可公开描述、审计 action。effect 至少区分 `READ`、`LOCAL_EDIT`、`TASK_WRITE_NO_COST`、`PAID_TASK`、`ADMIN_WRITE`、`EXTERNAL_IO`；一个工具只能调用其声明效力及权限内的应用服务，不能由模型回复、Skill 文本或 MCP 元数据升级。READ 不得调用 Submit；LOCAL_EDIT 不得产生付费 Task；PAID_TASK 先核现行人工流程与用户所选 Offering/输入，再由统一服务 Submit，未知上游收费不得盲重试；ADMIN_WRITE 永远另查管理 permission。工具取消只发请求，不宣称上游已停或退款。

Skill 是带 revision 的可信 recipe：列明可用工具、参数边界、输入 Asset/frame 来源、预期产物、步骤依赖和质量复核；修改 recipe 产生新 revision，不把一段 prompt 当执行权限。首个三视图 recipe 仅使用内建注册工具即可完成，不依赖任意 MCP。若输出方向无法核验，保留原 parent sheet 与子资产，SkillRun/Plan 标 `needs_review`，不改已 SUCCEEDED 的 Generation Task。

未来 MCP 仅作为可选 transport adapter：服务器端显式 allowlist 远端与工具映射；外部 tool description、返回文本、网页、图片 OCR、历史对话、Provider 错误都作为**不可信数据**，不得写入 system 指令、角色/permission、Offering 选择或批准事实。MCP 不直接获得数据库、Provider key、Task 终态写权限；所有副作用由本地 ToolRegistry 和应用服务再授权，限制网络目标、输入/输出大小、超时及日志脱敏。连接断开或重放不得重提已付费任务。开放 MCP 生态是独立 P2 决策，不是 G1 首个 Skill 的前置条件。

## 7. G1 验收与待定决策

设计评审需逐路径给出 401/403/404、owner/admin、revoked session、浏览器/非浏览器 CSRF、无存在性 oracle、成本同意可达性；用一个原生和一个兼容入口证明 TaskID/AssetID/状态/事件一致。公开 canary 在 Schema、inline、discovery、兼容、网络面和 304/ETag 均不得出现。后台普通用户拒绝与有权限管理员允许要分别动态测；下载须测不属于当前用户、Asset 未 READY、304、部分 ZIP 失败。Tool 测试用恶意输出试图把 READ 升级 PAID_TASK、跨 owner 读和绕过人工同意，均必须被服务层拒绝。以上是后续实现验收条件，本文件未运行测试。

**待定，不能由文档冒充现状：**

1. 无 Origin/Fetch Metadata 的非浏览器调用有哪些真实客户端，采用何种独立凭证、迁移窗口和 webhook/SSO 例外；未确定前不能把「两个头均缺失」直接标为漏洞或无风险。
2. 分支代码已将受保护任务资产、输入资产、旧生成图和旧资产代理统一为 `private, no-store`，代理不再发通配 CORS；Catalog 拒绝把相关字段改回可缓存策略。需以真实响应和共享缓存负控验收代理行为及大文件代价。旧版曾发出的 `immutable`/`public` 响应不能靠新响应头主动清除；若要求立即吊销，需核查并清除实际中间缓存或更换 URL。`immutable_asset_cache_control`、`binary_cache_control`、旧生成图 `cache_control` 仍在 Schema 类型中；清理或重新命名前须逐个核对消费者。
3. 管理 ZIP 局部失败采用 manifest、响应头还是结构化状态，以及审计持久化失败时下载/管理写操作的策略。
4. 每条 legacy capability 路径、Canvas/Plan/Chat 付费动作的现行人工确认覆盖表；覆盖不能证明的路径在代码阶段停用或导向既有人审。这里不设计新的报价或批准协议。
5. Public Schema 迁移期间 SPA 对 proxy 规则的最后消费者、Skill prompt 的公开需求与兼容版本窗口。

源码锚点：`internal/transport/httpapi/{server,security_middleware,chat_compat,canvas_compat,administration_generations,administration_usage,task_stream}.go`，`internal/authentication/{session,service}.go`，`internal/generation/module.go`，`internal/administration/generation_download.go`，`internal/catalog/{catalog,schema_types}.go`、`internal/catalog/default_catalog.json`，`internal/transport/webui/host.go`，`internal/chat/agenttools/executor.go`。图谱为 2026-09-28 18:33:40 UTC generation；上述路径的 coverage 检查为 `metadata_match/no_recorded_issue`，这是最佳努力信号，不代表源码全域完备。
