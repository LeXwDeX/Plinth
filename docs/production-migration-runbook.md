# 正式生产 Next → Go 停机迁移手册

## 执行前置条件

本手册规定正式迁移的执行门槛，不记录当前迁移进度或生产是否已切换。动态状态以正式迁移 Milestone #4 及原生 Issue #196 的验收记录为准。正式 inventory、基线和目标环境执行证据必须真实采集并核验；合成测试不能代替正式 inventory。

`dev-test` 是测试环境；2026-07-29 在该主机完成的是一次生产同构的全量迁移与切换演练，演练数据、账号、目录、容器名、Webhook、对账数字和源损失批准清单都不能直接用于正式生产。

在下列执行前置条件全部满足、证据已按原生 Issue 记录并由正式责任人批准前，禁止在正式生产执行 `scripts/finalize-go-production.sh`。

Codex 无法访问正式生产服务器。所有正式生产操作都必须以以下两种形式之一
交付给现场操作人：

- 仓库内、已测试、参数显式且失败自动回滚的脚本；
- 本文中与脚本逐步等价、包含预期输出和停止条件的手工命令。

不得要求 Codex 登录服务器，也不得把正式凭据或 `.env` 内容返回。

## 阅读顺序

1. 本文：正式生产迁移的唯一入口、阻断项和执行顺序。
2. `README-go.md`：Go 运行时、ETL 和事务切换机制。
3. `dev_test.md`：代码门禁及部署后验收口径。
4. `dev_ops.md`：dev-test 测试演练记录，仅用于参考，不是正式生产参数真源。
5. `docs/production-etl-design.md`：正式 ETL 的设计、不变量与历史诊断证据。
   触发条件：修改 ETL/对账/manifest 相关代码、准备正式
   迁移工件，或需要判断某缺口是否阻断迁移时读取。
6. `docs/production-asset-gap-diagnosis.md`：资产大规模未固化时的只读诊断
   手册。触发条件：快照演练或正式 ETL 中资产大规模未固化、或跨独立
   资产族同率未固化时读取（见“正式快照副本演练”）；未触发不读。

## 第一步：现场执行只读盘点脚本

把当前 `main` 仓库放到正式生产服务器后，由现场操作人执行：

```bash
cd <正式生产服务器上的仓库目录>

sudo scripts/collect-production-migration-inventory.sh \
  --legacy-root <旧 Next 项目根> \
  --legacy-container <旧 Next 容器全名> \
  --output /root/art-ai-formal-production-inventory.txt
```

脚本只创建输出报告和临时 SQLite 快照，不停止/重启容器，不修改旧数据库或
storage，不读取 `.env`、容器环境变量或业务行内容，也不访问外部网络。报告
权限为 `0600`。把报告内容返回即可，凭据文件不要返回。

如果不执行脚本，手工盘点也必须取得相同信息：主机名与磁盘、旧容器镜像/
运行状态/Compose 标签/端口/挂载、SQLite 一致性快照的 SHA-256、
`quick_check`、外键违规、11 张旧表行数，以及 storage 六个子目录的文件数
和字节数。手工操作同样禁止读取或输出容器环境变量。

取得并核验完整正式 inventory 后，才能生成正式生产专属的配置、验收基线、停机脚本和等价手工命令；在此之前不进入维护窗口。

采集器使用精确分隔的 Docker 模板保留挂载路径原文，并将 SQLite online backup 得到的私有副本转为 DELETE 模式，验证该副本的模式和完整性后计算摘要；不更改活动源库的 journal mode。来源摘要与 `verify-cutover-source.py` 的消费合同一致，包含已提交的 WAL 数据。

离线工件生成器只消费已核验的正式 inventory 和一份由正式责任人审阅的结构化附表；它不连接主机、不读取业务库，也不会从 dev-test 或模板默认值补正式事实。输入 inventory 与附表都必须是权限 `0600` 的普通文件。附表格式为 `art-ai-formal-migration-artifact-supplement-v1`，包含 `inventory_sha256`（完整 inventory 原始字节的 SHA-256）、`review`（审阅人、UTC 时间和审阅证据 SHA-256）以及 `acceptance`（目标输出资产数、正式验收入口、外部 Webhook origin、Go 数据库路径和 runtime.env 路径）。字段缺失、摘要不匹配、路径或 origin 不合法时生成器拒绝继续。审阅证据摘要用于工件追溯，需由现场责任人核实其指向的证据；生成器不验证外部审阅或证据文件本身。

现场操作人应在私有目录准备并审阅该附表，再在离线工作副本中运行：

```bash
umask 077
python3 scripts/generate-production-migration-artifacts.py \
  --inventory /root/art-ai-formal-production-inventory.txt \
  --supplement /root/art-ai-formal-migration-artifact-supplement.json \
  --output-dir /root/art-ai-formal-migration-artifacts
```

生成器要求输出目录此前不存在，不覆盖既有工件。输出目录权限为 `0700`，其中来源基线、验收基线和 manifest 为 `0600`，实例化验收脚本为 `0700`；manifest 记录 inventory、SQLite 快照、附表、验收模板及每个输出文件的摘要。生成后由现场责任人逐项复核并将 inventory 摘要、生成器 revision、manifest 摘要和复核结论记录到 Issue #196。只有真实正式 inventory 和相关验收证据经核验后，才能继续后续执行步骤；合成 fixture 的测试通过不代表正式迁移已验收。

## 正式 inventory 数据合同（执行前必须采集并核验）

以下值不得从 dev-test 推导：

| 项目 | 必须采集的数据 | 核验要求与证据 |
|---|---|---|
| 主机 | SSH 目标、主机指纹、操作账号 | 核对目标身份与主机指纹；记录脱敏 inventory 证据 |
| 流量入口 | 内网 URL、公开域名、端口、Nginx/Webhook 转发 | 绘制真实入口和转发关系；核验正式端点可达性 |
| 旧服务 | Compose 项目、Service、容器全名、启动/停止方式 | 从现场容器元数据核实项目、Service、标签、镜像及状态 |
| 旧数据 | Next 项目根、SQLite 文件、storage 各子目录 | 只读采集路径、挂载与目录 files/bytes；SQLite quick_check 和外键检查 |
| Go 数据 | Go 根、正式数据根、备份根、最小剩余磁盘 | 核实挂载和可用容量；按备份、staging、SQLite 临时文件与回滚余量计算 |
| 外部依赖 | LLM、生图、Matting、Webhook 地址 | 只记录必要 endpoint；在维护窗口前核验连通和路由，不记录密钥 |
| 凭据 | 正式生产 `runtime.env` 来源与权限验证方法 | 只核对文件存在性、属主和权限；不读取或返回内容；凭据文件权限 `0600` |
| 维护窗口 | 开始时间、最大停机时长、回滚截止时间 | 正式责任人书面批准，明确停止与回滚窗口 |
| 验收基线 | 用户、任务、账务、资产、Chat、Canvas 等源数据统计 | 从正式一致性快照采集并保存 SHA；目标侧按相同维度对账 |
| 历史缺失 | 正式生产专属的源损失候选及人工批准人 | 只能在快照完备性、恢复次序和计数门禁闭合后逐项核对/批准；绑定最终停写库 SHA |

凭据和批准清单只保存在正式生产主机的权限 `0600` 文件中，不写入本文、
命令输出、Git 或聊天记录。

若正式生产启用公网资产网关，`PUBLIC_BASE_URL` 指向的地址必须可被上游
Provider 访问（rigging `source_asset` 等回拉场景依赖）；该可达性必须在
进入维护窗口前验证，不得留到停机事务中排查。

## 正式事务脚本约束

以下是正式执行必须满足的参数、基线与安全约束；该节不记录代码改造或测试的进度。相关实现验收见原生 Issue #194 与 #196，其状态只以内部存档记录为准。

1. `scripts/finalize-go-production.sh` 强制显式传入旧容器全名及 Compose
   Project/Service、旧项目根、真实 SQLite/上传/任务缓存/审计附件路径、Go 根、
   Go Compose 项目和正式入口 URL；现场
   参数仍必须来自脱敏盘点报告，不得猜测。正常窗口由事务亲自停止旧容器；
   若旧容器在事务前已经确认停写，必须显式传入 `--legacy-already-stopped`，
   脚本会反向要求容器确实未运行，失败回滚时也不会擅自启动该容器。事务还
   必须显式传入 `--minimum-free-kib`；该值按正式备份、staging、SQLite 临时
   文件和回滚余量计算，不能使用测试环境默认值。
   `--expected-hostname` 校验现场执行主机的 `hostname`，
   `--expected-docker-id` 独立校验 `docker info --format '{{ .ID }}'`；主机名
   不代表 Docker daemon 身份。两者必须来自已核验的现场记录。
   `--go-bind-ip`、`--go-host-port` 和 `--preflight-port` 也必须显式给出；
   IP 必须合法，两端口须为不同的 1–65535 整数。旧服务、Go 服务与
   `<Go项目>_preflight` 项目不能重名。正式 wrapper 不继承测试端口或
   外部 `GO_PREFLIGHT_PROJECT`；普通测试栈发布的 preflight 行为不变。
2. `scripts/post-cutover-acceptance.sh` 强制读取权限
   `0600` 的正式基线 JSON，格式为：

   ```json
   {
     "users": 0,
     "generations": 0,
      "usage_tasks": 0,
      "usage_credits": 0,
      "audit_minimum": 0,
      "output_assets": 0,
      "input_assets": 0,
      "chat_sessions": 0,
      "chat_messages": 0,
      "canvas_projects": 0,
      "canvas_nodes": 0,
      "external_webhook_url": "https://formal-webhook.example.test"
   }
   ```

   各数值必须由正式快照盘点生成，示例中的 `0` 不是可用生产基线。
3. 底层 `scripts/cutover-go-production.sh` 自 #249 起不带任何主机路径默认值，
   `GO_PROD_ROOT` 与 4 个 `GO_LEGACY_*_DIR` 必须由调用方给出；正式
   事务只能由已显式参数化的 `finalize-go-production.sh` 调用。
4. dev-test 的 `final-source-loss-approved.json` 只绑定测试数据库 SHA-256
   和测试问题集；正式生产必须从正式停写快照重新生成候选、人工核对和批准。
5. 正式生产基线文件、运行环境和源损失批准清单均须为普通文件、权限
   `0600`，且不得提交。

### 来源快照基线与拒绝条件

正式事务还必须显式传入 `--source-baseline <0600 普通 JSON 文件>`。最低消费合同是：

```json
{
  "format": "art-ai-production-source-baseline-v1",
  "inventory_sha256": "<正式 inventory 的 64 位小写 SHA-256>",
  "database_snapshot_sha256": "<SQLite 一致性备份转 DELETE 后的 64 位小写 SHA-256>"
}
```

这份来源基线与 `--acceptance-baseline` 的目标数量基线不同，二者都由现场核验的
正式证据派生；来源基线的机械生成与 inventory 真实性验证归 #196。当前消费端
只验证 `inventory_sha256` 的格式，**不证明该摘要对应的 inventory 真实性**，
也不替代 11 表与六个 storage 目录的完整性验收。不得把示例占位符或合成基线用于正式切换。

`scripts/verify-cutover-source.py` 在停服务、创建事务目录前，用与采集器及备份脚本相同的
`sqlite3` 可执行文件，以 `-readonly` 打开源库并执行 online backup 到私有临时目录，
再把临时库切为 DELETE、执行 `quick_check` 并核对完整文件摘要；因此包含
已提交 WAL 页，不能用活动主库文件的直接 SHA 替代。最终停写备份由既有 backup
脚本生成，事务再以 `--snapshot` 校验该独立 DELETE 文件的实际摘要与完整性。
活动源核验的三条 SQLite 命令共用 60 秒超时；CLI 缺失、失败、超时或完整性异常均拒绝。
采集到切换期间应保持 SQLite CLI 版本一致；不同版本可能改变备份头中的 writer version，
即使数据相同也会改变完整文件摘要，不能屏蔽头部或改用逻辑行摘要放行。
不得重新备份该文件后比较：WAL 转 DELETE 后再次 backup 可能改变 SQLite 头部。
活动源在基线之后变化即拒绝；若变化发生在预检期间，则最终备份检查拒绝并恢复旧服务。
现场应重新核实基线，不能自动更新基线来放行。

就绪证明只接受固定八字段：`format`、`image_tag`、`database_sha256`、
`report_name`、`report_sha256`、`manifest_name`、`manifest_sha256`、`created_at_utc`。
未知、重复或缺失字段均拒绝，旧六字段证明不得用于正式接管。manifest 必须与报告同名
加 `.manifest.json`，其实际摘要须匹配；runner 与 takeover 共用
`scripts/etl-completeness-filter.jq` 校验完整域集合、源/加载/目标计数和积分等不变量。
不能只凭散文中的“对账结论: 通过”接管。

正式 wrapper 调用 takeover 时追加 `--finalize-legacy-id <已冻结并停止的原容器ID>`。
该模式只允许 `--no-build takeover`：子脚本先核对容器名字仍对应同一 ID，再按 ID
核对 Compose 标签与停止状态；名字已被复用、原 ID 不存在或重新运行都拒绝，
不改绑、不再次停止容器，也不启动 Go。子脚本不拥有旧服务回滚权限；它只清理
隔离 preflight，目录回位与原 ID 恢复统一由 finalize 完成，避免在目录恢复前启动旧服务。
独立直接调用 takeover 时仍自行负责旧服务恢复，但也按冻结 ID 启动，且 Go 停止失败
须报告回滚未完成并阻止旧服务启动。

生产健康 URL 从实际 `GO_WEB_BIND_IP` 推导：`0.0.0.0` 使用 `127.0.0.1`，
IPv6 通配 `::` 使用 `[::1]`，其他明确 IPv4/IPv6 使用绑定地址本身（IPv6 加括号），
端口为显式 Go 端口。preflight 仍使用它自己的 IPv4 回环地址和隔离端口。


故障路径和无 dev-test 默认值的回归验收规格归 Issue #194/#196 管理；运行本手册前仍需按实际部署 revision 核验相关门禁与脚本契约。

### read_gap 重跑与人工升级

`read_gap = source_count - loaded_count` 保留正负号；正值表示少读，负值表示多读。
任何非零值都使域及整体 manifest 失败，不能由 `skipped_count`、已批准源损失、目标
已有行或“对账结论”文字抵消。ETL 失败可能已提交部分目标数据，CLI 会明确报告这一点。

只有同时满足以下条件，才可在**隔离 staging** 上进行一次同输入幂等重跑：

1. 失败被定位为已修复的瞬时执行/读取故障；任务源码 revision、旧 SQLite 快照的
   完整 SHA-256、storage 快照清单及正式 baseline 均未变化。
2. 仍使用原隔离 target DB 与 CAS 目录；确认其属于本次迁移 staging，且首次运行
   没有提升、接管或被其他任务写入。重跑完成后逐域核对 manifest，所有
   `read_gap == 0`、域状态为 `pass` 且整体 `failure_count == 0`。
3. 输出到新的 report、manifest 和候选文件路径；不覆盖已有证据，不同时传入候选与
   批准文件，也不借重跑自动批准或重绑定源损失。

若故障需要更换源码 revision，应从同一冻结快照和空的隔离目标重新演练，并重做完整
对账；不得把不同 revision 的部分结果混在一个目标中。出现以下任一情况必须停止并交
正式迁移负责人判断：read_gap 重跑仍非零或方向/数量变化无法解释；source SHA、快照
清单、baseline 或部署 SHA 变化；目标身份或写入归属不确定；数据库校验/外键错误、
重复键冲突、manifest 缺域/重复域、SHA 不符、必要资产 unavailable；错误含义不明或
缺少必要观测证据。保留 staging、报告、manifest、脱敏日志和输入摘要供调查；禁止重试
到绿、换 target 避开问题、修改 baseline、把 read_gap 记成源损失，或因维护窗口压力
继续提升。

### 切换后分页任务抽样与资产字节核对

部署后只使用经正式授权的只读身份及实际部署的同源 API。任务读取以
`GET /api/v1/tasks?limit=<1..100>&cursor=<opaque>` 为分页合同：未传 `limit` 时服务端
默认 20，超过 100 会被限制为 100；公开任务页响应只有 `tasks` 和 `next_cursor`，
没有 `total` 字段，空游标表示末页。每页保持相同筛选条件（全量核对时不传
`capability`、`state` 或
`session_id`），依次消费所有页面，不拼接、修改或复用到其他身份/查询的游标；记录页数、
每页行数、末页位置和去重后的任务总数。以独立、经授权且与 API 相同 owner/筛选范围的
公开任务集合基线核对去重数；该基线来自另行核验的只读一致性数据库快照，按相同 owner
范围与公开查询的 tombstone 可见性规则独立计数，不从本次 API 页结果倒推。分页响应本身
不提供总数证明。应在任务创建受控且集合稳定的
验收窗口完成遍历；若能确认页面间有新增/删除，当前游标遍历作废，先稳定写入并重做。
跨 owner 全量读取要求持有
`tasks.read.any` 的已授权只读管理员身份；若仅能读取部分 owner，不能证明全库任务覆盖，
无法取得经授权的完整范围时 STOP。

ETL manifest 的 `generation_tasks.target_count` 统计的是带
`etl_import_items(domain='generation_task')` 标记的目标 Task；ETL reconciliation 不按
公开可见性排除 tombstone，也不计没有该 ETL 标记的原生 Task。公开任务列表则包含可见的
ETL 与原生 Task，并排除 tombstone。因此，不得默认将 API 去重数直接与
`generation_tasks.target_count` 比较。只有另经只读证据确认目标集合纯由本批 ETL 导入组成、
没有 tombstone 或额外原生 Task，且两边 owner 和筛选范围完全相同，才可直接比较；否则
分别从同一经授权只读一致性数据库快照按精确 ETL 标记 join 与公开可见规则建立集合基线，
记录各自的查询范围和快照 SHA，再与 ETL manifest 和 API 分页结果分别核对。不得通过
改写任一基线来消除差异。

在完整分页结果上按固定规则挑选可复算样本：令 `N` 为去重后的任务数，
`K = min(N, max(100, ceil(N / 100)))`；对每个任务计算
`SHA256(UTF8("art-ai-etl-observation-v1" + NUL + task_id))`，按摘要字节升序、再按
`task_id` 升序取前 K 个。再补入样本中尚未覆盖的每一种公开任务状态、Offering 和
必需资产格式各一项（按同一摘要排序取首项）。报告规则版本、N、K、实际样本数和
样本覆盖类别；外发报告以任务 ID 的 SHA-256 代替原 ID。

对样本任务的每个 `required` 资产，通过任务投影返回的同源鉴权路由
`GET /api/v1/tasks/{taskID}/assets/{assetID}` 下载完整字节；
不使用上游 URL、签名 URL、浏览器缓存或未鉴权地址。计算下载字节数与完整 SHA-256，
分别与任务投影中的 `size` / `sha256` 比较，并核对 HTTP 成功状态。只记录命中的
数量/摘要和结果，不保存资产内容。不得将 API 返回的摘要字段自身当作字节核验结果。

观察报告只允许包含 UTC 时刻、已核实部署完整 SHA、HTTP 方法和路由模板、页数/计数、
样本规则版本、经摘要化的任务标识、状态类别、资产角色/格式、HTTP 状态、公开问题
代码、预期与实际字节数及 SHA-256 匹配结论。移除 Cookie、Authorization、所有请求
查询值、上游地址/remote ID、存储路径、原始响应体、原始异常和个人信息；报告文件
权限为 `0600`。保留足以定位问题的摘要，不复制业务载荷。

以下情况立即 STOP，保留现场且不重试计费任务：分页游标重复/不前进、任务 ID 重复、
去重数量与同范围独立公开集合基线不符、ETL/公开集合不能各自建立可信基线、权限不足、
任务或必需资产缺失、非成功下载、大小或 SHA-256
不匹配、任务仍处于非终态、部署完整 SHA 无法核实、响应含未定义的错误类别或脱敏后
仍可能泄露敏感字段。确认差异来自数据事实后再按各自 Issue 与源损失审批前置条件处理；
浏览器显示、服务健康或 Provider 成功状态不能解除 STOP。

## 执行阶段

### 1. 只读盘点

- 记录旧容器、镜像、Compose 标签、端口、挂载和 Nginx/Webhook 拓扑。
- 对旧 SQLite 执行完整性与外键检查，统计所有业务表。
- 统计 storage 文件数、字节数、不可读/悬空文件和可用磁盘。
- 生成正式生产验收基线；本阶段不得停止服务或写数据库。

### 2. 正式快照副本演练

- 创建一致性数据库与完整 storage 备份。
- 在与生产隔离的目录、端口、Compose 项目和网络中执行 ETL。
- 对所有域严格对账，并遍历全部任务页、资产下载和管理统计。
- 若资产出现大规模未固化，或输入资产与任务产物等独立资产族同率
  未固化：先按 `docs/production-asset-gap-diagnosis.md` 完成只读诊断并
  证明快照完备性，再重新演练；缺席于隔离快照不等于缺席于正式生产。
- 需要机器核对 D1–D4/C1–C6 时，使用 [离线 verify-snapshot 合同](production-asset-gap-diagnosis.md#51-离线核对命令与证据信任边界)，仅向其提供另行授权的白名单采集证据和显式独立树。缺 mount、可写层、scope、冻结/epoch 声明或未解释差异均 STOP。supported 只说明输入证据与树自洽，不认证正式现场、不证明原子源快照、不代替 #196 的 inventory 来源核实，亦不产生迁移就绪或源损失批准。
- 快照完备性未证明前，禁止源损失审批，也禁止把批量缺失资产当作
  源损失生成候选。只有诊断确认“本地应有却无”的残余，才按
  `docs/production-etl-design.md` 的审批流程生成正式生产专属候选清单，
  人工批准后重新演练。
- 至少完成一次“故意验收失败并自动恢复旧服务”的回滚演练。

### 3. 构建唯一发布物

- 代码必须是已推送且工作区干净的 `origin/main`。
- 完整提交 SHA 写入 OCI `org.opencontainers.image.revision`。
- 同一镜像完成隔离预检、最终 ETL、Go 启动和验收，期间不得重新构建。
- 完成 `dev_test.md` §2 的前端与 Go 门禁（本地与 CI）。

### 4. 停机事务

由改造并验收后的 `scripts/finalize-go-production.sh` 统一执行：

1. 同镜像隔离预检；
2. 精确验证并停止旧 Next 容器，冻结写入；
3. 创建最终 SQLite 与完整 storage 备份并校验；
4. 从最终停写快照向全新 staging 执行严格 ETL；
5. 对账并生成绑定正式镜像、数据库和报告 SHA-256 的就绪证明；
6. 归档旧数据、提升 Go 数据，由独立 Go Compose 项目接管原流量入口；
7. 执行正式生产专属的无扣费验收；
8. 全部通过才提交。失败回滚只撤销已实际成功的逐项归档/提升；ETL 失败保留
   staging，提升后失败把新数据移到 `failed-new`，将 `previous` 中旧目录逐项回位。
   只启动事务亲自停止的原容器 ID，不按可能已被复用的容器名启动。原本已停止的
   容器保持停止。若 Go 停止或目录回位失败，报告“回滚未完成”，保留两套数据与
   全部备份，禁止启动旧服务；现场须先处理该故障，不能把中间状态视作成功恢复。

全链路禁止 `--remove-orphans`，旧容器、旧数据、备份、失败 staging 和
回滚镜像均保留，直到观察期结束并另行批准清理。

## 完成口径

- 正式入口返回 Go runtime，OCI revision 等于 `origin/main`。
- Go 单容器健康、0 重启，旧 Next 已停止但未删除。
- SQLite `quick_check=ok`、外键违规 0、全部版本化 migration 校验通过。
- 正式基线的用户、任务、Attempt、账务、资产、Chat、Canvas 等逐项对账。
- 全部任务分页、历史资产 SHA-256、登录/Session、Provider、Webhook 和
  管理接口通过；不执行未授权的计费生成。
- 备份可读、回滚命令已验证、切换记录和验收报告落盘。
