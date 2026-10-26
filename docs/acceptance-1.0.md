# 1.0 Acceptance Checklist（开源 1.0 功能验收清单）

本文是开源 1.0 的验收依据（GitLab #260）：列出要验什么、怎么验、预期结果是什么。它来自 `docs/product.md` 的产品规则和开源 1.0 系列 Issue（#247–#261）中的 1.0 标准。

**本文不记录验收结果。** 清单里的每一项都还没有按本文验收；逐项证据写在 #260 的原生 note 里，本文只在规则或范围变化时修改。某项依赖的 Issue 尚未关闭时，该项先标“阻塞于 #N”，不能跳过或改写预期来通过。

## 1.0 Standards（1.0 标准）

1.0 标准由用户在 2026-10-19 确定（见 #260 描述），本清单按这七条分组覆盖：

1. 本地 `docker compose up` 即可启动，构建不依赖任何内网资源（#248、#251、#259）。
2. Provider 的 API 地址和 key 通过 compose / env 配置（#252）。
3. Agent LLM 只支持 OpenAI chat completions 兼容 API，模型必须能读图（#253）。
4. 基础功能全部通过验收，没有已知阻塞 bug（本文、#260）。
5. 用户系统只有管理员和普通用户（#249）。
6. 基本安全基线（#247、#256）。
7. 界面支持英语和中文（#254、#255）。

不在 1.0 范围：用户自行添加 Provider、团队/组织、加密密钥存储、非 OpenAI chat 协议。

## How to Run（执行方式）

- **两种模式各走一遍。** Mock 模式（`PROVIDER_MODE=mock`、`AGENT_MODE=mock`，零 key）覆盖全部不依赖真实上游的项；真实 Provider 模式覆盖标为“真实”的项，每个 Provider 只做小额单次请求，先核对该端点当前官方协议、价格和账户余额，不重放已消费的请求。
- **两种语言各走一遍。** 带界面的项在 `zh-CN` 和 `en` 下各验一次（语言切换见 I18N 组）；纯 API/命令的项验一次即可。
- **浏览器与视口。** 产品只面向 PC（`docs/product.md` §1）：Chromium 与 WebKit 桌面浏览器，视口 1366×768、1440×900、1920×1080，全新浏览器上下文、禁用缓存。
- **以公开交付为准。** 上游返回成功、模型能打开、健康检查通过都不等于通过；生成类项须核对公开 Task 终态、必需产物、实际字节和鉴权下载（`docs/architecture.md`）。
- **证据格式。** 每条 note 写明：项目 ID、被测完整 commit SHA、模式（Mock/真实）、语言、浏览器与视口、结果（通过/失败/阻塞）、证据（截图、脱敏的请求与响应摘要、命令输出、`X-Request-ID`）。不贴密钥、Cookie、签名 URL 或完整提示词。
- **通过口径。** 全部项在两种模式、两种语言下通过（标为单一模式的项除外），且没有未关闭的阻塞 bug（#260 Acceptance）。

表中“模式”一列：M = Mock 模式，L = 真实 Provider 模式，M/L = 两种都验。

## Clean-Machine Deployment（全新机器部署验收）

在一台从未部署过本项目、不能访问任何原内网资源的机器上，只按公开仓库的 README 和文档从 clone 走到可用。这一组对应 1.0 标准 1、2 和 #251、#248、#259、#261 的验收。

### Machine Requirements（机器要求）

- Linux x86_64 或 macOS（Apple Silicon），已装 Docker Engine 与 Docker Compose v2、`git`、`curl`、`openssl`。不预装 Go、Node 或 pnpm（源码门禁项 DEP-12 例外）。
- 只能访问公共互联网。开始前确认机器解析不到也连不上原内网主机（核对方法与结果写进 #260 note，不在公开文档里列内网地址）。
- 没有旧的 Docker 卷、镜像缓存或 `.env`；记录 `docker version`、`docker compose version` 和操作系统版本。

### Procedure（步骤）

| ID | 模式 | 操作 | 预期结果 | 关联 |
|---|---|---|---|---|
| DEP-01 | M | 匿名 `git clone <公开仓库 URL>`，`git rev-parse HEAD` 记下 SHA | 无需凭据即可 clone；仓库里没有内网主机名、内网 IP 或内网部署流程（`scripts/check-public-tree.sh` 对快照扫描通过；迁移工具按 #259 说明除外） | #259、#261 |
| DEP-02 | M | 按 README 快速开始：`cp .env.example .env`，用文档给出的命令（如 `openssl rand -base64 48`）生成 `SESSION_SECRET` 等必填密钥，填管理员用户名与密码 | 必填项只有文档列出的几项；每项都有生成方法；`.env` 权限按文档设为 `0600` | #251、#256 |
| DEP-03 | M | `ART_AI_SOURCE_COMMIT=$(git rev-parse HEAD) docker compose up -d --build`（以 README 的命令为准） | 镜像全部从公共源构建成功，构建日志里没有内网地址；服务进入 healthy | #248、#251 |
| DEP-04 | M | `curl -fsS http://<主机>:<端口>/healthz` 与 `/readyz` | 两者都返回 200 | #251 |
| DEP-05 | M | `curl -fsS http://<主机>:<端口>/api/version` | 返回 JSON 的 `version` 字段等于 DEP-01 记下的完整 SHA（构建时未传 `ART_AI_SOURCE_COMMIT` 则为 `unknown`） | #251 |
| DEP-06 | M | 浏览器打开首页，用 `.env` 中的管理员登录 | 登录成功，进入工作台；首页没有 CSP 拦截或白屏，所有静态资源同源（网络面板里没有外部 CDN 请求） | #251、#256 |
| DEP-07 | M | 零 Provider key，在工作台按 IMG-01、M3D-01 各走一次 Mock 生成 | 生成任务成功，交付 Mock 图片与 3D 全部格式，可在舞台查看和下载 | #251 |
| DEP-08 | M | `docker compose restart`，再 `docker compose down` 后 `docker compose up -d` | 两次之后会话、画布、任务、资产与账户都还在；管理员密码不被 `.env` 覆盖（`ADMIN_PASSWORD_RESET` 未开） | #251、#256 |
| DEP-09 | M | 修改 `.env` 中端口与绑定地址后重新 `up -d` | 服务在新端口/地址监听；compose 中没有固定子网或固定 IP | #251 |
| DEP-10 | L | 按 Provider 配置文档在 `.env` 写入一个或多个 Provider 的 key 与地址，并设真实模式，`docker compose up -d` 重建 | 配了 key 的 Provider 的 Offering 在 `GET /api/v1/offerings` 中 `available=true`；没配的明示不可用；按 PRV 组逐个验证 | #252 |
| DEP-11 | M | `docker compose down -v` | 容器与数据卷被删除；文档写明这一步会删数据 | #251 |
| DEP-12 | M | 源码门禁：装 README 要求的 Go 与 pnpm 版本，`pnpm install --frozen-lockfile` 后执行 `dev_test.md` §2 的全部八项（或 `python3 scripts/verify-local.py --race`；不带 `--race` 时不含仅 CI 的 race 项） | 依赖全部从公共源安装；八项全部通过 | #248、#257 |

## Feature Checklist（功能验收清单）

### Authentication and Users（登录与用户）

1.0 只有管理员和普通用户两种角色。关联 #247、#249、#256。

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| AUTH-01 | M | 登录页输入管理员账号；`POST /api/auth/login` | 登录成功，响应设置 `HttpOnly`、`SameSite=Lax` 的会话 Cookie |
| AUTH-02 | M | 用错误密码和不存在的用户名分别连续登录 | 两者都只提示“用户名或密码错误”；超过 Schema 登录节流阈值后被限制 |
| AUTH-03 | M | 管理后台创建普通用户，再用该用户登录 | 新用户角色为普通用户，看不到管理入口；直接请求 `/api/admin/users` 等管理接口返回 403 |
| AUTH-04 | M | 管理后台修改用户角色、停用用户 | 只有 `user` / `admin` 两种角色可选；停用后该用户的会话失效、不能再登录；不能停用或降级最后一个管理员 |
| AUTH-05 | M | 登出（`DELETE /api/auth/session`）后用原 Cookie 请求 `/api/auth/session` 与 `/api/v1/tasks` | 返回未登录 / 401，登出前的 Cookie 已在服务端撤销 |
| AUTH-06 | M | `APP_ENV=production` 且 `DEV_MOCK_AUTH=true` 启动 | 启动失败并给出明确错误；生产模式下 `POST /api/auth/mock` 不可用 |
| AUTH-07 | M | 检查登录页与配置 | 没有内部 SSO 入口；`AUTH_MODE` 只接受开源版支持的模式 |
| AUTH-08 | M | 管理员设置或修改密码时使用过短密码 | 被拒绝，提示 Schema 规定的最小长度 |
| AUTH-09 | M | 用户 B 的 Cookie 请求用户 A 的会话、任务、资产与下载地址 | 全部拒绝（404 或 403），不泄露是否存在；管理员查看他人产物写审计 |

### Provider Configuration（Provider 配置）

1.0 内置 Meshy、Tripo、Hyper3D、Jimeng、Gemini，配了 key 就启用，没有 key 就不启用。关联 #252。

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| PRV-01 | L | 对每个内置 Provider：只在 `.env` 配它的 key（和可选地址），重建容器，查 `GET /api/v1/offerings` 和工作台模型选择器 | 该 Provider 的 Offering 可用，其他 Provider 明示不可用 |
| PRV-02 | L | 删掉 PRV-01 的 key 并重建 | 该 Provider 的 Offering 变为不可用，界面写明原因；不报错、不显示成可用、不静默换成别的模型 |
| PRV-03 | M/L | 对照 Provider 配置文档、`.env.example` 与 Product Schema | 三者列出的变量名一致（有测试比对）；文档写明每个变量的用途和计费风险 |
| PRV-04 | M | 浏览器取首屏 HTML、`/api/v1/schema` 与 `/api/v1/offerings` | 不含任何 key、运行配置或上游私有地址 |
| PRV-05 | L | 顶栏余额 | 显示已配置 Provider 账户里余额最低的一个，展开后逐个列出；未配置的不出现 |

### Agent

Agent LLM 只用 OpenAI chat completions 兼容 API，模型必须能读图。关联 #253；行为规则见 `docs/product.md` §4、§6。

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| AGT-01 | L | 用一个标准 OpenAI 兼容的视觉模型配置 `LLM_BASE_URL` / `LLM_MODEL` / key，不开任何厂商兼容开关；在对话里发一张图片问内容 | Agent 按图片内容作答 |
| AGT-02 | L | 配置一个不支持图片输入的模型 | 启动或首次调用时给出明确错误或提示，不静默丢图 |
| AGT-03 | M/L | 对话里说“生成一张……”“把这张图做成 3D 模型” | Agent 调用对应工具（`refine_image`、`create_model_3d_from_image` 等）；工具参数通过 Schema 校验 |
| AGT-04 | M/L | 会话批准模式为手工，请求文生 3D | 对话里出现行内批准卡，写明基础积分与合计；本轮以“仍在等待”收束，输入框不锁 |
| AGT-05 | M/L | 对 AGT-04 的卡分别点“批准”和“不批准”（用两张卡），双击“批准”，刷新页面 | 批准只提交一次冻结输入，结果发回同一会话；不批准不产生任务、不消耗积分；刷新后待批准列表仍在 |
| AGT-06 | M | 批准模式切到自动，再请求付费步骤 | 自动批准并注明；切换不会批准已在等待的卡 |
| AGT-07 | M | Agent 范围切到 2D 模式后要求生成 3D 模型；切到 3D 模式后要求改图 | 得到“当前模式不允许”的回复，没有工具调用；自动模式下正常 |
| AGT-08 | M | 在“设定摘要”里写入设定，再发后续消息；保存超过字数上限的内容 | Agent 后续回复遵循设定；超限内容不能保存 |
| AGT-09 | M | 输入 `@` 选择图片/模型句柄；在画布选中一张图后说“这张” | 句柄写进消息；Agent 把选中对象当作当前对象 |
| AGT-10 | M | Agent 回复过程中按“停止” | 本轮停止；已出现的批准卡仍可批准，界面说明已提交的上游任务可能仍计费 |
| AGT-11 | M | 管理员在 Agent 管理里修改系统提示词并发布，再重置 | 新轮次使用发布后的版本；普通用户读不到完整配置 |

### 2D Generation（2D 生成）

关联 `docs/product.md` §1.1、§3、§5。

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| IMG-01 | M/L | 选文生图模型，打开“参数”弹层，发送一句描述 | 参数项全部来自所选模型的 Schema；发送按钮写预计积分；任务成功，图片出现在对话结果卡和舞台 |
| IMG-02 | M/L | 新会话不改尺寸直接生成 | 使用默认 1:1 明确尺寸，没有“自动”选项；返回图片宽高以资产字节为准并与目标一致（#267） |
| IMG-03 | M/L | 数量设为 2 以上 | 结果卡并排显示多张候选；“选用”一张后刷新仍为已选用 |
| IMG-04 | M/L | 以一张上传图片做图生图 | 输出保留参考图构图；无效参考图明确报错，不静默降级为文生图 |
| IMG-05 | M/L | 在舞台用蒙版做局部重绘 | 只有蒙版区域改变，其余像素保持 |
| IMG-06 | M/L | 对图片执行去背景 | 交付透明背景 PNG；未配置去背景服务时该功能明示不可用 |
| IMG-07 | L | 每个可用的 2D Provider（Gemini、Jimeng 等）各做一次小额文生图 | 公开 Task 成功，图片可鉴权下载，字节与格式正确 |

### Canvas（画布）

关联 `docs/product.md` §3、§5。

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| CNV-01 | M | 生成完成后看画布；删掉该图后刷新、重新打开会话 | 结果自动加入画布一次；删除后不再回来；对话和版本条里的原素材仍在 |
| CNV-02 | M | 用文字、形状、铅笔、画笔、裁切、图层面板编辑画布，刷新页面 | 编辑全部保存，刷新后恢复 |
| CNV-03 | M | 导出画板 | 下载的 PNG 与画布虚线框内容一致 |
| CNV-04 | M | “合并为新版本”“发送到对话” | 生成新版本 / 成为下一条消息的附件，不调用模型、不扣积分 |
| CNV-05 | M | 双击图片打开大图；进入 2 联、4 联和滑块比较；按 Esc | 大图与比较正常；Esc 依次退出 |
| CNV-06 | M | 无输入焦点时按 `?` | 打开快捷键面板，内容来自 Schema；输入框里按快捷键不触发 |
| CNV-07 | M | 舞台工具条的按钮悬停 | 显示名称、说明、作用对象和费用行；置灰按钮第一行写原因；会调用模型的按钮带 ✦ |
| CNV-08 | M | 缩小浏览器窗口到 1366×768 及以下 | 舞台头不换行、不溢出，收起的文字仍在悬停说明里 |

### 3D Generation and Processing（3D 生成与处理）

交付格式见 `docs/product.md` §8。

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| M3D-01 | M/L | 文生 3D | 公开 Task 成功；3D 卡显示封面；舞台 3D 查看器可旋转缩放 |
| M3D-02 | M/L | 从一张图片做图生 3D | 同上；来源图片关系记录在任务详情中 |
| M3D-03 | M/L | 在右侧栏“格式”标签查看格式 | 列出该步骤的全部必需格式（`docs/product.md` §8.1） |
| M3D-04 | M/L | 在“加工”面板依次执行重拓扑、换贴图、绑骨、动画（按当前可用 Offering） | 每步是一个公开 Task；计划可暂停、恢复、重试失败步骤；不兼容来源被明确拒绝 |
| M3D-05 | M | 3D 舞台“截图到 2D” | 截图成为 2D 画布输入，并记下来源模型 |
| M3D-06 | M | 上传一个 10 MiB 内的自包含静态 GLB 作为功能来源 | 上传通过检查，不自动执行或扣费；超限或含外部依赖时明确拒绝 |
| M3D-07 | L | Meshy、Tripo（及启用后的 Hyper3D）各做一次小额 3D 生成 | 必需格式全部到齐，逐个鉴权下载，字节、大小、SHA-256 与文件签名正确 |

### Delivery and Downloads（交付与下载）

关联 `docs/architecture.md` 的资产交付契约。

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| DLV-01 | M/L | 下载每种格式两次 | 两次字节相同；文件名和扩展名正确 |
| DLV-02 | M/L | 3D 卡“下载全部（ZIP）”；`GET /api/v1/tasks/{taskID}/archive` | ZIP 首项是 `README.txt`，README 中没有 `http(s)://` 地址；README 列出的路径与实际条目一一对应 |
| DLV-03 | M | 匿名请求资产与 ZIP 地址；用户 B 请求用户 A 的地址 | 匿名 401；跨用户拒绝 |
| DLV-04 | M | 检查非图片资产的响应头 | `Content-Disposition: attachment`、`X-Content-Type-Options: nosniff`、`Content-Security-Policy: sandbox` |
| DLV-05 | M | “我的资源”页查看、下载、灯箱 | 只列出本人上传与产物；Esc、点外部或关闭按钮关闭灯箱 |

### Tasks and Recovery（任务与恢复）

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| TSK-01 | M | 顶栏打开任务中心 | 只显示当前会话的任务；点一行打开任务详情 |
| TSK-02 | M | 提示词含 `[mock:slow]` 生成，过程中刷新页面 | 进度继续，刷新后状态正确 |
| TSK-03 | M | 运行中任务点取消；失败任务点重试 | 取消后进入终态；重试创建新 Attempt，规则与界面提示一致 |
| TSK-04 | M | 任务运行中 `docker compose restart` | 重启后任务继续或进入明确终态；不重复提交、不重复计费 |
| TSK-05 | M | 提示词含 `[mock:insufficient_balance]` | 任务失败并显示余额不足的分类提示，不显示内部原因 |

### Skills（Skill）

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| SKL-01 | M | 新用户打开 Skill 选择器 | 为空；没有内置 Skill 和分类 |
| SKL-02 | M | 新建个人 Skill（名称、提示词、推荐尺寸）并选用 | 推荐尺寸成为尺寸默认值并标出“来自 Skill” |
| SKL-03 | M | 删除当前选中的 Skill 和另一个 Skill | 选中的被删后回到“不使用”；删除其他的不改变选择 |

### Administration（管理后台）

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| ADM-01 | M | 管理后台查看用户、用量、生成记录 | 数据与实际一致；下载他人产物写审计 |
| ADM-02 | M | 审计日志按动作和时间筛选；输入非法日期与分页参数 | 筛选正确；非法参数按兼容接口规则处理（非法日期得到空结果，分页参数按整数前缀解析），不报 5xx |

### Logging and Errors（日志与错误）

关联 #233、#256；规则见根 `AGENTS.md` 与 `dev_ops.md` 的日志说明。

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| LOG-01 | M | 任意请求查看响应头；触发一个 4xx | 每个响应带 `X-Request-ID`；错误体带同值 `request_id`，界面错误提示可见错误 ID |
| LOG-02 | M | 用 LOG-01 的错误 ID 搜容器日志（`docker compose logs`）和 `LOG_DIR` 下的日志文件 | 找到对应日志行，含路由、公开错误码和内部原因 |
| LOG-03 | M/L | 检查 LOG-02 与一次真实生成的日志 | 不含 key、Cookie、Authorization、密码、签名 URL 查询串、提示词或上传内容全文 |
| LOG-04 | M | 在浏览器里制造一个前端错误 | 服务端有对应 ERROR 日志；上报有大小和频率限制 |
| LOG-05 | M | `LOG_LEVEL` 设为非法值启动 | 启动失败并给出明确错误 |

### Internationalization（中英双语）

关联 #254（前端）与 #255（Schema、后端、Agent）。两者完成前本组为“阻塞于 #254 / #255”。

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| I18N-01 | M | 浏览器语言分别设为英文和中文，全新上下文打开 | 自动选择对应语言；`<html lang>` 随之变化 |
| I18N-02 | M | 手动切换语言后刷新、重新登录 | 选择被保存 |
| I18N-03 | M | 两种语言下逐页走查：登录、工作台、参数弹层、任务中心、我的资源、管理后台、所有弹窗与悬停说明 | 没有另一种语言的残留文案；没有布局溢出或截断 |
| I18N-04 | M | 英文界面下查看 Schema 驱动的表单、参数说明与错误提示 | 全部为英文；中文界面不退化 |
| I18N-05 | L | 英文界面下与 Agent 对话 | Agent 用英文回复 |
| I18N-06 | M | 日期、数字、积分显示 | 按当前语言格式化 |
| I18N-07 | M | 检查硬编码文案的守门 | 检查在 CI 中运行，新增硬编码文案会失败 |

### Security Baseline（安全基线）

逐项对照 `docs/self-deploy-security.md`。关联 #256。

| ID | 模式 | 验证方式 | 预期结果 |
|---|---|---|---|
| SEC-01 | M | 在 HTTPS 反向代理后设 `NEXT_PUBLIC_APP_URL=https://…` | 会话 Cookie 带 `Secure`；纯 HTTP 内网部署仍可登录 |
| SEC-02 | M | 配置 `TRUSTED_PROXY_CIDRS` 后，从代理和非代理地址分别伪造 `X-Forwarded-For` | 只有可信代理转发的地址被采用 |
| SEC-03 | M | 跨站 `Origin` 发状态变更请求 | 返回 403 |
| SEC-04 | M | 短时间大量请求 `/api/` | 超过限额返回 429 `rate_limited` 与 `Retry-After` |
| SEC-05 | M | 上传超过上限的文件、伪装类型的文件 | 超限被拒；非图片资产按附件下发，不能在站内执行 |
| SEC-06 | M | 首屏 HTML 的 CSP 与脚本 | CSP 覆盖唯一内联脚本，外链脚本同源，浏览器无 CSP 报错 |

### CI and Release（CI 与发布）

| ID | 模式 | 验证方式 | 预期结果 | 关联 |
|---|---|---|---|---|
| REL-01 | — | 在 GitHub 公开仓库开一个测试 PR | `.github/workflows/ci.yml` 的三个门禁作业（`frontend`、`go check`、`go test race`）全部运行并通过；分支保护要求它们通过。CI 不构建镜像（#257） | #257 |
| REL-02 | — | 合并后查看内网 GitLab 镜像 | 镜像收到同步，内网 runner 跑完 `.gitlab-ci.yml` | #257、#261 |
| REL-03 | — | 只读 README（中、英各一次）完成 DEP 组 | 不需要 README 之外的信息；中英文内容一致 | #258 |
| REL-04 | — | 检查 `LICENSE`、`CONTRIBUTING.md`、`SECURITY.md` 与依赖许可证清单 | MIT（2026-10-21 用户决定，不需要 `NOTICE`）；依赖许可证无冲突；需要决定的随仓素材已处理（见 #258） | #258 |
| REL-05 | — | 按 README「Docker Compose 自部署」用本清单验收的精确 SHA 的源码构建并启动 | 首个 1.0 版本只交付源码与 Compose 自部署，不打 tag，也不创建 GitHub Release | #261 |

## Automation（自动化）

#260 要求把能自动化的步骤做成可运行的冒烟测试。公开 CI 只跑 `dev_test.md` §2 的三个门禁作业，不构建镜像（#257），因此零 Key 的 Mock 自部署冒烟保留为本地手动验收：自部署后按 README 从 `.env.example` 起 `docker compose`，再运行 `scripts/smoke/smoke-public-mock.sh`。它覆盖 DEP-01/02/03/04/05/07/08/11、AUTH-01/05、PRV-04、IMG-01、M3D-01、DLV-01/02/03/04、LOG-01、SEC-03/06（逐项对应见该脚本头部注释与 #260 的 note）；脚本只接受 Mock Provider、Mock Agent、所有 `*_API_KEY` 为空的回环地址栈，不花费用。`scripts/smoke` 的其余脚本面向内部测试环境，经 `scripts/release-env.py smoke <环境>` 运行，不进公共 CI。真实 Provider 项、未列入上面的项与双语走查仍需人工按本文执行，结果记在 #260。
