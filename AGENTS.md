# AGENTS.md — Plinth 开发规约

本文件只放项目开发规约。产品规则见 `docs/product.md`，架构契约见 `docs/architecture.md`。
发现描述与代码、Schema 或部署状态不一致时，先核实真源，再更新文档。

## 真源和分层

下表规定模块职责与写入边界，不是实现完整性或验收通过的证明。

| 层 | 当前职责与写入边界 |
|---|---|
| `internal/catalog` | 加载、校验和投影 Product Schema；声明 Offering、参数、输入输出、费用和文案，不持有运行密钥明文或推进任务 |
| `internal/app` | 按 Schema 和运行配置装配服务、Provider Adapter 与后台 worker |
| `internal/{agents,chat}`、`internal/skillrun` | 解释意图、维护 Chat session/Agent checkpoint/SkillRun，选择 Offering，准备具体步骤并按会话批准策略请求执行批准，需要人工决定时再请求人类同意；引用公开 Task，不改写其终态 |
| `internal/agentconfiguration`、`internal/mcp` | 管理有界配置版本与只读工具范围；不编辑运行密钥、执行实现或付费 Grant，不推进公开 Task |
| `internal/processing` | 持久管理 3D Processing Plan、步骤顺序、暂停/恢复/重试与来源选择；每个公开步骤仍通过 Generation 提交 |
| `internal/generation` | 统一管理 PreparedInput、执行授权校验、Task/Attempt、租约调度、幂等、依赖、Observation、协调资产物化、事件和终态；是公开任务事实与状态机的唯一写入者 |
| `internal/{assets,assetdelivery,scene,canvas,capture}` | 管理资产所有权、字节与鉴权交付，以及场景/画布版本、服务端渲染清单和模型截图来源；不得伪造任务成功 |
| `internal/providers` 与上游协议 Adapter | 通过统一 Port 执行不可约的上游协议和私有子作业；不维护第二套公开 Task 生命周期。`internal/adapters` 还包含 SQLite 等持久化基础设施及任务读取投影，不全是 Provider Adapter |
| `internal/transport` 与 `src/**`、`web/**` | HTTP 层处理鉴权、owner 范围、兼容 API、公开投影、SSE 和 SPA 托管；前端消费公开 Schema/API，不直连上游或内置 Provider 私有配置表 |

依赖方向保持 `transport → 业务服务 → Provider Port/Adapter → 上游`；Catalog 向各层提供声明，不反向依赖业务模块。跨层传稳定 ID、版本和明确 DTO。实际源码与该边界冲突时，按事实修复，不把架构目标写成既成事实。

### 架构约束

**架构文档同步（2026-10-06 用户要求）**：后续改动涉及架构变化时，必须在同一交付中同步更新 `docs/architecture.md`。架构变化包括模块职责、依赖方向、数据与执行契约、持久化与恢复机制、前端数据流、资源交付及部署结构的变化。Provider/Schema 边界变化还须同步 `docs/provider-schema-architecture.md`，并在关联 GitHub Issue/PR 记录范围、实现、验证和剩余验收。文档按最终源码写清变化、保留的边界和验证范围。未实现或未验收的内容须标明。交付前必须核对最终代码 diff 与架构文档。存在应更新却未更新的架构说明时，不得报告该交付已完成。

1. **Schema 驱动**：可变产品配置（含运行策略、权限、路由、Provider/Offering 参数、约束、默认值、费用、文案、UI 控件及上游映射）由 Product Schema 声明。业务状态机、通用校验和不可约的 wire 解析可写代码，不能建立前端或 Handler 的第二份产品配置。
2. **Provider 轻代码**：同协议 Provider 优先登记连接与 Offering，并复用 Adapter；仅新 wire 协议需要隔离 Adapter 与契约测试。不得复制任务管理、状态机或页面分支。
3. **统一公开任务**：生成、处理及其他独立公开 Offering 都由 `internal/generation` 管理 Task/Attempt。Provider 为履行单一公开步骤而运行的上传、转换等私有子作业留在 Adapter 检查点内；HTTP、Agent 和 Processing Plan 不自行推进 Task 状态。HTTP/兼容层读取 Task 与产物须经内核或其投影/资产模块，不另建旁路查询与生命周期。
4. **UI 消费公开 Schema**：`/api/v1/schema`、扩展 Provider discovery 与任务投影提供前端可用的能力、控件、费用提示和状态文案。`application.runtime.configuration` 与密钥只留服务端，不进入首屏和公开投影。未注册或运行配置不可用的 Offering 明示不可用，不静默换模型。

- **细则指针**：三方隔离、3D 产物契约和 Provider 任务流程的细则见 `docs/architecture.md`。改动涉及资产交付、物化或任务状态时，必须先读。

## 问题核查

- **先只读，守住授权**：在当前授权范围内开始核查。用户限定只读时，不修改、不付费；否则按已有授权推进，不重复请示。仅在新增范围、缺少授权或存在无法自行消解的用户决策时询问。按问题选择必要证据，不为小问题扩成全量审计。
- **按顺序核实，不先猜修法**：先查适用的当前契约和版本，再对照实际用法、配置、请求与结果证据，最后沿源码定位。涉及上游时，以对应端点的当前官方文档为准，区分 API 版本、模型版本与服务端构建版本；不得从某个端点的版本推断全部接口都使用同一版本。纯本地问题先查项目契约、Schema 和相关依赖版本，不强行增加无关的外部核查。产品遵循 `docs/product.md` §1、§1.1 的 PC、最新 API 与最高质量规则；“最新 API”按端点核实，不按版本字符串大小猜测。文档无法获取或证据冲突时，标明缺口，不把推测写成契约。
- **对齐实际上下文**：按受影响链核对目标部署 revision、任务冻结的 Offering 和输入、已发布及该轮实际使用的 Agent 配置、owner/session、来源公开 Task 与上游 remote ID。涉及生成或处理时，核对 preview/refine 阶段、结果格式、参数类型、默认值、额外字段及已批准 Grant 的范围，不能把不同阶段、身份或版本混为一谈。
- **区分原始与重建证据**：实际发送的原始 HTTP 请求与按日志或代码重建的请求分开标注；重建请求不能证明当时已发送相同内容。同一 URL 不等于相同字节，比较输入或产物时核验实际字节及必要的摘要、格式信息。只保留定位所需的脱敏证据，不记录密钥或无关原始载荷。
- **再追源码与因果**：针对受影响版本（线上问题以实际部署 revision 为准），追查相关 Schema 映射、Adapter、公开 Task/Attempt 与资产检查链。用最小复现验证假设，用相关回归证明修复；分别列出已证事实、假设、未知项及支持因果结论的证据。源码存在、测试通过或时间上先后发生，均不能单独证明线上根因。
- **按公开交付契约验收**：Provider 返回 `SUCCEEDED`、模型能打开、服务健康或局部测试通过，不等于公开任务完整交付成功。按 `docs/architecture.md` 核对必需产物槽位、实际字节、资产状态及鉴权交付。不得用放松安全校验、把必需产物改成可选、盲目付费重试或仅增大超时来掩盖原因；契约变更须有独立依据并遵循既有授权与验收规则。

## 实施与验收

- 修改行为前写清预期、边界和验证路径。Schema/共享接口变更须检查 Catalog 校验、公开投影、扩展 discovery、兼容 API、前端消费者、fixture/快照与运行时可用性；跨 Provider 链需测成功、失败、跨用户、同/异 Provider、格式不兼容及重试/同意路径。
- `dev_test.md` §2 是本地与 CI 自动化门禁唯一真源；按最终 diff 和影响链运行。前端构建使用 pnpm；Go 验证使用 go 命令。不要把远端 CI 当本机可复现问题的首次测试环境。需要干净环境复现门禁时，用 `python3 scripts/host-ci/run.py <sha|HEAD>` 在 `dev-test` 主机的一次性容器里对精确提交跑同一套门禁（GitHub Actions 不可用期间它也是项目的 CI 替代，沿用 #79；规则见 `dev_test.md` §2「主机 CI」）。前端任务状态展示只读 Product Schema `task_states`；旧 `src/lib/constants.ts` 状态枚举已随路由不可达组件一并删除，不要重建前端状态表。
- **部署入口（2026-10-20 用户要求）**：部署文档入口是 [`docs/deployment.md`](docs/deployment.md)。用户只需在仓库根目录说要部署到哪个环境（例如「部署到正式测试环境」），Agent 就按该文档执行：从未纳入 Git 的 `deploy/local/environments.json`（模板 `deploy/environments.example.json`）按环境名解析目标，用 `scripts/release-env.py release <环境>` 发布，核对 revision，冒烟并汇报。环境名：`dev-test`（开发测试环境）、`formal-test`（正式测试环境，测试版本中的稳定版）；内部存档 #232 的隔离正式栈记为 `legacy-formal`。主机、地址、端口、目录与 Compose 名只写在本地环境文件，不写入仓库文档（本项目开源）。
- **晋级规则（2026-10-20 用户确认）**：开发测试环境（包括 Agent 测试）全部稳定通过以后，才发布到正式测试环境。即 `formal-test` 只接收已在 `dev-test` 发布成功、并且 L0、L1、L1 Agent 及本次相关浏览器/Agent 验收全部 PASS（无 skip、无 soft-fail）的精确 `main` revision；验收报告以本仓库原生 Issue comment 记录，`release-env.py` 读取并核对该报告与 `dev-test` 成功发布清单，缺任一项即拒绝。
- 产品修改只有发布到已定义测试环境并验证相关页面、API 和直接影响链后才能报告“完成”。各环境的部署事实与差异见 `docs/deployment.md`：`dev-test` 每次发布已推送的 `origin/main`；`formal-test` 只按上条晋级；`legacy-formal` 在 2026-10-20 新环境布局下去留待用户确认，确认前保持原样不操作。普通发布仅调用 `scripts/cutover-go-production.sh` 的 preflight，不调用 takeover、不重跑 ETL、不使用旧 `deploy.sh` 或 `--remove-orphans`，也不触碰同机其他业务。
- **功能完成后自动到 dev-test 回归（2026-10-02 用户确认）**：全自动指每次功能实现或修复完成后的交付流程，由 Agent 连续完成本机验证、Git 交付、`dev-test` 发布和部署后回归，无需用户逐步催办或再次确认。影响产品运行的代码、Schema、配置或前端产物改动，在本机通过 `dev_test.md` §2 门禁后，Agent 必须自动用 `scripts/release-env.py release dev-test` 发布，随后自动执行每次发布必跑的 L0 浏览器启动检查、L1 免费冒烟，以及本次功能涉及的页面、API 和直接影响链回归。涉及 Agent、生成或同意链路时，按 `scripts/smoke/README.md` 执行 L2 等相关真实模型验收，遵守既有小额测试和付费同意边界。不得只停在本机实现、构建通过或健康检查通过就报告完成。纯文档或 AGENTS 规则修改且不影响运行产物时，校验文档即可，不必重建运行镜像。CI 执行自动化门禁，不承担发布。
- **Git 交付常设授权（2026-10-02 用户确认；2026-10-27 起 PR 创建与合并同样无需逐次询问，CI 门禁按 `dev_test.md` §2「交付硬门禁」）**：为完成上述功能交付与 `dev-test` 回归，Agent 已获授权自动提交本次任务的改动、推送及将已验收改动正常合并到发布源 `main`，无需逐次询问。只暂存本次任务涉及的改动，不带入无关或他人尚未交付的改动，不提交密钥、运行环境文件、本地环境文件和数据；不得强推、改写历史、跳过 hooks 或撤销他人改动。此授权不扩大正式生产操作权限。
- **测试部署与发布源**：`dev-test` 的发布脚本只同步已推送的 `origin/main`，发布后核对镜像 revision 与目标完整 SHA 一致。`legacy-formal` 初始无 Provider Key，只晋级 `dev-test` 已通过规定验收的精确 `main` revision，回执以内部存档 #232 为准；`formal-test` 此前的回执在内部存档 #270，之后的晋级回执记在 GitHub #63 的 comment。各环境的发布、回滚、镜像清理和数据目录互相隔离。提交、推送与正常合并按上述常设授权执行；遇到门禁、权限或环境阻塞时报告具体原因，不把未部署的改动记作完成。
- **发布失败处置**：发布脚本构建或 preflight 失败时中止，旧实例不动；容器更新或健康检查失败由脚本回滚并留报告。L0/L1 及后续回归失败不在脚本自动回滚范围内，Agent 必须记录证据、按部署协议恢复已验证版本，再修复并重跑受影响检查和发布验收；回滚失败或环境阻塞须明确报告，不能降级验收或跳过失败项。发布和测试协议以 `docs/deployment.md`、`dev_ops.md`「Agent 全自动发布协议」与 `scripts/smoke/README.md` 为准。
- **各栈默认 DEBUG、错误必记录（#233，2026-10-19 用户要求）**：各栈默认 `LOG_LEVEL=debug`，所有 4xx/5xx、后台失败与前端错误都要有带请求 ID 和内部原因的服务端日志，日志写入各栈 `logs/` 并按大小/天数滚动（默认保留 30 天）。DEBUG 不得让 HTTP 响应泄露内部原因；日志不得含密钥、Cookie、Authorization、密码、签名 URL query、提示词或上传内容全文。用户报错时按其错误 ID（`X-Request-ID`/`request_id`）查日志。细节见 `dev_ops.md` 日志一节。
- **dev-test 操作常设授权（2026-09-25 用户确认）**：`dev-test` 的发布、重启、回滚、小额真实模型验收、测试期付费同意及有界镜像清理仍按既有授权执行。2026-10-19 用户另行授权 `legacy-formal` 的有界创建、已验收 revision 晋级与回滚、免费 L0/L1 冒烟及受支持的管理员初始路径；此授权不含写入 Provider Key、付费生成、数据导入或 nginx/域名变更，这些须用户明确决定。各栈互不操作，不触碰同机其他业务或其他栈的数据库备份与卷。
- **付费验收是正常交付流程（2026-10-18 用户确认）**：真实模型/付费验收是产品落地与功能验收的正常组成部分，不得把活动中某条「停止/暂停付费验收」的阶段性记录当成长期规则；是否需要付费验收按 Issue 范围与 `scripts/smoke/README.md` 判定，并以下达任务的用户最新指示为准。发起前核对当前官方协议、价格与余额，只做小额单次请求，绝不重放已消费的 intent；测试期付费同意按下方常设授权由 Agent 代为批准。仅正式生产与超出测试授权范围的生成仍需单独授权。**次数上限（2026-10-22 用户要求）**：每个版本（revision）、每个 Provider，Agent 依上述授权自行发起的付费验收请求以 10 次为限；第 11 次起，每次发起前须在当前对话中取得用户的人工批准。计数只在当前对话中有效，不需要记录或持久化。
- **直接使用 dev-test 的 `.env` 配置与 Key（2026-10-16 用户直接授权）**：`dev-test` Plinth 栈现有 `.env`、部署使用的运行环境文件及其中的 Provider/API Key，已授权 Agent 直接读取并用于本项目开发、调试、发布和真实模型验收。先按部署文档定位该栈实际使用的配置，缺少本地 Key 时直接使用其已有配置，不再要求用户重复提供或确认，也不再为该范围内的凭据使用单独请示。凭据只在必要的运行进程和受保护的环境文件中使用，不输出到日志、回复、文档或 Git；操作仍限定 Plinth 栈，不使用同机其他业务凭据。
- **dev-test 主机镜像保留**：构建前只查清单和容量，不删除镜像。仅在新实例健康且成功清单写入后，自动按创建时间保留精确仓库 `art-ai-go-production` 最新五个不同镜像 ID；额外保护运行镜像、发布前实际运行的回滚镜像、该主机测试、候选及 `legacy-formal` 三个 Compose 项目全部运行/停止容器引用的镜像，并保护各栈发布前实际运行的回滚镜像（正式首次晋级前须实现并验证，不能仅靠当前容器引用）及跨仓库共享别名，保护项可使数量超过五个。`formal-test` 是单项目主机，只保留运行镜像与本次回滚镜像。本镜像保留工具仅用 `docker rmi <id>`，不得强制、广域 prune、删除容器/卷/数据库备份或其他业务资源；发布备份清理是另一个独立步骤，只按下方 dev-test 发布备份保留规则处理 `backups/release-*`。删除失败只警告，不使发布失败。dry-run：`python3 scripts/retain-art-ai-images.py --dry-run --root <该环境发布根> --project <该环境 compose_project> [--protect-project <同机其他项目>…] --rollback-image art-ai-go-production:<已核实的上一运行标签>`（在目标主机同版源码目录内执行）；本地只读 SSH 用法见 `dev_ops.md`，失败与回滚路径不清理。
- **dev-test 发布备份轻量化与保留（#364，2026-10-23 用户确认，Issue note 100331 覆盖早期保留 3 份）**：`dev-test` 只存放测试材料，环境文件可写 `"backup": {"scope": "database", "keep": 2}`，停机时只备份 `production/data`，健康失败时也只恢复该目录；`storage-go`（不可变/只追加 CAS）与 `web-data`（只读）的候选写入字节保留，可能成为孤儿，不得称为完整回滚。省略 `backup` 时保持全量备份且不自动清理；只有环境名 `dev-test` 可设置 database 模式，`release-env.py` 拒绝用于 `promote_from`、`managed_by` 或其他环境，`scope` 只接受 `database`、`keep` 为 1–2，服务器段在构建前再次校验。备份范围写入 `backups/release-<tag>/scope`（单行 `database`/`full`；旧快照无 scope 且三个归档齐全时按 `full`）、`production/compose.env` 和失败报告。只有健康检查与成功清单均写入后，才调用 `scripts/retain-release-backups.py --apply`：正常清理最多保留最新 2 个已结束发布备份，更早的失败快照也按最新 2 个回收；进行中的快照可临时排除，失败报告与独立诊断日志保留。bootstrap、预构建、失败和回滚路径不清理，也不删除原始资产、当前或更早的源数据、首次迁移冷备或其他栈备份；清理非零只警告，不改变发布结果。同一发布根的发布备份/恢复、备份清理与手工恢复共用 `<root>/.release.lock`：服务器段在构建或改动数据前获取，retention 继承并校验描述符，手工恢复用 `release-lock.py run` 包住全过程；缺失或身份不符的锁即失败关闭；继承描述符在根/常规文件/owner/路径身份检查后直接 `flock`，共享同一 open file description 时复用锁，独立描述符在空闲时先取得锁、遇其他进程持锁则 busy，借用方不得解锁。递归删除前拒绝快照树内挂载点（含同设备 bind mount），挂载表不可用或异常时失败关闭。失败快照的零字节/缺失归档仅在独立失败报告存在时可清理；成功备份仍要求完整非空归档，删除资格不等于恢复资格。保留 database-only 快照可能被使用时，禁止离线 CAS/task 破坏性维护（见 `docs/storage-maintenance.md`），不得为回滚删除 CAS 字节。`formal-test` 与 `legacy-formal` 保持全量备份且不自动清理。
- **测试期付费同意由 Agent 代为批准（2026-09-28 用户直接授权）**：在 `dev-test` 做真实生成验收时，Agent 以「人」的身份代为点击/调用同意批准，不再逐次向用户请示。这**只是测试期操作授权**。该授权仅限 `dev-test`，不适用于 `formal-test` 或正式生产。产品的批准规则见 `docs/product.md`。
- 测试栈大模型接线（llms 链路、模型名、上游 key 占位）见 `dev_ops.md`「测试栈大模型接线」。
- `legacy-formal` 初始空库且不挂 legacy Next 数据；无 Provider Key 时远程 Provider Offering 按运行能力显示不可用，本地 Offering 仍按可用能力正常投影。部署与验收状态以内部存档 #232 的回执为准。
- 不提交密钥、运行环境文件和数据；不在日志、文档或回复复制凭据。安全审计只基于当前代码和业务证据，不使用外部 DayBreak 工具。代码保持语义明确、可维护。

## 进一步阅读

- `docs/product.md`：产品规则（平台、付费批准、Skill、3D 交付）与产品现状。
- `docs/architecture.md`：前后端职责、Provider 插槽、Schema 控制与文档真源的统一架构入口。
- `docs/design/README.md`：工作台设计原则与 UI 契约索引。

- GitHub 原生 Milestones、Issues 与 Pull requests：目标、工作范围、进度、阻塞、验收及交付记录。
- `docs/v3-architecture/README.md`：冻结输入、执行授权、Scene、RenderManifest 和 SkillRun 的设计与源码状态；目录内“待验收”记录不能当作当前部署事实。
- `docs/go-architecture.md`、`docs/provider-schema-architecture.md`：Go/Provider 分层及公开任务和私有子作业的历史基线；引用“当前”表述前先对照现行源码。
- `dev_test.md`：门禁（含 CI 与 `scripts/host-ci/run.py` 干净复现）、Safari 表单注意事项和部署后验收。
- `docs/acceptance-1.0.md`：开源 1.0 功能验收清单与全新机器部署验收步骤；逐项结果记在 GitHub #62，不写回该文档。
- `README-go.md`、`dev_ops.md`：运行与发布协议、测试拓扑及运维快照；实际状态须在目标环境只读核验。
- `internal/AGENTS.md`：Go 子树独有边界；若其指针因本文重写而过期，同步修正。

## SpecGit 项目适配

- **计划与状态只写 GitHub 原生记录（2026-10-14 用户要求；2026-10-27 起平台为 GitHub）**：Milestone 用作可选目标容器，Issue 是工作范围、进度、阻塞和验收状态的唯一真源，PR 关联实现与验证记录。仓库只保留产品架构设计、接口/操作规范和静态历史证据；不维护本地开发计划、工作包或当前状态台账。历史报告不得当作当前状态来源。入口：Milestones、Issues、Pull requests。
- **单一仓库与历史编号（2026-10-27 用户决定）**：`https://github.com/LeXwDeX/Plinth` 是唯一仓库，代码、Issue、PR 与 CI 都在这里；内部 GitLab 不再使用，不再向它推送或在其上记录状态。公开历史从单根快照开始，内部完整历史只作维护者本地存档，不推送到本仓库。2026-10-27 之前的提交信息、文档与脚本注释中的 `#N`、`!N` 指内部 GitLab 存档的 Issue/MR（文档中写作“内部存档 #N”或“GitLab #N”），不是本仓库同号的 Issue/PR；此后的 `#N` 指本仓库。每个提交推送后立即公开，推送前须通过 `python3 scripts/make-public-snapshot.py check --worktree`。
- **GitHub CLI 凭据**：通过 SpecGit/gh 查阅、创建或更新 GitHub 原生 Issue、Milestone、PR 等记录时，使用 `gh auth login` 保存的登录凭据（本机为系统钥匙串），不从项目 `.env` 读取或自动注入 `GH_TOKEN`/`GITHUB_TOKEN`。显式进程环境仍按 CLI 自身优先级处理，操作前核实实际账号、目标仓库与权限；不得要求用户把 token 粘贴到对话，不得打印、提交或把登录凭据复制到环境文件等其他持久配置。仍须遵循 SpecGit Skill 的查重、inspect/dry-run 和原生结果回读流程；凭据缺失或认证、网络失败时，按实际诊断报告。
- 本项目 SpecGit 仅作项目级集成，共用 CLI；涉及 SpecGit 任务时读取 `.agents/skills/specgit-native/SKILL.md`；该 Skill 由各工作树的 `specgit init`/`specgit setup` 在本地生成，不纳入 Git。**SpecGit 是维护者的内部流程工具**（#334）：公开仓库与新克隆里没有这个 Skill、`.specgit.yaml` 与 Git guard，下方受管区块里指向它的路径因此不存在。外部贡献者不需要安装 SpecGit，可以忽略该区块，按本文件其余规约开发、在 PR 里关联 Issue；维护者缺少该文件时按 `docs/specgit-project-integration.md` 用 `specgit init` 在本工作树生成。项目设置、初始化/升级、验证与交付边界见 `docs/specgit-project-integration.md`。`.specgit.yaml` 保持本地。不得手改 SpecGit 受管区块、managed Skill 或受管 hooks；Claude Code、Codex 与支持 Claude hooks 的定制 OpenCode 由 CLI 在 `.claude/settings.json`、`.codex/hooks.json`、`.opencode/hooks.json` 各自注册 SessionStart 与 PreToolUse。提交与推送由 Git guard 执行 checkpoint 门禁；远端观察使用显式有界 `watch`/`inbox`。
- **所有分支与工作树都须初始化 SpecGit（#242，2026-10-19 用户要求）**：Git guard 只在有 `.specgit.yaml` 的工作树执行 checkpoint，未初始化的工作树会被静默放行。新工作树执行 `specgit init --cwd <工作树> --config-file <工作树>/docs/specgit-project.yaml --manual-observe`；报 `ownership_conflict` 时先把分支变基到 `origin/main`。共享 hooks 的项目自有区块由 `scripts/install-specgit-required-hooks.py` 显式安装（默认只预览，合并不会激活），先 warn 后 enforce，安装、回退与对在跑工作树的影响见 `docs/specgit-project-integration.md`。升级 SpecGit CLI 后若受管区块变化，先在 main 刷新并合入 `AGENTS.md` 区块，否则新工作树 init 会报 `ownership_conflict`。

<!-- 下方区块由 SpecGit CLI 生成和维护，不要手改；它引用的 SKILL.md 只在初始化过的维护者工作树里存在，公开仓库中缺失属正常（见上文「SpecGit 项目适配」）。 -->

<!-- specgit:project:v2:start -->
## SpecGit 2

Read project-root `.agents/skills/specgit-native/SKILL.md` before work; if missing, initialize with the shared `specgit init` CLI or restore an existing integration with `specgit setup` ([installation](https://github.com/LeXwDeX/SpecGit/blob/main/docs/agent-install.md)).
<!-- specgit:project:v2:end -->
