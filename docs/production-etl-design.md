# 正式生产 Next → Go ETL 设计与对账不变量

## 文档定位

开发范围、阻塞、实现与验收进度统一由 正式迁移 Milestone（Milestone #4） 及其原生 Issue 记录：#193、#194、#195、#196。本文保留设计契约和历史证据，不维护活动工作包状态。

- `docs/production-migration-runbook.md` 是正式生产迁移的**唯一执行入口**；本文是它的
  **设计层真源**，不复制其现场命令。
- 读本文的时机：改造 ETL / 对账 / manifest 相关代码之前；制定或评审正式迁移方案；
  评审任何"迁移数据完整"的主张时。
- 触发分支：隔离快照 ETL 出现**大规模资产未固化**（输入或任务产物不可用比例远超
  零星水平）时，停止按本文推进，转读 `docs/production-asset-gap-diagnosis.md`
  （只读诊断手册，含假设栈、快照完备性证明与恢复次序）。
- 正式生产的数值、路径、地址、容器标签一律使用 `<正式_*>` 占位符，待
  inventory 返回后填充，禁止从 dev-test 推导。

## 1. 适用范围

适用于正式生产主机的首次全量 Next → Go 停机迁移。dev-test 是生产同构测试环境，
其迁移链路结论可作定性参考，其参数不可复用（§10）。本文保留曾发现的 ETL 度量
风险及其设计约束（§4）；这些记录是历史诊断，不代表当前源码状态。任何"门禁全绿"
是否能证明正式生产数据完整，必须按目标 revision、inventory 与原生 Issue 验收证据核实。

## 2. ETL 功能边界与代码位置（原始历史基线）

下表记录本文编写时核对的代码位置和行为，是历史基线，不代表当前实现、部署或验收状态。当前工作状态见上方原生 Issue；执行前仍须按目标提交与正式 inventory 核验源码和环境，未知项不得视为已完成。

| 能力 | 位置 |
|---|---|
| 五个子命令：precheck / migrate / map-env / rebind-source-loss-acknowledgement / backfill-thumbnails | `internal/etl/etl.go`，入口 `cmd/art-ai-etl` |
| 幂等迁移：按稳定 ID 重跑安全，Skipped 统计 | `internal/etl/migrate.go` |
| 隔离容器执行：`--network none --read-only --cap-drop ALL`，UID 100:101 | `scripts/run-go-etl-container.sh` |
| ETL 禁止远端产物（离线 RoundTripper） | `internal/etl/migrate_assets.go`（`offlineRoundTripper`） |
| 源损失候选 / 审批 / 重绑定闭环；绑定旧库 SHA-256，审批后旧库变更即拒绝 | `internal/etl/source_loss_acknowledgement.go`、`internal/etl/migrate.go`、`scripts/finalize-go-production.sh` |
| CAS 内容寻址落盘 + 全引用可读性检查 + SHA-256 抽验（样本上限 `casSHA256SampleLimit`） | `internal/etl/migrate.go` |
| 迁移就绪证明 migration-ready.txt（绑镜像 tag + DB SHA + 报告 SHA，0600 原子写） | `scripts/run-go-etl-container.sh` |
| 停机事务：预检→停旧→备份→严格 ETL→rebind→提升→接管→验收，trap 自动回滚，只归档不删除 | `scripts/finalize-go-production.sh` |
| 只读脱敏盘点：11 表 + 6 storage 子目录 + 快照 SHA + quick_check + 外键，0600，不读 env | `scripts/collect-production-migration-inventory.sh` |
| 备份 create/verify 子命令 | `scripts/backup-go-production.sh` |
| 缩略图不复制、按需重建 + 幂等 backfill（不扣费） | `internal/etl/etl.go`、`internal/etl/migrate.go` |

## 3. 源 → 目标矩阵

本节的“独立源计数现状”“read_gap 可见性”和矩阵结论均是本文初版核查时的历史观察，不是当前代码断言。当前实现与缺口状态以原生 Issue 的目标 revision 证据为准。

源侧 11 表 + 6 storage 子目录（`collect-production-migration-inventory.sh` 顶部
`inventory_required_tables` / `inventory_required_storage` 清单）；目标侧 14 个
对账维（`internal/etl/migrate.go` `compareETLReconciliation`）。

| # | 源 | 目标（Go 原生） | 转换要点 | 独立源计数现状 | read_gap 可见性 |
|---|---|---|---|---|---|
| 1 | `users` | `authentication_users` | username 回退链、roles 逗号→数组、UTC | 有（`SELECT COUNT(*) FROM users`） | 差值仅打印散文，不入 failureCount |
| 2 | `usage_ledger` | `generation_tasks` + `generation_attempts` + `generation_usage_ledger` | 按 taskId 分组；resultUrls 供产物投影 | 无（ledgerRows = 加载迭代数） | 完全不可见 |
| 3 | `task_cache` | 并入任务 / Attempt | cacheMerged 计数 | 无（= len 加载集） | 完全不可见 |
| 4 | `task_input_snapshot` | 并入 Attempt 输入快照 | snapshotMerged 计数 | 无（= len 加载集） | 完全不可见 |
| 5 | `asset_registry` | `generation_input_assets` + CAS blob | storagePath 防越界 + SHA-256 一致性校验 | 有（`migrate_assets.go`） | 不可固化入 failureCount；读取缺口不计 |
| 6 | `chat_sessions` | `go_chat_sessions` | — | 有 | 仅散文 |
| 7 | `chat_messages` | `go_chat_messages` | 悬空消息跳过 | 有 | 仅散文 |
| 8 | `canvas_projects` | `go_canvas_projects` | — | 有 | 仅散文 |
| 9 | `canvas_nodes` | `go_canvas_nodes` | 悬空节点跳过 | 有 | 仅散文 |
| 10 | `balance_snapshots` | `generation_balance_snapshots` | accountID 来自 Product Schema | 有 | 仅散文 |
| 11 | `audit_logs` | `administration_audit_events` + audit 文件目录迁移 | 日志行 + 文件双轨 | 有 | 仅散文 |
| 12 | `storage/assets` | CAS（输入资产字节） | 同 #5 | 仅 inventory 目录统计 | 缺文件→unavailable→failureCount；多文件/孤儿不可见 |
| 13 | `storage/generated` + `storage/cache` | CAS（任务产物字节） | 本地/缓存/审计副本多级回退；离线禁拉远端 | 仅 inventory 目录统计 | 缺失→源损失候选→需审批 |
| 14 | `storage/audit` | 目标 audit 目录 | 文件迁移 | 仅 inventory 目录统计 | fileSkipped 入 failureCount |
| 15 | `storage/task-inputs` | 并入输入资产读取 | 防越界 | 仅 inventory 目录统计 | 同 #5 |
| 16 | `storage/thumbnails` | 不复制，按需重建 | 旧缓存字节仅报告 | 仅 inventory 目录统计 | 设计决定 |
| 17 | 各域 skipped/异常行 | `legacy_migration_records` 隔离归档 | 可审计 | — | LegacyRecords 维对账 |

矩阵结论：#2/#3/#4（任务主链）是最核心且完全无独立源计数的域。

## 4. 历史代码缺口基线：源计数 / read_gap / 自指对账

以下根因分析和 A1–A8 清单保留最初诊断证据，不表示这些缺口仍存在。实现状态以原生 Issue #193、#194、#195 和 #196 中的范围与原生状态为准；需要判断当前源码时核验其精确 SHA，不从本节或 Issue 的存在推断完成/部署。

### 4.1 根因链（机器判定不含源侧完整性）

```
门禁判定"迁移成功" ⇔ ETL exit 0 ⇔ failureCount == 0
  ⇔ (各域 skipped 明细 + 输入资产 unavailable + 未确认源损失 + 对账 issue) 之和 == 0
```

- `failureCount` 表达式（`internal/etl/migrate.go`，`runMigrate` 收尾）**不含任何
  "独立源计数 − 迁移计数"项**：loader 少读行时 exit 0 与数据不完整并存。
- 14 维对账期望（`compareETLReconciliation`）自指于**本进程加载集基数**，不是源库
  真值；目标侧 `ReconcileETL`（`internal/adapters/sqlite/etl_reconciliation.go`）
  统计带 ETL 标记的原生行。加载集缩小 → 期望同步缩小 → 该维不可见。
- users 域是唯一把独立 COUNT 差值打印出来的域（"用户域: 源/迁移/跳过"行），但差值
  不进 failureCount；chat/canvas/balance/audit/asset_registry 五域的 COUNT 同样只喂
  散文。
- 任务链更严重：`taskReport.ledgerRows` 报告为"源"实为逐行迭代计数
  （`internal/etl/migrate_tasks.go` `loadLegacyLedgerGroups`）；cache/snapshot 计数
  同为 `len(加载结果)`。loader 少读一半，散文也自洽显示"源=迁移"。
- 门禁是 awk 文本匹配（`scripts/run-go-etl-container.sh`、
  `scripts/cutover-go-production.sh` 中匹配「对账结论: 通过」行），对"源≠迁移+跳过
  但结论行=通过"的合成报告文本仍判通过——文本门禁的假通过是代码路径的固有属性。

### 4.2 缺口清单

| 编号 | 缺口 | 位置 |
|---|---|---|
| A1 | read_gap 只在 users 域可见，差值只打印不入 failureCount | `internal/etl/migrate.go` |
| A2 | 14 维对账期望自指于加载集 | `internal/etl/migrate.go`（`compareETLReconciliation`） |
| A3 | 任务/账本/Attempt/输入快照无独立源 COUNT；ledgerRows 冒称"源" | `internal/etl/migrate_tasks.go` |
| A4 | 门禁是 awk 文本匹配而非结构化校验 | `scripts/run-go-etl-container.sh`、`scripts/cutover-go-production.sh` |
| A5 | 对账报告是散文 txt，无机器可读 manifest | `internal/etl/migrate.go` |
| A6 | 验收脚本硬编码 dev-test 数字、路径与外部 Webhook URL | `scripts/post-cutover-acceptance.sh` |
| A7 | finalize 默认值来自 dev-test；旧容器的 Compose 标签与容器名曾硬编码（#261 起改为必填参数 `--legacy-container`/`--legacy-compose-project`/`--legacy-compose-service`，值由本地环境文件夹 `deploy/local/` 提供，仓库不写） | `scripts/finalize-go-production.sh` |
| A8 | read_gap 回归 fixture 仅覆盖 users 域散文，14 维盲区无同构样本 | `internal/etl` 测试 |

A1–A5、A8 和 A6/A7 的拆分是历史诊断口径。后续修复规格分别见原生 Issue #193–#196；正式输入依赖和实际完成状态按对应 Issue 验收记录及正式 inventory 核实。`internal/etl/precheck.go` 默认仍支持
`<旧根>/storage/data/prod.db` 的历史布局；若正式 inventory 显示 SQLite 位于独立
挂载，必须通过显式 `--legacy-database` 传入真实文件路径，禁止由旧根猜测。

### 4.3 两类缺陷的分离

- **类一（本节）**：代码级、全域、**静默**的度量链缺陷——exit 可为 0，“门禁全绿”
  与数据不完整并存。修复规格与状态见原生 Issue #193。
- **类二**：现场级资产解析命中率异常——**响亮**（unavailable 入 failureCount →
  exit≠0 → 产出源损失候选），属于快照内容/布局/词汇与解析器期望不匹配。诊断与
  恢复次序见 `docs/production-asset-gap-diagnosis.md`。
- 两类独立修复、独立验收，且**都必须关闭**才能进入源损失审批：类一保证"数的是
  对的行”（分母可信），类二保证“文件真的不在”。修复对账度量不会提高任何命中率；
  修好快照不会让 read_gap 可见。

## 5. 对账工件契约

### 5.1 completeness manifest

`etl-completeness-manifest-v1` 是机器可读对账清单：migrate 结束时与 txt 报告并排
落盘（0600、原子写）。以下是逐域设计契约；当前实现覆盖范围与验收状态以原生 Issue
#193、#194、#195 为准。

```json
{
  "format": "art-ai-etl-completeness-manifest-v1",
  "legacy_database_sha256": "<最终停写快照 SHA-256>",
  "image_tag": "<镜像 tag>",
  "source_commit": "<origin/main 完整 SHA>",
  "generated_at_utc": "…",
  "domains": [
    {
      "domain": "usage_ledger",
      "source_count": 0,
      "loaded_count": 0,
      "imported_count": 0,
      "target_count": 0,
      "skipped_count": 0,
      "read_gap": 0,
      "unexplained_gap": 0,
      "status": "pass"
    }
  ],
  "credits": {"source_sum": 0, "loaded_sum": 0, "target_sum": 0, "equal": true},
  "cas": {"references": 0, "readable": 0, "sha256_sampled": 0, "sha256_mismatch": 0},
  "source_loss": {"candidate_count": 0, "acknowledged": 0, "unacknowledged": 0},
  "overall": {"failure_count": 0, "conclusion": "pass"}
}
```

验收不变量：

- 11 张源表的 `source_count` 来自同一只读 SQLite 事务中的独立 `COUNT(*)`，`loaded_count` 来自 loader 实际迭代，不能复用 SQL COUNT；额外的 `generation_tasks` 域使用源账本独立 `COUNT(DISTINCT taskId)` 与加载任务组数及目标 Task 数对账。NULL、空白 taskId 拒绝迁移；合法多笔账本同 taskId 只计一个 Task，Attempt 与账本仍逐笔核对。`task_outputs` 是产物投影声明数，不冒称独立源表 COUNT；
- 每域 `read_gap = source_count − loaded_count` 保留有符号差值，`unexplained_gap = read_gap`；二者必须为 0，任何少读或多读均计入 failureCount。`skipped_count` 仅描述已读记录的业务跳过，不抵消另一条未读记录；幂等重跑跳过量另列在文本报告，不放入此字段；
- 13 个必需域（11 张源表加 `generation_tasks`、`task_outputs`）必须完整且唯一；普通原生域要求 `loaded_count = imported_count + skipped_count`，业务跳过与已读成功行合计覆盖读取集。幂等导入跳过仍对应既有目标行，不增加业务 `skipped_count`；
- cache/snapshot 的 `imported_count` 是已合入任务与已隔离归档的合计，`archived_count` 是其中的归档数，非业务跳过。`target_count` 逐源 taskId 核对持久目标的 Task/Attempt 与 ETL 标记，或同表同 ID 的隔离归档与 ETL 标记；无关归档不能补足缺失源身份，不用内存合并计数充当目标查询；两域要求 `loaded_count = imported_count`、`skipped_count = 0` 且 `archived_count ≤ imported_count`。`task_outputs.skipped_count` 是问题明细数，不能套用行数分区等式，仅要求固化数不超过声明数，仍保留原有产物与源损失检查；
- `generation_tasks.target_count` 是 ETL reconciliation 对带 `etl_import_items(domain='generation_task')` 标记行的计数；公开任务列表还会包含未带该标记的原生 Task，并按 tombstone 排除删除项。未独立证明两者任务身份集合相同前，不得把公开列表总数当成该 `target_count` 的替代或直接相等断言；生产观察时按 runbook 分别建立对应范围的基线；
- `credits.source_sum` 来自同一源快照的独立 `SUM(estimatedCredits)`；`loaded_sum` 是加载累计，`target_sum` 来自目标 ETL 标记账本的 SQL SUM。三者必须一致，不能因加载金额与目标同错而通过；
- `overall.conclusion == "pass"` 当且仅当全部域 pass 且 failure_count == 0；容器 runner 要求上述完整唯一域集合，计数和金额为非负整数，拒绝字符串、小数、负数及缺失必需数字字段；
- 源损失候选 `issue_sha256` 按 §5.3.1 的固定字段编码生成；最终数据库快照仍以文件字节 SHA-256 绑定，存储树仍使用逐文件 SHA-256 清单。未声明派生算法的其他复合指纹不作为验收证据。

### 5.2 正式生产 baseline

由 inventory 报告**机械派生**：`{inventory_sha256, 11 表行数, 6 storage 子目录
files/bytes, 派生时间}`。一经生成即只读归档；停机事务与验收脚本用
`--baseline` 显式加载；**任何与 baseline 的偏差都是停止条件**。
停机事务的当前消费参数区分 `--source-baseline`（来源快照身份）与
`--acceptance-baseline`（目标数量/入口验收）。来源 JSON 的最低字段与 snapshot
算法以 runbook「来源快照基线与拒绝条件」为准：只读 online backup 必须纳入 WAL，
最终独立 DELETE 备份直接核对字节 SHA，不再重复 backup。`inventory_sha256` 的
证据关联由 #196 的授权采集/生成流程验证，当前消费端只验证其格式。正式 inventory 到
正式 baseline 的机械生成器及验收规格见原生 Issue
#196。合成测试树只能验证工具行为，不能代替从正式环境采集并核验真实 inventory，也不能据此完成该验收。

### 5.3 源损失审批 schema 与约束

以下保留源损失记录的数据合同。文件/代码路径是历史核查线索；执行时须在目标 revision 核实实现及其验收状态，跟踪见原生 Issue #195。

格式 `art-ai-go-source-loss-ack-v1`（`internal/etl/source_loss_acknowledgement.go`）：
`format / legacy_database_sha256 / approved / approved_by / approved_at_utc /
approval_reason / approval_evidence_sha256 / tasks[]{task_id, capability,
offering_id, provider_id, model_id, issue_count, issue_sha256, reason}`。约束保持：
候选与审批文件互斥使用、权限 0600 且 root 执行时属主 UID 100、审批后旧库指纹变化
即拒绝、rebind 要求问题集与最终停写快照逐项一致。

#### 5.3.1 issue_sha256 的可复算定义

候选文件中的 `issue_sha256` 是 `internal/etl/migrate_assets.go` 当前实现的摘要，
不是资产字节摘要，也不替代 `legacy_database_sha256`。当前生成器直接使用 Go `string`
中的原始字节，以单个 NUL 字节连接有序字段（不追加末尾分隔符），再计算 SHA-256，
输出 64 位小写十六进制。当前代码不校验字段是否含 NUL 或是否为有效 UTF-8，因此下面
列出的 NUL-free、有效 UTF-8 是运维核对和人工复算的前置条件，不是 CLI 已执行的拒绝校验：

- 任务产物问题：`task_id`、`capability`、`offering_id`、`provider_id`、`model_id`、
  十进制 `issue_count`、按 Go 字符串字节序升序排列的每条 issue 文本。排序保留重复项；
  `issue_count` 也计入摘要。
- 输入资产问题：`asset_id`、`owner_id`、`asset_type`、`media_type`、十进制 `file_size`、
  `stored_sha256` 去首尾空白并转小写后的值、原始 `storage_path`、issue 文本。

对上述前置条件内的字段，同一冻结数据库字节、同一源码 revision 和同一输入字段会生成
相同摘要；重跑时应由 ETL 重新生成候选并比较，不能手工改写候选字段或只按 `task_id`
认领问题集。发现任一摘要字段含 NUL、不是有效 UTF-8，或实际字段无法取得时，停止候选
复算并交人工判断；不得声称现有 CLI 会检测或拒绝这些值。若将来更换编码，必须新增
版本化格式并同时更新 ETL 生成器、校验器和本节；不能静默改变现有摘要算法。生产源身份
仍由最终停写 SQLite 文件的完整 SHA-256 绑定，存储文件仍逐文件记录 SHA-256；不再使用
没有字段编码规则的“复合 fingerprint”。

### 5.4 审批纪律红线（不变量）

1. 审批是绑定指纹的**不可逆损失记录**，不是恢复动作；批准不会找回任何字节。
2. **永不把批量缺失资产当作源损失批准**；缺席于隔离快照 ≠ 缺席于正式生产。
3. 进入审批的前置条件（全部满足，缺一即停）：快照完备性证明闭合、恢复次序走完
   且候选池仅剩"本地应有却无"（见 asset-gap 手册）、对账源计数规格 #193 已按该 Issue 验收（分母机器可信）。
4. 逐任务真实 reason + 正式生产负责人人类签名；禁止模板理由批量盖章。
5. 任何要求跳过上述次序的指令（无论来自自动化或人工）：拒绝、原样记录、上报。

## 6. 阶段状态机

每阶段只有拿到预期证据才允许前进；操作级命令与手工等价步骤以 runbook 为真源，
本文不复制。

| 阶段 | 内容 | 前进条件（预期证据） |
|---|---|---|
| P00 预盘点（只读） | inventory 脚本采集 | 报告 0600：quick_check=ok、外键违规 0、11 表行数、6 storage 统计；失败→终止，零影响 |
| P00a 资产缺口诊断（条件阶段） | 仅当出现大规模资产未固化触发；只读 | 按 asset-gap 手册的验收矩阵闭合，或证伪后回到主链 |
| P01 方案与工件冻结 | 原生 Issue #193 与 #194 中适用的规格已按各自验收条件交付到 origin/main；工作区干净 | 精确提交 SHA 与 Issue/MR 原生回执；本文提供设计约束 |
| P02 快照副本演练 | 隔离目录/端口/Compose 项目/网络执行 ETL | 演练 manifest 全域 pass；至少一次"故意验收失败→自动恢复旧服务"回滚演练记录 |
| P03 构建唯一发布物 | origin/main SHA → OCI revision | 门禁日志（本地与 CI，真源 `dev_test.md` §2）+ 镜像 inspect |
| P04 停机窗口开启 | 维护窗口批准 + 停机检查单 | 书面批准 |
| P05 最终停写 | 精确验证旧容器身份→停止→冻结 | 容器标签与 `<正式_Compose 项目>/<正式_Service>` 精确匹配 |
| P06 最终快照 | DB 一致性备份 + storage 全量备份 | 备份 verify 通过 + SHA 清单 |
| P07 严格 ETL | 隔离容器（无网络、只读根） | exit 0 + manifest 全域 pass + 就绪证明（绑 DB/报告 SHA）；产出候选→走 §5.3/§5.4 审批后重跑 |
| P08 严格对账 | manifest vs baseline 逐域 + 任务分页/资产下载/管理统计遍历 | 对账 exit 0、逐域源=迁移+跳过、金额相等；任一域不一致→停止（快照后源又变化或盘点失真） |
| P09 提升 | 旧数据归档→staging 提升→Go Compose 接管原入口 | 提升记录 |
| P10 无扣费验收 | 正式基线版验收脚本 | 验收全绿；任一失败→R 回滚（finalize trap） |
| R 回滚 | 停 Go→只撤销实际成功的逐项提升→previous 回位→重启原旧容器 ID | 与 P02 实弹验证的回滚同构；停 Go/回位失败须保留两套数据并阻断旧服务启动，报告未完成 |
| P11 观察期 | 保留全部备份/归档/旧容器 | 书面批准清理前不删除任何内容 |
| P12 完成 | 切换记录 + 验收报告落盘 | 归档 0600 |

不变量：P02 与 P07 必须使用**同一镜像**；全链路禁止 `--remove-orphans`；正式生产
必须使用自身的最终停写快照执行一次全量 ETL，禁止复用任何测试环境的就绪证明。

## 7. 数据完成口径（对 runbook 完成口径的数据层细化）

runbook「完成口径」是整体门槛；本节定义其中必须验证的数据完整性子层：

- manifest 全域 `status == "pass"` 且 `unexplained_gap == 0`，且每域 `source_count`
  等于正式 baseline 对应值；
- 所有域 `read_gap == 0`、`source_count == loaded_count`、`imported_count == target_count`；
- `credits.source_sum == credits.loaded_sum == credits.target_sum`；
- CAS：全部引用可读、SHA 抽验零不一致；
- 源损失全部已确认，`unacknowledged == 0`；
- 门禁以结构化校验（对 manifest 的程序化断言）为主，awk 文本匹配降级为冗余信号；
- completeness manifest 已成为 ETL 容器门禁的主证据；runner 与 takeover 使用同一
  `scripts/etl-completeness-filter.jq`；就绪证明的固定八字段必须包含 manifest 名与摘要，
  拒绝旧六字段、未知字段和摘要不符。散文报告仍作为人工排障和冗余信号，不能替代
  manifest 的逐域断言。

## 8. 原生规格与状态入口

正式迁移工作包的范围、实施、阻塞和验收由 Milestone #4 下的原生 Issues #193–#199 记录；本地不维护状态、完成矩阵或实施进度副本。资产解析诊断的规格入口见 [`production-asset-gap-diagnosis.md` §9](production-asset-gap-diagnosis.md)。Go 侧验证命令以 `dev_test.md` §2 为真源。

## 9. 正式 inventory 依赖

- 前置输入清单的真源是 runbook「生产资产清单」；**任一项未从正式 inventory
  收集并核验 → 不生成正式配置，不进入 P02 之后任何阶段**。正式 inventory 和
  baseline 的实现验收见原生 Issue #196；合成测试不能替代真实正式 inventory。
- 本设计附加的派生规则：baseline 由 inventory 机械派生并绑定其 SHA（§5.2）；验收
  脚本只读 baseline；源损失审批清单绑定**最终停写快照** SHA（区别于 P00 预盘点
  快照）；发布物要求 origin/main 完整提交 SHA + `dev_test.md` §2 门禁（本地与 CI）通过记录。
- inventory 必须确认旧库真实布局与 `resolveLegacyLayout` 的假设一致，否则按布局
  错配处理（诊断见 asset-gap 手册）。

## 10. dev-test 不可复用项（出现即返工）

- 任何数字基线：用户数、任务数、积分数、审计条数等（具体值见 `dev_ops.md`，
  本文不复述）。
- 容器与 Compose 项目名：旧 Next 容器与项目名，以及 dev-test、候选栈的 Compose 项目名（只在本地环境文件）。
- 数据根路径：dev-test 主机上的旧 Next 与 Go 数据根。
- 端口与地址：dev-test 入口及其依赖服务地址、外部 Webhook URL。
- dev-test 的 `final-source-loss-approved.json` 及其问题集、测试管理员凭据、
  runtime.env 任何值、Webhook 路径 secret。
- dev-test 演练的耗时/体积/资源峰值结论：仅定性参考，正式生产以自身演练重测。
