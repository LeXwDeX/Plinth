> 阅读入口已整理到 [现行架构](../architecture.md)。本文保留阶段决策与历史证据；其中“当前”、待办及验收仅适用于记录的分支/日期，不代表最新源码或部署。当前状态以原生 Milestones、Issues 和 MRs 的读回为准。

# Plinth v3：架构契约与历史证据

历史审计基线：`a32a93799c08cf98c6f5e0481e233809233b1b7c`。本目录保存 v3 审计形成的工程契约及按日期记录的历史证据；“目标”和历史实现描述均不代表当前代码、测试栈或验收状态。当前计划与进度只在 原生 Milestones、相关 Issues 和 MRs 中维护，不在文档复制状态表。Milestone 1「V3 Agent 验收」、2「能力扩展」、3「安全保留」、4「正式迁移准备」分别承载相应目标。

## 阅读顺序与职责

| 文档 | 对应工作包 | 决定的问题 |
|---|---|---|
| [01 身份、目录与输入](01-identity-input-scene.md) | D01、D02、D04 | 谁拥有和改写对象；Offering 与输入如何冻结；画布虚线与 2D/3D 表示如何保持所见即所得 |
| [02 运行时可靠性](02-runtime-reliability.md) | D03，兼顾 D01/D02 | Task/Attempt/Plan 状态、租约、幂等、结果不明、物化和资产缓存的失败边界 |
| [03 API、安全与工具](03-api-security-tools.md) | D02、D05，兼顾 D01 | 正式/兼容 API 的鉴权、所有权、CSRF、公开投影、下载、Tool/Skill/MCP 权限 |
| [04 Hyper3D 接入验证](04-hyper3d-provider-fit.md) | Provider 扩展性验证 | Rodin Gen-2.5 的 Schema/Adapter 边界、无 Key 的封闭状态及验收门槛 |
| [05 本地回归历史证据](05-local-regression.md) | 历史局部切片 | 该记录日期的代码检查与回归证据 |

已有 `docs/go-architecture.md` 和 `docs/provider-schema-architecture.md` 解释现行 Go 内核与 Provider 边界；本目录记录 v3 的增量契约及历史材料。本文不把 v1 的模型 ID 注册、双状态、上游 ID 替换平台 ID、进程内定时调度、阻塞创建、缓存失败判成功带回来。

## 单一事实链

以下内容是 v3 架构契约与 2026-09-29 工作分支记录；其中实现/验收措辞仅适用于原记录日期，不表示当前进度。

```text
Principal/owner + ChatSession + SceneObject(revision)
  + CatalogBundle(revision) + Offering(id, revision) + 参数/输入 Asset
  → PreparedInput/RenderManifest（内容冻结；尚未提交）
  → ExecutionGrant：精确绑定冻结输入与幂等操作（按会话批准模式由人或批准策略批准）
  → Generation.SubmitPrepared（唯一公开 Task/Attempt 状态机）
  → Provider Port/私有检查点 → Observation → 必需 Asset 本地 READY
  → Task/Event/Asset 的 owner-scoped API 投影
  → Chat/Plan/工作台引用同一个 Task ID；SceneObject 以 revision 比较更新表示
```

`ExecutionGrant` 是原有人类付费同意的持久、精确执行凭据，不是新的“报价批准”产品功能。输入 hash、费用估计和创建待决请求都不等于批准；现状下，只有独立的 Cookie 人类决定能把待决请求变为可提交状态。本轮从 3D Processing Plan 请求和响应中移除了 `approved_credits` 报价批准字段；数据库旧列暂保留为 0 以兼容回滚。原生任务、Agent、画布直提和 Processing Plan 均接入同一冻结输入及授权服务；旧兼容写入口先封闭，避免保留未授权旁路。

ExecutionGrant 按会话批准模式签发（规则见 `docs/product.md`）；此处机制说明保留自 2026-09-29 的实现记录。

机制：

- 每个会话保存批准模式（`go_chat_sessions.approval_mode`，迁移 0043）。默认手工。
- 手工模式下哪些能力要人批准，写在 Schema 的 `application.chat.approval_policy`。纯函数 `internal/approvalpolicy` 按“策略、模式、冻结输入的能力”判断，不读写状态。模式或能力不认识时按要人批准处理。
- 每个创建 Grant 的入口都在 `Prepare` 之后调用 `executiongrant.Service.ApplyPolicy`：Agent 工具、SkillRun、独立任务 Grant（`POST /api/v1/tasks/grants`）、Processing Plan 步骤。不需要人时，`ApproveByPolicy` 才把待决 Grant 变为已批准。
- `ApproveByPolicy` 的能力取自服务端保存的冻结输入，不取自调用方。它核对 owner、待决状态和冻结身份。不是待决状态会返回冲突。需要人时返回 `ErrHumanRequired`，Grant 保持待决。
- 自动批准的 Grant，`decided_by` 是 `policy:<模式>`，同时写入不可改的 `execution_grant_audit`。真人批准的记录是用户 ID。同一份 Grant 重复自动批准是幂等的。
- 之后的认领、绑定、重放核对与人批准完全相同。Grant 仍精确绑定冻结输入、操作和幂等键。
- Processing Plan 在服务端自动批准步骤：先保存冻结输入和 Grant 引用，再提交。崩溃后按步骤键核对，不盲目重发。任何一步出错都退回“暂停等人确认”，不会提交。
- 切换模式：`PUT /api/v1/chat/sessions/{sessionID}/approval`（路由在 Schema 中声明）。要真实的浏览器登录会话，只按 owner 读写，真实变更写审计。读取用同一路由的 GET。
- 积分：Agent 工具结果和观察、聊天消息、会话 DTO 都带估算积分，字段名来自 Schema。会话累计值由该会话已接受任务的冻结报价与输入累加，价格以后变化不会改写历史。累计值是估算，不是结算。
- 切换模式不会回头批准已经待决的 Grant。再次请求时按当时的模式判断。Agent Grant 的策略只评估一次，不会因之后的切换、恢复或巡检被批准。
- Agent 请求等待超时后轮次结束，Grant 保持待决并在 Schema 声明的期限内（`application.chat.pending_approvals.ttl_seconds`，默认 24 小时）可批准。之后的批准只提交一次冻结输入，结果以助手消息回到同一会话；过期的不能批准。机制见 `docs/architecture.md` 第 7 节。

## 状态记录边界

本目录中的状态措辞、验收描述和计划均属于其所标注日期/提交的历史记录，不是当前进度表。当前跟踪统一使用原生 Milestones、Issues 和 MRs；#162、#166、#173、#176–#179（见 !118）、!113 与 !118 是相关记录入口。请以原生记录最新读回为准。

以下架构材料保留职责、数据契约与写入边界，不再将完成状态或待办复制到文档。

## 各层写权限

| 事实/动作 | 唯一写入者 | 只读或受限消费者 |
|---|---|---|
| Product Schema 与 Offering 版本 | Catalog 构建/发布流程 | Agent、Generation、Provider、HTTP、SPA 读取编译结果 |
| ChatSession/Plan/SkillRun 意图与步骤 | Chat/Processing 业务服务，按 owner 与版本校验 | HTTP 翻译，SPA 投影；不能改 Task 终态 |
| SceneObject/Representation 与 scene revision | Scene/Canvas 业务服务，事务性 compare-and-swap | Agent 与 renderer 使用已冻结版本 |
| 公开 Task/Attempt/资源态与事件 | Generation 内核 | Provider 只提交 Observation/asset claims；HTTP/Agent/Plan 不自行推进 |
| 本地 Asset 字节与 READY 证据 | 资产模块经所有权和完整性校验 | Generation 判定必需资源，API 按范围读取 |
| Provider 私有 remote job/checkpoint | 对应 Adapter，经内核派发许可与大小约束 | 只能投影安全进度/资源事实，不进入公共 Task ID |

依赖方向仍为 `HTTP/SPA → Agent/Scene/Generation/Assets → Provider Port/Adapter`；Catalog 向各层提供声明，但不反向依赖业务。跨层只传稳定 ID、版本、白名单 DTO 和事件，不共享可变对象指针或直接访问另一层私有表。

## 契约评审记录

原 G1 表格及其状态属于 2026-09-29 工作分支的阶段评审快照，已从此入口移除，避免作为当前清单使用。G1 的问题域仍可作为架构讨论参考；需要跟踪的当前交付和验收项请在原生 Milestone/Issue/MR 中查看。G1 文档与契约评审不等同功能或部署验收。
