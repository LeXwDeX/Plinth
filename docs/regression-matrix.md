# 回归测试表

本表记录每个已交付行为由哪一层、哪个命令覆盖，以及最后一次在哪个 revision 上验证过。有了这张表，发布前后不用把所有行为重新遍历一遍。
表里的脚本路径由 `scripts/tests/test_regression_matrix.py` 校验（随「scripts tests」门禁运行）：路径必须存在，状态只能取四个值，`GAP` 行必须写备注。

## 1. 用途与用法

1. 发布前、发布后先读这张表。只跑「每次发布必跑」，以及主表里与本次改动相关的行（按「来源 Issue」和「功能 / 行为」对照最终 diff 与影响链），不重新遍历全部行。
2. 跑完一行，把它的「最后验证」改成被测 revision 的前 8 位加日期（如 `f5bc5e10 2026-11-28`），并在「证据」里放 Issue 评论链接（验收结果仍按 AGENTS.md 写进 Issue 评论，本表只放指针）。没跑的行不改；不能把别的 revision 的结果写进来，也不能只凭源码或单测推断线上结果。
3. 改变行为的 PR 必须在同一改动里新增或更新对应的行。没有对应脚本或测试的行标 `GAP`，在「备注」里写要补什么，并开 Issue 跟踪。

**验证层**：`unit`（本机与 CI 的单测：`pnpm test`、`go test`、`scripts/tests`）、`fake-stack`（本机 fake 整栈 `scripts/smoke/smoke-fake-stack.py`）、`dev-test-fake`（dev-test 的 fake 上游形态发布）、`dev-test-live-chat`（dev-test 的 `--live-chat` 形态：对话模型真实、生成走 fake，只耗对话 token）、`PROD-readonly`（正式测试环境 `formal-test`，只读、免费）、`PROD-paid`（`formal-test` 或 `--live-upstream` 的 dev-test 上的真实付费调用，每次先取得用户当次同意）。

**状态**：`OK` 有脚本或测试且自动判定；`MANUAL` 需要人工操作或判读；`GAP` 没有覆盖；`OBSOLETE` 脚本已过期、结果不作为验收证据。

**命令与环境**：对已部署环境的脚本一律经 `scripts/release-env.py smoke <环境>` 注入 `BASE` 与账号，表里为简洁只写脚本本身；fake 专用脚本在目标不是 fake 形态时拒绝运行，真实上游脚本要 `--live` 加 `ART_AI_LIVE_ACK=1`（`scripts/regression/README.md`）。

证据链接：E1 = [#2 评论：dev-test L0/L1/L1 Agent](https://github.com/LeXwDeX/Plinth/issues/2#issuecomment-7228077479)，E2 = [#2 评论：dev-test 相关验收](https://github.com/LeXwDeX/Plinth/issues/2#issuecomment-7228077876)，E3 = [#2 评论：dev-test 验收报告](https://github.com/LeXwDeX/Plinth/issues/2#issuecomment-7228078324)，E4 = [#705 评论：正式测试环境发布后回归](https://github.com/LeXwDeX/Plinth/issues/705#issuecomment-7232418886)，E5 = [#2 评论：正式测试环境生成纹理开关全量真实验证](https://github.com/LeXwDeX/Plinth/issues/2#issuecomment-7233999783)，E6 = [#2 评论：dev-test 生成纹理开关全 Offering fake 覆盖](https://github.com/LeXwDeX/Plinth/issues/2#issuecomment-7234115201)，E7 = [#709 评论：dev-test 真实对话模型画板兜底读取验收](https://github.com/LeXwDeX/Plinth/issues/709#issuecomment-7238702574)。

## 2. 每次发布必跑

| 顺序 | 时机 | 行 | 命令 |
|---|---|---|---|
| 1 | 合并前，对最终 head | G2 | `python3 scripts/host-ci/run.py <sha>` |
| 2 | `release-env.py release dev-test` 的 fake 形态发布后（脚本自动跑并写机器报告） | S1、S2、S3 | `scripts/smoke/smoke-l0-browser-boot.sh`、`scripts/smoke/smoke-l1-free.sh`、`scripts/smoke/smoke-l1-agent-mode.sh` |
| 3 | 晋级 `formal-test` 前，对精确 `main` revision | G4 | GitHub Actions `.github/workflows/ci.yml` 完整运行 |
| 4 | `formal-test` 发布后 | P1、P2 | `scripts/regression/post-release-readonly.py`、`scripts/smoke/smoke-l0-browser-boot.sh` |
| 5 | `formal-test` 发布后 | P6 | 服务端日志只读检查（人工） |

涉及画布的发布另跑 B1（`dev_test.md` §10.3e 的固定验收项）。其余行按本次改动挑选。

## 3. 主表

### 门禁

| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 | 备注 |
|---|---|---|---|---|---|---|---|---|---|
| G1 | 本机门禁（含 race） | #86、#218 | unit | `python3 scripts/verify-local.py --race` | `dev_test.md` §2 的 11 项门禁全部通过（`--race` 加跑第 7 项） | OK | — | | pre-push 跑不带 `--race` 的同一套 |
| G2 | 主机 CI：干净容器跑门禁 | #97 | unit | `python3 scripts/host-ci/run.py <sha>` | 提交状态 `host-ci/gates` success，11/11 | OK | fc2bf5f5 2026-11-28 | E2 | 集成 PR #706 的最终 head，合并后即 f5bc5e10 |
| G3 | 工作流重放 | #86 | unit | `python3 scripts/host-ci/run.py <sha> --workflows` | 提交状态 `host-ci/workflows` success | OK | — | | GitHub Actions 不可用时使用 |
| G4 | GitHub Runner 完整 CI | #257 | unit | `.github/workflows/ci.yml`（GitHub Actions） | `frontend`、`go check`、`go test race` 三个作业通过 | OK | — | | 晋级前对精确 revision 跑，不按 diff 缩减 |
| G5 | 本表与脚本不脱节 | #705 | unit | `python3 -m unittest scripts.tests.test_regression_matrix` | 表里路径都存在、状态合法、`GAP` 有备注、必跑脚本在主表、`AGENTS.md` 引用本表 | OK | — | | 随 G1 的「scripts tests」运行 |
| G6 | 发布门禁脚本的判定（修改点、主表全部小节、fake 差异表、豁免） | #707 | unit | `python3 -m unittest scripts.tests.test_release_gate` | 单测通过 | OK | — | | 随 G1 的「scripts tests」运行 |
| G7 | 发布前过发布门禁 | #707 | unit | `python3 scripts/release-gate.py --base <上一个 tag> --head <候选>` | 门禁 Agent 运行脚本并按 `docs/release-gate.md` 核对脚本判断不了的证据，结论 PASS | MANUAL | — | | 晋级 `formal-test` 前对候选 revision 运行；回执格式见 `docs/release-gate.md` |

### 发布后自动冒烟（dev-test fake 形态发布后 `release-env.py` 自动跑）

| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 | 备注 |
|---|---|---|---|---|---|---|---|---|---|
| S1 | L0 浏览器启动检查 | #411 | dev-test-fake | `scripts/smoke/smoke-l0-browser-boot.sh` | `PASS=16 FAIL=0 SKIP=0`，无 SOFT-FAIL | OK | f5bc5e10 2026-11-28 | E1 | 判定口径见 AGENTS.md「发布后冒烟判定与晋级校验」 |
| S2 | L1 免费冒烟 | #411 | dev-test-fake | `scripts/smoke/smoke-l1-free.sh` | `PASS=16 FAIL=0 SKIP=0` | OK | f5bc5e10 2026-11-28 | E1 | |
| S3 | L1 Agent | #411 | dev-test-fake | `scripts/smoke/smoke-l1-agent-mode.sh` | `PASS=38 FAIL=0 SKIP=0` | OK | f5bc5e10 2026-11-28 | E1 | |
| S4 | 晋级报告四项检查 | #270 | dev-test-fake | `scripts/release-env.py`（`release formal-test --acceptance-note <noteID>`） | `L0`、`L1`、`L1_agent`、`related` 均 pass、`failed=0`、`skipped=0`、`soft_failed=0` | OK | f5bc5e10 2026-11-28 | E3 | `related` 68 项为人工计数 |

### Fake 栈（本机，`scripts/smoke/smoke-fake-stack.py`）

| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 | 备注 |
|---|---|---|---|---|---|---|---|---|---|
| F1 | 门禁子集（`dev_test.md` §2 第 11 项所列场景与故障 `task-failed`、`artifact-expired-stuck`） | #218 | fake-stack | `python3 scripts/smoke/smoke-fake-stack.py --only <dev_test.md 第 11 项子集> --faults task-failed,artifact-expired-stuck` | 全部检查通过，栈日志无回环守卫拒绝（零真实上游） | OK | fc2bf5f5 2026-11-28 | E2 | 随 G2 运行；子集清单以 `dev_test.md` 为准 |
| F2 | 全量（全部场景与 7 个故障场景） | #218 | fake-stack | `python3 scripts/smoke/smoke-fake-stack.py` | 全部通过，约 15 分钟 | OK | — | | 改相关 Adapter 或 fake 时手动跑 |
| F3 | 三视图预算连续执行 | #243 | fake-stack | `python3 scripts/smoke/smoke-fake-stack.py --only three-view` | 场景全部通过 | OK | fc2bf5f5 2026-11-28 | E2 | 门禁子集内 |
| F4 | 自动批准默认、严格出卡、每回合上限 | #406、#432、#702 | fake-stack | `python3 scripts/smoke/smoke-fake-stack.py --only auto-approval` | 49 项全部通过：新会话与公开 Schema 默认 auto；严格出卡且不提交；每回合上限 200，第 201 个付费调用被 Schema 文案拒绝 | OK | — | | 不在门禁子集，改批准逻辑时手动跑 |
| F5 | Agent 链（含 Hyper3D Agent）与重抽 | #218 | fake-stack | `python3 scripts/smoke/smoke-fake-stack.py --only agent,reroll` | 场景全部通过 | OK | fc2bf5f5 2026-11-28 | E2 | 门禁子集内；`hyper3d-agent` 可单跑 |
| F6 | 图像与抠图 | #218 | fake-stack | `python3 scripts/smoke/smoke-fake-stack.py --only images` | 场景全部通过 | OK | fc2bf5f5 2026-11-28 | E2 | 门禁子集内 |
| F7 | 看图预算 | — | fake-stack | `python3 scripts/smoke/smoke-fake-stack.py --only vision-budget` | 场景全部通过 | OK | fc2bf5f5 2026-11-28 | E2 | 门禁子集内 |
| F8 | 概念候选 | — | fake-stack | `python3 scripts/smoke/smoke-fake-stack.py --only concept-candidates` | 场景全部通过 | OK | fc2bf5f5 2026-11-28 | E2 | 门禁子集内 |
| F9 | 自检（参考图比对、记录三态、上限与后台结果） | #246 | fake-stack | `python3 scripts/smoke/smoke-fake-stack.py --only self-check --work <目录> --keep` | 场景全部通过 | OK | — | | 独立栈与私有 Catalog，不并入门禁 |

### dev-test 浏览器验收（fake 上游）

| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 | 备注 |
|---|---|---|---|---|---|---|---|---|---|
| B1 | 画布以比例为准、多图加标记（常设验收） | #653 | dev-test-fake | `node scripts/acceptance/release-1-9-2-canvas-ratio.mjs` | 11/0（C1–C5） | OK | f5bc5e10 2026-11-28 | E2 | 涉及画布的发布必跑；v1.9.6 另在 WebKit 跑过 11/0，脚本本身固定 Chromium，WebKit 需临时改引擎 |
| B2 | 失败通知只提醒一次、三视图卡片标题 | #651、#633、#655 | dev-test-fake | `node scripts/acceptance/release-1-9-2-notices.mjs` | 7/0 | OK | f5bc5e10 2026-11-28 | E2 | |
| B3 | 自动方式下三视图、四视图同一回合出完 | #663 | dev-test-fake | `node scripts/acceptance/release-1-9-3-three-view-auto.mjs` | 6/0 | OK | f5bc5e10 2026-11-28 | E2 | WebKit 同 B1 |
| B4 | v1.9.0 全套（N5 对话栏宽度、#559/N2/#587 点击规则、#611 标记、项目组 P1–P4 等） | #586、#559、#587、#611、#695 | dev-test-fake | `node scripts/acceptance/release-1-9-0-browser.mjs` | v1.9.6：125 通过、3 失败，3 条均为 P4 对话框初始聚焦竞态（隔离重跑约 8 次出现 2 次） | OK | f5bc5e10 2026-11-28 | E2 | P4 偶发失败为已知，不挡发布；其余项须全过 |
| B5 | 浏览器矩阵子集 I18N-03/04、CNV-02..08、SKL-02/03（含 #681 画板动作护栏） | #548、#681、#705 | dev-test-fake | `bash scripts/acceptance/browser_matrix/run.sh dev-test`（`B3_ITEMS=I18N-03,I18N-04,CNV-02,CNV-03,CNV-04,CNV-05,CNV-06,CNV-07,CNV-08,SKL-02,SKL-03`） | 11 项 PASS | OK | f5bc5e10 2026-11-28 | E2 | SKL-02/03 已按 #548 新弹窗重写 |
| B6 | 详情弹窗几何 | #224 | dev-test-fake | `node scripts/smoke/result-detail-geometry.mjs` | 22/0 | OK | f5bc5e10 2026-11-28 | E2 | |
| B7 | 弹层滚动锁几何 | #74、#705 | dev-test-fake | `node scripts/smoke/overlay-scroll-lock-geometry.mjs` | 三个视口下滚动锁与几何检查通过 | MANUAL | — | | 依赖一个有内容的会话（#705-10），全新数据上要先人工造数 |
| B8 | 旧 Studio 场景脚本 | #705 | dev-test-fake | `scripts/acceptance/studio-scenarios.mjs` | — | OBSOLETE | — | | 起步卡等入口早已不存在，历史上 0/43，不属于验收 |

### 按功能的行为

| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 | 备注 |
|---|---|---|---|---|---|---|---|---|---|
| R-681a | 点击矩阵：单击不改画板、双击只插入被双击的一张、「选用」不放进画板、删掉的图不复活、Agent 新结果自动加入一次、舞台双击看大图 | #681 | dev-test-fake | `node scripts/regression/click-matrix.mjs` | 全部 PASS（M、A、B 三段） | OK | — | | 入库前的临时脚本在 v1.9.5 轮跑过，合并后的脚本未整段运行 |
| R-681b | 点击矩阵的放置与护栏逻辑 | #681 | unit | `src/components/chat/result-card-placement-681.test.tsx`、`src/lib/workspace/canvas-placement.test.ts`、`src/lib/canvas/inpaint-entry.test.ts` | `pnpm test` 通过 | OK | — | | |
| R-695 | 对话栏可拖到页宽 50%（给舞台留 320，最窄 440） | #695 | dev-test-fake | `node scripts/acceptance/release-1-9-0-browser.mjs`（N5）；单测 `src/components/studio/chat-resize-handle.test.tsx` | N5 PASS；单测通过 | OK | f5bc5e10 2026-11-28 | E2 | |
| R-696 | 窄窗口右栏自动收起 | #696 | unit | `src/hooks/use-narrow-window-panel.test.tsx`、`src/store/studio-store.test.ts` | 单测通过 | OK | — | | 浏览器侧多窗口宽度验证未入库，需要时人工按 900/1000/1100/1366 看 |
| R-676a | `/api/version` 带 `release` 与提交哈希 `version` | #676 | PROD-readonly | `python3 scripts/regression/post-release-readonly.py`；单测 `internal/transport/httpapi/version_release_internal_test.go` | `release` 非空，`version` 与 `EXPECT_SHA` 前缀一致 | OK | — | | 版本检查是入库时加进该脚本的 |
| R-676b | 顶栏与登录页显示版本号 | #676 | dev-test-fake | `src/components/layout/app-topbar-release.test.tsx`；界面人工核对 | 单测通过；顶栏显示发布名（如 `v1.9.6-rc`） | MANUAL | f5bc5e10 2026-11-28 | E2 | v1.9.6 只记录了顶栏，登录页未记录 |
| R-697 | 输入区附图不再有「用途」三选一 | #697 | unit | `src/components/studio/composer-chips.test.tsx`、`src/components/studio/composer-layout.test.tsx` | 单测通过 | OK | — | | |
| R-692 | 局部重绘蒙版只有「擦除」；提交的蒙版二值化；结果图不被擦掉 | #692 | unit | `src/components/canvas/canvas-batch-b-wiring.test.ts`、`src/__tests__/inpaint-dialog-gating.test.tsx`、`src/store/mask-store.test.ts` | 单测通过 | OK | — | | 浏览器侧画蒙版验证未入库 |
| R-687 | 带蒙版的 fake 编辑规则（opaque/transparent 且提示词指向 masked area 才按蒙版编辑，否则给失真形态） | #687 | unit | `internal/testutil/fakeupstream/openai/images-v1/fake_test.go`；登记 `docs/fake-register.md` 的 `openai-images-v1/edits-mask-follow` | `go test` 通过 | OK | — | | 登记为 constructed，见第 4 节 |
| R-690 | 版本条三视图/四视图批次的「详情」可以翻页 | #690 | unit | `src/components/studio/version-strip.test.tsx`、`src/lib/workspace/result-target.test.ts` | 单测通过 | OK | — | | 浏览器侧翻页与「全部产出」标题一致性未入库 |
| R-670 | 通知区域与任务徽标的读屏名称 | #670 | unit | `src/__tests__/toaster-label.test.ts`、`src/__tests__/task-center-cancel.test.tsx` | 单测通过 | OK | — | | |
| R-678 | 没选 3D 模型时点「转 3D」弹设置选模型（四条入口路径） | #678 | unit | `src/components/studio/model3d-confirm-dialog.test.tsx`、`src/components/studio/model3d-confirm-678-wiring.test.ts`、`src/hooks/__tests__/use-object-actions.test.tsx` | 单测通过 | OK | — | | |
| R-677 | 舞台顶栏「发送到对话」有反应 | #677 | unit | `src/components/studio/stage-header-wiring.test.ts` | 单测通过 | OK | — | | |
| R-680 | 「全部产出」的图片句柄显示为 @imgN 并说明用途 | #680 | unit | `src/components/studio/version-strip.test.tsx` | 单测通过 | OK | — | | |
| R-702a | 批准方式：自动第 101 次出卡、批准后再计 100（第 201 次再问）；审批方式 3D 每步问、2D 自动；严格方式 Agent 的 2D 也问、用户自己点的 2D 不再问、用户的 3D 仍要批准 | #702 | dev-test-fake | `python3 scripts/regression/approval-modes.py` | 全部 PASS（window、agent、budget、manual、chain 五段） | OK | — | | 临时脚本在 v1.9.5 轮跑过，合并后未整段运行 |
| R-702b | 批准默认值与策略单测 | #702、#406 | unit | `internal/app/approval_default_406_test.go`、`internal/app/approval_policy_default_schema_test.go` | `go test` 通过 | OK | — | | fake 栈一侧见 F4 |
| R-693a | 三视图续跑：跨过窗口点不打断、续跑只计数；严格方式下用户点「继续生成」不再出卡 | #689、#693 | dev-test-fake | `python3 scripts/regression/three-view-continuation.py --only window,strict` | 全部 PASS | OK | — | | `strict` 段由旧片段重组，首次运行以实际输出为准；单测 `internal/adapters/sqlite/three_view_continuation_693_test.go` |
| R-691 | 三视图缩略图不裁切（`object-contain`） | #691 | unit | `src/components/studio/three-view-card-691.test.tsx` | 单测通过 | OK | f5bc5e10 2026-11-28 | E2 | 浏览器侧只做了样式验证，非方图不裁切没有单独断言 |
| R-693b | 「稍后自动生成」只在 auto 且回合在跑时出现、不与手动按钮同现；审批方式手动续跑不出卡 | #693 | dev-test-fake | `node scripts/regression/three-view-card.mjs`；单测 `src/components/studio/three-view-card-691.test.tsx` | 全部 PASS | OK | — | | |
| R-611 | 手选 Skill 的标记（3 种批准方式 × 三视图/四视图） | #611 | dev-test-fake | `python3 scripts/regression/three-view-continuation.py --only marks`；`node scripts/acceptance/release-1-9-0-browser.mjs`（#611 各项） | used_skills 含该 Skill 且 source=user；B4 的 #611 项全过 | OK | f5bc5e10 2026-11-28 | E2 | 日期来自 B4 的 #611 项；`marks` 段合并后未整段运行 |
| R-704a | 「生成纹理」是用户可编辑参数；关闭时纹理质量、纹理版本、导出贴图格式与尺寸隐藏，PBR 强制 false；其他 advised 参数仍 400 | #704 | unit | `internal/catalog/texture_toggle_704_test.go` | `go test` 通过 | OK | — | | |
| R-704b | 所有带开关的 3D Offering：Schema 层、保存会话参数路径、消息参数路径、冻结输入、报价无纹理组件、交付字节 | #704 | dev-test-fake | `python3 scripts/regression/texture-switch-fake.py` | 全部 PASS；Tripo 默认 GLB 3 张贴图、关纹理 0 张，Meshy 关纹理 0 张（断言，其余只记 INFO） | OK | f5bc5e10 2026-11-28 | E2 | E2 记的是入库前的临时脚本验证 Tripo H3 v3.1 与 Meshy 7.1；贴图数断言是入库后新增，尚未在已部署的 fake 上运行；全 Offering 运行见 R-704i |
| R-704c | Tripo texture=false 的报价组件（60，无纹理项）与默认报价（90） | #704 | unit | `internal/catalog/tripo_texture_off_quote_704_test.go` | `go test` 通过 | OK | — | | 来自 a5176f3a 的回灌，尚未发布 |
| R-704d | fake 的 Tripo texture=false 交付无贴图 GLB（0 texture、0 image、1 material）；默认开 PBR 交付 3 张贴图的 GLB（3 texture、3 image、1 material） | #704 | unit | `internal/testutil/fakeupstream/tripo/v3/untextured_704_test.go`、`internal/testutil/fakeupstream/tripo/v3/pbr_textures_704_test.go`、`internal/testutil/fakeupstream/modelfixture_test.go`、`internal/providers/tripo/texture_off_704_test.go` | `go test` 通过 | OK | — | | 登记 constructed，见第 4 节；尚未发布 |
| R-704h | fake 的 Meshy `should_texture=false` 交付无贴图 GLB（0 texture、0 image、1 material；图生与多视图），true 或缺省仍有贴图；Adapter 发 `should_texture=false` 且不带 `enable_pbr` | #704 | unit | `internal/testutil/fakeupstream/meshy/openapi-v1/untextured_704_test.go`、`internal/providers/meshy/texture_off_704_test.go` | `go test` 通过 | OK | — | | 登记 constructed（`meshy-openapi-v1/image-to-3d-texture-off-glb`），见第 4 节；尚未发布 |
| R-704i | 「生成纹理」开关的 fake 全 Offering 回归 | #704 | dev-test-fake | `python3 scripts/regression/texture-switch-fake.py` | 全部 Offering 的任务全部 PASS | OK | f5bc5e10 2026-11-28 | E6 | v1.9.6 当时（入库前的临时脚本）9 个 Offering、27 个任务全部通过，逐项结果见 E6；入库后的脚本尚未运行 |
| R-704e | 真实：Tripo 文生 3D texture=false 报价 60、GLB 无贴图；默认报价 90、GLB 3 张贴图 | #704 | PROD-paid | `python3 scripts/regression/texture-live-tripo.py --live` | 11 项 PASS | OK | f5bc5e10 2026-11-28 | E4、E5 | 付费，每次先取得同意；不自动回答选择卡；E5 记的是入库前的同内容临时版本 |
| R-704f | 真实：开关存成会话参数（Tripo 文生、Tripo 图生、Meshy 7.1 图生） | #704 | PROD-paid | `python3 scripts/regression/texture-live-session-param.py --live` | 三个场景冻结开关为 false、报价无纹理组件、GLB 无贴图 | OK | f5bc5e10 2026-11-28 | E5 | 三个场景全部通过（15/0）：Tripo 文生报价 60、Tripo 图生报价 70，均无纹理组件；Meshy 无 native_quote；三者 GLB 均为 0 texture、0 image、1 material。E5 记的是入库前的同内容临时版本，只在 2D 起始图那一步回答选择卡 |
| R-704g | 设置面板的「默认估算」随开关联动 | #704、#705、#722 | unit | `internal/transport/httpapi/agent_params_quote_722_test.go`、`src/components/studio/params-texture-estimate-722.test.tsx` | 见 R-722-1…R-722-3 | OK | — | | #722 已实现，覆盖与证据记在 R-722-1…R-722-3 |
| R-722-1 | `agent/params` 的 scoped 投影带 `native_quote`：按已保存参数求值（默认值垫底、`forced_when`），Tripo v3.1 full 文生/图生、v3.1 多视图、P2 文生/图生/多视图与 Meshy 7.1 多视图关纹理后没有 texture* 组件且低于默认，Tripo H3 v3.1 文生为 60 且与 Normalize 后的冻结报价相同；Agent 设的纹理质量在关纹理后不计价，generate_parts 强制关纹理；Meshy 单图与 2D 不带该字段 | #722 | unit | `internal/transport/httpapi/agent_params_quote_722_test.go` | `go test` 通过 | OK | — | | 修复前 9 项失败（2 个测试与 7 个子测试） |
| R-722-2 | 面板「默认估算」读 `native_quote`：关纹理显示 60、不再显示 90，纹理组件不列出；未保存前回到 `default_native_quote`；畸形报价丢弃；Meshy 单图不显示原生估算 | #722 | unit | `src/components/studio/params-texture-estimate-722.test.tsx` | `pnpm test` 通过 | OK | — | | 修复前 2 项失败 |
| R-722-3 | dev-test fake：每个带开关的 3D Offering 新会话面板估算等于默认估算，保存关纹理后没有 texture* 组件、低于默认，并与 P 路径冻结报价总额相同；无原生报价的 Offering 不返回 `native_quote` | #722 | dev-test-fake | `python3 scripts/regression/texture-switch-fake.py` | E 组检查全部 PASS | OK | — | | E 组为 #722 新增，尚未在已部署的 fake 上运行；面板渲染的浏览器断言待集成后在 dev-test 补做 |
| R-699a | 比例选择卡显示 Agent 写的整句问题（≤300 字符原样，不传显示默认问题） | #699 | unit | `internal/chat/choice_question_699_test.go`、`internal/transport/httpapi/chat_choices_test.go` | `go test` 通过 | OK | f5bc5e10 2026-11-28 | E2 | v1.9.6 在 dev-test 浏览器上看过 299/300 字符；超过 300 字符整张卡不出现（已知，#705） |
| R-699b | 画风迁移的纠正模板 | #699 | unit | `internal/chat/restyle_card_699_test.go` | `go test` 通过 | OK | — | | |
| R-699c | 透明底显示棋盘格（结果卡、版本条、全部产出、详情、看大图、舞台、附件缩略图） | #699 | unit | `src/components/studio/transparency-checker-wiring.test.ts` | 单测通过 | OK | f5bc5e10 2026-11-28 | E2 | 浏览器侧由入库前的临时脚本看过，未入库 |
| R-699d | 真实对话模型加真实 2D 的画风迁移（风格图无全身时先出比例卡，答完只提交一次生成） | #699 | PROD-paid | — | 先出选择卡、答完只提交一次生成；风格图有全身时不出卡 | GAP | — | | 只有临时验证与结论（记在 #699），没有入库脚本；要补一个 live-only 脚本 |
| R-688a | 图只在画板上：非画板轮 Agent 先查看并指向画板路径；画板轮发起三视图并停在批准卡 | #688 | dev-test-live-chat | `python3 scripts/smoke/smoke-live-chat-three-view-route.py --live` | 第一轮四条断言通过；第二轮正面停在批准卡并被拒绝 | OK | f5bc5e10 2026-11-28 | E2 | 第二轮在严格方式下等不到最终回复而超时（脚本缺陷：三视图的卡不在待批准列表里），待修 |
| R-688b | 画板识别的五个说法（含画板轮对照） | #688 | dev-test-live-chat | `python3 scripts/regression/board-agent.py --live` | 每个场景「注意到画板」PASS | OK | de701fb8 2026-11-28 | E7 | 真实模型回答会波动，结论以多次运行为准；#709 起该脚本的判定改为 R-709-4；S1–S5 各 3 次全部通过 |
| R-709-1 | `view_canvas` 只读画板图层：空画板、图层清单（`added_after_last_turn`、`in_frame`、`transformed`、`in_conversation`，`preview` 为 null 并带 `preview_reason`）、多图层或有标注时给「以画板为主继续」、截断统计、画板轮/有素材/同一回复重复调用由服务端拒绝 | #709 | unit | `go test ./internal/chat/agenttools/ -run 709` | 单测通过 | OK | — | | |
| R-709-2 | 画板快照随非画板轮冻结：每张图片图层过 `VerifyAttachment`、外链与 data 地址不给句柄、按所有者取数、条数与文字上限；画板一行只数对话里没有的图层（对话里的同一张图不算，标注都算，0 时不出现）；上一轮之后新放的图层标 `added_after_last_turn` | #709 | unit | `go test ./internal/chat/ -run 709` | 单测通过 | OK | — | | |
| R-709-3 | `@canvasN` 作普通参考图（生成工具、三视图、`inspect_image`、`create_selection_mask`、`crop_image_regions`、`propose_asset_list`），批准卡参考说明标明来自画板 | #709 | unit | `go test ./internal/chat/generationtool/ ./internal/chat/agenttools/ ./internal/chat/imagemask/ -run 709` | 单测通过 | OK | — | | 版本条血缘未标画板（第一步未做） |
| R-709-5 | sqlite 端到端：经画布服务保存图层 → 画板 Port 与真实附件校验冻结快照（他人资产不可读）→ `view_canvas` 读出句柄 → `@canvasN` 解析到平台地址；下一轮只有新放的图层标 `added_after_last_turn`，画框外的图层 `in_frame` 为 false | #709 | unit | `go test ./internal/app/ -run 709` | 单测通过 | OK | — | | |
| R-709-4 | 兜底行为：图只在画板上时 S1 先看画板再出选择卡，S2–S4 直接用画板的图发起生成（严格方式停在批准卡），S5 画板轮对照不变 | #709、#688 | dev-test-live-chat | `python3 scripts/regression/board-agent.py --live` | 每个场景 PASS（S1–S8，每个场景 3 次） | OK | de701fb8 2026-11-28 | E7 | 真实模型回答会波动，结论以多次运行为准；脚本改走画布保存接口，并增加 S6（对话里已有图、画板新放一张）、S7（多图层加标注，选择卡带「以画板为主继续」）、S8（本轮带附图不看画板）；24 轮全部通过，S1 里模型会重复调用 `ask_choice`（3–4 次），未收紧 |
| R-709-7 | 选择卡动作「以画板为主继续」（`continue_on_canvas`）：点选后把上一条原话作为画板轮重发一次（`input_source` 为画板来源、带快照、关卡）；没有画框时卡片说明原因不发送；找不到原话时打开「以画板为主」、卡片保留 | #709 | dev-test-fake | `src/components/workspace/harness-choice-action.test.ts`；界面人工核对 | 单测通过；点选后对话出现上一条原话且为画板轮、卡片已答、只发一次 | MANUAL | — | | Agent 何时给出该选项依赖对话模型；单测另见 `src/lib/chat/choice-actions.test.ts`、`internal/catalog/continue_on_canvas_709_test.go` |
| R-709-6 | 非画板轮发送前等画板保存（拖入文件上传、画笔位图、防抖节点保存），上限 `canvas.frontend.send_save_wait_milliseconds`，超时或失败照常发送，不带画板内容；画板轮与没有画板时不等待 | #709 | unit | `src/lib/canvas/pending-edits.test.ts` | 单测通过 | OK | — | | 接线见 `src/components/studio/chat-column-wiring.test.ts` |
| R-705-1 | 批准卡「预计消耗」显示「/」、余额读不到；抠图批准卡类型显示「3D」 | #705 | dev-test-fake | — | 卡上的预计消耗与余额与结果卡一致；抠图显示为 2D | GAP | — | | 未修；要补 fake 复现与界面断言 |
| R-705-2 | `ResizeObserver` 良性事件不再当 ERROR 上报（真实 Error 仍上报） | #705 | unit | `src/lib/client-error-report.test.ts` | 单测通过 | OK | f5bc5e10 2026-11-28 | E2 | |
| R-705-3 | 图生图后版本条多出「上传」节点、血缘不直接指向来源版本 | #705 | dev-test-fake | — | 不多出上传节点，血缘指向来源版本 | GAP | — | | 未修；要补 fake 复现 |
| R-705-4 | 附 2 张图时弹「多个视角」询问条 | #705 | dev-test-fake | — | 待产品决定是否降级为不打断 | GAP | — | | 产品决定未出 |
| R-705-5 | 1000px 顶栏项目名不被压住；全部产出按 Esc 先清勾选再关面板 | #705 | unit | `src/components/studio/project-switcher.test.tsx`、`src/lib/workspace/output-groups.test.ts`、`src/components/studio/stage-wiring.test.ts` | 单测通过 | OK | f5bc5e10 2026-11-28 | E2 | 1000px 时项目名收缩到 0 宽（已知小问题） |
| R-705-6 | 蒙版里不按 Shift 画新选框时保留之前的画笔笔画 | #705 | unit | `src/store/mask-store.test.ts` | 单测通过 | OK | f5bc5e10 2026-11-28 | E2 | |
| R-705-7 | 画板上选中图片后合并/导出/局部重绘不置灰 | #705、#681 | unit | `src/lib/canvas/inpaint-entry.test.ts`、`src/components/studio/stage-header-wiring.test.ts` | 单测通过 | OK | f5bc5e10 2026-11-28 | E2 | |
| R-705-8 | 用户点「停止」取消不再弹红色失败 toast | #705 | unit | `src/lib/workspace/completion-notice.test.ts` | 单测通过 | OK | f5bc5e10 2026-11-28 | E2 | 被取消的任务在对话里仍是红色错误卡（已知） |
| R-705-9 | 文档措辞（批准窗口、切到预算方式、右侧栏收起） | #705 | unit | `docs/product.md`、`scripts/smoke/README.md` | 文档与实际行为一致 | MANUAL | — | | 文档项，人工核对 |
| R-705-10 | 升级演练：迁移在存量库副本上执行、读回检查 | #435、#705 | PROD-readonly | `python3 scripts/upgrade-drill.py --image <候选镜像> --database <备份副本>`；单测 `scripts/tests/test_upgrade_drill.py` | v1.9.6：迁移 116→117，24 项检查 23 项通过 | MANUAL | f5bc5e10 2026-11-28 | E2 | 已知三处缺陷待修（#705）：`integrity_check` 需改用应用引擎做；root 运行时没有 chown 副本库；空 `web` 目录只读挂载盖住镜像前端。唯一失败项是源库自带的索引问题 |

### 正式测试环境（PROD）发布后回归

| ID | 功能 / 行为 | 来源 Issue | 验证层 | 命令或脚本 | 期望 | 状态 | 最后验证 | 证据 | 备注 |
|---|---|---|---|---|---|---|---|---|---|
| P1 | 版本与健康、L1 子集（Schema、CSP、登录、跨站拒绝、会话与批准接口） | #2 | PROD-readonly | `python3 scripts/regression/post-release-readonly.py` | 全部 PASS | OK | f5bc5e10 2026-11-28 | E4 | v1.9.6 跑的是入库前的 11 项版本（全过，结果在运行日志里）；版本检查是入库时新增的 |
| P2 | L0 浏览器启动检查 | #2 | PROD-readonly | `scripts/smoke/smoke-l0-browser-boot.sh` | `PASS=16 FAIL=0 SKIP=0` | OK | f5bc5e10 2026-11-28 | E4 | 经 `release-env.py smoke formal-test` 运行；结果在该轮的运行日志里，评论未单列 |
| P3 | L1、L1 Agent 全量 | #2 | PROD-paid | `scripts/smoke/smoke-l1-free.sh`、`scripts/smoke/smoke-l1-agent-mode.sh` | 同 S2、S3 | MANUAL | — | | 真实形态会调用真实对话模型，每次先取得同意；平时以 P1 代替 |
| P4 | 迁移后的旧会话能打开、旧卡片正常显示 | #2 | PROD-readonly | 人工：打开发布前的会话 | 旧会话、旧选择卡、旧 2D 结果正常显示 | MANUAL | — | | |
| P5 | 界面抽查（顶栏版本、设置面板生成参数、本次新功能） | #2 | PROD-readonly | 人工抽查 | 与 dev-test 验收一致 | MANUAL | — | | |
| P6 | 服务端日志只读检查 | #233 | PROD-readonly | 人工：只读查看该栈的日志目录（`dev_ops.md` 日志一节） | 没有新的意外 ERROR；用户报错能按请求 ID 查到原因 | MANUAL | — | | 只读，不改动环境 |

## 4. Fake 与真实的已知差异

真源是 [`docs/fake-register.md`](fake-register.md)（由 `internal/testutil/fakeupstream/register/register.json` 生成；当前 193 条：real 84、constructed 104、gap 5）。登记表里长期存在的 5 条 gap 与本次发布无关，不进下表：`tripo-v3/poll-progress-not-modelled`、`tripo-v3/p-series-face-limit`、`openai-images-v1/generated-picture-pixel-entropy`、`gemini-images-v1/three-view-consistency-late-reject`、`gemini-images-v1/image-to-image-4k-wire`（计划与 Issue 见登记表）。

发布门禁按下表判定（`docs/release-gate.md` ②）：状态只能是 `OPEN`、`FIXED`、`ACCEPTED`；`OPEN` 行挡发布；`ACCEPTED` 的备注必须写用户在聊天里明确接受的日期。

| 差异 | 状态 | Issue | 记录日期 | 备注 |
|---|---|---|---|---|
| `tripo-v3/generation-texture-off-glb`（constructed）：Tripo texture=false（且未开 pbr）时 fake 交付带贴图 GLB，真实交付 0 texture、0 image、1 material | FIXED | #704 | 2026-11-28 | 回灌提交 a5176f3a，登记表条目 `tripo-v3/generation-texture-off-glb`（constructed）；按 v1.9.6 正式测试环境的 Plinth 侧观测，图生与多视图按文档构造 |
| Tripo 默认 GLB 贴图数：fake 1 张，真实 3 张 | FIXED | #704 | 2026-11-28 | fake 夹具回灌为 3 张贴图，登记为 constructed：开 PBR（默认 texture=true、pbr=true）的生成及其普通模型转换交付 `glb:pbr`（textures 3 / images 3 / materials 1），登记表条目 `tripo-v3/generation-texture-off-glb`；依据只有文生 3D 的正式测试环境 Plinth 侧观测，图生、多视图与三张贴图的槽位为构造。`texture-switch-fake.py` 仍只记录贴图数、不断言 |
| Meshy 7.1 `should_texture=false`：fake 仍交付带贴图 GLB，真实正式测试环境实测交付 0 贴图 GLB（textures 0 / images 0 / materials 1；v1.9.6，图生 3D，[#2 评论](https://github.com/LeXwDeX/Plinth/issues/2#issuecomment-7233999783)） | FIXED | #704 | 2026-11-28 | 已回灌：Meshy fake 对 image-to-3d、multi-image-to-3d 的 `should_texture=false` 交付无贴图 GLB（`core/untextured.go`），登记表条目 `meshy-openapi-v1/image-to-3d-texture-off-glb`（constructed，只有 Plinth 侧观测）；多视图按单图构造 |
| `openai-images-v1/edits-mask-follow`（constructed）：带蒙版编辑的成败条件与失真形态 | FIXED | #687 | 2026-11-28 | 条件与失真形态来自真实探针，fake 已按它实现；样本未入库，失真像素是近似 |

本表只记**本发布周期内发现**的真实与 fake 的差异：每次发布后清空已 `FIXED` 的旧行，或移到下面的「历史」小节；本周期没有发现差异时，表里写一行 `无新增差异（核对于 <候选 revision 前 8 位> <日期>）`。

### 历史

（暂无）

## 5. 维护规则

- **改行为的 PR**：同一改动里新增或更新对应的行（ID、期望与命令写准确，命令里的路径必须存在）；没有脚本的写 `GAP`、写备注并开 Issue。`scripts/tests/test_regression_matrix.py` 在门禁里检查路径与状态。
- **发布的人**：发布前读第 2 节与相关行；跑完一行就把「最后验证」改成被测 revision 前 8 位加日期，证据链接指向写了结果的 Issue 评论。没跑的行保持原值，失败的行不改日期，在 Issue 里记失败与处置。
- **真实调用回灌 fake 之后**：更新第 4 节，并把对应行的命令换成 fake 能复现的版本（AGENTS.md「真实结果回灌 fake」）。
- **脚本过期**：改成 `OBSOLETE` 并写原因，不删行，直到替代脚本入表。
- 本表只放状态指针，不复制证据正文；不写主机、地址、端口、目录、任务或资产 ID、账号口令。
