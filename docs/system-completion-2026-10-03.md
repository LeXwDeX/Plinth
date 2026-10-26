# 系统管理与用户资源补齐记录 — 2026-10-03

关联 Issue：#90。前置架构整理 Issue #89 已另行交付；本文件记录后续系统管理闭环的源码与验证边界。

当前状态：Skill 意图优先修复已合并并部署到测试栈，修复版橘猫图片及 L2 通过；修复版 L0/L1、API、owner 隔离、免费管理/同意界面、Meshy 与 Tripo 多格式资产物化，以及 Meshy/Tripo 3D→2D 画布往返的所记 Chromium/WebKit 桌面与移动范围均通过。Tripo 有纹理模型的浏览器组为 4/4 通过。本次定义的运行验收范围已完成。证据不代表真实 Safari 设备验收或全部 162 条 Skill 内容均已验证。首版物化失败根因未知、诊断版主体回归失败及其回滚作为历史记录保留。Issue #90 的原生状态为 closed。

## 1. 内容与权限真源

`internal/catalog/default_catalog.json` 的 `application.agent_configuration` 声明管理路由、字段限制、权限、文案、MCP 预置与私有连接引用。`internal/catalog/agent_configuration.go` 定义、校验并投影合同；`schema_clone.go` 对嵌套 MCP 工具、执行合同、连接 origin 与文案做防御复制。

管理员允许修改：有界系统提示词、2D/3D 上下文、风格 Skill 条目、已编译工具启停、已声明 MCP 预置启停。输入中的未知字段/选择、无效 Skill 合同、外部图片来源、无效模板或超限内容不能发布。新增 Skill 复用已打包资源，图生图模板保留 Schema 声明的用户/风格占位字段。

不开放编辑：执行 implementation key/effects、任意代码/命令、连接 URL/密钥、owner/session、Provider Task 状态机或付费同意策略。公开 Schema 只投影管理路由、限制与文案；普通有效配置和管理 MCP 摘要也不公开固定执行合同、连接详情或凭据。

## 2. 后端配置与持久轮次

| 路径 | 当前源码职责 |
|---|---|
| `internal/agentconfiguration/service.go` | 有效配置、预置、有限校验、版本发布、历史、恢复与重置；定义仓储 Port |
| `internal/adapters/sqlite/agent_configuration.go` | 原子 revision CAS、版本读取与历史查询 |
| `internal/adapters/sqlite/migrations/0041_agent_configuration.sql` | 配置版本表，不改写原 Chat/Generation 事实 |
| `internal/transport/httpapi/agent_configuration.go` | 管理权限/API、校验预览、发布审计、安全有效摘要和最新 Skill 目录 |
| `internal/chat/agent_configuration.go` / `types.go` / `orchestrator.go` | 新轮次冻结配置、Prompt、Skill 执行模板及工具清单，并持久到 checkpoint |
| `internal/chat/generationtool/executor.go` | 使用本轮冻结 Skill 准备输入；显式失效或不兼容 Skill 不静默忽略 |
| `internal/app/app.go` | 将仓储、配置服务、Chat、MCP 与 HTTP 消费者组合起来 |

管理读取通过 `GET /api/admin/agent-configuration` 返回当前版本、预置、安全工具摘要与历史。`POST /validate` 只校验与预览；`/publish` 必须提供 expected revision；`/restore` 和 `/reset` 发布新的 revision。并发冲突返回 409，校验失败不改变当前配置。

新轮次在首次模型请求前持久保存 `TurnCheckpoint.Configuration`。轮次挂起后，即使管理员发布新配置或进程重启，其 Prompt、工具与 Skill 不读取新版本。已有 PreparedInput、ExecutionGrant、幂等身份与 Task 恢复继续遵守 Generation 的原边界；配置更新不构成付费批准。

`GET /api/v1/agent-configuration/effective` 提供领域/所选生成模型对应的安全摘要；带 session ID 时再核归属。现有 Skill GET 使用最新配置。工作区选择的是稳定 Skill ID，生成时使用本轮冻结内容，而不是用户提交的 Skill 名称或随后的最新 Prompt。

## 3. MCP 实现与兼容范围

| 路径 | 当前源码职责 |
|---|---|
| `internal/mcp` | Schema 允许的只读工具、参数校验、启停与 owner/session 范围 |
| `internal/adapters/mcphttp` | 2026-07-28 metadata、JSON-RPC、Streamable HTTP JSON/SSE 与本地 request/response |
| `internal/adapters/mcpworkspace` | 当前会话的可用 Offering、Task 与产物公开读取 |
| `internal/adapters/mcpagent` | MCP 与既有 Agent executor 组合，保留历史恢复接口 |
| `internal/app/mcp_connections.go` | 本地预置与私有 runtime connection 装配 |
| `internal/transport/httpapi/mcp.go` | 同源 HTTP MCP 入口的权限与会话归属 |

默认预置 `workspace-read` 提供 `mcp_list_offerings`、`mcp_list_session_tasks`、`mcp_get_task_assets`。内部调用也走实际 JSON-RPC 帧；不能把原有普通工具重新命名就当成协议接入验收。

`POST /api/v1/mcp?session_id=…` 使用当前 Principal，校验本人读取权限和会话归属。当前 app 仅开放默认本地预置，外部 `streamable_http` 接线明确返回不可执行。虽然协议 Adapter 已有 JSON/SSE 契约测试，以及私有运行设置、精确 origin 与 redirect 拒绝机制，外部服务仍需未来补齐强输出 DTO 和可验证的 owner/session 范围合同后才能开放。

管理 MCP DTO 的 `available` 根据实际 runtime 填写；配置中启用一个预置不等于它可以执行。Declarations、普通有效工具摘要和调用准入均过滤不可用预置；nil runtime 不宣称 MCP 能力。普通有效摘要读取当前配置与防御复制的工具摘要，不加载管理员历史版本的完整配置。

实现不支持任意 URL/命令、写工具、任意 JSONSchema 高级构造或旧协议自动降级。发现到的远端工具不能自行进入注册表，固定 `READ` / `none` / `session` 声明也不能证明上游输出与数据范围安全。默认本地只读预置的通过证据不能推广为外部服务已开放或所有 MCP 服务兼容。

## 4. 前端与个人资源

管理页面 `src/app/admin/agent-configuration/page.tsx` 消费有限 DTO，提供 Prompt/Skill 编辑、工具/MCP 启停、校验预览、带 revision 发布与恢复。`src/lib/agent-configuration*` 收口同源 client 和异步资源；工作区 Skill 选择与有效摘要按 session、领域和生成模型隔离，晚到响应不能覆盖新范围。

`src/components/workspace/skill-selector.tsx` 与 AgentDialog 将所选稳定 Skill ID 交给服务端；不兼容时明确提示并阻止带失效选择提交。Skill 图片只使用同源打包缩略图。系统 Prompt 和模型执行策略不进入普通客户端状态。

`src/app/account/page.tsx`、`internal/transport/httpapi/account_resources.go` 提供本人用量、上传资源和公开任务产物入口。HTTP 由当前 Principal 推导 owner，不接受替换 user ID 来读取其他账户。管理读审计和旧资源私有地址投影亦在本次范围内。首版及最终修复版的所记界面和权限往返均已通过；当前最终修复版的精确覆盖见 §8。用量中的积分是估算值，不等于计费账单。

## 5. 已运行的本机定向验证

后端配置实施阶段已通过：

```sh
go test ./internal/catalog ./internal/agentconfiguration ./internal/chat/... ./internal/adapters/sqlite ./internal/app ./internal/transport/httpapi
go test -race ./internal/agentconfiguration ./internal/chat ./internal/chat/generationtool
git diff --check
```

重要证据包括：管理员权限/发布审计/409 冲突、无效写入保留旧版、SQLite 重开与历史恢复/重置、挂起轮次用新 Orchestrator 保留配置、checkpoint 重开保存 Prompt/工具/Skill、Generation 使用旧冻结 Skill、公开/管理投影剥离私有字段、防御复制，以及 MCP route 非本地/重名负控。

本次还将 JSON 全量格式化产生的无关紧凑排版改动恢复为 HEAD 字节；恢复前后解析后的完整 JSON 值相等。最终 diff 保留功能性 Agent/account 声明与文案变更。

最终集成后已通过 `dev_test.md` §2 七项本机门禁：TypeScript 零错误、61 个前端测试文件 / 351 项测试、生产构建、Go vet、完整 Go 单测、完整 Go race 及 CGO=0 构建。MCP 参数/发现/JSON-RPC/SSE/响应边界、冻结注册、默认预置归属与禁用、合法外部预置零拨号负控通过；Astra 最终方案与代码终审通过。该结论仍不代替部署后验收。

## 6. 首版交付、失败与回滚证据

首版功能提交为 `bb715030aae5f9e0f4f865d0bc95036e30f9fdc9`，MR !83 已正常合并；原生 CI 30397 的七项检查通过。主线提交 `598ff4eca0470c697ccdc8631a8be2bfe825845c` 经标准发布脚本部署为镜像 `598ff4e-20261002T235708Z`，OCI revision 与目标完整 SHA 相等。此前架构整理 MR !82 已合并，主线提交为 `15c28636c1c896d68a3ae741401166c9ea8c2e91`。

| 首版回归 | 结果及范围 |
|---|---|
| L0 / L1 | 16/16、12/12，通过 Chromium/WebKit 启动与免费 Agent 挂起后停止 |
| Schema / Offering / discovery / 会话参数 | 8/8，通过；无效 Offering 拒绝且零 Task |
| 两名普通用户的账户、上传、会话、画布、Task/Asset 隔离与管理审计 | 15/15，通过；owner 覆盖参数无效，跨用户读写拒绝 |
| 管理/账户/Skill/effective 浏览器回归 | Chromium/WebKit 桌面及移动共 4/4 组，通过 |
| 会话切换及晚到同意响应 | 4/4 组，通过；拒绝仅一次写入、零 Task |
| MCP 与配置免费 API | 默认预置真实协议发现/调用、session 范围、普通账户 403、revision 409 和配置恢复通过 |
| 配置与待同意步骤重启恢复 | 通过；新轮次确实使用新 Prompt，旧冻结步骤仍保留原同意 ID |
| 已冻结 Skill 的真实出图 | **失败**：上游已完成，内联图片校验/本地存储失败；Task 正确停止重发，但尚未完成生成验收 |

浏览器组使用全新上下文、禁用缓存并阻断非服务同源请求；上述组中外部请求尝试、page error、CSP 错误和意外生成写入均为零。WebKit 证据覆盖对应表单/工作区浏览器内核与移动视口，不等同于真实 Safari 设备或所有触控路径。

权限回归不宣称覆盖不存在的普通上传 DELETE、管理员专用画布 DELETE，或没有实际 pending ID 的同意/计划探测；非零用量统计比较、其他管理详情/下载审计仍需相应实测。临时用户与会话按各测试脚本恢复并验证；保留的画布夹具不参与付费生成。

失败 Task 为 `task_7865f157c44fb66e1b2c74b858bb6216`，Offering 为 `openai/gpt-image-2/text-to-image`，公开状态 `FAILED`、错误类别 `integrity_failure`，没有 READY 产物。私有事件为 `materialization.inline_store_failed`。候选 DB 没有保存内联产物 claims 或失败图片，旧实现也丢弃了底层错误；现有证据不足以将原因归为 PNG 内容、权限、磁盘或超时。未盲目重放该付费任务，未将失败改写为成功。

按发布失败协议停止唯一 Art AI 服务、保存候选状态、恢复发布前 `data` / `storage-go` / `web-data` 一致性备份，再恢复 `art-ai-go-production:9b63316-20261001T174917Z`，OCI revision 为 `9b6331682333d735cc34a3bfb0c42cc155557efc`。回滚后健康、L0 16/16、L1 12/12 通过。未运行 ETL，未混用新旧数据库，未触碰同机其他业务。

私有失败报告保存在测试栈 `production/reports/release-failures/598ff4e-20261002T235708Z-skill-regression/`；候选状态和 receipt 可供后续只读核对，不把其中日志、运行凭据、私有上游字段或数据提交到仓库。

## 7. 诊断版交付、内容回归与回滚

诊断增量提交 `ac377a2f207f3f29c88790798818b53e0408e9b2` 已通过七项本机门禁、Astra 终审及原生 CI 30400，MR !84 正常合并为主线 `6fee0c04474b3988930ddff8782f6c7d7d558c13`。标准发布脚本部署镜像 `6fee0c0-20261003T004030Z`，OCI revision 与完整主线 SHA 相等。

`local.WriteError` 保留原 cause 的 `Error` / `Unwrap`，仅向 Generation 的服务端日志提供白名单 stage/reason/errno；内联产物日志另含字节数、上限和检测 MIME。PNG 拒绝可区分外部地址、块结构、来源元数据 CRC 和检查大小。日志不格式化原错误，不写私有路径、地址、内容或未验证图片；公共失败原文、内容校验、CAS 和失败不重发策略保持不变。真实 Store 的字节一致性、内容/大小/IO 拒绝与清理，以及单次上游调用、错误链、日志白名单和公共失败隔离测试通过。该诊断没有改变内容接受条件，也不能证明历史存储失败的原因已修复。

| 诊断版回归 | 结果及范围 |
|---|---|
| L0 / L1、免费 API、两用户隔离 | 16/16、12/12、8/8、15/15，通过 |
| 管理/账户/Skill/effective、晚到同意响应 | Chromium/WebKit 桌面及移动，各 4/4 组，通过；外部请求、page error、CSP 错误和意外生成写入为零 |
| 普通用户的冻结 Skill、发布新配置并重启 | 原 pending ID、配置 revision 持久化；新对话使用新 Prompt 且零工具；旧冻结步骤只提交一个 Task，通过 |
| 默认 MCP、普通账户非零用量与产物 | 实际 JSON-RPC、跨 owner/session 拒绝、本人成功 Task/用量、缓存图片字节与 SHA-256，通过 |
| 选择 Skill 的实际图片内容 | **失败**：Task `task_a28bca5c6d9378b70a80e8e7dcbd6e73` 物化成功，但图片采用了 Skill 的固定人物与场景，没有用户要求的橘猫 |
| L2 | 7/7，通过；Task `task_e8bb20baf822e6af4ebcf6c847c33ed9` 的 633762 字节图片、SHA-256 与橘猫简笔画内容核对通过 |
| L4 / 3D↔2D 浏览器往返 | 诊断版验收时尚未运行；最终修复版 Meshy 的所记往返回归见 §8 |

内容失败与 Task 完整性成功是不同事实，不将成功 Task 改写为失败。所选预置 `skill-001-mappa-style-anime-action-still-jujutsu-kaisen-aesthetic` 的完整场景 Prompt 与用户意图简单拼接，未声明优先关系。只读核对冻结输入确认 1239 字符中两者都存在；不归因于截断。其缓存图片留作目视证据，不盲目重发该 Task。

待已提交的 L2 结束后，按一致性回滚协议保存诊断版候选状态，恢复 `release-6fee0c0-20261003T004030Z` 的 `data` / `storage-go` / `web-data`，再启动 `9b63316-20261001T174917Z`。旧镜像 revision 为 `9b6331682333d735cc34a3bfb0c42cc155557efc`；健康、回滚 L0 16/16 和 L1 12/12 已确认。私有报告在测试栈 `production/reports/release-failures/6fee0c0-20261003T004030Z-skill-intent-regression/`，未混用迁移后的数据库，未运行 ETL 或触碰其他业务。

## 8. Skill 意图修复与最终验收边界

预期：明确用户的主体、动作、内容与参考图片优先；Skill 只提供兼容的风格、线条、颜色、光影和构图。用户明确要求复刻完整 Skill 场景时，才按该请求采用场景内容。优先规则由 Product Schema 的文/图生图模板和默认 2D 上下文声明；不批量重写预置 Skill、不强行覆盖管理员已发布内容、不改变原 checkpoint、冻结输入或 ExecutionGrant。

源码修复仅更改上述三个 Schema 字符串，162 条预置 Skill 未重写。Catalog、Generation Tool 和 App 测试覆盖默认模板实际组合、原 Skill 内容保留、自定义短模板截断、旧冻结模板与旧 Skill，以及真实 PreparedInput 中的优先关系。五个定向后端包、七项本机门禁和 Astra 最终六文件方案/代码终审均通过；前端仍为 61 文件 / 351 项测试。测试只验证准备输入合同，不能证明生成模型服从规则。

修复提交 `fa82dfee57b934df68862d6ae0ed5dd9ad3d148d` 经 MR !85 正常合并到 `main`，主线 SHA 为 `378fc444864ddd327ac3826f94880638fc0b5e2a`。原生 CI 30402 七项必需门禁、七项本机门禁及 hook 检查通过，Astra 六文件终审通过，Issue #90 原生 readback 为 closed。标准发布脚本部署镜像 `378fc44-20261003T011417Z`，服务健康且 OCI revision 与精确主线 SHA 一致。发布前镜像为 `9b63316-20261001T174917Z`（OCI revision `9b6331682333d735cc34a3bfb0c42cc155557efc`）。

| 修复版部署后回归 | 结果及范围 |
|---|---|
| L0 / L1 | 16/16、12/12，通过；Chromium/WebKit 启动、Schema 注入、CSP、同源请求、健康与就绪、登录、会话、挂起 turn 取消 |
| 免费 Schema / Offering API | 8/8，通过；20 个 Offering、5 个 Provider 的 22 个 discovery 参数、9 个页面导航；未知 Offering 被拒绝且零 Task、未请求同意 |
| 两名普通用户 owner 隔离 | 15/15，通过；账户资源、上传、会话/消息、画布、Processing Plan/Consent scope、管理筛选和审计、现有成功 Task/READY 资产隔离；临时 session 和 user 清理通过 |
| 管理与 Skill 选择 UI | Chromium/WebKit 桌面与移动共 4/4 组通过；修订冲突、恢复、RBAC、Skill session 隔离和安全摘要；外部请求/page error/CSP error/付费写入均为 0 |
| 同意交互 UI | Chromium/WebKit 桌面与移动共 4/4 组通过；每组 fixture consent PUT 2 次、zero-Task session 2 个；外部请求尝试/page error/CSP error/Generation 写入均为 0 |
| 新配置及 pending Skill 重启 API | 所记 8 项通过：配置读取与 CAS、普通账户 403、MCP 现代协议读取及 owner 隔离、单个精确 pending Skill 请求、重启后 ready、配置/pending 持久化、新 turn 使用已发布 Prompt marker。此项不代替实际图片内容验收 |
| 修复版 Skill 实际图片 | 目视通过：`task_5dccc26f99c95fb6397d3c5cd0030a0c`，`session_a4Qtg6XUPMu7vOD50KgdJjOR`，READY `asset_ba4c620ebf758f4b4b3f5be9eae33fb4`；根任务目视确认橘猫线稿主体并带相容的动漫线条风格。未声称复刻完整 MAPPA 场景，也未声称 162 条 Skill 均已验证 |
| 修复版 L2 | 7/7 通过；`task_c3ad3f3b73c014338df0137dbd5f1aa3`，`session_HP1nepHDif1WzD4wq8uEBAa8`，READY `asset_01608d7cb36efe5ee911275a341dba48`，678688 bytes，SHA-256 `6d01a4130994816fd0933db071fa1747810e43715f5cb5dd514a9cdbb2631620`；helper 与实际 PNG 橘猫目视检查通过 |
| Meshy 3D→2D 画布往返 | Chromium/WebKit 桌面与移动 4/4 组通过。验证模型旋转、按源 Task/Asset 截取 2048 长边视图、导入 2D 画布、铅笔绘制、缩放/平移后以独立坐标计算矩形、刷新持久化及实际 PNG 导出。根任务目视确认桌面 3D 白色凳子与 512×512 导出中的凳子、笔迹和矩形；外部请求尝试、缺失资源、page error、CSP error、付费写入均为 0，Task 集合未增加。受控回执：`model-canvas-report-1790991207482/summary.json`。Meshy GLB 有 0 个 texture，只证明本次几何与截图路径，不证明纹理覆盖 |
| Meshy / Tripo L4 多格式资产物化 | 两个 Task 均 `SUCCEEDED`。Meshy 六种必需格式（3MF/FBX/GLB/OBJ/STL/USDZ）及预览；Tripo 六种必需格式（converted GLTF/USDZ/FBX/OBJ/STL/3MF）及预览，各资产均经两次同源下载并核对字节和哈希。脚本对公开 API 绝对 URL、已知 Provider 域及容器内容的隔离扫描通过。Tripo `converted.gltf` 实际为自包含 GLB，主模型 63,436,088 bytes，SHA-256 `c7f8e5a0d401afb54aaa0c986e7f24379c306ae67c6ace7710444fd119a0f2e1`。受控回执：`intent-l4.log`；此证据不代表纹理在浏览器渲染已验收 |
| Tripo 纹理与画布浏览器检查 | Chromium/WebKit 桌面与移动 4/4 组通过。每组使用全新上下文、禁用缓存并阻断非同源请求，实际加载和旋转有木纹纹理的模型，按精确 Task/Asset 截图送入 2D 画布，使用铅笔、缩放/平移和独立坐标计算绘制矩形，刷新恢复 ID/图像状态并导出非空 512×512 PNG。每组外部请求尝试、缺失资源、page error、CSP error、付费写入及目标 session Task 增量均为 0。根任务目视确认 Chromium 桌面 3D 视口和导出中的真实木纹凳子与构图/笔迹/矩形；WebKit 桌面与移动截图也经 worker 目视确认清晰。受控回执：`model-canvas-report-1790991596927/summary.json` |

系统管理及同意界面证据使用 Chromium/WebKit 浏览器内核，不能描述成真实 Safari 设备或完整触控验收。默认 MCP 通过只覆盖当前启用的本地只读预置；外部 Streamable HTTP MCP 仍未开放。Skill 图片和 L2 结果只支持本表对应的具体任务与内容，不推断通用模型服从性。

**最终 L4 与纹理边界：** Tripo Task `task_0b29de9e9b8f2f8258f65d82e6302095`（`session_bjtXkoD2cQy2s0Yfp-CsNoh-`）和 Meshy Task `task_1f8615eb211da37234177e739d1509d7` 均为 `SUCCEEDED`。各自六种必需格式和预览的服务端缓存、两次同源下载字节与哈希核验，以及公开 API 绝对 URL、已知 Provider 域和模型容器隔离扫描均通过。Tripo 只读模型回执确认 3 个 embedded bufferView images、3 个 textures、0 个 external dependencies；纹理浏览器回归 4/4 通过。Meshy GLB 有 0 个 textures；该任务的画布回归只证明几何与截图路径，不能作为纹理覆盖证据。模型/画布 harness 的三次早期定位问题发生在生成提交以外；按实际稳定 ID 与资产顺序修正测试 harness 后，最终各四组断言通过且未降低断言范围。早期 harness 错误不作为产品故障结论。

本次记录的运行验收已完成；不据此声称真实物理 Safari 设备/所有触控路径、全部 162 条 Skill 或外部 MCP 服务已验收。Issue #90 原生状态为 closed。

发布继续遵守项目常设授权和 `scripts/release-go-testenv.sh`；只发布已推送的 `origin/main`，不将旧镜像说成本地改动已上线。失败项按现行回滚协议处理；真实付费同意只允许测试操作代批准，不实现产品自动批准。
