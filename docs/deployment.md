# 部署入口

本文是 Plinth 内部环境部署的入口。用户在仓库根目录说「部署到 <环境>」（例如「部署到正式测试环境」）时，Agent 按本文执行。本项目开源：主机、地址、端口、目录与 Compose 名只写在未纳入 Git 的本地部署文件夹 `deploy/local/`，本文和其他仓库文档只用环境名。密钥只在各环境服务器的 `production/runtime.env`（`0600`）中，不进本地环境文件、仓库、Issue 或报告。

自部署（开源用户）不使用本文，见 `README.md` 的 `docker compose` 一条命令部署。

## 1. 环境

| 环境名 | 用途 | 接收的 revision | 数据 | 发布后验收 | 付费生成 |
|---|---|---|---|---|---|
| `dev-test` | 开发测试环境 | 每次已推送的 `origin/main` | 历史迁移与测试数据，保留不清理 | L0、L1、L1 Agent 必跑；涉及 Agent/生成/同意链路时按 `scripts/smoke/README.md` 跑 L2 等真实模型验收 | 常设小额授权（见 `AGENTS.md`） |
| `formal-test` | 正式测试环境（测试版本中的稳定版）：单版本、不留残余 | 只接收按 §4 晋级规则通过的精确 revision | 本栈独立，不跨主机复制 | L0、L1、L1 Agent | 不做；测试期付费同意授权不适用 |

2026-10-20 的环境布局由用户决定（#270）：`dev-test` 不变，新增 `formal-test`。与 `dev-test` 同主机的 `legacy-formal`（内部存档 #232 的隔离正式栈）经维护者 2026-10-30 决定已下线（GitHub #74）：Compose 项目、容器、专用镜像与 `<root>/formal/` 数据已移除，最终备份保留；环境名不再可用，说明见 `dev_ops.md`「dev-test 主机与镜像保留」。

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
| `backup` | 仅 `dev-test` 可选的发布备份策略（#364）：`{"scope": "database", "keep": 2}` 表示停机时只备份 `production/data`。正常成功清理最多保留最新 2 个已结束发布备份；进行中的快照可临时排除，但不承诺长期额外保留；更早的失败快照也按最新 2 个回收，独立失败报告与诊断日志保留。省略时为全量备份 `data`、`storage-go`、`web-data`，且不自动清理历史备份（`formal-test` 保持此默认）。`scope` 只接受 `database`，`keep` 为 1–2；带 `promote_from` 的环境不得设置 |
| `promote_from` | 只能从该环境晋级（`formal-test` 为 `dev-test`） |
| `single_stack` | 单项目主机：`image_keep`（保留镜像数，含运行镜像）、`keep_source_snapshots`（保留的 `app.prev-*` 数）、`allow_bootstrap`（允许空栈首次启动） |
| `layout` | Compose 主机路径与绑定地址（`GO_WEB_BIND_IP`、`GO_WEB_HOST_PORT`、`GO_PROD_*_DIR`、`GO_LEGACY_*_DIR`）；数据目录必须是 `<root>/production/<名称>`，未写时按此导出；legacy 目录没有缺省值，必须写明：单栈环境在 `<root>/production/` 下以 `legacy` 开头，共享主机（`dev-test`）可为发布根之外不含 `..` 的绝对路径 |
| `extra_compose_file` | 追加在主 Compose 文件之后的服务器本地覆盖文件（`<root>/production/` 下，例如固定项目网络子网与容器地址）；只用于本栈服务，隔离预检不用它 |
| `build` | `network`（`docker build --network`，例如构建容器须以宿主源地址访问内网镜像源时为 `host`）、`goproxy`（GOPROXY 构建参数）、`add_hosts`（`docker build --add-host` 的 `主机名:地址` 列表，用于解析内网 Go 代理等构建依赖主机；npm 固定走公共 registry）；npm 源固定为公共 `registry.npmjs.org`（#257），旧文件里的 `npm_registry` 会被拒绝，需删除 |
| `source_transfer` | `pipe`（缺省，`git archive` 经 SSH 管道解包）或 `scp`（本地生成 tar，scp 到 `<root>/releases/` 解包后删除） |
| `managed_by` | 已移除（#74，`legacy-formal` 下线）：环境文件里出现这个字段会被拒绝，并提示删除 |
| `acceptance` | 晋级验收报告所在的 GitHub 仓库 `forge_repo`（`owner/repo`）与 Issue 编号：`issue` 供 `formal-test`（旧字段 `legacy_formal_issue` 随 `legacy-formal` 下线不再使用，可从环境文件删除）；本仓地址 `forge_url`（验收证据 URL 必须以它开头）。`--acceptance-note` 传该 Issue 下某条 comment 的数字 ID，脚本用 `gh api repos/<forge_repo>/issues/comments/<ID>` 读取，并拒绝属于其他 Issue 的 comment |

查看：`python3 scripts/release-env.py list`、`python3 scripts/release-env.py show <环境>`。

## 3. Agent 工作流

前提：要发布的提交已推送到 `origin/main`；本机门禁按 `dev_test.md` §2 通过。

**部署到开发测试环境（`dev-test`）**

1. `python3 scripts/release-env.py release dev-test`（同步 → 构建 → preflight → 备份 → Compose 更新 → 健康 → 失败自动回滚）。
2. 核对容器 OCI revision 等于 `origin/main` 完整 SHA（`release-env.py smoke` 设置的 `EXPECT_SHA` 即读自容器标签，L1 Agent 冒烟同时比对 `/api/version`）。
3. 冒烟（§6），以及本次改动相关的页面、API 与 Agent/生成链路验收。
4. 全部 PASS 后，在 `acceptance.issue` 写入一条 §4 格式的验收报告 note；记下 note ID。

**部署到正式测试环境（`formal-test`）**

1. 确认目标 revision 在 `dev-test` 已按上面第 1–4 步稳定通过，并取得验收报告 note ID。不满足就停止并告诉用户缺什么，不降级验收。
2. `python3 scripts/release-env.py release formal-test --acceptance-note <noteID>`；不给 `--revision` 时取 `dev-test` 当前运行的 revision，也可给一个更早的已验收完整 SHA。
3. 冒烟（§6），核对 revision。
4. 在 #270 记录部署 SHA、冒烟结果与异常；不写回本文。

失败时按 §5 的自动回滚结果和报告处理；冒烟失败按 §7 手工回滚，记录证据后回到本地修复，并重新经过 `dev-test` 验收再晋级。

## 4. 晋级规则与验收报告

用户规则（2026-10-20）：开发测试环境（包括 Agent 测试）全部稳定通过以后，才发布到正式测试环境。`release-env.py` 对有 `promote_from` 的环境强制执行，任一项不满足即拒绝且不联系目标主机：

- revision 是 40 位小写完整 SHA，且是 `origin/main` 的祖先；
- 源环境的成功发布清单（`<root>/releases/manifest.txt`）中有该 revision 与报告中的同一镜像标签；
- `acceptance.issue` 下指定的 comment 中恰有一个如下 JSON fenced block，四项检查都是 `pass`、`failed: 0`、`passed > 0`、`skipped: 0`、`soft_failed: 0`，每项证据为本仓库内的完整 URL（校验函数在 `scripts/release-promotion.py`，`release-env.py` 加载，`scripts/tests/test_release_promotion.py` 覆盖）：

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

## 5. 发布流程

两个环境走同一套脚本，`release-env.py` 只把环境参数交给 `scripts/release-go-testenv.sh`，服务器段为 `scripts/release-server-steps.sh`：

1. 源码：远端旧 `app/` 移为 `app.prev-<时间>`，`git archive <revision>` 解到新 `app/`。
2. 服务器段：获取发布根锁 → 容量检查 → 以 `Dockerfile.go-runtime` 的 `runtime-with-migration-tools` 目标构建 `art-ai-go-production:<短SHA>-<时间>`（npm 固定公共源；按环境文件传 `GOPROXY`/`--add-host`）→ 日志目录以镜像运行身份写删验证 → `cutover-go-production.sh preflight`（回环端口、临时目录）→ 停旧服务做一致性备份（`backups/release-<tag>/`；默认备份 `data`、`storage-go`、`web-data`，只有 `dev-test` 可配置 `backup.scope=database` 仅备份 `production/data`）→ Compose 更新唯一 `art-ai-go` 服务 → healthz/readyz → 写成功清单 → 镜像保留与（仅 `dev-test` database 模式的）备份保留。
3. 失败：构建/预检失败不动旧实例；更新后健康失败先按备份范围恢复数据，再以发布前实际运行的镜像拉起，报告在 `production/reports/release-failures/`；失败路径不清理镜像和快照。整个服务器段持有发布根锁；清理 helper 继承并校验该描述符。`database` 模式只恢复数据库，候选写入的 `storage-go`/`web-data` 字节保持不动，因此不是完整回滚。

仓库不写死任何主机、地址、服务器路径或环境的 Compose 项目名（#249、#261）：`release-go-testenv.sh` 的 `RELEASE_HOST`/`RELEASE_ROOT`、服务器段的发布根与 `RELEASE_COMPOSE_PROJECT`（`release-env.py` 总是按 `compose_project` 传入）、镜像保留的 `--root`/`--project`、预检的 `GO_PROD_PROJECT` 和 `docker-compose.go-production.yml` 的 8 个主机路径都没有缺省值，一律来自环境文件。`layout` 中的键作为进程环境传给每次 Compose 与预检；设置了 `layout` 的环境成功后写出 `production/compose.env`（`0600`，无密钥：当前 `GO_IMAGE_TAG`、`GO_SOURCE_COMMIT`、`GO_RUNTIME_ENV_FILE` 与布局键），供手工 Compose 使用。

`single_stack` 环境的差异（服务器段收到 `RELEASE_SINGLE_STACK=1`）：

- 镜像只保留运行镜像和本次回滚镜像（`--keep 1`），源码快照只留最新一份 `app.prev-*`；
- 本项目没有容器且没有数据库时，允许空栈 bootstrap 首次启动，失败则停止候选并保留现场；有数据库而无容器时拒绝；
- 健康检查访问 `GO_WEB_BIND_IP:GO_WEB_HOST_PORT`。

与是否单栈无关、按环境文件设置的选项：`extra_compose_file` 加到每条本栈 Compose 命令的主文件之后（文件须已在服务器上，越界、缺失或符号链接即在构建前拒绝）；`build` 的 `network`、`goproxy` 与 `add_hosts` 只改变 `docker build` 参数，npm 源固定为公共 `registry.npmjs.org`（#257），镜像目标仍为 `runtime-with-migration-tools`；`source_transfer: scp` 只改变源码同步方式。`compose.env` 记录 `ART_AI_PROTECTED_PATHS`（#334）与布局变量。

`dev-test` 没有 `single_stack`：它在共享主机上，环境文件用 `protect_projects` 列出同机其他 Plinth Compose 项目（候选栈等），`release-env.py` 以 `RELEASE_PROTECT_PROJECTS` 传给服务器段，镜像保留对每个项目加 `--protect-project`，这些项目容器引用的镜像不被清理；环境文件还为它给出 `build`（Go 代理与 `--add-host`）和 `layout` 中的 4 个 legacy 目录。源码同步与服务器段命令序列与参数化之前相同，只多了上述项目参数（`scripts/smoke/test-release-stack.sh` 断言）。

### 发布备份范围与保留（#364）

`dev-test` 只存放测试材料，可在环境文件写 `"backup": {"scope": "database", "keep": 2}`：停机窗口只把 `production/data` 打包到 `backups/release-<tag>/data.tar`，回滚也只替换该目录。`storage-go` 是只追加的 CAS，`web-data` 只读；候选启动后写入的资产或 Web 文件在数据库回滚后保留，可能成为无引用字节。**这不是完整回滚**：发布失败后的旧服务可能看不到候选新增的 CAS/任务数据，只能按新写入处理，不能删除这些字节来伪造一致。

- 省略 `backup` 时保持全量行为：备份 `data`、`storage-go`、`web-data`，且不运行备份清理。`formal-test` 保持此默认。
- `release-env.py` 在加载环境文件时拒绝把 `backup` 用于 `promote_from` 或非 `dev-test` 环境；`scope` 只接受 `database`，`keep` 为 1–2。服务器段在构建前再次校验 `RELEASE_BACKUP_SCOPE`（`full`/`database`）与 `RELEASE_BACKUP_KEEP`，非法组合不产生副作用。
- 备份范围写入 `backups/release-<tag>/scope`（单行 `database` 或 `full`）、成功后的 `production/compose.env`（`RELEASE_BACKUP_SCOPE`、`RELEASE_BACKUP_TARGETS`）和失败报告的 `backup_scope`/`backup_targets`。手工回滚先读这些记录，不要猜归档内容。
- 只有健康检查与成功清单都写入后，`release-server-steps.sh` 才在 database 模式调用 `scripts/retain-release-backups.py --apply`：按最新 `keep` 份（当前批准配置为 2）回收更早的已结束发布备份，更早的失败快照也在回收范围内；进行中的快照可临时排除，失败报告和独立诊断日志保留。bootstrap、构建/preflight/备份/更新/健康失败和回滚路径都不调用清理。
- `releases/manifest.txt` 是 append-only 成功历史，可包含同一 tag 与同一完整 SHA 的重复行（成功时间戳不同）。retention 保留原始行序与字节摘要，不重写清单；仍要求最后一行匹配当前运行容器，并拒绝同一 tag 对应不同 SHA 的冲突记录。
- retention 只读取 tag 属于本次候选备份的失败报告；没有对应备份的历史诊断（包括旧正文格式）不再解析，字节保持原样。匹配候选的报告仍校验类型、安全性和正文中的 tag 行，文件名本身不构成完成证明。
- 清理失败只记警告，不改变已成功发布的结论；helper 不得把部分删除报告为成功。它不处理原始资产、数据库、首次迁移冷备或其他栈的备份。
- 保留 database-only 回滚快照可能被使用时，禁止运行离线 CAS/task 破坏性维护；详见 [CAS 存储维护](storage-maintenance.md) 的发布回滚快照一节。

本文只描述脚本合同；实际部署、清理结果与冒烟证据记录在 #364 的 Issue/MR 回执，不由本文宣称已完成。

## 6. 运行配置与冒烟

`formal-test` 的 `production/runtime.env`（用户 2026-10-20 决定）：整份复制 `dev-test` 的 `runtime.env`（含会话密钥、管理员初始化与 Meshy、Tripo、Hyper3D/Rodin 等全部 Provider Key），只改两类值——中转 Key 改为用户提供的中转 Key，`PUBLIC_BASE_URL`/`NEXT_PUBLIC_APP_URL` 改为该环境的 `base_url`。模型中转地址保持原值：中转按域名路由，其域名已解析到 `formal-test` 主机上的中转，改成字面 IP 会被反代拒绝。不重新生成密钥或管理员。服务器之间直接复制（例如 `scp -3`），不经本地落盘、不回显值，权限 `0600`。修改运行配置后只重建本栈的 `art-ai-go` 服务。

中转按来源 IP 放行。容器访问同机中转时来源是 Compose 项目网络中的容器地址，因此 `formal-test` 用 `extra_compose_file` 固定项目网络子网与容器地址，供中转放行；是否放行及放行范围由用户在中转侧决定。改变网络配置后须对本项目执行一次 `down`/`up` 让网络按新配置重建（只操作本项目）。

冒烟（`smoke` 从目标环境的 `runtime.env` 读取管理员登录，并设置 `BASE`、`EXPECT_SHA`（容器 OCI revision）`RELEASE_CONTAINER` 与 `RELEASE_ENV`（环境名，验收脚本据此读取同一部署的日志）；不打印密码）：

```bash
python3 scripts/release-env.py smoke <环境> -- bash scripts/smoke/smoke-l0-browser-boot.sh
python3 scripts/release-env.py smoke <环境> -- bash scripts/smoke/smoke-l1-free.sh
python3 scripts/release-env.py smoke <环境> -- bash scripts/smoke/smoke-l1-agent-mode.sh
```

**多个对话模型（#322）**：默认对话模型连接仍是运行环境文件里的 `LLM_BASE_URL`、`LLM_API_KEY`、`LLM_MODEL`。要给用户开放更多对话模型，在同一文件加 `LLM_CONNECTIONS=<ID>,<ID>`，并为每个 `<ID>` 显式写 `<ID>_BASE_URL`、`<ID>_API_KEY`、`<ID>_MODEL`（可选 `<ID>_CONTEXT_WINDOW_TOKENS`）；统一网关下每个地址都写同一个网关 URL，不设跨连接的别名或回落。完整规则见 [Provider 配置](provider-configuration.md#多个对话模型连接--multiple-chat-model-connections)。改完只重建本栈的 `art-ai-go` 服务，然后在管理后台「LLM 设置」点「测试连接」核对连通性与读图，再设置默认模型和开放的档位；设置按版本保存在数据库，跨发布保留。

**Multiple chat models (#322):** the default chat connection stays `LLM_BASE_URL`, `LLM_API_KEY` and `LLM_MODEL` in the runtime environment file. To offer more chat models, add `LLM_CONNECTIONS=<ID>,<ID>` to the same file and set `<ID>_BASE_URL`, `<ID>_API_KEY` and `<ID>_MODEL` (optionally `<ID>_CONTEXT_WINDOW_TOKENS`) for each `<ID>` explicitly; with one gateway every address is the same gateway URL, with no alias or fallback between connections. Rules: [Provider configuration](provider-configuration.md#多个对话模型连接--multiple-chat-model-connections). Rebuild only this stack's `art-ai-go` service, then use "Test connection" on the admin page "LLM settings" to check reachability and image input before choosing the default model and the offered levels; the settings are versioned in the database and survive releases.

本地运行时，`smoke-l1-agent-mode.sh` 只能比对 `/api/version` 与 `EXPECT_SHA`；日志文件检查在目标主机运行（设 `ART_AI_SERVER_ROOT=<root>`）或经 SSH 读 `production/logs/art-ai.log` 补核。

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

待用户决定：中转是否放行 `formal-test` 项目网络的固定来源地址（未放行前该环境的 Agent 对话请求被中转以来源 IP 拒绝）；`formal-test` 的域名/HTTPS 接入（共享 Nginx）及之后是否改为生产运行模式（要求 HTTPS 的 `PUBLIC_BASE_URL`）与 Meshy Webhook；去背景等跨主机依赖是否在 `formal-test` 主机另行部署；`dev-test` 验收通过后是否自动晋级。

## 9. dev-test 栈概况

- 入口：dev-test 的 `base_url`（`python3 scripts/release-env.py show dev-test`；地址只在未纳入 Git 的
  `deploy/local/environments.json`）
- Compose 项目：环境文件 `dev-test` 条目的 `compose_project`
- 运行入口：`cmd/art-ai`
- Product Schema 真源：`internal/catalog/default_catalog.json`
- 数据库：`production/data/art-ai.db`
- CAS：`production/storage-go`

2026-07-29 已在该测试环境完成 Next → Go 的停机全量迁移与严格对账。
该 Compose 项目只是测试主机上的生产同构项目，不代表正式生产已接管。
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
- **目录**：`<CI 目录>/.lock`（互斥）、`run/<run id>/`（本次 bundle 与工作目录，结束即删）、`results/<run id>/`（结果 JSON、容器日志、重试证据，保留最近 10 次）。
- **对象**：容器带标签 `plinth.host-ci=1`，名称 `plinth-ci-<run id>-aN`；镜像名 `plinth-ci:<12 位 hash>`，保留最新 2 个。与 Plinth 栈和同机其他业务的 Compose 项目、容器、卷、镜像互不相干；它们不在这套清理的范围内。
- **开跑条件**：`MemAvailable` ≥ 13 GiB（容器上限 12 GiB 加 1 GiB 余量，可配置），CI 目录与 Docker 根目录所在磁盘各 ≥ 10 GiB 空闲。同机其他业务忙时可能不满足，此时工具以退出码 70 停下，不会强行启动；这不是故障，等资源恢复后重跑。
- **手工排障**：只读查看 `docker ps -a --filter label=plinth.host-ci=1` 与 `<CI 目录>/results/`。需要清理时只删带这个标签的容器和 `<CI 目录>/run/` 下的子目录；下一次运行持锁后也会自动清理遗留。不要用 `docker system prune`、`docker image prune` 或通配符删除。
- **工作流重放（#86）**：`python3 scripts/host-ci/run.py <sha> --workflows` 复用同一目录、同一把 `.lock` 与同一预检，用固定版本的 act（`<CI 目录>/tools/act-<版本>/act`，首次使用时下载并校验发布包摘要）和按摘要固定的运行器镜像 `catthehacker/ubuntu:act-24.04` 重放 `pull_request` 与 `push` 事件，作业容器同样带标签 `plinth.host-ci=1`、限 4 CPU / 12 GiB；`<CI 目录>/act-cache` 是 Action 缓存。这是本地模拟，不是 GitHub self-hosted runner，主机不接受平台事件。与门禁模式的差别：作业需要联网。两种运行结束后，本机用 `gh` 登录对被测 SHA 写 commit status `host-ci/gates` 与 `host-ci/workflows`（`--no-status` 可关）。排障时多出的只读检查：`docker ps -a --filter label=plinth.host-ci=1` 同样能看到 act 的作业容器；act 创建的 `act-` 前缀卷和网络在运行结束时按“本次新增”清理，不要用通配符手工删除。
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

公开仓库 `LeXwDeX/Plinth` 于 2026-10-26 以一个单根快照提交（源 revision `6ba59481`）发布，不带内部开发历史；2026-10-27 起它是唯一仓库，此后的提交直接在其上进行，不再生成快照。下面的步骤只在需要从内部存档重建公开起点时使用；快照工具只在本地生成，不推送，替换公开仓库已有内容或强推前须由用户当场确认。日常开发中，同一扫描以 `dev_test.md` §2「public tree」门禁在每次推送前运行：每个推送的提交都会立即公开。

1. 选定源 revision：已合入 `main`、通过 `dev_test.md` §2 门禁和开源 1.0 验收（#260）的精确 SHA。
2. 先检查：`scripts/check-public-tree.sh <SHA>`（日常开发中同一扫描以 `dev_test.md` §2 第 8 项门禁「public tree」在 pre-push 与 CI 里对工作树运行）。它用 `git archive` 取出该 revision 的已跟踪文件，扣除 `scripts/public-snapshot.exclude` 列出的路径后逐个扫描（文本与二进制都扫）：私有网段前缀、内部主机与域名、服务器路径、本机临时路径、内部 Compose 项目名前缀、组织名称、本机用户目录（公开 CI runner 的家目录除外）、私钥头，以及 GitLab、OpenAI 式、GitHub、Meshy、Tripo 的凭据前缀（只匹配凭据长度，测试里的短假值不算，#334）；本机存在 `deploy/local/environments.json` 时，其中每个主机、地址、根目录、布局路径、Compose 项目与镜像源主机也作为字面量加入；还可以用 `--pattern`、`--patterns-file` 或未纳入 Git 的 `deploy/local/public-snapshot-patterns.txt`（每行一个正则，`#` 开头为注释）补充不想提交的私有词。有任一命中即以非零退出并列出 `文件:行`。
3. 生成：`python3 scripts/make-public-snapshot.py make --rev <SHA> --version v1.0.0 --out <仓库外的新目录> [--tag]`。工具在同级临时目录导出、排除、扫描，扫描失败时不留下任何目录；通过后 `git init -b main`，以本机 `git config user.name/user.email` 为作者和提交者生成唯一提交（提交信息写版本和源 SHA），`--tag`（可选，仅供后续版本；首次发布不创建 tag/Release）时在本地打同名附注 tag。新仓库没有 remote。
4. 在快照目录里按 README 构建（`go build ./...`、`pnpm install --frozen-lockfile` 与 `pnpm build`）并复查 `python3 scripts/make-public-snapshot.py check --tree <快照目录>`，再进入人工确认的发布步骤。

`dev_ops.md`、内部 Compose 文件和发布/冒烟脚本都在公开仓库里，因此它们必须保持干净：环境细节只在本地环境文件；构建与 CI 都只用公共 npm `registry.npmjs.org`（#257，不再有内网镜像覆盖）。仓库里的守门测试与扫描器本身用拼接或字符类书写被拒绝的标识，不含明文，因此扫描不设白名单。截图等二进制文件也按字节扫描。
