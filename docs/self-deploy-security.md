# 自部署安全配置

本文说明把 Plinth 暴露到本机之外时必须做的配置，以及 1.0 安全基线（内部存档 #256）各缺口的处理或接受理由。所有设置都是 Product Schema `application.runtime.configuration.settings` 声明的运行设置，经环境变量（`.env` / Compose `env_file`）注入；默认值见 `internal/catalog/default_catalog.json`。

## 1. 部署前必做

| 项 | 设置 | 要求 |
|---|---|---|
| 会话密钥 | `SESSION_SECRET` | 至少 32 字符的随机值，例如 `openssl rand -base64 48`。泄露即可伪造任意会话，更换后所有会话失效。`docker-compose.yml` 在它为空时拒绝启动，服务在不足 32 字符时拒绝启动；`.env.example` 留空，不提供可被原样带进部署的占位值 |
| 管理员 | `ADMIN_USERNAME` / `ADMIN_PASSWORD` | 只在管理员**不存在**时创建；密码长度不得少于 Schema `password_minimum_characters`，建议 20 位以上随机值（如 `openssl rand -base64 24`）。`docker-compose.yml` 要求它非空，所以它留在 `.env` 中；管理员已存在时它不再生效，在产品里改过的密码不会被覆盖。不用 Compose 运行时，首次登录后可从环境中删除 |
| 重置管理员密码 | `ADMIN_PASSWORD_RESET=true` | 默认 `false`。设为 `true` 后，下次启动会把已存在管理员的密码改成 `ADMIN_PASSWORD`，并像产品内改密一样吊销该管理员的全部已登录会话（密码与当前相同时不改也不吊销）；改完立即改回 `false`，并执行 `docker compose up -d --wait` 让容器读取新配置；只编辑 `.env` 或执行 `restart` 不会更新已有容器环境，否则每次重启都会覆盖在产品里修改过的密码。启动时创建或重置管理员都会写 INFO 日志「管理员初始化」（`action`、`username`、`user_id`、`sessions_revoked`）并记入管理后台审计（`bootstrap_admin_created` / `bootstrap_admin_password_reset`），均不含密码；未变化时只记日志 |
| Mock 登录 | `DEV_MOCK_AUTH=false` | `APP_ENV=production` 时开启 Mock 登录会拒绝启动；`docker-compose.yml` 固定 `APP_ENV=production` 与 `AUTH_MODE=session` |
| 监听地址 | `ART_AI_BIND_IP` / `ART_AI_PORT` | Compose 默认只绑定 `127.0.0.1:13333`。改成 `0.0.0.0` 或局域网地址前先完成本节其他项，对外服务放在 HTTPS 反向代理之后 |
| 资产签名 | `PUBLIC_BASE_URL` / `ASSET_SIGNING_KEY` | 只在上游 Provider 需要按 URL 回拉本服务资产时配置，两者须同时设置；生产环境要求 `https` 地址，密钥至少 32 字节（`openssl rand -base64 48`）。默认留空 |
| Provider 地址 | `<ID>_BASE_URL` | 每个启用的 Provider 只读自己的地址变量，不借用 Agent 的 `LLM_BASE_URL`，key 不会发往其他连接的地址。`GEMINI_BASE_URL` 没有默认值，设置 `GEMINI_API_KEY` 时必须同时设置，否则 Gemini 不启用；所有模型经同一网关时把各地址变量都显式设为网关地址。见 [Provider 配置](provider-configuration.md) |
| 运行环境文件 | `.env` | 权限 `0600`，不进 Git，不写进镜像 |

旧版本的 `SSO_*` 设置已退役；保留它们不会阻止启动，也不会启用 SSO。启动日志只列出已设置的旧变量名，请从 `.env` 中移除。账号密码及历史数据不因此改变。

## 2. HTTPS 与 Cookie

服务本身只监听 HTTP，HTTPS 由反向代理（nginx、Caddy、Traefik 等）终止。

- 把 `NEXT_PUBLIC_APP_URL` 设为对外的 `https://` 地址。Schema `cookie.secure_mode` 默认 `auto`：公开地址以 `https` 开头时会话 Cookie 带 `Secure`，否则不带（纯内网 HTTP 也能登录）。
- 会话 Cookie 始终 `HttpOnly`、`SameSite=Lax`，可在数据库撤销。
- 状态变更请求按 `Origin` / `Sec-Fetch-Site` 做同站校验；代理必须原样转发 `Host` 与 `Origin`。
- 登录（密码、受运行配置约束的 Mock）与登出响应在 HTTPS（含反代传来的 `X-Forwarded-Proto: https`，前提是该代理在 `TRUSTED_PROXY_CIDRS` 内）或 localhost/回环 Host 下带 `Clear-Site-Data: "cache"`，让浏览器丢掉缓存的私有缩略图。用 IP 地址访问的纯 HTTP 部署不发送该头（浏览器会报错且不执行，#134），改由前端在主动登出后清空会话存储和账号相关的本地草稿；缩略图响应带 `Vary: Cookie, Authorization`，登出或换账号后浏览器不会复用旧缓存。

## 3. 反向代理与真实客户端地址

登录失败节流和 API 限流按客户端 IP 计数。在代理之后，TCP 对端总是代理自己，所以需要声明可信代理：

```
TRUSTED_PROXY_CIDRS=172.18.0.1,10.0.0.5
```

Compose 部署里反向代理若运行在宿主机，容器看到的对端是 Docker 网桥网关（`docker network inspect art-ai_default` 中的 `Gateway`）；代理在另一个容器里时写该容器的地址。

- 逗号分隔的 CIDR 或单个地址（视为 /32、/128）。默认为空：任何 `X-Forwarded-For` 都被忽略，客户端就是 TCP 对端。
- 只有对端落在这些网段内时才读取 `X-Forwarded-For`，从右往左取第一个不属于可信代理的地址（条目带端口如 `1.2.3.4:5555`、`[2001:db8::1]:5555` 时去掉端口）；更左侧的条目可被客户端伪造，一律不用。
- 代理需要**追加**（nginx `$proxy_add_x_forwarded_for`）而不是透传客户端给的值。只列真正的代理地址，不要写 `0.0.0.0/0`。

## 4. 限流

| 范围 | 设置 | 默认 |
|---|---|---|
| 登录失败 | Schema `authentication` 登录节流（按客户端 IP + 用户名，持久化） | 见 Schema |
| 通用 API | Schema `runtime.http.api_rate_limit` | `/api/` 下每客户端每分钟 1200 次，超出返回 429 `rate_limited` 与 `Retry-After: 60` |
| 前端错误上报 | Schema `runtime.http.client_error_report` | 已登录每用户每分钟 30 条、请求体 16 KiB；未登录（`anonymous_*`）每客户端每分钟 5 条、全体每分钟 60 条、请求体 8 KiB、文本截断 1000 字符 |
| 并发任务 | Schema 任务并发上限 | 每用户 8 个 |

通用 API 限流是单进程内存固定窗口，用来挡住失控脚本和刷接口，不代替代理层的 DDoS 防护。“每客户端”对 IPv4 是单个地址，对 IPv6 是同一 /64 前缀（一个 IPv6 用户通常拥有整个 /64，可随意换地址）；登录失败节流同样按 /64 计数。`requests_per_minute` 设为 0 可关闭。

## 5. 已核查并接受的项

- **上传允许 `application/octet-stream`**：浏览器常把 `.glb` 报成 octet-stream，所以保留。安全不靠上传声明的类型：只有 Schema `inline_media_types` 中的图片按原类型内联下发，其余一律以 `attachment` + `application/octet-stream` + `Content-Security-Policy: sandbox` + `nosniff` 下发，不能借上传执行脚本；生成产物（来自上游 Provider，同样不可信）按同一规则下发：预览用图片内联，模型、MTL、报告等其他文件以 `attachment` 和安全文件名下发，所有产物响应（含一键 ZIP、兼容下载、签名回拉）都带 `Content-Security-Policy: sandbox` 与 `nosniff`（#315）；模型输入另由 `static-glb-v1` 解析器按结构和资源预算校验。单文件上限 50 MiB（`input_assets.upload_max_bytes`，#274 起；画板按交付尺寸无损导出的 PNG 需要它）；模型输入另受 `static-glb-v1` 的 10 MiB 上限约束，各 Offering 参数的 `max_bytes` 在提交时继续校验。
- **请求体上限**：通用 JSON 与 multipart 上限约 101 MiB（`runtime.http.json_body_max_bytes` / `multipart_body_max_bytes`），因为兼容接口仍接受内联 base64 参考图。体积小的接口各自用更紧的上限：登录 16 KiB（`auth_json_body_max_bytes`）、错误上报 16 KiB（未登录 8 KiB）、Agent 配置与个人 Skill 4 MiB。两项都可在 Schema 中下调。
- **可配置的上游 base URL（LLM、抠图等）**：只来自运维写的运行环境，不来自请求，因此不是用户可控的 SSRF 面。用户提交的参考图 URL 由 Provider 结果下载主机白名单和资产隔离检查约束。
- **未登录的前端错误上报**（#317）：登录页和会话过期后的页面也要能把前端错误写进服务端日志，所以 `POST runtime.http.routes.client_errors` 接受未登录请求，但限额更严，见 §4。滥用面与对策：
  - *刷日志*：每客户端（IPv4 单地址、IPv6 /64，经可信代理解析，见 §3）每分钟 5 条，另有全体未登录每分钟 60 条的总量上限，换地址也不能超过；`message` 与 `stack` 各截断到 1000 字符，一条上报连同访问日志最多约 8 KiB，打满总量时最多约 0.5 MiB/分钟；Compose 的 Docker 日志按 `50m × 5` 滚动，不会写满磁盘，但会更快挤掉旧日志（另配 `LOG_DIR` 时文件日志按自身的大小与天数滚动）。总量打满时，后来的未登录上报（含真实用户的）在该分钟内返回 429，已登录上报不受影响。
  - *伪造内容*：未登录上报的内容由任何人随意填写，只能当线索，不能当证据；日志标 `anonymous=true`、无 `owner_id`，便于区分和过滤。
  - *敏感信息*：只接受 `kind`、`message`、`stack`、`page`、`revision`、`request_id` 六个字段，其他字段一律 400；`page` 只保留路径，去掉查询串和片段（登录跳转目标或一次性令牌不进日志）；不记录 Cookie 或其他请求头（只留截断的 User-Agent）；所有属性照常走日志脱敏。
  - *跨站提交*：照常经同站 Origin / `Sec-Fetch-Site` 校验（SEC-03），浏览器里的跨站页面不能代为上报。
  - 不需要时把 `anonymous_global_per_minute` 设为 0，路由恢复为只接受已登录上报。
- **上游错误体写日志**：日志 handler 对所有属性统一脱敏（URL 查询串、`Bearer` 值、`key=value` 形式的密钥字段、名字像密钥的属性），见 `internal/logging`；上游错误摘录另有长度上限。

## 6. Git 历史

2026-10-19 对内网仓库全部 ref 做过一次密钥扫描（结果记录在 #256）。仓库历史曾包含一把真实 Provider Key；对外发布只用干净快照，不推送内网历史，且该 Key 须在供应商控制台轮换。自部署者不要从内网完整历史构建公开仓库。
