# 部署入口

本文是 Plinth 内部环境部署的入口。用户在仓库根目录说「部署到 <环境>」（例如「部署到正式测试环境」）时，Agent 按本文执行。本项目开源：主机、地址、端口、目录与 Compose 名只写在未纳入 Git 的本地部署文件夹 `deploy/local/`，本文和其他仓库文档只用环境名。密钥只在各环境服务器的 `production/runtime.env`（`0600`）中，不进本地环境文件、仓库、Issue 或报告。

自部署（开源用户）不使用本文，见 `README.md` 的 `docker compose` 一条命令部署；外部合作方的统一部署方式与出问题时的录制、诊断包流程见 [合作方部署指南](partner-deployment.md)。

## 1. 环境

| 环境名 | 用途 | 接收的 revision | 数据 | 发布后验收 | 付费生成 |
|---|---|---|---|---|---|
| `dev-test` | 开发测试环境 | 每次已推送的 `origin/main` | 每次发布全新数据（`"data": "ephemeral"`，#28）：不含历史迁移数据，只有运行环境引导的管理员测试账号；缺省为 fake 上游形态（#219，见下文「fake 上游形态」） | 发布脚本自动跑 L0、L1、L1 Agent 并写机器报告（fake 形态，零费用，见 §4）；L2 等真实模型验收只在用户当次同意后以真实形态发布再跑 | 真实形态与付费验收须当次同意（见 `AGENTS.md`） |
| `formal-test` | 正式测试环境（测试版本中的稳定版）：单版本、不留残余 | 只接收按 §4 晋级规则通过的精确 revision | 本栈独立，不跨主机复制 | L0、L1、L1 Agent（真实形态不自动跑，按 §6 手动执行并留证） | 不做；测试期付费同意授权不适用 |

只有上表两个环境可部署（#270）。`legacy-formal` 已下线，环境名不再可用（存档 #74）；环境沿革见 [`history.md`](history.md)。

## 2. 本地环境文件

内部环境的地址、路径、账号与运维细节只放在不被 Git 追踪的本地部署文件夹 `deploy/local/`（`.gitignore` 排除；旧位置 `deploy/*.local.json` 也被排除并仍可读取）。环境文件是 `deploy/local/environments.json`：新工作站从 `deploy/environments.example.json` 复制并填写，或由用户/协调会话提供。`scripts/release-env.py` 经 `scripts/local_environments.py` 查找它：先用 `--file` 或 `ART_AI_ENVIRONMENTS_FILE`，再依次找当前检出与主检出（`git rev-parse --git-common-dir` 的上一级）中的 `deploy/local/environments.json` 和旧的 `deploy/environments.local.json`，因此所有 worktree 共用主检出的一份。仓库里的文件只写环境名，具体值都从本地文件读。

| 字段 | 含义 |
|---|---|
| `ssh` | 部署账户与主机，免密 SSH |
| `root` | 发布根：`app/`（源码）、`releases/manifest.txt`、`backups/`、`production/` |
| `base_url` | 冒烟与回执使用的入口 |
| `compose_project` | Compose 项目名；容器为 `<项目>-art-ai-go-1` |
| `protect_projects` | 共享主机上同机其他 Plinth Compose 项目（例如候选栈）；镜像保留不清理它们容器引用的镜像。仓库脚本不写任何环境的项目名（#261），共享主机必须在这里列全；`single_stack` 环境不能设置。由 `release-env.py` 发布的环境若不是 `single_stack`，就按共享主机处理，缺这个字段即报错；文件里与它同主机（`ssh` 主机相同）的每个环境的 `compose_project` 都必须列出，`single_stack` 环境不得与其他环境同主机（#334） |
| `protected_paths` | 迁移工具（`backup-go-production.sh create`、`run-go-etl-container.sh`、`finalize-go-production.sh`）绝不能作为目标的目录：发布根之外的绝对路径列表（例如共享主机上旧 Next 项目根），没有就写 `[]`；由 `release-env.py` 发布的环境必填。`release-env.py` 把「`root` + 本列表」用冒号连成 `ART_AI_PROTECTED_PATHS`：`compose-env` 输出它供手工运行，`release` 经服务器段写入 `production/compose.env`。三个迁移工具在未设置 `ART_AI_PROTECTED_PATHS` 时拒绝运行（finalize 在停旧容器之前），确无受保护目录时须显式设为空串（#334） |
| `backup` | 可选的发布备份策略（#364、#66）：`{"scope": "database", "keep": 2}`（仅 `dev-test`，不得与 `promote_from` 同时出现）表示停机时只备份 `production/data`；`{"scope": "full", "keep": 2}`（`dev-test` 与 `formal-test` 可设，可与 `promote_from` 同时出现）仍备份 `data`、`storage-go`、`web-data`，成功后同样清理。正常成功清理最多保留最新 2 个已结束发布备份；进行中的快照可临时排除，但不承诺长期额外保留；更早的失败快照也按最新 2 个回收，独立失败报告与诊断日志保留。省略时为全量备份 `data`、`storage-go`、`web-data`，且不自动清理历史备份。`formal-test` 必须写 `{"scope": "full", "keep": 2}`（#66），全量备份成功后只保留最新 2 份；发布备份不含镜像和日志。`scope` 只接受 `database`/`full`，`keep` 为 1–2 |
| `data` | 仅 `dev-test` 可选（#28）：唯一取值 `"ephemeral"`，每次发布都是全新数据，数据库、资产存储与 legacy 缓存用匿名卷、不挂载主机目录；不得与 `backup` 或 `promote_from` 同时出现；省略时为持久数据（`formal-test` 保持此默认）。`release-env.py` 向服务器段传 `RELEASE_DATA_MODE=ephemeral` |
| `promote_from` | 只能从该环境晋级（`formal-test` 为 `dev-test`） |
| `single_stack` | 单项目主机：`image_keep`（保留镜像数，含运行镜像）、`keep_source_snapshots`（保留的 `app.prev-*` 数）、`allow_bootstrap`（允许空栈首次启动） |
| `layout` | Compose 主机路径与绑定地址（`GO_WEB_BIND_IP`、`GO_WEB_HOST_PORT`、`GO_PROD_*_DIR`、`GO_LEGACY_*_DIR`，另有两个可选键 `MCP_PUBLIC_BIND_IP`、`MCP_PUBLIC_HOST_PORT`，见 §6「外部 MCP 监听」）；数据目录必须是 `<root>/production/<名称>`，未写时按此导出；legacy 目录没有缺省值，必须写明：单栈环境在 `<root>/production/` 下以 `legacy` 开头，共享主机（`dev-test`）可为发布根之外不含 `..` 的绝对路径 |
| `extra_compose_file` | 追加在主 Compose 文件之后的服务器本地覆盖文件（`<root>/production/` 下，例如固定项目网络子网与容器地址）；只用于本栈服务，隔离预检不用它 |
| `build` | `network`（`docker build --network`，例如构建容器须以宿主源地址访问内网镜像源时为 `host`）、`goproxy`（GOPROXY 构建参数）、`npm_registry`（NPM_REGISTRY 构建参数，单个 http(s) 地址，指向内部包缓存）、`add_hosts`（`docker build --add-host` 的 `主机名:地址` 列表，用于解析内网 Go 代理等构建依赖主机；缺省 npm 源为公共 registry）；仓库缺省 npm 源为公共 `registry.npmjs.org`（#257：仓库不依赖内部源），内部环境只在本地环境文件里设 `npm_registry` |
| `source_transfer` | `pipe`（缺省，`git archive` 经 SSH 管道解包）或 `scp`（本地生成 tar，scp 到 `<root>/releases/` 解包后删除） |
| `managed_by` | 已移除（#74，`legacy-formal` 下线）：环境文件里出现这个字段会被拒绝，并提示删除 |
| `acceptance` | 晋级验收报告所在的 GitHub 仓库 `forge_repo`（`owner/repo`）与 Issue 编号：`issue` 供 `formal-test`（旧字段 `legacy_formal_issue` 随 `legacy-formal` 下线不再使用，可从环境文件删除）；本仓地址 `forge_url`（验收证据 URL 必须以它开头）。`--acceptance-note` 传该 Issue 下某条 comment 的数字 ID，脚本用 `gh api repos/<forge_repo>/issues/comments/<ID>` 读取，并拒绝属于其他 Issue 的 comment |

查看：`python3 scripts/release-env.py list`、`python3 scripts/release-env.py show <环境>`。

## 3. Agent 工作流

前提：要发布的提交已推送到 `origin/main`；本机门禁按 `dev_test.md` §2 通过。

**部署到开发测试环境（`dev-test`）**

1. `python3 scripts/release-env.py release dev-test`（同步 → 构建 → preflight → 备份 → Compose 更新 → 健康 → 失败自动回滚；`data: ephemeral` 时无备份，见下文「全新数据发布」；缺省为 fake 上游形态，真实形态加 `--live-upstream` 并设 `ART_AI_LIVE_ACK=1`，见下文「fake 上游形态」）。
2. 核对容器 OCI revision 等于 `origin/main` 完整 SHA（`release-env.py smoke` 设置的 `EXPECT_SHA` 即读自容器标签，L1 Agent 冒烟同时比对 `/api/version`）。
3. fake 形态下发布脚本接着自动建 Agent 专用账号并跑 §6 的 L0、L1、L1 Agent，报告落在发布根（§4）。真实形态不自动跑：按 §6 手动执行这三层（L1 会调用真实对话模型，须先取得当次同意），再跑本次改动相关的页面、API 与 Agent/生成链路验收。
4. 全部 PASS 后，在 `acceptance.issue` 写入一条 §4 格式的验收报告 note；记下 note ID。机器报告不用复制到 Issue，晋级时脚本直接读源环境发布根下的那一份。

**部署到正式测试环境（`formal-test`）**

1. 确认目标 revision 在 `dev-test` 已按上面第 1–4 步稳定通过，并取得验收报告 note ID。不满足就停止并告诉用户缺什么，不降级验收。
2. `python3 scripts/release-env.py release formal-test --acceptance-note <noteID>`；不给 `--revision` 时取 `dev-test` 当前运行的 revision，也可给一个更早的已验收完整 SHA。脚本除 Issue comment 外还核对 `dev-test` 发布根下该 revision 的机器报告（§4），报告缺失时按 §4 的回退规则只核对 comment。
3. 冒烟（§6），核对 revision。
4. 在 #270 记录部署 SHA、冒烟结果与异常；不写回本文。

失败时按 §5 的自动回滚结果和报告处理；冒烟失败按 §7 手工回滚，记录证据后回到本地修复，并重新经过 `dev-test` 验收再晋级。

## 4. 晋级规则与验收报告

开发测试环境（包括 Agent 测试）全部稳定通过以后，才发布到正式测试环境。`release-env.py` 对有 `promote_from` 的环境强制执行，任一项不满足即拒绝且不联系目标主机：

- revision 是 40 位小写完整 SHA，且是 `origin/main` 的祖先；
- 源环境的成功发布清单（`<root>/releases/manifest.txt`）中有该 revision 与报告中的同一镜像标签；
- 源环境发布根下该 revision 的**机器冒烟报告**存在时，其 L0、L1、L1 Agent 三层都是 `status` 为 `pass`、`failed: 0`、`passed > 0`、`skipped: 0`、`soft_failed: 0`；报告缺失时按下面的回退规则只核对 Issue comment；
- `acceptance.issue` 下指定的 comment 中恰有一个如下 JSON fenced block，四项检查都是 `pass`、`failed: 0`、`passed > 0`、`skipped: 0`、`soft_failed: 0`，每项证据为本仓库内的完整 URL（校验函数在 `scripts/release-promotion.py`，`release-env.py` 加载，`scripts/tests/test_release_promotion.py`、`scripts/tests/test_release_env.py` 覆盖）：

```json
{
  "formal_promotion": 1,
  "revision": "<完整40位小写SHA>",
  "image_tag": "<dev-test manifest 中对应的精确标签>",
  "checks": {
    "L0": {"status": "pass", "failed": 0, "passed": 1, "skipped": 0, "soft_failed": 0, "evidence": "<本仓 Issue/MR note 完整 URL>"},
    "L1": {"status": "pass", "failed": 0, "passed": 1, "skipped": 0, "soft_failed": 0, "evidence": "<…>"},
    "L1_agent": {"status": "pass", "failed": 0, "passed": 1, "skipped": 0, "soft_failed": 0, "evidence": "<…>"},
    "related": {"status": "pass", "failed": 0, "passed": 1, "skipped": 0, "soft_failed": 0, "evidence": "<本次相关浏览器/Agent 验收 note URL>"}
  }
}
```

`related` 覆盖本次改动涉及的浏览器与 Agent 验收（含按 `scripts/smoke/README.md` 需要的 L2 真实模型链路）。脚本无法判断「稳定」本身：报告只能在全部检查实际通过后写入，有失败、跳过或软失败就不写。

### 机器冒烟报告（`release-env.py` 自动产出，#411）

`dev-test` 的 fake 形态发布成功后，`release-env.py` 依次跑 `smoke-l0-browser-boot.sh`、`smoke-l1-free.sh`、`smoke-l1-agent-mode.sh`，把脚本自己的汇总行（`PASS=<n> FAIL=<n>[ SKIP=<n>]` 与 `SOFT-FAIL` 行）读成一份 JSON，落在发布根下不追踪的位置：

- 报告：`<root>/production/reports/smoke-<revision>.json`
- 每层日志：`<root>/production/reports/smoke-<revision>/<层名>.log`（`0600`，目录 `0700`）

```json
{
  "schema": "plinth.smoke-report",
  "version": 1,
  "environment": "dev-test",
  "revision": "<完整40位小写SHA>",
  "upstream_shape": "fake",
  "started_at": "2026-11-22T01:02:03Z",
  "finished_at": "2026-11-22T01:07:04Z",
  "checks": {
    "L0": {"status": "pass", "passed": 4, "failed": 0, "skipped": 0, "soft_failed": 0,
           "script": "smoke-l0-browser-boot.sh", "started_at": "…", "finished_at": "…",
           "log": "<root>/production/reports/smoke-<revision>/L0.log"},
    "L1": {"status": "pass", "passed": 9, "failed": 0, "skipped": 0, "soft_failed": 0, "…": "…"},
    "L1_agent": {"status": "pass", "passed": 40, "failed": 0, "skipped": 0, "soft_failed": 0, "…": "…"}
  }
}
```

- 判定：脚本退出码 0、汇总行 `failed=0`、`skipped=0`、输出无 `SOFT-FAIL` 行且 `passed>0` 才是 `status: "pass"`；`SKIP:`、`SOFT-FAIL:`、`PAID-TASK-ALERT`、非零退出码和缺汇总行都记 `fail`。判定规则与 `scripts/release-promotion.py` 的 `check_passed` 同一份，Issue comment 与机器报告因此口径一致。
- 落盘与失败：三层都会跑完再写报告；报告写不出去或某层不是 `pass` 时发布命令返回非零，但**发布本身不回滚**（这三层不在自动回滚范围）。按 `AGENTS.md`「发布后冒烟判定与晋级校验」记录证据、恢复已验证版本后再修。
- 回退：源环境没有该 revision 的报告时（例如 #411 之前发布的版本），晋级只核对 Issue comment，命令会打印一行提示；报告存在即核对，不存在的报告不会当成通过。
- 真实形态（`formal-test` 与带 `--live-upstream` 的 `dev-test`）不自动跑，也不写报告：L1 会调用真实对话模型，须每次取得当次同意，按 §6 手动执行。

### 保留数据升级演练（#435）

保留数据的环境从上一个运行版本跨到候选版本时，迁移第一次作用于存量行就发生在晋级发布现场；发布的 preflight 只在空的临时目录启动候选镜像，覆盖不到这一步。`scripts/upgrade-drill.py` 把这一步搬到晋级之前：用候选镜像在一次性目录里对一份存量数据库副本跑「启动 → 迁移 → 健康检查 → 只读冒烟」，把结果打成报告。

- 迁移逻辑由 `internal/adapters/sqlite/migration_v180_upgrade_test.go` 固化：用 v1.8.0 的迁移集合与合成数据建库（会话与消息、回合检查点、3D 处理计划、制作预算、SkillRun、任务与资产、执行授权、Agent 配置），再让当前代码升级到迁移头并逐列读回，还覆盖 0097/0107/0108/0109/0110/0111 的表重建与新增列。它随 `go test` 进门禁，但不使用真实数据。
- 演练脚本：`python3 scripts/upgrade-drill.py --image <候选镜像> --database <存量数据库副本> [--report <报告>] [--keep]`。脚本把副本复制进自己的目录（`--workdir` 指定位置，缺省 `mktemp -d`），以回环端口、`--read-only`、`--cap-drop ALL`、`no-new-privileges` 启动候选镜像，等 `/readyz` 与 `/healthz`，再用只读连接读回迁移后的库：`integrity_check`、`foreign_key_check`、台账连续性与版本、关键表的表存在性与行数（迁移不得丢行）。启动前脚本还在副本（只在副本里）以最新一版 Agent 配置为底追加一版 v1.8.0 形态的配置（#500、#525）：工具清单取打包的 v1.8.0 工具名清单（含已退役的 `observe_task`、`request_consent`）减去一个管理员关掉的工具，提示词取 v1.8.0 的默认文本；启动后要求这一版原样保留，且启动对账另发布了一个不含已退役工具的新版本，该版本为 format 2、只关闭种入时关掉的那一个工具（v1.8.0 之后新增的工具不得被关闭）、三处提示词仍标为跟随默认而不是冻结旧文本，否则演练失败。副本没有 `agent_configuration_versions` 表（早于 0041）时这一项记为失败。
- 默认不连真实环境：脚本只读来源文件；来源带 `-wal`/`-shm` 边车（正在使用的库）或位于环境文件某个 `root` 下（发布根、备份目录）时直接拒绝，退出码 2。确实要读真实路径时同时给 `--allow-real-path` 与 `ART_AI_LIVE_ACK=1`，只给一个仍然拒绝；来源的路径、大小与 sha256 记进报告。
- 退出码：0 通过、1 演练跑过但有检查失败、2 用法/守卫/环境错误（不启动容器）。容器与一次性目录总是清理，`--keep` 保留目录与 `report.md`；来源文件永不改动。
- 记录：把报告（至少 `结论` 与 `迁移版本 前 → 后`）写回本次晋级所用的 Issue；无论成败都不删除、不改动正式备份与正式实例。
- Agent 配置与回滚（#525）：升级后，已发布 Agent 配置里的工具与未自定义的提示词随 Schema 解析，不需要重置或重发；管理员自定义过的提示词保持不变，管理页提示「已自定义，不随版本升级更新」，要新默认须点「恢复跟随默认」后预览发布。新版本发布的配置版本（`format` 2）仍带旧镜像读得懂的 `enabled_tool_names`（只含 v1.8.0 清单内工具）与提示词文本，回滚到旧镜像不会因未知工具名而读不出；回滚后旧镜像自己发布的版本没有格式标记，再升级时按存量行换算，回滚期间管理员对新工具的关闭会丢失。

真实数据的一次演练仍是**授权动作**，不是自动门禁（脚本不在 `scripts/verify-local.py` 的门禁清单里，其单测随 `scripts tests` 跑）：执行前须取得用户当次授权，只用该环境最新一次发布成功的全量备份副本，不接触正式实例与正在使用的数据目录；把 revision、迁移版本与冒烟结论记到本次晋级所用的 Issue；无论成败删除演练副本与一次性目录。

## 5. 发布流程

两个环境走同一套脚本，`release-env.py` 只把环境参数交给 `scripts/release-go-testenv.sh`，服务器段为 `scripts/release-server-steps.sh`：

1. 源码：远端旧 `app/` 移为 `app.prev-<时间>`，`git archive <revision>` 解到新 `app/`。
2. 服务器段（`release-server-steps.sh` 与镜像保留 helper 先上传到发布根的 `tmp/`，不写主机 `/tmp`）：获取发布根锁 → 容量检查 → 以 `Dockerfile.go-runtime` 的 `runtime-with-migration-tools` 目标构建 `art-ai-go-production:<短SHA>-<时间>`（npm 缺省公共源，环境文件设 `build.npm_registry` 时用该源；按环境文件传 `GOPROXY`/`--add-host`）→ 日志目录以镜像运行身份写删验证 → `cutover-go-production.sh preflight`（回环端口、临时目录；临时目录放在发布根的 `tmp/` 下，不写主机 `/tmp`）→ 停旧服务做一致性备份（`backups/release-<tag>/`；默认备份 `data`、`storage-go`、`web-data`，只有 `dev-test` 可配置 `backup.scope=database` 仅备份 `production/data`，`backup.keep` 控制成功后清理）→ Compose 更新唯一 `art-ai-go` 服务 → healthz/readyz → 写成功清单 → 镜像保留与（仅设置了 `backup.keep` 的环境的）备份保留。发布脚本固定 `GO_LOG_LEVEL=debug`，测试与生产一致且不提供关闭入口；该值写入 `production/compose.env`，手工 Compose 也一致。
3. 失败：构建/预检失败不动旧实例；更新后健康失败先按备份范围恢复数据，再以发布前实际运行的镜像拉起，报告在 `production/reports/release-failures/`；失败路径不清理镜像和快照。整个服务器段持有发布根锁；清理 helper 继承并校验该描述符。`database` 模式只恢复数据库，候选写入的 `storage-go`/`web-data` 字节保持不动，因此不是完整回滚。

仓库不写死任何主机、地址、服务器路径或环境的 Compose 项目名（#249、#261）：`release-go-testenv.sh` 的 `RELEASE_HOST`/`RELEASE_ROOT`、服务器段的发布根与 `RELEASE_COMPOSE_PROJECT`（`release-env.py` 总是按 `compose_project` 传入）、镜像保留的 `--root`/`--project`、预检的 `GO_PROD_PROJECT` 和 `docker-compose.go-production.yml` 的 8 个主机路径都没有缺省值，一律来自环境文件。`layout` 中的键作为进程环境传给每次 Compose 与预检；设置了 `layout` 的环境成功后写出 `production/compose.env`（`0600`，无密钥：当前 `GO_IMAGE_TAG`、`GO_SOURCE_COMMIT`、`GO_RUNTIME_ENV_FILE`、`GO_LOG_LEVEL` 与布局键），供手工 Compose 使用。

`single_stack` 环境的差异（服务器段收到 `RELEASE_SINGLE_STACK=1`）：

- 镜像只保留运行镜像和本次回滚镜像（`--keep 1`），源码快照只留最新一份 `app.prev-*`；
- 本项目没有容器且没有数据库时，允许空栈 bootstrap 首次启动，失败则停止候选并保留现场；有数据库而无容器时拒绝；
- 健康检查访问 `GO_WEB_BIND_IP:GO_WEB_HOST_PORT`。

与是否单栈无关、按环境文件设置的选项：`extra_compose_file` 加到每条本栈 Compose 命令的主文件之后（文件须已在服务器上，越界、缺失或符号链接即在构建前拒绝）；`build` 的 `network`、`goproxy`、`npm_registry` 与 `add_hosts` 只改变 `docker build` 参数，未设 `npm_registry` 时 npm 源为公共 `registry.npmjs.org`（#257），镜像目标仍为 `runtime-with-migration-tools`；`source_transfer: scp` 只改变源码同步方式。`compose.env` 记录 `ART_AI_PROTECTED_PATHS`（#334）与布局变量。

`dev-test` 没有 `single_stack`：它在共享主机上，环境文件用 `protect_projects` 列出同机其他 Plinth Compose 项目（候选栈等），`release-env.py` 以 `RELEASE_PROTECT_PROJECTS` 传给服务器段，镜像保留对每个项目加 `--protect-project`，这些项目容器引用的镜像不被清理；环境文件还为它给出 `build`（Go 代理与 `--add-host`）和 `layout` 中的 4 个 legacy 目录。源码同步与服务器段命令序列与参数化之前相同，只多了上述项目参数（`scripts/smoke/test-release-stack.sh` 断言）。

### fake 上游形态（`data: ephemeral` 缺省，#219）

`dev-test`（`"data": "ephemeral"`）的发布缺省是 fake 上游形态，所有上游请求打到同一 Compose 项目里的 fake 服务，真实 Provider 零请求、零扣费：

- `release-env.py release dev-test` 向服务器段传 `RELEASE_UPSTREAM_SHAPE=fake`；`--live-upstream` 与 `ART_AI_LIVE_ACK=1` 同时给出才发布真实形态（只给一个即拒绝，且发起前须在当次对话取得用户同意）。其他环境给 `--live-upstream` 即拒绝；`formal-test` 永远是真实形态，不构建也不运行 fake。
- 服务器段（`RELEASE_UPSTREAM_SHAPE=fake` 只能与 `RELEASE_DATA_MODE=ephemeral` 同用，否则构建前拒绝）：另以 `Dockerfile.fake-upstream` 构建 `art-ai-go-fake-upstream:<tag>`（运行镜像不含 fake），在 `docker-compose.go-ephemeral.yml` 之后、`extra_compose_file` 之前叠加 `docker-compose.go-fake-upstream.yml`；先 `up -d --no-deps fake-upstream` 并等容器健康，再更新 `art-ai-go`。fake 启动失败或不健康时旧实例不动，失败报告阶段为 `fake-upstream-failed`。
- 叠加文件的 `environment` 覆盖运行环境文件的同名变量：全部 `*_BASE_URL` 指向 `http://fake-upstream:19000/...`，全部 Provider 与对话模型密钥换成 `fake-test-key`，废弃别名与 `LLM_CONNECTIONS` 清空，并设 `UPSTREAM_SHAPE=fake`、`FAKE_UPSTREAM_ORIGIN=http://fake-upstream:19000`。服务启动时 fail-closed 校验（任一连接未指向 fake、任一密钥不是 `fake-` 密钥、`LLM_CONNECTIONS` 非空即拒绝启动）；`LLM_MODEL` 沿用运行环境文件。
- 成功后 `production/compose.env` 记 `RELEASE_UPSTREAM_SHAPE=fake|live`，`release-env.py smoke` 据此向脚本设 `ART_AI_UPSTREAM_SHAPE`；fake 镜像只保留本次（非强制 `docker rmi`）。以真实形态发布 ephemeral 栈时，成功后移除本项目遗留的 `fake-upstream` 容器与镜像，失败只警告。
- 运行环境文件里的真实密钥只在真实形态读取；fake 形态的容器环境里同名变量已被 fake 值覆盖，但未被覆盖的其他变量仍随 `env_file` 进入容器，不需要真实形态时可以从 `dev-test` 的运行环境文件移除真实密钥。
- 在这一形态上触发 fake 的失败路径（#301）：浏览器、冒烟脚本与 Agent 只接触 Plinth，Plinth 的 Adapter 不带 `X-Fake-*` 头、密钥固定为 `fake-test-key`，fake 又不发布主机端口，所以头与 `fake-<场景>` 密钥两种选场景方式够不到抠图的失败路径。抠图 fake 另从上传图片自带的标记（文件头里的 `fake-file-faults:<故障>[,<故障>...];`，故障词同 `X-Fake-File-Faults`）读该文件的故障：没有控制端点，生产代码没有为此新增转发头或测试分支，不带标记的图片不受影响。`python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-fake-faults.py` 对这一形态断言「部分图片没抠成」提示（#284）与显存不足失败（#299）的公开投影，只对 fake 形态运行；用法、取舍与手工复现见 `scripts/smoke/README.md`「Fake 故障冒烟」。该脚本不是每次发布必跑，改动抠图、fake 故障、成功任务提示或失败文案后跑。

### 全新数据发布（`data: ephemeral`，#28）

`dev-test` 的环境文件写 `"data": "ephemeral"` 后，服务器段（`RELEASE_DATA_MODE=ephemeral`）改走另一条分支，每次发布都是全新数据：

- Compose 在主文件之后、`extra_compose_file` 之前叠加 `docker-compose.go-ephemeral.yml`（缺失或非普通文件即在构建前拒绝）：只保留日志主机挂载，数据库（`/app/data`）、资产存储（`/app/storage`）与 legacy 缓存使用匿名卷；`web-data`、legacy-generated、legacy-assets 不挂载。与 `RELEASE_BACKUP_SCOPE`/`RELEASE_BACKUP_KEEP` 同时出现即拒绝，未改动任何东西。
- 构建前只做 `bootstrap` 容量检查（构建空间与 Docker 根目录余量）；不建 `backups/release-<tag>`、不停机备份、不打 tar。构建、日志目录写检查、legacy 目录准备与 preflight 不变。
- 有旧容器：记录旧镜像标签和旧容器的匿名数据卷名，直接 `compose up -d --no-deps --renew-anon-volumes art-ai-go`；无旧容器（bootstrap）同样带 `--renew-anon-volumes`，且不再因主机 `production/data` 存在数据库而拒绝。
- 失败（候选启动失败、健康失败、清单写入失败）：旧镜像以 `--renew-anon-volumes` 重新拉起，数据同样是全新的，不是数据回滚；失败报告记 `data_mode=ephemeral`（阶段 `candidate-up-failed`、`unhealthy-after-up`，回滚也不健康为 `rollback-also-unhealthy`，回滚启动失败为 `rollback-start-failed`）。
- 成功：写清单与 `compose.env`（记 `RELEASE_DATA_MODE=ephemeral`，不再记备份范围），保留镜像，然后逐个 `docker volume rm` 记录下的旧匿名卷，失败只警告；不调用备份清理。首次从 bind 挂载切换过来时没有旧匿名卷，主机上原 `production/data`、`storage-go` 等目录不被读取也不被删除。
- 验收含义：冒烟与相关验收从空库开始，只存在运行环境 `ADMIN_USERNAME`/`ADMIN_PASSWORD` 引导的管理员账号，数据由测试流程自己创建。

### 发布备份范围与保留（#364）

`dev-test` 只存放测试材料，可在环境文件写 `"backup": {"scope": "database", "keep": 2}`：停机窗口只把 `production/data` 打包到 `backups/release-<tag>/data.tar`，回滚也只替换该目录。`storage-go` 是只追加的 CAS，`web-data` 只读；候选启动后写入的资产或 Web 文件在数据库回滚后保留，可能成为无引用字节。**这不是完整回滚**：发布失败后的旧服务可能看不到候选新增的 CAS/任务数据，只能按新写入处理，不能删除这些字节来伪造一致。

- 省略 `backup` 时保持全量行为：备份 `data`、`storage-go`、`web-data`，且不运行备份清理。`formal-test` 按 #66 配置 `"backup": {"scope": "full", "keep": 2}`：备份范围不变，健康检查与成功清单写入后按最新 2 份清理更早的已结束发布备份（发布备份不含镜像和日志）。
- `release-env.py` 在加载环境文件时拒绝：`scope=database` 用于 `promote_from` 或非 `dev-test` 环境；`scope=full` 用于 `dev-test`、`formal-test` 之外的环境；未知 `scope`。`keep` 为 1–2。服务器段在构建前再次校验 `RELEASE_BACKUP_SCOPE`（`full`/`database`）与 `RELEASE_BACKUP_KEEP`（full 可选，设置时须为 1–2），非法组合不产生副作用。
- 备份范围写入 `backups/release-<tag>/scope`（单行 `database` 或 `full`）、成功后的 `production/compose.env`（`RELEASE_BACKUP_SCOPE`、`RELEASE_BACKUP_TARGETS`）和失败报告的 `backup_scope`/`backup_targets`。手工回滚先读这些记录，不要猜归档内容。
- 只有健康检查与成功清单都写入后，`release-server-steps.sh` 才在设置了 `RELEASE_BACKUP_KEEP` 时调用 `scripts/retain-release-backups.py --apply`：按最新 `keep` 份（当前批准配置为 2）回收更早的已结束发布备份，更早的失败快照也在回收范围内；进行中的快照可临时排除，失败报告和独立诊断日志保留。bootstrap、构建/preflight/备份/更新/健康失败和回滚路径都不调用清理。
- `releases/manifest.txt` 是 append-only 成功历史，可包含同一 tag 与同一完整 SHA 的重复行（成功时间戳不同）。retention 保留原始行序与字节摘要，不重写清单；仍要求最后一行匹配当前运行容器，并拒绝同一 tag 对应不同 SHA 的冲突记录。
- retention 只读取 tag 属于本次候选备份的失败报告；没有对应备份的历史诊断（包括旧正文格式）不再解析，字节保持原样。匹配候选的报告仍校验类型、安全性和正文中的 tag 行，文件名本身不构成完成证明。
- 清理失败只记警告，不改变已成功发布的结论；helper 不得把部分删除报告为成功。它不处理原始资产、数据库、首次迁移冷备或其他栈的备份。
- 保留 database-only 回滚快照可能被使用时，禁止运行离线 CAS/task 破坏性维护；详见 [CAS 存储维护](storage-maintenance.md) 的发布回滚快照一节。

本文只描述脚本合同；实际部署、清理结果与冒烟证据记录在 #364 的 Issue/MR 回执，不由本文宣称已完成。

## 6. 运行配置与冒烟

`formal-test` 的 `production/runtime.env`：整份复制 `dev-test` 的 `runtime.env`（含会话密钥、管理员初始化与 Meshy、Tripo、Hyper3D/Rodin 等全部 Provider Key），只改两类值——中转 Key 改为用户提供的中转 Key，`PUBLIC_BASE_URL`/`NEXT_PUBLIC_APP_URL` 改为该环境的 `base_url`。模型中转地址保持原值：中转按域名路由，其域名已解析到 `formal-test` 主机上的中转，改成字面 IP 会被反代拒绝。不重新生成密钥或管理员。服务器之间直接复制（例如 `scp -3`），不经本地落盘、不回显值，权限 `0600`。修改运行配置后只重建本栈的 `art-ai-go` 服务。

运行模式由 Compose 变量 `GO_APP_ENV` 决定：`docker-compose.go-production.yml` 写的是 `APP_ENV: ${GO_APP_ENV:-production}`，会覆盖运行环境文件里的 `APP_ENV`。`dev-test` 的运行环境文件设 `GO_APP_ENV='test'`；`formal-test` 已接入 HTTPS 域名（#2），`APP_ENV` 与 `GO_APP_ENV` 都设为 `production`。只改 `APP_ENV` 不生效。生产模式启动即校验：拒绝 `AUTH_MODE=development`、`DEV_MOCK_AUTH`、空 `SESSION_SECRET` 与非 HTTPS 的 `PUBLIC_BASE_URL`，并要求持久会话存储。HTTPS 下会话 Cookie 带 `Secure`，冒烟的 `base_url` 必须是 HTTPS 地址，否则登录后被重定向回登录页。

中转按来源 IP 放行。容器访问同机中转时来源是 Compose 项目网络中的容器地址，因此 `formal-test` 用 `extra_compose_file` 固定项目网络子网与容器地址，供中转放行；是否放行及放行范围由用户在中转侧决定。改变网络配置后须对本项目执行一次 `down`/`up` 让网络按新配置重建（只操作本项目）。

**日志（#206）**：`GO_LOG_LEVEL=debug` 由发布脚本固定并写入 `production/compose.env`，测试与生产一致且不提供关闭入口。候选实例启动前记录 `production/logs/art-ai.log` 的行数；healthz/readyz 通过后校验本次启动新增的行包含应用写入的 DEBUG 标记（`marker=debug-log-ready`），文件在发布期间滚动时退化为检查末尾。校验失败按发布失败处理：`ephemeral` 走 `ephemeral_rollback`，持久数据走 `rollback_after_update`，与健康检查失败同级。应用在日志配置完成后写入该标记（`cmd/art-ai/main.go`）；日志滚动仍由运行设置 `LOG_FILE_MAX_MEGABYTES`、`LOG_FILE_MAX_FILES`、`LOG_RETENTION_DAYS` 控制。

**上游调用脱敏录制（#233）**：默认关闭，发布脚本不设置它。出问题时要给维护者上游的结构证据（用来完善 fake），可以在运行环境文件里打开：`UPSTREAM_CAPTURE_DIR=<容器内的绝对路径>`，可选 `UPSTREAM_CAPTURE_MAX_RECORDS`（记录文件条数上限，默认 2000，范围 1–100000）与 `UPSTREAM_CAPTURE_MAX_MEGABYTES`（总大小上限，默认 64，范围 1–4096）；把宿主机上的一个目录挂载到该路径，然后只重建本栈的 `art-ai-go` 服务。启动时目录不存在则以 0700 创建，已存在则收紧为 0700，之后每次上游调用写一个 0600 文件；容器用非 root 运行时，先让挂载目录归该用户所有。目录不可写、是符号链接、不是目录，或与 `STORAGE_ROOT`、`WEB_ASSETS_ROOT`、`LEGACY_GENERATED_IMAGES_ROOT`、`LOG_DIR` 相同、包含或位于其中，设置的上限无效，进程都拒绝启动，不会"以为在录、其实没录"；没设目录时上限变量被忽略。超过条数或总大小上限就删除最旧的记录，占用有界，重启后先按已有文件套用上限；只有 `*.exchange.json` 会被计数或删除，目录里的其他文件不动；也可以随时手工删除记录文件而不影响运行。写文件失败只记一条告警（首次与每千次），业务请求不受影响。这三个变量由 Product Schema 的运行设置声明（默认 Catalog 已声明）；用 `CATALOG_FILE` 换成不含这三项声明的旧 Catalog 时，变量被忽略，录制不会开启。

自部署（根目录的 `docker-compose.yml`，容器根文件系统只读）需要一个可写的挂载。不改仓库里的文件，写一个覆盖文件即可：

```yaml
# docker-compose.capture.yml
services:
  art-ai:
    volumes:
      - ./upstream-capture:/app/upstream-capture
```

在 `.env` 里加 `UPSTREAM_CAPTURE_DIR=/app/upstream-capture`，先让宿主机目录归容器用户所有（`mkdir -m 0700 upstream-capture`，再 `chown` 给容器用户，UID 用 `docker compose run --rm --entrypoint id art-ai -u` 查），然后 `docker compose -f docker-compose.yml -f docker-compose.capture.yml up -d`。目录归属不对时进程拒绝启动并说明原因。

- **记录什么（隐私）。** 每个文件是一次上游调用脱敏后的结构：字段名与类型、HTTP 状态码、错误码、状态枚举词、模型名，值之外的一切都换成占位符。不保存：密钥与鉴权头（只保留 `Content-Type`、`Accept`、`X-API-Version`、`Retry-After`）、URL 里的签名与 query（只记 query 键名）、提示词与对话全文及任何多词文本、媒体与上传文件的字节（只记字节数，换成固定合成文件）、上游地址（一律写成 `example.test`）、用户标识（`id`、`user_id` 等键下的数字写成 0，不透明 ID 换成 `id-N`）。超过 1 MiB 的单个请求或响应、传输错误、未读完与重定向只留一个不含值的跳过标记（原因、连接、HTTP 方法与状态码）。残余风险：用户把一个词写在未被识别为自由文本的字段里时，单个短词会被当作枚举保留。所以仍把目录当作受保护文件：0700 目录、0600 文件，只在受保护环境查看，外发前经 #234 的诊断包导出复扫。
- **如何确认已开启。** 启动日志有一条 INFO「上游调用脱敏录制已开启」，带目录与上限（未开启时只有 DEBUG 级别的「未开启」）；管理员可读 `GET /api/admin/diagnostics`，返回 `upstream_capture` 的 `enabled`、`directory`、`max_records`、`max_bytes`、`records`、`bytes`、`written`、`skipped`、`rotated`、`write_errors`（只有计数与上限，没有任何上游内容；不在 `/api/version` 里）。
- **怎么用。** 记录可直接交给 `go run ./cmd/fake-upstream-align draft -records <记录目录> -out <另一个 0700 目录> -name <名字> [-connection <连接 ID>]`：转成与已提交样本同格式的 `candidate/policy/meta` 草稿，再人工复核、对齐 fake、补回归测试（见 `docs/architecture.md`「可选上游记录与样本草稿」与「可选的上游调用脱敏录制」）。
- **关闭。** 去掉 `UPSTREAM_CAPTURE_DIR` 后重建服务；已写的文件不会被自动删除，用完自行清理。

**Opt-in capture of upstream calls (#233):** off by default. To give the maintainers structural evidence of what the upstream returned, set `UPSTREAM_CAPTURE_DIR=<absolute path inside the container>` in the runtime environment file (optionally `UPSTREAM_CAPTURE_MAX_RECORDS`, default 2000, and `UPSTREAM_CAPTURE_MAX_MEGABYTES`, default 64), mount a host directory there and rebuild only `art-ai-go`. The directory is created or tightened to mode 0700 and every record is a 0600 file; an unusable directory, a symbolic link, or one that overlaps the storage, web-assets, legacy-image or log directories stops the start-up. When a limit is exceeded the oldest records are deleted. A record keeps structure only (field names, types, status codes, error codes, status words, model names): never keys or authorization headers, signed URL queries, prompts or any multi-word text, media bytes, the upstream address or user identifiers. Confirm it from the start-up log or from the administrator-only `GET /api/admin/diagnostics`; feed the files to `fake-upstream-align draft` to turn them into sample drafts. Treat the directory as a protected location all the same.

**导出诊断包（#234）**：出问题要把现场交给维护者时，用同一个程序里的 `art-ai diagnose` 子命令打一个包，不要手工拷日志。它只读：数据库用只读连接打开，日志与录制目录只列出和读取，不启动服务、不迁移、不改任何已有文件，也不覆盖已存在的输出，只新写一个 0600 的 `tar.gz`，所以服务在运行时也能执行。它读的是服务自己的环境变量，数据库、日志、录制目录的位置因此与服务一致；它只读取自己要用的几个设置（`APP_ENV`、`CATALOG_FILE`、`DATABASE_PATH`、`LOG_DIR`、`UPSTREAM_CAPTURE_DIR`），其余设置有问题也不影响它（服务起不来恰恰是最需要诊断包的时候），这几个读不出来时在清单的 `warnings` 里记一个固定词。

```bash
art-ai diagnose --out <文件或已存在的目录> [--since 24h] [--max-bytes 32m]
```

- `--out`（必填）：输出文件；给已存在的目录则在其中按 `plinth-diagnose-<UTC 时间>.tar.gz` 命名。已存在的文件不会被覆盖。
- `--since`：时间范围，默认 `24h`，可写 `90m`、`7d` 或 RFC 3339 时间点；范围到命令开始的时刻为止，对日志、失败任务和录制文件都生效。
- `--max-bytes`：包内全部内容（未压缩）的体积上限，默认 `32m`，范围 `64k` 到 `1g`，可带 `k`/`m`/`g` 后缀。超出时每类从最旧的开始截断（失败任务摘要至多占剩余额度的 1/10，上游录制至多 3/10，其余给日志），清单为每类记下时间范围内共有多少条、包里放了多少条、是否截断。
- 退出码：`0` 成功；`1` 失败；`2` 参数错误；`3` 隐私扫描命中，未写出任何文件。

在容器里运行（输出目录须可写；自部署镜像根文件系统只读，`/tmp` 是可写的 tmpfs），再用 `exec -T … cat` 把包流式取出（`docker cp` 不能复制 tmpfs 里的文件），用完删除：

```bash
docker compose exec art-ai art-ai diagnose --out /tmp/plinth-diagnose.tar.gz
docker compose exec -T art-ai cat /tmp/plinth-diagnose.tar.gz > plinth-diagnose.tar.gz
docker compose exec art-ai rm /tmp/plinth-diagnose.tar.gz
```

不用 Compose 时把 `docker compose exec [-T] art-ai` 换成 `docker exec <容器>`（不加 `-t`）。服务反复重启、`exec` 进不去时，用同一份服务定义起一个一次性容器，把包写到数据卷里再取出（`/tmp` 在容器停止后消失）：

```bash
docker compose run --no-deps --name art-ai-diagnose art-ai diagnose --out /app/data/plinth-diagnose.tar.gz
docker cp art-ai-diagnose:/app/data/plinth-diagnose.tar.gz .
docker rm art-ai-diagnose
docker compose run --rm --no-deps --entrypoint rm art-ai /app/data/plinth-diagnose.tar.gz
```

包里有什么：

| 类别 | 成员 | 内容 | 来源 |
|---|---|---|---|
| 运行信息 `runtime` | `runtime.json` | 运行的提交 revision、Catalog 的 revision 标签与 SHA-256、`APP_ENV` 标签、Go 版本与平台、生成时间 | 构建时链接进二进制的提交；运行所用 Catalog 文档（内置或 `CATALOG_FILE`）的摘要 |
| 设置 `settings` | `settings.json` | Catalog 声明的每个运行设置：名称、可用的环境变量名、是否机密、是否已设置、哪个变量在起作用；仍被设置的已退役变量名。**不含任何值** | Catalog 的 `runtime.configuration`，不是另抄的一份清单 |
| 失败任务 `failures` | `failures.json` | 时间范围内最新的失败任务（至多 200 个）：任务 ID、能力、Offering、失败分类、可否重试、上游 HTTP 状态与错误码、各次尝试的结局、owner 的占位符、截断到 240 字符并再脱敏的上游错误摘录；另有按分类、状态、错误码的计数 | 数据库的只读查询，只读生成任务与尝试两张表；任务输入只用来识别该从包里去掉哪些提示词，不输出 |
| 日志 `logs` | `logs/art-ai.jsonl` | 时间范围内的日志行（含 DEBUG），逐值再脱敏后，按时间顺序，一行一条 JSON | `LOG_DIR` 下的 `art-ai.log` 与滚动文件 |
| 上游录制 `captures` | `captures/*.exchange.json` | #233 的上游脱敏录制，原样（只读取，不改写） | `UPSTREAM_CAPTURE_DIR` |
| 清单 | `manifest.json` | 每类的状态、条数、可用条数、是否截断、字节数、SHA-256、被丢弃的条数；revision、Catalog SHA-256、生成时间、时间范围、体积上限、隐私扫描结论 | |

日志与录制要先开：日志类别需要日志文件，设了 `LOG_DIR` 才有（发布脚本部署的栈已设；自部署默认只写容器输出，在 `.env` 加 `LOG_DIR=/app/data/logs` 后重建，日志写进数据卷，按 `LOG_FILE_MAX_MEGABYTES`、`LOG_RETENTION_DAYS` 滚动）；录制类别需要按上文打开 `UPSTREAM_CAPTURE_DIR`。没开的类别在清单里是 `unavailable`，原因写 `not_configured`；目录不存在是 `not_found`，读不了是 `unreadable`，原因只有这三个固定词，不带路径和错误文本。`docker compose logs` 的输出不经本命令脱敏，不要原样发出。

隐私有两种互补的处理，别混用：

- **脱敏（替换）**：日志行与失败摘录是自由文本，逐个值改写后才放进包。鉴权头、令牌、`sk-` 等密钥形状、签名参数（`X-Amz-Signature`、`token=` 等）与名为 `authorization`、`password`、`session_id`、`prompt` 的字段的值变成 `[redacted]`；URL 变成 `[url]`，IP 变成 `[ip]`，内网主机名变成 `[host]`，邮箱变成 `[email]`；UUID、32 位十六进制标识（用户 ID 的形状）、名为 `owner_id`、`username`、`email` 的字段的值，以及包里出现过的、至少 8 字节的用户标识（含自由文本里重复出现的）变成稳定的占位符 `id-1`、`id-2`……（同一个人在包里始终是同一个占位符）；失败任务输入里的提示词文本，在包内任何位置出现都变成 `[input]`；每个值截断到 1024 字节。保留任务 ID、请求 ID（`req_…`）、Offering ID、状态码、错误码、时间与耗时，这些是定位问题要用的。脱敏后仍被隐私扫描命中的日志行整行丢弃，按规则名计入清单的 `dropped`，不会出现在包里；同样被丢弃并计数的还有读不出时间与级别的行（`unparsable`，含正在写入的半行）、超过 1 MiB 的行（`too_long`）、读不了的日志文件（`unreadable_file`），以及录制目录里不是 0600 常规文件、格式不对或超过 4 MiB 的文件（`unreadable`、`oversize`）。
- **拒绝（不写文件）**：打包前对每个成员按最终要写出的字节再跑一遍独立隐私扫描（`internal/wirecapture/privacyscan`，与 #233 录制器、已提交样本用的是同一份规则）。任何命中，命令都不写文件、退出码 3，只说文件、JSON 路径和规则名，不回显值。录制样本不会被改写（改写过的就不是证据了），所以样本里的任何命中都说明录制或目录被破坏：删去或移走被点名的记录文件后重试。同样，已知的用户标识或提示词仍留在日志与失败摘要里时也拒绝。

残余风险：长度不足 8 字节的用户名只在键名为 `username` 等的字段里被替换，自由文本里的短用户名与普通词没法区分，不替换；任何不在上述键名下的自由文本，只经正则规则处理。所以包仍按受保护文件对待：发出前自己打开 `logs/art-ai.jsonl` 与 `failures.json` 看一眼，只发给维护者，不贴进公开 Issue。

**收到诊断包后的处理流程**（维护者）：

1. 解包到私有目录并核对清单：

   ```bash
   mkdir -m 0700 diag && tar -xzf plinth-diagnose-<时间>.tar.gz -C diag
   cat diag/plinth-diagnose-*/manifest.json
   ```

   先看 `revision` 与 `catalog_sha256` 是不是要排查的版本，`categories` 里有没有 `unavailable`、`truncated` 或 `dropped`（它们限定了能下的结论），`privacy.findings` 必为 0。解出的 `captures/` 是 0700、记录文件是 0600，正是下一步读取的格式。
2. 看失败：`failures.json` 的 `summary` 给出分类、上游状态码与错误码的分布，再用其中的任务 ID、请求 ID 在 `logs/art-ai.jsonl` 里找前后的日志；`settings.json` 告诉你该设置有没有配、走的是不是已弃用的别名。
3. 把录制转成样本草稿：

   ```bash
   mkdir -m 0700 <工作树外的另一个目录>
   go run ./cmd/fake-upstream-align draft -records diag/plinth-diagnose-<时间>/captures -out <另一个 0700 目录> -name <名字> [-connection <连接 ID>] [-offering <Offering ID>]
   ```

   写出与已提交样本同格式的 `candidate`、`policy`、`meta`（见「上游调用脱敏录制」与 `docs/architecture.md`「可选上游记录与样本草稿」）。
4. 对齐 fake：草稿有损（提示词、错误消息、URL 都是占位符），人工复核后，依样本修正 fake 的响应结构、状态机与错误码；接口与 fake 不一致时先对齐 fake、再改实现（`AGENTS.md`「上游接口与 Fake」）。
5. 补回归测试：基于 fake 的回归测试复现这次的失败（用失败摘要里的分类、状态码、错误码核对 Adapter 的错误映射），样本放进对应版本的 `testdata/`，运行 `UPDATE_WIRE_COVERAGE=1 go test ./internal/testutil/wirefixture -run Coverage` 更新覆盖矩阵。
6. 收尾：在对应 Issue 里只记结论与脱敏后的结构；诊断包与解包目录用完即删，不提交进仓库。

**Diagnostics bundle (#234):** `art-ai diagnose --out <file or existing directory> [--since 24h] [--max-bytes 32m]` writes one new 0600 `tar.gz` and nothing else. It is read-only (read-only database connection, directories only listed and read, nothing started, migrated or overwritten) and reads the service's own environment, so it runs beside a live stack with `docker compose exec art-ai art-ai diagnose --out /tmp/plinth-diagnose.tar.gz` followed by `docker compose exec -T art-ai cat /tmp/plinth-diagnose.tar.gz > plinth-diagnose.tar.gz` (`docker cp` cannot read tmpfs). A bundle holds the running revision and Catalog SHA-256, the names (never values) of the Catalog's runtime settings and whether each is set, a summary of the newest failed tasks (failure class, upstream status and code, a short sanitized excerpt), the log lines of the time range scrubbed value by value, and the #233 capture records unchanged, plus `manifest.json` (per category: entries, entries available, whether the size limit cut it, bytes, SHA-256). Two behaviours apply: free text (logs, excerpts) is scrubbed, replacing credentials, URLs, addresses, e-mail addresses, user identifiers and prompts with placeholders and dropping any line the privacy scan still objects to; every member is then scanned again as it will be written, and any finding refuses the whole export (exit code 3, no file, only file, path and rule are reported). Capture records are never rewritten, so a finding in one refuses the export. On receipt: unpack into a private directory, read the manifest, run `go run ./cmd/fake-upstream-align draft -records <dir>/captures -out <another 0700 directory> -name <name>`, review the draft, align the fake, add regression tests, and delete the bundle.

冒烟（`smoke` 从目标环境的 `runtime.env` 读取管理员登录，并设置 `BASE`、`EXPECT_SHA`（容器 OCI revision）`RELEASE_CONTAINER` 与 `RELEASE_ENV`（环境名，验收脚本据此读取同一部署的日志）；不打印密码）：

```bash
python3 scripts/release-env.py smoke <环境> -- bash scripts/smoke/smoke-l0-browser-boot.sh
python3 scripts/release-env.py smoke <环境> -- bash scripts/smoke/smoke-l1-free.sh
python3 scripts/release-env.py smoke <环境> -- bash scripts/smoke/smoke-l1-agent-mode.sh
```

`dev-test` 的 fake 形态发布由 `release-env.py release dev-test` 自动跑上面三条（顺序：L0 → L1 → L1 Agent），逐层留日志并写机器报告，位置与字段见 §4；发布命令在报告写不成或任一层不是 `pass` 时返回非零，但发布不回滚。真实形态（`formal-test` 与 `--live-upstream` 的 `dev-test`）仍按上面三条手动执行：L1 在真实形态会调用真实对话模型，须先取得当次同意（`AGENTS.md`「上游接口与 Fake」），L0 不涉及上游、随时可跑。手动执行时可用 `python3 scripts/release-env.py smoke <环境> -- bash scripts/smoke/<脚本>` 逐层跑，脚本自己的汇总行就是报告读的那一份；机器报告只由发布入口写，手动跑不覆盖它。

**多个对话模型（#322）**：默认对话模型连接仍是运行环境文件里的 `LLM_BASE_URL`、`LLM_API_KEY`、`LLM_MODEL`。要给用户开放更多对话模型，在同一文件加 `LLM_CONNECTIONS=<ID>,<ID>`，并为每个 `<ID>` 显式写 `<ID>_BASE_URL`、`<ID>_API_KEY`、`<ID>_MODEL`（可选 `<ID>_CONTEXT_WINDOW_TOKENS`）；统一网关下每个地址都写同一个网关 URL，不设跨连接的别名或回落。完整规则见 [Provider 配置](provider-configuration.md#多个对话模型连接--multiple-chat-model-connections)。改完只重建本栈的 `art-ai-go` 服务，然后在管理后台「LLM 设置」点「测试连接」核对连通性与读图，再设置默认模型和开放的档位；设置按版本保存在数据库，跨发布保留。

**Multiple chat models (#322):** the default chat connection stays `LLM_BASE_URL`, `LLM_API_KEY` and `LLM_MODEL` in the runtime environment file. To offer more chat models, add `LLM_CONNECTIONS=<ID>,<ID>` to the same file and set `<ID>_BASE_URL`, `<ID>_API_KEY` and `<ID>_MODEL` (optionally `<ID>_CONTEXT_WINDOW_TOKENS`) for each `<ID>` explicitly; with one gateway every address is the same gateway URL, with no alias or fallback between connections. Rules: [Provider configuration](provider-configuration.md#多个对话模型连接--multiple-chat-model-connections). Rebuild only this stack's `art-ai-go` service, then use "Test connection" on the admin page "LLM settings" to check reachability and image input before choosing the default model and the offered levels; the settings are versioned in the database and survive releases.

本地运行时，`smoke-l1-agent-mode.sh` 只能比对 `/api/version` 与 `EXPECT_SHA`；日志文件检查在目标主机运行（设 `ART_AI_SERVER_ROOT=<root>`）或经 SSH 读 `production/logs/art-ai.log` 补核。

### 外部 MCP 监听（#332）

外部 MCP 客户端用本人的个人访问 Key 直连进程里的独立监听（只挂 MCP 路由，不经 Web 服务，也不经反向代理）。接入方的连接、鉴权、工具、Tasks 扩展与示例见 [外部 MCP 使用文档](external-mcp.md)。默认关闭，要同时满足三件事才可达：

1. Schema `application.mcp_public.enabled` 为真（默认 Catalog 为真）。
2. 运行环境文件 `production/runtime.env` 设 `MCP_PUBLIC_LISTEN_ADDR=0.0.0.0:13343`。缺省为空，空就不监听；容器内的地址必须是 `0.0.0.0:13343`，与 `docker-compose.go-production.yml` 固定的容器端口 13343 对应，改成别的端口映射就对不上。
3. 环境文件 `layout` 的两个可选键决定主机一侧：`MCP_PUBLIC_BIND_IP`（IPv4，缺省 `127.0.0.1`，只有本机能连；对局域网开放写主机的局域网地址，`0.0.0.0` 表示所有网卡）与 `MCP_PUBLIC_HOST_PORT`（缺省 13343）。可以只写一个。

两个键都不写的环境发布行为与此前完全一致，`compose.env` 也不出现它们。写了的键由发布脚本与其他 `layout` 键一样导出给 Compose 并写入 `production/compose.env`（手工 Compose 与 `release-env.py compose-env` 用同一份值）；值不是合法的 IPv4 或端口时在构建之前拒绝，端口不能与同一地址上的 `GO_WEB_HOST_PORT` 相同。同一台主机上有两个用这个 Compose 文件的栈时，两栈的 `MCP_PUBLIC_HOST_PORT` 必须不同。隔离预检与仍在运行的栈同时存在，所以预检容器不用栈的 MCP 主机端口，改用回环上的 `CUTOVER_PREFLIGHT_MCP_PORT`（缺省 13342）。

监听没有 TLS，访问 Key 是 Bearer 令牌：只在局域网或本机使用，不得经公网明文传输；要对公网开放，须在进程里配置证书或在前面放 TLS 终结（本仓库不提供）。候选栈的 Compose 文件（`docker-compose.go-candidate.yml`）与自部署的根 `docker-compose.yml` 不发布这个端口。发布后用该 Key 对 `POST /mcp` 调 `server/discover` 与 `get_status`，确认监听与状态块（行为见 [`architecture.md`](architecture.md)「对外 MCP 监听」）。要确认只读检查，可对一个已成功的 3D 任务调 `inspect_model`（`task_id` 必填，`asset_id` 缺省用主模型文件，`purpose` 可选）：它返回模型事实与按制作简报用途或 `purpose` 的逐条判定。抽查 `purpose` 时可对同一任务分别按默认用途与 `purpose=game_character` 各调一次，确认 `usage_check.purpose` 与预算随参数改变、`purpose_source=argument`，未知用途返回 `invalid_arguments` 并列出可用 ID。补救提示（`fix_hint`）只点名 MCP 的 `process_model` 能执行的能力（如 remesh、rigging、format-conversion）；UV、水密、比例、碎片与退化面等 MCP 执行不了的修复项给“没有可用能力”的说明，不再点名 uv-unwrap、resize、repair-printability 或 model-merge。它只支持 GLB，不写自检记录、不计步、不扣费。Catalog 声明了 `inspect_model` 时，`chat.model_review.inspect.limits` 必须同时声明（加载时校验）；进程未装配模型检查器或会话服务时监听启动失败。要确认图片检查，可先调 `upload_image` 再对返回的图片 ID 调 `inspect_image`，或对一个已成功的 2D 任务调 `inspect_image`（`task_id` 必填，`asset_id` 用该任务 `role=image` 的产物）：它返回图片事实与按制作简报用途（或 `purpose` 参数）的清单判定，只支持 PNG/JPEG/WebP（GIF 返回 `image_unreadable`），不写自检记录、不计步、不扣费；用只读 Key 调用前后，同一资产的步数应不变。Catalog 声明了 `inspect_image` 时，`chat.image_review.limits` 的 `max_bytes` 与 `max_pixels` 必须为正数，且 `mcp_public.inspect_image.max_pixels` 不超过 `limits.max_pixels`（加载时校验）；进程未装配图片检查器或会话服务时监听启动失败。要确认状态块的自检结论，对一个已被平台 Agent 审阅过（`chat_self_checks` 有记录）的成功 3D 任务调 `get_task` 或 `get_task_assets`：`inspection` 的 `verdict` 取 `passed`/`issues`/`unchecked`/`uncheckable`，`failed` 只列失败的检查名，`waiver` 与 `checked_at` 一并给出；2D 任务与纯 MCP 任务为 `null`。读取失败只写 `null` 并记服务端日志，调用本身不失败。

**签名下载。** `get_task_assets` 传 `signed: true` 时，监听按 Schema `application.mcp_public.signed_download_path`（缺省 `/mcp/signed-assets/{token}`）签发短时地址，有效期取 `signed_download_ttl_seconds`（默认 300 秒，不超过 `signed_download_max_ttl_seconds`，当前 3600 秒）。签发要求运行环境文件同时配置 `PUBLIC_BASE_URL` 与 `ASSET_SIGNING_KEY`（启动校验；`ASSET_SIGNING_KEY` 不少于 32 字节），只有 `ASSET_SIGNING_KEY` 非空时监听才具备签发能力；签名 key 由 `ASSET_SIGNING_KEY` 按用途派生，与输入资产签名令牌互不通用。没有配置签名密钥时，`signed: true` 返回 `isError` 与错误码 `signed_download_unavailable`，不签发链接；未签名的鉴权下载仍可用。签名地址不要求 `Authorization`、不计入 Key 限流，但同样只走这个监听、没有 TLS：只能在内网或本机使用，不得经公网明文传输。令牌无效、过期、被篡改或所有者不符一律 404；服务端不记录令牌明文，访问日志把令牌脱敏为 `{redacted}`。客户端拿到的绝对地址按请求的 Host 与协议拼成，与鉴权下载一致，不依赖 `PUBLIC_BASE_URL`。

监听按 Schema `access_keys.rate_limit` 限流（每把 Key 每分钟的请求数、每个客户端地址每分钟的鉴权失败次数），计数在进程内，重启清零，只对单实例成立。监听不信任任何代理，不读 `trusted_proxy_cidrs`：客户端地址就是到达容器的 TCP 对端，经 Docker 网关或 NAT 到达的调用方共用一个地址、一起计数。

**升级说明与 Catalog（#332 第 7c-2 步）。** 迁移 0099 只新增索引 `chat_self_checks(owner_id, task_id, updated_at DESC)`，不改表或列，随启动迁移自动应用；它支撑状态块跨会话按任务读取平台自检结论。使用自定义 `CATALOG_FILE` 的部署升级前必须在其 `mcp_public.status_block.schema` 中补上 `inspection`（对象或 `null`，含 `task_id`、`source`、`verdict`、`purpose`、`failed`、`waiver`、`checked_at`）并加入 `required`；缺这个属性时 Catalog 校验失败、进程拒绝启动，默认 Catalog 已包含。

**升级说明：资产清单（#327）。** 迁移 `0100_asset_lists.sql` 新增 `asset_lists` 与 `asset_list_items` 两张表及索引、唯一性与外键约束：`request_key` 唯一、每项目至多一份 `confirmed` 的部分唯一索引、`source_session_id`/`session_id` 的 `ON DELETE SET NULL` 与项目外键 `ON DELETE CASCADE`；不修改旧表、不回填数据，随启动迁移自动应用。未声明 `application.asset_list` 的旧 Catalog 启动后不装配清单服务，四条清单路由不注册，Agent 工具也不会出现（旧 Catalog 本身没有声明 `propose_asset_list`），所以升级后旧 Catalog 仍可启动。使用自定义 `CATALOG_FILE` 并要启用该功能时，须补上 `application.asset_list`，并同时声明 `application.project`、`application.step_policy`（3D 计划步数口径）与四条 `runtime.http.routes` 键；缺项会使 Catalog 校验失败、进程拒绝启动。回滚到旧版本不需要删表：旧版本不读取这两张表，保留的新表不影响启动或既有数据。功能尚未在 dev-test 验收，验收记录见 Issue #327。

**升级说明：清单推进、验收看板与整批交付（#328/#329）。** 本版本序列追加四条迁移，全部随启动自动应用、只追加且不回填数据：`0101_asset_list_runs.sql` 新增 `asset_list_runs`（每份清单至多一行：状态与暂停原因）与 `asset_list_presence`（每个浏览器标签页的心跳），并给 `asset_list_items` 追加 `last_turn_message_id`、`next_action_at`、`block` 三个带默认值的列；`0102_asset_list_item_reviews.sql` 新增只追加的 `asset_list_item_reviews`（每版结果一条通过/打回决定，外键随清单级联删除）；`0103_input_asset_crops.sql` 新增裁图来源表；`0104_asset_list_item_review_writes.sql` 给 `asset_list_items` 追加 `accepted_at`、`return_reason`、`return_note`、`return_count`、`work_round`，给决定表追加 `reasons_json`、`note`，均带默认值。声明 `application.asset_list` 的自定义 `CATALOG_FILE` 必须同时声明 `project_asset_list_board` 路由；声明 `review` 段还须声明 `project_asset_list_item_review`，声明 `review.archive` 时须声明 `project_asset_list_archive`，开启 `execution.enabled` 时须声明 `project_presence`、`project_asset_list_run`、`project_asset_list_item`；缺项会使 Catalog 校验失败、进程拒绝启动。未声明这些段的旧 Catalog 仍可启动，只是对应路由（含看板、验收决定与整批 ZIP）不注册。**回滚注意：** 迁移台账只增不减，当前二进制打开迁移版本高于自身的数据库会直接拒绝启动（错误写明“高于当前二进制”的版本号），所以不要把已由本版本迁移过的数据库交回旧镜像运行；回滚到旧镜像须同时恢复该次发布前的备份（`dev-test` 每次发布是全新数据，直接以旧镜像重发即可）。功能尚未在 dev-test 验收，验收记录见 #329。

**升级说明：访问 Key 用量与整体开关（#332 管理端）。** 迁移 `0105_access_key_usage.sql` 给 `access_keys` 追加 `request_count INTEGER NOT NULL DEFAULT 0`（已有 Key 从 0 开始，不回填历史调用），并新建 `access_key_settings(key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`；服务只写一行 `access_keys.enabled`（`true` / `false`），没有这一行时取 Schema `application.access_keys.enabled`。不修改其它列，随启动迁移自动应用。回滚到旧版本时按 §7 恢复升级前的数据库备份（迁移版本守卫会拒绝打开 migration 版本高于旧二进制的库）；新列与新表本身不需要手工删除，旧版本的查询与插入都显式列出列名，也不会读取它们。使用自定义 `CATALOG_FILE` 时，`application.access_keys.enabled` 缺省是 JSON 布尔零值 `false`；默认 Catalog 显式写 `true`，所以自定义 Catalog 若新增管理视图却没有写 `enabled`，会在管理员手动开启前拒绝所有 Key 请求。要启用管理视图，还须声明 `administration.access_keys`、三条 `runtime.http.legacy_routes`（`admin_access_keys`、`admin_access_key`、`admin_access_keys_switch`）、`administration.access_keys.permission` 引用的已有 permission、四条审计动作（加入 `application.audit_actions`）与 `presentation` 的必需文案；缺项会让 Catalog 校验失败、进程拒绝启动。未声明管理视图的旧 Catalog 不注册管理路由，监听也保持始终开启，升级后仍可启动。功能尚未在 dev-test 验收，验收记录见 Issue #332。

**升级说明：3D 步数结果对象（#330 第 3B 步）。** 本版本起，声明了 `application.step_policy` 的自定义 `CATALOG_FILE`（包括从 3A 升级来的文档）还必须声明 `step_policy.result.schema`：校验要求 `mode`（string）与 `used`（integer）两个属性存在、带说明并列入 `required`，`cap` 与 `remaining`（均为 integer）可声明但不得列入 `required`，`required` 中也不能出现这四个以外的键；缺 `result` 或声明不合规时 Catalog 校验失败、进程拒绝启动。要让生成类工具的结果带上步数对象，还须在该工具的 `result_fields` 中声明 `steps` 键（不能是保留键，也不能与同一工具的其他结果键重名）；不声明仍可启动，只是该工具的结果不带这个对象。默认 Catalog 已全部声明。最小片段：

```json
"step_policy": {
  "result": {
    "schema": {
      "type": "object",
      "description": "本资产在 3D 步数模式下的步数现状；读取不到时不出现这个对象",
      "properties": {
        "mode": {"type": "string", "description": "agent=由 Agent 自己管理，unlimited=不限，cap=有硬上限"},
        "used": {"type": "integer", "description": "本资产已用的 3D 步数"},
        "cap": {"type": "integer", "description": "硬上限模式下的上限；其他模式不出现"},
        "remaining": {"type": "integer", "description": "硬上限模式下的剩余步数；其他模式不出现"}
      },
      "required": ["mode", "used"]
    }
  }
}
```

工具侧只需一个键（其余结果键按各工具原有声明保留）：

```json
"result_fields": {"success": "success", "steps": "steps"}
```

**升级说明：再给几步与步数上限恢复（#330 第 5B/5C 步）。** 迁移 `0106_step_extra_grants.sql` 只追加两列、不回填数据，随启动迁移自动应用：`go_chat_sessions.extra_steps INTEGER NOT NULL DEFAULT 0`（每个资产——一个会话——累计追加的 3D 步数，清单子会话与外部 MCP 资产共用）与 `asset_list_items.step_cap_turn_message_id TEXT NOT NULL DEFAULT ''`（runner 在被步数上限挡住的那一轮记下 assistant 消息 ID，用户追加步数或继续后从新一轮接着做，而不是重判同一轮）。不修改 `updated_at`，不影响会话列表排序。回滚到旧版本时按 §7 恢复升级前的数据库备份（迁移版本守卫会拒绝打开 migration 版本高于旧二进制的库）；两列本身不需要手工删除，旧二进制显式列出列名、不会读取它们。使用自定义 `CATALOG_FILE` 时，若声明 `application.step_policy`，须补上 `step_policy.extra_steps`（`{default, maximum, total_maximum}`，要求 `1 <= default <= maximum <= total_maximum`）、三条文案 `messages.extra_steps_limit`（含 `{maximum}` 与 `{extra}`）、`messages.extra_steps_single_limit`（含 `{maximum}`）、`messages.extra_steps_unavailable`、审计声明 `audit.extra_steps_action` 与 `audit.extra_steps_resource_type`，并把该动作加入 `application.audit_actions`（带中英文标签）；开启 `asset_list.execution.enabled` 时还须声明 `runtime.http.routes.project_asset_list_item_steps`（参数恰好为 `{projectID}`、`{listID}`、`{itemID}`）。外部 MCP 资产会话的「再给几步」还需要可选路由 `runtime.http.routes.chat_session_steps`（参数恰好为 `{sessionID}`；缺省时该入口不注册，清单项入口不受影响）。缺必需项会让 Catalog 校验失败、进程拒绝启动；默认 Catalog 已全部声明。第 5B/5C/6 步的界面、路由与调度器行为尚未在 dev-test 验收，记录见 Issue #330。

## 7. 冒烟失败后的手工回滚

自动回滚只覆盖发布脚本内的失败。冒烟失败且已有前一版本时（首次 bootstrap 版本没有前一版本，只能停止服务并保留现场）：

**手工恢复必须在发布锁内执行。** 同一发布根的发布备份/恢复、备份清理与手工恢复共用 `<root>/.release.lock`；`release-server-steps.sh` 在构建或改动数据前获取，`retain-release-backups.py` 校验并继承同一描述符。手工恢复若不加锁，可能和清理或另一次发布并发移动同一目录。用 `release-lock.py run` 把整个“校验 → 停止 → 移动/解包 → 启动”序列放在一个锁会话中。锁通过 `RELEASE_LOCK_FD`/`RELEASE_LOCK_ROOT` 继承：在锁会话内再调用 `release-lock.py run` 会复用同一描述符，不会重复加锁或死锁。继承描述符先通过根、常规文件、owner 和路径身份检查，再直接 `flock LOCK_EX|LOCK_NB`：共享同一 open file description 时复用已持有锁；独立描述符在锁空闲时先取得锁再执行操作，遇到其他进程持锁则报 busy。借用方不得解锁继承描述符，由最外层持有者在结束时释放。没有 `/proc` 锁表回退或环境变量绕过；helper 缺失或身份不符即失败关闭。

```bash
ssh <ssh>
cd <root>
tail -2 releases/manifest.txt     # 末行为失败版本 T，上一行为回滚目标 P 及其 SHA
python3 app/scripts/release-lock.py run --root "$PWD" -- bash -s <<'RECOVERY'
set -euo pipefail
T=<失败版本标签>
P=<回滚目标标签>
P_SHA=<回滚目标完整SHA>
C="docker compose --env-file production/runtime.env --env-file production/compose.env -p <compose_project> -f app/docker-compose.go-production.yml -f <extra_compose_file>"
scope_file="backups/release-$T/scope"
if [ -e "$scope_file" ] || [ -L "$scope_file" ]; then
  [ -f "$scope_file" ] && [ ! -L "$scope_file" ] ||
    { echo "invalid backup scope file: $scope_file" >&2; exit 1; }
  scope=$(cat "$scope_file") ||
    { echo "cannot read backup scope: $scope_file" >&2; exit 1; }
  case "$scope" in
    database) archives=(data.tar) ;;
    full) archives=(data.tar storage-go.tar web-data.tar) ;;
    *) echo "unknown or malformed backup scope: $scope" >&2; exit 1 ;;
  esac
else
  # A legacy snapshot has no scope file; only a complete set is a restore point.
  scope=""
  archives=(data.tar storage-go.tar web-data.tar)
fi
for archive in "${archives[@]}"; do
  path="backups/release-$T/$archive"
  [ -f "$path" ] && [ ! -L "$path" ] && [ -s "$path" ] ||
    { echo "backup archive missing, empty or not a regular file: $path" >&2; exit 1; }
  tar -tf "$path" >/dev/null ||
    { echo "backup archive is unreadable: $path" >&2; exit 1; }
done
$C stop art-ai-go
mkdir -p "backups/release-$T/failed"
mv production/data "backups/release-$T/failed/"
if [ "$scope" = database ]; then
  tar -C production -xf "backups/release-$T/data.tar"
else
  mv production/storage-go production/web-data "backups/release-$T/failed/"
  for d in data storage-go web-data; do tar -C production -xf "backups/release-$T/$d.tar"; done
fi
GO_IMAGE_TAG=$P GO_SOURCE_COMMIT=$P_SHA $C up -d --no-deps art-ai-go
curl -fsS <base_url>/readyz
RECOVERY
```

- `$PWD` 必须是规范化绝对发布根（进入 `<root>` 后即是）；脚本拒绝相对路径、`..` 或符号链接根。
- scope 先校验：文件存在时，以上 Bash 示例去除末尾换行后的值须为 `database` 或 `full`；未知、空、含额外非空行、符号链接或其他类型立即拒绝。自动清理 helper 另要求文件恰好为单行，不接受额外空行。文件缺失时是旧快照，只有 `data.tar`、`storage-go.tar`、`web-data.tar` 三个归档都存在、可读、非空且 `tar -tf` 通过才按 `full` 恢复。
- 每个必需归档在 `$C stop` 和任何 live 目录移动之前完成存在、普通文件、非空和 `tar -tf` 校验；任一失败立即退出，不动服务或数据。`database` 只要求并恢复 `data.tar`，保留候选写入的 `storage-go`/`web-data` 字节；`full`（或通过上述校验的旧快照）恢复全部三个目录。
- `backups/release-<T>/` 是发布 T 之前（P 运行时）的停机备份。不完整或失败的快照（零字节、缺失归档、`tar -tf` 失败）只具备清理资格，不具备恢复资格；不要用失败报告推断归档可恢复。
- 后给的 `--env-file` 覆盖先给的；没有 `extra_compose_file` 的环境去掉最后一个 `-f`。尚未写出 `compose.env` 的环境（例如本变更后首次发布前的 `dev-test`），先在本地用 `python3 scripts/release-env.py compose-env <环境>` 生成同样内容。

删除或清理备份前，helper 还会拒绝快照 `failed/`、`restore/` 等树内出现的任何挂载点（包括与父目录同设备的 bind mount），并拒绝无法读取或格式异常的挂载表；这防止递归删除经由挂载别名进入 live 目录。

`scope` 为 `database` 只把数据库恢复到 P；候选写入的 `storage-go`/`web-data` 字节保留，可能成为孤儿。不要把它们当作已恢复数据，也不要在持有该快照时运行离线 CAS/task 破坏性维护（见 [CAS 存储维护](storage-maintenance.md)）。回滚后在对应 Issue 记录原因、证据与 scope；如需要完整恢复，先停止写入并对 DB、CAS 与 web-data 做新的全量一致备份，再按评审方案处理。

## 8. 未定事项

未决：中转是否放行 `formal-test` 项目网络的固定来源地址（未放行前该环境的 Agent 对话请求被中转以来源 IP 拒绝）；`formal-test` 的 Meshy Webhook；去背景等跨主机依赖是否在 `formal-test` 主机另行部署；`dev-test` 验收通过后是否自动晋级。

## 9. dev-test 栈概况

- 入口：dev-test 的 `base_url`（`python3 scripts/release-env.py show dev-test`；地址只在未纳入 Git 的
  `deploy/local/environments.json`）
- Compose 项目：环境文件 `dev-test` 条目的 `compose_project`
- 运行入口：`cmd/art-ai`
- Product Schema 真源：`internal/catalog/default_catalog.json`
- 数据库：`production/data/art-ai.db`
- CAS：`production/storage-go`

该 Compose 项目只是测试主机上的生产同构项目，不代表正式生产已接管（Next → Go 迁移沿革见 [`history.md`](history.md)）。
这里的旧 Next 容器和旧数据目录停止、封存且不删除；dev-test 的普通发布
不得重跑 ETL，也不得改写 `production/migration-ready.txt`。

## 10. 正式生产迁移

目标生产运行时是纯 Go 单容器。正式生产尚未迁移；正式停机迁移的唯一入口是
[production-migration-runbook.md](production-migration-runbook.md)。Go 运行机制和 dev-test
测试演练记录见 [README-go.md](../README-go.md)，发布与验收要求见 [dev_ops.md](../dev_ops.md) 和
[dev_test.md](../dev_test.md)。Agent 不直接访问正式服务器；现场第一步运行只读盘点脚本
`scripts/collect-production-migration-inventory.sh`。

## 11. 测试主机 CI（#97）

`dev-test` 主机上另有一个专用于门禁的 CI 目录和一次性容器，用法与规则见 [`dev_test.md`](../dev_test.md)「主机 CI」。这里只记运维事实，主机与路径只在本地环境文件里：

- **入口**：`python3 scripts/host-ci/run.py <sha|HEAD>`，目标主机取环境 `dev-test` 的 `ssh`；CI 目录取该条目的可选字段 `"host_ci": {"dir": "<绝对路径>"}`（模板见 `deploy/environments.example.json`），缺省 `/var/lib/plinth-ci`。它必须与任何环境的 `root` 和 `protected_paths` 分开，`run.py` 会拒绝重叠。`release-env.py` 不读这个字段。
- **目录**：`<CI 目录>/.lock`（槽位 0）、`.lock-<k>`（槽位 k，仅在 `slots` > 1 时用到）、`.maint.lock`（构建与保留的短时维护锁）、`run/<run id>/`（本次 bundle 与工作目录，结束即删）、`results/<run id>/`（结果 JSON、容器日志、重试证据，保留最近 10 次）。
- **对象**：容器带标签 `plinth.host-ci=1`、`plinth.host-ci.run=<run id>`、`plinth.host-ci.slot=<k>`，名称 `plinth-ci-<run id>-aN`；镜像名 `plinth-ci:<12 位 hash>`，保留最新 2 个。与 Plinth 栈和同机其他业务的 Compose 项目、容器、卷、镜像互不相干；它们不在这套清理的范围内。
- **开跑条件**：`MemAvailable` ≥ 11 GiB（容器上限 10 GiB 加 1 GiB 余量，可配置）加上其他正在运行的 host-ci 容器还可能占用的余量，CI 目录与 Docker 根目录所在磁盘各 ≥ 10 GiB 空闲。同机其他业务忙时可能不满足，此时工具以退出码 70 停下，不会强行启动；这不是故障，等资源恢复后重跑。运行期间宿主 `MemAvailable` 若跌破 2 GiB 下限，工具会杀掉自己的容器并以退出码 70、状态 `host_memory_low` 结束（该保护用于避免共用主机出现主机级 OOM，见 `docs/history.md`）。并行槽位默认 1，`slots` 调大（最多 4）是显式选择，只应在已记录的 `memory_peak_bytes` 和主机剩余内存足以同时容纳多个运行时才这样做。
- **手工排障**：只读查看 `docker ps -a --filter label=plinth.host-ci=1` 与 `<CI 目录>/results/`。需要清理时只删带这个标签的容器和 `<CI 目录>/run/` 下的子目录；下一次运行持锁后也会自动清理遗留。不要用 `docker system prune`、`docker image prune` 或通配符删除。
- **工作流重放（#86）**：`python3 scripts/host-ci/run.py <sha> --workflows` 复用同一目录、同一套槽位锁与同一预检，用固定版本的 act（`<CI 目录>/tools/act-<版本>/act`，首次使用时下载并校验发布包摘要）和按摘要固定的运行器镜像 `catthehacker/ubuntu:act-24.04` 重放 `pull_request` 与 `push` 事件，作业容器同样带标签 `plinth.host-ci=1`、限 4 CPU / 10 GiB；`<CI 目录>/act-cache` 是 Action 缓存。这是本地模拟，不是 GitHub self-hosted runner，主机不接受平台事件。与门禁模式的差别：作业需要联网。两种运行结束后，本机用 `gh` 登录对被测 SHA 写 commit status `host-ci/gates` 与 `host-ci/workflows`（`--no-status` 可关）。排障时多出的只读检查：`docker ps -a --filter label=plinth.host-ci=1` 同样能看到 act 的作业容器；act 创建的 `act-` 前缀卷和网络在运行结束时按“本次新增”清理，不要用通配符手工删除。
- **定位**：GitHub Actions 不可用期间，本项目的 CI 替代（沿用 #79 的裁定），同时是本地的干净复现环境；镜像定义在 `Dockerfile.ci`，入口 `scripts/host-ci/entry.sh`，见 `dev_test.md`「主机 CI」。

## 内部运维资产（自部署不需要）

以下文件只服务于内网测试/正式栈和历史 Next→Go 迁移（#259），不在自部署路径里，
自部署者可以忽略：

- Compose：`docker-compose.go-production.yml`（`dev-test` 与 `formal-test`）、
  `docker-compose.go-candidate.yml`（历史候选栈）。
- 发布与验收：`scripts/release-*`、`scripts/retain-art-ai-images.py`、`scripts/smoke/`、
  `scripts/cutover-go-production.sh`、`scripts/finalize-go-production.sh`、
  `scripts/backup-go-production.sh`、`scripts/deploy-go-candidate.sh` 与相应
  `scripts/test-*` 脚本、`dev_ops.md`。
- 历史迁移与有界维护：`cmd/art-ai-etl`、`internal/etl`、`cmd/recover-existing-assets`、`cmd/complete-conversion-delivery`、
  `scripts/run-go-etl-container.sh`、`scripts/map-go-production-env.sh`、
  `scripts/collect-production-migration-inventory.sh`、
  `scripts/generate-production-migration-artifacts.py`、
  `scripts/local-migration-rollback-drill.sh`、`scripts/verify-cutover-source.py`。
  这些迁移与维护命令只打进内部镜像目标 `runtime-with-migration-tools`；
  `Dockerfile.go-runtime` 的默认目标 `runtime`（自部署所用）不构建、不包含它们。
  服务内保留的 legacy 兼容读取（旧生成图、旧资产代理）在自部署中没有挂载目录，
  不影响运行，1.0 之后单独删除。

这些文件暂留原路径：内网发布与回滚脚本按固定路径读取它们（正式栈回滚会从旧版本
代码快照里按原路径找 Compose 文件），移动需要同时改造回滚逻辑，另行处理。

## 发布公开快照（#261）

公开仓库 `LeXwDeX/Plinth` 起始于一个不带内部开发历史的单根快照提交（见 [`history.md`](history.md)「编号对照」）；它是唯一仓库，提交直接在其上进行，日常开发不生成快照。下面的步骤只在需要从内部存档重建公开起点时使用；快照工具只在本地生成，不推送，替换公开仓库已有内容或强推前须由用户当场确认。日常开发中，同一扫描以 `dev_test.md` §2「public tree」门禁在每次推送前运行：每个推送的提交都会立即公开。

1. 选定源 revision：已合入 `main`、通过 `dev_test.md` §2 门禁和开源 1.0 验收（#260）的精确 SHA。
2. 先检查：`scripts/check-public-tree.sh <SHA>`（日常开发中同一扫描以 `dev_test.md` §2 第 9 项门禁「public tree」在 pre-push 与 CI 里对工作树运行）。它用 `git archive` 取出该 revision 的已跟踪文件，扣除 `scripts/public-snapshot.exclude` 列出的路径后逐个扫描（文本与二进制都扫）：私有网段前缀、内部主机与域名、服务器路径、本机临时路径、内部 Compose 项目名前缀、组织名称、本机用户目录（公开 CI runner 的家目录除外）、私钥头，以及 GitLab、OpenAI 式、GitHub、Meshy、Tripo 的凭据前缀（只匹配凭据长度，测试里的短假值不算，#334）；本机存在 `deploy/local/environments.json` 时，其中每个主机、地址、根目录、布局路径、Compose 项目与镜像源主机也作为字面量加入；还可以用 `--pattern`、`--patterns-file` 或未纳入 Git 的 `deploy/local/public-snapshot-patterns.txt`（每行一个正则，`#` 开头为注释）补充不想提交的私有词。有任一命中即以非零退出并列出 `文件:行`。
3. 生成：`python3 scripts/make-public-snapshot.py make --rev <SHA> --version v1.0.0 --out <仓库外的新目录> [--tag]`。工具在同级临时目录导出、排除、扫描，扫描失败时不留下任何目录；通过后 `git init -b main`，以本机 `git config user.name/user.email` 为作者和提交者生成唯一提交（提交信息写版本和源 SHA），`--tag`（可选，仅供后续版本；首次发布不创建 tag/Release）时在本地打同名附注 tag。新仓库没有 remote。
4. 在快照目录里按 README 构建（`go build ./...`、`pnpm install --frozen-lockfile` 与 `pnpm build`）并复查 `python3 scripts/make-public-snapshot.py check --tree <快照目录>`，再进入人工确认的发布步骤。

`dev_ops.md`、内部 Compose 文件和发布/冒烟脚本都在公开仓库里，因此它们必须保持干净：环境细节只在本地环境文件；构建与 CI 都只用公共 npm `registry.npmjs.org`（#257，不再有内网镜像覆盖）。仓库里的守门测试与扫描器本身用拼接或字符类书写被拒绝的标识，不含明文，因此扫描不设白名单。截图等二进制文件也按字节扫描。
