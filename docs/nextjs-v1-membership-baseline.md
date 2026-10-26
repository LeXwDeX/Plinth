# Next.js v1 会员基线对照

本文记录 Go 迁移版**曾经缺失、现已补齐**的会员与安全能力，并给出 v1
（Next.js 版）原始行为的取证结论。用途有两个：

1. 迁移期判断"这个缺口是不是真的存在"时的唯一事实来源，而不是靠印象。
2. 回归时确认 Go 版行为是对齐 v1 的有意增强，而非无意语义漂移。

## 取证方法与边界

- v1 代码以独立 worktree 检出：分支 `review/nextjs-v1-membership`，提交 `6521c63`
  （`feat(prod): 增加离线生产迁移盘点入口`）。
- 结论只依据**该提交的实际源码**（`logAudit` 调用点、`route.ts` 文件清单、限流/管理员
  保护相关标识符的检索结果），不依据 v1 文档或提交说明。
- v1 已停止演进，本文不描述 v1 的目标形态。Go 版是现行实现；v1 在此只作为
  "迁移前是什么样"的参照。

## 逐项对照

| 能力 | v1 实际行为 | Go 版交付 | 性质 |
|---|---|---|---|
| 登录失败限流 | **无**。全仓检索 `rate limit` / `RateLimit` / `lockout` 无命中 | `loginFailureLimiter` + `LoginFailureStore` 落库（`0029`），存储故障 fail-closed | 新增 |
| 管理读操作审计 | **无**。`admin/audit-logs`、`admin/usage`、`admin/generations` 三处读路由 `logAudit` 命中数均为 0；`admin/users` 仅写路径记审计 | 五个读模块逐请求记审计，动作名由 Schema `read_action`/`resource_type` 声明 | 新增 |
| 最后一个管理员保护 | **无**。检索 `last admin` / `最后的管理员` 无命中 | Schema `guards.minimum_active_administrators` + `ErrLastAdministrator`，降权/停用/删除统一拦截，HTTP 409 | 新增 |
| 自助改密 | **无**。`src/app/api/auth/` 下只有 `login`、`session`、`sso`；带 password 的路由仅 `auth/login` 与 `admin/users*` | `POST /api/auth/password`：验旧口令 → 弱口令拒 → 吊销全部会话 | 新增 |
| 跨用户读归属校验 | 依赖各路由自行判断 | `taskReadScope` + `GetScoped` 统一；跨用户返 404 | 沿用并收紧 |
| 死配置治理 | 存在 `DEV_MOCK_AUTH_BYPASS_USER_STATE` 类注入项 | 删除无消费方的注入项，并加门禁防复发 | 治理 |

## 结论

**上述五项在 v1 全部不存在，是 Go 迁移过程中暴露（而非引入）的缺口。**
迁移版把它们当作"v1 没有所以不算回归"的辩解不成立——登录限流、逐请求审计、
最后管理员保护、自助改密都属于安全与可运维底线，缺失会在生产环境造成
实际损失（撞库无阻、管理员把自己锁在门外、无法独立改密、越权读取无痕迹）。

Go 版的实现一律遵循 `AGENTS.md` 的架构四原则：这些能力的**契约与文案**
（动作名、资源类型、守卫阈值、口令长度、错误文案）全部由
`internal/catalog/default_catalog.json` 声明，由 `internal/app` 装配注入，
不在 handler 或服务里硬编码第二真源。`internal/transport/httpapi/server_integration_test.go`
的测试夹具已与 `app.go` 同源接线，避免新增声明在夹具里静默丢失。

## 尚未闭环的已知缺口

以下项本次未处理，不应被当作已完成：

- 批准同意无 TTL；`ListMessages` 无分页；`InpaintDialog` 仍有硬编码 prompt 与
  `reference_assets`。
- C1 之后读审计接口自身每次被调用都会写入一条 `admin_read_audit_logs` 事件
  （单次请求只增一条，不会失控，但审计表按读取频率增长）。上线前需确认后台审计页
  是按需拉取而非轮询。
- dev-test 上此前用空邮箱建出的**存量账号**仍无法登录。`bebb7e7` 之后建号强制
  邮箱必填，但存量死号不会自动修复，验收前需清理或用合法邮箱重建探针账号。
