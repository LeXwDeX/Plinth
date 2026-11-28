# 发布门禁

发布门禁是晋级 `formal-test` 之前必须通过的一道检查（#707）。它不替代测试，只核对测试与验收是否真的做完、做对了版本、留了证据：回归测试覆盖本次全部修改点，fake 与真实上游的差异已回灌或经用户明确接受，验收证据齐全。

门禁由两部分组成：

- `scripts/release-gate.py`：只读本地 git，做确定性检查（下面的 ① 和 ②），输出 `PASS`/`FAIL` 与原因，退出码 0/1，用法错误 2。
- 门禁 Agent（`.claude/agents/release-gate.md`）：先读本文件，再跑脚本，然后用只读的 `gh` 核对脚本判断不了的证据（③），给出逐项结论；通过时输出回执 JSON。

任一项不通过，整次门禁就是 `FAIL`，没有「基本通过」。

## 谁在何时运行

| 时机 | 运行者 | 范围 |
|---|---|---|
| 晋级 `formal-test` 前（`dev-test` 发布、L0/L1/L1 Agent 与本次相关回归都做完之后） | 主会话调用门禁 Agent | ①②③（③ 中「晋级后回归」除外） |
| 打 tag、发 GitHub Release 前（`formal-test` 人工 Review 与晋级后回归做完之后） | 主会话再次调用门禁 Agent | ①②③ 全部 |

两次都对同一个候选 revision、以上一个 tag 为 `--base` 运行：

```sh
python3 scripts/release-gate.py --base <上一个 tag> --head <候选完整 SHA>
python3 scripts/release-gate.py --base <上一个 tag> --head <候选完整 SHA> --json
```

脚本默认读取候选 revision 里的 `docs/regression-matrix.md` 与本文件（`git show <head>:<path>`），所以表和规则都必须已经提交进候选 revision。`--matrix`、`--rules` 只用于对照草稿，不能作为门禁结论的依据。

## 修改点

修改点按 `--base..--head` 的提交计算：

- 非合并提交的 `Issue: #N` 行（`Issues:` 同样识别，一行可写多个 `#N`）引用的每个 Issue；
- 每个合并的 PR（提交标题 `Merge pull request #N …`）所关联的 Issue：该 PR 分支上提交的 `Issue:` 行，以及合并提交正文里的 `Closes/Fixes/Resolves #N`。一个 Issue 都没关联的 PR，本身作为修改点 `#N`；
- 改动到的 fake/Provider/Catalog 区域：`internal/testutil/fakeupstream/`、`internal/providers/`、`internal/catalog/default_catalog.json`（Offering 与参数）。区域不单独要求回归表行，而是触发检查 ②。

没有 `Issue:` 行的非合并提交直接判 `FAIL`：无法追溯的改动不能证明被回归覆盖。

## ① 回归覆盖 100% 修改点

通过标准（脚本判断）：

1. 候选 revision 里存在 `docs/regression-matrix.md`，且有主表（列：`ID`｜`功能 / 行为`｜`来源 Issue`｜`验证层`｜`命令或脚本`｜`期望`｜`状态`｜`最后验证`｜`证据`）。
2. 回归表本身在范围内被修改：`git diff --name-only base..head` 含 `docs/regression-matrix.md`。
3. 每个修改点在主表里至少有一行 `来源 Issue` 含它（`OBSOLETE` 行不算），并且**含它的每一个非 `OBSOLETE` 行**都满足：
   - `状态` 为 `OK` 或 `MANUAL`（`GAP` 不通过）；
   - `最后验证` 形如 `<revision 前 8 位> <YYYY-MM-DD>`，且 revision 等于候选 revision 前 8 位；
   - `证据` 非空（不是空白或 `—`）。
4. 或者该修改点在下面「豁免清单」里有一条**有效**豁免。

「含它的每一行都要在候选 revision 上重新验证」比「至少一行」更严：改动了某个 Issue 的代码，这个 Issue 登记过的所有行为都可能受影响。

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
3. 晋级前回归证据（`dev-test` 上本次相关回归）存在且对应候选 revision；打 tag 前的第二次运行还要求晋级后回归证据（`formal-test` 上的回归）存在且对应候选 revision。
4. 回归表里每一行的 `证据` 链接能打开，且记录的是候选 revision 上的结果（抽查不够：本次修改点涉及的行逐条核对）。

## 回执

门禁通过时，门禁 Agent 输出一个回执 JSON 块，由人或主会话贴到 #2 的评论里；门禁 Agent 自己不发评论：

```json
{"release_gate": 1, "revision": "<完整 40 位 SHA>", "status": "pass", "rules_version": <本文件变更记录条数>, "base": "<上一个 tag>", "changed_issues": [704, 705], "checked_at": "<UTC 日期>"}
```

晋级时，`formal_promotion` 验收报告（格式 2）的 `release_gate.evidence` 指向这条评论；`scripts/release-env.py release formal-test` 读取该评论，要求其中恰有一个 `release_gate` 块、`revision` 与候选完整一致、`status` 为 `pass`，否则拒绝晋级且不联系目标主机（格式见 `docs/deployment.md` §4）。

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
