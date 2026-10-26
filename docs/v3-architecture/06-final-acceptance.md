# v3 架构实施：2026-09-29 历史验收记录

本页只记录 2026-09-29 精确提交的架构代码与测试环境验收证据，不代表当前源码、部署或验收状态。文中的未覆盖范围是当时的验收边界，不是当前待办清单；当前状态只在 原生 Milestones、Issues 和 MRs 维护。

## 不可变交付身份

| 证据 | 读回结果 |
|---|---|
| 架构 MR | GitLab !76，source `f7abef16c3112e80d9d0e769043350c32348f972`，merge `e2e31c552c806f544e6110896e203e9a77cb00b1`。对应 Issue #78–83 均已关闭。 |
| 架构 CI | source pipeline 30363 和 merge pipeline 30364 均为 `success`；前端类型/测试/构建、Go vet/test/race/build 全绿。合并提交与 source 树相同。 |
| L1 脚本修复 | 新的 Cookie 写入同源门禁使旧脚本的无 `Origin` 写入正确返回 403；GitLab !77 仅修复 smoke 脚本。source `8a7ce6543adf4d1330dfe888bb654d43078552a0`，merge `66d560a201aa33742b6b5e0982e72b596c25f4b8`，Issue #84 已关闭。 |
| 最终 main CI | source pipeline 30365 和最终 main pipeline 30366 均为 `success`。本地推送前七项门禁、`bash -n`、ShellCheck warning 级、`git diff --check` 通过。 |
| 最终发布 | dev-test 测试栈镜像 `art-ai-go-production:66d560a-20260929T021801Z`；容器 OCI revision、`releases/manifest.txt` 与最终 main 均指向 `66d560a201aa33742b6b5e0982e72b596c25f4b8`。测试栈容器为唯一健康实例，`healthz`/`readyz` 均 200；迁移头为 40。发布前一致性备份保留在 `backups/release-66d560a-20260929T021801Z/`。 |

## 测试环境行为

| 链路 | 结果与边界 |
|---|---|
| 浏览器与鉴权 | 最终镜像上，Chromium/WebKit 未登录首屏 L0 为 **12/12**，Schema 注入、React 挂载、CSP、页面异常和 console error 均通过；测试机本机 Python Playwright 的已登录 Chromium 工作区返回 200 且无 CSP/console 错误。部署后 L1 为 **12/12**：同源 Cookie 写入成功、跨源写入 403、Agent 挂起后停止并收束。 |
| 2D Agent 和资产 | L2 **7/7**：双会话待同意时第三会话仍可响应；A 明确批准后 Task `task_72eaa725df7c237c40606fa020cfd3e5` 到 `SUCCEEDED`，B 拒绝后零 Task。L3 单模型一图 **6/6**：`task_1ebf46564f10f6d3d9e731d88a960edd` 的 1,620,028 字节图片下载、媒体魔数、服务端 SHA-256/大小均匹配，跨用户 Task 与 Asset 均 404；报告 `production/reports/smoke-l3/l3-manual-20260929T095117Z.txt`。这些真实模型测试在架构代码镜像 `e2e31c5` 上执行，随后仅合入 L1 脚本修复并重部署同一运行代码。 |
| 三视图 SkillRun | `skill_6a485c879cd85dea7f923a40539b941adf932dac7fd1f61e` 以本地源图创建。正、侧、背三步分别生成 `task_382dcce9364cd93d3cdad369efef989c`、`task_dde553c4244c1a2c2246e2ad45ce9b96`、`task_50b04b91856d2715b0acabe8e4407c03`；每步经过准备→人类测试同意→公开 Task→本地 READY→审阅接受，下一步消费上一产物。首步重复 `submit` 回放原 Task。最终重启后，三步仍为 `output_ready/accepted` 并保有各自的输出 Asset ID。测试期的批准由 Agent 在产品外执行；产品里没有自动同意。 |
| 3D Agent 和模型 | Tripo `tripo/tripo-v3.1/text-to-3d` 真实任务 `task_6cc78be4366c522961cd68e7790b5b29` 经 Agent 精确同意到 `SUCCEEDED`；GLB 资产 `asset_49cd580caeda8c7f00a6167bc0952125` 为 70,387,160 字节，下载 SHA-256 与声明一致，文件头为 `glTF`。重启后 Task 与模型资产仍可读取。旧的直接付费写入 `/api/meshy/text-to-3d` 在已登录同源请求下返回 403。 |
| CAS 离线扫描 | 对最终发布**停机窗口的一致性备份副本**执行 `art-ai-storage --check`，退出码 0，`read_only:true`，81 个 blob、91 条引用、`issue_count:0`。报告保留在 `production/reports/cas-integrity-66d560a.json`；隔离解包副本已清理。这个扫描证明该时间点备份中的引用完整，不保证未来写入或外部上游永不失败。 |

## 该次验收的未覆盖范围（历史边界）

- Hyper3D/Rodin 依[官方 Gen-2.5 文档](https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5)完成 Schema 加隔离 Adapter 的**无 Key 协议验证**，但仍 `enabled:false`。真实 Key、签名下载域名 allowlist、供应商 credit 到平台积分的换算及真实文/图生 3D 结果尚无证据，不能启用。
- 3D→2D Capture 的所有权、源 Task/模型 Asset、PNG 字节和幂等契约有本地测试；真实 WebGL 像素与持久 Scene Representation 的浏览器端闭环没有本次实证，不能从 API 通过推导成所见即所得的全面 UX 验收。
- Remesh/Rigging/Animation 的完整 Agent 连续处理和工作区操作体验仍属后续产品实施。已有公开 Offering/Processing Plan/Generation 边界与同意机制，不把尚未验收的真实付费处理链写成完成。

以上只说明该次验收未覆盖的部分，不构成当前状态判断。该次验收确认的架构边界为：Product Schema 管可变项，Adapter 只管上游 wire，Generation 独占公开 Task/Attempt 状态机，Agent 和 Processing 通过冻结输入及单次人类执行授权提交，API/资产按 owner 与 READY 事实对外投影。新的 Provider 或 UX 工作应复用这些边界，不另造任务生命周期。
