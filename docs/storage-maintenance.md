# CAS 存储维护

`art-ai-storage` 随 Go 镜像提供，也可通过 `go build ./cmd/art-ai-storage`
构建。命令只处理指定存储根内由 CAS 写入器产生的 `blobs/` 文件、
`tmp/asset-*` 临时文件、Provider 上传时留在 `tmp/provider-*` 的私有副本（#321，正常结束即删除，
只有进程中断才会留下），以及图片预览缓存（#291）；其他历史缓存、ETL 源目录和
封存数据不在范围内。

图片预览缓存在 Product Schema `application.runtime.asset_delivery.asset_preview.cache_subdirectory`
（默认 `asset-previews`）下，文件名是原图 SHA-256 加预览尺寸
（`<两位前缀>/<sha256>-<尺寸>.png`）。原图 blob 也以同一个 SHA-256 命名，所以预览
与 blob 用同一份引用快照判断：仍有被引用的 blob 是这个摘要、且尺寸是当前
`maximum_dimension` 的预览保留；其余预览和中断写入留下的 `*.png.tmp-*` 在超过
最小保留时间后列为候选。目录里不是预览存储写出的文件不动。预览删错也只会在
下次请求时重新生成，不影响原图。

## 发布回滚快照与离线维护互斥（#364）

`dev-test` 可配置 `backup.scope=database`，只把 `production/data` 打包到
`backups/release-<tag>/`；该目录的 `scope` 文件只含一行 `database`（全量快照为
`full`），`database` 对应 `data.tar`，`full` 对应三个归档。没有 `scope` 的旧快照
只在 `data.tar`、`storage-go.tar`、`web-data.tar` 三个归档齐全时按 `full` 读取。
这类快照不包含 `storage-go` 的 CAS 字节或 `web-data` 的 Web 文件。运行时对 CAS
只追加、不删除 blob，`web-data` 只读；候选发布写入的字节在数据库回滚后仍保留，
可能没有数据库引用，属于预期的孤儿字节，不是损坏。

**只要 database-only 回滚快照仍可能被使用，就不得运行离线 CAS 或任务保留的
破坏性维护**：`art-ai-storage --execute`、`art-ai-retention` 的 `apply`/`cleanup`，
以及任何按旧数据库引用清单删除 CAS 字节的操作。恢复后的旧数据库可能看不到候选
新增的 CAS 引用；凭它清理会删掉仍需人工核对的数据，且无法用该快照恢复。只读的
`--check`、默认 dry-run 清单和 `receipt` 查询可以继续。

要继续破坏性维护，必须先结束回滚窗口：停止该栈写入，按发布协议确认不再需要
database-only 快照，再对它做新的数据库、CAS 与 `web-data` 全量一致备份。不得为
让 database-only 回滚“一致”而删除候选 CAS 字节，也不得把该快照当作完整恢复点。
手工恢复和发布备份清理共享 `<root>/.release.lock`：恢复必须用
`release-lock.py run --root <root> -- ...` 包住停止、移动、解包与启动；需要移动
CAS 目录的破坏性维护也应在同一锁内执行，避免与发布/清理并发。

## 生成清单

必须显式提供属于同一运行实例的数据库和存储根。默认只检查，不删除：

```sh
art-ai-storage --database /path/to/data/art-ai.db --storage /path/to/storage > storage-dry-run.json
```

自定义 Product Schema 可使用 `--catalog /path/to/catalog.json`。最小保留时间
来自 `application.runtime.storage_maintenance.minimum_age_seconds`，默认
86400 秒。默认清单可在服务运行时生成，但仅代表扫描时的快照。

引用基准是同一个只读 SQLite 事务中的：

- `generation_assets.storage_key`；
- `generation_input_assets.storage_key`；
- `generation_artifact_claims.stored_asset_json.storage_key`（待物化本地图片）。

所有任务的引用均保留，包括被标记删除或已失败的任务。此命令不执行数据库
资产保留策略，不把“前端看不见”当作“无人引用”。数据库缺失、读取失败或
损坏的 claim JSON 会使操作失败，禁止以空引用集合继续删除。

数据库任务保留使用下述独立的 `art-ai-retention` 命令。历史 2026-08-24
清理过程的原始记录仅存于 git 历史（原 `docs/archive/task-retention-cleanup-20260824.md`，
已清理），不构成当前保留期、候选规则或运行授权。

JSON 报告包含引用数量、内容大小、磁盘容量/可用字节/占用百分比、孤儿数量、
预览数量/大小/孤儿数量（`preview_*`，候选同样列在 `candidates` 中，键以预览目录开头）、
候选存储键/修改时间/大小，以及实际删除结果。报告退出非零时仍可能含部分
结果；以退出码和每项 `deleted` 字段判断，不能只看文件是否生成。

## 只读完整性扫描

`--check` 与孤儿清理分开运行。它需要显式的同实例数据库与存储根，并要求所有
存储写入者停止；命令在存储维护锁下读取数据库引用快照并核对 CAS 字节，输出
`read_only: true` 报告，列出缺失、损坏、key 不匹配或引用元数据冲突。示例：

```sh
art-ai-storage --database /path/to/data/art-ai.db --storage /path/to/storage --check > storage-integrity.json
```

检查失败时以退出码和报告为准。该扫描不修复 blob、不改写 Asset/Task 状态，
也不代表扫描后没有并发写入者造成的新变化。对损坏项应先保存报告和备份，
再依可验证来源决定重新物化或人工处置；不要通过 `--execute` 清除仍被引用的内容。

## 执行清理

先检查清单并完成数据库与存储的一致性备份。停止使用该存储根的所有 Go
服务、ETL 和其他写入进程，再运行：

```sh
art-ai-storage --database /path/to/data/art-ai.db --storage /path/to/storage --execute > storage-cleanup.json
```

所有 `local.Store` 在生命周期内持有 `.maintenance.lock` 的共享锁；清理执行
需要取得排他锁。服务仍在运行时，命令立即失败，不等待和强制解锁。排他锁
覆盖“读取最新引用 → 完成全部目录扫描 → 删除候选文件”的全过程，因此
旧文件复用、延迟登记和新的写入都不能穿过清理边界。执行会重新读取引用，
不会直接使用先前 dry-run 的候选集合。宽限时间是额外保护，不能替代锁。

清理中断后可重跑，已删除候选不会再次出现。删除不提供内置撤销，若需恢复
使用执行前的一致性备份。不要在多个不同数据库共享同一 CAS 根的拓扑中
执行此命令；当前运行时的一个存储根对应一个数据库。

## 升级行为

同步图片先在有界路径中写入 CAS，再提交图片引用；数据库不再保存新图片的
完整 inline BLOB。提交失败遗留的文件将在超过保留时间且没有上述引用时
出现在维护清单。旧数据库中尚未完成物化的 inline claim 继续按原路径恢复。

## 独立的任务保留命令

`art-ai-retention` 随同版本 Go 运行镜像提供，路径为 `/usr/local/bin/art-ai-retention`，
也可通过 `go build ./cmd/art-ai-retention` 独立构建。它使用 Generation 的
RetentionService、SQLite 维护端口和受限的本地 CAS 端口；不调用 Provider
任务删除，不改变存活任务终态，也不由服务器 worker 或定时任务自动运行。
查看命令参数可运行 `art-ai-retention prepare --help`；解析帮助在打开数据库、
创建清单或取得维护锁之前退出，不执行 prepare。二进制随镜像提供不表示授权
执行维护；#59 的验收仅包含隔离 fixture，不允许为验收运行 live purge。

默认 Product Schema 的 `application.runtime.task_retention.enabled` 为 false，
`minimum_age_seconds` 为 0。启用必须在显式提供的私有 Schema 中填写正数保留期；
程序不会从历史清理日期、孤儿清理宽限时间或当前日期推断保留期。旧 Catalog
完全省略该段时只表示禁用，不补入任何执行默认值。

范围由 Schema 声明：只考虑 `task_states` 中的终态 Task，时间依据
`finished_at`；输入资产依据 `created_at`，都必须严格早于显式 cutoff。
租约、后续调度和待完成资源物化会保护任务。保留策略是 `preserve_all`：
Ledger、Grant/批准、PreparedInput、Chat、Agent checkpoint、SkillRun、Processing
Plan、Scene/RenderManifest/Capture、Canvas 及其 JSON 引用继续保护来源。
删除候选自己的 Attempt、Claim、Asset、Event、Tombstone 和读取投影属于同一
删除组，不会仅因自引用而永远保留；跨候选引用以固定点收敛。共享 CAS 文件
只有在所有引用都属于最终删除组时才进入删除清单。因此有永久审计引用的任务
可能永远不成为候选，不能通过忽略审计或更改必需产物规则让清单变大。

当前解析器核对完整迁移与 sqlite_schema，读取所有表的有类型字段，递归解析
JSON、嵌套 JSON 文本、历史 base64 工具参数及 URL 转义引用，包括候选来源列表。
损坏 JSON、未知参数编码、未登记 CAS 引用、非空 legacy_migration_records 或
未知表结构会拒绝维护。它采用保守字符串引用匹配，可能多保留，不会把“没有
外键”当作“没有引用”。快照上限为 100 万行、字段值合计 256 MiB，备份数据库
摘要验证上限为 2 GiB；超限必须评审新的有界实现，不能删去检查后继续操作。

### 清单、人工审批与恢复

以下路径都是示例。须先停止该数据库/CAS 的所有写入进程；一个 CAS 只允许
对应一个数据库。每个命令都要求绝对、规范化且无符号链接的路径。源目录、
数据库及 sidecar 不得与输出重叠。prepare 只生成清单，但仍取得 CAS 排他维护锁
和 SQLite `BEGIN IMMEDIATE` 写入保留，避免在并发变化的对象上做清理决定；
它不创建或迁移源数据库。

```sh
art-ai-retention prepare --database /data/art-ai.db --storage /data/cas --catalog /ops/retention-schema.json --cutoff 2026-01-01T00:00:00Z --manifest /ops/review/manifest.json
```

清单含冻结策略、cutoff、数据库/CAS 设备及 inode、全库快照/Schema 摘要、精确
Task/Input ID、受保护原因和全部相关 CAS 的大小/摘要/inode。输出返回完整
SHA-256。文件以独占创建、只读权限和 fsync 保存；这不是操作系统 immutable
标记。修改任意字节、重复 JSON 字段、额外空白或使用旧 SHA 都会拒绝执行。

```sh
art-ai-retention apply --database /data/art-ai.db --storage /data/cas --catalog /ops/retention-schema.json --manifest /ops/review/manifest.json --sha256 REVIEWED_SHA256 --backup /ops/backups/new-retention-directory
```

apply 会重新核对完整清单，并要求独立人类操作者在控制终端输入身份和
`APPROVE apply <完整SHA-256>`。姓名、参数、环境变量或 stdin 管道不是审批凭据；
Agent 不得代答，测试替身只用于隔离 Go fixture。每次执行绑定一个随机 nonce。
该机制记录人在受信任维护终端作出的确认，不提供远程身份认证或抗本机管理员
篡改保证。保留脚本或审批授权不等于已经审阅当前清单。

确认后，在仍持有 CAS 排他锁和数据库写入保留时，先检查备份容量，再独占创建
新备份目录。使用独立只读 SQLite 连接的 online backup API 保存数据库；另建
`restore-verified.sqlite` 做恢复往返，并比较所有有类型行、完整性与外键。
全部数据库所需 CAS 字节逐个复制到独立 inode，并重新读取核验大小和摘要，
原始 canonical manifest 同时保存在备份目录。复制不是硬链接，也不覆盖旧备份。
全部核验成功后，精确删除及不可变 receipt 在单个数据库事务中提交。

CAS unlink 发生在数据库提交之后，数据库与文件系统不具备跨资源原子性。
若中断或某文件删除失败，命令返回非零及 `database_committed`、receipt、
`deleted`/`already_absent`/`failed_key`；应保留标准输出与退出码。提交结果不确定时，
只能先用只读 receipt 查询确认，不能重新执行 apply，也不能自动覆盖源库恢复：

```sh
art-ai-retention receipt --database /data/art-ai.db --sha256 REVIEWED_SHA256
art-ai-retention cleanup --database /data/art-ai.db --storage /data/cas --catalog /ops/retention-schema.json --manifest /ops/review/manifest.json --sha256 REVIEWED_SHA256
```

cleanup 需要新的 `APPROVE cleanup <完整SHA-256>` 人工确认，保存独占的审批记录，
并重新核对原备份数据库 SHA、canonical manifest 及全部备份 CAS 字节（包括
源已删除项），再核对 receipt、提交后全库摘要、所有剩余引用和每个待删文件。
任何备份丢失或损坏都拒绝继续。只有清单内仍
存在且身份与内容一致的无引用文件可以 unlink；已缺失文件单独报告，不能冒充
本轮成功删除。库或引用已变化时停止，需新的维护评审。恢复时在独立位置验证
备份数据库与 CAS 后，另行制定停机替换方案；本命令不会自动回写数据库。

验证范围是本地隔离 SQLite/CAS fixture：WAL 与 DELETE journal、写锁排斥、
备份恢复、引用闭包、清单篡改、路径攻击及部分清理恢复。它不构成真实环境清理
验收或下一次运行授权；现场清单审阅、恢复演练和执行证据应记录在对应 Issue。
