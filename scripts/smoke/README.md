# dev-test 冒烟与发布脚本

Agent 控制的 `dev-test` 发布与冒烟协议（按环境名部署与冒烟见 `docs/deployment.md`：`python3 scripts/release-env.py smoke <环境> -- bash scripts/smoke/<脚本>` 为同一套 L0/L1/L1 Agent 脚本设置 `BASE`、管理员登录、`EXPECT_SHA` 与 `RELEASE_CONTAINER`；在目标主机运行 L1 Agent 时另设 `ART_AI_SERVER_ROOT=<发布根>`）。各环境相互隔离；部署与验收事实以对应 Issue 回执为准（`formal-test` 晋级回执见 #2；`legacy-formal` 已下线，存档 #74）。操作边界见 `dev_ops.md`「Agent 全自动发布协议」及其「dev-test 主机与镜像保留」一节。

## 发布（Agent 本地一键执行，无 CI）

```
git push origin main                       # 待发布内容先进 main
python3 scripts/release-env.py release dev-test   # 同步→构建→preflight→备份→Compose 更新→健康（驱动 release-go-testenv.sh）
```

- 凭据只在本地，远端命令通过本地 `deploy/local/environments.json` 中该环境的 `ssh` 执行。测试栈发布由 `python3 scripts/release-env.py release dev-test` 驱动 `scripts/release-go-testenv.sh`（`RELEASE_HOST`、`RELEASE_ROOT` 必填，无缺省主机/根）执行；`formal-test` 晋级命令与来源验收 note 格式见 `docs/deployment.md`。
- `dev-test` 的 fake 形态发布成功后，发布入口自动依次跑本页 L0、L1、L1 Agent 三层，读各脚本自己的 `PASS=/FAIL=/SKIP=` 汇总行与 `SOFT-FAIL` 行，写机器报告 `<root>/production/reports/smoke-<revision>.json` 与每层日志（#411）。任一层不是 `pass`（含 skip、soft-fail、缺汇总行）或报告写不出去时发布命令返回非零，但发布不回滚；判定口径与失败处置见 `AGENTS.md`「发布后冒烟判定与晋级校验」。真实形态不自动跑，按上文表格手动执行并留证。
- `smoke-l0-browser-boot.sh`、`smoke-l1-free.sh`、`smoke-l1-agent-mode.sh` 对非默认 `BASE` 都要求显式完整 `ADMIN_USERNAME` 与 `ADMIN_PASSWORD`，避免误读测试运行环境凭据。
- 候选启动或健康检查（healthz/readyz）失败时，按本次备份范围恢复数据并启动发布前实际运行的镜像。标签从原容器读取。失败报告写在服务器 `production/reports/release-failures/<tag>-<stage>.txt`，并带 `backup_scope`/`backup_targets`。
- 默认备份 `data`、`storage-go`、`web-data`，且不自动清理历史备份。仅 `dev-test` 可在环境文件写 `"backup": {"scope": "database", "keep": 2}`：停机只备份 `production/data`，回滚只替换该目录；候选写入的 `storage-go`/`web-data` 字节保留，可能成为孤儿，因此不是完整回滚。手工回滚先读 `backups/release-<T>/scope`（单行 `database` 或 `full`；旧快照无 scope 且三个归档齐全时按 `full`）。离线故障注入回归：`bash scripts/smoke/test-release-rollback.sh`（含 database scope）以及实际 helper 的单行 scope 夹具（`test-release-stack.sh`）。
- 构建或 preflight 失败直接中止——旧实例未被触碰，无需回滚。
- 测试栈健康检查与成功清单写入后，自动执行 Plinth 镜像保留：按创建时间保留 `art-ai-go-production` 最新五个不同镜像 ID，额外保护运行镜像、发布前实际运行的回滚镜像，以及发布时传入的测试栈与 `protect_projects` 所列其他 Compose 项目（`--project`/`--protect-project`，名字来自本地环境文件）任意运行或停止容器引用的镜像。发布与清理共用锁。保护项可能使数量超过五个。只用无强制的 `docker rmi <id>`，失败记警告，不改变发布成功结果；镜像工具不删容器、卷、数据库备份或其他仓库镜像。dry-run 见 `dev_ops.md`。
- database 模式在健康检查与成功清单写入后，再调用 `scripts/retain-release-backups.py --apply`，`keep` 为 1–2（当前配置 2）；正常清理最多保留最新 2 个已结束发布备份，更早失败快照也按最新 2 个回收。进行中快照可临时排除，失败报告与独立诊断日志保留。bootstrap、失败、回滚和预构建路径不清理；清理非零只警告，不改变发布结果。helper 只处理 `backups/release-*`，不碰原始资产、数据库、首次迁移冷备或其他栈备份。保留 database-only 快照可能被使用时，禁止离线 CAS/task 破坏性维护。
- 备份目录在停机前创建，并写入单行 `scope` 文件（`database`/`full`）。备份目标（targets）在成功后的 `production/compose.env` 和失败报告中记录，快照内不另存 targets。目录准备失败时旧实例继续运行。已有同名备份不会被删除；重复标签须换用新的发布标签。
- 停机或备份失败时，先重新启动并检查旧实例，再写失败报告。报告目录不可写时仍执行恢复。健康请求的连接超时为 3 秒，总超时为 5 秒；重试次数有上限。
- 线上无法在线解决的问题：记录报告 → 回滚 → 回到本地修复后再发布。
- 同一发布根的发布备份/恢复、备份清理与手工恢复共用 `<root>/.release.lock`：`release-server-steps.sh` 在构建或改动数据前持锁，清理 helper 校验并继承该描述符；缺失或身份不符即拒绝。身份检查后直接 `flock` 继承描述符：共享描述符复用锁，空闲描述符先取得锁，其他进程持锁则 busy；借用方不得解锁。手工恢复必须在 `release-lock.py run` 下执行，详见 `docs/deployment.md` §7。删除前还拒绝快照树内的挂载点（含同设备 bind mount），无法读取挂载表时失败关闭。
- 失败快照可能有零字节或缺失归档，只有在独立失败报告存在时才可清理；成功备份仍要求完整且非空的归档。删除资格不代表可作为恢复点。
- 发布合同回归：`python3 scripts/tests/test_release_env.py`、`python3 scripts/tests/test_release_lock.py`、`python3 scripts/smoke/test-release-backup-retention.py`、`bash scripts/smoke/test-release-stack.sh`、`bash scripts/smoke/test-release-rollback.sh`。这些是本机 mock 合同测试，不代替真实部署或远端清理验收。

## 上游形态与真实开关（#219）

`dev-test` 缺省以 fake 上游形态发布（`docs/deployment.md`「fake 上游形态」）：所有 Provider 与对话模型连接指向同栈的 fake 服务。脚本默认针对 fake 形态，真实上游需要 `--live` 参数与 `ART_AI_LIVE_ACK=1` 两个开关同时给出，缺省或只给一个即拒绝（退出码 2），每次真实运行前还须在当前对话取得用户同意。判定逻辑在 `upstream_shape.py`：目标形态取 `ART_AI_UPSTREAM_SHAPE`（`release-env.py smoke` 按该环境 `production/compose.env` 的 `RELEASE_UPSTREAM_SHAPE` 设置），否则读 `$ART_AI_SERVER_ROOT/production/compose.env`；读不到按真实形态处理。复制脚本到服务器运行时，`upstream_shape.py`（以及 L1 Agent 用的 `seed-fake-3d-task.py`）须与脚本同目录。

**混合形态（`mixed`，#416）：** `release-env.py release dev-test --live-chat` 发布的栈里生成侧仍是 fake，对话连接指向真实网关（`docker-compose.go-mixed-upstream.yml`、`UPSTREAM_SHAPE=mixed`；见 `dev_test.md`「真实对话 + fake 生成」）。`upstream_shape.py` 把它报成 `mixed`，并按「非 fake」处理：两个门禁都要 `--live` 与 `ART_AI_LIVE_ACK=1`（对话侧是真实付费调用，只给生成会低估成本），`live-only` 的 L2–L5 直接拒绝，因为生成侧不是真的。于是「真实对话 + fake 生成」只能由显式要求真实对话的脚本使用，普通 fake 场景不会误连真实网关。

| 脚本 | fake 形态 | 真实形态 |
|---|---|---|
| L0 `smoke-l0-browser-boot.sh` | 直接运行（不涉及上游） | 直接运行 |
| L1 `smoke-l1-free.sh` | 直接运行：3D 请求带 fake 对话模型的工具标记 `[[fake:tool:create_model_3d …]]`，确定地得到待批准卡片 | 会调用真实对话模型：需 `--live` + `ART_AI_LIVE_ACK=1` |
| L1 Agent `smoke-l1-agent-mode.sh` | 先用 `seed-fake-3d-task.py` 经 fake 生成一个本用户 SUCCEEDED 3D 任务（零费用），第 9 节格式/归档验收不再 SKIP | 不提交生成，保持只读 |
| Fake 故障 `smoke-fake-faults.py`（#301） | 直接运行：用带标记的图片触发抠图 fake 的部分成功与显存不足，断言公开投影 | 拒绝（退出码 2；标记只对 fake 有意义，没有 `--live` 模式） |
| 直连 L5（#540）`smoke-l5-2d-direct.py`、`smoke-l5-matting.py`、`matting-probe.py` | 直接运行：2D 七任务、抠图多文件批次与服务探针都对 fake 跑通，fake 未建模的偏差记为 INFO；整栈场景 `l5-direct`（`smoke-fake-stack.py`）跑同一批脚本 | 需 `--live` + `ART_AI_LIVE_ACK=1`；fake 形态下给 `--live` 同样拒绝；内容拦截探针另需第三个开关 |
| L2、L3、L4、L5 | 拒绝（需要真实上游；先以 `--live-upstream` 发布真实形态） | 需 `--live` + `ART_AI_LIVE_ACK=1` |

## 全新数据起点（#28）

`dev-test` 每次发布都是全新数据（环境文件 `"data": "ephemeral"`，匿名卷，不含历史任务、会话或老账号）。因此每次冒烟与相关验收都从空库开始：只有运行环境 `ADMIN_USERNAME`/`ADMIN_PASSWORD` 引导的管理员测试账号可登录，脚本和手工验收按真实用户流程创建自己的会话与数据，不依赖历史任务（例如 L1 Agent 的“已有 3D 任务”检查在空库上走其文档化的 SKIP 路径；需要产物时先走真实流程，如 #25：模拟用户沟通 → Agent 先 2D 后 3D）。`formal-test` 不适用。

## 冒烟分层

| 层 | 脚本 | 何时跑 | 费用 |
|---|---|---|---|
| L0 浏览器启动 | `smoke-l0-browser-boot.sh` | **每次发布后必跑** | 零（不产生任何生成任务） |
| L1 免费冒烟 | `smoke-l1-free.sh` | **每次发布后必跑** | 零（先把会话设为手工方式，再用 3D 请求取得待批准卡片，停止本轮后确认卡片仍待批准，再拒绝；无任务、无真实生成） |
| L1 Agent 批准方式验收 | `smoke-l1-agent-mode.sh` | **每次发布本功能集（批准方式、Agent 3D 工具、格式补齐/ZIP、设定摘要、选定、版本链、重试）后必跑**；与 L1 一起跑 | 零（不提交任何生成，只读已有 3D 任务） |
| Fake 故障冒烟（#301） | `smoke-fake-faults.py` | 改动抠图、fake 故障、成功任务提示或失败文案后，对 fake 形态的 dev-test 跑；不是每次发布必跑（见下文「Fake 故障冒烟」） | 零（背景去除无价格，目标只能是 fake 形态） |
| L2 真实模型链路 | `smoke-l2-livemodel.sh` | 只在用户当次同意后、对真实形态发布的 dev-test 跑（`--live` + `ART_AI_LIVE_ACK=1`，#219） | 一张小额文生图（约 6 积分）；设 `L2_APPROVE_3D=1` 再加一次 3D 生成（约 25 积分） |
| L3 批量回归 | `smoke-l3-batch.sh` | 只在用户当次同意后、对真实形态发布的 dev-test 跑（`--live` + `ART_AI_LIVE_ACK=1`，#219） | 每模型 5 张小额文生图 |
| L4 3D 格式与隔离 | `smoke-l4-3d-isolation.py` | 3D Provider、格式或缓存边界变更后跑 | Meshy 一次生成；Tripo 一次生成及六次转换；`ART_AI_3D_PROVIDERS=hyper3d` 时一次 Rodin 文生 3D（3 Hyper3D credits，只核必需 GLB） |
| L5 真实能力回归 | `smoke-l5-*.py`，入口 `smoke-l5-all.sh` | 只在用户当次同意后运行（`AGENTS.md`「上游接口与 Fake」），结果回灌 fake | 真实付费，必须同时给 `--live` 与 `ART_AI_LIVE_ACK=1`，目标须为真实形态；见下文 L5 一节 |
| 直连 L5（#540） | `smoke-l5-2d-direct.py`、`smoke-l5-matting.py`、`matting-probe.py` | 2D 三家与抠图服务的「每个 API 功能真实调用一轮」验收；只在用户当次同意后对真实形态运行，结果回灌 fake；对 fake 形态可随时运行（见下文「直连 L5」） | fake 形态零；真实形态 2D 约 0.47 美元（`--large` 约 0.57），抠图零费用但占用共享 GPU |
| 公共 Mock 自部署冒烟 | `smoke-public-mock.sh` | 自部署后本地手动运行（`SMOKE_ENV_FILE=.env bash scripts/smoke/smoke-public-mock.sh`）；不再由公共 CI 运行（#257：CI 不构建镜像） | 零（环境文件须显式写 `PROVIDER_MODE=mock`、`AGENT_MODE=mock`，只接受 Mock Provider/Agent、全部 `*_API_KEY` 为空的回环地址栈）；不用于内部环境 |

L0–L4 各层在 `dev-test` 主机上执行时用 `ART_AI_SERVER_ROOT=<root> bash scripts/smoke/<脚本>`（`<root>` 为该环境发布根，读取本栈 `runtime.env`；L1 Agent 要核对容器 revision 时另设 `RELEASE_CONTAINER=<release-env.py show 的 container>`，脚本不再带缺省容器名，#261）；在本地执行时用 `python3 scripts/release-env.py smoke dev-test -- bash scripts/smoke/<脚本>`（自动设置 `BASE`、`ADMIN_*`、`EXPECT_SHA`、`RELEASE_CONTAINER`）。

L5 脚本是 `.py`，不读取 `runtime.env`，只在本地通过 `release-env.py smoke` 运行：单个脚本 `ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-<名称>.py --live`，全部脚本 `ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- bash scripts/smoke/smoke-l5-all.sh --live`（目标须为以 `--live-upstream` 发布的真实形态，#219）。每次运行前须在当前对话说明范围与预计费用并取得用户同意（`AGENTS.md`「上游接口与 Fake」），不再有自行发起的次数额度；运行后把真实结果中 fake 未覆盖的部分回灌 fake。

### 批准方式对冒烟的影响（会话级，迁移 0043）

- 会话批准方式由 Schema `chat.approval_policy` 声明（公开投影有 `default_mode`、`modes` 和 `manual_approval_capabilities`）；新会话取 `default_mode`，当前是 `auto`（#406），已有会话保持存值。
  - `auto`（新会话默认）：所有付费步骤自动批准，包括严格方式要人批准的四类和带额外积分的步骤。仍要人决定的只有 #294 的再扣费保护：同一会话再次生成“已计费但没保存”的同类结果时出批准卡。自动方式没有制作计划、额度或三视图连续推进。
  - `budget`（界面「审批」，#702）：`manual_approval_capabilities` 声明的四类（当前 `text-to-3d`、`image-to-3d`、`image-to-splat`、`retexture`）需要人工批准；此外 `manual_credits_threshold`（当前 20）使任何单步积分达到该值的步骤也出卡，所以重拓扑、绑骨、分割、补面这类加工步骤不再因为能力未列入而直接扣费。2D、动画和格式转换（基础估算低于阈值）由策略批准（`grant.decided_by` 为 `policy:budget`），不出现待批准同意，直接产生真实任务。Agent 不主动提议制作计划；用户明确要求并批准计划后，计划内步骤自动连续执行，计划外、超出该单位剩余额度、服务端无法报价或会再次扣费的步骤仍出批准卡。`auto` 与 `budget` 下本会话每批准满 100 步（`generation_checkpoint`），下一步出一次批准卡。
  - `manual`（严格）：Agent 发起的每一个付费步骤都需要人工批准，2D 也一样（#702）；用户自己在浏览器里点的 2D 按钮（三视图“继续生成”、重试等）不再确认第二次，3D 仍逐张批准。格式转换包括免费的本地 GLB↔glTF 和上传 GLB→STL（当前 Schema 基础估算 5 积分）；具体费用以 Offering 和冻结授权为准，策略批准不表示免费；Agent 步骤带额外积分（如附加导出格式、有计费的转换选项）时，审批方式下也要在对话里的批准卡上人工批准。
- 冒烟脚本不把「新会话默认值」当成隐含前提，但也不是每个脚本都显式切换：需要特定方式时显式 `PUT /api/v1/chat/sessions/{id}/approval`；保留默认时，要么先读公开 Schema 的 `default_mode` 核对新会话，要么由策略直接批准、不需要切换。各脚本的处理方式：
  - `smoke-l1-agent-mode.sh`：读 Schema `default_mode` 核对新会话与 `GET approval`，再依次 `PUT` `budget`、`auto`、`manual` 并各自回读。
  - `smoke-l1-free.sh`、`smoke-l2-livemodel.sh`、`smoke-l3-batch.sh`、`smoke-public-mock.sh`、`seed-fake-3d-task.py`：先 `PUT` `manual`，再等 3D 待批准卡或核对策略批准来源。
  - `smoke-l5-budget.py` 先 `PUT` `budget`；`smoke-l5-agent-hard.py` 的 `manual-approval` 用例先 `PUT` `manual`；其余 L5 脚本经 `l5_common.py` 的 `new_session()` 显式 `PUT` `auto`，切换失败即中止，不回落到服务端默认。
  - `smoke-fake-stack.py` 按场景显式设置：等批准卡的 `agent`、`hyper3d-agent`、`mask-selection` 用 `manual`；`three-view`、`four-view` 的计划链与 `plan-checkpoint`、`concept-candidates` 的 3D 部分用 `budget`（`three-view` 的严格臂另用 `manual`，#663 的自动臂用 `auto`）；不需要卡的场景用 `auto`。
  - `smoke-l4-3d-isolation.py` 不切换会话：授权已由策略批准时直接提交，只有 `pending` 才由测试操作员代批。
  - `wire-capture-browser.mjs`：preflight 只断言新会话等于公开 Schema 的 `default_mode` 并显示对应标签；`--live` 在发送前通过同一批准方式菜单切到 `manual`，以取得真实批准卡。
  - `smoke-fake-stack.py --only agent-quality`：轨迹运行器的 `_quality_bind_session` 显式 `PUT` `budget`，跟随制作计划；`agent_quality.py` 只做评估与报告，不创建会话。
- 保留默认、不切换的脚本：`smoke-fake-stack.py` 的其余场景靠 `auto` 默认直接提交（`auto-approval` 场景显式断言公开 Schema 与新会话都是 `auto`，并断言已保存的会话保留自己的方式）；`smoke-l4-3d-isolation.py` 不碰会话批准方式；`wire-capture-browser.mjs` 的 preflight 也只断言新会话等于 `default_mode`。
- 因此待批准同意只能用 3D 请求取得。L1 免费、L2、L3 在需要待批准同意时都先 `PUT /api/v1/chat/sessions/{id}/approval`（`{"approvalMode":"manual"}`）显式设为手工方式，再发 3D 请求；3D 模型取自 `/api/v1/offerings?capability=text-to-3d`（第一个可用、推荐、无额外计费上限的模型，`SMOKE_3D_MODEL` 可覆盖）。**不要再用 2D 请求去等待同意**：它会花钱，并让免费冒烟变成付费冒烟。
- 脚本代批同意只发生在同意确实待批准时（L2 的 3D 批准、L3 兜底、L4 的 `status=pending` 授权）；已由策略批准的授权不再 `PUT` 决定（会得到 409）。

- L2/L3 都通过已登录 Cookie 写会话、发消息和批准/拒绝时携带同源 `Origin: $BASE`。L2/L3 必须先让 Agent 调用实际生成工具（L2 的 3D 部分 `create_model_3d`，L2 的 2D 部分和 L3 每张图 `refine_image`），以生成工具建立的 exact prepared execution 作为待批准对象（#491 已删除 `request_consent` 工具，付费步骤只由执行授权批准）。批准卡片独立于轮次保存，旧的 `/api/v1/agent/consent` 不再反映它；脚本先 `GET /api/v1/chat/sessions/{id}/pending-approvals` 读取状态为 pending 的卡片及其 `request_id`、`capability`，再 `PUT /api/v1/tasks/grants/{request_id}` 提交 `{"<成员名>":"approve"}` 或 `{"<成员名>":"deny"}`；成员名与浏览器一样，按公开 Schema（`GET /api/v1/schema`）的 `chat.api.fields.decision` 读取，默认成员名为 `decision`（#120）。L2 停止 A 后和拒绝 B 后会拒绝会话里剩下的待批准卡片；L3 只批准 2D 图片步骤，其他卡片一律拒绝。批准动作只发生在产品外 smoke 脚本，产品本身仍要求明确人工批准。
- L3 协议：每模型每轮 `IMAGES_PER_MODEL` 张（默认 5，**协议上限 5**），满一轮即止不循环；零人工（脚本代批同意仅限产品外 smoke 操作，属测试操作授权）；断言 = 终态 SUCCEEDED + required 资产 READY + URL 200 + media_type 魔数一致 + sha256/size 与服务端声明字节级一致 + 连续回合必须出现**新 task id** + 跨用户任务/资产 403/404；报告缺省落服务器 `$ART_AI_SERVER_ROOT/production/reports/smoke-l3/`（`L3_REPORT_DIR` 可覆盖）。
  - 用法：`ART_AI_SERVER_ROOT=<root> ART_AI_LIVE_ACK=1 MODELS=gpt-image-2.6-sunburst bash smoke-l3-batch.sh --live`（`MODELS` 缺省 = schema 全部可用 text-to-image 模型；`IMAGES_PER_MODEL` 缺省 5）。
  - L3 每个会话先设为手工（严格）方式；严格方式下 2D 也出批准卡（#702），脚本按 pending-approvals → grants 批准后继续，同意没有出现而任务已开始时直接等任务；两种情况都继续断言终态与字节。
  - 满一轮无问题 → **暂停分析**，不继续加图（dev_test.md §11.3 口径）。
  - 红证已做：`MODELS=nonexistent-model` → FAIL（EXIT=1），失败可被检测。

- L0 覆盖：真实浏览器加载 SPA、**CSP 未拦截首屏脚本**、工作区首页可渲染。
  这是 CSP 解封门禁二的运行期那一半——`internal/transport/webui/shell_csp_test.go`
  只断言响应头里含脚本 `sha256`，L0 才验证浏览器真的把它执行起来了。
  有 `ADMIN_USERNAME`/`ADMIN_PASSWORD` 时先登录再校验工作区首页，否则只校验公开登录页。
  依赖 playwright（不进发布依赖，按 `PLAYWRIGHT_MODULE_PATH` 或本地安装解析；
  首次需 `npx playwright install chromium`）。
- 详情弹窗几何回归（#224）：`result-detail-geometry.mjs` 登录后打开“我的资源”里一个多候选任务，在 1440×900、1280×720 与 390×844 下连续点击“下一个”并用公开 Schema 声明的上一个/下一个键往返翻页，记录 `result-detail-prev`/`result-detail-next`、弹窗与预览框的 `getBoundingClientRect()`，断言位置和尺寸变化 ≤ 1px、图片加载前后弹窗高度不变、滚动到底再回顶不推动翻页行，并核对小视口下滚动区仍可用。零费用、只读、不创建任务；证据截图与 JSON 仅在设置 `SMOKE_EVIDENCE_DIR` 时写入该目录。用法：`BASE=<dev-test 的 base_url> ADMIN_USERNAME=... ADMIN_PASSWORD=... SMOKE_EVIDENCE_DIR=<绝对路径> node scripts/smoke/result-detail-geometry.mjs`。
- 弹层滚动锁几何回归（#74）：`overlay-scroll-lock-geometry.mjs` 登录后在工作台右栏「功能」选「绑骨」，打开「执行服务」下拉再关闭，在 1440×900、1280×720 与 1024×768 下每轮重新加载（竞态只在首次打开出现，默认 `ROUNDS=5`），在每次 DOM 变化与每一帧采样 `documentElement`、`body`、`studio-shell` 的宽度、左边界与 `scrollLeft`，以及 `<html>` 的溢出量和内联 `scrollbar-gutter`，断言全程不变、不溢出、未写入 gutter，并断言下拉定位层是 `position: fixed`。竞态与滚动条模式有关，无头 Chromium 不一定撞上，最后一项是确定性的根因断言（旧实现每次 FAIL）。「功能」页只在会话里有 3D 模型时才有内容（#705），所以脚本开始前先调用同目录的 `seed-fake-3d-task.py` 造一个有 SUCCEEDED 3D 任务的会话（零费用，只在 fake 形态运行），或用 `OVERLAY_SESSION=<会话 ID>` 指定已有模型的会话；造不出或进了会话仍没有「绑骨」选项时记 SKIP（不算通过）。零费用，只改未保存的「功能」草稿，不保存、不发消息；证据仅在设置 `SMOKE_EVIDENCE_DIR` 时写入。用法：`python3 scripts/release-env.py smoke dev-test -- node scripts/smoke/overlay-scroll-lock-geometry.mjs`（或直接给 `BASE=<dev-test 的 base_url> ADMIN_USERNAME=... ADMIN_PASSWORD=... ART_AI_UPSTREAM_SHAPE=fake`）。
- L1 覆盖：健康检查、Schema 投影、SPA 安全头、登录、CSRF 拒绝、会话/任务 API、**等待批准→停止免费路径**（用 3D 请求在手工方式下取得待批准卡片；读 `GET /api/v1/chat/sessions/{id}/pending-approvals`，不再读旧的 `/api/v1/agent/consent`，后者不反映独立于轮次的批准卡片；停止本轮后断言卡片仍是 pending，再用 `PUT /api/v1/tasks/grants/{request_id}` 提交 `{"<成员名>":"deny"}` 拒绝，成员名按公开 Schema 的 `chat.api.fields.decision` 读取，默认 `decision`（#120），最后断言会话零任务）。
  - L1 用手工（严格）方式，2D 也出批准卡（#702），误调用的 2D 工具通常停在卡片上；等待批准卡片期间一旦会话里出现任务就立即停止本轮，打印 `PAID-TASK-ALERT` 并计 FAIL（退出码 1）；只有会话零任务且批准卡片未出现时才是 `SOFT-FAIL`。出现 `PAID-TASK-ALERT` 时请人工复核该会话。
- L1 Agent 批准方式验收覆盖（`smoke-l1-agent-mode.sh`，零费用、不提交生成、不调用 LLM）：healthz/readyz；构建版本（`EXPECT_SHA` 必填，`/api/version` 与容器 revision 都必须等于它）；Schema 批准策略（`default_mode` 必须属于 `modes`，手工批准能力清单精确）；新会话响应与 `GET approval` 都必须等于 Schema 的 `default_mode`；随后显式 `PUT` `budget`、`auto`、`manual` 并各自回读、跨源 `PUT` 返回 403、非法方式返回 400；设定摘要读写往返、2000 字符可存、2001 字符返回 4xx 且不被截断；选定对象为空、未知任务的 `PUT` 返回 4xx、`DELETE` 成功；版本链对新会话返回 200；随机未知任务的 `formats`、`archive`、`retry`、`formats/complete` 都是 404；登录用户在**已获取的有界候选集内**最新 applicable 的 SUCCEEDED 3D 任务（自有会话，按创建时间；任务列表按 Schema `model_delivery.capabilities` 每个能力每页 100，最多检查 500 个去重候选，不保证是全局最新或穷尽）的 `GET formats`（200，Schema 的 7 种格式 ID 精确集合、无重复、每项 `id`/`status` 为字符串）和归档契约：`applicable` 与 `archive_available` 必须是 JSON 布尔值；字段缺失、类型错误、格式清单读取失败都 FAIL，不静默改用另一个任务。`archive_available=true` 时 `GET archive` 必须通过同目录 `read-archive.sh` 的真实 ZIP 检查（200/206、首项 `README.txt`、README 内无 `http(s)://` 地址；已知总量低于 `ARCHIVE_MAX_BYTES` 时做完整 ZIP 检查，否则按其文档化上限只做首项检查）。`archive_available=false` 时 `GET archive` 必须返回 413 且 JSON `code=too_large`；409/empty 或其他拒绝都 FAIL，不视为可用归档；随后在同一 500 候选列表内找一个 `archive_available=true` 的既有自有任务执行真实 ZIP 检查，找不到则 FAIL。完全没有自有 SUCCEEDED 3D 任务时才 SKIP；已发布 Agent 配置含 `update_session_notes`、`retexture_model`、`process_model`、`list_session_models`、`plan_model_processing`、`get_processing_plan`、`find_history_images`；另外从公开 Schema `runtime.http.routes.offerings` 指定的端点查询 `capability=format-conversion`，至少一个 Offering 的 `available=true` 时，将 `schema.chat.tools` 中同 capability 的工具加入必需集合（当前为 `convert_model_format`），管理员配置与非管理员 effective 回退复用该集合；全部不可用时不追加。只读 discovery 不执行转换、不调用 LLM、不创建 Task。缺工具时 FAIL，并提示保留自定义配置、补齐工具后重新发布；公开 Schema/discovery 的非 200、JSON 损坏、列表或 `available` 布尔类型错误、可用转换缺少工具映射，以及配置响应的必需字段缺失或类型错误，同样 FAIL，不打印响应原文。
  - 两栈日志（#233）：在服务器上运行时还检查本栈 `production/logs/art-ai.log`（可用 `ART_AI_LOG_FILE` 覆盖）存在，且最近一条 `日志已配置` 为 `log_level=DEBUG`、`directory=/app/logs`；目录可见但文件缺失或级别不对计 FAIL。
  - 用法：`ART_AI_SERVER_ROOT=<root> EXPECT_SHA=<origin/main 完整 SHA> bash smoke-l1-agent-mode.sh`（服务器上）。输出末行 `PASS/FAIL/SKIP` 计数，有 FAIL 退出码为 1。
  - 构建版本（#229）：`/api/version` 返回构建时以 ldflags 链接进 `art-ai` 二进制的提交，与容器 OCI 标签 `org.opencontainers.image.revision` 同源（都来自 `GO_SOURCE_COMMIT`）；未注入完整 40 位 SHA 的本地构建返回 `unknown`。脚本严格断言 `/api/version` = `EXPECT_SHA`，在服务器上再断言容器 revision = `EXPECT_SHA`；缺 `EXPECT_SHA` 或不一致都是 FAIL，不再 SKIP。
  - ZIP 验收依赖同目录的 `read-archive.sh` 和 `check-archive.py`，复制脚本到服务器时三者须放在同一目录。先请求 Range `0-4194303`，即使返回 200、忽略 Range 或没有 Content-Length，也在前 4 MiB 主动断开；仅在达到字节上限时容忍 curl 的写管道错误/SIGPIPE。`ARCHIVE_PARTIAL_TIMEOUT` 默认 59 秒，对齐 #159「前 4 MiB 在 60 秒内到达」的要求，独立于普通请求的 60 秒。校验 HTTP 200/206、`application/zip` 和 ZIP 本地文件头；支持 stored/deflate 与数据描述符，解压后的 README 最多 4 MiB，并核对完整结束、大小和 CRC；同时检查布局（#222）：README 列出的路径必须安全；只有 OBJ 包可以用文件夹，且模型必须在同一文件夹有实际随行文件（README 里只标注“未保存”的引用不算），OBJ 的材质与贴图必须在 OBJ 所在文件夹内（允许贴图子目录），其他附件必须在 `other/` 下（只剩依赖文件、没有模型的包不按模型判定）；完整 ZIP 双向核对——README 列出的携带路径都必须是实际条目，每个条目也都必须被 README 描述。零字节预检超时或未到上限的读取超时仍为 FAIL，不因已收到 README 而容忍 curl 28。
  - #159 后端修正：Archive 仍在响应前完成全部资产摘要及隔离检查。对目标任务的七个已有资产串行采样，原隔离检查合计 84.257 秒，热点是二进制窗口上的重复地址正则匹配，不是 SHA256。加入每次原始/解码正则匹配前的必要字面量预筛后，同一诊断流程降至 29.485 秒；所有资产仍通过完整检查。这是临时 helper 的测量，不是部署后 HTTP 验收。预筛保留原正则、三轮解码、UTF-16 投影和重叠窗口，不缓存或省略安全检查。59 秒期限与该后端修正一起交付，不以放宽超时替代性能修复；实际前 4 MiB 延迟仍须发布后测量。
  - 总大小按 Content-Range 总值或 200 响应的 Content-Length 判断：已知且低于 `ARCHIVE_MAX_BYTES`（默认 200 MiB）时保留完整 `zipfile` 检查；否则只做首项检查并明确输出 `partial check only`，不是对中央目录、其余条目或完整 ZIP 的验收。Go Handler 支持（#223） `Accept-Ranges: bytes` 与单区间 Range：带 `Range` 的请求先物化 ZIP，再由 `http.ServeContent` 返回 206、`Content-Range` 与 `Content-Length`；无 `Range` 的普通响应仍是流式 200、不显式设置 Content-Length。因此脚本的首个 Range 请求通常能读到总大小并走完整检查。
  - 离线自测（不用凭据、不访问服务器）：`python3 scripts/smoke/test-archive-check.py`。覆盖 stored/deflate、数据描述符、错误首项、README 地址、损坏/截断、59 秒默认期限，README 布局（单模型在根目录、OBJ+随行依赖保留文件夹、缺失引用不构成理由、`other/`、同内容文件只收录一份的「内容相同的文件」一节不算条目、仅依赖包、不安全与未知路径），以及本机隔离 HTTP 的 206、忽略 Range 的 200、未知大小、小包全量检查、零字节预检超时和已收到 README 后的读取超时。后端差分与基准：`go test ./internal/assets/local -run 'TestAddress(Scan|Marker)'`、`go test ./internal/assets/local -run '^$' -bench BenchmarkAddressMarkerPrefilter -benchmem`。镜像保留 mock 测试：`python3 scripts/smoke/test-image-retention.py`。发布环境合同（默认命令逐字不变、环境文件、晋级拒绝、bootstrap）：`bash scripts/smoke/test-release-stack.sh`。
  - Agent 配置读管理员接口 `GET /api/admin/agent-configuration`（非管理员账号退回 `GET /api/v1/agent-configuration/effective?creative_domain=model-3d`）。已发布的旧配置不会自动带上新工具；优先读取当前版本，保留系统提示、Skill、既有工具及其他自定义设置，仅补齐缺失的工具后重新发布，使用当前 `expected_revision` 和同源 `Origin`，再回读生效配置核对。只有明确要恢复默认时才在管理页「Agent 配置」重置，或调用 `POST /api/admin/agent-configuration/reset`（同样带 `expected_revision` 和同源 `Origin`）；重置会把系统提示、Skill 和工具恢复为默认，不能用于无差别补工具。
  - 最新任务归档契约离线自测（不调用网络）：`python3 scripts/smoke/test-agent-mode-archive.py`。执行冒烟脚本第 9 节并用 stub 响应覆盖最新超大归档 + 较旧可下载任务回退、`archive_available=true` 却 413、false 却 200/409、字段缺失/类型错误、错误 413 code、无可用归档、无 3D 任务 SKIP、候选上限与最新优先/去重，以及 formats 缺失/列表类型/坏元素/坏 status/重复或错误 ID 时立即 FAIL 且不请求 ZIP。该离线测试里的 `read-archive.sh` 是控制流 stub；真实 ZIP 200/206、README、字节与解析校验由 `python3 scripts/smoke/test-archive-check.py`（17 个测试）覆盖。
  - Agent 配置离线自测（不调用网络）：`python3 scripts/smoke/test-agent-mode-configuration.py`。直接执行冒烟脚本中的配置检查块，用 stub 响应覆盖旧四工具配置拒绝、六工具配置通过、非管理员生效配置回退、分别缺失两个 Processing Plan 工具、已知工具目录不匹配或为空、配置接口不可读，以及 JSON 损坏、必需字段缺失和类型错误；另覆盖按实际可用性要求转换工具、全部不可用不追加、Schema 工具改名和路由映射、管理员/effective 两条分支，以及 discovery/Schema 的 HTTP、JSON、字段和类型错误；共 73 个响应场景，并核对只请求声明的只读端点、错误不泄露响应内容。
- L2 覆盖两部分。第一部分：2D 审批方式（`budget`）= 策略批准——一张小额文生图，不出现待批准同意，任务 SUCCEEDED，批准来源为 `policy`，显示本步和会话累计积分；脚本能读到 SQLite（`ART_AI_DB`，服务器上缺省 `$ART_AI_SERVER_ROOT/production/data/art-ai.db`）时再核对 `grant.decided_by = policy:budget`，读不到打印 SKIP（严格方式下 2D 也出卡，#702）。第二部分：3D 手工（严格）方式 = 人工批准——A/B 双会话分别触发确切的 3D 生成待批准执行、不冻结 C；拒绝 B 并断言零任务；A 默认停止本轮并断言零任务，只有设了 `L2_APPROVE_3D=1` 才批准 A（一次真实 3D 生成，约 25 积分）并断言 SUCCEEDED。从 GET 读取对应 request ID 后才发送决策，未读到当前 request ID 时不发送。
- 测试数据按协议**保留**在 `dev-test`，不清理；发布备份快照是另一类数据，按 #364 的保留规则处理。
- L4 在 `dev-test` 主机的 Plinth 测试栈运行：先从 `<root>/production/runtime.env` 导出管理员登录环境变量，再以 `ART_AI_LIVE_ACK=1 python3 scripts/smoke/smoke-l4-3d-isolation.py --live` 执行（#219：双重开关，目标须为真实形态；原 `ART_AI_LIVE_3D_ACK` 不再使用）。脚本作为测试操作员批准处于 `pending` 的冻结任务（会话批准方式已批准的授权不再重复决定），等待公开 Task 成功，核对 Meshy 六格式与 Tripo 六格式的本地 URL、两次下载字节、大小和 SHA-256；不会输出密钥或原始上游响应。`ART_AI_3D_SESSION_ID` 可让第一个 3D 任务接在已有 2D 生成会话中，用于工作区往返验收。它不代替真实浏览器网络检查与离线依赖检查。
- **2D 成品图的抽样口径见 `dev_test.md` §11.3**（每轮随机抽样、2D 不限量；
  一批图仍无问题时别靠加量，先分析公共路径）。本目录不另定标准。
- 单元/集成/竞态门禁由本地门禁与 CI 覆盖（各项在哪里执行见 `dev_test.md` §2），不属于本目录职责。

## L5 真实能力回归

L5 用真实请求回归关键能力链路，每个脚本只测一个能力范围，共用 `l5_common.py`（登录、会话、发消息、等待判定、`toolCalls` 解析、报告）。脚本会产生真实费用，没有同时给出 `--live` 与 `ART_AI_LIVE_ACK=1`，或目标是 fake 形态时直接拒绝执行（退出码 2，#219）。

```
ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-<名称>.py --live --report <目录>
ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- bash scripts/smoke/smoke-l5-all.sh --live --report <目录>
```

`release-env.py smoke` 提供 `BASE`、`ADMIN_*`、`EXPECT_SHA`；L5 只通过它在本地运行。输出 `PASS` / `FAIL` / `SKIP` 计数，有 FAIL 时退出码为 1；`--report <目录>` 写每个脚本的 JSON 报告（三视图脚本还保存各视图图片；四视图复现脚本另写 evidence，并把「复现到什么」记为 INFO，不计入三个计数）。脚本不输出凭据、Cookie、签名 URL 的 query 或提示词全文。生成模型参数一律传 Offering 的 `model_id`，模型与参数选项从 Schema 读取，不写死厂商。

等待规则：先等本轮回复成为最终内容（不再是 `__GENERATING__` 占位），再等预期能力的任务都已创建并全部进入终态，任务未创建前不判定结束。硬断言只看工具调用、任务状态、必需产物和审批卡片，对 LLM 文字最多做关键词检查。

| 脚本 | 覆盖范围 | 费用量级 |
|---|---|---|
| `smoke-l5-chat-models.py` | 每个可用对话模型 × 其每个思考级别各一轮，回复为最终内容且无错误 | 仅对话 token |
| `smoke-l5-2d-to-3d.py` | 全新会话首轮开始后生成标题；同一轮先 2D 再 3D，两个任务 SUCCEEDED、必需产物 READY | 一张图加一次图生 3D |
| `smoke-l5-parallel.py` | 同一轮并行提交两张图，两个任务 SUCCEEDED | 两张图 |
| `smoke-l5-three-view.py` | 左右不对称角色图 → 三视图 → 补齐 `left`、`back` → 多视图 3D（必须调用 `create_model_3d_from_views`）；各视图保存为 `view-<键>.png` 供人工核对 `left` 方向 | 约 4 张图加一次多视图 3D |
| `smoke-l5-split.py` | 拆分生成：Tripo 网格分割、Hyper3D Bang（需 Rodin 来源）、图片智能分割；某链路在当前 Schema 不可用时输出 SKIP 并写明原因；`L5_SPLIT_ONLY=segment,bang,image` 可只跑部分链路 | 两到三次 3D 生成加分割 |
| `smoke-l5-2d-params.py` | 按 Schema 声明的输出尺寸跑矩阵（1:1、竖版、横版、一个非默认尺寸，每个可用文生图模型至少一张）；每个会话先用 `model2d` 锁定模型，按 `model_id` 认交付任务并核对真实像素比例；图生图单参考图保持原比例，多参考图时 `reference_purposes` 生效。`L5_2D_MODELS=<model_id|provider_id>`、`L5_2D_SHAPES=portrait`、`L5_2D_SKIP_MATRIX=1`、`L5_2D_SKIP_REFERENCES=1` 可限流 | #242 后每个提示可能先出 4 张概念草图再出定稿；按 provider 计数，单提示约 5 次请求 |
| `smoke-l5-canvas.py` | 经场景 API 放入两张图、移动与缩放（每次编辑修订号变化），再带着画板快照附件（脚本用标准库把两张源图左右拼成一张 PNG，字段与浏览器一致；源图不是 PNG 时 FAIL 并写明格式）按画板改图、把画板转 3D；核对画板附件存在、改图不引用旧的单图句柄；画板未变时第二轮重发相同快照沿用原句柄：第二轮用户消息不新增 `@img` 句柄，整个会话只有一个画板快照句柄（#53；轮次 selection 不在公开 API 中，第二轮改图引用仅随详情输出供人工核对） | 两到三张图加一次 3D |
| `smoke-l5-3d-params.py` | 每个可用 3D Provider 一条：分部件或贴图开关、全部声明格式须交付、参数进入冻结输入；再核对一次格式补齐请求 | 每个 Provider 一次 3D 生成 |
| `smoke-l5-2d-split.py` | 2D 拆分：角色图拆成部件图（至少三张，且以角色图为参考）、部件抠图、部件图转 3D | 约 6 张图加一次 3D |
| `smoke-l5-agent-hard.py` | 刁钻对话：一句话多步、中途改主意、按序号引用、前后矛盾、不支持的能力、超限尺寸、手工批准卡片、模糊指代、英文与中英混合、口语三视图；`L5_AGENT_HARD_ONLY=用例名,…` 可只跑部分 | 约 10 张图和两到三次 3D |
| `smoke-l5-budget.py` | 「审批」批准方式（#103；新会话默认是自动方式，#406，所以两条链各用一个新会话并先显式切到「审批」方式，API 值为 `budget`）。批准链：读回的批准方式须为 `budget`，锁定一个有报价的文生 3D 模型（`L5_BUDGET_MODEL=<model_id>` 可指定，否则优先 Tripo P2，其次其他 Tripo 模型），发多步请求（生成游戏道具、检查、必要时优化，并明确要求先提交制作计划：#702 起 Agent 不主动提议计划）后须出现待批准的制作计划卡（预算 ID、按单位的限额 `budgets`、各单位报价 `estimated_totals` 与步骤，每个用到的单位都要有不低于报价的限额），按 Schema 的 `chat_session_budget` 路由像浏览器一样批准；批准响应须带 `turn_message_id`，脚本跟随服务端自动开启的这一轮（#134），不再补发消息；计划执行期间不出现逐步付费批准卡，任务价格逐单位不超过该单位的计划限额，服务端账本（会话预算 `units[]` 的已扣费加预留）也逐单位不超过限额，至少一个 3D 任务 SUCCEEDED 且必需产物 READY。拒绝链：同样请求，拒绝计划卡，拒绝响应不带 `turn_message_id`（不开新一轮），零任务、零扣费、没有遗留的待批准卡，回复说明没有执行；`L5_BUDGET_ONLY=approve,deny` 可只跑部分链路 | 一次 3D 生成，加计划内可能的检查或优化步骤；拒绝链不生成 |
| `smoke-l5-four-view-multiview.py` | #417：美术反馈的「四视图 → Meshy 7.1 多视图 3D」在「审批」方式（API 值 `budget`）的制作计划卡下复现。新会话 `PUT approval` 为 `budget`，3D 选择器锁 `meshy-7.1`（单视角），2D 锁 6 积分模型；上传一张角色图（`--image`，或 `--generate-image` 多付一张图）并说「先用这张图做四视图（front/left/back/right），再用四视图生成 3D」，并明确要求先提交制作计划（#702 起 Agent 不主动提议计划）；记录计划卡的 `budgets`/`estimated_totals`/步骤并按 Schema 路由批准。`right` 是可选视角，服务端不会自动续跑：四视图组出现且 `right` 可启动时，脚本显式 `POST .../three-views/{run}/views/right`（429 重试），Agent 自己启动则跳过。计划外的 `image-to-image` 逐步卡按「人」批准并记录，`image-to-3D` 逐步卡保持待批准（这正是「第 5 步停住」的形态）。四视图全部成功后若回合结束而第 5 步没执行，再发一条 3D 域自然语言消息（**不**传结构化 multiview 参数），观察 Agent 是否调用 `create_model_3d_from_views`、是否省略 `model`。报告额外写 `smoke-l5-four-view-multiview-evidence.json`（工具调用与实参、计划估价与各任务 `native_quote` 逐单位对比、任务 Offering/状态、视角状态、耗时、`verdict`）；`verdict` 取 `reproduced` / `not_reproduced` / `inconclusive`，复现失败本身不算脚本失败。`--control/--no-control`（默认跑）另开自动方式会话锁 `meshy-7.1` 单视角，要求一个 image-to-3D 任务 SUCCEEDED。`--max-paid`（默认 6）是整轮付费任务上限，超过即停止并报告；可用 `L5_FOUR_VIEW_IMAGE`、`L5_FOUR_VIEW_MAX_PAID`、`L5_FOUR_VIEW_MODEL` 替代同名参数 | 四视图 4 张（约 24 积分）+ 多视图 3D 一次 + 对照单视角 3D 一次；`--generate-image` 再加一张图 |
| `smoke-l5-chat-wire.py` | #536：对话模型一轮线路验收，只经 Plinth 聊天 API：同一会话先流式文本回合（一句话、无工具），再只读工具回合（`list_offerings`，上一回合的 reasoning 回传，I28）；会话标题生成；另开会话选读图模型（`--vision-model`，默认 `qwen3.8-omni-flash`，部署未提供则 SKIP 并列出可用模型）传一张 96 像素的探针图并提问。硬断言只看回合结束、无失败文案与工具错误、工具名、零生成任务、标题已生成，不看模型措辞。门禁是 `fake-default`：fake 目标无开关即可运行（给 `--live` 被拒），mixed 或 live 目标需要 `--live` 与 `ART_AI_LIVE_ACK=1`，缺一或只设一半退出码 2。真实运行须先取得用户当次同意；条带坏图（绕过 #535 最小尺寸）不是这个脚本的回合，用独立只读探测取。证据写 `smoke-l5-chat-wire-evidence.json`；要自动回灌 fake，发布时开 `UPSTREAM_CAPTURE_DIR` | 仅对话 token |
| `smoke-l5-astra-tools.py` | #417/#401：新会话用 `PUT /api/v1/chat/sessions/{id}/model` 选 `gpt-astra`（`--chat-model` 可改），发一条需要只读工具的话术（查可用 3D 模型并报最便宜的），断言回合正常结束、无失败文案与工具错误、`toolCalls` 含 `list_offerings`、会话零生成任务（只花对话 token）。部署未提供该模型时 SKIP 并列出可用模型 | 对话 token |

测试数据按协议保留在 `dev-test`。纯逻辑的离线单元测试：`python3 -m unittest scripts.tests.test_l5_common`。

#417 的两个脚本只有离线部分进门禁：`scripts/tests` 的单测门禁（`unittest discover`，见 `dev_test.md` §2 第 10 项）会自动收入 `test_l5_four_view.py`，两个脚本在缺开关时的拒绝（退出码 2）也由 `test_l5_common.py` 与 `test_upstream_shape.py` 覆盖；真实链路本身不进门禁，只在当次同意下手动运行。离线单测：`python3 -m unittest scripts.tests.test_l5_four_view` 覆盖四视图脚本的模型选择、计划/任务逐单位报价对比、视角终态与 `verdict` 判定，单测本身不访问网络。`smoke-l5-astra-tools.py` 的只读回合由 live 运行验证，离线只做脚本拒绝检查（无开关时退出码 2）。

`smoke-l5-all.sh` 默认逐项运行对话模型、`astra-tools` 与 `chat-wire`；四视图链最贵且需要源图，只有显式设置 `L5_INCLUDE_FOUR_VIEW=1` 才加入，源图用 `L5_FOUR_VIEW_IMAGE=<本地图片>` 传入（没有源图时 `four-view-multiview` 记为 SKIP 并说明 `--image`）。单跑四视图链：

```
ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- \
  python3 scripts/smoke/smoke-l5-four-view-multiview.py --live --report <目录> --image <角色图路径> --max-paid 6
ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- \
  python3 scripts/smoke/smoke-l5-astra-tools.py --live --report <目录>
```

四视图链的 `--generate-image` 会先用自动方式会话生成一张角色图（多一次付费图任务，`--max-paid` 需相应加 1）；`--no-control` 跳过单视角对照组。脚本不批准计划外的 `image-to-3D` 逐步卡，故成功复现后该会话会留待处理的卡，属预期证据，不自动清理。

### 3D 端点链脚本（#539 Meshy 与 Hyper3D Rodin，#567 Tripo）

1.9.0 发布前「每个 Provider 的每个 API 功能只调用真实上游一轮」用三个脚本覆盖 Meshy、Rodin 与 Tripo 的 3D 端点。它们和上面的 Agent 脚本不同：不经 Agent，固定 Offering，直接走公开任务 API 的 `prepare`、`grants`、`tasks` 三步，公共逻辑在 `l5_endpoints.py`。

**默认连 fake。** 门禁用 `upstream_shape.py` 的 fake-default：目标是 fake 形态（`ART_AI_UPSTREAM_SHAPE=fake`，`release-env.py smoke` 会按环境设置）时不带任何开关直接运行，并拒绝 `--live`；目标不是 fake（live、mixed 或形态未知）时必须同时给 `--live` 与 `ART_AI_LIVE_ACK=1`，缺一退出码 2，且在发出任何请求之前。每次真实运行还须先在对话里取得用户当次同意（`AGENTS.md`「上游接口与 Fake」）。

| 脚本 | 链路（每步一个请求，后者以前者为来源） | 预计费用与默认上限 |
|---|---|---|
| `smoke-l5-meshy-endpoints.py` | 文生图 `nano-banana-2`（T-pose 人形）→ 图生图（侧视图）→ t2.5 文生 3D（preview 与 refine 在同一 Task 内）→ t2.5 图生 3D（2k）→ 7.1 多图生 3D（两张图，2k 几何、8K 纹理；8K 被明确拒绝时另发一个 2K 纹理的新请求 `multiview_2k`，不是重放）→ remesh（来源 t2.5 图生 3D，三角面、10000 面）→ convert、retexture、rigging、uv-unwrap、resize、analyze、multi-color（共用 remesh 来源）→ animation（接 rigging）→ text-to-motion（独立） | 按 Catalog 预估合计约 142 Meshy credits（逐步：文生图 6、图生图 6、t2.5 文生 3D 15、t2.5 图生 3D 20、7.1 多图 40、remesh 5、convert 1、retexture 15、rigging 5、animation 3、uv-unwrap 5、resize 1、analyze 0、multi-color 10、text-to-motion 10；审计表的官方价合计约 132，差在 t2.5 图生 3D 与 retexture 的默认预估高于 2K 官方价：retexture 冻结报价 15、2K 真实实扣约 10，#633）；默认 `--max-credits 160`；t2.5 refine 价与积分单价官方未列（2026-11-24 真实一轮 refine 2k 实扣 10）|
| `smoke-l5-hyper3d.py` | Rodin 文生 3D（Quad、10000 面、`texture_mode=high`、TAPose、quad_normal、seed 1）→ Bang（来源任务）→ 取回其 GLB 作为 Bang 上传模式与 Texture 的模型（超过 Offering 的上传上限则这两步 SKIP 并说明）→ 图生 3D（两张图只带一个 `image_label`，Adapter 补 `?`） | 约 5.0 Hyper3D credits（文生 1.0、Bang 两次各 0.5、Texture 产品路径默认 2.0、图生 1.0）；默认 `--max-credits 7`；`--no-texture` 去掉 Texture；需要 Full API access |
| `smoke-l5-tripo-endpoints.py` | v3.1 文生 3D（只要几何：纹理与 PBR 关、标准几何、3000 面、只导出 GLTF，`text_geo`）→ rig-check → rigging v2.5 与 v3.0 → retarget（接 v3.0 绑骨，`preset:walk`）→ 三角面降面（1000 面）→ segment → quick_cap 补全与 AI 补全（都接 segment）→ texture（Offering 默认值）→ 取回 `text_geo` 的 GLB 上传，转 STL（`file_token` 输入）与 Smart Segmentation（模型）；独立的：图生 3D、多视图生 3D（正面加左侧两张）、图生四视图 → 编辑其正面、图生 splat、Smart Segmentation（图片）；P2 文/图/多视图生 3D（纹理关、3000 面）；最后一次产品默认参数的文生 3D（极致纹理、精细几何、六种 `output_formats`）。输入图由脚本绘制（白底上几块色块拼成的人形），上传的 GLB 是 `text_geo` 自己的产物 | 按 Catalog 约 1290 Tripo 积分：text_geo 15、rig_check 0、rigging 两次各 45、retarget 10、降面 95、segment 90、quick_cap 80、AI 补全 100、texture 80、转 STL 5、Smart Segmentation 模型 105 / 图片 135、图生 25、多视图 25、四视图 10、编辑 5、splat 30（官方未公布价，按 Catalog 预估）、P2 三项各 100、默认文生 90。加工类 Offering 的附加导出格式是固定的（每种 +10），所以只按基础价算的约 840 达不到；默认 `--max-credits 900`，会在 P2 之前停下，整链要显式给 `--max-credits 1300`（或之后用 `--only/--seed` 补跑）。没有 `--cleanup`：Tripo 结果无需删除 |

通用规则：

- **每个 POST 只发一次。** 失败或超时只读取任务（Observe），不重放；要再跑某一步，用 `--only 步骤名 --seed 来源步骤=任务ID`（种子任务都在同一个会话时，这一轮沿用该会话而不新开，因为 Tripo 编辑多视图只接受同一会话的图生四视图任务，#625；须用种子任务所属账号登录；新请求，须先读懂第一次为什么失败）。服务端只在 Rodin 对提交答 `503` 且带 `Retry-After`（上游明确未受理）时重发，由 Catalog 连接声明（`retry_submit_server_error`，#563 收窄），脚本关不掉；重发用尽，以及其他 5xx 或超时，该步按「提交结果不明」结束、可能已计费，在收尾的余额对账里显现。
- **预算上限。** 每步提交前用冻结报价（没有就用 Offering 预估价，免费步骤另行声明）累计，超过 `--max-credits` 的那一步与之后的步骤都不发送，报告里记为未提交。
- **余额对账。** 整轮前后各读一次公开余额面板（面板有短时缓存，收尾前等 `--balance-wait` 秒），差值与各步报价之和写入 `RECONCILE` 行与证据文件；报价是估算，差值不当作通过条件，只要求余额没有增加。
- **产物检查。** 必需槽位齐全、每个产物的字节数与 SHA-256 与公开声明一致、模型文件二次下载一致、魔数与格式相符；GLB 从自身字节读三角面、贴图边长、网格数与包围盒高度。Meshy 的三角面上限、resize 高度，以及 Bang 的分部件数，只对真实上游强制（fake 的模型是固定夹具，数值只记录）；Meshy 的 `target_polycount` 是目标而不是硬上限，2026-11-24 真实一轮 t2.5 文生 3D 目标 15000 得到 15804 三角面（+5.4%，实测最大上浮），所以上限取目标的 110%（约两倍实测上浮），仍能查出 Quad 被按两个三角面计（+100%）或目标被忽略；Rodin 文生 3D 的三角面按「每个 Quad 面约 2.18 个三角面，±35%」强制，fake 与真实上游都查（2026-11-24 真实一轮 10000 面得到 21002，约 2.10，落在区间内）。
- **原始结果。** 任务载荷与下载的字节只写到工作树外的 0700 临时目录，结束即删除（`--keep-raw` 保留并打印路径）；`--report <目录>` 只写脱敏的 `<脚本>.json` 与 `<脚本>-evidence.json`（每步的 Offering、任务 ID、状态序列、报价、产物角色/大小/SHA-256 前缀、失败类别，不含 URL 查询串、提示词与凭据）。密钥只读环境变量，从不打印。
- **Tripo 的每步对账。** 公开任务 API 不暴露上游的 `credits_consumed`，所以脚本每步核对三件事：任务自己的 `native_quote` 等于 prepare 时冻结的报价（生成类），任务的 `credits_charged` 不超过这一步的上限（加工类用公开 Offering 的 `maximum_estimated_credits`，即基础价加固定导出格式），以及这一步前后公开余额面板的变化（`INFO: <步骤> cost: quoted …, balance moved …`，写入证据的 `balance_moved`）。真实目标上每次读余额先等 `--balance-wait` 秒（面板缓存 30 秒），`--no-step-balance` 关掉逐步读取；余额变化与报价不符只记为证据（账号的其他用途或缓存也会让它动），整轮仍以开头与收尾的 `RECONCILE` 对账。fake 只给任务计费、账户余额固定，所以 fake 上逐步余额不动，属预期。
- **Tripo 的成功后格式。** v3.1 生成的主模型与 GLTF 到位即 `SUCCEEDED`，其余请求的格式在成功后陆续补上（#462）；脚本在 `resources_pending` 清空、请求的格式都到齐后才核对格式，并对后到的每个格式重新下载、核对字节数、SHA-256 与魔数（只读任务，不重发）。
- **Tripo 未覆盖。** `tripo/tripo-v3.1/format-conversion`（以任务为输入的转换）、非 full 的 `tripo/tripo-v3.1/text-to-3d` 与 `image-to-3d`、`tripo/geometry/decimate-quad` 在 Catalog 里是停用的，不在公开 API 上；任务的六种格式由默认文生的 `output_formats` 与降面的固定导出覆盖。
- **Tripo 降面的三角面上限。** Tripo 把 `face_limit` 当目标而不是上限：2026-11-24 的真实一轮（#577）请求 1000（v3.0、`quad=false`、`bake`、`smart_uv`，任务回显 1000），来源 2814 个三角面，交付 1566 个（+57%，STL 导出按字节算同为 1566），所以 Plinth 传参无误。脚本对降面取目标的两倍为上限（`DECIMATE_SLACK`），并要求结果少于来源 `text_geo` 的三角面数，两项都只对真实上游强制；其余 Tripo 生成的三角面仍按 `BOUND_SLACK`（5%）。
- **Tripo 的真实样本（2026-11-24 真实一轮，#577）。** 这一轮的上游脱敏录制已回灌 fake：样本是 `internal/testutil/fakeupstream/tripo/v3/testdata/evidence-577-real-round.*`，回归是同目录的 `real_round_577_test.go`；fake 的轮询改为按请求回显参数、queued 起即报全价、image-to-splat 30 积分、导入模型的转换带 `normalize_*`、渲染图一律 WebP。仍是构造、没有真实样本的部分：P2 三个 Offering（文生、图生、多视图）、图片 Smart Segmentation、并发 2000、限流 1007。拒绝文案由对齐工具的免费否定探测采集（`tripo/v3` Profile 的 `probe-*` 步骤，全部 `OptIn`、非 `Paid`，用 `-steps upload,probe-…` 点名运行；清单与各步的真实答复见 `align.go` 的 `Probes`）。2026-11-25 已对官方 API 跑过一次（12 个请求、0 积分、全部被拒），样本是 `evidence-577-negative-probes.*`：已退役 model 与未知 model 同为 400/1004 `invalid model …, allowed values: …`（按字典序），不是 2015；`generate_parts`、fast 配 v2.5、`seg_type`、convert 格式的 1004 文案，上传 2003/2004 的 suggestion，以及错误 Key 的 401 `Invalid API key` 都已按真实回灌。2015 等业务码和其余 1004 文案仍是构造。
- **Meshy 收尾。** Plinth 的公开 API 没有上游删除，也不暴露上游作业 ID，所以收尾是单独、显式的命令：运行写出的证据文件里有按依赖逆序排好的 `cleanup`（任务 ID 与上游路径）；操作者从服务端库（`generation_attempts.upstream_job_id`，按创建顺序）整理出 `{任务ID: [上游作业ID, …]}`，再运行 `python3 scripts/smoke/smoke-l5-meshy-endpoints.py --cleanup 证据.json --remote-ids 映射.json`（环境里要有 `MESHY_API_KEY`，非官方地址再设 `MESHY_BASE_URL`）。每个 DELETE 只发一次，依赖它的先删、refine 先于 preview；404 视为已删，409（仍在运行）报告而不重试。指向非回环地址同样要双开关。Meshy 结果只保留三天，所以真实一轮的步骤不要拖到隔天。

本机验证：`python3 -m unittest scripts.tests.test_l5_endpoints`（离线，随脚本单测门禁运行）覆盖门禁、一次提交、预算、计划、检查与收尾；整链对 fake 栈的验证用 `python3 scripts/smoke/smoke-fake-stack.py --only meshy-endpoints,hyper3d-endpoints,tripo-endpoints`（作为进程运行三个脚本，fake 形态、无开关，Tripo 链带 `--max-credits 1400` 以跑完全部步骤；不在 `verify-local.py` 与 CI 的门禁子集里，Tripo 链在 fake 上约需四到五分钟，在不带 `--only` 的全量运行里；全程零真实上游）。这一整链第一次对 fake 运行就发现 `meshy/meshy-t2.5/image-to-3d` 默认请求会把空的 `pose_mode` 发给 Meshy（其余三个 Meshy 生成 Offering 都声明了「为空则不发」），已补 Catalog 的 `omit_when`，回归是 `internal/catalog` 的 `TestEveryMeshyGenerationOfferingOmitsAnEmptyPoseMode`。

两条链已在 `dev-test` 真实形态各跑过一轮（2026-11-24，#568；Meshy 15 个功能实扣 132 积分，Rodin 5 个请求实扣 5 credit），上游脱敏录制已回灌 fake：Meshy 的结构、状态序列、价格、格式与 CDN 内容类型都与 fake 一致，差在任务对象回显哪些分辨率键（改为按请求回显：几何 `2k`/`4k` 回显并置 `ultra_mode`，贴图只有 `4k`/`8k` 回显并置 `hd_texture`，2K 不回显）；Rodin 的 Texture 产物按分辨率命名为 `base_high_pbr.glb`、贴图边长按请求（Catalog 表单 extreme-high + High 为 12288）。样本与对照证据见 `internal/testutil/fakeupstream/meshy/openapi-v1|v2/testdata/*-real-round-2026-11-24.*` 与 `hyper3d/api-v2/testdata/evidence-568-processing-high.*`，回归测试是各目录的 `real_round_568_test.go`；未回灌的差异（Bang/Texture 的子作业数、Rodin Quad 系数的第二个样本、Meshy 产物文件名）记在 `docs/architecture.md` 的 fake 小节。

Rodin 还有几项只能直连上游探测，由对齐工具 `cmd/fake-upstream-align` 的 `hyper3d/api-v2` Profile 新增的步骤承担（每个提交步骤带两个免费的 `-status`、`-download` 后续步骤，全部标 `OptIn`，默认的官方运行不发，须用 `-steps` 点名单独运行、用户当次同意、带 `-confirm-paid`，全部按官方页面构造、待真实一轮定稿）：`balance-usage`（`check_balance?usage_page=1`，免费，取 `usage[]` 形态）；`reject-seed-probe`（`seed=65536`，超出文档上限，看真实拒绝是 400 信封还是 HTTP 201 加 `INVALID_REQUEST`，被受理则计 0.5）；`rodin-text-quadpose`（游戏角色请求的原始体，约 1）；`rodin-image-label-short`（两张图一个 `image_label`，直接发短列表看服务是否受理，约 0.5）；`bang-upload`（model 上传模式加参考图，约 0.5，模型取 `bang` 步骤的产物）；`texture-images`（Texture 参考图用官方页面的复数字段 `images`，Plinth 与 fake 现用单数 `image`，约 0.5，结果决定是否改 Catalog 的 `wire_name`；fake 目前只认单数，所以该步骤暂不对 fake 重跑）。Meshy 的对齐 Profile 已含 DELETE、余额、各生成与加工端点，本轮不新增。

### 混合形态：角色只在画板上时的三视图路由（#688）

`smoke-live-chat-three-view-route.py` 复测 #688：角色图只放在会话画板上（上传后作为场景对象放置，不进对话），手选三视图 Skill 后发「把这个角色的三视图图片都生成出来。」（非画板轮）。断言 Agent 先调用 `find_history_images` / `list_project_assets` 查看，回复指向画板路径（「以画板为主」或 `@画布`），不先让用户描述角色，也不发起三视图；随后发一个画板轮（`inputSource: canvas`，画板快照作为 selection，与浏览器一致）「看画板」，断言调用 `create_three_view`、正面停在批准卡上，脚本拒绝这张卡。每一轮都输出工具调用摘要（工具名与截短的参数）。图片默认用标准库画一个简单人形，可用 `--image PATH` 换成真实角色图。

- **只对 dev-test 的混合形态运行**：须是 `release-env.py release dev-test --live-chat` 发布的栈（形态 `mixed`），fake 与真实形态都拒绝；`RELEASE_ENV` 必须是 `dev-test`，本机有 `deploy/local/environments.json` 时 `BASE` 还须等于其中 dev-test 的 `base_url`；必须同时给 `--live` 与 `ART_AI_LIVE_ACK=1`。缺任一条退出 2。
- **成本**：只有两轮真实对话模型的 token；生成侧是 fake，会话用严格方式且批准卡被拒绝，不出图、不计费。每次运行前仍须在当前对话取得用户同意。
- **用法**：`ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-live-chat-three-view-route.py --live [--image PATH] [--report DIR]`。退出码：0 = 无 FAIL（SKIP 不算通过），1 = 有 FAIL，2 = 拒绝运行。
- 脚本只做过语法检查，尚未对混合形态实跑；判定用的回复措辞（「以画板为主」「@画布」、是否先要求描述角色）是启发式检查，FAIL 时以输出的回复与工具调用摘要人工复核。

## 直连 L5（#540）

`smoke-l5-2d-direct.py`、`smoke-l5-matting.py`、`matting-probe.py` 不经 Agent：每个任务用公开任务接口（`POST /api/v1/tasks/prepare`、`/grants`、`/tasks`）原生提交，各步骤至多发一次，观察 Task 并核对公开交付。共用 `l5_direct.py`（门禁、图片构造、私有目录、一次性提交、证据脱敏）与 `l5_common.py` 的登录和客户端。它们与上面的 L5 脚本不同的地方是门禁：用 `upstream_shape.py` 的 `fake-default`。

- **fake 形态**（`ART_AI_UPSTREAM_SHAPE=fake`，`release-env.py smoke` 在 fake 形态的 dev-test 上会设置）：直接运行，零费用，不接受 `--live`（给了也拒绝，退出码 2）。
- **真实或混合形态**：必须同时给 `--live` 与 `ART_AI_LIVE_ACK=1`，缺一退出 2，未知形态按真实处理；每次真实运行前须在当前对话取得用户同意（`AGENTS.md`「上游接口与 Fake」）。三个脚本都不在 `smoke-l5-all.sh` 里。
- **fake 与真实的判定差别**（混合形态的生成侧与抠图服务也是 fake，按 fake 判定，但门禁仍要求两个开关）：fake 还没建模的行为（固定小图尺寸与固定 JPEG、抠图返回不透明白底 64x64、没有 `/openapi.json`、只有部分参数界）在 fake 形态只记为 INFO（输出 `INFO (not modelled by the fake yet)`，并写入证据的 `info`），不算通过；同一偏差在真实形态记为 FAIL。这样脚本本身能在 fake 栈上验收，又不会把 fake 缺口当成通过。

```
python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-2d-direct.py
ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-2d-direct.py --live --report <目录> [--large] [--only 任务键,...]
ART_AI_LIVE_ACK=1 python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-l5-matting.py --live --report <目录>
ART_AI_LIVE_ACK=1 MATTING_BASE_URL=<服务地址> MATTING_API_KEY=<密钥> python3 scripts/smoke/matting-probe.py --live --report <目录>
```

**2D 七任务**（`smoke-l5-2d-direct.py`，一任务一提交，开服务端脱敏录制 `UPSTREAM_CAPTURE_DIR` 后再运行，见 `docs/deployment.md`「上游调用脱敏录制」，结束后用 `fake-upstream-align draft` 转样本）：

| 任务键 | 请求 | 预计费用（美元，转述估算） |
|---|---|---|
| `openai-generate` | gpt-image-2.6-sunburst 文生图：1536x1152、quality=low、n=2；两张图字节不同，解码尺寸记录偏差 | 0.02 |
| `openai-edits` | 图生图：两张 512 PNG（`image[]`）加同尺寸 mask，1024x1024、low、n=1 | 0.02 |
| `gemini-generate` | gemini-3-pro-image 文生图：16:9、1K（`--large` 为 4K） | 0.134（4K 0.24，需复核） |
| `gemini-edit` | 图生图：两张 512 参考图（其一带 alpha），3:4、1K | 0.134 |
| `jimeng-generate` | seedream-4-5 文生图：1920x1920，内嵌图片（`--large` 为 4096x4096） | 0.04 |
| `jimeng-edit` | 图生图：两张 512 参考图（其一带 alpha），1920x1920（`--large` 为 4096x4096） | 0.04 |
| `jimeng-sequence` | 组图：`sequential_image_generation=auto`、`max_images=2`，交付 1 到 2 张 | 0.08 |

默认一轮约 0.47 美元，`--large` 约 0.57 美元；网关与方舟的积分换算未知。`--budget-usd`（默认 0.75）是整轮估算花费的上限，会超的任务被跳过而不是发送，`--max-tasks`（默认 7）限制次数。公开 API 要求「建议不更改」的参数改动附理由，脚本为 `quality` 与 `image_size` 带固定理由（`parameter_reasons`）。Gemini 4K 与 Seedream 4096x4096 响应很大、耗时长，丢了响应的请求可能已被计费，所以只在显式 `--large` 时运行。每项断言：Task SUCCEEDED，交付张数在预期范围内且字节与声明的大小和 SHA-256 一致，多张图互不相同，像素比例符合请求；精确像素尺寸与请求不同、图片格式与惯例不同只记录偏差。

**内容拦截探针**（OpenAI moderation、Gemini 安全拦截、即梦敏感内容）不在默认任务里，因为可能触发账号风控。`--content-probe --provider openai|gemini|jimeng --probe-prompt-file <文件>` 只运行这一个探针，一个 Provider 一次提交，需要第三个开关 `ART_AI_CONTENT_PROBE_ACK=1`（用户须当次确认这段提示词），不能与 `--large`、`--only` 同用，fake 形态拒绝。提示词只读自文件、只发送一次，不打印也不落盘（证据只有长度与 SHA-256 前缀，私有目录只留脱敏摘要）；结果（Task 成功，或失败类别）只观察不判定。

**抠图**（`smoke-l5-matting.py`，零费用）：经 Plinth 提交一个批次：512x512 PNG、256x256 JPEG、一个扩展名 `.png` 的非图片字节（fake 形态改用带 fake 故障标记的 PNG，走同一条部分成功通知路径），默认参数（1024/1024，不用 2048，共享显卡）。期望 Task SUCCEEDED、交付两张带 alpha 的 PNG（颜色类型 6）且尺寸等于各自来源、公开通知为 `partial_inputs` 且 `inputs=[3]`、任务列表与详情一致。服务自己的批次状态词与第 3 个文件的状态、`invalid_reason` 不在公开 Task 里，取自服务端录制或 `matting-probe.py`；若服务对非图片字节整批返回 422，Task 为 FAILED，这就是结论，是否拆开探测由用户决定。`matting-probe.py` 直连服务（`MATTING_BASE_URL`、`MATTING_API_KEY`，只读环境变量），只读探测：`/health`、`/openapi.json`（三个端点、枚举与 PipelineParams 各字段的界，完整文档写到私有目录供做脱敏快照与漂移测试）、`birefnet_process_res=511`、`expand=1001`、`sensitivity=1.5` 的 422、非空错误密钥的状态与 `WWW-Authenticate`、未知批次与未知任务下载的 404、无密钥下载的 401；服务把不该受理的值受理时会留下一个 64x64 的批次，只记录不删除。

**原始结果与证据**：原始 Task JSON 与交付字节写到工作树外的 0700 目录（`--private-dir` 指定已存在的 0700 目录，缺省建临时目录），结束即清理，`--keep-private` 保留；`--report <目录>` 写通常的 PASS/FAIL/SKIP 报告和 `<脚本名>-evidence.json`（状态序列与耗时、每张图的格式、字节数、SHA-256 前缀、像素尺寸、报价与调整、冻结参数，不含提示词、签名 URL 查询、地址与密钥）。密钥只读环境变量。提交只发一次：提交结果未知（无应答或 5xx）只读任务列表观察，失败的 Task 不重试。

**对齐 Profile 的新增步骤**（`cmd/fake-upstream-align`；这些步骤标 `OptIn`：不带 `-steps` 的 `official`/`export` 不发它们，默认的付费清单与例行刷新的费用不变，只有 `-steps` 点名才发；均标付费，需 `-confirm-paid`，每次发起前按 `AGENTS.md`「上游接口与 Fake」说明次数与预计费用并取得当次同意；`fake` 与 `compare` 不受影响，fake 恒跑全部步骤；官方答案在真实运行前没有样本，fake 对应行为是 constructed）：OpenAI `generations-n2-1536x1152`、`generations-quality-max`（Catalog 默认 quality=max 的裁决，约 0.11 美元）、`generations-invalid-size`（1000x1000，预期免费 400）、`edits-two-images-mask`（`image[]` 两张加 `mask`）；Gemini `generate-16x9-4k`、`edit-3x4-two-references`、`generate-invalid-ratio`（7:1，网关可能 400 也可能静默出图）；Jimeng `generations-4096`、`edits-two-references-4096`、`generations-sequential-max2`、`generations-invalid-size`（1024x1024 低于 3686400 像素的官方下限，预期免费 400）。它们在进程内 fake 上全部可运行（`registry_test.go` 守）；现有 `alignment-report.md` 来自此前的官方运行，没有这些步骤，真实运行后用 `compare` 重生成。

离线单测：`python3 -m unittest scripts.tests.test_l5_direct`（门禁拒绝矩阵、输入对 Catalog 的契约、预算、图片、私有目录、一次性提交、提示词不落盘、抠图判定、探针对脚本化服务），随 `scripts/tests` 门禁运行。

## Agent 产出质量评测（#238）

固定请求集在 `scripts/smoke/agent-quality/requests.json`（版本 5：9 个 3D 用例：人形角色、硬表面道具、场景件、生物、打印手办、AR 商品、参考图生 3D、多步检查点加工、开放候选；7 个 2D 用例：角色立绘、图标/道具图、系列一致性、项目系列复用、3D 输入图、开放概念方向、多轮偏好），视觉量表与评分提示词在 `scripts/smoke/agent-quality/rubric.json`，两者都带版本号。共用逻辑在 `agent_quality.py`，离线单测：`python3 -m unittest scripts.tests.test_agent_quality`。

### 轨迹评测（fake，免费，可重复）

```
python3 scripts/smoke/smoke-fake-stack.py --only agent-quality --report <目录>
```

在 `smoke-fake-stack.py` 的隔离 fake 栈（真实 chat/编排/批准/Task/render 路径，fake LLM 只把请求集每个 step 的 tool 标记变成工具调用）上跑完整请求集。它评的是**系统对参考轨迹的实现**：用途卡、制作简报、付费批准、3D 任务、`inspect_model`、`render_model_views`、最终回复是否按请求集走通；不是模型自主选择的质量。`project` / `project_setup` 步骤只在轨迹运行器里搭建 #240 的项目夹具：建项目、把首张定稿图提升为标准形象、写项目风格指南、在项目里新开会话，不计入工具调用。硬断言是最终回复非失败、预期能力任务 SUCCEEDED、必需工具组与先后顺序、无失败任务；付费/工具/打断预算的数值只记录、不参与通过判定（#498：阈值是调参基线，健康运行也会超过），`inspect_model` 与 `render_model_views` 次数作为软指标记录。`3d-multistep-rig-checkpoint` 还驱动预算计划卡、服务端检查点与 `revise_processing_plan`，报告记录 `rework_count`（失败任务加修订次数）、`revision_count`、检查点状态计数与用例成功率；这些质量指标只用于 base/分支对比展示，不进入自动回退门槛。零成本、离线、只用回环地址，结果稳定，可用于门禁（当前未接入 CI，需要时把 `agent-quality` 加进 `--only` 列表）。报告写 `agent-quality-trajectory.json` 与 `.md`；`main` 的基线放在 `scripts/smoke/agent-quality/baselines/`。#251 的请求集版本 3 对比记录包括 `agent-quality-trajectory-<分支 revision>`、`agent-quality-base-2829d2d6.*`（在临时 worktree 只复制请求集与评测 harness 后运行，因此报告标 dirty）以及 `agent-quality-compare-2829d2d6-<分支 revision>.*`；汇总指标里的工具/付费调用增长来自新增用例的检查点修正步骤，逐用例预算仍通过。

### 产出评测（真实上游，按次同意）

```
BASE=... ADMIN_USERNAME=... ADMIN_PASSWORD=... ART_AI_LIVE_ACK=1 \
  python3 scripts/smoke/eval-agent-output.py --live --report <目录>
```

只发送请求集的用户话术，由真实 Agent 自行决定轨迹；脚本自动回答用途卡、按 Schema 路由批准卡片（auto 批准方式下通常没有逐步卡），记录打断、工具调用、付费调用与任务。产出收集为 `artifacts/`：3D 模型用 `scripts/smoke/inspect_model_eval.go`（复用 `internal/modelinspect`，与 Agent 的 `inspect_model` 同一套事实）计算面数、拓扑、水密、UV、部件与用途预算判定；2D 记录尺寸、格式、背景/主体覆盖；`render_model_views` 的视图保存为评分图。报告写 `agent-quality-output.json` 与 `.md`，同时生成 `scoring-packet.json` 与 `scoring-sheet.md`：量表、提示词、参考图与渲染图都在里面，由人工或评审模型评分后写入 `scores.json`，再用 `--scores scores.json` 合并。评分文件形如 `{"<用例 id>": {"reference_fidelity": {"score": 4, "reason": "…"}, "craft": {"score": 3, "reason": "…"}}}`；维度取 `rubric.json` 的 `per_request_axes`。脚本不内置评审模型、调用次数或费用，也不自动重试真实上游。3D 产物按 GLB、glTF 的顺序选取（#444：Meshy 先交付 3MF，旧规则会把它当 GLB 检查）；没有 glTF 时检查结果写明交付的格式，不交给 GLB 解析器。追问与卡片回答在本会话回合结束后再发，会话忙的 409 有界重试，等待与重试次数写进各用例的 `sends`。报告头部固定列出 `upstream_shape`、可比指标与不可比指标（#498：生成侧是 fake 时产物指标不可比，占位产物还会反过来影响 chat 指标），并给出基线的可用性——`baselines/` 下没有同 mode 且请求集版本相同的报告时直接写「无法与基线对比」，不给出可比较的假象。该 runner 只发用户话术、不发 `steps` 里的 feedback，所以 `preference:*` 五项按 `skipped` 记录；付费/工具/打断预算的数值同样只记录，都不参与通过判定。任何异常退出都会留下错误行与非零退出码（中断 130，其余 1），跑到一半崩溃也会先写出报告再退出。

### 两次运行对比

```
python3 scripts/smoke/eval-agent-compare.py <基线.json> <候选.json> [--tolerance N] [--json 结果.json] [--new-request-set]
```

逐用例比较检查状态与汇总指标：检查 pass→fail、用例缺失、工具/付费/打断次数增加是回退；自检次数增加是改善。有回退时退出码 1，可直接用作门禁；`--no-fail` 只报告。`--exclude-check-prefix <前缀>`（可重复）把指定检查排除出回退判定；基线里 detail 含 `bad GLB magic` 的检查（#444 之前的误判）自动列为「不可比」，单独成表。`--new-request-set` 是请求集有意变更时的显式报告模式（#240）：请求集版本/内容差异不再算不可比，新增用例记为新增，汇总指标只比较两边共有的用例；`2d-series-project` 与 `2d-3d-input-concept` 的加入用该模式对比旧基线 `baselines/agent-quality-trajectory-77fc6878.json` 与新基线，退出码仍按共有用例的结果决定。

### 交互形态指标（#407）

报告里的 `metrics` 另记三项，用来比较提示词改写前后 Agent 的「手感」：`agent_rounds`（带工具调用的助手消息数，即 Agent 轮数）、`refusals`（被服务端拒绝的工具调用，按拒绝码分组：公开 API 的工具调用带拒绝码时读 API，否则给 `eval-agent-output.py` 传 `--refusals-log <本地结构化日志>`，按会话与时间窗统计「agent tool call refused」行）、`text_questions`（以问句结束且没有工具调用的助手回复，启发式）。比较时轮数与拒绝次数增加算回退，文本反问只记录。fake 栈的 `--only agent-quality` 用脚本化 LLM，改提示词前后这三项相同，只能证明评测链路与对话路径没坏；提示词是否让真实模型少反问、少被拒，要在真实对话形态下按同一名单前后各跑一次（需用户当次同意）。

## Fake 上游整栈自检（#96）

`python3 scripts/smoke/smoke-fake-stack.py`（可加 `--only project` 单独跑 #240 的项目资产继承场景，`--only matting-faults` 单独跑 #301 的抠图文件级故障场景（见下节「Fake 故障冒烟」），`--only image-purpose` 单独跑 #239 的 2D 用途场景（#543 起 2D fake 按请求尺寸出图，该场景里 Gemini 3:4 的 4K 图为 3584x4800，#282 的用途清单步骤断言 `short_edge` 通过；本机未跑该栈，待 `dev-test` fake 形态验证），`--only visual-anchors` 单独跑 #250 的一致性视觉锚场景，`--only vision-budget` 单独跑 #247 的视觉预算场景，`--only offering-knowledge` 单独跑 #249 的模型自述场景，或 `--only work-ledger` 单独跑 #248 的会话生产账本场景，或 `--only self-check` 单独跑 #246 的 3D 自检契约场景，或 `--only plan-checkpoint` 单独跑 #251 的制作计划检查点场景，或 `--only preferences` 单独跑 #241 的会话偏好场景，或 `--only concept-candidates,reroll` 单独跑 #242 的开放概念方向与「再来一个」场景，或 `--only mcp` 单独跑 #332 的对外 MCP 监听场景（访问 Key、上传、图生 3D、轮询、12 个产物的 SHA-256、鉴权与签名下载、`inspect_model`、`inspection` 为 null、remesh；监听只绑回环端口 19413，可用 `FAKE_STACK_MCP_PORT` 改）；`--preferences off` 用同一 revision 关闭该功能，供 #238 改动前后对比）：先启动 `cmd/fake-upstream`，再以 live 模式启动本机隔离栈（Tripo、Meshy、Hyper3D、LLM、OpenAI/Gemini/Jimeng 图像与抠图的 base URL 指向 fake），核对三家余额面板、Tripo 生成 → rig-check → rigging → animation、segment → complete、Meshy 文生 3D → remesh → rigging → animation / retexture / convert、Hyper3D 文生 3D（交付的 PBR 包随请求变化：展示档 6144² 的 base color，游戏角色 8000 个四边面得 17,440 个三角面与 2048² 贴图，#303）→ Bang → texture（链路里的 Rodin 模型选 `texture_mode=high`，展示档的 6144² 超过 Texture 上传的 4096 上限）、三家图像的文生图与图生图、抠图、Tripo 的图生 3D / splat / multiview 与 edit / 两种 Smart Segmentation / decimate、Agent 对话 → 工具调用 → 批准 → 任务 → 回复，以及各故障场景的 Task 终态与错误 class。零成本、离线，只用回环地址，不读取真实 Key，**不属于**发布验收（L0/L1/L2 仍按上文），也不替代真实付费验收。说明见 `dev_test.md`「Fake 上游自检」。`python3 scripts/smoke/smoke-fake-stack.py --only project` 覆盖 #240：建项目并保存生产约定，项目内会话生成角色 A，把完成的 Task 图片提升为标准形象并把同一引用写成项目风格指南参考图；同项目新会话的空简报继承默认用途、会话 wire 的 `styleLockSource` 为 `project`，第二张图（图生图）的冻结输入同时含本人上传图与服务端复制的项目风格图，`refine_image` 结果的 `style_sources` 点名「角色 A」（fake 上游的固定回复不带引用行；mock Agent 把 `style_sources` 转述为「引用项目资产」的文案由 `internal/agents/mock/project_test.go` 覆盖）；删除项目资产返回 204 后原 Task 产物仍能按原 SHA-256 下载。跨用户访问项目/资产与 @projectN 句柄解析在 fake 栈不可观测（开发模拟登录只有一个 mock 用户），由 `internal/transport/httpapi/projects_test.go`、`internal/chat` 与 `internal/chat/generationtool` 的 Go 测试覆盖。`python3 scripts/smoke/smoke-fake-stack.py --only image-purpose` 覆盖 #239：新 2D 请求出 2D 用途卡且选项等于公开的 `image2d_usage.profiles`，用途写入简报，`refine_image` 按用途推荐模型并把画幅覆盖冻结进输入、结果带 `model_choice`，未写 `image_use`（空简报）时按 `default_profile` 的推荐模型与画幅覆盖执行，显式 `model2d` 锁冲突不提交、无模型时照锁定模型执行，单主体纯色背景图通过 `inspect_image` 的 `3d_input` 清单。立绘与默认（概念设定）用途的首推模型是 Gemini 3 Pro Image（4K 档，#282），场景因此断言冻结输入的 `image_size`；并用 `inspect_image` 的 delivery 清单判定 fake 的 64×64 图低于立绘 1536 的最小短边：`short_edge` 失败、hint 点名可达到的模型、失败项留在会话账本（该步骤随 #282 加入，尚未在 fake 栈上运行过）。`python3 scripts/smoke/smoke-fake-stack.py --only image-self-check` 覆盖 #245：`inspect_image` 对合格、带背景、多主体、贴边裁切四类样例的判定，以及图生 3D 闸门对不合格输入图的拒绝与 `input_override_reason` 放行。`python3 scripts/smoke/smoke-fake-stack.py --only mask-selection` 覆盖 #244（独立私有 Catalog 把 2D 编辑列入人工批准能力，端口可用 `FAKE_STACK_FAKE_PORT=19580 FAKE_STACK_PORT=19581`）：先以 64×64 JPEG 画板快照配 PNG 蒙版发送画布来源轮次，断言返回 400 的格式不一致文案、会话无消息且不建 Task（#583）；再上传不透明源图与透明矩形蒙版并发送画布来源轮次，批准卡的 `inputs` 含 `parameter=mask` 的蒙版资产与“只改选区”说明，冻结输入把蒙版写进 Offering 的 `mask` 参数，fake `/images/edits` 只改蒙版内像素、蒙版外与源图逐字节相同；再选无蒙版参数的 Gemini 模型发送同一蒙版，断言工具结果拒绝且不建 Task；最后以上一轮生成结果作为 `@selected` 目标调用 `create_selection_mask`（region 模式）生成蒙版（覆盖 TaskID+AssetID 的 Task 产物读取路径）、断言它出现在回复图片里，并把它作为 `refine_image` 的 mask 完成一次成功生成。跨用户拒绝在 fake 栈不可观测（开发模拟登录只有一个 mock 用户，见 `check_delivery`），由 `internal/chat` 与 `internal/transport/httpapi` 的 Go 测试覆盖。`python3 scripts/smoke/smoke-fake-stack.py --only offering-knowledge` 覆盖 #249：公开 Offering 自述（只含三项、中文默认与英文译文）、禁用 Offering 后的公开/Agent 投影变化、`list_offerings` 的知识与可用模型过滤、生成工具描述里按所选模型的写作指导与 no-text 说明、用途覆盖数据、超限提示词在提交前拒绝且不建 Task，以及引用模板改写后的 `final_prompt`/`prompt_note` 与数据库里冻结 PreparedInput 的实际提交输入一致。`python3 scripts/smoke/smoke-fake-stack.py --only work-ledger` 覆盖 #248：第一回合提交超过 120 字的提示词，经 5 个普通回合后手动压缩上下文，再由 `get_production_step` 从检查点回执取回完整的原始/最终提示词与参数来源；下一回合只把“black cloak”改成“red cloak”并比对最终冻结提示词只有该片段变化；风格锁定原文与参考图句柄在压缩后仍逐字出现在系统提示；`inspect_model` 的面数遗留问题与修复提示在下一回合的系统提示中可见（断言 `get_production_step` 回执里的 passed/unknown/failed 与账本行逐项一致、未知项单列不并入通过，且失败项名称与步骤句柄、Task ID 在同一行）；在用户消息里选中第一张结果、再把会话 pick 换成第二张后，下一回合系统提示出现“已被后续选择取代”的否决清单（由现有选择记录投影，不依赖 #241 的显式否决写入）；全量扫描服务端日志，断言提示词内容与唯一标记不落日志。该场景不进 `scripts/verify-local.py` 的门禁子集，需要时用 `--only work-ledger --work <目录> --keep` 单独运行。 `python3 scripts/smoke/smoke-fake-stack.py --only self-check` 覆盖 #246：图生 3D 结果调用 `compare_model_reference` 时得到参考图与模型渲染的并排图和确定性指标，服务端按真实 inspect/reference 调用写入自检记录，Agent 用 `record_self_check` 写通过/遗留结论，卡片数据（GET self-checks）与系统提示中的三态和计数一致；未检查的新结果保持未自检且提示“不得说成已检查”；手动压缩上下文后记录仍可读；轮次与每轮渲染上限按 Schema 文案拒绝而结论仍可写；两笔付费修正的计数从 1/1 到 2/1 并注明已到上限；后台完成的结果在下一回合列为待自检，并在该回合被 inspect、对照参考图并写入通过记录（写完后不再列待自检）。该场景使用独立的 `self-check` 栈与调小上限的私有 Catalog，不进 `scripts/verify-local.py` 的门禁子集，需要时用 `--only self-check --work <目录> --keep` 单独运行。 `python3 scripts/smoke/smoke-fake-stack.py --only visual-anchors` 覆盖 #250：风格锁的说明与参考图数进入系统提示且原文只出现一次（fake 专用 `[[fake:system-contains TEXT]]` 由模型回复 `present/absent` 佐证）、Agent 用会话句柄提议身份锚的确认卡（接受原样写入、拒绝不写、未经确认卡直接写入被服务端拒绝、用户风格锁不变）、下一回合 `refine_image` 无句柄时冻结输入含锚点 asset 与身份用途、Meshy 7.1 单图上限下锚点保留且普通参考被丢并在工具结果 `reference_note` 里点名句柄，以及 Meshy retexture 在参考槽位空闲时把风格锁参考图写为 `style_asset` 且不注入风格文本。该场景使用 fake 专用系统提示约定，不是真实上游契约。`python3 scripts/smoke/smoke-fake-stack.py --only preferences` 覆盖 #241：一条消息里四个 `refine_image` 标记出四张候选，选用第一张、以 `reasonPreset=style` + 自由文本否决第二张，GET 断言 selected 带 alternatives、rejected 带原因；下一轮用 fake-only `{"prompt_from_preferences":true}` 让 fake 读取系统提示里的偏好条目生成 prompt，断言冻结输入同时含被选方向与被否方向+原因；手动压缩上下文后再出一轮仍含；PUT 编辑后旧文本消失、新文本出现；DELETE 后不再注入；直接向数据库写入第二 owner 的会话与偏好行，GET/POST/PUT/DELETE 全部 404 且不回显内容。`--preferences off` 把私有 Catalog 的 `maximum_entries` 置 0，同一场景断言列表为空、写入 400，供 #238 的 off/on 对比使用；两份轨迹报告与对比 markdown 已随基线提交在 `scripts/smoke/agent-quality/baselines/`。该场景使用独立 `preferences` 栈与 `keep_recent_rounds=1` 的私有 Catalog，不改变默认 Catalog。`python3 scripts/smoke/smoke-fake-stack.py --only vision-budget` 覆盖 #247：fake 的 chat 控制面（`GET/DELETE /fake/control/chat-requests`）记录每个请求的图片 detail、是否 data URI、字节数与紧邻标签；一次 `set_params{n:6}` + `refine_image` 的回合断言同回合参考图仍以 high detail 留下、六个候选以一张带句柄的联系表回来，并从 `/context` 读到视觉 token 估算、每请求 token 上限（12000）与图片名额（10，张数）三个独立成员；随后 `inspect_image` 的 `region` + `show_picture` 断言裁剪图以 crop detail 进入下一请求；一次 3D 任务 + `render_model_views` 断言自检渲染图以 high detail 与结果缩略图各自占名额。fake 的 `n>1` 多候选与请求记录都是 constructed-from-docs 的测试夹具，不是真实上游契约。`python3 scripts/smoke/smoke-fake-stack.py --only plan-checkpoint` 覆盖 #251：预算模式下批准含 Meshy 7.1 文生 3D、remesh、rigging 与 resize 预案的制作计划（计划卡展示验收标准与拆分的最坏估算），生成后在 remesh 继承的原点检查点上由服务端评估并停下（下一步不提交），Agent 用 `revise_processing_plan` 插入命中的 resize 预案，resize 与 rigging 在已批准预算内完成，全程无第二张批准卡；断言总估算不超过批准的 100 历史积分。全部走 fake 上游、fake LLM 与回环地址。`python3 scripts/smoke/smoke-fake-stack.py --only three-view` 覆盖 #243：预算制下批准含三视图与多视图 3D 步骤的计划后，Agent 连续完成正面、左侧、背面（可选右侧不自动开始），左/背面的冻结输入以正面输出为源，过程无待批准卡，最后用 `three_view_group` 提交 Tripo P2 多视图 3D 成功；严格模式下只启动正面，正面完成后左侧与背面仍不自动开始；人在三视图卡片上点左侧后才启动，且左侧冻结输入包含正面输出。图生图按现行批准策略自动批准，所以严格模式不出现逐视角对话批准卡，逐张卡片的点击就是人工闸门。#663 的自动臂：会话切到 `auto` 后，一次 `create_three_view` 调用在同一回合里出完正面、左侧、背面（组 3/3、右侧不开始），带 `include_right:true` 的四视图请求再出右侧（组 4/4）；断言全程没有批准卡、会话只有一个回合且回合自行结束、检查点里只有那一次调用（参数含或不含 `include_right`），后续视角的冻结输入以正面输出为源。全部使用 fake 图像上游与确定性 64×64 单主体假图，不调用真实上游。 `python3 scripts/smoke/smoke-fake-stack.py --only four-view` 覆盖 #416：3D 工作区锁在单图 Meshy 7.1、批准方式 `budget` 时，批准含四个 `image-to-image` 与一个 Meshy 7.1 多视图 `image-to-3d` 步骤的计划，Agent 出完 front/left/back 后由人启动可选的 right，四视图全完成后第 5 步以 `create_model_3d_from_views` 提交（工具调用不带 `model`，按锁的同族形状解析，不撞锁定、不选计划外的 Tripo）；断言计划卡上多视图步骤的预估（40 meshy.credit，含纹理、8K、2K Ultra）等于 Task 冻结的 `native_quote`、回合自行结束、全程没有计划外的逐步批准卡，并读到检查点里那次调用的参数确实没有 `model`。全部使用 fake 上游与 fake LLM，零真实上游。`python3 scripts/smoke/smoke-fake-stack.py --only concept-candidates,reroll` 覆盖 #242：开放 2D 请求在同一次回复里画 3 张概念草图并开出含 3 个选项与缩略图的方向卡，答卡后 `refine_image` 以所选草图为参考继续；预算计划的一个步骤声明 2 个候选，计划卡显示 `candidate_group`/`candidate_index` 展开，批准后同一次回复并行提交两个 Tripo 3D 调用且各自冻结不同 `model_seed`；「再来一个」为已成功任务创建新任务、同 `Idempotency-Key` 复用同一任务、冻结种子变化，手动模式返回 202 `awaiting_decision` 且不建任务。全部使用 fake 图像/3D 上游、fake LLM 与回环地址。`python3 scripts/smoke/replay-recovery-on-fake.py` 在同一个 fake 上重放受控验收的 chain、lost-response、formats（缺格式补齐）与 image（图片本地 wire 故障）（下节 Recovery Fixture 的同一套 runner 与种子工具），不依赖真实 Meshy 余额。`python3 scripts/smoke/smoke-fake-stack.py --only mcp` 覆盖 #332 的对外 MCP 监听：无 Key 的 `tools/list` 回 401，用本地启动管理员（不是 mock 身份，监听会核对 Key 的持有人）建 `generate` Key，随后只带 Bearer Key 上传 512×512 PNG、图生 3D 计 1 步、同键重放不重复计步、同键换资产按 `idempotency_conflict` 拒绝、轮询到 SUCCEEDED、12 个产物（6 种格式、OBJ 的 MTL、4 张 PBR 贴图与缩略图）按 SHA-256 下载、未知产物 404、未签名地址不带 Key 回 401 而签名地址不带 Key 也能取且篡改 token 末位回 404、`inspect_model` 报三角面与检查项、MCP-only 任务的 `inspection` 为 null、remesh 计第 2 步并交付 Offering 的全部必需槽位；监听只绑回环端口 19413（`FAKE_STACK_MCP_PORT`），全程零真实上游。

`python3 scripts/smoke/smoke-fake-stack.py --only asset-list-run` 覆盖 #328 的清单自动推进：私有 Catalog 打开 `application.asset_list.execution`（Catalog 允许的最小值：poll 500ms、在场心跳 5s/宽限 30s、并行 1 项、每项 2 轮），续轮文案带 fake 的 `[[fake:script:...]]`，因此确认后不再发送任何用户消息，runner 自动为每项开轮、调用 fake LLM、由 fake LLM 依次调用 `create_model_3d`（Tripo P2）和 `submit_asset_list_item{done}`。进度栈断言确认后 run 为 running 且两项 todo、两项在没有人工消息的情况下到 review（source 会话只有提案那一条 user 消息，项会话的 user 消息全是 runner 的续轮文案）、每项恰好一个 SUCCEEDED 的 3D Task；心跳停止超过宽限后 run 变 paused(presence) 且不新增 Task（把最后心跳回拨到宽限外，避免真等 30 秒），心跳恢复后回到 running；停掉栈再用同一数据库重启，断言 turn 行与 Task 不变（重启对账不重复开轮/提交）。在场栈在确认前不写心跳，断言 runner 在第一次开轮前就 paused(presence)、两项仍 todo 且没有项会话；写入心跳后 run 自动 running 并完成两项。失败栈的 script 直接报 `failed`，断言两项各自 failed（reason 为上报文本）、run completed、没有 Task，且第二项确实被开轮，证明一项失败不阻断下一项。步数上限栈把账户 `step-policy` 的 list 设为 cap=1（真实浏览器 `PUT /api/v1/account/step-policy`）：第一项（3D）第一轮建 Task 成功，第二轮 `create_model_3d` 被步数闸门拒绝，该项 working 且 round=2、block=step_cap，同时释放并行名额；调度器随后认领第二项（2D，描述里带自己的 fake 脚本），它不计步并申报到 review；此时没有可自动推进的项，run 才变 paused(step_cap)，等待后两项都不再新增 Task；再给第一项追加 1 步后 block 清除、run 恢复，调度器从新一轮继续出第二个 3D Task，第二项仍是 review 且不受影响。该分支基线上在场只有仓储和迁移、没有 HTTP 心跳路由，场景按路由 present/leave 写入的同一 owner/project/tab 行直接写栈的 SQLite，runner 仍走真实 PresenceStore；四个栈都只用回环地址与 fake 上游。`python3 scripts/smoke/smoke-fake-stack.py --only asset-list` 覆盖 #327 的制作清单：聊天里的 `propose_asset_list` 工具调用创建草稿，工具结果与会话/项目清单摘要给出 `list_id`、`item_count`、计划步数和非空 `origin_message_id`；详情保留种类、路线、可用性快照与计划步数（3D 项 `planned_model3d_step_count>0`）；PUT 整体替换按 `expected_version` 递增版本，旧版本 409；确认幂等且项目列表显示 confirmed；第二份草稿确认时 409 `asset_list_active_conflict`（带 `active_list_id`/`active_list_title`），`decision=replace` 后旧 confirmed 变 superseded；拒绝变 rejected；1 秒草稿 TTL 的独立栈读到 `effective_status=expired` 且决策 409 `asset_list_expired`；禁用全部 rigging Offering 的独立栈里路线为 `available=false` 且 reason 非空，但计划步数仍按计划值计入；只含 `kind:"image"` 的清单 `planned_model3d_step_count=0`；bootstrap 管理员读 mock 用户的项目清单与单份清单均为 404。三个私有栈都只用回环地址与 fake 上游。

`python3 scripts/smoke/smoke-fake-stack.py --only asset-list-review` 覆盖 #329 的清单验收看板：私有 Catalog 打开 `asset_list.execution`，并同时替换续轮与打回文案里的 fake 脚本。两项清单在没有人工消息的情况下到 review，看板给出每项的验收快照（`review` 里的产物 asset id/大小/SHA-256、3D 逐条判定、步数）与空的 `latest_review`；清单版本 CAS 过期的通过请求返回 409 `conflict`；通过一项后该清单项变 accepted、登记 `passet_` 项目资产（项目资产列表可见），打回另一项（原因 `style` 与说明）后该项变 returned、round 2，同一子会话收到带原因与说明的重做文案并重新到 review（第二项 SUCCEEDED Task、快照 round 2）；整批 ZIP 在没有任何已通过项时 409 `asset_list_archive_empty`，只通过一项时只打包该项、打回中/未通过项不进包，两项都通过后断言 `manifest.json` 的项名/位置/轮次/目录（`01-`、`02-`）与根 `README.txt`，逐个条目的字节、大小与 SHA-256 与该快照一致，并与任务 API 返回的同 asset 字节一致；`item_ids` 可分批下载；bootstrap 管理员对同清单的 board、archive 与 review 决定均为 404。零真实上游请求由整栈的 `zero real upstream requests` 检查统一断言。

## Fake 故障冒烟（#301）

dev-test 的 fake 形态里，浏览器、冒烟脚本和 Agent 只接触 Plinth，Plinth 自己的 Adapter 再调 fake。Adapter 的请求没有 `X-Fake-Scenario`、`X-Fake-File-Faults` 头，密钥固定为 `fake-test-key`（不选任何场景），上传文件名还被改写成 `input_<序号>.<扩展名>`，所以头和密钥这两种选场景方式在整栈上够不到抠图 fake 的失败路径：#284 的「部分图片没抠成」提示与 #299 的显存不足失败，此前只有单测和 fake 测试覆盖。

**选场景的方式：图片自带的标记。** 唯一原样到达 fake 的是上传图片的字节（输入按上传时的样子原样保存、原样交给 Adapter），所以抠图 fake 也从文件头里读该文件的故障：

```
fake-file-faults:<故障>[,<故障>...];
```

故障词与 `X-Fake-File-Faults` 的相同：`rejected`（服务拒收，创建响应里就是 REJECTED）、`failed`（批次结束时该文件失败）、`gpu_oom`（失败，任务文本是服务的显存不足原话，图片尺寸取自图片自己）、`renamed`（服务用自己的名字回显该文件）。列表以 `;` 结束，写在文件头 1 MiB 内：PNG 放在 `tEXt` 块（keyword `Comment`，CRC 有效，图片照常解码）。fake 不区分格式，只在头部字节里找这段文本；脚本与 Go 辅助函数（`mattingapiv1.MarkPNG`）只会写 PNG。文件在批次里排第几都得到自己的故障，并与请求头给该位置的故障相加（`rejected` 仍优先）；请求级场景（限流、超时等）仍用头或密钥，本地 `smoke-fake-stack.py` 的 `faults` 就是这样。

取舍：

- 不做控制端点：dev-test 里 fake 只在 Compose 项目网络内监听、不发布主机端口，浏览器与冒烟脚本够不到它；经 Plinth 转发则要给生产 Adapter 加转发头或测试分支，不允许。
- 不做「下 N 次请求」的全局计数：并发的批次、重试与轮询会互相消耗它，测试之间还要清理。标记写在输入里，没有共享状态：并发批次互不影响（Go 测试在 `-race` 下同时跑 24 个批次），不带标记的图片永远不受影响，也没有要清理的东西。
- 代价：只能选文件级故障；依赖 Plinth 原样保存并交出上传字节。哪天字节被改写，冒烟会失败而不是静默通过，`internal/app/matting_fault_marker_301_test.go` 用真实的输入存储与应用注册的 Adapter 守着这一点。

**脚本。** 零费用（背景去除没有价格），只对 fake 形态运行，其他形态退出码 2，没有 `--live`：

```
python3 scripts/release-env.py smoke dev-test -- python3 scripts/smoke/smoke-fake-faults.py
```

脚本用管理员测试账号登录，建自己的会话，上传带标记的图片，经公开 Task API（prepare → grant → 提交）提交四个背景去除批次，等它们一起跑完，再逐项断言公开投影。文案不写死，取自公开 Schema（`GET /api/v1/schema` 的 `content.messages`）：

1. #284 `[正常, failed, 正常, rejected]`：Task SUCCEEDED，交付 2 张（字节与 SHA-256 核对），`notices` 恰好一条 `partial_inputs`，`inputs` 为 `[2, 4]`，`detail` 等于 `task.notice.partial_inputs_named` 套入 `task.notice.input` 与 `task.notice.input_separator` 的整句；会话任务列表里的同一任务带同样的 `notices`。
2. #299 真实运行的批次 `[2048×2048 gpu_oom, 16×16]`：SUCCEEDED，交付 1 张，`inputs` 为 `[1]`。
3. #299 单张 2048×2048 `gpu_oom`：Task FAILED，`failure.class` 为 `upstream_unavailable`、`retryable` 为 `false`、`detail` 等于 `failure.upstream_unavailable`（各 Provider 共用的一句），无交付、无 `notices`，服务的原话（显存、`BIREFNET_PROCESS_RES`、`SDMATTE_PROCESS_RES`、图片尺寸）不出现在公开投影里（#299：失败文案按分类取，服务原话没有公开通道）。
4. 对照：不带标记的两张图 SUCCEEDED，交付 2 张，没有 `notices`。

本地处理（#538）不接任何上游：`python3 scripts/smoke/smoke-fake-stack.py --only model-convert,model-parts` 用脚本自己生成的 GLB 夹具，经公开 API 跑 `local/static-glb/to-gltf` → `local/static-gltf/to-glb`（往返后三角面、节点名、网格数不变）、`local/model-parts/split`（按节点与按连通块，每个部件各一个 GLB，清单点名每个部件；拆分与合并只读同会话的 Task 输出，夹具先经转换变成 Task 输出）与 `local/model-parts/merge`（三角面为来源之和），并断言非 GLB 与单模型合并在建任务前被拒；两个场景在门禁子集里，登记一致性由 `scripts/tests/test_fake_stack_local_processing.py` 守。真实一轮要补的抠图脚本（`smoke-l5-matting.py` 与只读探针 `matting-probe.py`，含一次 `GET /openapi.json` 取得 `internal/testutil/fakeupstream/matting/api-v1/testdata/openapi.snapshot.json`）尚未编写；快照不存在时对应 Go 测试 skip 并写明 PENDING CAPTURE。

同样的检查是本机整栈冒烟的 `matting-faults` 场景：`python3 scripts/smoke/smoke-fake-stack.py --only matting-faults`（不带 `--only` 的全量运行包含它，单跑含构建缓存命中时本机约十秒；不在 `verify-local.py` 的门禁子集里，fake 与 Adapter 的契约由 `go test` 守）。脚本和场景共用 `fake_faults.py`；复制脚本到别处运行时，`fake_faults.py` 与 `upstream_shape.py` 须与脚本同目录。离线单测：`python3 -m unittest scripts/tests/test_fake_faults.py`（图片、Schema 句子、每条断言的反例、`run()` 的提交顺序与批准、脚本对非 fake 目标的拒绝，以及与 Go 源码里标记和故障词的一致性）。

**手工复现。** `python3 scripts/smoke/fake_faults.py picture failed.png --fault failed` 写一张带标记的 PNG（`--size 2048x2048 --fault gpu_oom` 是 #299 的那张图；`--fault` 可重复），在 fake 形态 dev-test 的背景去除里和普通图片一起上传即可。浏览器只对 Schema 声明的格式（BMP→PNG）重新编码，PNG 与 JPEG 原样上传，标记因此保留；图片若被别的工具重新编码，标记会丢，那一张就按普通图片处理。

**状态：** 本机整栈（真实 Plinth 应用 + fake，回环端口）已验；`dev-test` 上的运行待该次发布后验收。

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
git status --short   # must print nothing: capture refuses a dirty worktree
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
- `studio-approval-menu` must show the public Schema's `default_mode` label; the public
  session approval endpoint must agree with that mode (#415). In a live round the entry
  switches to the strict mode through the same menu before sending, so the real approval
  card for this capability appears; the preflight never switches modes.
- `composer-settings-open` (the gear beside the send button, #405) → the models tab
  `composer-settings-tab-models` → `workspace-model-picker-model` → the unique
  `menuitemradio` containing the public model label selects Meshy; Esc closes the
  settings dialog (`composer-settings-dialog`). The default is
  the recommended available Meshy text-to-3D Offering, else the first available;
  unavailable/explicitly missing choices fail instead of silently changing model.
- `composer-settings-open` → the parameters tab `composer-settings-tab-params` →
  `params-domain[data-domain="model-3d"]` must name the exact
  public Offering in `data-offering`; `params-cost` must match its declared estimate;
  Esc closes the dialog.
- `studio-composer textarea` receives only the fixed non-private synthetic input
  exported as `syntheticPrompt` by this script. Pre-register that **exact** string
  in the reviewed initial policy's `Allowed` map for a capture preflight. Only its
  SHA-256 enters the report. `composer-send` is inspected and, under `--live` only,
  clicked once; Enter is never pressed in the textarea. The draft is cleared before a
  non-live exit.

Before/after reads require zero session Tasks and zero pending approvals. The
network guard permits reads plus four one-shot writes: empty-body mock login,
empty-session creation, its canvas creation and its validated initialization PUT.
Allowances are consumed before forwarding the original request and never re-armed
after failure or an uncertain response. Writes must be fetches from the main UI
frame; canvas writes must come from the workspace. Cross-session/project writes,
generation, approval, Agent sends, uploads, deletion and all other writes remain
blocked in a non-live run. Off-origin requests and WebSockets are also blocked and
counted as failures. Same-origin static/decoder/asset paths and same-origin blob
assets are normal, not Provider leaks. Local API list queries are allowed; this does
**not** waive the recorder's rejection of upstream query-bearing requests. No
request is fulfilled with fake data, no store is seeded, and a non-live run creates
or retries no execution intent. The guard is not a server-side outbound sandbox: do
not attach it to a server with existing running work. Use only the fresh isolated
capture server, or a fresh zero-key mock server for selector verification.

`--live` changes the guard's shape instead of adding allowances: every same-origin
non-GET request is forwarded (`requestDecision` returns `write` for all of them), so
the approval-mode switch, the Agent send and the approval decision are not one-shot
entries, are not counted individually and cannot be refused by the guard.
Cross-session/project writes, uploads and deletion are not blocked either, so the
live round is a real paid write path and belongs only on the fresh isolated capture
server.

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

**`--live` drives one real paid round.** `--live --capture-root ...` requires the
reviewed manifest and excludes `--mock-preflight`. After the same successful page
preflight it switches the session to `manual` through the menu a person uses,
re-reads the session to prove the switch, picks the Meshy model, matches the
declared price label, fills the synthetic prompt, clicks send once, waits for the
approval card and approves it once, then polls the session's Tasks until one is
`SUCCEEDED` (20 minutes; `FAILED`/`CANCELED` fail immediately). It ends with the CSP
and browser-guard counters at zero, `status=live_completed`,
`capture=live_candidate_pending_review`, exit 0. A non-live run still exits 0 with
`status=preflight_pass`, `submitted=false` and `capture=blocked_capture` after
clearing the draft and re-saving the canvas; check both fields. Failures exit 1 with
a fixed `failure_stage` and, for rejected canvas initialization content, a fixed
`canvas_guard_rejection` code. The live round still does not verify the approval
card's exact price, does not wait for or byte-verify the required Meshy assets, and
exports no wire capture, so it does not complete #365:

1. The recorder has bounded private media spooling and synthetic recipe
   substitution, but this browser entry does not export the recorded exchange as a
   candidate corpus; the recorder's bounds are unchanged (a body over 1 MiB is
   written as the value-free `body-over-limit` marker).
2. Registered query keys are accepted without retaining values; unknown keys still
   invalidate capture.
3. `Projection` covers dynamic task IDs and signed asset URLs, but the reviewed
   manifest still has to register every field and host; no raw getter or blanket
   allow exists.
4. The pending card's exact price, the required READY assets and their byte digests
   are unchecked, and multi-turn Agent evidence is still separate acceptance work.

The remaining entry work is the exact pending-card price check, the required READY
asset wait with byte-digest verification, and the capture export. That continuation
must preserve the same consumed intent after any uncertain response; it must never
retry a paid click to repair capture. The main operator coordinates a new authorized
live round serially. This preflight does not complete #365 or claim a Meshy asset
capture.

#### After a capture: review, coverage matrix and privacy scan

Capture only runs from a clean worktree (`TestWireCaptureServer` stops when
`git status --porcelain --untracked-files=all` prints anything), so a sample's
source label always names the code that produced it. A candidate still enters the
repository only after human privacy and provenance review. When committing one
(`*.candidate.json` with its `.meta.json` and `.policy.json`), run both offline
checks and commit the regenerated matrix with it:

```bash
UPDATE_WIRE_COVERAGE=1 go test ./internal/testutil/wirefixture -run Coverage -count=1
go test ./internal/testutil/wirefixture -count=1
```

`testdata/wire-coverage.generated.json` is generated from the Catalog and the
committed samples, never edited by hand; units without a real sample stay
`missing`. `TestCommittedWireCorpusHasNoPrivateData` rescans every committed
candidate, meta, policy, evidence file and the matrix, and rejects policies that
`ValidatePolicy` refuses. The full-stack replays are in
`internal/app/agent_flow_replay_test.go`. See `docs/architecture.md`
"Wire 黄金样本、覆盖矩阵与全栈离线回放" for rules, limits and what is not covered.

#### Recording bounds and the 2026-11-20 window (#446)

The runtime recorder (`UPSTREAM_CAPTURE_DIR`, #233) buffers each JSON, SSE or
multipart body up to a fixed **1 MiB**; it has no setting to raise it today.
A larger body is written as the value-free `body-over-limit` marker, a body the
sanitizer will not hold as `sanitizer-refused`, and one the client stopped
reading as `incomplete-body`. The live window on `dev-test` on 2026-11-20 wrote
**1171 records: 774 complete exchanges and 397 skip markers**
(`body-over-limit`: LLM 253, openai-images 26, gemini-images 9, meshy 13, tripo
1, matting 1; `sanitizer-refused`: LLM 89, GPT 3; `incomplete-body`: 2).

Complete records covered: Meshy OpenAPI v1 `text-to-image`, `image-to-3d`,
`remesh`, `retexture`, `resize`, `rigging`, `animations`, `convert`,
`print/analyze` and asset downloads (glb, png, obj, mtl, fbx, stl, usdz, 3mf);
Meshy OpenAPI v2 `text-to-3d` preview and refine (create, running, succeeded);
Tripo `/v3/files`, `/v3/generation/image-to-multiview`, `/v3/tasks` and artifact
downloads; matting `api/v1/batches/{id}` polls and one PNG; `LLM`
`/v1/chat/completions` (13) and one `GPT` `/v1/responses` 429 (the gateway's
daily model-group quota). Not covered, because the body never reached the
recorder: every chat request over 1 MiB (the Agent prompt with tool schemas),
the image creation requests carrying data-URI pictures (openai-images,
gemini-images) and the Meshy `image-to-3d` creates carrying the uploaded picture.

The selected samples from this window are committed as
`*-2026-11-20.candidate.json` beside the older captures, produced by
`fake-upstream-align draft` from the private records. Prompts, URLs and opaque
IDs are the sanitizer's placeholders; the quota error's `group_name`, the
matting download locator and the prompts were replaced with placeholder values
before committing. The Meshy engine templates from the newest sample of each
key; the Tripo, matting and OpenAI fakes keep their older templates, and a test
holds each to the newest capture's structure.

One difference is recorded rather than copied: the 13 `LLM`
`/v1/chat/completions` answers carry `choices[0].message.reasoning_content` and
`usage.{prompt,completion}_tokens_details` (including
`usage.prompt_tokens_details.text_tokens`) and no `system_fingerprint` or
`prompt_cache_hit_tokens`/`prompt_cache_miss_tokens`, while the fake's committed
chat sample has the latter two and no `text_tokens`. Those are optional,
model-specific fields; the fake keeps its own model's body because the
adapters read none of them.

**Next recording.** To capture the bodies this window missed, raise the per-body
bound first. The sanitizer policy's `MaxBodyBytes` is capped at **8 MiB**
(`8388608`, `internal/wirecapture/project.go`) and is configurable for
`TestWireCaptureServer` through its manifest; the runtime recorder's 1 MiB is
the constant `defaultRuntimeBodyLimit`, so reaching 8 MiB there needs a code
change (a Schema-declared setting) and is not available today. A larger bound
also records more text: review every draft before committing, and never widen
the bound to keep a prompt or a URL verbatim. Request bodies that are
irreducibly large (the Agent prompt, data-URI image uploads) are still recorded
only as markers today.

### Recovery Fixture

此夹具用于缺少自然失败/暂停样本时的有界验收。普通 `cmd/art-ai` 二进制不包含故障控制器；只有 `go test -c ./internal/app` 生成的测试二进制能运行 `TestAcceptanceServer`。`dev-test` 主机上的实际故障进程使用独立 loopback 端口及私有目录，不能使用 `dev-test` 主实例的入口端口，也不能复用共享数据库、资产或 Catalog 的写路径。`dev-test` 主实例的同版正常回归仍按上文 L0/L1/L2 执行。

1. 先只读检查源数据库、来源成功任务、资产 SHA-256/大小及容量。`seed-acceptance-fixture.py` 默认 dry-run；`--apply` 使用 SQLite backup 保存一致性快照，拒绝活动任务、任何待调度 Task、活动 Plan/Chat turn，并逐文件复制最多五个选定成功来源的实际资产（默认总上限 2 GiB）。源/目标目录必须分开，不使用硬链接。失败保留 `SEED_FAILED`，不能启动该副本。数据库属于私有数据，不打入脱敏报告。
2. 在私有根目录内准备 Catalog 副本和前端产物，以完整已提交 SHA 编译测试二进制：`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -ldflags '-X github.com/LeXwDeX/Plinth/internal/app.acceptanceSourceSHA=<完整 SHA>' -o <test-binary> ./internal/app`。清单绑定 owner、源码 SHA、专用地址、前端和 Catalog 路径；计划场景绑定 session/plan/step/capability/Offering 及最多 1–10 次 Provider Start；图片故障与独立 `standalone` 场景各只允许一次 Start。一次性故障与出站预算保存在私有持久标记中，重启不会重置。
3. 测试栈凭据只传入进程环境。启动需 `ART_AI_ACCEPTANCE_ACK=isolated-test-only`、`ART_AI_ACCEPTANCE_ROOT=<私有根>`、`ART_AI_ACCEPTANCE_MANIFEST=<根内清单>`，运行 `-test.run '^TestAcceptanceServer$' -test.timeout 0`。仅测试装配启用固定 owner 的 mock 登录以取得正常签名会话；Grant 决策仍经实际鉴权与批准策略。默认真实 Provider；离线 mock 必须显式声明，报告必须区分，不得冒称真实调用。`ready.json` 保存进程、源码和二进制/前端/Catalog 摘要。
4. `run-controlled-acceptance.py` 管理已登记场景和私有 Catalog A/B 重启；默认只预检，执行须显式 `--execute`。以其 `--help` 为参数合同。`--browser-command 'node <controlled-acceptance-browser.mjs>'` 在恢复阶段使用真实页面按钮；helper 从 `--context` 文件读取阶段，不伪造 HTTP 响应或修改前端 store。设置 `PLAYWRIGHT_MODULE_PATH` 指向隔离 Playwright。没有浏览器 helper 的 API 结果不能替代页面验收。

   all 模式的丢响应绑骨样本引用本轮已成功并通过交付校验的 Remesh 首步 Task，不直接再次绑骨原始高面数模型。`--only lost-response` 必须显式给出 `--source-lost-task <已有 Meshy Remesh Task ID>`。缺少来源、owner/session 不符、非成功 Remesh、仅格式转换（`convert_format_only=true`）、冻结 Offering 不符、冻结输入的面数不在该来源自身 Offering 声明范围内或 GLB 字节校验失败，均在写 manifest 或启动 worker 前拒绝。该参数不会替换格式场景使用的两份原始来源。

   格式补齐场景要求 `--source-format` 指向真实 SUCCEEDED 且 coverage 中确有 `meshy/processing/remesh` 缺失格式步骤的来源。BLEND 已移除（#221），六格式齐全的 Meshy 生成来源没有 remesh 可补齐的格式（唯一缺失的 glTF 由免费本地转换提供，remesh 不提供），旧的 Meshy 12 资产来源不满足该前提；需要显式换用真正缺格式的来源。没有合适来源时脚本在创建任何付费计划前中止，不得新建替代付费计划。

   丢响应场景 ID 和幂等 key 包含来源 Task ID，重复使用同一来源保留原身份。换用另一个已验证来源建立新样本，不重置旧故障 marker、旧预算或旧失败 Task。报告保存 Remesh 来源、冻结面数参数/范围和恢复前后聚合摘要；all 模式另核对并记录本轮成功 Rigging Task 对该 Remesh 的消费，作为已有的真实兼容证据。
5. 故障仅注入依赖边界：中间步骤出站前明确失败、受理后丢响应、准备暂不可用、受理前断开，以及图片 Adapter 的本地 `insufficient_quota` wire 响应。成功步骤仍经真实 Provider 和完整交付检查。记录旧成功步骤保留、Task/Attempt/Grant/PreparedInput/幂等身份、重复/跨用户/旧版本拒绝、出站次数及必需产物两次下载的哈希和大小。Catalog B 必须在已冻结的暂停计划上验证拒绝，恢复 A 后才继续；不改写任何生命周期数据来制造状态。
6. 保留实际浏览器名称、版本与来源、地址、完整 SHA、交互截图和 API 结果。余额样本验证中文解释且不提供重试；纯读取与被拒绝操作核对前后 Task/待批准无意外新增。失败样本、单次故障与正常出站分开记录；出站唯一性不等于第三方结算审计。停止仅限本轮记录的进程 PID，保留样本与报告。

独立候选资格使用 `fault:"standalone"`，不填 `plan_id`，`step_index` 为 0。先通过正常 API 准备输入，再把 owner/session、capability、Offering ID/revision、PreparedInput ID/hash、至少16字符的幂等键和 `max_provider_starts:1` 写入私有清单，然后批准 Grant 并以同一幂等键提交。额度按 owner+幂等键持久保存，改场景名称或 fault 不能重置；结果未知不重新提交。控制器经只读 `TaskAdmissionStore` 核对任务实际批准的 PreparedInput ID，再检查冻结输入，并约束 Start、Observe 和 Tripo 私有准备恢复的 wire 输入，保留 Adapter 的输入资产预检。每个新的公开步骤须有自己未消费的意图和批准，不能复用已消费身份。私有 Catalog 可以仅开启本次候选，默认 Catalog 保持禁用；所有必需资产两次鉴权下载、大小/摘要/格式、匿名401和跨用户404仍必须逐项核验，资格通过后还需主测试栈同版正常 UI/Agent 回归。测试装配用通用包装器包住没有专用包装的 Provider（如 Hyper3D、本地转换），可选的 `ProviderInputValidator` 等端口因此不可见：隔离进程里的格式清单可能把实际会被转换 profile 拒绝的 glTF 显示为“可转换”，且任何不在清单场景里的 Start（包括免费的本地转换）都会被拒绝。格式清单与补齐格式要在正常二进制的严格 session 副本上核对（#187，2026-10-21）。

离线回归：`python3 scripts/smoke/test-acceptance-seed.py`、`go test ./internal/app -run '^TestAcceptance'`。它们检查隔离与恢复合同，不代替真实 Provider、实际字节、发布门禁或读屏验收。每轮结果写 GitHub Issue/PR，本文不作为当前进度台账。
