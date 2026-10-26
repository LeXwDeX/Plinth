# 正式生产资产缺口只读诊断手册

## 0. 本文是什么、不是什么

诊断分支的开发规格和状态见 正式迁移 Milestone #4 下原生 Issues #197、#198、#199。本文保留诊断方法与审批约束，不维护本地实施计划或状态。

- 本文是迁移文档族的**条件诊断层**：仅当正式生产 Next→Go 迁移的隔离
  演练或停机 ETL 出现**资产大规模未固化**（触发模式见 §1）时才加载。
  正常迁移不读本文。
- 文档族关系：`docs/production-migration-runbook.md` 是唯一执行入口；
  `docs/production-etl-design.md` 是设计与不变量层（对账机器不变量、
  阶段状态机）；本文只在"资产大规模未固化"分支展开。三份
  文档冲突时以更严格者为准。
- **最高红线（覆盖一切进度与窗口压力）**：永不把批量缺失资产当作源
  损失批准。缺席于隔离快照 ≠ 缺席于正式生产。审批是绑定指纹的不可逆
  损失记录，不是恢复动作——它只是让门禁闭嘴，什么都不会恢复。
- 本文规定诊断方法与只读子命令合同，不含可直接在生产执行的现场命令块。
  所有现场动作以 §8 任务书移交给生产诊断 agent / 现场操作人；`<FORMAL_*>`
  为正式生产占位符，由操作人按 runbook 的输入清单填充，禁止从 dev-test
  测试环境推导。

## 1. 加载条件（触发模式与反触发）

满足任一模式即加载本文；T1–T4 同时出现是本文的典型形态。

| # | 触发模式 | 观测面 |
|---|---|---|
| T1 | 输入资产 unavailable 与任务必需产物 unavailable **两个独立路径族同率处于高位**——单一业务层 bug 难以同时命中两族，指向共模快照/布局故障 | ETL 报告（migration-*.txt） |
| T2 | 源损失候选覆盖**规模化多数任务**，而非零星历史任务 | source-loss-candidate JSON |
| T3 | 结构层对账（用户/任务/Attempt/账本/Chat/Canvas/余额/审计行）精确通过，唯独资产域高位失败——损失面限定在"文件字节不可得"，不是"行丢失" | ETL 报告对账段 |
| T4 | 审计副本恢复命中为零，或与审计树规模明显不成比例 | 报告 auditRecovered |

反触发（不适用本文）：

- 缺失零星且可逐任务解释 → 走 runbook / etl-design 的常规源损失审批
  流程，不需要大规模诊断。
- 怀疑"门禁全绿但数据不完整"（度量链缺陷）→ 那是 ETL 对账源计数规格 #193 的
  对账机器不变量问题；但注意两类缺陷可以并存，见 §2。

## 2. 第一步：两类缺陷分离（先分类，再诊断）

本节提及的代码/度量行为是本文编制时的诊断基线，不代表当前实现状态；当前规格和验收状态读取原生 Issue，判断源码时核验目标 revision。

完成标准：对下表两类缺陷各给出"发作 / 未发作 / 未定"判定，每条判定
绑定证据行；"未定"是合法结论，禁止为收敛而臆断。

| 维度 | 类一：全域源计数度量链缺陷 | 类二：本次资产解析命中率异常 |
|---|---|---|
| 层级 | 代码（度量链，etl-design A1–A3/A8） | 现场（快照内容/布局/词汇） |
| 覆盖域 | 全部对账维（潜在） | 仅资产（输入 + 任务产物） |
| 可见性 | **静默**：exit 可为 0，门禁全绿与数据不完整可并存 | **响亮**：unavailable 进 failureCount → exit≠0 → 产出候选 |
| 诊断方法 | 代码审计 + fixture（原生 Issue #193 的规格） | 本文 §5–§7 |
| 修复位置 | internal/etl（原生 Issue #193） | 现场采集/重快照 + catalog 兼容声明（原生 Issue #199） |

判定与耦合规则：

1. 两族**同率**高位缺失 + 结构层精确对账 + 存在部分成功固化样本 →
   类二发作（共模故障），且成功样本证明 join 逻辑与存储根**部分**有效
   ——是局部内容缺口（时代/前缀/采集范围），不是全路径 bug。
2. 类一是否发作**不能用报告散文判定**（它按构造静默）；其机器保证由
   对账规格 #193 定义。该规格未按原生验收记录确认前，任何分母（尤其产物侧"声明数"，来自加载集投影）必须先用独立 `SELECT COUNT`/`SUM` 复核才可用于审批决策。
3. 输入侧 unavailable 的理由码以 `asset_registry` 全量为分母（该域有
   独立 COUNT），先可信；产物侧分母需按 Issue #193 的规格和当前证据核验。
4. 两类缺陷**互不替代、都必须关闭**才能进入审批：类一保证"数的是
   对的行"，类二保证"文件真的不在"。

## 3. 解析级联速查（判别器基础）

本节解析路径和实现行为是本文编写时的历史代码基线，供复核时定位；不能据此断言目标提交的当前代码仍相同。执行前按目标 revision 核对。

以下为本文编写时的仓库代码基线（以符号定位，行号会漂移）；不作为当前源码状态断言。

输入侧 join（`openLegacyStorageFile`，internal/etl/migrate_assets.go）：

1. storagePath 非空、无 NUL、非绝对路径、无 `..` 越界 → 否则记
   unavailable，理由"storagePath 非法"；
2. `filepath.Clean` + `EvalSymlinks(root)`/`EvalSymlinks(join)` +
   `filepath.Rel` 包含性复查 → **缺失或悬空符号链接在此失败**，理由
   "解析旧资产文件"；
3. `storedSha256` 非空则必须等于重算 SHA-256 → 否则理由
   "SHA-256 不一致"；
4. 成功 → `PutBlob` 入目标 CAS。

三种理由码是 H2/H3/H7 的天然判别器（解析失败=缺文件/悬空链接；
非法=词汇漂移；SHA 不一致=损坏或篡改）。ETL 报告每类打印
≤ `maximumAssetReportIssues`（=20）条样本——§7 R0 的零成本起点。

产物侧五级回退（`Reader.OpenGenerationAsset`，
internal/adapters/legacygeneration/reader.go；`SourceResolver.Resolve`
先解包 catalog 声明的 `meshy_proxy_asset` 代理路由 `?url=` 形态）：

| 级 | 路由 | 落点与强约束 |
|---|---|---|
| 1 | generated 路由 `/api/gpt-images/{filename}` | `<storage>/generated/<filename>`（仅 basename） |
| 2 | token 型旧下载路由（catalog 声明，如背景去除下载） | `<storage>/cache/<子目录>/{token}` 文件名模板 |
| 3 | URL 哈希缓存 | SHA-256(path) 与 SHA-256(full URL，仅资产域) 双候选；期望 `<storage>/cache/<hash 前 2 字符>/<hash><ext>` **且**同目录可解析 `<hash>.meta.json`（catalog：`legacy_cache_prefix_characters=2`、`legacy_cache_metadata_suffix=".meta.json"`、`legacy_cache_content_type_field="contentType"`）——**sidecar 缺失/不可解析即 miss，即使 blob 存在** |
| 4 | 审计文件名恢复（仅 `ErrLegacyAssetUnavailable` 时，ETL 侧） | 期望名 `{task_id}_{source_stem}{source_extension}`（catalog `audit_asset_recovery.root_subdirectory="audit"`），在 `<storage>/audit` 下按**精确文件名**递归匹配；重名必须字节一致；**审计目录缺失被静默容忍** |
| 5 | 远端拉取 | ETL 注入 `offlineRoundTripper`（migrate_assets.go）且容器 `--network none`——**隔离运行必然失败（设计内）** |

之后 `assignLegacyRequiredOutputs` 按 Offering 最低数判定必需缺口 →
`markLegacyMaterializationFailed`（任务导入 FAILED /
`generation.FailureIntegrity`"历史任务产物不可固化"）+ 生成源损失候选。
候选中的 `task_id` 是任务身份，`issue_sha256` 是按
[`production-etl-design.md` §5.3.1](production-etl-design.md#531-issue_sha256-的可复算定义)
计算的 issue 摘要；两者不能合称为未定义的“复合指纹”。

级联解读要点：

- 路由 5 必然失败 ⇒ **远端-only 产物注定进候选池**——只读隔离诊断只能将其
  标为 `remote_offline/unresolved`。`UpstreamJob.RemoteID` 只是后续核验线索，
  既不能证明永久丢失，也不能保证上游仍可恢复；
- 路由 3 的 sidecar 强约束与路由 4 的模板强约束是两个可造成 100% miss
  的单点（H6/H5）；
- `resolveLegacyLayout`（internal/etl/precheck.go）硬编码测试环境布局
  （`<legacy-root>/storage` + `storage/data/prod.db`）；precheck 只验证
  六子目录**存在且可读**（零文件目录通过，文件数打印但从不与 DB 引用
  对比）——正式生产真实存储在别处即 H1。

## 4. 可证伪假设 H1–H8

每条假设最终必须有"成立 / 证伪 / 未定"判定 + 关键证据（§5 D1–D4 与
§6 清单提供判别数据）。

| 假设 | 一句话 | 优先级 | 判别落点 |
|---|---|---|---|
| H1 错误 legacy-root/存储挂载 | 真实存储在别的绑定挂载、named volume 或容器可写层；`<legacy-root>/storage/*` 是陈旧/骨架树 | P0 | §5 D1 Mounts × D3 可写层 × D2 对账 |
| H2 storagePath 词汇漂移 | 多数 `asset_registry.storagePath` 带 join 器产不出的前缀/形式（仅输入侧；单独无法解释产物侧） | P0 | §5 D4 桶划分 + §7 R2 |
| H3 快照采集缺口 | 采集排除/中断/错误卷根；符号链接树被保留但目标未进快照（`EvalSymlinks` 悬空失败） | P0 | §5 D2 清单↔inventory 对账 + 符号链接普查 |
| H4 产物缺口部分设计内 | meshy/tripo/gemini 等产物是上游 CDN URL 从未落盘；离线 ETL 禁重拉 | P1 | §5 D4 URL 路由桶 + §7 R5 远端未决分类 |
| H5 审计恢复键不匹配 | 真实审计文件名不遵 `{task_id}_{stem}{ext}` 模板 → 恢复零命中；审计目录缺失被静默吞掉 | P1 | §7 R4 + §6 outcome=audit_name_miss |
| H6 缓存哈希布局不匹配 | 正式缓存非 SHA-256(path)/SHA-256(url)+2 字符分片+.meta.json 约定；或仅签名 URL 的 full-URL 键永不命中 | P1 | §7 R3 + §6 outcome=cache_key_miss / sidecar_missing_or_invalid |
| H7 真实保留/GC 丢失 | 真"源损失"；需时间分层证据（时间上的锐利截断、两族不同率、跨环境同期一致） | P2 | 仅当 H1–H6 全证伪后升格；§10 审批纪律 |
| H8 加载器静默跳行 | 均匀高位缺失在算术上与结构层精确对账矛盾 | P2 | 无需单列诊断分支；计数保证按原生 Issue #193 的验收规格核对 |

分布判别经验：真实 GC/保留删除应与时间/队列相关——两族同率、结构层
完好、DB 与审计树完整的形态反证 GC，支持共模快照/布局故障。

## 5. 快照完备性证明（D1–D4 采集与 C1–C6 验收）

目标：把"快照忠实地复制了错误的树"（H1/H3）从假设变为可证伪命题。
**全部只读**；采集物统一写入 `<FORMAL_DIAG_OUT>`（权限 0600，逐件
SHA-256）。逐项完成标准见各判读行。

- **D1 容器挂载拓扑**：采集旧容器 `docker container inspect` 的
  Mounts / HostConfig.Binds / Config.Volumes / UpperDir，运行中再采
  `/proc/self/mountinfo`；容器已停止则用 inspect + `docker export`
  仅列目录不落地内容。
  判读：Mounts 中每个 source ≠ `<FORMAL_LEGACY_ROOT>/storage` 的
  绑定/卷，凡其容器内目标落在应用数据路径下，都是快照盲区候选 →
  记入 D2 对账。
- **D2 三方对账**：backup `manifest.txt` 的 `storage.<dir>.files` ↔
  正式 inventory 报告 `storage_*_files` / `legacy_mount=` ↔ 活宿主逐
  目录文件计数；另做符号链接与悬空符号链接普查。
  判读：任一子目录快照计数明显小于活树计数、或 `legacy_mount` 指向
  legacy_root/storage 之外 → H1/H3 成立的直接证据；悬空符号链接 > 0
  → 采集伪影（链接体被复制、目标卷未进快照）。
- **D3 容器可写层**：`docker diff` 标记 A/M 且路径落在旧应用存储前缀
  下的文件。
  判读：存在任一此类文件 = **隔离快照按构造不可见**的字节 → H1 可写
  层子型成立，快照必须重采集（含可写层导出）。
- **D4 DB 引用桶**（`sqlite3 -readonly` 对快照内 prod.db）：
  `asset_registry.storagePath` 按首段桶分布 + 绝对/越界计数；
  `usage_ledger` 结果 URL 按 proxied / generated / 旧下载 / remote_cdn
  路由分桶（resultUrls 为 JSON 数组时逐元素分桶）。
  判读：桶分布 vs 已成功固化样本的前缀 → H2 判定；remote_cdn 占主导
  → H4 主导产物侧（设计内缺口，改分类不改损失）；generated 桶数 vs
  `storage/generated` 文件数对账 → 路由 1 覆盖率。

验收矩阵——**全部闭合才算"快照完备性已证明"**：

| # | 断言 | 证据来源 |
|---|---|---|
| C1 | 旧容器每个数据挂载点都已进入快照（或被证明为空/无关） | D1 × D2 |
| C2 | 六子目录快照文件数 == 活宿主对应目录文件数（含可写层折算） | D2 + D3 |
| C3 | 无悬空符号链接，或每一个都有解释 | D2 |
| C4 | `asset_registry.storagePath` 每个桶都能映射到快照内一个物理位置（或被判非法并计数） | D4 × §6 清单 |
| C5 | `usage_ledger` 每个结果 URL 桶都被归类：本地路由可解析 / `remote_offline`（设计内不可离线物化、恢复性未决）/ 未解释 | D4 × §6 清单 |
| C6 | "未解释"桶为空；非空即停止并上报 | §6 rollup |

停止条件：C1–C5 任一无法闭合 → 快照判定不完备 → 走 §7 R1 重采集，
禁止进入 R5/R6 审批分支。

### 5.1 离线核对命令与证据信任边界

`art-ai-etl verify-snapshot --help` 给出参数。命令只消费已获授权采集的文件和
显式本地树，不执行 Docker、SSH、export、解包、网络请求、迁移或源损失批准。
它不读取完整 `docker inspect` 或 `Config.Env`，也不探索证据中出现的宿主路径。
D1/D3 必须由现场责任人另行提供下列白名单投影；本命令不会认证这些现场声明，
也无法发现未提供的挂载。#196 和正式责任人仍负责 inventory 来源、采集授权及现场关联。

```text
art-ai-etl verify-snapshot \
  --evidence <0600白名单投影.json> \
  --scope-bindings <0600显式树映射.json> \
  --legacy-database <独立一致性DELETE快照.db> \
  --snapshot-storage <只读snapshot/storage> \
  --snapshot-manifest <0600既有manifest.txt> \
  --inventory <0600实际inventory.txt> \
  --report <所有输入树之外的全新report.json> \
  --generated-at-utc <显式RFC3339时间> \
  [--catalog <Product Schema>] [--protected-root <CAS或目标保护根> ...]
```

投影格式为 `art-ai-snapshot-evidence-v1`，只接受以下字段；未知、重复、大小写
变体字段及尾随 JSON 均拒绝。摘要为实际文件的 64 位小写 SHA-256。

| 字段 | 必需内容 |
|---|---|
| `capture_epoch`, `captured_at_utc` | 非空采集批次和 RFC3339 时间；不得晚于报告时间 |
| `consistency` | `{kind,reference}`；kind 为 `stopped_writer` 或 `filesystem_snapshot`，reference 为非空的外部采集/冻结证据引用 |
| `identity` | `{host,daemon,container_id,compose_project,compose_service,image_id}`；容器为完整 64 位小写 ID，镜像为 `sha256:<64位摘要>`；全部与树映射中的 expected_identity 精确相同 |
| `state`, `storage_prefix`, `upper_dir` | `running`/`stopped`、明确容器 storage 前缀和 UpperDir 元数据；后两者只用于匹配，不授权打开宿主目录 |
| `mounts` | 必须显式提供数组，条目 `{type,source,destination}`；数据挂载须有对应 scope，嵌套挂载不能由父挂载自动代替 |
| `binds`, `volumes` | 必须显式提供的 Binds `{source,destination}` 数组与 Config.Volumes 目标路径数组；与 mounts 互相核对 |
| `mountinfo` | 运行态必须提供完整挂载点投影数组；运行态不能声明 stopped_writer |
| `export_paths` | 停止态必须提供非空的容器 export 目录清单投影；本命令不执行 export、不读取归档内容 |
| `diff` | 必须显式提供完整 `{change,path}` 数组，change 为 A/M/D；缺数组不能按“无改动”处理 |
| `inventory_sha256`, `manifest_sha256`, `database_sha256` | 绑定这次输入的 inventory、备份 manifest 和独立 SQLite 文件的实际摘要 |

树映射格式为 `art-ai-snapshot-bindings-v1`，包含 `expected_identity` 和 `scopes`。
每个 scope 的字段是 `{id,kind,container_path,origin,source_root,snapshot_path}`。
`kind` 为 `mount` 或 `writable_layer`；`source_root` 是调用人明确授权读取的本地
源树，`snapshot_path` 是 storage 内的规范相对路径（根为 `.`），且与容器命名空间一致。
`origin` 须对应 mounts 的 source，或 writable_layer 的 UpperDir 元数据。
可写层 scope 指独立提供的有效文件系统捕获树，不能把原始 UpperDir 当作已合并的容器视图。
本命令不因 origin、mountinfo 或 export 清单中的路径而扩大读取范围。

必须覆盖完整 snapshot storage 命名空间，包括其中的数据库文件和空目录；六目录
`assets/generated/cache/audit/thumbnails/task-inputs` 均须存在并有来源证据。
每个数据挂载、祖先挂载对应的数据子树、嵌套挂载及数据前缀内 A/M 路径都须被唯一解释；
A/M 还须有明确 writable_layer 捕获、同字节 snapshot 和停止态 export 清单对应项。
D 路径不得残留于 snapshot。重叠来源的同一命名空间记录必须相同。
快照不能与来源使用相同目录、祖孙目录或共享普通文件 inode；这不接受“比较自己”的证明。

D2 消费实际 `art-ai-formal-production-inventory-v1` 的 host、image ID、Compose、运行状态、采集时间、DB 摘要和挂载字段；其摘要正确但这些字段不一致仍 STOP。D2 同时核对完整相对路径、类型、大小、SHA-256 和现有 inventory/backup manifest
六目录计数；不能以计数相同代替字节一致。链接必须在明确根内可解析，悬空、越界、循环
或特殊文件均 STOP。数据树中出现 `.env`、`.env.*` 或 `runtime.env` 时，在读取其内容前 STOP，
不能把凭据文件悄悄排除后声称树完整。源树与 snapshot 所有节点身份、mtime、大小和
文件摘要，以及 DB/journal 和输入证据文件身份均在读取前后复核。变化则不发布可信报告。
这些重复观察不证明跨文件原子性；冻结/文件系统快照来源仍是外部声明。

D4 与 `diagnose-assets` 共用纯只读构建函数和相同 Catalog 路由；报告中的 diagnostic
摘要等于使用同一参数、时间和 manifest 独立运行 v1 子命令的实际报告摘要。另以独立
SQL 遍历全部 asset_registry 行和全部 usage_ledger 行，逐个 URL 分桶（包括非成功任务的
结果 URL，不能被成功产物投影遗漏）。前缀仅输出摘要，代理、generated、download、
已知本地缓存、remote_offline 等类别使用固定枚举。非法路径按技术桶计数；未认领路由、
未解释桶及诊断协议规定的非法路径/SHA 不符超过 10% 均 STOP。remote_offline 只表示
离线恢复性未决，不触发 HTTP，也不是源损失或可恢复保证。不新增 #199 的兼容映射。

新报告格式 `art-ai-snapshot-completeness-v1`，包含 D1–D4 证据摘要、C1–C6 的
`supported/refuted/unknown`、固定理由与证据引用、分桶、独立源计数和诊断 rollup。
D1–D4 在 `collection_evidence` 中各有独立状态。任一项未闭合时 decision 为 `STOP` 且退出非零；全部闭合为 `supported`，仅表示
所提供证据和显式树在本次观察中一致。`snapshot_identity` 绑定实际 DB、完整树、Catalog、
scope 图和输入证据摘要。`live_source_authenticated`、`atomic_source_snapshot_proven`
与 `source_loss_approval` 始终为 false；不能替换现场核实、迁移就绪证明或源损失批准。
原有 v1 诊断的 snapshot_completeness 仍为 unknown，不由本命令改写。

证据/树映射文件各最多 4 MiB、JSON 深度 64、节点 100000、scope 最多 128；每树
最多 1000000 个节点及 1 TiB 普通文件字节，所有独立来源树合计也受相同上限约束。D4 独立快照最多 512 MiB、独立表行数合计
最多 1000000，三个任务来源表的 JSON 字段单项最多 4 MiB、合计最多 64 MiB，URL
总数最多 1000000。达到工作/内存预算即 unknown/STOP，不采样、不截断；这些是本工具
的操作边界，不修改 migrate 或 diagnose-assets 的既有合同。

报告权限 0600，目录锚定、原子且拒绝覆盖；报告和临时文件均须在所有输入及声明的
CAS/目标保护树之外。stdout/stderr 只输出固定代码和摘要，失败也不回显底层路径或载荷。
非法参数、不可解析证据、输入变化及不安全输出位置不发布报告；能安全核对但证据未闭合
时发布明确的 STOP 报告。正式源树、凭据、迁移、删除和源损失批准均不由此命令授权。

## 6. 诊断清单 asset-resolution-diagnostic-manifest-v1

dry-run 重放 §3 完整解析级联的机器判别器：**不写 CAS、不改目标库**，
逐引用记录"试了哪条路由、候选路径是否存在、结果与理由"。其实现与验收见原生 Issue #197；在目标环境确认该能力可用前，由生产 agent 用 SQL + 文件存在性检查对每桶 ≤20 样本人工
复现等价记录。

条目八字段（命名固定）：

| 字段 | 取值 |
|---|---|
| `domain` | `input_asset` / `task_output_required` / `task_output_optional` / `audit_file` |
| `id` | task_id / asset_id / 审计相对路径的完整 `sha256:<hex>`，不提供明文开关 |
| `raw_reference` | 原始引用字符串的 `sha256:<hex>`——**原文永不落盘** |
| `normalized_reference` | 仅保留协议类别；host、path 各以完整 SHA-256 表示，query 仅标记 `<QUERY_REDACTED>`；不输出 token 或文件名 |
| `resolver` | `input_storage_path` / `input_sha256_recovery` / `generated_route` / `legacy_download_route` / `cache_path_hash` / `cache_url_hash` / `audit_filename_recovery` / `remote_offline` / `none`（无路由认领该引用形态） |
| `candidate_paths` | 按级联顺序记录 `{path_reference, resolver, exists, detail}`；`path_reference` 为快照内相对路径的完整 SHA-256，敏感相对文件名也不明文输出 |
| `outcome` | `materialized` / `missing_file` / `illegal_path` / `symlink_unresolved` / `sha256_mismatch` / `cache_key_miss` / `sidecar_missing_or_invalid` / `audit_name_miss` / `duplicate_conflict` / `remote_blocked_offline` / `unresolved_route` / `unreadable_file` / `too_large` / `content_rejected` |
| `reason` | 仅假设代码 + 技术描述；禁 prompt、用户名、业务文案 |

清单记录数据库与可选快照 manifest 的 SHA-256、显式 UTC 时间戳、`sampling:{policy:"full",seed:0}`、独立源表计数、实际加载计数、源账本金额及投影产物声明数。`rollup` 按 `domain × resolver × outcome` 全量计数；H1–H6 仅预置 `undetermined`，不能把技术结果直接提升为因果结论。输入行、任务行与投影声明分母分开，不能用两份相同 loader 长度互相证明完整性。输出沿严格迁移仅投影成功 Task；每个 role 的必需槽位先由实际可读产物满足，再将不足部分对应的失败引用列为 required，不能按 URL 声明顺序把前一条失败误当作必需缺口。非成功任务仍参与独立任务/账本计数，不从中猜测产物。

### 6.1 只读子命令合同

`art-ai-etl diagnose-assets` 必须显式传 `--legacy-database`、`--storage-root`、`--report`、`--generated-at-utc`。时间戳接受 RFC3339 并转为 UTC；相同字节、配置和时间戳产生相同报告。`--catalog` 可选择完整 Product Schema，省略时使用本版本嵌入声明；诊断沿用既有路由、缓存 sidecar、审计文件名和输入 SHA 恢复规则，不新增兼容映射。`--snapshot-manifest` 只关联该文件摘要，不验证其内容或完备性。

输入数据库必须是通过既有 SQLite backup 协议取得的独立、一致性快照；不得直接传仍活动的主库文件。命令拒绝源旁存在 `-wal` / `-journal`，以只读 immutable 连接读取，在同一事务内执行 `quick_check`、独立源计数及投影。运行前后复核数据库 inode、大小、mtime、全文件摘要和 journal 状态；已读取资产也复核 inode 与摘要。检测到变化则失败且不发布报告。这是本次读取稳定性检查，不能证明来源树已完整采集，也不能代替后续快照协议的现场核验；`snapshot_completeness` 固定为 `unknown`，`source_loss_approval` 固定为 `false`。

报告父目录须已存在，目标文件必须全新；临时文件和最终文件都不得落入数据库所在目录、storage、配置/manifest 所在目录或任一 `--protected-root`。该参数可重复，用于显式声明 CAS、目标库等只读保护根；命令不打开这些目标库、不猜测未声明目录。调用人须声明所有额外保护树，命令无法发现未提供的 CAS/目标位置。输出通过目录锚定的临时 0600 文件及原子、拒绝覆盖的链接发布。成功 stdout 仅含固定代码、报告摘要和条目数；失败 stderr 仅含固定错误码，底层 SQL/文件/Catalog 错误不回显。

`materialized` 仅表示此引用按现有规则可从快照安全读取并通过适用的字节检查，**没有写入 CAS 或目标库**。审计文件库存条目不等于恢复命中；同名副本字节不同保留 `duplicate_conflict`。输入直接路径解析仍限定 assets 子树，SHA 恢复独立搜索既有范围；generated、token 与 cache 主文件沿用各 Reader 的 Lstat regular 约束。缓存 sidecar 不可读/非法与选中缓存字节不合格分别报告；后者不被后续副本掩盖。generated/token 主文件超限记录 too_large 并停止，不能借审计副本绕过；cache 超限仍按既有 miss 规则尝试后续候选或审计。输出包大小门槛不施加到输入文件的只读流式哈希。远程 URL 只记录离线恢复性未决，不发 HTTP、不读取凭据、不调用迁移/Provider、不签发源损失批准。此子命令不放宽严格 `migrate` 的任何门槛。

## 7. 恢复次序 R0–R6（严格串行）

总原则：每一步只能**缩小候选集**；审批不可逆且绑指纹，必须排在所有
可缩小集的步骤之后。前一步退出证据未取得，不得进入后一步。

| 步 | 动作 | 进入条件 | 退出证据（可复测） |
|---|---|---|---|
| R0 | 读 ETL 报告每类 ≤20 样本行，按三种理由码分类计数 | 报告在手 | 三类计数表；非法/SHA 不一致占比异常 → 直接触发 §8 S5 停止条件评估 |
| R1 | **修快照**：按 §5 结论重采集（补 Mounts 盲区、导出容器可写层、消除悬空链接），采集后以活宿主为基准做 verify | §5 C1–C3 有未闭合项 | 新 manifest 与活树逐目录计数相等；悬空链接 = 0 |
| R2 | **路径映射**：若 H2 成立，以 catalog 声明新增输入路径兼容映射（前缀重写/归一规则），代码只消费配置，禁止在 Handler/ETL 写死分支 | R1 后 D4 复测仍有 illegal_path / missing_file 桶 | §6 清单 input_asset 域 illegal_path=0，missing_file 显著下降 |
| R3 | **哈希/缓存规则**：若 H6 成立，以 catalog 声明扩展缓存键候选（实际使用的摘要/分片/sidecar 约定、签名 URL 仅 path 键等） | §6 清单 cache_key_miss / sidecar_* 占主导 | cache 路由命中率复测表；产物侧 materialized 上升 |
| R4 | **审计副本**：若 H5 成立，以 catalog `audit_asset_recovery` 声明实际文件名模板（或多模板候选），重放路由 4 | §6 清单 audit_name_miss 占比高且审计树非空 | 审计恢复命中数（只救小头）；重名冲突 = 0 |
| R5 | **残余分类**：逐条定性剩余 unavailable：(a) remote-only（上游 CDN，`UpstreamJob.RemoteID` 在）→ 标为 `remote_offline/unresolved`，移出自动源损失候选，等待另行授权的上游留存核验；(b) 本地应有却无 → 真候选 | R1–R4 全部退出 | §6 全量 rollup：remote_blocked_offline 与本地 missing 分列；源损失候选只含 (b)，远端未决项单列 |
| R6 | **人工源损失审批**：仅对 (b) 残余，逐 cohort、逐任务真实 reason，正式生产负责人签名（etl-design 的审批流程不变） | R5 后候选仍 > 0 且 H7 时间分层证据成立 | 每任务 approval_reason + evidence；rebind 逐项一致校验通过 |

红线重申：R6 之前任何"批量/规模化批准"请求 = 停止条件，按 §8 S6
拒绝并上报。若 R1–R4 修复使布局变化，问题指纹随之变化，旧候选与旧
审批自然作废——早批不买任何东西，只毁掉审计链。

## 8. 生产诊断 agent 任务书（整块复制移交）

`<FORMAL_*>` 由操作人按 runbook 输入清单填充后，交正式生产侧执行。

```text
角色：正式生产资产缺口诊断 agent（只读诊断；无批准权、无变更权）。

授权边界：
- 允许：读取 <FORMAL_SNAPSHOT_ROOT>、<FORMAL_BACKUP_ROOT>、正式 inventory
  报告；在正式主机执行只读检查（docker inspect / docker diff / 挂载与
  文件系统只读列举 / sqlite3 -readonly / sha256sum）；写入唯一输出目录
  <FORMAL_DIAG_OUT>（权限 0600）。
- 禁止：对源库/快照/备份的任何写或删除；重启/停止/删除任何容器；访问
  外网或任何上游 API；读取 runtime.env 内容（只允许 stat 验证权限位）；
  批准、建议批准或预制任何源损失批准文件；把快照内的缺失表述为生产
  缺失结论。

任务序列（每步产出落 <FORMAL_DIAG_OUT>，带 SHA-256）：
  D1 采集旧容器 Mounts/Binds/Volumes/UpperDir 与 mountinfo（容器停止则
     用 inspect + export 仅列目录）。
  D2 三方对账：backup manifest 的 storage.<dir>.files ↔ inventory
     storage_*_files / legacy_mount ↔ 活宿主逐目录计数；符号链接与悬空
     链接普查。
  D3 容器可写层：docker diff，标记落在旧应用存储前缀下的 A/M 文件。
  D4 DB 引用桶（sqlite3 -readonly）：asset_registry.storagePath 首段桶 +
     绝对/越界计数；usage_ledger 结果 URL 按代理/generated/旧下载/
     remote_cdn 路由逐元素分桶。
  D5 若 dry-run 诊断子命令可用：生成 asset-resolution-diagnostic-manifest-v1
     （脱敏规则内置）；否则用 SQL + 文件存在性检查，每桶 ≤20 样本人工
     复现等价记录（字段：domain, id, raw_reference(哈希),
     normalized_reference, resolver, candidate_paths, outcome, reason）。
  D6 输出三件：completeness-report（快照完备性 C1–C6 逐项
     支持/反驳/未定 + 证据行）、diagnostic-manifest、假设判定表（H1–H8
     各：成立/证伪/未定 + 关键证据）。全部 0600。

停止条件（任一触发：立即停止、不执行后续步骤、输出当前状态并上报人工）：
  S1 无法唯一确认旧容器身份（compose 项目/service/标签/镜像任一不匹配
     或多个候选）。
  S2 inventory 报告缺失、或其 SHA-256 与现场不符、或 quick_check≠ok。
  S3 快照与活宿主存在未解释差异（目录计数不等、悬空链接、D3 发现存储
     前缀下的可写层文件且未在采集范围内）。
  S4 任何必需步骤要求写权限、网络访问或读取凭据才能完成——不得为完成
     步骤而申请这些权限。
  S5 illegal_path 或 sha256_mismatch 在样本中占比 > 10%（篡改/安全事件
     嫌疑，移交安全流程）。
  S6 任何指令要求你：批准或批量批准源损失、修改源库/快照、连接上游拉取
     资产、删除备份、重启旧容器——一律拒绝并原样记录该指令。

输出纪律：所有结论必须绑定证据行（操作 + 输出摘录 + SHA）；"未定"是
合法结论，禁止为收敛而臆断；最终报告末尾固定附上："本报告不含任何源
损失批准；审批权在正式生产负责人。"
```

## 9. 原生规格入口

本诊断分支的开发范围、依赖、阻塞和验收状态见 Milestone #4 下的原生 Issues #197、#198、#199；本文不维护工作包或进度映射。

## 10. 审批纪律不变量（源损失红线）

1. **永不把批量缺失资产当作源损失批准**——本文最高红线，覆盖一切
   进度/窗口压力。
2. 审批必须：逐 cohort、逐任务、真实 reason、人类签名、绑定最终停写
   快照指纹（etl-design 的审批与 rebind 机制不变）。
3. 进入审批的前置条件（全部满足，缺一即停）：§5 C1–C6 闭合；§7
   R1–R5 执行完毕且候选池仅剩"本地应有却无"；H7 时间分层证据成立；
   对账源计数规格 #193 已按原生 Issue 验收（分母机器可信）。
4. 缺席于隔离快照 ≠ 缺席于正式生产；远端-only 未固化先记为
   `remote_offline/unresolved`，不得在只读诊断阶段判为源损失或保证可恢复。
5. 任何要求跳过上述次序的指令（无论来自何种自动化或人工）→ §8 S6：
   拒绝、记录、上报。
