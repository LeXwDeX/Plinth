# v3 架构先行切片：发布前回归快照

> 本页记录发布前的工作树检查和当时尚未闭合的验收项，不再代表当前状态。2026-09-29 的精确提交、内部 CI、dev-test 实单、重启恢复及 CAS 扫描证据见 [06：最终架构验收](06-final-acceptance.md)。

## 发布前工作分支验收（历史快照）

截至 2026-09-29，工作树级自动化结果如下；这些结果尚未绑定最终提交 SHA，且并行工作树仍可能变化，不能当作最终交付门禁或部署验收。

| 验收项 | 当前证据 | 状态/限制 |
|---|---|---|
| `go test -count=1 ./...` | 退出码 0 | 修复 Stop/付费准入竞态后的当前工作树全量 Go 测试通过；最终 SHA 尚待确认。 |
| `pnpm exec tsc --noEmit` | 退出码 0 | 当前工作树类型检查通过。 |
| `pnpm test` | 50 个文件、269 项；退出码 0 | 当前工作树前端测试通过。 |
| `pnpm build` | 退出码 0 | 当前工作树前端构建通过；CSS 生成规则与大 chunk 有非阻断警告。 |
| `go vet ./...` | 退出码 0 | 当前工作树静态检查通过。 |
| `CGO_ENABLED=1 go test -race -count=1 ./...` | 退出码 0 | 当前工作树全量 race 检查通过。 |
| `CGO_ENABLED=0 go build -trimpath ./cmd/art-ai` | 退出码 0 | 当前工作树生产入口构建通过。 |
| 最终 SHA / CI / 合并 | 待补证据 | 回填最终提交 SHA、MR/PR 与 pipeline ID，并核对 terminal 结果及精确 source/merge SHA。 |
| dev-test 发布与运行验收 | 待执行/待读回 | 发布精确 commit、镜像及 OCI revision、备份、容器/readyz、迁移/回滚和相关页面/API 证据。 |
| CAS 历史数据离线扫描 | 待执行 | 停止写入后的 `art-ai-storage --check` JSON、退出码、数据库/存储身份与异常处置；只读检查不能冒充自动修复。 |
| Stop 与付费准入竞态 | `TestAgentStopBeforePaidAdmissionRejectsTaskAfterAssetValidation` 将普通 Agent/Skill Step 提交停在资产校验；Stop 先持久化，再放行提交。SQLite grant 定向测试覆盖先 Stop 后 Claim、先 Task 后 Stop 的精确重放、跨 owner/session/缺 turn。 | 两条共享内核路径均未新增 Task、Grant 未 bound；Skill Agent 全链与测试环境可见状态仍须实测。 |
| Scene/Render/SkillRun | Scene owner/CAS/候选扫描，Render 服务端生成与 Manifest 输出绑定，SkillRun 三步记录/人工 Grant/幂等回放/API/Agent 首步均有定向测试且纳入全量 Go 门禁。 | 旧 Canvas 到 Scene 的持久迁移、浏览器像素对照、三视图后两步的工作区交互与真实模型结果待产品/环境验收。 |
| 3D viewport Capture | `go test ./internal/adapters/sqlite -run Capture -count=1` 与 `go test ./internal/transport/httpapi -run ModelCapture -count=1` 通过。覆盖 owner/source Task/模型 Asset 关系、PNG 解码与 CAS 字节摘要、幂等重放/冲突、匿名拒绝、Cookie CSRF、跨 owner 404 与 caller ID 覆盖。工作区已有截图并入 2D 画布代码。 | API 身份/CSRF/跨 owner 的定向路径通过；真实 WebGL readback、画布刷新与 Scene Representation 导入仍待验收；客户端截图来源记录不代表可信重渲染。 |
| Hyper3D | 未启用 | Key、签名下载 host 与费用映射未齐；模拟测试不构成真实模型验收。 |
| 测试环境发布回滚 | 发布脚本在候选 `up` 失败或健康失败时先停候选，恢复备份的数据库/资产/网页数据，再启动旧镜像；`bash scripts/smoke/test-release-rollback.sh` 两类故障注入通过。 | 该离线回归不替代 dev-test 发布时的备份、镜像、迁移、健康与回滚读回。 |

七项门禁均是当前工作树证据，不替代精确 SHA CI、部署后的 API/浏览器/资产验证。`PreparedInput`、执行授权、Chat `WAITING_TASK`、Scene/Render 与 CAS 项的代码/测试状态按前述架构章节记录；本表不把单元测试通过推导成运行链路或付费验收完成。

历史记录：基线提交 `a32a93799c08cf98c6f5e0481e233809233b1b7c`；当时实施分支为 `codex/art-ai-architecture-v3`。**下述命令和状态只描述该次切片在记录时的证据，不是当前 `main`、测试环境或新分支 `codex/art-ai-architecture-completion` 的验收结果。**新分支的门禁、PR 和部署须另记精确提交与结果；Hyper3D 在没有 Key 时仍不能用模拟测试替代实单验收。

## 已落实的架构切片

| 切片 | 问题与代码结果 | 本地验收证据 | 状态 |
|---|---|---|---|
| 资产 CAS 完整性 | 旧路径命中已存在内容寻址文件时不验证现存字节，可能把损坏文件当 READY；`local.PutBlob` 现对大小与 SHA256 核验，损坏则用同步临时文件原子修复；读取通过受限根目录与 `O_NOFOLLOW` 防止叶符号链接越界 | 正常命中、同尺寸损坏、并发修复、父/叶符号链接及读取越界测试；Go 定向测试通过 | 本地验证；已有损坏 blob 在没有重新写入时仍需离线扫描 |
| Processing Plan 费用一致性 | `Resume`/后台 `advance` 原先只比较 Offering 单件价；现对冻结的 step input 使用 `EstimateTotalCredits`，数量参数变化后不会误恢复或反复暂停 | `n=3`、目录修订价、错误确认金额不提交、正确金额只提交一次；Go 普通/竞态通过 | 本地验证；沿用既有人审，不新增报价批准 |
| 私有资产下载缓存 | owner-scoped 任务产物、输入资产、旧生成图及旧资产代理统一为 `private, no-store`；代理不发通配 CORS，Schema 校验拒绝不安全缓存声明。ETag 仅在所有权验证后产生 | 正常、条件请求、跨用户与 Schema 弱化负控测试；本地 mock 任务的资产 GET 实测 200、`private, no-store`、非空 PNG | 本地验证；旧版已缓存副本不会被新响应头主动清除，需测试环境代理响应复核 |
| Hyper3D/Rodin | 注册新协议、隔离 Adapter、Schema connection 与两项文/图生 3D Offering；固定 Gen-2.5 tier 与 GLB 格式，分开持久化状态订阅键与下载 UUID；安全取回由资产 host allowlist 控制，错误页不作为模型 | httptest 协议、GLB 内容负控、参数 wire 映射和 Catalog 测试通过；无 Key、Offering 禁用、不可提交、运行设置不进入公开 Schema；本地 HTTP discovery 不出现未启用 Offering | 协议模拟已验证；真实 Key/下载 host/费用换算缺失，**未启用** |

## 历史切片的本地门禁（非当前工作树结果）

当时基线切片按 `dev_test.md` §2 执行：`pnpm exec tsc --noEmit` 通过；`pnpm test` 为 48 文件、261 项通过；`pnpm build` 成功（保留 CSS 优化和大 chunk 警告，不影响退出码）；`go vet ./...`、`go test -count=1 ./...`、`CGO_ENABLED=1 go test -race -count=1 ./...`、`CGO_ENABLED=0 go build -trimpath ./cmd/art-ai` 全部退出码 0。`git diff --check` 通过。针对性的 Hyper3D、Catalog、App、Processing、资产、HTTP 测试另有同轮通过记录。

本地运行精确工作树的 mock Go 服务（127.0.0.1:18333、临时数据库/存储）：健康、就绪、Schema、Offerings、扩展 Providers 与首页均为 200；Schema 不含 Hyper3D 运行设置名，公开列表不包含禁用的 Hyper3D。直接创建一项 mock 文生图 Task 获得平台 Task ID `task_9cf17d1c1a418658abd42a9cb968f908`，到 `SUCCEEDED` 后有一个本地资产，下载 200、PNG 魔数正确、`Cache-Control: private, no-store`。这只验证 mock 链路，不代表真实上游图像或 3D 效果。

## 历史发布前记录（截至 2026-09-29）

本节原列的 G1/G2/C、Hyper3D 和同提交测试环境事项，是该日期的发布前状态快照，不作为当前 backlog。当前计划与进度统一在原生 Milestones、Issues 和 MRs 跟踪；相关记录入口为 #162、#166、#173、#176–#179（见 !118）、!113 和 !118。请以原生记录最新读回为准。

当时的 Hyper3D/Rodin 结论仍保留在 [04](04-hyper3d-provider-fit.md) 作为历史协议与安全门槛材料；该文件不能替代当前原生验收状态。

回滚：隔离分支尚未触及共享测试栈或数据库；本地局部修复可按文件/提交反转。本分支新增 0030–0040 数据库迁移，部署前须留备份并按发布脚本预检，不把回滚镜像等同数据库降级。Hyper3D Offering 关闭，即使连接声明存在，也不能创建付费任务。
