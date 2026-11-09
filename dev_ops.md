# 运维协议

本文写 Plinth 内部环境（`dev-test`、`formal-test`）的现行运维要求。部署入口与环境差异见 [`docs/deployment.md`](docs/deployment.md)；主机、地址、端口、目录与 Compose 名只在本地 `deploy/local/environments.json`。旧 Next 栈运维记录、迁移演练证据与事实快照等历史内容见 [`docs/history.md`](docs/history.md)，不得照其操作。

## Agent 全自动发布协议

本节脚本、`production/` 报告路径和 L2 付费验收义务只适用于 `dev-test`（开发测试环境）。部署按环境名（`dev-test`、`formal-test`）进行，入口为 [`docs/deployment.md`](docs/deployment.md)；`formal-test` 只接收 `dev-test` 全部稳定通过（含 Agent 测试）的 revision。`legacy-formal` 已下线，不是可部署环境（存档 #74）。

容量检查分阶段执行，不是在停服前一次完成。构建前或停服前的检查失败时旧服务不变。停服后的备份及候选启动前检查失败时，脚本先恢复旧镜像，再恢复并检查旧服务。恢复失败时服务保持停止，原备份保留并写报告。检查按设备比较 `df` 与 `statvfs` 可用值，并为 Docker 构建、TMPDIR、归档、回滚展开和恢复暂存分别估算需求；每个目标文件系统在扣除估算需求后至少留 1 GiB 和 4,096 个 inode。未知或不支持的容量数据失败关闭。
容量检查是瞬时测量，不预留空间，也不能阻止共用主机的并发写入。恢复报告不可写不能阻止恢复尝试。健康请求的连接超时为 3 秒，总超时为 5 秒。成功清单使用同目录临时文件原子替换。
`release-capacity.py` 为容量检查实现。默认按全量备份估算 `data`、`storage-go`、`web-data`；`dev-test` 的 `backup.scope=database` 只按 `data` 估算归档与恢复空间，但仍核对全部三个挂载、读写标记和路径。离线故障注入见 `scripts/smoke/test-release-rollback.sh`（含 database scope）。当前回归覆盖既有场景、容量场景及 Python 单元测试族；目标环境 Python 3.12.3/GNU tar 1.35 的 mock-only 合同测试也通过。它们验证本机脚本合同，不代替真实发布或部署后回归。

发布原则：

1. **不做 CI 发布**：CI 原则上只做集成测试；发布由 Agent 在本地驱动脚本完成，无需人类干预。
2. **失败协议**：候选启动或健康检查失败时，脚本先按本次备份范围恢复数据，再启动发布前实际运行的镜像（`database` 范围只恢复 `production/data`，候选写入的 `storage-go`/`web-data` 字节保留，不是完整回滚）。镜像标签从原容器读取。失败报告留服务器 `production/reports/release-failures/`，其中写明 `backup_scope` 与 `backup_targets`；Agent 在关联 GitHub Issue 中记录问题、证据、修复与重试结论，再回本地修复并重试。构建/preflight 失败直接中止，旧实例未动。
3. **部署凭据全在本地**（SSH 目标与发布根只在本地 `deploy/local/environments.json`），不进任何远端系统。
4. **各栈单实例**：`dev-test` 与 `formal-test` 分别只运行一个 `art-ai-go` 服务。
5. **测试栈冒烟该做的全做**：L0/L1 每次测试发布后必跑；L2 真实模型链路（含一次小额生成）在涉及 Agent/生成/同意链路的测试发布后跑。`formal-test` 只运行免费 L0/L1/L1 Agent。
6. **LLM 噪声在脚本代码中兜底/重试**（consent 未出现自动重发一次消息再等），不靠人工判定。
7. **冒烟不清理测试数据**；`dev-test` 每次发布本身从全新数据开始（`data: ephemeral`，见 `docs/deployment.md`）。

操作入口（脚本为唯一真源，本节不复制步骤细节）：

```
python3 scripts/release-env.py release dev-test  # 本地发布入口：按环境文件传参（同步→构建→preflight→备份→Compose 更新→健康→失败自动回滚）
scripts/release-go-testenv.sh        # 发布驱动（RELEASE_HOST、RELEASE_ROOT 必填，无缺省主机/根）
scripts/release-server-steps.sh      # 服务器段（由发布入口 scp 执行；<tag> <sha> <remote_root> 均必填）
scripts/smoke/smoke-l1-free.sh       # L1 免费冒烟（每次发布后必跑）
scripts/smoke/smoke-l1-agent-mode.sh # L1 批准方式/3D 交付验收（本功能集每次发布后必跑，零费用）
scripts/smoke/smoke-l2-livemodel.sh  # L2 真实模型链路（按需）
scripts/smoke/README.md              # 分层口径
```

镜像目标与自部署边界（#251/#259）：`Dockerfile.go-runtime` 的默认目标 `runtime` 是开源自部署镜像，不含历史迁移命令；内部环境（`dev-test`、`formal-test`）使用的 `art-ai-go-production` 镜像由 `release-server-steps.sh` 以 `--target runtime-with-migration-tools` 构建（含 `art-ai-etl`、`art-ai-recover-assets` 与 `/app/legacy-*`），`docker-compose.go-production.yml` / `docker-compose.go-candidate.yml` 的 `build.target` 相同，镜像内容与发布前一致。根目录 `docker-compose.yml` 只供自部署，内部环境不使用它，也不得用它操作 `dev-test` / `formal-test` 的 Compose 项目。内部 Compose 与发布脚本仍在原路径。

manifest（发布历史）：`<root>/releases/manifest.txt`（`<root>` 为本地环境文件中该环境的发布根）。它是 append-only 成功历史，同一 tag 与同一完整 SHA 可重复出现（成功时间戳不同）；retention 保留原始行序与字节摘要，要求最后一行匹配当前运行容器，并拒绝同一 tag 的不同 SHA。普通发布的回滚目标取发布前实际运行的镜像。首次迁移的冷备、preflight、takeover 流程另见 `scripts/cutover-go-production.sh`；它需要 `GO_PROD_ROOT=<root>/production` 与 4 个 `GO_LEGACY_*_DIR`，普通发布由 `release-server-steps.sh` 导出。

### 隔离验收进程

经授权的可控故障/版本变化验收可在 `dev-test` 主机另启绑定 `127.0.0.1` 专用端口的测试二进制。它使用独立私有目录、真实静止数据库快照及选定来源资产；不改变 `dev-test` 主实例、候选 Compose 项目、`dev-test` 入口监听、共享 Catalog、数据库或其他业务。运行凭据只从已授权测试栈环境传入进程，不写入清单、报告或仓库。测试数据库和原始环境文件不打入脱敏报告。实际控制参数、出站上限、重启与验证方式见 `scripts/smoke/README.md`。

隔离验收不是额外部署源：测试二进制绑定已交付完整 SHA，`dev-test` 主实例仍按本协议从已推送 `origin/main` 发布并独立回归。停止隔离进程时只操作本轮记录的 PID，保留样本与证据，不删除共享资源。

### dev-test 主机

`dev-test` 主机是多业务共享主机。Plinth 测试栈即 `dev-test`（入口为 dev-test 的 `base_url`，`python3 scripts/release-env.py show dev-test`）：Compose 项目为环境文件 `dev-test` 条目的 `compose_project`，服务 `art-ai-go`，数据位于 `<root>/production/`。

`legacy-formal` 的下列入口不存在，禁止使用：`release-go-formal.sh`、`docker-compose.go-formal.yml`、正式栈无 Key 冒烟（`smoke-l1-formal-keyless.py` 与测试）、正式栈事务的离线测试，以及 `retain-art-ai-images.py` 里读写 `<root>/formal/reports/image-pins.json` 的保护逻辑；环境文件的 `managed_by` 与 `acceptance.legacy_formal_issue` 字段不再支持。保留并改名为 `scripts/release-promotion.py` 的是 `formal-test` 晋级仍在使用的验收报告与成功清单校验（`release-env.py` 加载，`scripts/tests/test_release_promotion.py` 在 CI 门禁中覆盖）。晋级来源报告的格式见 `docs/deployment.md` §4。

### 日志

各栈日志（#233）：运行设置 `log_level`（`LOG_LEVEL`，`debug`/`info`/`warn`/`error`，默认 `debug`，非法值启动失败）；`log_directory`（`LOG_DIR`）非空时，进程把同一份 JSON 日志写到 stdout 和该目录下的 `art-ai.log`，按 `log_file_max_megabytes`（默认 50）滚动、最多保留 `log_file_max_files`（默认 60）个滚动文件且删除超过 `log_retention_days`（默认 30）天的文件，单栈上限约 3 GiB；文件写失败只在 stderr 报一次，不影响 stdout。`dev-test` 挂载 `<root>/production/logs/`（`GO_PROD_LOG_DIR`），Compose 设置 `LOG_LEVEL=debug`、`LOG_DIR=/app/logs`，Docker stdout 均为 `json-file` `50m × 10`。测试发布在预检和停旧实例前以镜像运行身份 chown 并实际写删一次该目录，不可写即中止。每个响应带 `X-Request-ID`，错误体带同值 `request_id` 作为用户可报的错误 ID；访问日志在最外层，4xx 记 WARN、5xx 记 ERROR，字段含 request_id、路由模板、session_id、owner_id、公开错误码及内部原因（Go error 链）；HTTP 响应仍只含公开错误码与文案。前端错误边界、`window` error 与未处理的 Promise 拒绝经限大小、限量的 `POST /api/v1/client-errors`（Schema `runtime.http.routes.client_errors` / `client_error_report`）写入服务端 ERROR 日志「前端错误」，带本次上报的 `request_id`：已登录按 owner 每分钟限量并记 `owner_id`；登录前页面（登录页、会话过期后）用更严的 `anonymous_*` 限额（请求体、文本长度、每客户端与全体每分钟总量），记 `anonymous=true`，`page` 只留路径（#317）。所有日志属性统一脱敏：键名含 password/secret/token/api_key/authorization/cookie 的值、Bearer/Basic 凭据、`key=`/`token=` 等赋值、Provider key 形状、URL query（含签名参数）和长 base64 data URI 一律替换为 `[redacted]`。查看：`docker logs <container>`（`release-env.py show <环境>` 的 `container`）或主机上对应 `logs/art-ai.log`；用用户报告的错误 ID 搜 `request_id`。原始日志仅在受保护环境查看，对外只保留定位所需的脱敏片段。

### dev-test 主机与镜像保留

各栈的发布、回滚、数据目录和镜像清理互相隔离，不触碰同机其他业务。测试栈镜像保留规则如下：

- 构建前只核对镜像清单和容量；**不在构建前删除镜像**。`release-server-steps.sh` 仅在新实例健康检查与成功清单写入均成功后调用 `retain-art-ai-images.py --apply`。失败或回滚路径不运行清理。
- 精确限定仓库 `art-ai-go-production`，按 Docker 镜像创建时间保留最新 **五个不同镜像 ID**（不是五个标签）。保护当前运行镜像、发布前实际运行的回滚镜像，以及测试栈与环境文件 `protect_projects` 所列其他 Compose 项目（`retain-art-ai-images.py --project`/`--protect-project`，发布时由环境文件传入）所有运行或停止容器引用的镜像。通过共享锁协调发布和清理。跨仓库共享标签或 digest 的镜像也保留，避免按 ID 删除影响其他仓库。保护项可能让总数超过五个，不为凑数删除容器。`python3 scripts/smoke/test-image-retention.py` 覆盖这些分支。
- 本镜像保留工具仅执行 `docker rmi <完整镜像 ID>`，无 `-f`，无 `image prune -a`；不删容器、卷、构建缓存、数据库、一致性备份或历史归档。逐个删除失败只记警告并继续；清单无法可靠读取时中止本轮清理并警告，但不将健康发布改为失败。清理前再核对容器引用；并发新增引用仍由非强制 `rmi` 保护。发布备份清理是另一个独立步骤，只按下方 dev-test 备份保留规则处理 `backups/release-*`，不扩大本工具的删除范围。
- helper 默认 dry-run，执行删除必须显式 `--apply`；必须传入已核实的回滚镜像标签或完整 ID，不能从「第二新镜像」猜回滚目标。发布脚本自动传入实际旧容器的镜像标签。独立 dry-run（服务器已有同版源码时）：

  ```bash
  python3 <root>/app/scripts/retain-art-ai-images.py \
    --dry-run --root <root> --project <compose_project> \
    --protect-project <protect_projects 每项> \
    --rollback-image art-ai-go-production:<已核实的上一运行标签>
  ```

  发布前本地检查新版 helper，无需复制或改写服务器文件：

  ```bash
  ssh <dev-test 的 ssh> 'python3 - --dry-run --root <root> --project <compose_project> --protect-project <protect_projects 每项> --rollback-image art-ai-go-production:<已核实的上一运行标签>' \
    < scripts/retain-art-ai-images.py
  ```

  输出 `KEEP` 和 `WOULD REMOVE`，不调用删除。离线回归：`python3 scripts/smoke/test-image-retention.py`，使用 mock Docker，不访问真实 Docker 或服务器。当前脚本改动的远端 dry-run 与实际发布后验收须由发布回执记录，不能以本机 mock 代替。

### 发布备份范围与保留（#364、#66）

本节适用于环境文件写了 `backup` 的环境。`dev-test` 默认走全新数据模式（`data: ephemeral`），不做发布备份，只有仍写 `backup` 时才适用。`formal-test` 必须写 `"backup": {"scope": "full", "keep": 2}`：每次发布全量备份，成功后只保留最新 2 份（#66）。发布备份只含 `data`、`storage-go`、`web-data`（或 database 范围的 `data`），不含镜像和日志。

`dev-test` 只存放测试材料，不必每次发布都完整复制 `data`、`storage-go`、`web-data`，环境文件可为 `dev-test` 写 `"backup": {"scope": "database", "keep": 2}`：

- `scope` 为 `database` 时，停机窗口只备份 `production/data` 到 `backups/release-<tag>/data.tar`，健康失败回滚也只替换数据库。`storage-go` 是应用不可变/只追加 CAS，`web-data` 只读；候选启动后写入的字节保留，可能成为无引用孤儿。**这不是完整回滚**，不能称为全量恢复，也不能为了“一致”删除这些字节。
- `scope` 为 `full` 时备份范围与省略 `backup` 相同，`keep` 只控制成功后的清理。省略 `backup` 时保持全量备份 `data`、`storage-go`、`web-data`，且不运行备份清理。`formal-test` 不得使用 database 模式。
- `release-env.py` 在加载环境文件时拒绝：`scope=database` 用于 `promote_from` 或非 `dev-test` 环境；`scope=full` 用于 `dev-test`、`formal-test` 之外的环境；未知 `scope`。`keep` 为 1–2。服务器段在构建前再次校验 `RELEASE_BACKUP_SCOPE`（`full`/`database`）与 `RELEASE_BACKUP_KEEP`（full 可选，设置时须为 1–2），非法组合不产生副作用。
- 范围记录在 `backups/release-<tag>/scope`（单行 `database` 或 `full`）、成功后的 `production/compose.env` 和失败报告。手工回滚先读这些记录；`database` 只恢复 `data.tar`，保留资产与 Web 字节。没有 `scope` 的旧快照仅在三个归档齐全时按 `full` 处理。
- 只有健康检查与成功清单写入后，服务器段才在设置了 `RELEASE_BACKUP_KEEP` 时调用 `scripts/retain-release-backups.py --apply`。正常清理最多保留最新 2 个已结束发布备份（当前批准 `keep=2`），更早的失败快照也按最新 2 个回收；进行中的快照可临时排除，失败报告与独立诊断日志保留。不处理原始资产、数据库、首次迁移冷备或其他栈的备份。bootstrap、构建/preflight/备份/更新/健康失败和回滚路径都不调用清理。
- 同一发布根的发布备份/恢复、备份清理与手工恢复共用 `<root>/.release.lock`（`scripts/release-lock.py`）。服务器段在构建或改动数据前获取锁并持有到发布结束；设置了 keep 的 retention 从 `RELEASE_LOCK_FD`/`RELEASE_LOCK_ROOT` 继承并校验同一描述符，缺失或身份不符时拒绝，不提供环境变量绕过。校验根、常规文件、owner 和路径身份后，直接对继承描述符执行 `flock LOCK_EX|LOCK_NB`：共享同一 open file description（正常继承）时复用已持有锁；独立描述符在锁空闲时先取得锁再操作；其他进程持锁时失败 busy。借用方不得解锁继承描述符，原持有者在最外层结束时释放。手工恢复用 `release-lock.py run --root <root> -- ...` 包住停止、移动、解包与启动全过程。发布镜像保留使用另一个 `releases/image-retention.lock`，两者不合并；`release-lock.py` 不调用镜像工具，避免自锁。
- 清理递归删除前拒绝快照树内的挂载点，包括与父目录同设备的 bind mount；Linux `/proc/self/mountinfo` 是权威来源，缺失、为空或格式异常时失败关闭。该检查只用于删除，不改变成功清单或恢复资格。
- 失败快照可能有零字节或缺失归档：只有独立失败报告存在时才可清理；成功清单中的备份仍要求完整且非空的归档。失败快照的删除资格不等于恢复资格。
- retention 只读取 tag 属于本次候选备份的失败报告；没有对应备份的历史诊断（包括旧正文格式）不再解析，字节保持原样。匹配候选的报告仍校验类型、安全性和正文中的 tag 行，文件名本身不构成完成证明。
- 清理非零只记警告，不把部分删除报成成功，也不改变已成功发布的结果。helper 默认 dry-run；手工核对（服务器已有同版源码时）：

  ```bash
  python3 <root>/app/scripts/retain-release-backups.py \
    --root <root> --project <compose_project> \
    --rollback-image art-ai-go-production:<已核实的上一运行标签> --keep 2 --dry-run
  ```

- 保留 database-only 回滚快照可能被使用时，禁止运行离线 CAS/task 破坏性维护（`art-ai-storage --execute`、`art-ai-retention` 的 apply/cleanup 等）；细节见开发文档 [CAS 存储维护](docs/storage-maintenance.md)。只读 `--check` 与 dry-run 不受影响。

本机合同回归：`python3 scripts/tests/test_release_env.py`、`bash scripts/smoke/test-release-stack.sh`、`bash scripts/smoke/test-release-rollback.sh`。它们不代替真实部署、容量测量或远端清理验收；实际结果记录在 #364 的 Issue/MR 回执。

### formal-test 正式测试环境（#270）

`formal-test` 的定位、与 `dev-test` 的差异、运行配置规则、发布/回滚命令和验收见 [`docs/deployment.md`](docs/deployment.md)；主机、地址、端口、目录与 Compose 名只在本地 `deploy/local/environments.json`。晋级回执记在 #2。

## 服务器信息
- 服务器A（`dev-test`）：SSH 与发布根见本地 `deploy/local/environments.json`（`python3 scripts/release-env.py show dev-test`）；
- 服务器B（`formal-test`）：见本地 `deploy/local/environments.json`；

## dev-test 测试栈

服务器A（`dev-test` 主机）是多个业务共用的主机。本节只说明 Plinth 的
`dev-test` 测试栈；其他业务不在操作范围。
`dev-test` 是纯 Go 单容器，用于生产同构测试：

- Compose 项目：环境文件 `dev-test` 条目的 `compose_project`
- 容器：`<compose_project>-art-ai-go-1`（`release-env.py show dev-test` 的 `container`）
- 当前镜像与源码 SHA：以服务器上的容器 OCI 标签为准，使用
  `docker inspect <container>` 查询
  `org.opencontainers.image.version` 与 `org.opencontainers.image.revision`；
  发布后验收必须确认 revision 等于已推送的 `origin/main` 完整 SHA
- 测试数据根：`<root>/production`（`<root>` 为 dev-test 发布根）
- 原生数据库：`production/data/art-ai.db`
- 原生 CAS：`production/storage-go`
- 运行环境：`production/runtime.env`（权限 `0600`）
- 入口：dev-test 的 `base_url`（`python3 scripts/release-env.py show dev-test`）

### 测试栈大模型接线

Plinth 的大模型对话与图片生成请求从该测试栈出站访问 llms 正式服务器
网关的 `/v1`（地址在 dev-test 运行环境文件 `LLM_BASE_URL`；链路：测试客户端 → `dev-test` Plinth 测试栈 →
llms 正式服务器；后者不限制客户端与模型）。`production/runtime.env` 中的
`LLM_BASE_URL`、`OPENAI_BASE_URL` 指向该入口；`LLM_API_KEY`、
`OPENAI_API_KEY` 使用测试上游 key `<TEST_API_KEY>`（仅适用
测试链路；正式生产密钥仍只存正式生产自身的运行环境文件），文件权限保持
`0600`。测试栈使用无版本别名：默认连接 `LLM_MODEL=qwen-omni-flash`（网关当前解析为 qwen3.8-omni-flash；思考档位 low/medium/xhigh，默认 xhigh）；第二连接 `LLM_CONNECTIONS=GPT`、`GPT_MODEL=gpt-astra`（当前解析为 gpt-6-astra；思考档位 low/medium/high/xhigh/max，默认 high）。别名让上游网关升级版本时下游配置无需改动（#64）。请求走 OpenAI 兼容 `chat/completions`。思考参数按 Product Schema `chat.agent.thinking.model_profiles` 的模型前缀发送，`qwen-omni-flash` 命中同名 profile（`enable_thinking=true`、`reasoning_effort` 默认 `xhigh`，上下文窗口 1000000），`gpt-astra` 命中同名 profile（`reasoning_effort` low/medium/high/xhigh/max 五档，默认 `high`）；其他中国大陆模型仍用官方最高档。除 `<TEST_API_KEY>` 外的密钥不写
仓库、命令输出或记录，也不要把同机代理测试端口当作 Plinth 的模型入口。
Provider 变量的规范名与别名见 `docs/provider-configuration.md`（#252）：图像 Provider 只由自己的 `<ID>_API_KEY` 启用，不再借用 `LLM_API_KEY`；测试栈的 `OPENAI_API_KEY`、`GEMINI_API_KEY` 已单独设置。每个连接只读自己的地址变量：`LLM_BASE_URL` 只是 Agent 对话模型的地址，`OPENAI_BASE_URL`、`GEMINI_BASE_URL` 都不回落到它。测试栈所有模型都经同一 llms 网关，运行环境文件须把 `LLM_BASE_URL`、`OPENAI_BASE_URL`、`GEMINI_BASE_URL` 都显式设为该网关的 `/v1`；`GEMINI_BASE_URL` 没有默认值，漏设时 Gemini 不注册，启动日志记 `Provider 连接缺少地址，未启用`（`missing_settings=GEMINI_BASE_URL`）。
改接线前只读核对目标环境，改后核对容器生效配置及真实生成结果；模型列表和健康检查不等于生成验收。

`dev-test` 测试栈允许重部署、修改管理员口令、清空数据；但发布后必须保持
上游与客户端接线可用。变更运行接线时先备份环境文件，只重建
dev-test Compose 项目的 `art-ai-go` 服务；回读容器的 URL、凭据是否非空及
是否一致时仅输出布尔结果，再以真实文生图和图生图任务核对产物落盘。

### 普通发布约束

不要使用 `deploy.sh`、旧 `docker compose up` 或 Prisma 命令更新服务器A
的 `dev-test`；这些只属于 [`docs/history.md`](docs/history.md) 中的旧 Next 记录。旧 Next 数据根封存不删，候选 Compose 项目的容器保持停止。

针对 `dev-test` 主机的普通 Go 版本发布不得重跑
ETL，也不得改写 `production/migration-ready.txt`。新镜像必须从已推送的
`main` 精确提交
构建，把完整 SHA 写入 OCI revision；随后使用
`GO_PROD_PROJECT=<compose_project> scripts/cutover-go-production.sh preflight --image-tag <tag>
--source-commit <sha> --no-build` 做隔离预检，再以
`docker-compose.go-production.yml` 和 dev-test 的 Compose 项目名更新单个
Go 服务；新容器或无扣费线上验收失败时恢复一致性备份和前一镜像标签。
完整命令见 `README-go.md`「后续普通发布」。

### 任务列表与模型预览

任务列表必须保持 Repository 的“两阶段读取”：先在窄行上排序/分页 Task
ID，再按 ID 批量加载完整记录。宽行直接排序会在含大体积历史 `input_json` 的数据上耗尽容器 tmpfs。

任务列表与首页的模型预览只能按 Product Schema
`task_history.assets.preview_roles` 选择 `thumbnail`、其次 `image`；
不得把 `texture` 当模型截图。历史截图补迁移优先读取旧 Next 的
pathname-hash 只读缓存，只有精确登记的 14 个原站缺失 task ID 可以保留
缺图；新增未登记缺失必须让补迁移命令失败。

### 无扣费验收

```bash
BASE=<dev-test 的 base_url>   # python3 scripts/release-env.py show dev-test
curl "$BASE/healthz"
curl "$BASE/api/version"
```

登录、管理统计、历史资产 SHA-256、九类 Provider 可用性和 Webhook 的完整
验收脚本记录在
`scripts/post-cutover-acceptance.sh`。live Provider
有效生成会产生外部费用。`dev-test` 的小额真实模型验收按 `AGENTS.md` 的 dev-test 常设授权与付费验收规则执行；超出该测试授权范围的生成和正式生产生成均须另行明确授权。
