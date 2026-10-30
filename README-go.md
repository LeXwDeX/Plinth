> 架构与前后端职责统一见 [架构总览](docs/architecture.md)。本文负责运行上手与运维入口；带日期的状态/迁移验收是历史快照。

# Plinth 纯 Go 重构

这是一个不依赖 Node.js/Next.js/Prisma/CGO 的独立 Go 运行时。Node/pnpm
只存在于 Docker 多阶段构建的前端编译阶段；最终镜像由一个 Go 进程统一提供
React 静态应用、SPA 路由、静态资源与全部 HTTP API。它不是 TypeScript
文件的逐行移植，而是围绕稳定 Task、可追溯 Attempt、数据驱动 Offering
和持久资产重建的生成平台内核。

## 当前交付

- 统一 Product Schema Catalog：全部 Provider×Model×Capability Offering（数量以 `GET /api/v1/offerings` 为准）、运行策略、权限和展示内容只有一个真源。
- 统一 `202 Accepted` 的 Task API；同步、异步 Provider 对调用方透明。
- SQLite WAL 持久化、CAS 版本、持久租约、Attempt 历史和 append-only Event。
- 输入资产上传、用户隔离、媒体类型/数量/大小校验。
- 输出 Asset 可直接成为后续任务输入；需要 URL 的上游使用短时 HMAC 能力 URL。
- 上游 Claim 到内容寻址 Blob 的原子落盘；必需资产完整后才允许成功。
- OpenAI Images 文生图/图生图真实适配器。
- Gemini Images 文生图/图生图真实适配器，参考图与结果均使用受限内联数据。
- Meshy 文生图、图生图、文生 3D、图生 3D、Remesh、Retexture、Rigging 和动画真实适配器。
- Tripo 文生 3D/图生 3D 真实适配器，图生 3D 输入先上传为上游 file token。
- Matting 批量背景去除真实适配器，支持异步批次和带鉴权结果下载。
- Mock Adapter、HTTP API、Bearer/开发身份模式和容器镜像。
- 原 React 页面与组件经 Vite 静态编译后由 Go Web Host 直接托管，UI/UE
  不变；运行时不包含 Next Server、Node 进程或反向代理。
- 页面路由、静态资源前缀、登录重定向、导航、布局模式和展示文本都从同一
  Product Schema 投影；浏览器直接调用原公开 API 路径，不经过 `/api/go`
  等候选代理前缀。
- 核心 Task、Asset、Provider 签名资产 URL 均从 Product Schema 编译和渲染，
  不在 Go Transport 中写死 `/api/...`。
- 任务历史、Provider 发现、认证会话和余额的路由、查询/响应字段、嵌套
  wire 字段、控制值与前端刷新行为同样从 Product Schema 投影；外部任务
  类型别名统一由 `task_stream.aliases` 声明。
- `/api/providers` 默认输出是与旧站精确兼容的公开投影；Go 前端按 Schema
  声明请求 `projection=extended`，取得条件、提示、输入和新增字段。历史
  字段省略、参数顺序和约束差异属于 Product Schema 兼容配置，不在 Handler
  中形成 Provider 分支。

聊天会话、消息、SSE、Agent 工具调用、Skill Catalog、提示词增强、Canvas 项目/节点与 WebP 图片合并、Provider Webhook、Schema 驱动 SSO、用户与审计后台、生成/用量/余额后台、用户资产总览、旧状态回填、历史上传资产预览及历史代理资产均已切换到 Go。Catalog 中未注册的 Offering 在 live 模式 API 会用 `available:false` 明确标识，不会静默走假服务。

外部 API 迁移核对：`src/app/**/route.ts` 扫描（含根路径 Webhook）确认
77 个 Next.js 外部方法均已有经过测试的 Go 兼容契约。

## 本地启动

要求 Go 1.26+（`go.mod` 的最低版本；镜像与 CI 使用 1.27，推荐 1.27）。

```bash
go test ./...
go run ./cmd/art-ai
curl http://127.0.0.1:13333/readyz
```

默认使用 `PROVIDER_MODE=mock`、`AUTH_MODE=development`，数据写入 `./data/art-ai-go.db`，Blob 写入 `./storage-go`。

`DEV_MOCK_AUTH=true`（不校验密码即可签发会话的 mock 登录）只用于开发；`APP_ENV=production` 时进程拒绝启动（#247），与生产禁止 `AUTH_MODE=development` 相同。它默认关闭，`.env.example` 不提供该项；自部署 Compose 固定 `APP_ENV=production`。

提交一个任务。公开 API 把「确认本次执行」放在提交之前：先 `prepare` 冻结输入，再 `grants` 申请执行授权（需要人决定时再批准），最后用授权提交。执行授权只接受浏览器登录会话（Cookie）发起，开发身份和 Bearer token 会得到 `403`。所以下面用 `AUTH_MODE=session` 启动本地 Mock 服务，再用管理员登录：

```bash
AUTH_MODE=session SESSION_SECRET='至少32字符的随机串' ADMIN_PASSWORD='本地管理员密码' \
  go run ./cmd/art-ai
```

```bash
B=http://127.0.0.1:13333
J='Content-Type: application/json'; O="Origin: $B"    # 带 Cookie 的写请求必须带同源 Origin
# 1. 登录，Cookie 存到 cj（用户名默认 admin，密码即 ADMIN_PASSWORD）
curl -sS -c cj -X POST $B/api/auth/login -H "$J" -H "$O" \
  -d '{"username":"admin","password":"本地管理员密码"}'
# 2. 创建会话，选一个可用的 Offering
SID=$(curl -sS -b cj -X POST $B/api/chat/sessions -H "$J" -H "$O" -d '{"title":"demo"}' | jq -r .id)
OFF=$(curl -sS -b cj "$B/api/v1/offerings?capability=text-to-image" | jq -r '[.offerings[]|select(.available)][0].id')
# 3. prepare：冻结输入，得到 prepared_input_id
PID=$(curl -sS -b cj -X POST $B/api/v1/tasks/prepare -H "$J" -H "$O" \
  -d "{\"session_id\":\"$SID\",\"capability\":\"text-to-image\",\"offering_id\":\"$OFF\",\"input\":{\"prompt\":\"青铜机械鸟\"}}" | jq -r .prepared_input_id)
# 4. grants：申请执行授权。会话批准方式为自动时 status 直接是 approved；
#    status 为 pending 时（例如手工批准的 3D 步骤），还要由人批准：
GRANT=$(curl -sS -b cj -X POST $B/api/v1/tasks/grants -H "$J" -H "$O" -H "Idempotency-Key: demo-$PID" \
  -d "{\"prepared_input_id\":\"$PID\"}")
RID=$(echo "$GRANT" | jq -r .request_id)
[ "$(echo "$GRANT" | jq -r .status)" = pending ] && \
  curl -sS -b cj -X PUT $B/api/v1/tasks/grants/$RID -H "$J" -H "$O" -d '{"decision":"approve"}'
# 5. 提交：返回 task_id，再轮询任务直到 state 为 SUCCEEDED
TID=$(curl -sS -b cj -X POST $B/api/v1/tasks -H "$J" -H "$O" -H "Idempotency-Key: demo-$PID" \
  -d "{\"prepared_input_id\":\"$PID\",\"authorization_request_id\":\"$RID\"}" | jq -r .task_id)
curl -sS -b cj $B/api/v1/tasks/$TID | jq '{state, assets: [.assets[]?.url]}'
```

这个流程在本机 Mock 栈上实测通过（`jq` 见 README 的前置工具）。完整的脚本化版本是 `scripts/smoke/smoke-public-mock.sh`，覆盖 3D 批准卡、每个资产的字节校验和登出撤销。

核心接口：

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/healthz` | 进程存活检查 |
| `GET` | `/readyz` | 数据库、后台工作进程及存储就绪检查 |
| `GET` | `/api/v1/schema` | 前端与运行时共用的安全 Product Schema 投影 |
| `GET` | `/api/v1/offerings` | Catalog 与运行时可用性 |
| `POST` | `/api/v1/assets` | `file` 字段上传输入资产 |
| `POST` | `/api/v1/tasks` | 持久接受 Task |
| `GET` | `/api/v1/tasks` | 只读分页列表 |
| `GET` | `/api/v1/tasks/{id}` | 只读详情 |
| `POST` | `/api/v1/tasks/{id}/cancel` | 取消 |
| `GET` | `/api/v1/tasks/{id}/events` | 增量事件 |
| `GET` | `/api/v1/tasks/{id}/assets/{assetID}` | 鉴权下载 |
| `GET` | `/api/tasks` | 与现有 UI 兼容的任务历史（所有角色只返回本人任务，#158） |

## live 模式

生产 Web 使用带服务端期限及持久撤销记录的 HMAC v2 Session Cookie。退出后旧 Cookie 立即失效；旧版 Cookie 的兼容截止时间由 Schema 的 `authentication.session.legacy_accept_until` 声明。只有无浏览器的 API
调用方需要显式选择 `AUTH_MODE=bearer`；此时 `AUTH_TOKENS_JSON` 才是 token
到 owner 的 JSON 映射，token 至少 24 字符，且不得提交。

```bash
APP_ENV=production \
PROVIDER_MODE=live \
AGENT_MODE=live \
AUTH_MODE=session \
SESSION_SECRET='至少32字符且稳定保存' \
ADMIN_PASSWORD='稳定保存的管理员密码' \
OPENAI_API_KEY='...' \
go run ./cmd/art-ai
```

至少配置一个已实现 Provider 的 key 才能使用对应远程功能；没有远程 key 时仍可启动 live 服务，本地 Offering 可继续使用，远程 Offering 应投影为不可用。外部 Catalog 可通过 `CATALOG_FILE` 覆盖，加载时会严格拒绝未知字段、重复身份和无效契约。

已实现的凭据变量为 `OPENAI_API_KEY`、`GEMINI_API_KEY`、`JIMENG_API_KEY`、`MESHY_API_KEY`、`TRIPO_API_KEY`、`HYPER3D_API_KEY` 和 `MATTING_API_KEY`；对应 Base URL 都可单独覆盖。完整清单（含别名）以 [docs/provider-configuration.md](docs/provider-configuration.md) 为准。

Meshy 后处理需要把本地/跨 Provider 模型交给上游。下游内网零互联网，模型输入一律以 data URI 内联提交：Offering 声明 `provider_options.inline_model_input`（上限由 `max_bytes` 声明，默认 60MB），声明后即使 `PUBLIC_BASE_URL`+`ASSET_SIGNING_KEY` 已配置也不发布签名 URL；`asset` 参数未声明内联的 Meshy Offering 在启动校验即被拒绝，因此 HMAC 签名 URL 回拉路径虽保留在 Adapter 中，但没有任何合法 Offering 会走到它。`asset-array` 参考图固定内联。能力不可用时显示 `available:false`，启动日志记录原因，页面呈现 Schema 文案而非破损表单。

后处理启用条件与旧 Next.js 版本的差异（详见 `openspec/changes/go-postprocessing-parity/`）：

| 维度 | 旧 Next.js 版 | Go 版 |
|---|---|---|
| 启用凭据 | 仅 `MESHY_API_KEY` | `MESHY_API_KEY`；模型输入由 Offering `inline_model_input` 声明承载，不需要 `PUBLIC_BASE_URL`+`ASSET_SIGNING_KEY` |
| 内网/无公网部署 | 后处理可用（data URI 内联） | 后处理可用（同样 data URI 内联，但上限、适用能力由 Product Schema 声明） |
| 模型输入 | multipart 文件转 base64 data URI 内联，或 URL 透传 | 平台 Asset → 按 Offering 声明 data URI 内联（声明内联时不使用签名 URL） |
| 安全语义 | 裸 URL 透传，无签名/所有权校验 | HMAC 签名 + 过期时间 + 所有权隔离；内联路径不落 URL（有意增强，不回退） |

Meshy 返回资产的字段路径、集合形态、角色和变体也由 Offering 的 `provider_options.result_assets` 声明。加载 live 配置时会在启动阶段校验这些 wire Schema，避免任务提交并扣费后才发现结果协议不可解析。

容器验证用自部署入口 `docker-compose.yml`：在 `.env` 设 `PROVIDER_MODE=live` / `AGENT_MODE=live` 与所需 key 后执行 `docker compose up -d --build`，再 `curl http://127.0.0.1:13333/readyz`。步骤与必填项见 [README 的 Docker Compose 自部署](README.md#docker-compose-自部署)。原 `docker-compose.go.yml`（bearer 身份、legacy 挂载）已随 #251 删除。

## dev-test 主机隔离候选环境

候选环境固定放在 `<dev-test 发布根>`（下文 `<root>`），不得覆盖旧生产
`<旧 Next 根>`。旧生产与候选栈的 Compose 项目名都由本地环境文件提供
（仓库不写环境的项目名，#261）；启动候选时必须显式传
`-p <候选项目>`，`scripts/deploy-go-candidate.sh` 从 `CANDIDATE_PROJECT` 读取。由于项目名隔离，
候选栈的操作不得关联或重启旧生产容器。

候选配置分成两类：

- `<root>/candidate.env`：唯一入口的 `GO_WEB_BIND_IP`、
  `GO_WEB_HOST_PORT`、目录、镜像标签、Provider 模式、认证配置，
  `GO_LEGACY_ASSET_CACHE_DIR`、`GO_LEGACY_THUMBNAIL_CACHE_DIR` 两个候选
  缓存副本目录，以及候选独立的 `GO_WEBHOOK_PATH_SECRET`、
  `GO_WEBHOOK_KEY`。
- `<root>/provider.env`：Provider URL 与凭据接线，权限必须为 `0600`，不得提交仓库，也不得复制旧生产 webhook key/path secret。

`docker-compose.go-candidate.yml` 只有 `art-ai-go` 一个服务，并自动读取
`${GO_PROVIDER_ENV_FILE:-../provider.env}`；因此 Provider 可用性不依赖执行
部署命令的 shell。变量名及别名仍以 Product Schema 的
`application.runtime.configuration` 为唯一真源，例如 Matting 同时接受
`MATTING_API_KEY` 与 `MATTING_API_TOKEN`。Webhook 契约是
`application.provider_webhooks[]` 集合，运行时按每项稳定 ID 读取其秘密
设置并编译路由，不在 Transport 中增加 Provider 分支。为避免误接旧生产
回调，Compose 会用候选专属 `GO_WEBHOOK_PATH_SECRET`、
`GO_WEBHOOK_KEY` 覆盖当前 Meshy 契约在 Provider 文件中的同名旧值；未
配置候选值时 Webhook 固定返回 404。验收脚本只有在同时传入匹配的
`CANDIDATE_WEBHOOK_PATH_SECRET`、`CANDIDATE_WEBHOOK_KEY` 时才执行成功、
鉴权和错误响应测试。

候选 Go 通过 `GO_LEGACY_ASSET_CACHE_DIR` 和 `GO_LEGACY_THUMBNAIL_CACHE_DIR` 把隔离缓存副本挂载到 `/app/legacy-cache`、`/app/legacy-thumbnails`，容器内环境接线由 Product Schema 声明。代理缓存未命中和缩略图生成都需要写权限；需要验证历史资产时，应先把旧数据库与缓存复制到候选目录，再指向这些副本。严禁把旧生产数据库或缓存目录直接挂载为候选可写目录。

历史上传资产由 Product Schema 的 `legacy_input_assets_root` 配置驱动，候选 Compose 会把 `${GO_WEB_STORAGE_DIR}/assets` 只读挂载到 `/app/legacy-assets`，并设置 `LEGACY_INPUT_ASSETS_ROOT=/app/legacy-assets`。当候选数据库快照包含旧 `asset_registry` 表时，`/api/assets/{id}` 会先读取 Go 原生资产，只有原生记录不存在时才回退旧元数据和旧文件；所有权、管理员访问、审计、JSON 与二进制契约保持统一。

```bash
cd <root>/app
GO_IMAGE_TAG=替换为镜像标签 scripts/deploy-go-candidate.sh
```

候选启动严禁使用 `--remove-orphans`，避免误伤同宿主机的旧生产 Compose。
部署脚本会先用独立临时 SQLite、Blob、缓存目录及回环端口运行同镜像预检；
通过后才验证旧候选容器的 Compose 标签并按完整名称停止
`<候选项目>-art-ai-web-1`。如果正式 Go 服务未能健康启动，则自动恢复旧
候选 Web 容器。新候选验收通过后，才可按完整名称显式移除这个已停止的
历史容器。

候选栈（历史，地址见本地环境文件）仍保留，但正式切换后候选容器保持
停止；需要下一次发布演练时才显式启动。页面、静态资源和 API 共用该
候选入口，数据库、Blob 和历史缓存副本与 `dev-test` 主实例全部分离。候选已使用
与旧站相同的 HMAC Session 边界，
登录身份、双向 Cookie、Provider 余额和默认 Provider 发现契约均已完成
差分验收。Mock 验收使用独立回环端口启动同一种单容器拓扑，
用于验证所有生成 Capability，不接触计费 Provider。候选自动验收默认
`CANDIDATE_ALLOW_GENERATION=false`，只执行无扣费契约；Mock Provider
环境需显式设为 `true`。live 候选的有效远程生成必须另行取得显式授权。

## dev-test 测试环境全量迁移演练

`dev-test` 主机由多个业务共用。本节现行发布命令只涉及 Plinth 的 `dev-test` 栈；同机的 `legacy-formal` 已于 2026-10-30 下线（存档 #74，见 `dev_ops.md`）。各内部环境按环境名部署，入口 `docs/deployment.md`；正式测试环境 `formal-test`（#270）只晋级 `dev-test` 已全部验收的 revision。
Plinth 的对话和图片模型出站请求访问 llms 正式服务器
网关的 `/v1`（地址在 dev-test 运行环境文件 `LLM_BASE_URL`；链路：测试客户端 → `dev-test` Plinth 测试栈 →
llms 正式服务器；后者不限制客户端与模型）。测试链路上游 key 为
`<TEST_API_KEY>`（2026-09-27 用户给定，仅适用测试链路；正式生产密钥只存正式
生产自身运行环境文件，权限 `0600`）。测试栈对话模型为 `qwen3.8-omni-flash`（2026-10-30 维护者决定，替代 `deepseek`，不再使用 deepseek）；请求走 OpenAI 兼容 `chat/completions`。思考参数按 Product Schema `chat.agent.thinking.model_profiles` 的模型前缀发送，该模型命中 `qwen3.8-omni-flash` profile（`enable_thinking=true`、`reasoning_effort` 默认 `xhigh`，上下文窗口 1000000）；其他中国大陆模型仍用官方最高档。部署只更新 dev-test Compose 项目（环境文件 `compose_project`）的 `art-ai-go`
服务，不操作同机其他业务或代理测试实例。接口目录可达不能替代真实
生成、资产落盘和页面回归。

### 已完成状态（2026-07-29，仅测试环境）

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

### 镜像构建的依赖源（#248/#257）

`Dockerfile.go-runtime` 只从公共 npm registry（`https://registry.npmjs.org/`）安装 pnpm 与前端依赖，Go 模块走 `GOPROXY`（默认 `https://proxy.golang.org,direct`）；`pnpm-lock.yaml` 只记完整性校验值、不写死任何源，因此外部网络可直接 `docker build -f Dockerfile.go-runtime .`。npm 源固定为公共 registry，构建不再接受覆盖：内部环境由 `scripts/release-env.py` 只把环境文件中的 Go 代理（`build.goproxy`）与 `--add-host`（`build.add_hosts`）传给 `scripts/release-server-steps.sh`（对应 `RELEASE_GOPROXY`、`RELEASE_BUILD_ADD_HOSTS`），仓库不写死镜像源；旧本地环境文件里的 `build.npm_registry` 会被拒绝，需删除该字段。构建阶段与 CI 同用 `golang:1.27`。

镜像目标（#259）：默认目标 `runtime` 是自部署镜像，含 `art-ai`、`art-ai-storage`、`art-ai-retention`；`runtime-with-migration-tools` 在其上追加历史迁移与有界维护命令 `art-ai-etl`、`art-ai-recover-assets`、`art-ai-complete-conversion-delivery` 与 `/app/legacy-*` 挂载点，供内部环境（`dev-test` 与 `formal-test`）使用。`scripts/release-server-steps.sh` 以 `--target runtime-with-migration-tools` 构建，`docker-compose.go-production.yml` 与 `docker-compose.go-candidate.yml` 的 `build.target` 相同，内部运维与候选构建因此使用同一目标并包含同一组维护命令。

### dev-test 后续普通发布（不重跑 ETL）

`dev-test` 是 Plinth 测试栈（Compose 项目为环境文件的 `compose_project`，目录 `<root>/production/`），继续按本节现行命令发布。测试日常发布入口为 `python3 scripts/release-env.py release dev-test`（它向 `scripts/release-go-testenv.sh` 传入必填的 `RELEASE_HOST`、`RELEASE_ROOT`）。同机曾有隔离正式栈 `legacy-formal`（内部存档 #232），已于 2026-10-30 下线（存档 #74），不再有对应的晋级脚本；正式测试环境 `formal-test` 的晋级入口与来源验收报告格式见 `docs/deployment.md`。

各栈默认 DEBUG，应用日志同时写 stdout（`json-file` `50m × 10`）和各栈 `logs/art-ai.log`（50 MiB 滚动、保留 30 天），响应带 `X-Request-ID` 错误 ID，日志统一脱敏（#233）。详情见 `dev_ops.md`。

普通发布必须来自已推送且工作区干净的 `main`。镜像标签用于人类识别，
完整提交 SHA 另写入 OCI revision 并由预检脚本校验：

```bash
git fetch origin
git switch main
git pull --ff-only origin main
test -z "$(git status --porcelain)"
source_commit="$(git rev-parse HEAD)"
image_tag="${source_commit:0:12}-$(date -u '+%Y%m%dT%H%M%SZ')"

# docker-compose.go-production.yml 的 8 个主机路径变量没有缺省值：读取发布写出的
# <root>/production/compose.env，或在本地用 python3 scripts/release-env.py compose-env dev-test 生成同名内容
set -a; . <root>/production/compose.env; set +a
export GO_PROD_ROOT=<root>/production   # cutover-go-production.sh 还需要 compose.env 中的 4 个 GO_LEGACY_*_DIR
export GO_RUNTIME_ENV_FILE=<root>/production/runtime.env
export GO_IMAGE_TAG="$image_tag"
export GO_SOURCE_COMMIT="$source_commit"

docker compose \
  --env-file "$GO_RUNTIME_ENV_FILE" \
  --env-file <root>/production/compose.env \
  -p <compose_project> \
  -f docker-compose.go-production.yml \
  build art-ai-go

GO_PROD_PROJECT=<compose_project> scripts/cutover-go-production.sh preflight \
  --env-file "$GO_RUNTIME_ENV_FILE" \
  --image-tag "$GO_IMAGE_TAG" \
  --source-commit "$GO_SOURCE_COMMIT" \
  --no-build
```

预检不触碰 `dev-test` 主实例。进入停机窗口后先记录旧镜像标签并对
`production/data`、`production/storage-go`、`production/web-data` 做一致性
备份，再用同一 Compose 项目更新唯一服务并等待 healthy。新容器或无扣费
线上验收失败时，停止新容器、恢复备份和旧镜像标签后重新启动；不得启动旧
Next 容器作为普通版本回滚手段。

自动发布仅在新实例健康且成功清单写入后清理 `art-ai-go-production` 镜像：按创建时间保留最新五个不同 ID，另保留运行镜像、发布前实际运行的回滚镜像，以及 Plinth 两个 Compose 项目所有运行/停止容器引用的镜像。跨仓库共享标签/digest 的镜像也保留；保护项可使数量超过五个。构建前只查清单与容量，不删除镜像。只用 `docker rmi <id>`，不强制、不做广域 prune、不删容器、卷或数据库备份。删除失败记警告，不使发布失败。

发布前 dry-run（在本地仓库运行；回滚标签必须来自已核实的实际上一运行镜像，不按创建时间猜）：

```bash
ssh <dev-test 的 ssh> 'python3 - --dry-run --root <root> --rollback-image art-ai-go-production:<上一运行标签>' \
  < scripts/retain-art-ai-images.py
```

该命令只输出 `KEEP` / `WOULD REMOVE`，不改写服务器文件。服务器已有同版 helper 时也可直接运行 `python3 <root>/app/scripts/retain-art-ai-images.py --dry-run --root <root> --rollback-image art-ai-go-production:<上一运行标签>`。完整边界和离线验证见 `dev_ops.md`「dev-test 主机两栈边界与镜像保留」。

Go 原生表的结构升级只允许使用
`internal/adapters/sqlite/migrations/` 中只追加、带 SHA-256 校验的版本化
migration。需要数据回填或重写时，先在生产快照副本演练并对账，再在停机
备份后随同一 `main` 镜像执行；失败恢复备份。`cmd/art-ai-etl` 和
`scripts/finalize-go-production.sh` 只属于一次性 Next → Go 全量迁移；
`dev-test` 普通发布严禁调用，也不得改写 `migration-ready.txt`。这段只约束原 Next → Go 全量迁移，不适用于 #232 的独立空库正式栈；后者不导入数据，按 `dev_ops.md` 两栈规则晋级。

以下内容保留为首次迁移的可审计执行设计，不是每次发布都要重跑的步骤。

正式生产切换不沿用“先启动空库 Go、再在线灌数据”的顺序。唯一受支持的
入口是 `scripts/finalize-go-production.sh`，它要求传入 `main` 的完整提交
SHA，并校验镜像的 OCI revision 标签；还要求现场提供主机名、Docker daemon ID、
来源快照基线、容器/Compose 身份、IP 与 Go/preflight 端口。正式参数与拒绝条件以
[`docs/production-migration-runbook.md`](docs/production-migration-runbook.md) 为准。
随后在一个失败可恢复的事务中执行：

1. 用待发布的同一镜像在临时目录和回环端口预检；
2. 验证并停止现场显式声明的旧 Compose 容器，冻结所有旧写入；
3. 对现场数据库路径和完整 storage 生成最终备份，并再次核对来源基线；
4. 在 Go 尚未启动时向全新 staging 目录执行隔离 ETL；
5. 严格对账通过后生成包含镜像版本及数据库、报告、manifest SHA-256 的
   八字段 `migration-ready.txt`；runner 与 takeover 共用完整 manifest 门禁；
6. 逐项归档原生产目录、提升迁移数据，再由显式声明的独立 Go 项目接管
   现场 IP/端口，不继承 `dev-test` 测试拓扑；
7. 执行登录、Session、历史资产、Provider、Webhook 与管理数据的无扣费
   线上验收，全部通过后才提交事务。

线上验收完成前失败按实际成功的归档/提升逐项回滚；正常恢复后只启动事务
亲自停止的原容器 ID，原本已停止的容器保持停止。formal takeover 子脚本接收
该冻结 ID，只确认身份/停止状态，不按名字重新停容器，也不自行回滚旧服务；
目录回位和旧服务恢复由外层统一负责。健康探测使用实际绑定地址，通配地址映射为
同地址族回环。若 Go 停止或目录恢复失败，
报告回滚未完成、保留两套数据并阻止旧服务启动，不把中间状态视为成功恢复。
脚本不删除旧容器、旧数据、失败 staging 或归档，也不使用
`--remove-orphans`。

R9 真实体量演练确认有 38 个历史 Task 的 40 个必需输出在旧 storage、
审计副本和可查询上游中均已永久缺失。用户随后明确批准这些 Task 保留全部
元数据与账务、降级为 `FAILED/integrity`，绝不伪造 Asset。最终事务仍按
严格模式重新生成候选问题集，并要求与已批准 R9 问题身份/指纹逐项一致；
最终绑定清单为
`production/reports/final-source-loss-approved.json`。任何问题集变化都会
让重新绑定失败并自动恢复旧生产。

生产运行环境文件位于 Go 根目录的 `production/runtime.env`，权限必须为
0600。确认清单同样必须为 0600，root 执行时由隔离 ETL 用户 UID 100
持有。历史演练证据（2026-07-29 候选演练记录）已移出仓库，只保留在内部 Git 历史中。当前迁移规格与状态见原生 Milestone #4。


## 扩展 Provider

兼容现有协议的新模型只添加 Offering 数据。新协议实现 `generation.ProviderPort`，注册稳定 `adapter_key`，再通过 httptest 契约测试验证状态映射、限流、提交含糊性与资产读取。Generation、HTTP 路由和页面均不应出现供应商私有分支；运行参数、路由元数据和用户可见文本也不得在代码中建立第二真源。

架构证据、状态机和迁移路径见 [docs/go-architecture.md](docs/go-architecture.md)，领域语言见 [CONTEXT.md](CONTEXT.md)。

## CAS 存储维护

镜像内的 `art-ai-storage` 默认输出孤儿文件和磁盘水位清单。执行清理需要停下该存储根的所有写入进程，步骤及引用边界见 [CAS 存储维护](docs/storage-maintenance.md)。

镜像内的 `art-ai-retention` 提供独立离线任务保留命令；`art-ai-retention prepare --help` 仅显示参数，不打开数据库或生成清单。默认策略禁用，准备需要显式私有 Schema 与 cutoff，apply 和恢复 cleanup 每次都要求真实人类在控制终端批准精确 manifest；不得由 Agent 代答，也不由服务自动运行。#59 仅允许实现与隔离验收，live purge 仍须另行批准。完整备份、恢复与部分失败合同见 [任务保留](docs/storage-maintenance.md#独立的任务保留命令)。

镜像内的 `art-ai-recover-assets` 用于受信运维人员验证已有产物包并附加恢复回执，协议见 [已有产物恢复](docs/architecture.md#已有产物恢复协议207)。必须提供 `--database`、`--owner`、`--source-task`、`--source-attempt`、`--package`、`--expected-package-sha256` 和 `--offering`；默认只读预演，在私有临时 CAS 验证全部字节。只有显式 `--apply --storage-root <已有资产根>` 才写入已验证字节和不可变回执，且部署必须已完成 0054；它不提交 Task、不改写原失败记录。运行自定义 Product Schema 时同时传入 `--catalog <该部署使用的Schema文件>`。首次实际 `--apply` 前须按现行 Plinth 发布协议完成一致性备份并明确回滚范围；CLI 没有独立撤回模式，不能直接删除共享 CAS 或用旧库覆盖备份后的用户写入。包摘要必须来自独立审定记录，不能把临时计算的新摘要当作原始证据；正式生产操作仍须独立授权。
