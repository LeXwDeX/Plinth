# dev-test 冒烟与发布脚本

Agent 控制的 `dev-test` 发布与冒烟协议（按环境名部署与冒烟见 `docs/deployment.md`：`python3 scripts/release-env.py smoke <环境> -- bash scripts/smoke/<脚本>` 为同一套 L0/L1/L1 Agent 脚本设置 `BASE`、管理员登录、`EXPECT_SHA` 与 `RELEASE_CONTAINER`；在目标主机运行 L1 Agent 时另设 `ART_AI_SERVER_ROOT=<发布根>`）。各环境相互隔离；部署与验收事实以对应 Issue 回执为准（`legacy-formal` #232，`formal-test` #270）。操作边界见 `dev_ops.md`「Agent 全自动发布协议」及其两栈边界一节。

## 发布（Agent 本地一键执行，无 CI）

```
git push origin main                       # 待发布内容先进 main
python3 scripts/release-env.py release dev-test   # 同步→构建→preflight→备份→Compose 更新→健康（驱动 release-go-testenv.sh）
```

- 凭据只在本地，远端命令通过本地 `deploy/local/environments.json` 中该环境的 `ssh` 执行。测试栈发布由 `python3 scripts/release-env.py release dev-test` 驱动 `scripts/release-go-testenv.sh`（`RELEASE_HOST`、`RELEASE_ROOT` 必填，无缺省主机/根）执行；正式（`legacy-formal`）晋级命令与来源验收 note 格式见 `dev_ops.md`。正式镜像晋级要求与该完整 SHA 匹配的 test manifest 项和 OCI revision，并验证测试栈在晋级期间身份不变。
- 正式栈 `legacy-formal` 使用独立的无 Provider Key 环境和 `SESSION_COOKIE_NAME=art_ai_formal_session`。免费冒烟只跑 L0/L1，不跑 L2/L3/L4 或任何真实生成。命令显式设置 `BASE`、`TEST_BASE` 和正式管理员环境变量，例如：

  ```bash
  BASE=<legacy-formal 的 base_url> \
  TEST_BASE=<dev-test 的 base_url> \
  ADMIN_USERNAME='<正式管理员名>' ADMIN_PASSWORD='<正式管理员密码>' \
  python3 scripts/smoke/smoke-l1-formal-keyless.py --expect-empty
  ```

  `BASE` 必须等于本地环境文件（`deploy/local/environments.json` 或 `ART_AI_ENVIRONMENTS_FILE`）中 `legacy-formal` 条目的 `base_url`，否则脚本拒绝运行；地址用 `python3 scripts/release-env.py show legacy-formal` 查看。`--expect-empty` 只用于正式首次晋级后、尚未有正式用户数据时；后续例行验收省略该参数。检查只读健康、readiness、公开 Schema、Offerings、会话/任务列表和鉴权闭合，不创建会话、Task、Grant、消息或审批，不调用 LLM；脚本要求每个 Offering 有 `provider_id`，核验所有 `provider_id != local` 项不可用，且至少一个本地项可用。它捕获 formal 登录签发的真实 Cookie，先向 `TEST_BASE` 发送该 Cookie 的两组只读 GET：`/api/auth/session` 必须返回匿名用户与 `isAdmin=false`，`/api/v1/tasks?limit=1` 必须返回 401；再将同一 secret 错按 test 默认 cookie 名重放，两项请求也必须被拒绝。随后在 formal 登出，并用独立 opener 重放登录时捕获的原 Cookie，必须看到匿名 session 和 401 tasks，证明该真实 Cookie 已撤销。它不会登录 test、读取 test 凭据或发送 test 写请求。`TEST_BASE` 必须显式提供且不能等于 `BASE`。正式管理员通过受支持的 BootstrapAdmin 初始化；首次成功登录后移除启动用 `ADMIN_PASSWORD` 并重建验证，原凭据 JSON 保持本地 `0600` 文件并只交用户。
- `smoke-l0-browser-boot.sh`、`smoke-l1-free.sh`、`smoke-l1-agent-mode.sh` 对非默认 `BASE` 都要求显式完整 `ADMIN_USERNAME` 与 `ADMIN_PASSWORD`，避免误读测试运行环境凭据。L1-free 会调用 Agent，不可用于无 Key formal 验收；formal runner 保持只读 L1 范围。
- 候选启动或健康检查（healthz/readyz）失败时，按本次备份范围恢复数据并启动发布前实际运行的镜像。标签从原容器读取。失败报告写在服务器 `production/reports/release-failures/<tag>-<stage>.txt`，并带 `backup_scope`/`backup_targets`。
- 默认备份 `data`、`storage-go`、`web-data`，且不自动清理历史备份。仅 `dev-test` 可在环境文件写 `"backup": {"scope": "database", "keep": 2}`：停机只备份 `production/data`，回滚只替换该目录；候选写入的 `storage-go`/`web-data` 字节保留，可能成为孤儿，因此不是完整回滚。手工回滚先读 `backups/release-<T>/scope`（单行 `database` 或 `full`；旧快照无 scope 且三个归档齐全时按 `full`）。离线故障注入回归：`bash scripts/smoke/test-release-rollback.sh`（含 database scope）以及实际 helper 的单行 scope 夹具（`test-release-stack.sh`）。
- 构建或 preflight 失败直接中止——旧实例未被触碰，无需回滚。
- 测试栈健康检查与成功清单写入后，自动执行 Plinth 镜像保留：按创建时间保留 `art-ai-go-production` 最新五个不同镜像 ID，额外保护运行镜像、发布前实际运行的回滚镜像，以及发布时传入的测试栈、候选栈与 `legacy-formal` Compose 项目（`--project`，名字来自本地环境文件）任意运行或停止容器引用的镜像和 formal pins 中的 current/rollback/pending 镜像。正式发布与清理共用锁；formal 根存在而 pins 缺失或无效时停止清理。保护项可能使数量超过五个。只用无强制的 `docker rmi <id>`，失败记警告，不改变发布成功结果；镜像工具不删容器、卷、数据库备份或其他仓库镜像。正式晋级不触发清理。dry-run 见 `dev_ops.md`。
- database 模式在健康检查与成功清单写入后，再调用 `scripts/retain-release-backups.py --apply`，`keep` 为 1–2（当前配置 2）；正常清理最多保留最新 2 个已结束发布备份，更早失败快照也按最新 2 个回收。进行中快照可临时排除，失败报告与独立诊断日志保留。bootstrap、失败、回滚和预构建路径不清理；清理非零只警告，不改变发布结果。helper 只处理 `backups/release-*`，不碰原始资产、数据库、首次迁移冷备或其他栈备份。保留 database-only 快照可能被使用时，禁止离线 CAS/task 破坏性维护。
- 备份目录在停机前创建，并写入单行 `scope` 文件（`database`/`full`）。备份目标（targets）在成功后的 `production/compose.env` 和失败报告中记录，快照内不另存 targets。目录准备失败时旧实例继续运行。已有同名备份不会被删除；重复标签须换用新的发布标签。
- 停机或备份失败时，先重新启动并检查旧实例，再写失败报告。报告目录不可写时仍执行恢复。健康请求的连接超时为 3 秒，总超时为 5 秒；重试次数有上限。
- 线上无法在线解决的问题：记录报告 → 回滚 → 回到本地修复后再发布。
- 同一发布根的发布备份/恢复、备份清理与手工恢复共用 `<root>/.release.lock`：`release-server-steps.sh` 在构建或改动数据前持锁，清理 helper 校验并继承该描述符；缺失或身份不符即拒绝。身份检查后直接 `flock` 继承描述符：共享描述符复用锁，空闲描述符先取得锁，其他进程持锁则 busy；借用方不得解锁。手工恢复必须在 `release-lock.py run` 下执行，详见 `docs/deployment.md` §7。删除前还拒绝快照树内的挂载点（含同设备 bind mount），无法读取挂载表时失败关闭。
- 失败快照可能有零字节或缺失归档，只有在独立失败报告存在时才可清理；成功备份仍要求完整且非空的归档。删除资格不代表可作为恢复点。
- 发布合同回归：`python3 scripts/tests/test_release_env.py`、`python3 scripts/tests/test_release_lock.py`、`python3 scripts/smoke/test-release-backup-retention.py`、`bash scripts/smoke/test-release-stack.sh`、`bash scripts/smoke/test-release-rollback.sh`。这些是本机 mock 合同测试，不代替真实部署或远端清理验收。

## 冒烟分层

| 层 | 脚本 | 何时跑 | 费用 |
|---|---|---|---|
| L0 浏览器启动 | `smoke-l0-browser-boot.sh` | **每次发布后必跑** | 零（不产生任何生成任务） |
| L1 免费冒烟 | `smoke-l1-free.sh` | **每次发布后必跑** | 零（先把会话设为手工方式，再用 3D 请求取得待批准卡片，停止本轮后确认卡片仍待批准，再拒绝；无任务、无真实生成） |
| L1 Agent 批准方式验收 | `smoke-l1-agent-mode.sh` | **每次发布本功能集（批准方式、Agent 3D 工具、格式补齐/ZIP、设定摘要、选定、版本链、重试）后必跑**；与 L1 一起跑 | 零（不提交任何生成，只读已有 3D 任务） |
| L2 真实模型链路 | `smoke-l2-livemodel.sh` | 涉及 Agent/生成/同意链路的发布后跑 | 一张小额文生图（约 6 积分）；设 `L2_APPROVE_3D=1` 再加一次 3D 生成（约 25 积分） |
| L3 批量回归 | `smoke-l3-batch.sh` | 涉及生成/同意/资产链路的发布后跑（按需） | 每模型 5 张小额文生图 |
| L4 3D 格式与隔离 | `smoke-l4-3d-isolation.py` | 3D Provider、格式或缓存边界变更后跑 | Meshy 一次生成；Tripo 一次生成及六次转换；`ART_AI_3D_PROVIDERS=hyper3d` 时一次 Rodin 文生 3D（3 Hyper3D credits，只核必需 GLB） |
| 公共 Mock 自部署冒烟 | `smoke-public-mock.sh` | 自部署后本地手动运行（`SMOKE_ENV_FILE=.env bash scripts/smoke/smoke-public-mock.sh`）；不再由公共 CI 运行（#257：CI 不构建镜像） | 零（只接受 Mock Provider/Agent、全部 `*_API_KEY` 为空的回环地址栈）；不用于内部环境 |

L0–L4 各层在 `dev-test` 主机上执行时用 `ART_AI_SERVER_ROOT=<root> bash scripts/smoke/<脚本>`（`<root>` 为该环境发布根，读取本栈 `runtime.env`；L1 Agent 要核对容器 revision 时另设 `RELEASE_CONTAINER=<release-env.py show 的 container>`，脚本不再带缺省容器名，#261）；在本地执行时用 `python3 scripts/release-env.py smoke dev-test -- bash scripts/smoke/<脚本>`（自动设置 `BASE`、`ADMIN_*`、`EXPECT_SHA`、`RELEASE_CONTAINER`）。

### 批准方式对冒烟的影响（会话级，迁移 0043）

- 每个会话有批准方式：`auto`（所有付费步骤自动批准）或 `manual`（默认）。手工方式下**只有** `text-to-3d`、`image-to-3d`、`retexture` 需要人工批准；2D、重拓扑、绑骨、动画和格式转换由策略批准（`grant.decided_by` 为 `policy:manual`），不出现待批准同意，直接产生真实任务。格式转换包括免费的本地 GLB↔glTF 和上传 GLB→STL（当前 Schema 基础估算 5 积分）；具体费用以 Offering 和冻结授权为准，策略批准不表示免费；Agent 步骤带额外积分（如附加导出格式、有计费的转换选项）时，手工方式下也要在对话里的批准卡上人工批准。规则真源是 Schema `chat.approval_policy`。
- 因此待批准同意只能用 3D 请求取得。L1、L2 在需要待批准同意时都先 `PUT /api/v1/chat/sessions/{id}/approval`（`{"approvalMode":"manual"}`）显式设为手工方式，再发 3D 请求；3D 模型取自 `/api/v1/offerings?capability=text-to-3d`（第一个可用、推荐、无额外计费上限的模型，`SMOKE_3D_MODEL` 可覆盖）。**不要再用 2D 请求去等待同意**：它会花钱，并让免费冒烟变成付费冒烟。
- 脚本代批同意只发生在同意确实待批准时（L2 的 3D 批准、L3 兜底、L4 的 `status=pending` 授权）；已由策略批准的授权不再 `PUT` 决定（会得到 409）。

- L2/L3 都通过已登录 Cookie 写会话、发消息和批准/拒绝时携带同源 `Origin: $BASE`。L2/L3 必须先让 Agent 调用实际生成工具（L2 的 3D 部分 `create_model_3d`，L2 的 2D 部分和 L3 每张图 `refine_image`），以生成工具建立的 exact prepared execution 作为待批准对象；不依赖旧 `request_consent` 流程。批准卡片（2026-10 起）独立于轮次保存，旧的 `/api/v1/agent/consent` 不再反映它；脚本先 `GET /api/v1/chat/sessions/{id}/pending-approvals` 读取状态为 pending 的卡片及其 `request_id`、`capability`，再 `PUT /api/v1/tasks/grants/{request_id}` 提交 `{"decision":"approve"}` 或 `{"decision":"deny"}`。L2 停止 A 后和拒绝 B 后会拒绝会话里剩下的待批准卡片；L3 只批准 2D 图片步骤，其他卡片一律拒绝。批准动作只发生在产品外 smoke 脚本，产品本身仍要求明确人工批准。
- L3 协议（2026-09-28 用户确认）：每模型每轮 `IMAGES_PER_MODEL` 张（默认 5，**协议上限 5**），满一轮即止不循环；零人工（脚本代批同意仅限产品外 smoke 操作，属测试操作授权）；断言 = 终态 SUCCEEDED + required 资产 READY + URL 200 + media_type 魔数一致 + sha256/size 与服务端声明字节级一致 + 连续回合必须出现**新 task id** + 跨用户任务/资产 403/404；报告缺省落服务器 `$ART_AI_SERVER_ROOT/production/reports/smoke-l3/`（`L3_REPORT_DIR` 可覆盖）。
  - 用法：`ART_AI_SERVER_ROOT=<root> MODELS=gpt-image-2.5-sunburst bash smoke-l3-batch.sh`（`MODELS` 缺省 = schema 全部可用 text-to-image 模型；`IMAGES_PER_MODEL` 缺省 5）。
  - L3 不再等待同意：每个会话先设为手工方式；2D 由策略批准，脚本直接等新任务出现。若同意恰好待批准，仍由脚本按 pending-approvals → grants 批准后继续；两种情况都继续断言终态与字节。
  - 满一轮无问题 → **暂停分析**，不继续加图（dev_test.md §11.3 口径）。
  - 红证已做：`MODELS=nonexistent-model` → FAIL（EXIT=1），失败可被检测。

- L0 覆盖：真实浏览器加载 SPA、**CSP 未拦截首屏脚本**、工作区首页可渲染。
  这是 CSP 解封门禁二的运行期那一半——`internal/transport/webui/shell_csp_test.go`
  只断言响应头里含脚本 `sha256`，L0 才验证浏览器真的把它执行起来了。
  有 `ADMIN_USERNAME`/`ADMIN_PASSWORD` 时先登录再校验工作区首页，否则只校验公开登录页。
  依赖 playwright（不进发布依赖，按 `PLAYWRIGHT_MODULE_PATH` 或本地安装解析；
  首次需 `npx playwright install chromium`）。
- 详情弹窗几何回归（#224）：`result-detail-geometry.mjs` 登录后打开“我的资源”里一个多候选任务，在 1440×900、1280×720 与 390×844 下连续点击“下一个”并用公开 Schema 声明的上一个/下一个键往返翻页，记录 `result-detail-prev`/`result-detail-next`、弹窗与预览框的 `getBoundingClientRect()`，断言位置和尺寸变化 ≤ 1px、图片加载前后弹窗高度不变、滚动到底再回顶不推动翻页行，并核对小视口下滚动区仍可用。零费用、只读、不创建任务；证据截图与 JSON 仅在设置 `SMOKE_EVIDENCE_DIR` 时写入该目录。用法：`BASE=<dev-test 的 base_url> ADMIN_USERNAME=... ADMIN_PASSWORD=... SMOKE_EVIDENCE_DIR=<绝对路径> node scripts/smoke/result-detail-geometry.mjs`。
- L1 覆盖：健康检查、Schema 投影、SPA 安全头、登录、CSRF 拒绝、会话/任务 API、**等待批准→停止免费路径**（用 3D 请求在手工方式下取得待批准卡片；读 `GET /api/v1/chat/sessions/{id}/pending-approvals`，不再读旧的 `/api/v1/agent/consent`，后者不反映独立于轮次的批准卡片；停止本轮后断言卡片仍是 pending，再用 `PUT /api/v1/tasks/grants/{request_id}` `{"decision":"deny"}` 拒绝，最后断言会话零任务）。
  - L1 不能阻止 LLM 误调用 2D 工具（手工方式下 2D 由策略批准，会直接产生付费任务），但等待批准卡片期间一旦会话里出现任务就立即停止本轮，打印 `PAID-TASK-ALERT` 并计 FAIL（退出码 1）；只有会话零任务且批准卡片未出现时才是 `SOFT-FAIL`。出现 `PAID-TASK-ALERT` 时请人工复核该会话。
- L1 Agent 批准方式验收覆盖（`smoke-l1-agent-mode.sh`，零费用、不提交生成、不调用 LLM）：healthz/readyz；构建版本（`EXPECT_SHA` 必填，`/api/version` 与容器 revision 都必须等于它）；Schema 批准策略；新会话默认手工、`PUT` auto 再回读、`PUT` manual、跨源 `PUT` 返回 403、非法方式返回 400；设定摘要读写往返、2000 字符可存、2001 字符返回 4xx 且不被截断；选定对象为空、未知任务的 `PUT` 返回 4xx、`DELETE` 成功；版本链对新会话返回 200；随机未知任务的 `formats`、`archive`、`retry`、`formats/complete` 都是 404；登录用户在**已获取的有界候选集内**最新 applicable 的 SUCCEEDED 3D 任务（自有会话，按创建时间；任务列表按 Schema `model_delivery.capabilities` 每个能力每页 100，最多检查 500 个去重候选，不保证是全局最新或穷尽）的 `GET formats`（200，Schema 的 7 种格式 ID 精确集合、无重复、每项 `id`/`status` 为字符串）和归档契约：`applicable` 与 `archive_available` 必须是 JSON 布尔值；字段缺失、类型错误、格式清单读取失败都 FAIL，不静默改用另一个任务。`archive_available=true` 时 `GET archive` 必须通过同目录 `read-archive.sh` 的真实 ZIP 检查（200/206、首项 `README.txt`、README 内无 `http(s)://` 地址；已知总量低于 `ARCHIVE_MAX_BYTES` 时做完整 ZIP 检查，否则按其文档化上限只做首项检查）。`archive_available=false` 时 `GET archive` 必须返回 413 且 JSON `code=too_large`；409/empty 或其他拒绝都 FAIL，不视为可用归档；随后在同一 500 候选列表内找一个 `archive_available=true` 的既有自有任务执行真实 ZIP 检查，找不到则 FAIL。完全没有自有 SUCCEEDED 3D 任务时才 SKIP；已发布 Agent 配置含 `update_session_notes`、`retexture_model`、`process_model`、`list_session_models`、`plan_model_processing`、`get_processing_plan`、`find_history_images`；另外从公开 Schema `runtime.http.routes.offerings` 指定的端点查询 `capability=format-conversion`，至少一个 Offering 的 `available=true` 时，将 `schema.chat.tools` 中同 capability 的工具加入必需集合（当前为 `convert_model_format`），管理员配置与非管理员 effective 回退复用该集合；全部不可用时不追加。只读 discovery 不执行转换、不调用 LLM、不创建 Task。缺工具时 FAIL，并提示保留自定义配置、补齐工具后重新发布；公开 Schema/discovery 的非 200、JSON 损坏、列表或 `available` 布尔类型错误、可用转换缺少工具映射，以及配置响应的必需字段缺失或类型错误，同样 FAIL，不打印响应原文。
  - 两栈日志（#233）：在服务器上运行时还检查本栈 `production/logs/art-ai.log`（可用 `ART_AI_LOG_FILE` 覆盖）存在，且最近一条 `日志已配置` 为 `log_level=DEBUG`、`directory=/app/logs`；目录可见但文件缺失或级别不对计 FAIL。
  - 用法：`ART_AI_SERVER_ROOT=<root> EXPECT_SHA=<origin/main 完整 SHA> bash smoke-l1-agent-mode.sh`（服务器上）。输出末行 `PASS/FAIL/SKIP` 计数，有 FAIL 退出码为 1。
  - 构建版本（#229）：`/api/version` 返回构建时以 ldflags 链接进 `art-ai` 二进制的提交，与容器 OCI 标签 `org.opencontainers.image.revision` 同源（都来自 `GO_SOURCE_COMMIT`）；未注入完整 40 位 SHA 的本地构建返回 `unknown`。脚本严格断言 `/api/version` = `EXPECT_SHA`，在服务器上再断言容器 revision = `EXPECT_SHA`；缺 `EXPECT_SHA` 或不一致都是 FAIL，不再 SKIP。
  - ZIP 验收依赖同目录的 `read-archive.sh` 和 `check-archive.py`，复制脚本到服务器时三者须放在同一目录。先请求 Range `0-4194303`，即使返回 200、忽略 Range 或没有 Content-Length，也在前 4 MiB 主动断开；仅在达到字节上限时容忍 curl 的写管道错误/SIGPIPE。`ARCHIVE_PARTIAL_TIMEOUT` 默认 59 秒，对齐 #159「前 4 MiB 在 60 秒内到达」的要求，独立于普通请求的 60 秒。校验 HTTP 200/206、`application/zip` 和 ZIP 本地文件头；支持 stored/deflate 与数据描述符，解压后的 README 最多 4 MiB，并核对完整结束、大小和 CRC；同时检查布局（#222）：README 列出的路径必须安全；只有 OBJ 包可以用文件夹，且模型必须在同一文件夹有实际随行文件（README 里只标注“未保存”的引用不算），OBJ 的材质与贴图必须在 OBJ 所在文件夹内（允许贴图子目录），其他附件必须在 `other/` 下（只剩依赖文件、没有模型的包不按模型判定）；完整 ZIP 双向核对——README 列出的携带路径都必须是实际条目，每个条目也都必须被 README 描述。零字节预检超时或未到上限的读取超时仍为 FAIL，不因已收到 README 而容忍 curl 28。
  - #159 后端修正：Archive 仍在响应前完成全部资产摘要及隔离检查。对目标任务的七个已有资产串行采样，原隔离检查合计 84.257 秒，热点是二进制窗口上的重复地址正则匹配，不是 SHA256。加入每次原始/解码正则匹配前的必要字面量预筛后，同一诊断流程降至 29.485 秒；所有资产仍通过完整检查。这是临时 helper 的测量，不是部署后 HTTP 验收。预筛保留原正则、三轮解码、UTF-16 投影和重叠窗口，不缓存或省略安全检查。59 秒期限与该后端修正一起交付，不以放宽超时替代性能修复；实际前 4 MiB 延迟仍须发布后测量。
  - 总大小按 Content-Range 总值或 200 响应的 Content-Length 判断：已知且低于 `ARCHIVE_MAX_BYTES`（默认 200 MiB）时保留完整 `zipfile` 检查；否则只做首项检查并明确输出 `partial check only`，不是对中央目录、其余条目或完整 ZIP 的验收。2026-10-18 #223 起 Go Handler 支持 `Accept-Ranges: bytes` 与单区间 Range：带 `Range` 的请求先物化 ZIP，再由 `http.ServeContent` 返回 206、`Content-Range` 与 `Content-Length`；无 `Range` 的普通响应仍是流式 200、不显式设置 Content-Length。因此脚本的首个 Range 请求通常能读到总大小并走完整检查。
  - 离线自测（不用凭据、不访问服务器）：`python3 scripts/smoke/test-archive-check.py`。覆盖 stored/deflate、数据描述符、错误首项、README 地址、损坏/截断、59 秒默认期限，README 布局（单模型在根目录、OBJ+随行依赖保留文件夹、缺失引用不构成理由、`other/`、仅依赖包、不安全与未知路径），以及本机隔离 HTTP 的 206、忽略 Range 的 200、未知大小、小包全量检查、零字节预检超时和已收到 README 后的读取超时。后端差分与基准：`go test ./internal/assets/local -run 'TestAddress(Scan|Marker)'`、`go test ./internal/assets/local -run '^$' -bench BenchmarkAddressMarkerPrefilter -benchmem`。镜像保留 mock 测试：`python3 scripts/smoke/test-image-retention.py`。发布环境合同（默认命令逐字不变、环境文件、晋级拒绝、bootstrap）：`bash scripts/smoke/test-release-stack.sh`。
  - Agent 配置读管理员接口 `GET /api/admin/agent-configuration`（非管理员账号退回 `GET /api/v1/agent-configuration/effective?creative_domain=model-3d`）。已发布的旧配置不会自动带上新工具；优先读取当前版本，保留系统提示、Skill、既有工具及其他自定义设置，仅补齐缺失的工具后重新发布，使用当前 `expected_revision` 和同源 `Origin`，再回读生效配置核对。只有明确要恢复默认时才在管理页「Agent 配置」重置，或调用 `POST /api/admin/agent-configuration/reset`（同样带 `expected_revision` 和同源 `Origin`）；重置会把系统提示、Skill 和工具恢复为默认，不能用于无差别补工具。
  - 最新任务归档契约离线自测（不调用网络）：`python3 scripts/smoke/test-agent-mode-archive.py`。执行冒烟脚本第 9 节并用 stub 响应覆盖最新超大归档 + 较旧可下载任务回退、`archive_available=true` 却 413、false 却 200/409、字段缺失/类型错误、错误 413 code、无可用归档、无 3D 任务 SKIP、候选上限与最新优先/去重，以及 formats 缺失/列表类型/坏元素/坏 status/重复或错误 ID 时立即 FAIL 且不请求 ZIP。该离线测试里的 `read-archive.sh` 是控制流 stub；真实 ZIP 200/206、README、字节与解析校验由 `python3 scripts/smoke/test-archive-check.py`（17 个测试）覆盖。
  - Agent 配置离线自测（不调用网络）：`python3 scripts/smoke/test-agent-mode-configuration.py`。直接执行冒烟脚本中的配置检查块，用 stub 响应覆盖旧四工具配置拒绝、六工具配置通过、非管理员生效配置回退、分别缺失两个 Processing Plan 工具、已知工具目录不匹配或为空、配置接口不可读，以及 JSON 损坏、必需字段缺失和类型错误；另覆盖按实际可用性要求转换工具、全部不可用不追加、Schema 工具改名和路由映射、管理员/effective 两条分支，以及 discovery/Schema 的 HTTP、JSON、字段和类型错误；共 73 个响应场景，并核对只请求声明的只读端点、错误不泄露响应内容。
- L2 覆盖两部分。第一部分：2D 手工方式 = 策略批准——一张小额文生图，不出现待批准同意，任务 SUCCEEDED，批准来源为 `policy`，显示本步和会话累计积分；脚本能读到 SQLite（`ART_AI_DB`，服务器上缺省 `$ART_AI_SERVER_ROOT/production/data/art-ai.db`）时再核对 `grant.decided_by = policy:manual`，读不到打印 SKIP。第二部分：3D 手工方式 = 人工批准——A/B 双会话分别触发确切的 3D 生成待批准执行、不冻结 C；拒绝 B 并断言零任务；A 默认停止本轮并断言零任务，只有设了 `L2_APPROVE_3D=1` 才批准 A（一次真实 3D 生成，约 25 积分）并断言 SUCCEEDED。从 GET 读取对应 request ID 后才发送决策，未读到当前 request ID 时不发送。
- 测试数据按协议**保留**在 `dev-test`，不清理；发布备份快照是另一类数据，按 #364 的保留规则处理。
- L4 在 `dev-test` 主机的 Plinth 测试栈运行：先从 `<root>/production/runtime.env` 导出管理员登录环境变量，再以 `ART_AI_LIVE_3D_ACK=1 python3 scripts/smoke/smoke-l4-3d-isolation.py` 执行。脚本作为测试操作员批准处于 `pending` 的冻结任务（会话批准方式已批准的授权不再重复决定），等待公开 Task 成功，核对 Meshy 六格式与 Tripo 六格式的本地 URL、两次下载字节、大小和 SHA-256；不会输出密钥或原始上游响应。`ART_AI_3D_SESSION_ID` 可让第一个 3D 任务接在已有 2D 生成会话中，用于工作区往返验收。它不代替真实浏览器网络检查与离线依赖检查。
- **2D 成品图的抽样口径见 `dev_test.md` §11.3**（每轮随机 1–5 张、不超过 5 张；
  满 5 张仍无问题就暂停先分析，不要继续加图）。本目录不另定标准。
- 单元/集成/竞态门禁由本地门禁与 CI 覆盖（各项在哪里执行见 `dev_test.md` §2），不属于本目录职责。

## 可控恢复验收夹具（隔离进程）

### Opt-In Wire Capture

`TestWireCaptureServer` is a separate, test-only HTTP server for later browser-driven
capture. It does not use `TestAcceptanceServer`, which forces a mock Agent and does
not install the recorder hook. Ordinary `go test` skips the interactive entrypoint.
The implementation has offline verification only: no live corpus or browser capture
is claimed, and #365 is not complete.

Prepare a caller-owned, real 0700 directory **outside every checkout**, without bind
mounts. It must contain `catalog.json` (a reviewed Product Schema copy) and
`capture.json`, both caller-owned regular 0600 files. Do not reuse a shared data
directory or an earlier `run/`. Frontend files are read-only; build them separately
and provide the absolute directory containing the built `index.html`.

The private manifest has this shape. Replace the placeholders before use. The
small policy below illustrates registration, not a policy that covers live chat
or generation. Register the exact reviewed keys, public strings/numbers and
terminal aliases needed by the chosen scenario; never approve whole user prompts
or private content merely to make export pass. `policy_name` must itself be in
`Allowed`. Host aliases terminate at `example.test`, which must also be allowed
for policy preflight. All selected connections must be configured and actually
assembled. Chat IDs come from `chatConnections` (default `LLM`); Provider IDs come
from `application.runtime.provider_connections`. Account readers are not selectable.

```json
{
  "address": "127.0.0.1:18765",
  "source_sha": "<40 lowercase hex characters>",
  "catalog_sha256": "<64 lowercase hex characters>",
  "web_assets_root": "<absolute built frontend directory>",
  "scenario": "reviewed-scenario",
  "policy_name": "reviewed-v1",
  "duration_seconds": 900,
  "connections": ["LLM"],
  "policy": {
    "MaxBodyBytes": 8388608,
    "Keys": {"error": true, "message": true},
    "Allowed": {"reviewed-v1": true, "example.test": true, "v1": true, "chat": true, "completions": true},
    "Aliases": {"<actual lowercase upstream host including port if any>": "example.test"}
  }
}
```

Commands below run from the exact source worktree. `CAPTURE_ROOT` is the prepared
external private directory. Use an inspected source revision; an injected SHA is
a build label, not proof that a dirty source tree matches that commit. Do not use
`-trimpath`: the test binary needs its existing build-time source worktree to
check directory isolation. Keep the source worktree available while running.

```bash
git status --short
git rev-parse HEAD
shasum -a 256 "$CAPTURE_ROOT/catalog.json"
go test -c -ldflags "-X github.com/LeXwDeX/Plinth/internal/app.wireCaptureSourceSHA=$(git rev-parse HEAD)" -o "$CAPTURE_ROOT/wire-capture.test" ./internal/app

# Supply only authorized test credentials through the process environment.
# No credentials belong in the manifest, command arguments or public reports.
PROVIDER_MODE=live AGENT_MODE=live \
ART_AI_WIRE_CAPTURE_ACK=isolated-wire-capture-only \
ART_AI_WIRE_CAPTURE_ROOT="$CAPTURE_ROOT" \
"$CAPTURE_ROOT/wire-capture.test" -test.run '^TestWireCaptureServer$' -test.v -test.timeout 0
```

The server uses the manifest's numeric loopback address and port; wildcards,
hostnames, port zero and non-loopback addresses are rejected. Wait for its normal
readiness route (`/readyz` in the default Catalog) before opening that address in
an isolated browser. Login uses the test-only mock-auth path for a fixed non-admin
owner. Product authorization and paid approval policies still apply. The ACK is
not permission to spend money. Do not expose the port with a proxy or tunnel.
The process stops after the declared 1–3600 seconds, or on SIGINT/SIGTERM. For a
compiled binary, Ctrl-C initiates normal shutdown; do not kill it forcibly if a
complete candidate is needed.

`run/` contains fresh SQLite/storage/cache directories, a random isolated session
secret in memory, frozen Catalog/manifest copies, private binary evidence and
`events.jsonl`. Logs retain only time and severity; upstream messages/attributes
and raw HTTP server diagnostics are suppressed to avoid credential disclosure.
There is no full diagnostic log, log rotation, resume, seed import, fixture
substitution or automatic cleanup. Keep this private directory private after
failure; do not upload it or its manifest (aliases can contain private values).

Only selected Provider/LLM transports are recorded. Unselected transports and
account-balance reads remain normal and unrecorded: this is **not a network or
spending sandbox**. Select Tripo's `<connectionID>.native-source` separately when
needed; the wrapper preserves its existing DNS/IP pinning, TLS, no-proxy and
no-redirect client. HTTPS base URLs and policy preflight do not prove every later
dynamic URL/body is supported. Unregistered query keys, unsupported media, inline
binary content, incomplete streams and sanitizer failures invalidate export.
Registered query keys are accepted, but their values are never retained. Supported
binary bytes are spooled privately (2 GiB per body, 4 GiB per round) and the
deferred exporter replaces them with fixed synthetic recipes before writing a
public candidate; unknown captured media types still fail closed. Never retry a
paid action just to repair a capture failure.

After clean application shutdown, every selected connection must have at least
one exchange and `Recorder.AssertComplete()` must pass. Only then does
`WriteCandidate` create `run/candidate.json` with mode 0600, without overwrite.
Shutdown, capture or export failure exits the test unsuccessfully without a
successful candidate; it does not alter business responses, force a retry or
repair Task state. Empty runs fail. API/model/build metadata remains `unknown`
unless independently established; the runner records the linked App SHA and
actual Catalog digest, not guessed versions. The candidate still requires human
privacy and provenance review before entering a repository. Directory checks do
not defend against a malicious same-user process replacing files after checks.

Offline regression commands (no upstream, credentials or deployment required):

```bash
env -u ART_AI_WIRE_CAPTURE_ACK -u ART_AI_WIRE_CAPTURE_ROOT GOPROXY=off GOSUMDB=off go test ./internal/app -run '^TestWireCapture' -count=1
env -u ART_AI_WIRE_CAPTURE_ACK -u ART_AI_WIRE_CAPTURE_ROOT GOPROXY=off GOSUMDB=off CGO_ENABLED=1 go test -race ./internal/app ./internal/testutil/wirefixture -run '^Test(WireCapture|Deferred)' -count=1
```

#### Deferred capture (test package only)

The server manifest accepts an optional field. Merge this member into the manifest
above; it is not a replacement manifest:

```json
{"deferred_limits": {"MaxBytes": 16777216, "MaxExchanges": 128}}
```

Omit it to keep the ordinary immediate recorder, whose JSON `Close` can drain
unread bytes up to its existing body limit. When present, `MaxBytes` must be
1–67108864 and `MaxExchanges` 1–1024. Startup still validates the frozen original
policy, metadata and selected connections, then calls
`NewDeferredRecorder(metadata, limits)`. It does not require dynamic mappings or
the final policy file to exist at startup. Deferred mode only observes caller
reads: it never pre-reads, drains on Close, calls `GetBody`, retries transport calls
or changes business bytes/errors, URL or ContentLength.

Prepare **one** independently reviewed exact policy as `final-policy.json` in the
same private root, not in `run/` and not at an external configurable path. It can
be prepared during collection. Use a caller-owned regular 0600 file, created
exclusively without overwriting an earlier file; the root must remain caller-owned
0700 outside the worktree. The server reads it only after clean application
shutdown, through a directory handle with no-follow/non-blocking flags, at most
1 MiB. Symlinks, non-regular files, wrong permissions/owner, unknown JSON fields,
trailing JSON or a missing file fail closed. Its JSON shape is the `policy` object
above, not another manifest.

Keep exactly the original `Keys`, `PublicHosts`, `Projection` and `QueryKeys`, and
retain all original `Allowed` and `Aliases` registrations unchanged.
`MaxBodyBytes` must stay positive and cannot exceed the original limit. Additional
exact `Allowed` values and terminal `Aliases` are permitted only if the existing
strict sanitizer preflight accepts them; no keys can be added, including sensitive
keys. Unknown values still fail at Finalize. `Projection` maps registered,
non-sensitive JSON field names to `id`, `ids`, `url`, `timestamp`, `number` or `text`:
`id` accepts identifier-shaped strings, `ids` accepts one such string or an array
of them; `url` requires a registered `PublicHosts` host, drops query values and
uses a fixed `fixture-N` path; `timestamp` accepts a JSON number or RFC 3339 string
and emits one fixed public timestamp; `number` emits zero; `text` replaces a registered non-sensitive string with the fixed public value `ok`. `QueryKeys` controls
acceptance only; values never enter `candidate.json`. Do not project `arguments`:
tool-call arguments are JSON text and must stay valid JSON. SSE fragment fields such
as `content` and `reasoning_content` keep empty fragments empty; `text`/`opaque`
projection must not replace an empty string with `ok`.
The file is not an automatic approval channel, a raw-to-Allowed extractor or
permission to approve whole prompts/private content. The original manifest and
connection configuration are not reloaded as runtime controls.

The byte budget counts retained body buffer capacity and copied URL/header/metadata
strings across both in-flight and completed pending exchanges. The exchange limit
bounds the entire round, including in-flight entries and cancellation callbacks.
Each body is also capped at 8 MiB. This is not a process RSS limit: business bodies,
allocator overhead and sanitizer scratch space are outside this budget. Checks run
during each observed Read, before retaining more bytes.

A live SSE consumer may stop after the last complete event without closing the
body. Before Finalize, call `DiscardIncomplete()` to drop only unfinished SSE
exchanges and canceled auxiliary requests. Completed exchanges stay valid;
non-SSE truncation, transport errors and sanitizer failures still invalidate
export.

Stop new requests and wait for all RoundTrips and body I/O to finish. Then call
`Finalize(reviewedExactPolicy)` with independently reviewed exact mappings. Unknown
keys (including sensitive keys), unapproved values and missing mappings still fail
closed; no strings are auto-approved. Finalize clones the maps, enforces a smaller
Policy body limit, and publishes sanitized exchanges in request-entry order only
after strict validation. Do not mutate the supplied maps concurrently with this
call. Before success, `AssertComplete` fails and `Exchanges` is empty. The library
API returns incomplete without sealing on an early Finalize; a second Finalize
after sealing is rejected. The server makes one export attempt: any missing file,
policy, capture, shutdown or export failure calls `Release`, even if the library
would otherwise permit waiting. Supplying a new file/policy cannot repair that
round. Export never makes a network request or retries a business action.

After Finalize, AssertComplete and per-connection coverage pass, the server builds
a public replay policy from the original public Keys/Allowed and validated
terminal values in the sanitized candidate's payloads, paths and retained headers.
It does **not** copy private Aliases or scan raw data. It validates the candidate
again with this alias-free policy. `run/replay-policy.json` is bounded to 1 MiB;
its directory passes the existing `NewPrivateStore` isolation checks, and an
`os.Root` handle creates it exclusively with mode 0600. Only then does the existing
`WriteCandidate` create `run/candidate.json`. If candidate writing fails, the new
policy file is removed; pre-existing files are never overwritten. No candidate is
written when policy writing fails. Neither private manifest nor final policy is
exported. After review, these two public files can feed `NewReplay` without private
aliases; use canonical `example.test` requests and call `AssertConsumed`.

Raw copies stay only in memory. There is no raw getter, arbitrary mutation callback
or raw log/error/disk output. Sealed Finalize success/failure, budget or I/O failure,
cancellation while incomplete, and explicit `Release` drop capture-owned raw data
and callbacks. Abandoned captures must call `Release`; it does not read or close
business bodies. A limit failure permanently invalidates the round and stops
collection, but later requests still pass through. A new request after sealing
also invalidates the evidence. Failures retain only one fixed reason.

Deferred mode ignores `Recorder.Policy` until Finalize. `NewDeferredRecorder` keeps
no media store; the capture server uses `NewDeferredRecorderWithEvidence`, which
spools supported binary responses to `run/evidence/` as the caller reads them.
Read-only asset GET failures may be retried by the business layer; the failed attempt is dropped from evidence. Other transport failures invalidate the round. Truncated or early-closed media invalidates evidence without changing business
bytes. The exporter writes `run/evidence-manifest.json` (sequence, part, type,
length, SHA-256) and substitutes fixed recipes in the public candidate. Projection
and registered query keys produce stable `id-N`/`fixture-N` values; raw IDs, signed
query values and media bytes never enter the candidate. Actual browser capture and
a live corpus are still not part of this README's accepted evidence. The local App
tests use synthetic JSON, SSE and media interactions through prepare/assemble and
the real administrator connection-probe route; they do not complete #365 or prove
a full live chain.

#### Meshy browser preflight (no submission)

`wire-capture-browser.mjs` uses the same Playwright module resolution and managed
launcher as L0. Playwright stays outside product dependencies (`PLAYWRIGHT_MODULE_PATH`
can select an existing installation). It opens a fresh 1440×900 Chromium context;
`--browser webkit` selects WebKit. Build `build/go-web` separately if absent.
This is a **restricted-write preflight**, not a read-only check. Each run requires
a fresh isolated owner with no sessions, canvas projects or Tasks; those public
projections are checked before entering the workspace. Use separate empty databases
for Chromium and WebKit. Reusing a populated owner fails before workspace navigation;
the entry never deletes or resets existing data to make a rerun pass.

```bash
node --check scripts/smoke/wire-capture-browser.mjs
node --test scripts/smoke/wire-capture-browser.test.mjs

# Against an already-running TestWireCaptureServer, with its reviewed private root:
node scripts/smoke/wire-capture-browser.mjs --capture-root "$CAPTURE_ROOT"
# --base http://127.0.0.1:<port> is optional, but must match capture.json if supplied.
# --offering <public Offering ID> requires that exact available Meshy text-to-3d item.
```

The browser checks `/login` (`#username`), reads the injected/public Schema, then
uses its test-only mock-login route with an empty body. No password, key, `.env`,
saved browser profile or cookie file is read. The capture owner must be the fixed
non-admin `wire-capture-owner`. The manifest/Catalog digest and built `index.html`
are checked locally. A manifest source SHA is only a label, **not** running-server
attestation or proof that dirty source/built assets match that commit. Startup
isolation and live configuration still belong to `TestWireCaptureServer` above;
the browser does not start it, create its configuration or fetch its credentials.

The current router has no direct text-to-3D generation form. `HarnessPanel` saves
Agent function settings; it does not submit a generation. This entry therefore
uses the real `/` Agent workspace, without an API prepare/submit shortcut:

- The workspace creates its first empty session on initial load. Its POST must
  contain only the public Schema's title field with an empty string; the HTTP 201
  response binds the new session ID in memory. There is only one create allowance.
- Normal workspace canvas startup may create exactly one project for that new
  session. Its POST body must contain only the Schema's `session_id` field (currently
  `sessionId`) with the new session ID. HTTP 201 must report `created=true`, the same
  owner/session, a valid new project ID and initial revision 1. This is creation,
  not the existing-project reuse check used by historical-content diagnostics.
- Autosave allows exactly one PUT to that project's public canvas-nodes route.
  It must use revision 1 and the Schema's current coordinate version, default
  artboard dimensions and blank initial layer, with no uploaded image or external
  content. The only placement registration is the empty-lineage protocol watermark
  `since:1970-01-01T00:00:00.000Z` from `planAutoJoin([], null)`, not a Task/Asset key.
  The save response must be HTTP 200 with the expected node count. A public GET
  then verifies owner/session/project, revision 2, dimensions, version and saved
  initial nodes. It is checked again after one declared autosave delay plus 500 ms
  (the declared delay is bounded to 0–10 seconds).
- `studio-approval-menu` must show the public Schema's manual label; the public
  session approval endpoint must agree and require approval for this capability.
- `workspace-model-picker` → `workspace-model-picker-model` → the unique
  `menuitemradio` containing the public model label selects Meshy. The default is
  the recommended available Meshy text-to-3D Offering, else the first available;
  unavailable/explicitly missing choices fail instead of silently changing model.
- `composer-params` → `params-domain[data-domain="model-3d"]` must name the exact
  public Offering in `data-offering`; `params-cost` must match its declared estimate.
- `studio-composer textarea` receives only the fixed non-private synthetic input
  exported as `syntheticPrompt` by this script. Pre-register that **exact** string
  in the reviewed initial policy's `Allowed` map for a capture preflight. Only its
  SHA-256 enters the report. `composer-send` is inspected, never clicked; Enter is
  never pressed in the textarea. The draft is cleared before normal exit.

Before/after reads require zero session Tasks and zero pending approvals. The
network guard permits reads plus four one-shot writes: empty-body mock login,
empty-session creation, its canvas creation and its validated initialization PUT.
Allowances are consumed before forwarding the original request and never re-armed
after failure or an uncertain response. Writes must be fetches from the main UI
frame; canvas writes must come from the workspace. Cross-session/project writes,
generation, approval, Agent sends, uploads, deletion and all other writes remain
blocked. Off-origin requests and WebSockets are also blocked and counted as
failures. Same-origin static/decoder/asset paths and same-origin blob assets are
normal, not Provider leaks. Local API list queries are allowed; this does **not**
waive the recorder's rejection of upstream query-bearing requests. No request is
fulfilled with fake data, no store is seeded, and no execution intent is created
or retried. The guard is not a server-side outbound sandbox: do not attach it to
a server with existing running work. Use only the fresh isolated capture server,
or a fresh zero-key mock server for selector verification.

For a **separately started, disposable zero-key mock server**, the same page path
can be checked without a capture manifest:

```bash
node scripts/smoke/wire-capture-browser.mjs --mock-preflight \
  --base http://127.0.0.1:<mock-port> --web-assets-root "$PWD/build/go-web"
```

Start that server using `APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock
AUTH_MODE=development DEV_MOCK_AUTH=true`, a non-admin mock identity, numeric
loopback `APP_ADDR`, the built frontend, and fresh isolated database/storage/log
paths. Pass only non-secret variables in a clean process environment, not a copied
`.env`. `--mock-preflight` labels the result as local mock selector verification;
it is not live wire evidence or independent proof of the server's runtime mode.

The only output is a JSON status/summary with versions, digests, public labels,
declared cost, `preflight_kind=restricted_writes`, verified canvas revision/binding
status and allowed/blocked write counts. There are no trace/HAR/screenshots, raw console/error
messages, URL queries, response dumps, raw prompts or persisted browser state.
Do not enable Playwright `DEBUG`/`PWDEBUG` logging (the entry rejects them).

**Live remains blocked before the first send.** `--live --capture-root ...` is an
explicit live request, not an escape hatch: after a successful page preflight it
returns `status=blocked_capture`, `submitted=false`, exit 2. A normal successful
preflight exits 0 with `preflight=pass` and `capture=blocked_capture`; check both
fields. Failures exit 1 with a fixed `failure_stage` and, for rejected canvas
initialization content, a fixed `canvas_guard_rejection` code. There is no dead paid
click or imaginary capability endpoint in this version. The refusal follows the
actual current capture/export contract:

1. The capture server now has bounded private media spooling and synthetic recipe
   substitution, but this browser entry does not yet drive a paid send or wait for
   all required Meshy assets.
2. Registered query keys are accepted without retaining values; unknown keys still
   invalidate capture.
3. `Projection` covers dynamic task IDs and signed asset URLs, but the reviewed
   manifest still has to register every field and host; no raw getter or blanket
   allow exists.
4. Real browser capture, multi-turn Agent evidence and a live corpus are still
   separate acceptance work.

The remaining entry work is to add the real Agent send → exact pending-card price
check → one approval → Task/required READY asset wait and byte-digest verification.
That continuation must preserve the same consumed intent after any uncertain
response; it must never retry a paid click to repair capture. The main operator
coordinates a new authorized live round serially. This preflight does not complete
#365 or claim a Meshy asset capture.

### Recovery Fixture

此夹具用于缺少自然失败/暂停样本时的有界验收。普通 `cmd/art-ai` 二进制不包含故障控制器；只有 `go test -c ./internal/app` 生成的测试二进制能运行 `TestAcceptanceServer`。`dev-test` 主机上的实际故障进程使用独立 loopback 端口及私有目录，不能使用 `dev-test` 主实例的入口端口，也不能复用共享数据库、资产或 Catalog 的写路径。`dev-test` 主实例的同版正常回归仍按上文 L0/L1/L2 执行。

1. 先只读检查源数据库、来源成功任务、资产 SHA-256/大小及容量。`seed-acceptance-fixture.py` 默认 dry-run；`--apply` 使用 SQLite backup 保存一致性快照，拒绝活动任务、任何待调度 Task、活动 Plan/Chat turn，并逐文件复制最多五个选定成功来源的实际资产（默认总上限 2 GiB）。源/目标目录必须分开，不使用硬链接。失败保留 `SEED_FAILED`，不能启动该副本。数据库属于私有数据，不打入脱敏报告。
2. 在私有根目录内准备 Catalog 副本和前端产物，以完整已提交 SHA 编译测试二进制：`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -ldflags '-X github.com/LeXwDeX/Plinth/internal/app.acceptanceSourceSHA=<完整 SHA>' -o <test-binary> ./internal/app`。清单绑定 owner、源码 SHA、专用地址、前端和 Catalog 路径；计划场景绑定 session/plan/step/capability/Offering 及最多 1–10 次 Provider Start；图片故障与独立 `standalone` 场景各只允许一次 Start。一次性故障与出站预算保存在私有持久标记中，重启不会重置。
3. 测试栈凭据只传入进程环境。启动需 `ART_AI_ACCEPTANCE_ACK=isolated-test-only`、`ART_AI_ACCEPTANCE_ROOT=<私有根>`、`ART_AI_ACCEPTANCE_MANIFEST=<根内清单>`，运行 `-test.run '^TestAcceptanceServer$' -test.timeout 0`。仅测试装配启用固定 owner 的 mock 登录以取得正常签名会话；Grant 决策仍经实际鉴权与批准策略。默认真实 Provider；离线 mock 必须显式声明，报告必须区分，不得冒称真实调用。`ready.json` 保存进程、源码和二进制/前端/Catalog 摘要。
4. `run-controlled-acceptance.py` 管理已登记场景和私有 Catalog A/B 重启；默认只预检，执行须显式 `--execute`。以其 `--help` 为参数合同。`--browser-command 'node <controlled-acceptance-browser.mjs>'` 在恢复阶段使用真实页面按钮；helper 从 `--context` 文件读取阶段，不伪造 HTTP 响应或修改前端 store。设置 `PLAYWRIGHT_MODULE_PATH` 指向隔离 Playwright。没有浏览器 helper 的 API 结果不能替代页面验收。

   all 模式的丢响应绑骨样本引用本轮已成功并通过交付校验的 Remesh 首步 Task，不直接再次绑骨原始高面数模型。`--only lost-response` 必须显式给出 `--source-lost-task <已有 Meshy Remesh Task ID>`。缺少来源、owner/session 不符、非成功 Remesh、仅格式转换（`convert_format_only=true`）、冻结 Offering 不符、冻结输入的面数不在该来源自身 Offering 声明范围内或 GLB 字节校验失败，均在写 manifest 或启动 worker 前拒绝。该参数不会替换格式场景使用的两份原始来源。

   格式补齐场景要求 `--source-format` 指向真实 SUCCEEDED 且 coverage 中确有 `meshy/processing/remesh` 缺失格式步骤的来源。2026-10-18 #221 移除 BLEND 后，六格式齐全的 Meshy 生成来源已没有 remesh 可补齐的格式（唯一缺失的 glTF 由免费本地转换提供，remesh 不提供），此前记录的 Meshy 12 资产来源不再满足该前提；需要显式换用真正缺格式的来源。没有合适来源时脚本在创建任何付费计划前中止，不得新建替代付费计划。

   丢响应场景 ID 和幂等 key 包含来源 Task ID，重复使用同一来源保留原身份。换用另一个已验证来源建立新样本，不重置旧故障 marker、旧预算或旧失败 Task。报告保存 Remesh 来源、冻结面数参数/范围和恢复前后聚合摘要；all 模式另核对并记录本轮成功 Rigging Task 对该 Remesh 的消费，作为已有的真实兼容证据。
5. 故障仅注入依赖边界：中间步骤出站前明确失败、受理后丢响应、准备暂不可用、受理前断开，以及图片 Adapter 的本地 `insufficient_quota` wire 响应。成功步骤仍经真实 Provider 和完整交付检查。记录旧成功步骤保留、Task/Attempt/Grant/PreparedInput/幂等身份、重复/跨用户/旧版本拒绝、出站次数及必需产物两次下载的哈希和大小。Catalog B 必须在已冻结的暂停计划上验证拒绝，恢复 A 后才继续；不改写任何生命周期数据来制造状态。
6. 保留实际浏览器名称、版本与来源、地址、完整 SHA、交互截图和 API 结果。余额样本验证中文解释且不提供重试；纯读取与被拒绝操作核对前后 Task/待批准无意外新增。失败样本、单次故障与正常出站分开记录；出站唯一性不等于第三方结算审计。停止仅限本轮记录的进程 PID，保留样本与报告。

独立候选资格使用 `fault:"standalone"`，不填 `plan_id`，`step_index` 为 0。先通过正常 API 准备输入，再把 owner/session、capability、Offering ID/revision、PreparedInput ID/hash、至少16字符的幂等键和 `max_provider_starts:1` 写入私有清单，然后批准 Grant 并以同一幂等键提交。额度按 owner+幂等键持久保存，改场景名称或 fault 不能重置；结果未知不重新提交。控制器经只读 `TaskAdmissionStore` 核对任务实际批准的 PreparedInput ID，再检查冻结输入，并约束 Start、Observe 和 Tripo 私有准备恢复的 wire 输入，保留 Adapter 的输入资产预检。每个新的公开步骤须有自己未消费的意图和批准，不能复用已消费身份。私有 Catalog 可以仅开启本次候选，默认 Catalog 保持禁用；所有必需资产两次鉴权下载、大小/摘要/格式、匿名401和跨用户404仍必须逐项核验，资格通过后还需主测试栈同版正常 UI/Agent 回归。测试装配用通用包装器包住没有专用包装的 Provider（如 Hyper3D、本地转换），可选的 `ProviderInputValidator` 等端口因此不可见：隔离进程里的格式清单可能把实际会被转换 profile 拒绝的 glTF 显示为“可转换”，且任何不在清单场景里的 Start（包括免费的本地转换）都会被拒绝。格式清单与补齐格式要在正常二进制的严格 session 副本上核对（#187，2026-10-21）。

离线回归：`python3 scripts/smoke/test-acceptance-seed.py`、`go test ./internal/app -run '^TestAcceptance'`。它们检查隔离与恢复合同，不代替真实 Provider、实际字节、发布门禁或读屏验收。每轮结果写 GitLab Issue/MR，本文不作为当前进度台账。
