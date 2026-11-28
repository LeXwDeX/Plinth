# internal — Go 运行时协作指引

> 本文件只放 Go 子树**独有**的约束；全局事实一律指向真源、不复述（防止副本漂移）。

## 真源指针（改动前必读）

- **Product Schema、Provider 注册与 Agent 路由**：根 `AGENTS.md` 的「真源和分层」，以及 `docs/architecture.md` 的「Provider 任务流程与 UI 对齐」；架构细节见 `docs/go-architecture.md`。
- **内核 / Provider 边界语义（公开父任务 vs 私有子任务）**：`docs/provider-schema-architecture.md`；`generation/` 按 Schema 管理公开 Task/Attempt（含以公开 Offering 形式提供的转换任务），仅 Provider 内部子作业不进入公开流、只以 Opaque 检查点透传；内核不得硬编码 per-format 子任务类别/分支，Provider 专属方法/参数/错误/UI/资源声明归 Schema，代码只跑通用规则或不可约私有编排；import 层有两道门禁，都只查直接 import、不证明语义边界：`internal/generation/kernel_boundary_test.go` 只扫 `generation/` 包目录本层的生产文件，禁止其 import `providers`、`adapters`、`transport`、`app`；`internal/architecture/boundaries_test.go` 遍历整个 `internal/` 的生产文件，按层校验（`schema` 只依赖自身，`catalog` 只依赖自身与 `schema`；`generation`、`chat`、`processing`、`skillrun`、`agents` 不得 import `app`、`transport`、`providers`、`adapters`；`providers` 不得 import `app`、`transport`、`chat`、`processing`、`skillrun`、`agents`）。
- **Go 门禁命令**：唯一真源 `dev_test.md` §2「自动化门禁项」（Go 四条），本文件不复制命令；不得把 pnpm 门禁误套到 Go 代码。
- **部署 / 项目隔离 / ETL 约束**：部署入口 `docs/deployment.md`（按环境名部署，目标在本地 `deploy/local/environments.json`）；另见根 `AGENTS.md`「实施流程」与「环境与运维」节与 `dev_ops.md` 的测试环境章节。部署事实以对应 Issue 回执为准（`prod` 晋级回执见 #2）；`legacy-formal` 已下线，不是可部署环境（存档 #74）。

## 模块边界（本子树独有）

- `catalog/`：Product Schema 加载、校验与投影（含各路由 schema 测试），是内容与契约的收口点。
- `generation/`：任务生成业务流程，只消费 catalog 声明，不内嵌产品内容。
- `transport/`：HTTP 传输层（handler/路由），不得内嵌业务分支或产品文本。
- `providers/`：上游 Provider 协议接入，参数/校验/路由声明来自 Offering，代码只做协议解析与调用。
- `adapters/`：业务端口的基础设施实现，不全是 Provider 接入：`sqlite`（持久化与任务读取投影）、`catalogrecipes`（声明映射为 SkillRun 执行配方）、`mcphttp`/`mcpagent`/`mcpworkspace`（MCP 传输与读取）、`legacygeneration`/`legacyassetproxy`（历史任务与资产来源读取）。
- `wirecapture/`：上游 HTTP 交换的脱敏、`Validate` 与可选落盘录制（#233）；只依赖标准库，可被正式程序链接，由 `app/` 在装配期包裹连接的 transport，`providers/` 与 `agents/` 不引用它。`testutil/wirefixture` 在其上构建测试侧捕获与回放，不复制规则；`wirecapture/privacyscan` 是独立的第二道隐私扫描。
- `diagbundle/`：`art-ai diagnose` 的诊断包（#234）：只读地汇集运行信息、Catalog 声明的设置名称、失败任务摘要、日志与 #233 录制，自由文本经 `Scrubber` 逐值脱敏、整包按最终字节再过 `wirecapture/privacyscan`，命中即拒绝写文件。不依赖 `app/`，不写库、不改日志与录制目录；设置名单取自 Catalog，不另抄；`cmd/art-ai` 只分发子命令，来源解析在 `app/diagnose.go`。
- 依赖方向（运行时调用流向与代码 import 方向的区别）以根 `AGENTS.md`「真源和分层」表下方一句为准，本文件不复述。
