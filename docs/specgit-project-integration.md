# SpecGit 项目集成

本项目使用 SpecGit 管理 GitLab 原生 Milestone、Issue 和 MR。安装版 `specgit --help`、`specgit --schema` 是命令契约；项目已登记的受管资产版本与当前可执行文件版本分别核对，不从一个推断另一个。集成范围仅为项目；共用 CLI，但不要求或注册全局指导、全局 Skill 或宿主 hooks。`.specgit.yaml` 是本机配置，不提交。

## 项目真源与文件边界

- Milestone 是可选的目标/计划容器；按需复用或创建。
- Issue 是独立目标的范围、方案、验收、进度、阻塞和证据的唯一真源。开始跟踪前检索重复项，Issue 至少写清 Why、Scope、Approach、Acceptance。
- MR 用于集成相关 Issue 的改动，并关联实现、验证、审查和 CI 记录；它不是版本或发布。发布仍按单独授权和运维规约进行。
- 仓库内保留产品架构设计、接口/操作规范及静态历史证据。开发计划、工作包、当前状态和验收进度不在本地 Markdown 台账维护；需要跟踪时写入对应原生对象。历史文档只证明其记录时点，不是当前状态来源。

项目入口：Milestones、Issues、Merge requests。

## 当前配置

`docs/specgit-project.yaml` 是手工维护的项目偏好；它保留项目选择的语言（`zh`，2026-10-21 用户要求）、模板和验证策略。GitLab 是原生工作跟踪与代码评审平台。当前声明关闭 native auto-merge 和 Agent 自动关闭 Issue；这些开关不改变用户已经授权的普通交付流程。

宿主集成按 `AGENTS.md` 的项目约定分别安装：Claude Code 使用 `setup --scope project --agent claude --hook-purpose guard --hook-purpose context`，Codex 使用 `setup --scope project --agent codex --hook-purpose guard --hook-purpose context`，支持 Claude hooks 的定制 OpenCode 使用 `setup --scope project --agent opencode --opencode-claude-hooks --hook-purpose guard --hook-purpose context`；`guard` 安装 Git guard，`context` 注册 SessionStart/PreToolUse。先检查各自 dry-run，不相互导入配置。`--hook-purpose none` 只是不新增宿主 hook，并保留已有 hook；它不能证明当前工作树已经注册或执行 guard。Git guard 与七门禁入口另行核验；门禁更新按 `dev_test.md` §2 预览、验证和激活，保留 SpecGit 受管块。

`specgit doctor` 明确使用 `--provider gitlab --remote origin`，因为本仓库还有 GitHub remote。当前离线检查成功协议为 `status=offline_checked`、`evidence.health.status=healthy` 且 `evidence.health.host_behavior=not_verified`；这表示本地静态资产检查健康。离线检查不访问远端，也不验证宿主实际加载配置或真实回调。证据只适用于实际检查的工作树、CLI 版本和当时资产；其他工作树或旧版本的状态不能移用。`status` 能读取声明和 checkpoint，也不等于 `doctor` 的资产检查通过。init dry-run 仅列出 planned paths，不能据此判断各路径内容变化或覆盖风险。初始化与升级前后仍须读回目标文件、声明、checkpoint 和 hook。

项目 Skill 位于 `.agents/skills/specgit-native/SKILL.md`。它由项目 CLI 在每个工作树本地生成为 managed asset，并由 setup 写入 Git 本地 exclude，不纳入 Git；新工作树执行 `specgit init --cwd <工作树> --config-file <工作树>/docs/specgit-project.yaml --manual-observe` 生成声明、Skill 与 `AGENTS.md` 入口（见下节），宿主 hooks 另按上段 `setup` 安装。升级时按当前 CLI 流程预览并刷新，不能手改受管 Skill，也不能复制到宿主级目录。若安装报告 ownership conflict，保留现场并按 CLI diagnostics 的恢复步骤处理；未知所有权不得猜测接管。受管资产的恢复由 CLI 事务执行，不手工备份、恢复旧块或覆盖其他内容。

## 所有分支与工作树强制初始化

2026-10-19 用户确定 SpecGit 为本项目交付规约，所有分支与工作树都须遵守（#242）。`.specgit.yaml`、Skill 与宿主配置都是各工作树本地文件，不随 Git 出现在其他工作树或 clone 中。`.git/hooks` 由同一 clone 的所有工作树共享，SpecGit 2.14.0 的 Git guard 却只在有 `.specgit.yaml` 的工作树执行 checkpoint：没有声明、也没有 SpecGit 私有状态的工作树暂存改动后 guard 退出 0，即静默放行。新建工作树默认就是这种状态。

**新工作树的初始化**：在该工作树运行

```
specgit init --cwd <工作树> --config-file <工作树>/docs/specgit-project.yaml --manual-observe
```

init 只写本工作树的 `.specgit.yaml`、Skill、私有状态和 `AGENTS.md` 入口，不安装 hooks，`info/exclude` 区块已是当前内容时不变。init 只接管与当前 CLI 生成内容逐字相同的 `AGENTS.md` 受管区块；分支上的区块较旧时报 `ownership_conflict`，先把分支变基到 `origin/main` 再运行。因此升级 SpecGit CLI 后若受管区块变化，先在独立工作树刷新该区块并通过 MR 合入 main，再初始化其他工作树。init 需要能访问 GitLab。

**项目自有 hook 区块**：`scripts/install-specgit-required-hooks.py` 在共享 hooks 中维护 `artai-specgit-required:v1` 区块，不改动 SpecGit guard 与七门禁区块：

- pre-commit、pre-push：位于 SpecGit guard 区块之前，检查当前工作树根目录是否有 `.specgit.yaml`。`warn` 模式打印原因和一行修复命令（上面的 `specgit init`，路径已展开）后放行；`enforce` 模式打印同样的内容后拒绝。
- post-checkout：`git worktree add` 从空版本检出时，调用受仓库管理的 `scripts/specgit-new-worktree-setup.py`。helper 在缺少 `.specgit.yaml` 时运行 `specgit init`（init 需要能访问 GitLab；helper 不读取项目 `.env`，也不从中注入凭据，使用 SpecGit/glab 本身已登录的认证会话。调用者显式设置的进程环境按 CLI 规则原样传递，具体身份另按原生 CLI 读回核实）；已有声明时跳过 init 并重跑后续接线和检查。因此 `initialized` 只表示存在项目声明，不表示宿主 setup 或 doctor 已就绪。helper 预览并执行 `specgit setup --hook-purpose guard --hook-purpose context`：`generic` 必须安装 `guard`，其不支持的 `context` 会从 CLI 回执中明确显示为 declined；`claude`、`codex` 和支持 Claude hooks 的定制 `opencode` 必须安装 `guard` 与 `context`，OpenCode 另加 `--opencode-claude-hooks`。缺少该宿主必需 purpose 时失败，并保留 CLI 诊断。最后使用 `specgit doctor --provider gitlab --remote origin --offline --json` 验证离线静态资产；输出明确标为 `offline static healthy` 和 `host_behavior=not_verified`，不声称远端健康、宿主加载或真实回调通过。所有受管文件和宿主 hook 都只由 SpecGit CLI 写入。普通分支切换不触发；失败输出诊断及 helper 重试命令，不让 `git worktree add` 失败。调用前去掉 Git 传给 hook 的 `GIT_DIR` 等变量，避免 CLI 误认工作树。

安装器默认只输出精确 diff，`--apply` 才写入；只使用 Git 默认的 clone `common-dir/hooks`，拒绝任何来源（system、global、local 或 worktree）的显式 `core.hooksPath`，避免相对路径在 linked worktree 中解析到别处，也避免绝对路径把其他仓库的提交或 checkout 接入本项目 hooks。安装前要求 pre-commit/pre-push 是可执行 shell hook，且含唯一完整的 SpecGit guard 区块；缺失、不可执行或格式不支持时不写任何文件。已有 post-checkout 也必须是可执行普通文件，否则整个计划拒绝且不写文件。支持的 shebang 是明确的 POSIX `sh`、`bash`、`dash` 及 `env` 对应形式。文件原子替换并保留模式，重复执行为 unchanged，区块被改动、重复或残缺时拒绝改写。`--mode warn` 与 `--mode enforce` 可互相切换且只改 pre-commit/pre-push；`--uninstall` 只移除本区块，保留没有 ownership receipt 可证明由安装器创建的 shebang-only post-checkout。`--status` 只读列出各 hook 的模式和每个工作树的状态：`initialized`（仅有项目声明，setup/doctor 未核实）、`ready`（可直接 init/setup）、`stale-block`/`no-block`（需先变基到 `origin/main`）、`missing`（目录已不存在）。`--init-worktrees` 预览所有 `ready` 与 `initialized` 工作树，`--apply` 对每个执行“缺声明时 init、重跑四类宿主 setup、doctor”，即使之前 init 已成功但 setup/doctor 失败也可安全重试；CLI 负责受管文件 ownership。其他状态只列原因。hooks 不随 Git 分发，每个 clone 各自安装；同一 clone 的所有工作树共用。

**上线顺序**（合并本节对应 MR 不会激活任何 hook；以下在没有 Agent 正在提交/推送的时段，从检出已合并 `origin/main` 的工作树执行）：

1. `python3 scripts/install-specgit-required-hooks.py --status`：只读确认工作树数量和各状态。
2. `--mode warn` 预览，再 `--mode warn --apply`：此后新工作树自动 init 并接线四类宿主；未初始化工作树的 commit/push 照常通过，只多打印修复命令。
3. `--init-worktrees` 预览，再 `--init-worktrees --apply`：对全部 `ready` 与 `initialized` 工作树初始化（如缺失）、接线并运行各自 doctor；`initialized` 只代表声明文件存在，重试会补齐或复核宿主接线。
4. 再次 `--status`；剩余 `stale-block`/`no-block` 工作树须变基到 `origin/main` 后 init，或在完成后移除。确认可以接受剩余工作树被拒绝后，`--mode enforce` 预览，再 `--mode enforce --apply`。
5. 回退：`--mode warn --apply` 恢复放行，`--uninstall --dry-run` 预览、`--uninstall --apply` 完全移除。

**对已有工作树的影响**：初始化后的工作树由 SpecGit guard 接管，下次 commit/push 前须先用 `specgit issue <id>` 选定 Issue checkpoint，否则 guard 拒绝并提示选择命令；这是规约本身的要求。`warn` 模式下未初始化工作树不受阻；`enforce` 后它们的 commit/push 被拒绝，直到 init 成功。`git rebase`、快进合并不经过 pre-commit，旧分支可以先变基再 init。

**工作树数量上限与宿主 hooks**：SpecGit 2.14.0 的 `doctor` 与带 `--hook-purpose` 的 `setup` 会发现全部工作树，超过 64 个时报 `Worktree discovery exceeds 64 worktrees.` 并中止；`init`、`issue`、`status` 与 Git guard 不受影响。自动 helper 依次初始化并为四个声明的 Agent 运行 `setup`，再运行离线 doctor；任一步失败都会留下明确诊断，但不会撤销成功的 `init` 或失败 `setup` 前已完成的宿主配置，也不会让 worktree checkout 失败。执行 `setup` 或 `doctor` 前，先清理已完成的工作树使数量不超过 64。doctor 通过证明当前 CLI 对本地声明、受管资产和所选宿主 hook 的静态检查通过；它不证明宿主已加载配置或真实回调行为，真实运行仍需在对应宿主验证。`CLAUDE.md`、`.claude/settings.json`、`.codex/hooks.json` 和 `.opencode/hooks.json` 是 CLI 管理的产物，逐项回读确认后再报告接线完成。

## 初始化与升级

先在目标项目工作树只读核验 Git remote、当前分支、SpecGit 版本、项目偏好、`.specgit.yaml`、checkpoint、Skill ownership 和有效 hook 路径。确认 CLI 版本和项目目标后，按当前安装版 `specgit --help` 与 `specgit --schema` 为准。先做 doctor、init/setup dry-run；确认计划仅涉及已授权的项目资产后再执行。不要把 dry-run、声明或 guard 通知当作写入授权。

每个工作树独立核对初始化与 setup 状态。后续升级时重复上述只读检查与预览，检查生成 guidance 和 managed Skill 的 ownership，再按授权刷新；保留人工区块、现存 hook 字节、其他 worktree 的 checkpoint，以及 `.specgit.yaml` 的本地性质。不得手改 SpecGit 受管区块、Skill、旧生成块或 hooks 来绕过 CLI。使用 `init --config-file` 时传入已核实的声明绝对路径。

### 新工作树的受管资产所有权

Skill 曾被 Git 跟踪；自 #209 起不再跟踪。检出 #209 之前的提交、或从归档恢复的工作树，可能带来该文件，却不会带来原工作树 Git 私有目录中的 setup ownership receipt。不能只凭文件存在或内容相同判定可由 CLI 覆盖。

SpecGit 2.10.0 至 2.13.0 的 `setup` 和 `doctor` 会把无匹配 receipt 的既有项目 Skill 报为 `ownership_conflict`。这是保护既有文件的设计；这些版本没有认领命令，`doctor --fix` 和重新 init 均不能补造 setup 的所有权证明。先核查原工作树的真实记录及 CLI 支持的事务恢复条件。没有匹配证据时保留文件并记录资产刷新未完成；不得复制其他工作树的 receipt/checkpoint、手写记录、删除文件后伪装首次安装，或把此冲突解释为 GitLab 权限不足。

资产刷新受阻与原生交付能力分别验证。已有可读取的项目 Skill、正常选择的 Issue、实际执行的 Git guard，以及通过原生 GitLab 读回的 CI/审阅/合并证据，仍须逐项检查；它们不把未通过的资产检查变成 ready。若确需新增认领能力，应在 SpecGit 上游独立设计、验证和交付，不能在本项目改写受管文件来实现。

## 验证与交付

任务开始时检索同一目标的原生 Issue，检查状态、范围和验收；复用准确匹配的 Issue。新目标才创建完整 Issue，并按依赖拆分独立、可验收的工作。只读审查、审计和本地集成维护不要求另建 Issue。文档工作依项目适用的文档规则核对事实、引用和 diff；运行行为变更继续遵守 `dev_test.md` §2 及部署验收要求。

写入 GitLab 前使用当前 CLI 的 dry-run 并核对目标、身份、Issue/MR 和变更。MR 关联相关 Issue；保持 draft 直到审阅准备完成。用原生状态读回精确 MR head、CI、review、merge 以及应关闭 Issue 的结果。CLI 命令成功、离线 status、hook 通知或历史报告都不能单独证明远端交付完成。Milestone、Issue、MR、merge 与 Release 是不同状态；按本轮授权收尾并报告未验证或受阻项。

### 同一目标复用准备结果

开始时用 `specgit issue <id> --inspect --json` 查重并核对完整规范，随后预览并选择准确的既有 Issue。有效 checkpoint 留在当前工作树的 Git 私有目录，由 SpecGit 管理；同一目标后续实现、提交和推送沿用它，不为每次命令重新选择或创建 Issue。`specgit status --json` 检查本地身份和选择，Git guard 继续执行原有检查。

目标、分支、remote/项目/target 或 Issue 规范变化时，重新检查原生正文和当前身份，再用支持的 `issue` 命令刷新；不能手改、复制或移植 checkpoint。其他 worktree 独立选择。评论补充验证事实不等于修改范围；实际规范变化不能用旧选择证明。发生 guard 拒绝时按诊断处理，不使用跳过 hooks 的选项。

准备 Issue 正文、MR 最终摘要和关联引用时，把同一次事实变化的相关修改放在一起审阅。先原生读回，生成具体差异，执行适用 dry-run，再在现有授权内分别写入。写前再次确认正文/更新时间与预览一致；冲突就重新合并，不能覆盖并发编辑。超时或结果不明先读回，禁止盲目重发。只在规范、实现、验证或阻塞变化时更新。

Issue 保存验收逐项结果、精确 source/deployment SHA、环境、关键证据链接、未覆盖范围与阻塞。MR 说明触发问题、最终行为、相关本地检查、当前 head CI（1.0 前暂停期间为合并前本机 `verify-local.py --race` 全量，见 dev_test.md §2）和评审结论，并链接 Issue 的规范证据；不复制完整验收台账或为每一步新增 Issue。临时命令输出与工作树私有门禁凭据是运行证据，不成为第二套计划或状态真源。

### 每个 MR head 一个有界观察者

指定一个 Agent 负责一个明确 MR、完整 head SHA 和稳定 session；其他 Agent 向该负责人汇总，不同时轮询同一流水线。使用安装版支持的 `specgit watch --request <iid> --session <stable-id> --goal checks --timeout-seconds 1800 --poll-seconds 60 --json`，或一个有明确截止时间的原生轮询循环。`watch` 的参数范围先以本机 schema 核对。读取命令退出码之后还须检查报告中的实际 CI 状态；退出 0 不代表 CI 通过。

普通运行阶段只读有界状态；状态未变不反复读取完整 job trace 或重写进度。失败、卡住或需要诊断时才读取相关作业 trace。新 push/head、流水线重试或取消使旧 CI 结果失效：结束旧观察，核对新 head 并重新绑定观察目标。不得把通知、旧 head 的成功或某几个完成作业当作当前 head 的完整七门禁成功。到期且未完成则记录实际状态和恢复条件，不能把 timeout 当通过，也不启动无限守护进程。

准备 consequential write 时仍需新的原生读回：ready/merge 前确认当前 head、target、CI、review/discussion 和保护状态；merge 后确认目标合并 SHA 和 Issue 状态。这些读回不属于应省略的重复轮询。

### 实现合并与部署验收分离

当实现必须先合入发布源才能部署验收，MR 使用 `Related: #<id>`，验收 Issue 保持开放。检查 MR 正文、源提交消息和平台的 closing-Issue 关联，避免夹带 `Closes`/`Fixes` 导致提前关闭。实现审阅、本地七门禁、当前 head 独立 CI 和保护全部照常执行；Related-only 只改变关闭时点。

SpecGit 2.9.2 与 2.10.0 均已观察到 `pr --ready --dry-run` 要求选中 Issue 的 closing reference。2.10.0 创建 MR 默认追加 `Closes`，且没有显式延期验收关联模式；这是该版命令契约的限制，不能据此判定尚未完成的部署验收应关闭。每次先核对当前安装版能力并保留最小失败命令、版本、退出码与诊断。若仍要求 closing reference，保留 checkpoint/guard，原生创建或准备 Related-only MR，用 `specgit pr --request <iid> --status --json` 读回；审阅完成后在已有授权内用原生 GitLab ready/merge。不要为了 ready 补入虚假的 closing reference，也不要修改受管资产、共享 CLI、保护或 auto-merge 偏好。

合并后原生确认目标 SHA 已合并且验收 Issue 仍开放；完成同 SHA 对应的发布、L0/L1 和受影响链验收后，把规范证据写入 Issue，再在已有关闭授权内显式关闭并读回。失败时保持开放并按发布协议恢复已验证版本。仅开发 hook/文档且不影响运行产物的改动，记录不需部署的范围依据，仍核对本地检查、CI、评审、合并和关闭。普通 merge 授权不等于 Release 发布授权，Related-only 路径不扩大任何授权范围。

## 宿主 hooks

本项目使用 Claude Code、Codex 与支持 Claude hook 协议的定制 OpenCode；其项目配置分别位于 `.claude/settings.json`、`.codex/hooks.json` 与 `.opencode/hooks.json`，由 CLI 独立注册。新工作树不能凭其他工作树的配置文件声称宿主集成已激活。SpecGit 2.13 起宿主回调只保留 SessionStart（短规则与 Skill 入口）和 PreToolUse（按需上下文）；checkpoint 只在真实 Git commit/push 边界由 Git guard 检查，不拦截普通编辑。PostToolUse、Stop 与异步唤醒已退休，不再注册；升级时由 `specgit setup` 迁移旧受管块，并只清除 SpecGit 自有的这些事件登记，其他工具的 hooks 保持原样。注册记录与真实 guard 阻止行为分别验证；远端观察使用显式有界 watch/inbox。官方 OpenCode 的项目 Skill 发现机制不等于该定制版本的 hook 支持；宿主能力和具体注册方式以当前 CLI 及实际运行时为准。

## 历史

2026-10-02 以前的全局注册、宿主 hook 和 SpecGit 2.2.1 说明仅是历史记录，不再是本项目的安装或维护要求。历史报告按原记录时点阅读，不作为当前配置、Issue 状态或部署版本的证明。
