# 发布门禁

发布门禁是晋级 `prod` 之前必须通过的一道检查（#707）。它不替代测试，只核对测试与验收是否真的做完、做对了版本、留了证据：回归测试覆盖本次全部修改点，fake 与真实上游的差异已回灌或经用户明确接受，验收证据齐全。

门禁由两部分组成：

- `scripts/release-gate.py`：读本地 git 做确定性检查（下面的 ① 和 ②），输出 `PASS`/`FAIL` 与原因，退出码 0/1，用法错误 2。git 之外它只读一样东西：单元行以 `CI` 为证据时，该行 revision 的提交状态 `host-ci/gates`（只读 `gh api`，见 ①）。
- 门禁 Agent（`.claude/agents/release-gate.md`）：先读本文件，再跑脚本，然后用只读的 `gh` 核对脚本判断不了的证据（③），给出逐项结论；通过时输出回执 JSON。

任一项不通过，整次门禁就是 `FAIL`，没有「基本通过」。

## 谁在何时运行

| 时机 | 运行者 | 范围 |
|---|---|---|
| 晋级 `prod` 前：AGENTS.md 五阶段的阶段 3 收尾、进入阶段 4 之前（`dev-test` 发布、L0/L1/L1 Agent 与本次相关回归都做完之后） | 主会话调用门禁 Agent，`--phase promote` | ①②③（① 中 `prod-` 层的行与 ③ 中「晋级后回归」除外） |
| 打 tag、发 GitHub Release 前：阶段 5 打 tag 之前（`prod` 人工 Review 与晋级后回归做完之后） | 主会话再次调用门禁 Agent，`--phase tag` | ①②③ 全部 |

两次都对同一个候选 revision、以上一个 tag 为 `--base` 运行，只是阶段不同（不写 `--phase` 时按 `tag`，即全部要求）：

```sh
# 晋级 prod 前（阶段 3 收尾、进入阶段 4 之前）
python3 scripts/release-gate.py --base <上一个 tag> --head <候选完整 SHA> --phase promote --json
# 打 tag、发 GitHub Release 前（阶段 5 打 tag 之前）
python3 scripts/release-gate.py --base <上一个 tag> --head <候选完整 SHA> --phase tag --json
```

`--json` 输出里的 `phase` 记录本次是哪个阶段；去掉 `--json` 得到可读文本。

脚本默认读取候选 revision 里的 `docs/regression-matrix.md` 与本文件（`git show <head>:<path>`），所以表和规则都必须已经提交进候选 revision。`--matrix`、`--rules` 只用于对照草稿，不能作为门禁结论的依据。

## 修改点

修改点按 `--base..--head` 的提交计算：

- 非合并提交的 `Issue: #N` 行（`Issues:` 同样识别，一行可写多个 `#N`）引用的每个 Issue；
- 每个合并的 PR（提交标题 `Merge pull request #N …`）所关联的 Issue：该 PR 分支上提交的 `Issue:` 行，以及合并提交正文里的 `Closes/Fixes/Resolves #N`。一个 Issue 都没关联的 PR，本身作为修改点 `#N`；
- 改动到的 fake/Provider/Catalog 区域：`internal/testutil/fakeupstream/`、`internal/providers/`、`internal/catalog/default_catalog.json`（Offering 与参数）。区域不单独要求回归表行，而是触发检查 ②。

没有 `Issue:` 行的非合并提交直接判 `FAIL`：无法追溯的改动不能证明被回归覆盖。

## ① 回归覆盖 100% 修改点

通过标准（脚本判断）：

1. 候选 revision 里存在 `docs/regression-matrix.md`，且有主表（列：`ID`｜`功能 / 行为`｜`来源 Issue`｜`验证层`｜`命令或脚本`｜`期望`｜`状态`｜`最后验证`｜`证据`）。主表是「3. 主表」一节下按小节拆开的**全部**表（表头含 `ID`、`来源 Issue`、`状态`、`最后验证`），逐行读取；`来源 Issue` 里用 `,` 或 `、` 分隔的每个 `#N` 都算。回归表「5. 已知缺陷（不进门禁）」一节不是主表，门禁不读。
2. 回归表本身在范围内被修改：`git diff --name-only base..head` 含 `docs/regression-matrix.md`。
3. 每个修改点在主表里至少有一行 `来源 Issue` 含它（`OBSOLETE` 行不算），并且**含它的每一个非 `OBSOLETE` 行**都满足：
   - `状态` 为 `OK` 或 `MANUAL`（`GAP` 不通过）；
   - `最后验证` 形如 `<revision 前 8 位> <YYYY-MM-DD>`，且这个 revision 对候选**仍然有效**：等于候选 revision 前 8 位；或者能被 git 解析为唯一的提交、是候选的祖先（`git merge-base --is-ancestor`），且 `git diff --name-only <该 revision>..<候选>` 里没有运行产物。运行产物的口径与「豁免清单」相同：不在 `docs/`、`scripts/`、`.github/`、`.claude/` 下、也不是根目录 `*.md` 的文件（脚本里是同一个函数）。解析不了（不存在、前缀不唯一）、不是祖先、此后改了运行产物，都按无效处理并在原因里写明；
   - `证据` 非空（不是空白或 `—`）。`验证层` 为 `unit` 的行可以写 `CI`：这时脚本还要求该行 revision 最新的提交状态 `host-ci/gates` 为 `success`（`gh api repos/<owner>/<repo>/commits/<完整 SHA>/statuses`，owner/repo 取自 `origin`；同一 revision 只查一次）。读不到状态（`gh` 不可用、认证或网络失败）、没有该状态或状态不是 `success`，该行不通过。离线或测试时用环境变量 `RELEASE_GATE_STATUS_FILE` 指向一个 JSON 对象代替 `gh`：键是完整 SHA 或至少 8 位的前缀，值是 `host-ci/gates` 的状态（如 `{"fc2bf5f5": "success"}`），没列出的 revision 视为没有状态。其他层的 `CI` 只是一段普通证据文字，不触发这项核对；
   - `--phase promote` 时，`验证层` 以 `prod-` 开头的行（`prod-readonly`、`prod-paid`；前缀不区分大小写）不要求上面两条：它们属于晋级后回归，在 `prod` 上验证，由 `--phase tag` 那次要求。这类行仍要存在、状态仍要是 `OK` 或 `MANUAL`（`GAP` 照样不通过）。
4. 或者该修改点在下面「豁免清单」里有一条**有效**豁免。

「含它的每一行都要有当前验证」比「至少一行」更严：改动了某个 Issue 的代码，这个 Issue 登记过的所有行为都可能受影响。祖先 revision 上的验证只在此后没有运行产物变化时才算数，所以放宽的只是「必须正好是候选」：回归表本身就在候选里，写不出候选自己的 SHA，而只改文档、脚本或 CI 的提交不会让已做的验证失效。

**缺陷不是回归行。** 没修的已知缺陷、待产品决定的问题写在回归表「5. 已知缺陷（不进门禁）」（`缺陷`｜`Issue`｜`记录日期`｜`备注`），由各自的 Issue 跟踪，不拦发布；主表不放「没修的缺陷」。缺陷修好时，在同一改动里写回主表成为回归行，此后按上面的规则核对。

**跑 dev-test 的 fake 行**：`python3 scripts/regression/run-fake-rows.py`（`--dry-run` 只列出要跑的行与命令，`--only <ID,...>` 只跑其中几行）依次运行主表里 `dev-test-fake` 层、状态为 `OK`/`MANUAL`、命令是单条 `python3`/`bash`/`node` 命令的行，逐行打印 `PASS`/`FAIL` 与耗时，输出带本地 `HEAD` 与日期，用来贴证据评论；它不改回归表。

## ② fake 缺陷全程补齐并有文档记录

通过标准（脚本判断）：

1. 范围内改了 `internal/testutil/fakeupstream/`、`internal/providers/` 或 `internal/catalog/default_catalog.json` 时，必须满足其一：
   - `docs/fake-register.md` 或 `internal/testutil/fakeupstream/register/register.json` 在范围内被修改；
   - 回归表「Fake 与真实的已知差异」一节里有一行 `无新增差异（核对于 <候选 revision 前 8 位> <YYYY-MM-DD>）`。核对于其他 revision 的这一行不算。
2. 回归表必须有「Fake 与真实的已知差异」一节，其中的表（列：`差异`｜`状态`｜`Issue`｜`记录日期`｜`备注`）：
   - 不得有 `OPEN` 行，不论本次是否改了 fake；
   - `ACCEPTED` 行的 `备注` 必须写明用户明确接受的日期（`YYYY-MM-DD`）；
   - 状态只能是 `OPEN`、`FIXED`、`ACCEPTED`。

门禁 Agent 另外核对（脚本判断不了）：DEV → TEST → 回归过程中发现的每一处真实与 fake 的差异，都已经回灌进 fake 并**合并进候选 revision**（不是停在未合并的分支里），或登记为 `ACCEPTED`；回灌按 `AGENTS.md`「上游接口与 Fake」的完成标准，登记表条目与 `docs/fake-register.md` 一致。

## ③ 验收证据齐全

由门禁 Agent 用只读的 `gh issue view`、`gh api` 核对，脚本只列出清单：

1. 候选 revision 在 `dev-test` 的机器冒烟报告 L0、L1、L1 Agent 三层都是 `pass`，`failed=0`、`skipped=0`、`soft_failed=0`、`passed>0`（口径见 `AGENTS.md`「发布后冒烟判定与晋级校验」；Agent 不登录主机，以 #2 或关联 Issue 里引用该报告的评论为准）。
2. #2 里有该候选 revision 的 `dev-test` 验收证据评论（L0、L1、L1_agent、related），评论里的 revision 是候选完整 SHA，链接真实指向本仓库对应评论。
3. 晋级前回归证据（`dev-test` 上本次相关回归）存在且对应候选 revision；打 tag 前的第二次运行还要求晋级后回归证据（`prod` 上的回归）存在且对应候选 revision。
4. 回归表里每一行的 `证据` 链接能打开，且记录的是该行「最后验证」revision 上的结果，而该 revision 已由脚本判定对候选有效（①）；抽查不够：本次修改点涉及的行逐条核对。`unit` 行的 `CI` 证据由脚本核对提交状态，不需要链接。

## 回执

门禁通过时，门禁 Agent 输出一个回执 JSON 块，由人或主会话贴到 #2 的评论里；门禁 Agent 自己不发评论：

```json
{"release_gate": 1, "revision": "<完整 40 位 SHA>", "status": "pass", "rules_version": <本文件变更记录条数>, "base": "<上一个 tag>", "changed_issues": [704, 705], "checked_at": "<UTC 日期>"}
```

晋级时，`prod_promotion` 验收报告（格式 2）的 `release_gate.evidence` 指向这条评论；`scripts/release-env.py release prod` 读取该评论，要求其中恰有一个 `release_gate` 块、`revision` 与候选完整一致、`status` 为 `pass`，否则拒绝晋级且不联系目标主机（格式见 `docs/deployment.md` §4）。

## 豁免清单

只有改动全部落在 `docs/`、`scripts/`、`.github/`、`.claude/` 或根目录 `*.md` 的 Issue（纯文档、脚本、CI 类）可以豁免回归表行；改了运行产物的 Issue 豁免无效。每条豁免必须写 Issue（恰好一个 `#N`）、理由和**用户在聊天里明确同意的日期**；没有同意日期的豁免无效，该 Issue 仍要求回归表行。豁免对整个 Issue 生效；Issue 之后又改了运行产物，豁免自动失效，应删除该行。

| Issue | 类别 | 理由 | 用户同意日期 |
|---|---|---|---|

## ④ 规则自我迭代协议

1. 每次发布后（tag 与 GitHub Release 完成后），门禁 Agent 复盘本次放过了什么、漏掉了什么：有没有发布后才发现的回归、fake 差异、证据缺口，规则或脚本有没有误判。
2. 复盘结论落成对本文件与 `.claude/agents/release-gate.md`（需要时连同 `scripts/release-gate.py`）的修改建议，**只通过 PR 提交，门禁 Agent 不自行合并**。
3. 规则**只能收紧或补充**。放宽任何检查（删掉一项、降低标准、扩大豁免范围）必须由用户在聊天里明确同意，并写入下面的变更记录：日期、内容、同意依据。
4. 每条规则变更都在下表追加一行；回执里的 `rules_version` 等于本表的行数。

## 变更记录

| 日期 | 内容 | 原因 | 同意依据 |
|---|---|---|---|
| 2026-11-28 | 创建发布门禁：①回归覆盖 100% 修改点、②fake 差异回灌并登记、③验收证据齐全、④规则只收紧的自我迭代协议；晋级 `formal-test` 要求格式 2 验收报告带 `release_gate` 回执 | v1.9.6 带着已知 fake 差异发布，也没有回归测试表（#707） | 新增规则（收紧），无需放宽同意 |
| 2026-11-28 | ①的主表明确为「3. 主表」下的全部表，脚本逐表读取所有行 | 脚本原来只读到第一张表（门禁 5 行），其余小节的行没有被核对（#707） | 修正（收紧），无需放宽同意 |
| 2026-11-28 | 环境名 `formal-test` 改为 `prod`，验收报告键 `formal_promotion` 改为 `prod_promotion`（格式号不变，不保留旧键） | 统一环境命名（#716） | 改名，检查项与标准不变，无需放宽同意 |
| 2026-11-28 | ①放宽「最后验证必须正好是候选」：候选的祖先且此后没有运行产物变化（口径同豁免清单）也有效；新增 `unit` 行证据写 `CI` 时核对 `host-ci/gates` 为 success；回归表新增「5. 已知缺陷（不进门禁）」，缺陷由 Issue 跟踪、不拦发布；脚本加 `--phase promote|tag`，`promote` 不要求 `prod-` 层行的当前验证；新增 fake 行入口 `scripts/regression/run-fake-rows.py`。其余检查（fake 差异表、豁免清单、`Issue:` trailer、状态合法性、`GAP` 要有备注）不变 | 回归表在候选里，写不出候选自己的 SHA；单元行没有认 CI 的办法；没修的缺陷当成 GAP 行拦发布；PROD 层的行只能在晋级后验证（#718） | 放宽的只有「最后验证必须正好是候选」一条，维护者 2026-11-28 在对话中同意（#718）；其余为补充 |
