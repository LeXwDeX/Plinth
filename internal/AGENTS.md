# internal — Go 运行时协作指引

> 本文件只放 Go 子树**独有**的约束；全局事实一律指向真源、不复述（防止副本漂移）。

## 真源指针（改动前必读）

- **Product Schema、Provider 注册与 Agent 路由**：根 `AGENTS.md` 的「真源和分层」，以及 `docs/architecture.md` 的「Provider 任务流程与 UI 对齐」；架构细节见 `docs/go-architecture.md`。
- **内核 / Provider 边界语义（公开父任务 vs 私有子任务）**：`docs/provider-schema-architecture.md`；`generation/` 按 Schema 管理公开 Task/Attempt（含以公开 Offering 形式提供的转换任务），仅 Provider 内部子作业不进入公开流、只以 Opaque 检查点透传；内核不得硬编码 per-format 子任务类别/分支，Provider 专属方法/参数/错误/UI/资源声明归 Schema，代码只跑通用规则或不可约私有编排；`kernel_boundary_test.go` 只是 import 层直接隔离门禁。
- **Go 门禁命令**：唯一真源 `dev_test.md` §2「自动化门禁项」（Go 四条），本文件不复制命令；不得把 pnpm 门禁误套到 Go 代码。
- **部署 / 项目隔离 / ETL 约束**：部署入口 `docs/deployment.md`（按环境名部署，目标在本地 `deploy/local/environments.json`）；另见根 `AGENTS.md`「实施与验收」节与 `dev_ops.md` 的测试环境章节。部署事实以对应 Issue 回执为准（`formal-test` #270；`legacy-formal` 已于 2026-10-30 下线，存档 #74）。

## 模块边界（本子树独有）

- `catalog/`：Product Schema 加载、校验与投影（含各路由 schema 测试），是内容与契约的收口点。
- `generation/`：任务生成业务流程，只消费 catalog 声明，不内嵌产品内容。
- `transport/`：HTTP 传输层（handler/路由），不得内嵌业务分支或产品文本。
- `adapters/` 与 `providers/`：上游 Provider 协议接入，参数/校验/路由声明来自 Offering，代码只做协议解析与调用。
- 依赖方向保持单向：transport → 业务（generation 等）→ adapters/providers → 上游；catalog 被各层消费，不反向依赖业务模块。
