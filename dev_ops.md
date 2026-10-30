# 服务器访问

## 文档交付前事实快照（更新 2026-10-06T03:22:49Z）

测试栈运行版本为 `fab9763fd8d521a5521e9e9a998caec3ad944e7c`。Ops 主线为 `8749129ff385094f4ff49a9e82a96867676ef4fe`。两份发布脚本不进入运行镜像。Ops 交付没有重建或重启 fab 容器，不能把 Ops Git SHA 当作浏览器运行字节的 SHA。

以下 Issue 状态沿用文档交付前的原生采样回执（34 项关闭，仅 #121、#122 开放）。本节于 2026-10-06T03:22:49Z 更新示例脱敏记录。文档 Git 待交付状态不预填后续结果。后续 Git、CI、MR 和 Issue 完成状态以本次文档 MR 的原生回执为准，不预填未来提交或关闭状态。

原生 #91–#126 共 36 个工作项。34 项关闭，仅文档 #121、#122 开放。#95、#119 是测试工作项。工作项数量不是产品缺陷数量。#123、#124、#125、#126 已随正常合并闭合。

fab 的 L0、L1、L2、L4、免费 API、免费 Skill Stop 与标准四组浏览器已有实际通过回执。标准八份模型导出 PNG 已由 Root 查看，内容为预期 Meshy 白凳和 Tripo 纹理凳。标准图片导出中三组曾显示共享会话中的旧画板。独立 image v8 已用同一 P3 图片补证：Chromium/WebKit 桌面及移动视口四组实际通过，63.321 秒；Root 查看四份 512×512 PNG，均为新橙猫，没有旧凳。

rapid v12 完整四组、每组三场景的 LIVE 实际通过，共 12 个 trial，84.647 秒，退出码 0。Chromium/WebKit 各覆盖桌面和移动视口。十二次均核对同 owner、session、来源 Task/Asset、原模型完整消费、真实 viewer、独立 Page 信号和同一节点保存、刷新、重新选择。十二次原截图上传均为 201，观测 jobs 全部终结。

Root 实际查看四组 control 的完整 PNG，均为预期白色 Meshy 凳。十二份截图与十二份原上传文件逐一核对完整 hash。每组的三份图 hash 相同；这不是十二次独立看图。自然切换分别为 35、37、21、24 毫秒。四组延迟 PNG 场景在原 GET 阻塞期间均观察到服务端节点；切换分别为 74、72、107、101 毫秒。

本轮外域请求、HTTP 错误、未预期失败、close 错误和 observer 错误均为零。各组原始失败请求仍为 9、7、6、7 条；严格 Skills 范围替换分类为 8、5、6、7 条，完整原消费的模型取消分类为 1、2、0、0 条。不能写成所有原始请求零失败。分类依赖精确同请求证据与源码、时序关联；原生取消的底层原因仍 UNKNOWN。

305 项本机测试实际通过，但其中包含最终函数组合、未变 helper 回归和旧主脚本基线。ASTRA 的九项独立组合使用本机模拟，不能代替真实浏览器。rapid v12 原生报告 SHA256 为 `50ab4b2eca1b563dd8b651c3e16e16f747b4199d8a23ac9ffea0c7f9e44f9341`；Root VQA 为 `7908ae5697aa77d575f54501712a9dd5528d4dafb61ae960429dd68e392aefc5`。


旧 rapid v7、v8、v11 和诊断整轮 FAIL 保留。WK400 v3 单次诊断的原 UI 上传返回 201；截图和下载字节一致，单 control 的节点、刷新和重新选择通过。整轮因四次 Skills 原生取消仍为 FAIL。历史 WebKit 上传 400 未复现，具体原因 UNKNOWN；服务器日志窗口中的一条 400 没有唯一请求关联，不能据此推定原因。模型原生取消的底层原因也仍 UNKNOWN。新验收只按同一原请求的完整消费证明和精确范围替换证明分类，保留原失败事件，不回填旧 PASS。

十二处凭据形状示例引用已替换为明确占位符，其中 dev_ops 五处、dev_test 一处、AGENTS 一处、实现台账四处、README 一处。这是引用次数，不是十二个有效密钥。审查预览曾漏遮短示例，原记录保留；后续校验只保存计数与 hash。未核验凭据有效性。没有核验有效性、旋转或改写 Git 历史。

未覆盖物理 Safari、完整触控与无障碍、全部 Skill、本轮未实际触发的解码器、Chat 连续驱动 3D 处理、完整历史来源链，以及 `plan_submit_unreconciled` 的专门人工处置入口。产品计划未实现不等于已复现缺陷。不能保证未知缺陷为零。

运行验收与 Root 图片查看已完成。六份最终文档的 Git 交付尚未完成。本机七项于 2026-10-06T03:03:10.992Z 至 03:09:51.975Z 实际运行，401.0423 秒，全部通过，绑定 freeze-v1。最终六文档随后追加了示例脱敏和事实措辞；该回执仅复用于相同代码、测试及构建输入，不表示最终六文档版本重新运行了本机七项。下一步仍为 ASTRA 最终六文档终审、正常 hooks 七项、精确来源 CI 七项和主线 CI 七项，以及文档 MR 正常合并。MR 仅关联 #121、#122，正常合并后自动关闭。当前文档 Issue 仍开放，不能预先写成文档闭环。纯文档不重建镜像、不重启测试栈。

- 每次同步生成下面[服务器数量]的subagent去同步部署，验证，测试；
  - 服务器A是多应用开发环境，有外部Nginx的Docker服务承载Webhook回传给本项目Docker；

## Agent 全自动发布协议（2026-09-27 确认，现行发布方式）

本节现行脚本、`production/` 报告路径和 L2 付费验收义务仅适用于 `dev-test`（开发测试环境）。`legacy-formal`（#232 隔离正式栈）已于 2026-10-30 下线（GitHub #74），不再有现行发布路径。

**环境布局（2026-10-20 用户决定，#270）**：部署入口改为 [`docs/deployment.md`](docs/deployment.md)，按环境名（`dev-test`、`formal-test`）部署，目标主机与路径只在本地 `deploy/local/environments.json`。本节协议适用于 `dev-test`；`formal-test` 只接收 `dev-test` 全部稳定通过（含 Agent 测试）的 revision。同机的 `legacy-formal`（内部存档 #232 的隔离正式栈）在新布局下经维护者 2026-10-30 决定下线（GitHub #74），见下文「dev-test 主机与镜像保留」。

容量检查分阶段执行，不是在停服前一次完成。构建前或停服前的检查失败时旧服务不变。停服后的备份及候选启动前检查失败时，脚本先恢复旧镜像，再恢复并检查旧服务。恢复失败时服务保持停止，原备份保留并写报告。检查按设备比较 `df` 与 `statvfs` 可用值，并为 Docker 构建、TMPDIR、归档、回滚展开和恢复暂存分别估算需求；每个目标文件系统在扣除估算需求后至少留 1 GiB 和 4,096 个 inode。未知或不支持的容量数据失败关闭。
容量检查是瞬时测量，不预留空间，也不能阻止共用主机的并发写入。恢复报告不可写不能阻止恢复尝试。健康请求的连接超时为 3 秒，总超时为 5 秒。成功清单使用同目录临时文件原子替换。
`release-capacity.py` 为容量检查实现。默认按全量备份估算 `data`、`storage-go`、`web-data`；`dev-test` 的 `backup.scope=database` 只按 `data` 估算归档与恢复空间，但仍核对全部三个挂载、读写标记和路径。离线故障注入见 `scripts/smoke/test-release-rollback.sh`（含 database scope）。当前回归覆盖既有场景、容量场景及 Python 单元测试族；目标环境 Python 3.12.3/GNU tar 1.35 的 mock-only 合同测试也通过。它们验证本机脚本合同，不代替真实发布或部署后回归。

七条既定决策：

1. **不做 CI 发布**：CI 原则上只做集成测试；发布由 Agent 在本地驱动脚本完成，无需人类干预。
2. **失败协议**：候选启动或健康检查失败时，脚本先按本次备份范围恢复数据，再启动发布前实际运行的镜像（`database` 范围只恢复 `production/data`，候选写入的 `storage-go`/`web-data` 字节保留，不是完整回滚）。镜像标签从原容器读取。失败报告留服务器 `production/reports/release-failures/`，其中写明 `backup_scope` 与 `backup_targets`；Agent 在关联 GitHub Issue 中记录问题、证据、修复与重试结论，再回本地修复并重试。构建/preflight 失败直接中止，旧实例未动。
3. **部署凭据全在本地**（SSH 目标与发布根只在本地 `deploy/local/environments.json`），不进任何远端系统。
4. **各栈单实例**：`dev-test` 与 `formal-test` 分别只运行一个 `art-ai-go` 服务。
5. **测试栈冒烟该做的全做**：L0/L1 每次测试发布后必跑；L2 真实模型链路（含一次小额生成）在涉及 Agent/生成/同意链路的测试发布后跑。`formal-test` 只运行免费 L0/L1/L1 Agent。
6. **LLM 噪声在脚本代码中兜底/重试**（consent 未出现自动重发一次消息再等），不靠人工判定。
7. **测试数据保留在 `dev-test`**，冒烟不清理。

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

用户授权的可控故障/版本变化验收可在 `dev-test` 主机另启绑定 `127.0.0.1` 专用端口的测试二进制。它使用独立私有目录、真实静止数据库快照及选定来源资产；不改变 `dev-test` 主实例、候选 Compose 项目、`dev-test` 入口监听、共享 Catalog、数据库或其他业务。运行凭据只从已授权测试栈环境传入进程，不写入清单、报告或仓库。测试数据库和原始环境文件不打入脱敏报告。实际控制参数、出站上限、重启与验证方式见 `scripts/smoke/README.md`。

隔离验收不是额外部署源：测试二进制绑定已交付完整 SHA，`dev-test` 主实例仍按本协议从已推送 `origin/main` 发布并独立回归。停止隔离进程时只操作本轮记录的 PID，保留样本与证据，不删除共享资源。

### dev-test 主机与镜像保留

`dev-test` 主机是多业务共享主机。Plinth 测试栈即 `dev-test`（入口为 dev-test 的 `base_url`，`python3 scripts/release-env.py show dev-test`）：Compose 项目为环境文件 `dev-test` 条目的 `compose_project`，服务 `art-ai-go`，数据位于 `<root>/production/`。

**`legacy-formal` 已下线（2026-10-30，维护者决定，GitHub #74）。** 它是 2026-10-19 在同一主机、同一发布根下建成的隔离正式栈（内部存档 #232）：独立 Compose 项目、独立端口，数据在 `<root>/formal/`，空库起步、无 Provider Key，由 `scripts/release-go-formal.sh` 晋级。2026-10-20 改用独立主机的 `formal-test` 后，它的去留一直待决，#60 之后晋级验收来源 `acceptance.legacy_formal_issue` 也未设置。下线只动该栈自己的资源：先对 `<root>/formal/` 做全量备份（数据库、Storage、Web data、日志、报告、运行环境、源码快照与镜像导出，逐件记录大小与 sha256 并验证可列出内容），再停止并移除它的 Compose 项目与网络、删除它独有的镜像和 `<root>/formal/` 目录。该栈没有命名卷。同一发布根里 `dev-test` 的 `production/` 与 `app/`、旧候选 Compose 项目、旧 Next 目录和同机其他业务都未触碰。最终备份保存在发布根之外的独立位置，主机路径与校验值记在 GitHub #74，不写进仓库。

随之移除：`release-go-formal.sh`、`docker-compose.go-formal.yml`、正式栈无 Key 冒烟（`smoke-l1-formal-keyless.py` 与测试）、正式栈事务的离线测试，以及 `retain-art-ai-images.py` 里读写 `<root>/formal/reports/image-pins.json` 的保护逻辑；环境文件的 `managed_by` 与 `acceptance.legacy_formal_issue` 字段不再支持。保留并改名为 `scripts/release-promotion.py` 的是 `formal-test` 晋级仍在使用的验收报告与成功清单校验（`release-env.py` 加载，`scripts/tests/test_release_promotion.py` 在 CI 门禁中覆盖）。晋级来源报告的格式见 `docs/deployment.md` §4。

测试栈日志（#233，2026-10-19 实现，部署验收见 #233）：运行设置 `log_level`（`LOG_LEVEL`，`debug`/`info`/`warn`/`error`，默认 `debug`，非法值启动失败）；`log_directory`（`LOG_DIR`）非空时，进程把同一份 JSON 日志写到 stdout 和该目录下的 `art-ai.log`，按 `log_file_max_megabytes`（默认 50）滚动、最多保留 `log_file_max_files`（默认 60）个滚动文件且删除超过 `log_retention_days`（默认 30）天的文件，单栈上限约 3 GiB；文件写失败只在 stderr 报一次，不影响 stdout。`dev-test` 挂载 `<root>/production/logs/`（`GO_PROD_LOG_DIR`），Compose 设置 `LOG_LEVEL=debug`、`LOG_DIR=/app/logs`，Docker stdout 均为 `json-file` `50m × 10`。测试发布在预检和停旧实例前以镜像运行身份 chown 并实际写删一次该目录，不可写即中止。每个响应带 `X-Request-ID`，错误体带同值 `request_id` 作为用户可报的错误 ID；访问日志在最外层，4xx 记 WARN、5xx 记 ERROR，字段含 request_id、路由模板、session_id、owner_id、公开错误码及内部原因（Go error 链）；HTTP 响应仍只含公开错误码与文案。前端错误边界、`window` error 与未处理的 Promise 拒绝经限大小、限量的 `POST /api/v1/client-errors`（Schema `runtime.http.routes.client_errors` / `client_error_report`）写入服务端 ERROR 日志「前端错误」，带本次上报的 `request_id`：已登录按 owner 每分钟限量并记 `owner_id`；登录前页面（登录页、会话过期后）用更严的 `anonymous_*` 限额（请求体、文本长度、每客户端与全体每分钟总量），记 `anonymous=true`，`page` 只留路径（#317）。所有日志属性统一脱敏：键名含 password/secret/token/api_key/authorization/cookie 的值、Bearer/Basic 凭据、`key=`/`token=` 等赋值、Provider key 形状、URL query（含签名参数）和长 base64 data URI 一律替换为 `[redacted]`。查看：`docker logs <container>`（`release-env.py show <环境>` 的 `container`）或主机上对应 `logs/art-ai.log`；用用户报告的错误 ID 搜 `request_id`。原始日志仅在受保护环境查看，对外只保留定位所需的脱敏片段。

各栈的发布、回滚、数据目录和镜像清理互相隔离，不触碰同机其他业务。测试栈镜像保留规则如下：

- 构建前只核对镜像清单和容量；**不在构建前删除镜像**。`release-server-steps.sh` 仅在新实例健康检查与成功清单写入均成功后调用 `retain-art-ai-images.py --apply`。失败或回滚路径不运行清理。
- 精确限定仓库 `art-ai-go-production`，按 Docker 镜像创建时间保留最新 **五个不同镜像 ID**（不是五个标签）。保护当前运行镜像、发布前实际运行的回滚镜像，以及测试栈与环境文件 `protect_projects` 所列其他 Compose 项目（`retain-art-ai-images.py --project`/`--protect-project`，发布时由环境文件传入）所有运行或停止容器引用的镜像。通过共享锁协调发布和清理。跨仓库共享标签或 digest 的镜像也保留，避免按 ID 删除影响其他仓库。保护项可能让总数超过五个，不为凑数删除容器。`python3 scripts/smoke/test-image-retention.py` 覆盖这些分支（#74 删除 `formal/` pin 保护后同步更新）。
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

### dev-test 发布备份范围与保留（#364）

`dev-test` 只存放测试材料，用户已确认不必每次发布都完整复制约 21 GB 的 `data`、`storage-go`、`web-data`。环境文件因此可为 `dev-test` 写 `"backup": {"scope": "database", "keep": 2}`：

- `scope` 为 `database` 时，停机窗口只备份 `production/data` 到 `backups/release-<tag>/data.tar`，健康失败回滚也只替换数据库。`storage-go` 是应用不可变/只追加 CAS，`web-data` 只读；候选启动后写入的字节保留，可能成为无引用孤儿。**这不是完整回滚**，不能称为全量恢复，也不能为了“一致”删除这些字节。
- 省略 `backup` 时保持全量备份 `data`、`storage-go`、`web-data`，且不运行备份清理。`formal-test` 不使用本节的 database 模式或自动清理。
- 只有环境名 `dev-test` 可设置 `backup`；`release-env.py` 拒绝把它用于 `promote_from` 或其他环境，`scope` 只接受 `database`，`keep` 为 1–2。服务器段在构建前再次校验 `RELEASE_BACKUP_SCOPE`/`RELEASE_BACKUP_KEEP`，非法组合不产生副作用。
- 范围记录在 `backups/release-<tag>/scope`（单行 `database` 或 `full`）、成功后的 `production/compose.env` 和失败报告。手工回滚先读这些记录；`database` 只恢复 `data.tar`，保留资产与 Web 字节。没有 `scope` 的旧快照仅在三个归档齐全时按 `full` 处理。
- 只有健康检查与成功清单写入后，服务器段才在 database 模式调用 `scripts/retain-release-backups.py --apply`。正常清理最多保留最新 2 个已结束发布备份（当前批准 `keep=2`），更早的失败快照也按最新 2 个回收；进行中的快照可临时排除，失败报告与独立诊断日志保留。不处理原始资产、数据库、首次迁移冷备或其他栈的备份。bootstrap、构建/preflight/备份/更新/健康失败和回滚路径都不调用清理。
- 同一发布根的发布备份/恢复、备份清理与手工恢复共用 `<root>/.release.lock`（`scripts/release-lock.py`）。服务器段在构建或改动数据前获取锁并持有到发布结束；database 模式的 retention 从 `RELEASE_LOCK_FD`/`RELEASE_LOCK_ROOT` 继承并校验同一描述符，缺失或身份不符时拒绝，不提供环境变量绕过。校验根、常规文件、owner 和路径身份后，直接对继承描述符执行 `flock LOCK_EX|LOCK_NB`：共享同一 open file description（正常继承）时复用已持有锁；独立描述符在锁空闲时先取得锁再操作；其他进程持锁时失败 busy。借用方不得解锁继承描述符，原持有者在最外层结束时释放。手工恢复用 `release-lock.py run --root <root> -- ...` 包住停止、移动、解包与启动全过程。发布镜像保留使用另一个 `releases/image-retention.lock`，两者不合并；`release-lock.py` 不调用镜像工具，避免自锁。
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

### formal-test 正式测试环境（2026-10-20 用户决定，#270）

`formal-test` 的定位、与 `dev-test` 的差异、运行配置规则、发布/回滚命令和验收见 [`docs/deployment.md`](docs/deployment.md)；主机、地址、端口、目录与 Compose 名只在本地 `deploy/local/environments.json`。部署回执记在 #270。

## 服务器信息
- 服务器A（`dev-test`）：SSH 与发布根见本地 `deploy/local/environments.json`（`python3 scripts/release-env.py show dev-test`）；
- 服务器B（`formal-test`）：见本地 `deploy/local/environments.json`；

# ═══════════════════════════════════════════════
# 服务器A 测试环境（权威，2026-07-29）
# ═══════════════════════════════════════════════

服务器A（`dev-test` 主机）是多个业务共用的主机。本节只说明 Plinth 的
`dev-test` 测试栈；其他业务不在操作范围。
`dev-test` 已由纯 Go 单容器接管，用于生产同构测试：

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

Plinth 的大模型对话与图片生成请求从该测试栈出站访问 llms 正式服务器
网关的 `/v1`（地址在 dev-test 运行环境文件 `LLM_BASE_URL`；链路：测试客户端 → `dev-test` Plinth 测试栈 →
llms 正式服务器；后者不限制客户端与模型）。`production/runtime.env` 中的
`LLM_BASE_URL`、`OPENAI_BASE_URL` 指向该入口；`LLM_API_KEY`、
`OPENAI_API_KEY` 使用测试上游 key `<TEST_API_KEY>`（2026-09-27 用户给定，仅适用
测试链路；正式生产密钥仍只存正式生产自身的运行环境文件），文件权限保持
`0600`。测试栈对话模型为字面量 `deepseek`（2026-10-19 起网关目录已无 `qwen-max`、`glm-flash`，直接请求返回 400 `model not supported by any target`；经用户确认改用 `deepseek`，网关当前路由到 `deepseek-flash`）；请求走 OpenAI 兼容 `chat/completions`。思考参数按 Product Schema `chat.agent.thinking.model_profiles` 的模型前缀发送，DeepSeek 用 `thinking.type=enabled`、`reasoning_effort=low`（用户 2026-10-20 决定；官方只有 low/high/max 三档），工具循环回传 `reasoning_content`；其他中国大陆模型仍用官方最高档。除 `<TEST_API_KEY>` 外的密钥不写
仓库、命令输出或记录，也不要把同机代理测试端口当作 Plinth 的模型入口。
Provider 变量的规范名与别名见 `docs/provider-configuration.md`（#252）：图像 Provider 只由自己的 `<ID>_API_KEY` 启用，不再借用 `LLM_API_KEY`；测试栈的 `OPENAI_API_KEY`、`GEMINI_API_KEY` 已单独设置。每个连接只读自己的地址变量：`LLM_BASE_URL` 只是 Agent 对话模型的地址，`OPENAI_BASE_URL`、`GEMINI_BASE_URL` 都不回落到它。测试栈所有模型都经同一 llms 网关，运行环境文件须把 `LLM_BASE_URL`、`OPENAI_BASE_URL`、`GEMINI_BASE_URL` 都显式设为该网关的 `/v1`；`GEMINI_BASE_URL` 没有默认值，漏设时 Gemini 不注册，启动日志记 `Provider 连接缺少地址，未启用`（`missing_settings=GEMINI_BASE_URL`）。
`dev-test` 测试栈允许重部署、修改管理员口令、清空数据；但发布后必须保持
上游与客户端接线可用。变更运行接线时先备份环境文件，只重建
dev-test Compose 项目的 `art-ai-go` 服务；回读容器的 URL、凭据是否非空及
是否一致时仅输出布尔结果，再以真实文生图和图生图任务核对产物落盘。

### 测试栈大模型接线（从 AGENTS.md 移入，2026-10-08）

- Plinth 测试栈的大模型对话和图片请求链路是「测试客户端 → `dev-test` Plinth
  测试栈 → llms 正式服务器（llms 网关的 `/v1`，地址在 dev-test 运行环境文件 `LLM_BASE_URL`）」。llms 正式
  服务器不限制客户端与模型；测试栈对话模型为字面量 `deepseek`（2026-10-19 起网关目录已无 `qwen-max`、`glm-flash`，直接请求返回 400 `model not supported by any target`；经用户确认改用 `deepseek`，网关当前路由到 `deepseek-flash`）；请求走 OpenAI 兼容 `chat/completions`。思考参数按 Product Schema `chat.agent.thinking.model_profiles` 的模型前缀发送，DeepSeek 用 `thinking.type=enabled`、`reasoning_effort=low`（用户 2026-10-20 决定；官方只有 low/high/max 三档），工具循环回传 `reasoning_content`；其他中国大陆模型仍用官方最高档。
  测试链路上游 key 为 `<TEST_API_KEY>`（2026-09-27 用户给定，仅用于测试环境文案
  与接线）；正式生产密钥仍只存其自身运行环境文件，不写仓库。`dev-test`
  测试栈允许重部署、修改管理员口令、清空数据；但发布后必须保持上游与
  客户端接线可用，否则无法做真实验收。改接线前只读核对目标环境，改后
  核对容器生效配置及真实生成结果；模型列表和健康检查不等于生成验收。

2026-09-04 已发布 BMP 支持及 23 项系统修复，页面版本 `20260904(1)`。
精确镜像/源码、迁移对账、无扣费验收与备份恢复位置见

旧 Next 容器（名称由本地环境文件夹 `deploy/local/` 提供，仓库不写）已不存在（2026-09-02 实测 `docker ps -a` 已无
此容器）；旧数据根 `<旧 Next 根>` 封存不删。候选 Compose
项目的容器仍在、保持停止。
不要再使用 `deploy.sh`、旧 `docker compose up` 或 Prisma 命令更新服务器A
的 `dev-test`；这些只属于下方历史 Next 记录。

`dev-test` 测试数据的首次迁移已完成，针对该主机的普通 Go 版本发布不得重跑
ETL，也不得改写 `production/migration-ready.txt`。新镜像必须从已推送的
`main` 精确提交
构建，把完整 SHA 写入 OCI revision；随后使用
`GO_PROD_PROJECT=<compose_project> scripts/cutover-go-production.sh preflight --image-tag <tag>
--source-commit <sha> --no-build` 做隔离预检，再以
`docker-compose.go-production.yml` 和 dev-test 的 Compose 项目名更新单个
Go 服务；新容器或无扣费线上验收失败时恢复一致性备份和前一镜像标签。
完整命令见 `README-go.md`「后续普通发布」。

`dev-test` 测试演练证据（2026-07-29 历史记录，`<root>` 为 dev-test 发布根）：

- 备份：
  `<root>/backups/final-cutover-20260729-final-r1`
- 旧 Go 生产目录归档：
  `<root>/production-archives/20260729-final-r1`
- 对账报告：
  `<root>/production/reports/migration-final-approved.txt`
- 切换记录：
  `<root>/production/reports/cutover-record.txt`
- 切换后 Webhook 热修复发布记录：
  `<root>/production/reports/post-cutover-release-webhook-r1.txt`
- 切换后任务列表修复发布记录：
  `<root>/production/reports/post-cutover-release-tasklist-r1.txt`
- 切换后历史模型截图补迁移记录：
  `<root>/production/reports/post-cutover-release-preview-r2.txt`

切换后对账为：用户 5，Task/Attempt/账本各 1,148，积分 8,109，输出
Asset 1,543（原迁移产物 1,473 + 历史模型截图 70），输入 Asset 689，
Chat 32/193，Canvas 9/11，余额快照 232，隔离归档 141；38 个原站已缺件
任务保持 `FAILED/integrity`。另有 14 个可选模型截图在旧 Next 缓存、
历史签名 URL 和 Provider 任务中均已不存在，按“Next 缺、Go 也缺”原则
保持缺图，不得伪造截图或回退为 texture 预览。数据库 `quick_check=ok`、
外键违规为 0。

任务列表必须保持 Repository 的“两阶段读取”：先在窄行上排序/分页 Task
ID，再按 ID 批量加载完整记录。迁移数据含约 7MB 的历史 `input_json`；
恢复为宽行直接排序会重新耗尽容器 64MB tmpfs。当前 1,148 条历史任务的
默认 36 页已全量验收为 HTTP 200。

任务列表与首页的模型预览只能按 Product Schema
`task_history.assets.preview_roles` 选择 `thumbnail`、其次 `image`；
不得把 `texture` 当模型截图。历史截图补迁移优先读取旧 Next 的
pathname-hash 只读缓存，只有精确登记的 14 个原站缺失 task ID 可以保留
缺图；新增未登记缺失必须让补迁移命令失败。

无扣费生产验收：

```bash
BASE=<dev-test 的 base_url>   # python3 scripts/release-env.py show dev-test
curl "$BASE/healthz"
curl "$BASE/api/version"
```

登录、管理统计、历史资产 SHA-256、九类 Provider 可用性和 Webhook 的完整
验收脚本记录在
`scripts/post-cutover-acceptance.sh`。live Provider
有效生成会产生外部费用。`dev-test` 的小额真实模型验收按本节测试栈常设授权执行；超出该测试授权范围的生成和正式生产生成均须另行明确授权。

# ═══════════════════════════════════════════════
# 历史 Next 运维记录（只读，服务器A 禁止照此部署）
# ═══════════════════════════════════════════════

# ═══════════════════════════════════════════════
# 服务器A 旧 Next 目录结构与职责（历史说明）
# ═══════════════════════════════════════════════

## 代码目录（唯一）
```
<旧 Next 根>/app/       ← rsync 同步目标，也是 docker compose 操作目录
```
- 所有 `docker compose build / up / down` 必须在此目录执行；
- `.env` 保存在此目录，rsync 时 `--exclude '.env'` 保护，不覆盖；

## 持久化数据目录（唯一，容器实际挂载的）
```
<旧 Next 根>/storage/            → 容器内 /app/storage
  ├── data/prod.db  SQLite 测试源数据库（约 615 MB，演练前持续写入）
  ├── assets/       用户上传资产（约 1.5 GB）
  ├── audit/        审计资产（约 14 GB）
  ├── cache/        Provider 资产缓存（约 16 GB）
  ├── generated/    AI 生成图片（约 2.7 GB）
  ├── thumbnails/   缩略图（约 25 MB）
  └── task-inputs/  任务输入文件（约 275 MB）

<旧 Next 根>/docker-data/cache/  → 容器内 /app/.next/cache
  （Next.js ISR/构建缓存，体积较小）
```

以上路径与体积于 2026-07-28/29 Go 全量迁移演练时重新核验。`dev-test` 测试
源数据库真源是 `storage/data/prod.db`；旧文档曾写成
`docker-data/db/prod.db`，
该位置不得再用于备份或迁移。

## 遗留目录说明（勿删，但也不要使用）
| 路径 | 说明 | 状态 |
|------|------|------|
| `<早期代码目录>/` | 早期代码目录，历史上曾从此启动 compose，现已废弃 | 保留备查，不操作 |
| `<早期代码目录>/docker-data/` | 旧 docker-data，数据极小（68K db），未被任何容器挂载 | 废弃 |
| `<旧 Next 根>/app/docker-data/` | 中间过渡期产物，含 21MB 旧 prod.db（Mar 30），未被当前容器挂载 | 废弃 |
| `<旧 Next 根>/app/storage/` | 空目录，未被容器挂载 | 废弃 |
| `<旧 Next 根>/docker-data/db/` | 旧数据库位置，不是当前测试源真源 | 废弃 |
| `<旧 Next 根>/dev.db` | 早期遗留，未使用 | 废弃 |
| `<早期代码目录>/dev.db` | 早期遗留，未使用 | 废弃 |

## 为什么之前 docker compose down 无效
> **根因**：容器是从 `<早期代码目录>/` 启动的，docker compose 项目 label 中
> `com.docker.compose.project.working_dir = <早期代码目录>`。
> 从 `<旧 Next 根>/app/` 执行 `docker compose down` 时，
> compose 根据 working_dir 匹配不到这些容器，显示空列表，命令无效。
>
> **当前状态（2026-04-17 核查）**：容器仍运行于 `<早期代码目录>/` 项目下，
> 但挂载路径已正确指向 `<旧 Next 根>/` 数据目录。
>
> **一次性修复方法**（下次部署时顺手执行，数据不会丢失）：
> ```bash
> # 从旧目录 down（能正确停止容器）
> cd <早期代码目录> && docker compose down
> # 从新目录 up（以后所有操作统一走这里）
> cd <旧 Next 根>/app && docker compose up -d
> ```
> 两个 docker-compose.yml 挂载路径完全一致，切换不影响数据。

# 服务器A、B配置区别（其他都一样）
- 服务器A，WEB-HOOK地址：公网 Webhook 地址（见该服务器运行环境文件）
- 服务器A：MESHY_WEBHOOK_KEY（Header 名 x-webhook-key）应从该服务器对应栈的获准运行环境配置中读取；本历史记录不保存示例值。
- 服务器B，WEB-HOOK地址：公网 Webhook 地址（见该服务器运行环境文件）
- 服务器B：MESHY_WEBHOOK_KEY（Header 名 x-webhook-key）应从该服务器对应栈的获准运行环境配置中读取；本历史记录不保存示例值。
- 服务器C，WEB-HOOK地址：公网 Webhook 地址（见该服务器运行环境文件）
- 服务器C：MESHY_WEBHOOK_KEY（Header 名 x-webhook-key）应从该服务器对应栈的获准运行环境配置中读取；本历史记录不保存示例值。
- 注意：需要外部Nginx和APP内都配置单个文件20MB大小，以达成限制上传文件大小的目的；

# ═══════════════════════════════════════════════
# 部署步骤（以服务器A为例）
# ═══════════════════════════════════════════════

## 架构说明
- app 容器直接暴露 13333 端口到宿主机（docker-compose.yml 中 `ports: "13333:3000"`）；
- 无 Docker 内部 nginx，HTTPS 由服务器外部 Nginx 处理；
- 外部 Nginx 需配置 upstream 指向 `127.0.0.1:13333`（不再走 Docker 内部网络）。

### 外部 Nginx 配置变更（一次性）
移除 Docker 内部 nginx 后，外部 Nginx 的 upstream 需要从原来的 `http://app:3000`（Docker 内部网络）
改为 `http://<Docker网关IP>:13333`（通过宿主机端口直连）。
> **注意**：外部 nginx 也是 Docker 容器，不能用 `127.0.0.1`（那是容器自己），
> 需要用 Docker bridge 网关 IP（通常为 `172.17.0.1`）。

> **⚠️ 历史存档（Next.js 旧栈部署，勿用于现行发布）**：本节及以下
> 「1. 同步代码」至「4. 部署验证」描述的是已下线的 Next.js 旧部署方式
> （`<旧 Next 根>/app`、`docker compose build`）。现行 Go 栈
> 发布一律走「Agent 全自动发布协议」（`python3 scripts/release-env.py release <环境名>`），
> 生产同构环境运维见 `scripts/cutover-go-production.sh` 与
> `README-go.md`。本节仅保留作历史排查参考。

deploy.sh 会自动检测并修正，也可手动操作：
```bash
# 1. 获取 Docker 网关 IP
GATEWAY_IP=$(docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}')
# 2. 找到外部 nginx 配置中 MeshyAI 相关的 upstream/proxy_pass
docker exec nginx grep -rn 'app:3000\|meshyai' /etc/nginx/
# 3. 将 app:3000 改为 ${GATEWAY_IP}:13333
# 4. docker exec nginx nginx -t && docker exec nginx nginx -s reload
```

## 1. 同步代码（不覆盖服务器专属配置）
```bash
rsync -avz --delete \
  --exclude 'node_modules' --exclude '.env' --exclude '.env.local' \
  --exclude 'dev.db' --exclude 'docker-data' --exclude 'storage' \
  --exclude '.git' --exclude 'tsconfig.tsbuildinfo' --exclude '.next' \
  ./ <dev-test 的 ssh>:<旧 Next 根>/app/
```

## 2. 构建镜像
```bash
ssh <dev-test 的 ssh> \
  "cd <旧 Next 根>/app && docker compose build --no-cache app"
```

## 3. 重启容器
```bash
ssh <dev-test 的 ssh> \
  "cd <旧 Next 根>/app && docker compose up -d"
```
> ⚠️ 如果这是首次从 `<旧 Next 根>/app/` 启动（而非 <早期代码目录>/），
> 请先执行：`cd <早期代码目录> && docker compose down`，再执行上面命令。
> 执行一次后，之后所有操作统一在 `<旧 Next 根>/app/` 下进行即可。

## 4. 部署验证
```bash
#  健康检查（直连 app 端口）
curl <dev-test 的 base_url>/api/health
#  WebHook（应返回 401）
curl -X POST <dev-test 的 base_url>/<webhook 路径>/
#  页面访问：浏览器打开 dev-test 的 base_url 确认可登录
```

# ═══════════════════════════════════════════════
# 全新服务器初始化（仅首次部署需要）
# ═══════════════════════════════════════════════

## 1. 安装 Docker
```bash
#  国内服务器可能无法访问 Docker 官方源，改用阿里云镜像：
apt-get update && apt-get install -y ca-certificates curl gnupg
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://mirrors.aliyun.com/docker-ce/linux/ubuntu/gpg | \
  gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
  https://mirrors.aliyun.com/docker-ce/linux/ubuntu $(lsb_release -cs) stable" | \
  tee /etc/apt/sources.list.d/docker.list
apt-get update && apt-get install -y docker-ce docker-ce-cli containerd.io \
  docker-buildx-plugin docker-compose-plugin
systemctl enable --now docker
```

## 2. 配置 Docker 镜像加速（拉取基础镜像用）
```bash
cat > /etc/docker/daemon.json << 'EOF'
{
  "registry-mirrors": [
    "https://docker.1ms.run",
    "https://docker.xuanyuan.me"
  ]
}
EOF
systemctl restart docker
```

## 3. 创建目录结构
```bash
mkdir -p <旧 Next 根>/app
mkdir -p <旧 Next 根>/storage/{assets,audit,cache,generated,task-inputs}
mkdir -p <旧 Next 根>/docker-data/{db,cache}
```

## 4. 创建 .env
```
#  参考服务器A的 .env，修改以下字段：
#  - NEXT_PUBLIC_APP_URL → 新服务器地址
#  - MESHY_WEBHOOK_KEY  → 新服务器对应密钥
#  - 其他字段保持不变
```

# ═══════════════════════════════════════════════
# 注意事项
# ═══════════════════════════════════════════════

## 环境差异
服务器上环境与本机不一样（Docker HostName、运行配置等），
不能沿用本项目的 .env，需以服务器实际情况调整。
目前服务器已调整好，若部署到新服务器则需重新配置。
端口已固化在 docker-compose.yml 中（`13333:3000`），部署时无需 sed 修正。

## 数据库迁移
docker-entrypoint.sh 会自动执行 prisma migrate deploy。
若遇到迁移冲突（如之前用过 prisma db push），需手动解决：
```bash
  docker compose stop app
  docker compose run --rm app npx prisma migrate resolve --applied <迁移名>
  docker compose up -d
```

## Docker 镜像加速
国内服务器无法直接访问 Docker Hub，构建前需配置镜像加速：
  /etc/docker/daemon.json → registry-mirrors
构建失败并提示 "i/o timeout" 时，请检查此配置。

## 每次部署后必须验证
1. 网站可访问、可登录
2. WebHook 通讯正常
3. AI 对话出图可正常创建会话和生成图片
