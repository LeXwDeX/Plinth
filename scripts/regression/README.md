# 回归脚本（scripts/regression）

[`docs/regression-matrix.md`](../../docs/regression-matrix.md) 主表里「命令或脚本」列指向的可复用回归脚本。来源是
v1.9.5、v1.9.6 两轮发布回归里调通的临时脚本，入库时合并整理过：地址与账号全部从环境变量取，没有默认地址，
截图与结果写系统临时目录（或 `OUT`），不写进仓库。

## 环境变量

一律经 `scripts/release-env.py smoke <环境>` 注入（凭据只在子进程环境里，不落盘）：

| 变量 | 用途 |
|---|---|
| `BASE` | 目标环境的 origin（http/https，不带路径） |
| `AGENT_TEST_USERNAME`、`AGENT_TEST_PASSWORD` | 该环境的 Agent 专用账号（AGENTS.md「Agent 专用账号」） |
| `ART_AI_UPSTREAM_SHAPE` | 目标的上游形态 `fake` / `mixed` / `live`；fake 专用脚本只在 `fake` 时运行 |
| `ART_AI_LIVE_ACK` | 真实上游脚本的第二道开关，须为 `1` 且同时给出 `--live` |
| `EXPECT_SHA` | 可选；`post-release-readonly.py` 用它核对 `/api/version` |
| `OUT`、`PLAYWRIGHT_MODULE_PATH` | 浏览器脚本的输出目录与 Playwright 位置（解析顺序同 `scripts/acceptance/browser_matrix/lib.mjs`） |

`/api/version` 不带上游形态，所以 fake 专用脚本按 `ART_AI_UPSTREAM_SHAPE` 判定（与 `scripts/acceptance/release-1-9-*.mjs`
相同）：不是 `fake` 或给了 `--live` 时退出码 2，不会发起任何生成。真实/付费脚本走 `scripts/smoke/l5_common.py` 的闸门：
缺 `--live` 或 `ART_AI_LIVE_ACK=1` 时退出码 2，形态不符（`live-only` 要求 `live`，`chat-live` 要求 `mixed` 或 `live`）也拒绝；
它们把 `AGENT_TEST_*` 交给 l5_common 当登录账号。每次真实运行前须在当前对话取得用户同意（AGENTS.md「上游接口与 Fake」）；
例外是 `dev-test` 上的 2D 真实调用（真实对话模型 token、真实 2D 生图），按该节不需逐次同意，开跑前后在回复里写明目的与大致用量，
两个开关照旧要给；3D、`formal-test`、正式生产与带 `--live-upstream` 的发布（含 3D 通道）仍须当次同意。

## 脚本

| 脚本 | 层 | 花费 | 覆盖 |
|---|---|---|---|
| `click-matrix.mjs` | dev-test-fake，浏览器 | 免费 | #681 点击矩阵：单击不改画板、双击只插入这一张、选用不进画板、删掉不复活、Agent 新结果自动加入一次、舞台双击看大图 |
| `approval-modes.py` | dev-test-fake，API | 免费 | #702 批准方式：自动的 100/101/201 窗口、审批 3D 每步问、严格下 Agent 的 2D 也问而用户自己点的 2D 不再问、三种方式的 Meshy 处理链 |
| `three-view-continuation.py` | dev-test-fake，API | 免费 | #689/#693 续跑跨窗口点不打断、只计数；#611 手选 Skill 标记（3 种方式 × 三视图/四视图）；严格下用户点「继续生成」不再出卡 |
| `three-view-card.mjs` | dev-test-fake，浏览器 | 免费 | #693 卡片：「稍后自动生成」只在 auto 且回合在跑时出现、不与手动按钮同现；审批方式下手动续跑 |
| `texture-switch-fake.py` | dev-test-fake，API | 免费 | #704 所有带「生成纹理」开关的 3D Offering：Schema 层、消息参数与会话参数两条路径、冻结输入、报价、交付 |
| `texture-live-tripo.py` | PROD-paid | 付费：两次 Tripo 文生 3D（v1.9.6 实测 60 与 90 Tripo 积分） | #704 真实：关纹理报价 60、GLB 无贴图；默认 90、GLB 有贴图 |
| `texture-live-session-param.py` | PROD-paid | 付费：Tripo 文生、Tripo 图生、Meshy 7.1 图生各一次与两张 2D 源图 | #704 真实：开关存成会话参数时三种 Offering 的冻结输入、报价与 GLB |
| `board-agent.py` | dev-test-live-chat | 只耗对话 token（5 轮），生成卡全部拒绝 | #688 图只在画板上时 Agent 是否注意到画板 |
| `post-release-readonly.py` | PROD-readonly | 免费，不调上游 | 发布后只读冒烟：版本、健康、Schema、CSP、登录、跨站拒绝、会话与批准接口 |

`lib.py`、`lib.mjs` 是共用的登录、会话、批准、任务、三视图与 PNG/GLB 小工具；fake 闸门函数 `fake_refusal()` /
`fakeRefusal()` 不连网，可离线自检。

## 运行

```bash
# fake 专用（dev-test 的 fake 形态发布之后）
python3 scripts/release-env.py smoke dev-test -- node scripts/regression/click-matrix.mjs
python3 scripts/release-env.py smoke dev-test -- python3 scripts/regression/approval-modes.py --only window,agent
python3 scripts/release-env.py smoke dev-test -- python3 scripts/regression/texture-switch-fake.py --offering tripo/tripo-v3.1-full/text-to-3d

# 发布后只读（任何环境）
python3 scripts/release-env.py smoke formal-test -- python3 scripts/regression/post-release-readonly.py

# 真实上游：先在对话里取得当次同意
ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke formal-test -- python3 scripts/regression/texture-live-tripo.py --live
```

每个脚本最后一行是 `=== <名字>: PASS=n FAIL=n ===`（l5_common 系另带 `SKIP=n`），有 FAIL 时退出码 1，环境或闸门不满足时退出码 2。

## 已知限制

- 入库后只做过 `node --check`、编译检查与离线拒绝路径自检，合并后的脚本还没有整段对环境跑过；各段的断言来自已跑通的临时脚本，
  其中 `three-view-continuation.py` 的 `strict` 段与 `three-view-card.mjs` 的 AUTO 采样是按旧片段重组的，首次运行结果以实际输出为准。
- fake 与真实的差异（GLB 贴图数等）只记录、不断言，见 `docs/fake-register.md` 与 `docs/regression-matrix.md`「Fake 与真实的已知差异」。
- 脚本依赖界面 `data-testid` 与 Schema 文案；界面改版后先单段运行确认选择器仍有效。
