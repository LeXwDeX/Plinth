# 历史记录

本文收集从 `AGENTS.md`、`dev_ops.md`、`dev_test.md`、`docs/deployment.md` 等规约入口移出的历史背景：规则的来源与变更、环境沿革、编号对照，以及旧运维记录。**这里只用于追溯，不是现行要求**；现行要求以 `AGENTS.md` 及其引用的规约文档为准。记录按原时点阅读，不证明当前配置、Issue 状态或部署版本。

## 1. 编号对照

- 2026-10-26：公开仓库 `LeXwDeX/Plinth` 以一个单根快照提交（源 revision `6ba59481`）发布，不带内部开发历史（#261）。
- 2026-10-27 起：GitHub `LeXwDeX/Plinth` 成为唯一仓库，迁移前的内部仓库与其 runner 停用（存档 #60）。此前的提交信息、文档与脚本注释中的 `#N`、`!N` 指迁移前内部存档的 Issue/MR（文档中写作“内部存档 #N”或“内部存档 MR !N”）。
- 2026-10-30：公开重置。原仓库改名为私有存档 `LeXwDeX/Plinth-archive`，保留 2026-10-27 至 2026-10-30 的 Issue/PR；本仓库重新创建，只推送当时的 `main`，编号从 #1 重新开始。2026-10-30 之前写下的 `#N`（含“GitHub #N”）指该存档仓库，文档中写作“存档 #N”；此后的 `#N` 指本仓库。1.0 剩余验收接续于 #1，`formal-test` 晋级回执接续于 #2。

## 2. 环境沿革

- 2026-07-29：`dev-test` 测试环境完成 Next → Go 的停机全量迁移与严格对账（证据见 §5.2）。该 Compose 项目只是测试主机上的生产同构项目，不代表正式生产已接管。
- 2026-09-02：实测旧 Next 容器已不存在（`docker ps -a` 无此容器）；旧数据根封存不删，候选 Compose 项目的容器保持停止。
- 2026-09-04：发布 BMP 支持及 23 项系统修复，页面版本 `20260904(1)`。
- 2026-10-02 以前：SpecGit 使用全局注册与宿主 hook（SpecGit 2.2.1 说明）；之后改为项目级集成。
- 2026-10-08：运行时与 Schema 真源、三方隔离与 3D 产物契约、Provider 任务流程与 UI 对齐三节从根 `AGENTS.md` 移入 `docs/architecture.md` §11–§13；测试栈大模型接线移入 `dev_ops.md`。
- 2026-10-19：在 `dev-test` 同一主机、同一发布根下建成隔离正式栈 `legacy-formal`（内部存档 #232）：独立 Compose 项目、独立端口，数据在 `<root>/formal/`，空库起步、无 Provider Key，由 `scripts/release-go-formal.sh` 晋级。当时用户曾给出 `legacy-formal` 的有界操作授权，正式栈保持 Provider Key 为空直至用户决定写入；该授权与约束随该栈下线失效。
- 2026-10-20：用户决定环境布局（#270）：`dev-test` 不变，新增独立主机上的 `formal-test`；部署入口改为 `docs/deployment.md`，按环境名部署。`formal-test` 的 `production/runtime.env` 整份复制 `dev-test` 的运行环境、只改中转 Key 与公开地址，也是同日的用户决定。`formal-test` 早期回执在内部存档 #270，之后的晋级回执记在 #2。
- 2026-10-23：`dev-test` 发布备份可用 database 范围、最多保留 2 份（#364；Issue note 100331 覆盖了早期保留 3 份的方案）。
- 2026-10-30：`formal-test` 接入 HTTPS 域名，`APP_ENV` 与 `GO_APP_ENV` 都设为 `production`（#2）。
- 2026-10-30：维护者决定下线 `legacy-formal`（存档 #74）。2026-10-20 改用 `formal-test` 后它的去留一直待决，存档 #60 之后晋级验收来源 `acceptance.legacy_formal_issue` 也未设置。下线只动该栈自己的资源：先对 `<root>/formal/` 做全量备份（数据库、Storage、Web data、日志、报告、运行环境、源码快照与镜像导出，逐件记录大小与 sha256 并验证可列出内容），再停止并移除它的 Compose 项目与网络、删除它独有的镜像和 `<root>/formal/` 目录。该栈没有命名卷。同一发布根里 `dev-test` 的 `production/` 与 `app/`、旧候选 Compose 项目、旧 Next 目录和同机其他业务都未触碰。最终备份保存在发布根之外的独立位置，主机路径与校验值记在存档 #74，不写进仓库。随之删除 `release-go-formal.sh`、`docker-compose.go-formal.yml`、正式栈无 Key 冒烟与离线测试、镜像保留里的 `formal/` pin 保护，环境文件不再支持 `managed_by` 与 `acceptance.legacy_formal_issue`；`release-formal.py` 中仍在用的验收报告与清单校验保留为 `scripts/release-promotion.py`。
- 2026-11-02：`dev-test` 改为每次发布全新数据（`"data": "ephemeral"`，#28），默认不再做发布备份；测试栈模型改用无版本别名 `qwen-omni-flash`、`gpt-astra`（#64）。
- 2026-11-03：`formal-test` 发布备份改为 `{"scope": "full", "keep": 2}`，成功后自动清理，只保留最新 2 份，发布备份不含镜像和日志（#66）；此前 `formal-test` 全量备份但不自动清理。

## 3. 规则来源

`AGENTS.md` 现行要求的来源，供追溯；规则内容以 `AGENTS.md` 为准。

| 规则 | 来源 |
|---|---|
| 测试期付费同意由 Agent 代为批准（仅 `dev-test`） | 2026-09-28 用户直接授权 |
| `dev-test` 操作常设授权（发布、重启、回滚、小额真实模型验收、测试期付费同意、有界镜像清理） | 2026-09-25 用户确认 |
| 功能完成后自动到 `dev-test` 回归；Git 交付常设授权 | 2026-10-02 用户确认；2026-10-27 起 PR 创建与合并同样无需逐次询问 |
| 架构文档同步 | 2026-10-06 用户要求 |
| 产品仅面向 PC，UI 验收用桌面视口 | 2026-10-08 用户确认 |
| 计划与状态只写 GitHub 原生记录 | 2026-10-14 用户要求；2026-10-27 起平台为 GitHub |
| 直接使用 `dev-test` 的 `.env` 配置与 Key | 2026-10-16 用户直接授权 |
| 付费验收是正常交付流程 | 2026-10-18 用户确认 |
| 各栈默认 DEBUG、错误必记录（#233） | 2026-10-19 用户要求 |
| 所有分支与工作树都须初始化 SpecGit（#242） | 2026-10-19 用户要求 |
| 按环境名部署、晋级规则（#270） | 2026-10-20 用户要求与确认 |
| `dev_test.md` §2 race 只在 CI、普通 `go test` 只在本地 | 2026-10-20 用户确认去重 |
| SpecGit 项目语言 `zh` | 2026-10-21 用户要求 |
| 付费验收次数上限（每 revision 每 Provider 10 次） | 2026-10-22 用户要求 |
| 单一仓库与历史编号 | 2026-10-27 用户决定 |
| 不设分支保护 | 2026-10-27 维护者决定 |
| GitHub 记录用中文 | 2026-10-30 用户确认 |
| `dev-test` 每次发布与冒烟都从全新数据开始（#28） | 2026-11-02 用户要求 |
| 测试栈模型用无版本别名（#64） | 2026-11-02 维护者决定 |
| `formal-test` 发布备份全量、保留 2 份、成功后清理（#66） | 2026-11-03 用户要求 |
| 测试链路上游 key 占位 `<TEST_API_KEY>` | 2026-09-27 用户给定 |
| `dev_test.md` §11.3 L3 协议 | 2026-09-28 用户确认 |

## 4. 门禁与主机 CI 的决策背景

- 本地与 CI 曾各跑全部七项门禁，每个 MR 要跑两次 race，CI 还多跑一次被 race 覆盖的普通 `go test`。2026-10-20 去重后改为：race 只在 CI 跑、普通 `go test` 只在本地跑。
- 2026-10-20 流水线 30897/30899：前端各作业运行 7–27 秒，却各排队 18–55 分钟，Go 各作业还要等前端阶段结束；据此把前端几项合为一个作业。
- 本机 `-timeout=30m`：2026-10-19 同一工作站并行多个会话的门禁时，`httpapi`、`sqlite` 等包多次超过 Go 默认的单包 10 分钟上限而误报失败，单独重跑均通过；2026-10 在单台 runner 上 `httpapi` 包 race 约 596 秒。
- 主机 CI 运行中内存保护（#5）：2026-10-30，在共用测试主机上，一次容器上限 12 GiB 的门禁运行期间发生主机级 OOM（load 约 1100），内核杀掉了同机另一个业务栈里没有内存上限的 uvicorn 进程（RSS 约 5.9 GB）。同日容器上限从 12 GiB 降到 10 GiB、预检阈值从 13 GiB 降到 11 GiB，并行改为可选（默认 1 个槽位）。此前 15 GiB 预检阈值在实测中过严：测试主机上另一个无关业务容器常驻约 6 GiB，`MemAvailable` 常态只有约 14 GiB，连续多个 PR 的完整门禁因预检返回 `not_run`。
- 主机 CI 取代了此前临时目录里的手工 stage/launch 流程（#97）。

## 5. 旧运维记录（从 `dev_ops.md` 移入）

以下内容按原文保留，只作追溯。服务器A禁止照旧 Next 记录部署。

### 5.1 文档交付前事实快照

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

### 5.2 `dev-test` 迁移演练证据（2026-07-29）

`<root>` 为 dev-test 发布根。

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

任务列表两阶段读取的依据：迁移数据含约 7MB 的历史 `input_json`；恢复为宽行直接排序会重新耗尽容器 64MB tmpfs。当时 1,148 条历史任务的默认 36 页已全量验收为 HTTP 200。

### 5.3 旧 Next 运维记录（服务器A 禁止照此部署）

#### 服务器A 旧 Next 目录结构与职责（历史说明）

##### 代码目录（唯一）
```
<旧 Next 根>/app/       ← rsync 同步目标，也是 docker compose 操作目录
```
- 所有 `docker compose build / up / down` 必须在此目录执行；
- `.env` 保存在此目录，rsync 时 `--exclude '.env'` 保护，不覆盖；

##### 持久化数据目录（唯一，容器实际挂载的）
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

##### 遗留目录说明（勿删，但也不要使用）
| 路径 | 说明 | 状态 |
|------|------|------|
| `<早期代码目录>/` | 早期代码目录，历史上曾从此启动 compose，现已废弃 | 保留备查，不操作 |
| `<早期代码目录>/docker-data/` | 旧 docker-data，数据极小（68K db），未被任何容器挂载 | 废弃 |
| `<旧 Next 根>/app/docker-data/` | 中间过渡期产物，含 21MB 旧 prod.db（Mar 30），未被当前容器挂载 | 废弃 |
| `<旧 Next 根>/app/storage/` | 空目录，未被容器挂载 | 废弃 |
| `<旧 Next 根>/docker-data/db/` | 旧数据库位置，不是当前测试源真源 | 废弃 |
| `<旧 Next 根>/dev.db` | 早期遗留，未使用 | 废弃 |
| `<早期代码目录>/dev.db` | 早期遗留，未使用 | 废弃 |

##### 为什么之前 docker compose down 无效
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

#### 服务器A、B配置区别（其他都一样）
- 服务器A，WEB-HOOK地址：公网 Webhook 地址（见该服务器运行环境文件）
- 服务器A：MESHY_WEBHOOK_KEY（Header 名 x-webhook-key）应从该服务器对应栈的获准运行环境配置中读取；本历史记录不保存示例值。
- 服务器B，WEB-HOOK地址：公网 Webhook 地址（见该服务器运行环境文件）
- 服务器B：MESHY_WEBHOOK_KEY（Header 名 x-webhook-key）应从该服务器对应栈的获准运行环境配置中读取；本历史记录不保存示例值。
- 服务器C，WEB-HOOK地址：公网 Webhook 地址（见该服务器运行环境文件）
- 服务器C：MESHY_WEBHOOK_KEY（Header 名 x-webhook-key）应从该服务器对应栈的获准运行环境配置中读取；本历史记录不保存示例值。
- 注意：需要外部Nginx和APP内都配置单个文件20MB大小，以达成限制上传文件大小的目的；

#### 部署步骤（以服务器A为例）

##### 架构说明
- app 容器直接暴露 13333 端口到宿主机（docker-compose.yml 中 `ports: "13333:3000"`）；
- 无 Docker 内部 nginx，HTTPS 由服务器外部 Nginx 处理；
- 外部 Nginx 需配置 upstream 指向 `127.0.0.1:13333`（不再走 Docker 内部网络）。

###### 外部 Nginx 配置变更（一次性）
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

##### 1. 同步代码（不覆盖服务器专属配置）
```bash
rsync -avz --delete \
  --exclude 'node_modules' --exclude '.env' --exclude '.env.local' \
  --exclude 'dev.db' --exclude 'docker-data' --exclude 'storage' \
  --exclude '.git' --exclude 'tsconfig.tsbuildinfo' --exclude '.next' \
  ./ <dev-test 的 ssh>:<旧 Next 根>/app/
```

##### 2. 构建镜像
```bash
ssh <dev-test 的 ssh> \
  "cd <旧 Next 根>/app && docker compose build --no-cache app"
```

##### 3. 重启容器
```bash
ssh <dev-test 的 ssh> \
  "cd <旧 Next 根>/app && docker compose up -d"
```
> ⚠️ 如果这是首次从 `<旧 Next 根>/app/` 启动（而非 <早期代码目录>/），
> 请先执行：`cd <早期代码目录> && docker compose down`，再执行上面命令。
> 执行一次后，之后所有操作统一在 `<旧 Next 根>/app/` 下进行即可。

##### 4. 部署验证
```bash
#  健康检查（直连 app 端口）
curl <dev-test 的 base_url>/api/health
#  WebHook（应返回 401）
curl -X POST <dev-test 的 base_url>/<webhook 路径>/
#  页面访问：浏览器打开 dev-test 的 base_url 确认可登录
```

#### 全新服务器初始化（仅首次部署需要）

##### 1. 安装 Docker
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

##### 2. 配置 Docker 镜像加速（拉取基础镜像用）
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

##### 3. 创建目录结构
```bash
mkdir -p <旧 Next 根>/app
mkdir -p <旧 Next 根>/storage/{assets,audit,cache,generated,task-inputs}
mkdir -p <旧 Next 根>/docker-data/{db,cache}
```

##### 4. 创建 .env
```
#  参考服务器A的 .env，修改以下字段：
#  - NEXT_PUBLIC_APP_URL → 新服务器地址
#  - MESHY_WEBHOOK_KEY  → 新服务器对应密钥
#  - 其他字段保持不变
```

#### 注意事项

##### 环境差异
服务器上环境与本机不一样（Docker HostName、运行配置等），
不能沿用本项目的 .env，需以服务器实际情况调整。
目前服务器已调整好，若部署到新服务器则需重新配置。
端口已固化在 docker-compose.yml 中（`13333:3000`），部署时无需 sed 修正。

##### 数据库迁移
docker-entrypoint.sh 会自动执行 prisma migrate deploy。
若遇到迁移冲突（如之前用过 prisma db push），需手动解决：
```bash
  docker compose stop app
  docker compose run --rm app npx prisma migrate resolve --applied <迁移名>
  docker compose up -d
```

##### Docker 镜像加速
国内服务器无法直接访问 Docker Hub，构建前需配置镜像加速：
  /etc/docker/daemon.json → registry-mirrors
构建失败并提示 "i/o timeout" 时，请检查此配置。

##### 每次部署后必须验证
1. 网站可访问、可登录
2. WebHook 通讯正常
3. AI 对话出图可正常创建会话和生成图片
