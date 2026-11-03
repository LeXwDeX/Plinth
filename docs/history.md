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

### 5.4 `README-go.md` 的 dev-test 迁移结果快照（2026-07-29，仅测试环境；从 `README-go.md` 移入）

Go 已接管 `dev-test` 的入口（绑定 `0.0.0.0`，端口见本地环境文件）。当前生产同构测试容器
（`release-env.py show dev-test` 的 `container`）的镜像标签与源码 SHA 以容器 OCI 标签为准；
发布验收必须确认 `org.opencontainers.image.revision` 等于已推送的
`origin/main` 完整 SHA。旧 Next 容器已删除（2026-09-02
实测 `docker ps -a` 无此容器）；候选栈容器仍在、保持停止。
`dev-test` 测试数据的初次全量迁移已经完成，针对该测试栈的普通发布不得再次
运行 ETL。正式生产尚未迁移，不能复用本节的账号、数据、对账数字或批准
清单；正式流程见 `docs/production-migration-runbook.md`。

最终停写快照的迁移结果为：

- 用户 5；Task / Attempt / 逐笔账本各 1,148；积分合计 8,109；
- 输出 Asset 1,543（原迁移产物 1,473 + 历史模型截图 70），输入 Asset
  689；
- Chat 会话/消息 32/193；Canvas 项目/节点 9/11；
- 余额快照 232；旧数据隔离归档 141；
- 38 个旧任务的 40 个必需输出在原 Next 数据中已缺失。用户已批准按
  “Next 缺、Go 也缺”的原则保留身份、参数、Attempt 与账务，并落为
  `FAILED/integrity`，不伪造 Blob 或成功状态。
- 原 Next 曾声明截图的 84 个成功模型任务中，70 个真实 PNG/WebP 已从
  旧缓存补迁移为 `thumbnail` Asset；另外 14 个在旧缓存、历史签名 URL
  与 Provider 任务中均已不存在，按“Next 缺、Go 也缺”保持缺图。

测试 SQLite `quick_check=ok`、外键违规为 0。迁移后的原管理员登录、
5 个前端资源、九类 Capability Provider 发现、1,148 条历史任务与
8,109 积分后台汇总、历史资产 HTTP 下载及 SHA-256、内部 Webhook 和外部
Nginx Webhook 均已验收。未在 live Provider 上创建计费任务；九类创建
行为由候选 Mock 全套验收覆盖，生产只做无扣费可用性验证。

切换后发现迁移任务中的大 `input_json` 会让旧列表 SQL 在
`ORDER BY` 临时 B-tree 中耗尽容器 64MB tmpfs。Repository 已改为先排序和
分页 Task ID、再批量读取完整 Task；生产 `/api/tasks` 默认 36 页已全部
返回 200。5xx 域错误同时补充了不向客户端泄露 Cause 的结构化服务端日志。
首页与任务历史只按 Product Schema 中的 `thumbnail`、`image` 优先级选择
预览，不再把 `texture` 当成模型截图；浏览器已实测首页、任务历史和第 4
页正常显示/加载。

证据位置（2026-07-29 历史证据，`<root>` 为 dev-test 发布根）：

- 最终备份：
  `<root>/backups/final-cutover-20260729-final-r1`
- 切换前 Go 生产目录归档：
  `<root>/production-archives/20260729-final-r1`
- 最终对账：
  `<root>/production/reports/migration-final-approved.txt`
- 迁移就绪证明：
  `<root>/production/migration-ready.txt`
- 切换记录：
  `<root>/production/reports/cutover-record.txt`
- 切换后 Webhook 热修复发布记录：
  `<root>/production/reports/post-cutover-release-webhook-r1.txt`
- 切换后任务列表修复发布记录：
  `<root>/production/reports/post-cutover-release-tasklist-r1.txt`
- 切换后历史模型截图补迁移记录：
  `<root>/production/reports/post-cutover-release-preview-r2.txt`

## 6. 文档改写时移出的历史叙述（#68）

以下为把规范文档改写成现行要求时，从各文档移出的变更过程与决策叙述，仅作追溯；现行要求以对应文档正文为准。

### 6.1 docs/architecture.md §1–13

#### §7「结果的主模型与做不了的前置步骤（#326）」案例（#325 F1）
**案例（#325 F1）。** 过期的 Meshy Rigging 结果 `task_8dd90530…` 在面板里的「当前来源」是 `basic_animations.running_armature_glb_url`：79,600 B、288 个三角面、没有贴图的骨架。该任务 8 个模型文件里排在第一的是这类附带动画文件（非必需，`basic_animations.` 前缀槽位 `minimum: 0`），必需的 `rigged_character_glb_url`（105,271,888 B）排在最后。舞台按「最小序号的 GLB」取、Agent 句柄按 `source_variant_preference`（`glb`…，Meshy 绑骨/动画的变体名都不在其中）退回第一个文件，于是都取到骨架；面板据此自动补加的「先绑骨」实际会拿骨架去绑。换成正确文件后，105 MB 又超过 Rigging `source_asset` 的 60 MiB，这条路走不通。

#### §7「暂停的计划与旧失败卡按同一规则判断（#326 后续）」案例（#326 P）
**案例（#326 P）。** 计划 `4c8af6de…` 在加工面板拒绝授权后停在 `plan_consent_required`，冻结的输入点名的多视图结果 `task_01a1114f…` 的上游作业已失效（`upstream_job_gone=true`），面板仍给出「准备本步骤确认 · 15」，继续后会在内核依赖检查以 `source_expired` 失败；这个暂停计划还让面板的复选框全部不可用。另外，92969c0c 上产生的 `source_expired` 失败卡文字在失败时写定为「生成失败」，最后一轮仍提供「重新生成（约 15 积分）」。

#### §7「上游原任务的年龄与失效（N1，#324）」案例段落
**上游原任务的年龄与失效（N1，dev-test `92969c0c` 验收 #324）。** 那次 Meshy Retexture 引用的多视图结果 `task_01a1114f…` 是 #295 的「重新取回」任务：它创建于 10-20，但交付它的上游作业属于 10-18 创建、入库失败的来源任务，提交时作业已近 3 天 19 小时，超过 Meshy 的 3 天保留，上游回 404；规则按重新取回任务自己的创建时间算成 35 小时，所以没换用已保存文件。多视图图生的能力是 `image-to-3d`，本来就在 Retexture `task_capabilities` 里，不是能力漏判。

#### §7「步骤只冻结第 1 张」后续 E 的原始问题经过（已改为约束句）
选 Meshy 7.1 附两张图时，步骤只冻结第 1 张，但等待批准的工具结果没有说明（reference_note 原来只在任务成功后由 wait 补上），真实 Agent 回复「两张图一起放进了请求」；批准卡只列 @img1，批准后的结果消息只写「Meshy 7.1 生成完成。」。

#### §4「浏览器验收发现」类叙述（#189 灯罩详情单文件下载；详情三处问题；附件文件名缓存 #187 B；选择卡确认无反馈）原文要点已改为约束句，原叙述：灯罩详情只给第一个 STL；creative-lab-* 不在 model_delivery.capabilities 导致格式显示不可用；来源未知只出现在我的资源详情。

### 6.2 docs/architecture.md §14–15

#### §14 开头
同机隔离正式栈 `legacy-formal`（内部存档 #232）已下线，不是可部署环境（见本节末「`legacy-formal` 下线（存档 #74）」）；下面各段中凡提到它的脚本、测试与字段均为历史记录。

#### §14「按环境名部署与正式测试环境（#270）」
新布局下内部环境以名称区分……参数化（#270；#249 起主机与发布根不再有缺省值，见下段）：……源码同步与服务器段命令序列与参数化之前相同；……`retain-art-ai-images.py` ……默认保留五个镜像（`--root` 必填，#249；不读取 `<root>/formal/` 下的 pin，#74）。保留的边界：……`formal-test` 的运行配置按用户规则整份沿用 `dev-test`。

#### §14「环境标识清理（#249）」「公开快照清理（#261）」
旧的 `deploy/*.local.json` 仍可读；`docker-compose.go-production.yml` 的 8 个主机路径改为 `:?` 必填；`RELEASE_COMPOSE_PROJECT` 改为必填；`retain-art-ai-images.py` 以必填 `--project` 与可重复的 `--protect-project` 取代写死的项目清单；迁移工具拒绝宽泛目标的规则改为通用规则；旧内部 CI 配置只用公共 `registry.npmjs.org`，不再接受 CI 变量或环境文件的内网 npm 镜像覆盖（#257）；文档中的内部 forge 链接改为 `#N`/`!N` 引用。验证范围中曾列入 `test-formal-release.py`、`test-formal-server.py`、`test-l1-formal-keyless.py`（均随 #74 删除）；#261 记录「未发布到任何环境，发布脚本改动的线上验收待下次 dev-test 发布」。

#### §14「发布与快照的安全兜底（#334）」
三处收紧，均不改普通发布的服务器流程……（2）凡由它发布的环境（`managed_by` 字段已移除，#74）……自测对象曾含 `legacy-formal`。

#### §14「单一仓库（存档 #60）」
迁移前的内部仓库及其 runner 停用（旧内部 CI 配置已删除）；晋级验收报告改为本仓库 Issue comment：`acceptance` 用 `forge_repo`、`issue`（`formal-test`）与 `legacy_formal_issue`（`release-go-formal.sh`，已随 #74 移除），`read_acceptance_comment`（现在位于 `release-promotion.py`）……；`forge_url` 的主机就是公开仓库，不再作为禁用字面量。

#### §14「`legacy-formal` 下线（存档 #74）」（全段）
运行面：只对它自己的 Compose 项目执行 `down`（不带 `-v`），移除其容器、网络、专用镜像（`050af5e` 构建，没有其他容器或标签引用）和 `<root>/formal/` 目录；该栈没有命名卷，数据都在 `<root>/formal/` 的 bind mount 里。移除前对 `<root>/formal/` 做全量备份（状态目录与运行环境、源码快照、镜像导出、容器 inspect 与 Compose 清单），存放在发布根之外，逐件校验 sha256 并列出归档内容；备份位置与校验值记在 #74，不入库。同一发布根下 `dev-test` 的 `production/`、`app/`、`backups/`、`releases/`、旧候选 Compose 项目、旧 Next 目录以及同机其他业务都没有改动，`dev-test` 的 `protect_projects` 去掉它的项目名。代码面：删除 `scripts/release-go-formal.sh`、`docker-compose.go-formal.yml`、`scripts/smoke/smoke-l1-formal-keyless.py` 及其测试、`scripts/smoke/test-formal-server.py`（正式栈事务与回滚）；`scripts/release-formal.py` 只剩 `formal-test` 晋级共用的验收报告、成功清单与 Issue comment 校验，改名为 `scripts/release-promotion.py`，对应测试移到 CI 门禁里的 `scripts/tests/test_release_promotion.py`（原 `smoke/test-formal-release.py` 的正式栈事务用例随栈删除）；`retain-art-ai-images.py` 去掉 `<root>/formal/reports/image-pins.json` 的读取、写入与按 pin 保护，保留 `--project`/`--protect-project` 的容器引用保护，`test-image-retention.py` 同步；`release-env.py` 不再支持环境字段 `managed_by`（出现即报错并提示删除），环境文件模板去掉 `legacy-formal` 条目与 `acceptance.legacy_formal_issue`。验证：`test_release_env.py`、`test_release_promotion.py`、`test-release-stack.sh`、`test-release-rollback.sh`、`test-image-retention.py`、`test-release-backup-retention.py`，以及下线后 `dev-test` 的 `/api/version`、`/readyz` 与 L0 冒烟；去掉 pin 保护后的镜像保留 `--apply` 路径要到下次 `dev-test` 发布才在真实主机上运行。

#### §14 日志段之前的段落（旧双栈叙述）
两栈日志按 #233 契约运行（见下段）。镜像保留通过共享锁保护正式运行、回滚和待晋级 pins；正式根存在但 pin 缺失、损坏、权限/文件身份不可靠或引用 ID 不在准确仓库清单时停止清理。该 fail-closed 行为已由 `test-image-retention.py` 本地测试，正式主机验收仍以 #232 Issue 回执为准。正式 smoke 只读验证 test 栈拒绝正式登录态 Cookie（也检查以 test 默认 cookie 名重放时的拒绝），并在 formal 登出后用真实旧 Cookie 验证撤销；不登录或写入 test 栈。（pin 与 legacy-formal 正式 smoke 已随 #74 删除；本段整体移出。）

#### §14 其余变更过程措辞
#79「增加布尔输入」；#86「门禁集合新增第 9 项」「改用 yaml 库（新增开发依赖）」；#249「已删除 internal/authentication/sso……」「`/api/auth/sso/*` 不再注册」；#274「前 日志只有『上传文件为空、过大或不可读取』」；#240「补测后补齐」；#134「前端改为显式清理本地存储」；#118「原为 forbidden」；#154「不再因管理员权限跨 owner 列出全部任务」；#259「决定保留到 1.0 之后」；§15「dev-test 镜像内容与发布、回滚行为不变」「未在本轮实施」「`docker-compose.go-formal.yml` 与 `legacy-formal` 的回滚兼容已随 #74 删除」。均改写为现状条款。

### 6.3 docs/architecture.md 其他

- 全文机械去除：日期+来源标注（如「2026-10-13 已验…」「（#N，2026-10-22；…）」）中的日期，以及 `note NNNNN` 评论编号（原文保留 Issue 编号、revision、状态标注）。
- 章节标题去日期：「对话里的选择卡」「前端复查修复」「静默执行与换来源确认」「Agent 提出处理计划」「上游限流排队」「失败任务重试」「Tripo 几何与分件的启用」「第 14 节 dev-test 部署与日志目标（`legacy-formal` 已于 2026-10-30 下线）」等。

- architecture.md 开头「更新：2026-10-08，补充 Issue #134/#135 的 PC Skill 弹窗与可选分类合同。」（更新日志行，已删除）
- architecture.md §Fake 上游服务（#96）：「此前比较表行数，窗口约一秒，机器同时跑重负载时偶发失败，重放脚本为此把 fake 的排队与运行轮询拉长（`-queued-polls 2 -running-polls 8`）」（#139 对账脚本的旧做法）
- architecture.md §Wire 黄金样本（#89）：「Tripo v3 原先没有真实样本的七个端点…端点表改为 `real`。补采推翻了几处按文档构造时的假设」（样本由构造改为实采的过程叙述）
- architecture.md §Meshy 生成模型（#329）后续 E：「`reference_note` 原来只在任务成功后由 `wait` 补上」类问题经过叙述保留于该条问题陈述中
- architecture.md §Creative Lab（#100）：「2026-10-22 曾分两批全部启用」「Catalog revision 日期前缀沿用 #103 起使用的 2026-10-30 日期…所以比实际合并日晚一天」（revision 日期与合并日差异的过程叙述）
- architecture.md §Tripo 官方合同复核：「（的 #185 记录误写为“没有端点页”，已更正）」（#185 旧记录勘误）
- architecture.md §Creative Lab 的 Agent 工具（#189）：「（隔离 Agent 验收发现拒绝文案曾混入构建阶段，构建 model_id 也曾能被当作模板选中，已修复）」
- architecture.md §BLEND/可选格式：「两条 2026-10-05 的历史时点记录已随审查报告移出仓库（内部 Git 历史保留）。」
- architecture.md §Tripo 原样缓存（分部件模型）：「起因是 dev-test 上分部件任务的 converted.fbx 被判 fbx_invalid_structure…重试 8 次后失败」（缺陷经过，保留错误码于现行描述）
- architecture.md §LLM（#253）：「`llm_model` 默认值按用户 2026-10-19 指示改为 llms 网关别名 `deepseek`（替代已下线的 `qwen3.8-flash`…）」（旧默认值，已被 #64 的 `qwen-omni-flash` 取代）；「DeepSeek 例外：用户 2026-10-20 改为…」的决定来源
- architecture.md §会话压缩（#322）：「#322 dev-test 验收 note 98983 发现：原来 `chat.context.maximum_messages`=20 在压缩开启时仍截断每个请求」（缺陷发现经过）
- architecture.md §14 首段：「2026-10-19 在同一主机、同一发布根下曾建同机隔离正式栈 `legacy-formal`（内部存档 #232）：独立端口与 Compose 项目，数据在 `<root>/formal/`，空库起步、无 Provider Key，会话 cookie 名独立（`SESSION_COOKIE_NAME`…默认值兼容测试栈）；其晋级器只接受测试发布 manifest 与 OCI revision 同时匹配、且验收已写入原生 note 的精确 `main` revision」（已下线栈的描述）
- architecture.md §14 环境标识清理（#249）：「（当时）`release-formal.py` 从环境文件 `legacy-formal` 条目读 ssh/root、从 `acceptance.forge_url` 读验收证据前缀…（以上三者已随 #74 删除或改写）」与「当时保留的边界（迁移工具拒绝危险根目录的固定清单、旧内部 CI 配置的内网 npm 镜像、内部 Issue 证据链接与历史记录）已由下段 #261 清理」
- architecture.md §14 公开快照清理（#261）：「（当时）`release-formal.py` 从环境文件 `legacy-formal`、`dev-test` 条目读取正式栈与测试栈项目…（已随 #74 删除）」与「纯历史记录（审查报告、验收回执、旧架构档案、`.scratch/` 运维草稿）删除」
- architecture.md §14 单一仓库：「2026-10-27 之前的 `#N`/`!N` 指内部存档编号」（改写为不含日期的说明）
- architecture.md §14 legacy-formal 下线：「维护者决定废弃同机隔离正式栈。」
- architecture.md §主机 CI（#97）：「2026-10-30 一次 12 GiB 上限的运行期间，共用测试主机发生主机级 OOM、同机另一业务进程被杀」（事故经过，改写为一句理由）
- architecture.md §Fake 上游服务（#96）：「原先只能付费调真实上游，或用 `internal/providers/mock` 绕过 Adapter」（fake 上游出现前的做法）
- architecture.md §画布背景菜单（#277）：「此前只写本机 store，项目加载时又被服务端旧值覆盖，刷新后回到点阵」；§画笔蒙版：「此前纯画笔笔画是每笔一条 `opacity=0.75` 的 `Line`…重叠处逐笔加深、正在画的一笔也叠在栅格上加深」；§画板发送：「此前单张未改动原图会直接发原图」；§画布放置：「（#284：此前只看最右图片…）」「此前 `useCanvasArtboard` 每次渲染返回新对象…（#239 的直接原因）」
- architecture.md §前端：「（#263：此前撤销会把旧画板坐标的快照原样放回新画板…）」「此前切换瞬间会短暂拿旧版本链当新会话的」「此前这两点使需要参考图的 Hyper3D Texture / Bang 无法从面板提交」「此前 Meshy 重拓扑等所有 Offering 都显示“/”」「#324 起；此前只显示供应商简称」「此前每个消费者挂载都 mutate()」「此前草稿停在挂载时的空串」
- architecture.md §3D 处理工具：「此前传的是空偏好…（#334）」「此前用附件记录的第一个模型文件…（#321 B）」「此前对话按 Offering priority 先选 local/static-glb/to-gltf…」「此前读的是冻结 Offering 的裸 estimated_credits」「此前后者只删会话行」「此前 91.8 MB 的文件按 MiB 换算被说成 87.5 MB」
- architecture.md §Agent 计划提交核对（#160）：「此前该状态只能去任务列表人工查看」；§本轮上传提示（#293）：「此前的模板写的是“请使用 {tool} 工具处理”…」；§能力说明（#92）：「处理类 Offering 原先隐藏文件型输入」
- architecture.md §i18n（#254）：「这推翻了原先「用 `fields` 路径」的设想」；§Meshy Convert（#330）：「补齐格式原来对 Meshy 以外的来源发 Remesh 的 convert_format_only」；§Rodin/Bang：多处「此前」叙述
- architecture.md §同一轮内引用本轮结果（#25）：「起因：formal-test 上一轮回复先用 `refine_image` 抽出单体树，再用 `create_model_3d_from_image` 引用这张新图 `@img3`，被拒为“句柄无效”，3D 步骤没有提交。原因是工具只在本轮开始前的历史里查引用…」
- architecture.md §Agent 看到画板图（#16）：「起因：…所以 `agentRequest` 的视觉图片从未包含它，模型只读到 `image.canvas` 文字标签。现在…」；§单张参考图保持比例（#296）：「起因：#293 验收里对话上传一张 800×600 的图…」
- architecture.md §结果的主模型（#326）：「起因。dev-test 验收 #325（#325 F1）时…」案例叙述的引导语；§暂停的计划（#326 后续）：「起因。dev-test `4876a4f8` 验收（#326）发现 P」的引导语
- architecture.md §Creative Lab Agent 工具（#189）：「构建工具原先声明 `canvas_source: refuse`，于是在解析来源之前就整轮拒绝…」；§Agent 批准方式块（#7）：「Agent 过去拿不到会话的真实批准方式…曾把手工会话说成“自动”」
- architecture.md §同批付费生成并发提交（起因：线上一轮回复要求拆 6 个部件图，却被逐个生成）；§三视图与处理计划排队重提（#59，起因：上一节只让对话内的生成工具排队）
- architecture.md §画布尺寸提示：「服务端 `canvasSizeNote` 原先把同比例更小也算跟随，一并改正」；§内容审核（#35）：「原先只显示“任务输入无效”」；§结构检查预算（#226）：「真实 Meshy multiview FBX 此前在第 4 张图的结构遍历处被误拒」；§下载名（#264）：「此前 `GET …/assets/{assetID}` 不带 `Content-Disposition`…下载为 `asset_<id>.zip`」；§协调器错误（#298）：「此前凡映射为 `ErrUnavailable` 的错误都会结束协调器…」；§参数编辑（#246）：「此前每个 hook 实例各自持有编辑…」；§分件结果格式清单（#187）：「此前分件结果不在格式投影范围内…」；§HTTP 错误（#118/#316）：「此前它们自行编码，403/401 缺 `request_id`」

### 6.4 docs/product.md

- §1.1 Meshy 升级：「参考图尺寸、像素、大小超限的拒绝理由此前在英文界面也是中文，现在同样按界面语言」；「Nano Banana Pro 文生图、图生图由 6 改为官方的 9」；「模型文件上限由 60 MiB 提高到 70 MiB」；「文生 3D 的提示词上限改为官方的 800 字」；「按当天的 Meshy 官方文档」（2026-10-22）。
- §4 多个兼容服务选择卡：「用户原话：『多个兼容服务的时候，优先确定用户选择的，否则最好是 agent 询问用户，最后兜底一个。』」（#321，2026-10-22 用户决定）；「#321 后续，2026-10-22 用户决定，note 98949/98950」；「#321 第二轮，dev-test note 99042 的问题 G/H/I」。
- §4 Meshy Rigging 提示：「（…即 70 MiB；#329 前为 62.91 MB）」。
- §4.1：验收截图目录 `<内部目录>`（内部路径）。
- §4.2：「用户 2026-10-22：『思考强度是 low high 那些』」「用户 2026-10-20 决定 DeepSeek 默认 low」「用户 2026-10-22：『默认 80% 压缩。压缩后保留 5 轮。』」「用户 2026-10-22：『图片仍然可以被@，毕竟这个属于附件机制』」；「#322 dev-test 验收 note 98983 后修正」。
- §4.3：「统一网关下每个地址显式写同一个 URL，用户约定」；「即未设置时与 #322 之前行为一致」；note 98950/99059。
- §5：「2026-10-10 Lex 确认」（对话无付费步骤时不写积分）；「取代了无人读取的『隐藏失败』偏好」；「局部重绘蒙版改叫蒙版、像素画笔改叫画笔」；「原来的 R/L/O/P 和 I=生成键位已取消」；「2026-10-10 起没有聚焦模式（快捷键 F 已删除）」；「虚线框居中 dev-test 验收 #282 note 96919」。
- §8：Tripo 绑骨检查各次真实执行日期（2026-10-15/16）与 #186 note 94376；「#186 路线 B（用户决定）」（Tripo Rig v3.0、Texture/Retarget 启用，2026-10-17/20）；Smart Segmentation 2026-10-21/22 起；OBJ 压缩包下载修复 2026-10-22。
- §8.1：「2026-10-09 在当时的 60 MiB 上限下，66 MB 的高面数 Tripo GLB 曾按此拒绝」；「BLEND 全局下线（2026-10-18，#221 用户确认）」；「USDZ/USDC 数值弱校验（2026-10-13 用户确认，#167）」；「没有 UV 视图（2026-10-11 去掉）」；note 97106/97403/97469/96368。
- §8.2：「2026-10-08 用户确认」的要求清单标题；「#330 后续，note 99645」。
- §8.3：「Creative Lab 自 #100（2026-10-29）起」「2026-10-22 至 #100 停用前」「2026-10-21 复核官方合同」。
- 其余：§2/§6.1/§8.2 标题中的「2026-10-08 用户确认」；§4.1 标题「2026-10-09 用户要求」；§7「2026-10-29 对 main 核对，SKL-01 裁定」。

### 6.5 docs/provider-schema-architecture.md 等

#### docs/provider-schema-architecture.md 开头「更新」行（已删除）
更新：2026-10-22，补充「Meshy 官方合同基线」（#329）；2026-10-09，补充「最新 API 与最高质量」设计宗旨与 `fixed` 参数合同，以及 Meshy 文生 3D 自动精细化；2026-10-08 补充 Issue #134/#135 的 PC Skill 界面与可选分类合同。

#### docs/provider-schema-architecture.md「Meshy 官方合同基线」表
- Image to 3D 行：参考图此前最多 4 张，多于 1 张时发送文档没列的 `image_urls`（现为 1 张、只发 `image_url`）。
- Remesh 行：#330 前曾发 `convert_format_only`；Retexture 行：此前依赖 `latest`、声明过 `negative_prompt`；`nano-banana-pro` 估算由 6 改为 9。
- 「模型文件的大小上限」：70 MiB 此前为 60 MiB（#329 之前）；「Hyper3D 输出合同」与「上游输入一律内联」处同样的 60 MiB 括注已删。

#### docs/provider-schema-architecture.md「最新 API 与最高质量」现状表
- Meshy Retexture：此前误用不存在的 `resolution` 字段，上游实际按默认 2K 输出（现 `texture_resolution=8k`）。
- Meshy 文生图/图生图：2026-10-10 曾停用后恢复 `nano-banana-2`，旧版兼容 API 的 `legacy_models` 与 `legacy_constraint_option_labels` 随之恢复。
- Seedream：2026-10-10 选“可选比例”方案（低档由 `forced_when` 升档）；GPT `output_format` 非透明背景强制值由 webp 改为 png。
- 「固定不删声明」原标注“2026-10-10 审核返工”。

#### docs/provider-schema-architecture.md「Creative Lab」
- 2026-10-22 分两批启用（第一批：冰箱贴、手办、潮玩公仔、积木人偶、折叠解压玩具；第二批：钥匙扣、灯罩、像素解压拼图、键帽）；2026-10-29（#100）起全部 `enabled:false`。正文已改为现行状态加指向 architecture.md 的说明，批次描述保留为“曾分两批”。
- 钥匙扣/冰箱贴导出分辨率：早先记录的 64–2048 已被 512 上限取代。

#### docs/provider-schema-architecture.md Meshy 多图 / STL-3MF 槽位
- 2026-10-18 一次真实公开任务因 URL 后缀推断 MIME 失败（FBX `application/octet-stream`、OBJ `application/x-tgif`、USDZ/3MF 空）以 `integrity_failure` 失败，据此 Adapter 只在 `model/*` 声明且响应为 `application/x-tgif` 时保留声明类型（正文保留该事实）。
- #297：2026-10-21 本机隔离验收（#264）发现 Meshy 换贴图可选 STL 槽位接收 HTML 错误页、可选 3MF 槽位接收无模型部件的 ZIP，随后加入按 variant/MIME 识别的 STL/3MF 交付检查。

#### docs/image-api-parameters.md
- 「2026-10-09 更新」段落头：质量参数改为后端固定；Meshy `nano-banana-2` 曾停用，2026-10-10 恢复。
- 「结论修订（取代 2026-09-17 的『均未对代理逐项验证』）」：现标题为「结论（GPT Image 2.5 Sunburst 全声明参数）」。
- 「此前 `image_config`… 探针均未生效… 已撤下页面中这一过早结论」中的“已撤下”句。
- 旧站点 2026-09-09 的 GPT 图生图 JSON 记录：「本期按 S2 实测改为 multipart」改写为“以 S2 实测 multipart 为准”。
- 章节「旧模型名兼容决策（2026-09-27）」改名为「旧模型名兼容」。

#### docs/go-architecture.md
- 「本批保留 Go 单进程与统一任务内核，补充资产检查、原子写入、付费步骤恢复、会话保存和同源解码资源的实际结构」及「本页原先混合了 Next→Go 迁移分析……完整内容已移出仓库」。

#### docs/ui-kit-framework.md
- 「橙色表示提醒（2026-10-09 用户要求）」「推荐标签（2026-10-09 用户要求改为白字，提高对比度）」。

#### docs/3d-special-capabilities-design.md
- 「用户要求先找全特殊功能和本地上传边界……」改写为设计范围陈述。

### 6.6 docs/design 与 docs/v3-architecture

#### docs/design/README.md
- 开头：「本目录收录 2026-10-08 至 10-11 工作台重新设计的方案、原则及当时的实施记录。原件在项目共享文件夹 `design/`；仓库副本形成于 2026-10-11。」
- 「当前跟踪」末句：「已迁出的历史材料可从固定快照查阅：Agent 工作区实现台账（历史文件 `docs/agent-workspace-implementation-ledger.md`（已移出仓库，见内部 Git 历史 `b43d4018`））、Provider/Schema 实施计划（历史文件 `docs/provider-schema-implementation-plan.md`（已移出仓库，见内部 Git 历史 `b43d4018`））。」

#### docs/design/studio-ui-spec.md 「6. 历史实施拆分记录」末段
「2026-10-11 原记录指出当时尚未发布 dev-test、尚未完成 L0/L1 和页面回归。该记录是历史状态，不作为当前待办或验收结论；当前发布与验收进度只在原生 Milestones、Issues 和 MRs 更新。」

#### docs/design/right-panel-principles.md
- R4：「这是你今天早些时候的明确要求，PR #40（`3970ede`）已经删掉了收起按钮、记住状态和窄屏自动收起。」
- 开头：「顺序按你说的：先定 UE（这份），再定 UI，最后统一代码框架。」
- 「用户决定（2026-10-22，#321 note 98949/98950）」等引用头已改为不含人物日期的标题（正文规则保留）。

#### docs/v3-architecture/README.md
- 「原 G1 表格及其状态属于 2026-09-29 工作分支的阶段评审快照，已从此入口移除」（契约评审记录）

#### docs/v3-architecture/04-hyper3d-provider-fit.md
- 顶部状态段原文（含「下面除最后一节外都是历史记录」「2026-09-29 查阅……当时没有 Hyper3D API Key，历史试验不构成『真实生成可用』证据」）
- 兼容性表「积分展示」行原文：「在单位/换算与现有人工同意语义确定前保持 Offering enabled:false、预估值 0」
- 兼容性表「产物域名」行原文：「asset_hosts 暂为空……获得 Key 后以实际回包……登记精确 host」
- 「2026-10-16 原生候选更新（未启用）」整节原文改写为「原生候选配置」；删去「当前 Schema 已取代上文历史零估值与 Medium 首版说明」「全部候选仍禁用、host allowlist 为空」（Rodin 已于 #188 启用）

#### docs/v3-architecture 01-03
- 仅去除日期/「本轮」措辞，无整段移出。
- 05-local-regression.md、06-final-acceptance.md 为纯验收/证据报告，未改。
