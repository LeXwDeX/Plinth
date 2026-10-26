# 当前 Go 开发栈的离线数据完整性核验

`scripts/check-development-integrity.py` 只核验操作员提供的独立、静止的 Go SQLite / CAS 私有副本。它不运行 ETL、不读取运行环境文件、不下载上游产物、不修改公开任务，不生成正式迁移就绪或源损失批准。唯一部署环境的功能验收与正式迁移是两个不同合同；不得把开发栈 identity 当作 formal inventory。

## 捕获与身份

先按 `dev_ops.md` 核对 Plinth 实际 Compose 项目、服务、容器、OCI revision、镜像 ID 和挂载，禁止打印完整 inspect / 环境变量。确认没有活跃 Task 或其他 Plinth 写进程，并协调测试窗口。只停止核实过的 Plinth 容器；在新的 `0700` 私有目录中用 SQLite backup 取得独立数据库，复制完整原生 CAS 树，随后立即恢复同一容器并核对健康、revision。任何复制失败也必须恢复服务；保留失败证据，不将部分副本标记完整。不得触碰共享主机其他业务、运行数据库或已有备份。

在私有副本封存前，将 SQLite backup 的 journal mode 转为 `DELETE`，避免原生 `mode=ro` checker 在 WAL 模式副本旁创建 SHM/WAL；这是私有副本的封存操作，必须记录 `private_database_normalization`，不是运行库迁移。副本文件保持 `0600`、目录 `0700`；可执行 checker 为 `0700`。从同一已核实容器提取已有 `/usr/local/bin/art-ai-storage`，不改运行容器。

私有 `identity.json` 至少包含下列字段。时间、停止/恢复记录、挂载和封存记录应同时保留。身份内容来自操作员实际采集；检查器只做格式和摘要绑定，不能认证采集者、证明其确实停止了全部写入方。

```json
{
  "target": "development",
  "project": "<observed Compose project>",
  "service": "<observed service>",
  "container_id": "<full 64-hex container ID>",
  "image_id": "sha256:<full image digest>",
  "revision": "<full 40-hex deployed revision>",
  "consistency": "stopped_art_ai_writers",
  "snapshot_complete": true,
  "private_database_normalization": "sqlite_backup_journal_mode_delete"
}
```

不要为了匹配历史文档而复制无关 legacy 目录。当前 Go DB 的旧表是否存在、ETL 标记和历史隔离记录计数，决定旧 Next H2/H5/H6 假设是否适用于本次持久记录；若存在旧记录则保持 `unknown`，另做针对性诊断。没有旧记录并不证明旧挂载的内容完整，也不批准移除挂载。

## 执行

```bash
python3 scripts/check-development-integrity.py \
  --database <private-art-ai.db> \
  --storage <private-storage-go> \
  --identity <private-identity.json> \
  --storage-checker <same-revision-art-ai-storage> \
  --report <new-private-report.json>
```

输出必须位于 CAS 之外，不能覆盖输入或既有文件。DB / identity 文件及其直接父目录不得对 group/world 开放；原生 checker 可执行文件权限不套此限制。SQLite WAL/SHM/journal、输入路径符号链接、CAS 符号链接或特殊文件、输入前后变化均拒绝可信报告。检查器使用 immutable/query-only SQLite；唯一可能创建的 CAS 文件是原生 checker 的 `.maintenance.lock`，仅发生在私有副本，不纳入数据清单。运行库和运行 CAS 从不传给此命令。

## 核验范围与结论

- SQLite `integrity_check`、外键检查及独立表 COUNT 对照实际加载行数；Task/Attempt/会话 owner、输入资产 owner、PreparedInput 引用 owner 和字节、恢复回执 owner/来源关系。
- 会话删除路径不删除 immutable PreparedInput，且其 session 列无外键；已不存在的 PreparedInput 会话只以 `prepared_session_absent` 观察计数保留，不将其误判为跨 owner。存在会话但 owner 不符仍拒绝。观察计数不证明历史删除原因。
- 原生 `art-ai-storage --check` 全量检查输出、输入、物化 claim 和恢复回执；原生 Go 校验器保留回执摘要的唯一实现。Python 独立枚举同样引用并比较计数，检查全 CAS 树实际大小、SHA-256、key 分片/摘要、缺失或损坏文件。孤立 CAS 对象只计数，不删除，也不等同损坏。
- 每个 `SUCCEEDED` Task 使用自己的冻结 `offering_json`，不拿当前 Catalog 替代历史合同。必需输出按 role、精确 variant/前缀、minimum 和声明顺序检查，一个资产不能重复满足两个槽位；只对分配的最低数量核对 required 标记；另对全部已持久化产物（包括条件必需、额外及非必需产物）按精确 variant 优先、最长前缀次之、role 通配最后的冻结声明检查非空、媒体类型、大小上限及精确 delivery profile。条件必需 `minimum=0` 和非阻断 background/on-demand 保留原语义。所有资产的字节仍须核对。
- `ready` 资源必须有匹配的本地资产；成功任务已经持久化的必需资源槽若非 `ready` 则拒绝，旧任务缺少整个资源 map 不凭空补造槽位；非成功历史不承诺完整成功产物，非必需 `incomplete` / `failed` / `denied` / `abandoned` 单独计数，不伪造成功，也不自动修复。
- 报告只输出固定状态、计数、身份摘要、已提供的部署 revision / 镜像 / 容器身份与 DB、CAS 清单、冻结 Offering、生成器和原生 checker 摘要；不打印 owner、Task ID、URL、文件名、提示词或业务内容。返回码 `0` 为所述检查通过，`2` 为已生成 STOP 报告，`1` 为输入/执行无效，不能接受报告。

通过只说明该私有快照在列出的检查范围内一致。它不重新验证 GLB/ZIP 等容器的全部语义、上游原始字节、鉴权 HTTP 交付或 UI 功能，也不证明正式迁移就绪；这些仍由对应任务验收覆盖。报告必须随确切捕获 revision 和时间解释，后续新增任务或发布不自动继承旧快照结论。

## 回归

`python3 -m unittest discover -s scripts/tests -p test_development_integrity.py` 覆盖损坏/缺失字节、必需槽位及其单次分配、条件/非必需产物、失败历史、跨 owner/Attempt、READY 缺少本地证据、legacy 条件、符号链接与输入变化。已有 Go checker 的 receipt/CAS fixture 继续作为复用端口的证据；适用最终门禁遵循 `dev_test.md` §2。
