# 合作方部署指南

本文写给自行部署 Plinth 的外部合作方运维人员（#235）。目标有两个：按统一的方式部署；出问题时，把日志和脱敏后的证据发回与你对接的 Plinth 维护者，用于排查问题，并完善维护者手里的 fake（模拟各家上游的测试替身，不产生费用）。

本文只写流程，不重复细节：基础的自部署说明见 [README](../README.md#docker-compose-自部署)，对外提供服务前的安全配置见 [自部署安全配置](self-deploy-security.md)，录制与诊断包的完整参数、字段和维护者收包后的处理流程见 [部署入口](deployment.md)（第 6 节的「上游调用脱敏录制（#233）」与「导出诊断包（#234）」）。

> **状态**：上游调用脱敏录制（#233）与诊断包导出（#234）已通过源码与自动化测试验证，尚未在真实 Docker 卷与容器用户上完成部署验收（见 [架构文档](architecture.md) 的对应小节）。某一步的实际表现与本文不符时，请把终端输出原样告知维护者（不要附带 `.env` 的内容）。

## 1. 前置条件

- **主机**：Docker Engine 与 Docker Compose v2.24+（`env_file` 的 `required` 写法需要它），以及 `git`、`openssl`、`curl`。命令按 Linux/macOS 的 shell 书写。不需要安装 Go、Node 或 pnpm，镜像从源码构建。
- **构建网络**：构建主机能访问 Docker Hub（基础镜像 `node:22-alpine`、`golang:1.27`、`alpine:3.23`）、公共 npm registry（固定为 `https://registry.npmjs.org/`）与 Go 模块代理（默认 `https://proxy.golang.org,direct`，可在 `.env` 用 `GOPROXY` 改源）。首次构建需要几分钟。
- **端口**：服务默认只监听本机 `127.0.0.1:13333`（`ART_AI_BIND_IP`、`ART_AI_PORT`）。要让其他机器访问，先放到 HTTPS 反向代理之后，并完成 [自部署安全配置](self-deploy-security.md)。
- **上游与对话模型**：连真实上游需要各 Provider 的 API key；Agent 需要一个 OpenAI chat completions 兼容、能读图的对话模型。见 [Provider 配置](provider-configuration.md)。只想验证部署与诊断流程时，保持默认的 `PROVIDER_MODE=mock`：不调用上游生成服务，不产生费用。
- **磁盘**：数据库与资产放在命名卷 `art-ai-data`、`art-ai-storage`；按本文配置，日志与录制也写进 `art-ai-data`。日志文件按默认值最多约 3 GiB（单个文件 50 MiB、最多 60 个滚动文件，超过 30 天的删除），录制默认最多 64 MiB；容器输出日志由 Docker 另按 `50m × 5` 滚动。

## 2. 部署步骤

```bash
git clone https://github.com/LeXwDeX/Plinth.git
cd Plinth
# git checkout <提交 SHA 或 tag>    # 需要固定版本时去掉行首的 #，版本与维护者约定
cp .env.example .env
sed -i.bak \
  -e "s|^SESSION_SECRET=.*|SESSION_SECRET=$(openssl rand -base64 48)|" \
  -e "s|^ADMIN_PASSWORD=.*|ADMIN_PASSWORD=$(openssl rand -base64 24)|" \
  .env && rm .env.bak
chmod 600 .env
```

接着用编辑器按第 3 节补全 `.env`（不要用 `cat`、`grep` 打印 `ADMIN_PASSWORD`：口令会留在终端回滚和 shell 历史里），然后构建并启动：

```bash
ART_AI_SOURCE_COMMIT=$(git rev-parse HEAD) docker compose up -d --build --wait
```

- `ART_AI_SOURCE_COMMIT` 把提交写进镜像，`/api/version` 与诊断包里的 `revision` 才是真实的提交 SHA（否则为 `unknown`）。没有 `.git` 的源码包里 `git rev-parse HEAD` 会失败：换成完整的 40 位 SHA，或去掉这个前缀。
- `--wait` 等到健康检查通过才返回，启动失败时直接报错。常见报错与处理见 README 的「启动失败时」。
- 登录：浏览器打开 `http://127.0.0.1:13333/login`，用户名是 `ADMIN_USERNAME`（默认 `admin`），密码是 `.env` 里的 `ADMIN_PASSWORD`。

## 3. 运行配置

除下表外，`.env` 里的其余项都有可用的默认值。`.env.example` 是模板，改完 `.env` 后用 `docker compose up -d --wait` 让容器读取新值（`docker compose restart` 沿用旧环境，不会读取）。

| 变量 | 取值 | 要求与说明 |
|---|---|---|
| `SESSION_SECRET` | `openssl rand -base64 48` 的输出，至少 32 字符 | **必填**。签发登录会话；更换后所有人需要重新登录 |
| `ADMIN_PASSWORD` | 至少 6 个字符，建议 `openssl rand -base64 24` | **必填**。首次启动时创建首个管理员（`ADMIN_USERNAME`，默认 `admin`） |
| `PROVIDER_MODE` | `live` 或 `mock`（默认） | `live` 才调用真实上游，并消耗对应账户的积分或费用 |
| `<ID>_API_KEY`、`<ID>_BASE_URL` | 如 `MESHY_API_KEY`、`TRIPO_API_KEY` | 只填要用的 Provider，设置了 key 才启用，见 [Provider 配置](provider-configuration.md) |
| `LLM_BASE_URL`、`LLM_API_KEY`、`LLM_MODEL` | OpenAI 兼容、能读图的对话模型 | `AGENT_MODE` 默认 `live`，要用 Agent 就必须配置 |
| `LOG_LEVEL` | **`debug`** | **统一要求**。`.env.example` 默认写的是 `info`，复制模板后要改掉；可选 `debug`/`info`/`warn`/`error`，写错服务拒绝启动 |
| `LOG_DIR` | **`/app/data/logs`** | **统一要求**。把同一份 JSON 日志另写成滚动文件 `art-ai.log`（数据卷内，目录不存在时自动创建）；不设就没有日志文件，诊断包里也没有日志 |
| `LOG_FILE_MAX_MEGABYTES`、`LOG_FILE_MAX_FILES`、`LOG_RETENTION_DAYS` | 默认 `50`、`60`、`30` | 可选。单个文件大小（MiB）、滚动文件个数、保留天数，都必须是正整数 |
| `UPSTREAM_CAPTURE_DIR` | 默认空，即**关闭**；开启时用 `/app/data/upstream-capture` | 出问题时才开，见第 6.2 节；必须是容器内的绝对路径 |
| `UPSTREAM_CAPTURE_MAX_RECORDS` | 默认 `2000`，范围 1–100000 | 录制文件条数上限；只在设置了目录时生效 |
| `UPSTREAM_CAPTURE_MAX_MEGABYTES` | 默认 `64`，范围 1–4096 | 录制总大小上限（MiB）；只在设置了目录时生效 |

- **变量名以源码为准**：自部署的 `docker-compose.yml` 把 `.env` 注入容器，日志级别就是 `LOG_LEVEL`。`GO_LOG_LEVEL` 是内部发布栈的 `docker-compose.go-production.yml` 里的 Compose 变量（映射成容器里的 `LOG_LEVEL`），写进自部署的 `.env` 不起作用。
- **日志要从一开始就配好**：日志文件只从设置 `LOG_DIR` 之后才开始写，`docker compose logs` 里更早的内容不能事后补进诊断包。
- `LOG_DIR` 不可写时服务拒绝启动，日志写「日志目录不可写」。
- `.env` 含密钥：权限 `0600`，不进 Git，不发给任何人。

## 4. 健康检查

```bash
docker compose ps                                                              # art-ai 应为 healthy
curl http://127.0.0.1:13333/readyz                                             # {"service":"art-ai-go","status":"ok"}
curl http://127.0.0.1:13333/api/version                                        # {"version":"<完整提交 SHA>"}
docker compose exec art-ai grep -c debug-log-ready /app/data/logs/art-ai.log   # 至少为 1
```

- `/readyz` 在数据库读写检查失败时返回 503；`/healthz` 只表示进程存活。Compose 的健康检查每 10 秒访问 `/readyz`。
- `/api/version` 返回构建时写入的完整提交 SHA（没有传 `ART_AI_SOURCE_COMMIT` 时为 `unknown`）。报告问题时一并给出它。
- 最后一行确认 DEBUG 日志真的写进了日志文件：服务在日志配置完成后写一条 DEBUG 标记 `debug-log-ready`。刚启动后检查；运行很久后这一行可能已滚动进旧文件。结果为 0 说明 `LOG_LEVEL` 不是 `debug` 或 `LOG_DIR` 没生效。
- 容器输出的是同一份日志：`docker compose logs -f art-ai`。
- 建议部署完成后空跑一次诊断包导出（第 6.3 节），确认链路通畅：应以退出码 `0` 结束，摘要里 `logs` 一行有条数。`PROVIDER_MODE=mock` 下即可，零费用。

## 5. 升级与回滚

**升级**：

1. 与维护者确认目标版本，`git fetch origin`，`git checkout <目标版本>`。
2. 备份（见下）。
3. `ART_AI_SOURCE_COMMIT=$(git rev-parse HEAD) docker compose up -d --build --wait`。必须带 `--build`：只写 `docker compose up -d` 不会用新代码重新构建镜像。
4. 做第 4 节的健康检查，`/api/version` 应是目标版本的完整 SHA。

**备份**：升级前备份 `art-ai-data` 与 `art-ai-storage` 两个命名卷（`docker volume ls` 里带 Compose 项目名前缀，默认是 `art-ai_art-ai-data`、`art-ai_art-ai-storage`）。数据库以 WAL 模式运行，要得到一致的备份，先 `docker compose stop art-ai`，备份完成并确认可读后再升级。备份方式按你们的惯例，本文不规定。日志与录制也在 `art-ai-data` 里，会随卷进入备份。

**迁移与回滚的规则**：

- 数据库迁移在新版本首次启动时自动执行，只向前：仓库里没有反向迁移。
- 旧版本不能打开已被新版本迁移过的数据库，会拒绝启动，日志写 `数据库 migration 版本 <N> 高于当前二进制 <M>`。
- 所以升级含单向数据库迁移，**回滚依赖升级前的备份**。两个版本之间是否含迁移取决于它们的差异；无法确认时一律按「含」处理。
- 回滚：`docker compose down`（不带 `-v`）→ 用升级前的备份还原两个卷（先把卷里现有内容移走，不要叠加还原）→ `git checkout <旧版本>` → `ART_AI_SOURCE_COMMIT=$(git rev-parse HEAD) docker compose up -d --build --wait` → 第 4 节的健康检查。
- 没有升级前的备份就没有回滚，只能向前升级到修复版本。动手之前，先按第 6 节导出诊断包保留现场。
- `docker compose down -v` 会删除两个卷，连同数据库、资产、日志和录制；任何时候都不要在出问题后先执行它。

## 6. 出问题时

先确认运行的版本包含 #234（源码里有 `internal/diagbundle/` 目录）。更早的版本不识别 `diagnose`，会忽略它并尝试再启动一个服务进程，不要在其上执行本节的命令。

### 6.1 先保留现场、记下信息

- 不要 `docker compose down -v`，不要清理 `art-ai-data`。
- 记下：出问题的大致时间（注明时区）、做了什么操作、界面上的「错误 ID」（形如 `req_…`，对话发送、去背景提交等失败的提示会附上它）、`/api/version` 的 SHA。
- 不要用 `docker compose config`、`docker inspect` 的输出代替诊断包：它们会展开 `.env` 里的密钥。`docker compose logs` 的输出也不经诊断包的脱敏，不要原样发出。
- 构建阶段就失败（没有镜像、服务从未运行）时诊断包不适用：把 `docker compose build` 的终端输出发给维护者，发出前先检查其中没有内部地址或凭据。

### 6.2 开启录制（默认关闭）

日志与失败摘要能说明「什么失败了」；上游当时返回的结构，要靠录制。录制只记开启之后发生的上游调用，所以先开，再复现问题。每次上游调用写一个脱敏后的文件，只含结构（字段名与类型、状态码、错误码、状态词、模型名），不含密钥、提示词、媒体与用户数据，见第 7 节。

1. 在 `.env` 里加上：

   ```dotenv
   UPSTREAM_CAPTURE_DIR=/app/data/upstream-capture
   # UPSTREAM_CAPTURE_MAX_RECORDS=2000
   # UPSTREAM_CAPTURE_MAX_MEGABYTES=64
   ```

   目录放在数据卷里，进程启动时以 `0700` 创建，不需要额外挂载。想把目录挂到宿主机，需要覆盖文件和目录属主设置，做法见 [部署入口](deployment.md)「上游调用脱敏录制（#233）」。
2. `docker compose up -d --wait`（不是 `restart`）。
3. 确认已开启：启动日志有一条 INFO「上游调用脱敏录制已开启」。

   ```bash
   docker compose logs art-ai | grep "上游调用脱敏录制已开启"
   ```

   录制目录不可用（不是绝对路径、不可写、是符号链接，或与 `STORAGE_ROOT`、`WEB_ASSETS_ROOT`、`LEGACY_GENERATED_IMAGES_ROOT`、`LOG_DIR` 相同、包含或位于其中），或上限无效时，服务拒绝启动，原因写在 `docker compose logs art-ai`，不会出现「以为在录、其实没录」。管理员登录后，也可以在浏览器打开 `/api/admin/diagnostics`，看 `upstream_capture` 的 `enabled`、`records` 等计数（只有计数与上限，没有任何上游内容）。
4. 复现问题。录到的文件：`docker compose exec art-ai ls /app/data/upstream-capture`。`PROVIDER_MODE=mock` 下 Provider 不调用上游，不产生它们的记录。
5. 超过条数或总大小上限时，最旧的记录会被删除，占用有界。

### 6.3 导出诊断包

`art-ai diagnose` 是同一个程序里的子命令，只读：数据库用只读连接打开，日志与录制目录只列出和读取，不启动服务、不迁移、不改任何已有文件，也不覆盖已存在的输出，只新写一个 `0600` 的 `tar.gz`，所以服务运行时可以直接执行。它读取服务自己的环境变量找到数据库、日志与录制目录；其余设置有问题也不影响它，所以服务起不来时它仍然可用。

```bash
docker compose exec art-ai art-ai diagnose --out /tmp/plinth-diagnose.tar.gz
docker compose exec -T art-ai cat /tmp/plinth-diagnose.tar.gz > plinth-diagnose.tar.gz
docker compose exec art-ai rm /tmp/plinth-diagnose.tar.gz
chmod 600 plinth-diagnose.tar.gz
```

- 第 1 行导出，打印摘要；第 2 行把包流式取到当前目录；第 3 行删除容器里的临时文件。包放在仓库目录之外更稳妥，避免被 `git add -A` 带进提交。
- 输出写在 `/tmp`：自部署容器的根文件系统只读，`/tmp` 是 64 MiB 的内存盘（tmpfs）。`docker cp` 不能复制 tmpfs 里的文件（见 [Docker 文档](https://docs.docker.com/reference/cli/docker/container/cp/)），所以用 `exec -T … cat` 取出。默认的体积上限（未压缩 32 MiB）下，压缩后的包不会超出它；调大 `--max-bytes` 时，改按 6.4 节的方式写到数据卷。
- 取回后核对大小与格式：摘要第一行有包的字节数，`ls -l plinth-diagnose.tar.gz` 应一致，`tar -tzf plinth-diagnose.tar.gz` 能列出内容。
- 不用 Compose 时，把 `docker compose exec [-T] art-ai` 换成 `docker exec <容器名>`（不加 `-t`）。

可选参数：

| 参数 | 默认 | 说明 |
|---|---|---|
| `--out` | 必填 | 输出文件；给已存在的目录则在其中按 `plinth-diagnose-<UTC 时间>.tar.gz` 命名；已存在的文件不会被覆盖 |
| `--since` | `24h` | 时间范围：`90m`、`7d` 这样的时长，或 RFC 3339 时间点；止于命令开始的时刻，对日志、失败任务和录制都生效 |
| `--max-bytes` | `32m` | 包内全部内容（未压缩）的体积上限，`64k` 到 `1g`，可带 `k`/`m`/`g`；超出时每类从最旧的开始截断，清单里记下 |

退出码：

| 退出码 | 含义 |
|---|---|
| `0` | 成功，包已写出 |
| `1` | 失败，如输出文件已存在、输出目录不存在 |
| `2` | 参数错误，如缺少 `--out`、`--since` 的写法无法识别 |
| `3` | 隐私扫描命中，**未写出任何文件**；错误信息只列文件、JSON 路径和规则名，不回显值。录制记录不会被改写，所以命中说明录制或目录被破坏：移走被点名的记录文件后重试，或缩小 `--since`。无法自行处理时，把这段错误信息发给维护者（它不含任何值） |

成功时的摘要（节选，数字仅为示意）：

```text
诊断包已写出：/tmp/plinth-diagnose.tar.gz（48213 字节，权限 0600）
时间范围：2026-11-15T08:00:00Z 至 2026-11-16T08:00:00Z；版本 <完整提交 SHA>
  settings  运行设置名称：62 条
  failures  失败任务摘要：3 条
  logs      日志行：1840 条
  captures  上游脱敏录制：不可用（未配置，not_configured）
```

某一类显示「不可用」时，原因只有三个固定词：`not_configured`（没设 `LOG_DIR` 或 `UPSTREAM_CAPTURE_DIR`）、`not_found`（目录或数据库不存在）、`unreadable`（读不了）。

### 6.4 服务起不来时

服务反复重启（`docker compose ps` 显示 `Restarting`）或 `exec` 进不去时，用同一份服务定义起一个一次性容器（同一个 `.env`、同一组数据卷），把包写进数据卷再取出（`/tmp` 在容器停止后就没了），最后删除：

```bash
docker compose run --no-deps --name art-ai-diagnose art-ai diagnose --out /app/data/plinth-diagnose.tar.gz
docker cp art-ai-diagnose:/app/data/plinth-diagnose.tar.gz .
docker rm art-ai-diagnose
docker compose run --rm --no-deps --entrypoint rm art-ai /app/data/plinth-diagnose.tar.gz
chmod 600 plinth-diagnose.tar.gz
```

同名容器已存在时先 `docker rm art-ai-diagnose`。数据库、日志目录和录制目录读不到时，对应类别只是标记「不可用」，导出照常完成。

### 6.5 检查并发回

```bash
mkdir -m 0700 diag && tar -xzf plinth-diagnose.tar.gz -C diag
```

1. 看 `diag/plinth-diagnose-<时间>/manifest.json`：每类的状态（`included`/`empty`/`unavailable`）、条数、`truncated`（体积上限截掉了最旧的部分）和 `dropped`（因无法安全放入而丢弃的条数）。
2. 打开 `logs/art-ai.jsonl` 与 `failures.json` 看一眼，确认没有你不愿外发的内容（见第 7 节的残余风险）。想少给就缩小 `--since` 重新导出；不要手工改包内文件，清单里的条数与 SHA-256 会对不上，录制记录被改写后也不再是证据。
3. 把包发给**与你对接的 Plinth 维护者**，走你们约定的私下渠道；不要贴进公开 Issue、Pull Request 或群聊。同时附上 6.1 记下的信息。
4. 用完即删：本地的包与 `diag/`、容器里的临时文件。

维护者收包后解包到私有目录，对照清单，把录制转成样本草稿来对齐 fake、补回归测试，只在 Issue 里记结论与脱敏后的结构，包与解包目录用完即删，不提交进仓库（见 [部署入口](deployment.md)「收到诊断包后的处理流程」）。如果你发现的是安全漏洞，不要走诊断包，按 [SECURITY.md](../SECURITY.md) 私下报告。

**关闭录制**：删去 `.env` 里的 `UPSTREAM_CAPTURE_DIR`，执行 `docker compose up -d --wait`。已写的记录不会自动删除，确认不再需要后：

```bash
docker compose exec art-ai rm -r /app/data/upstream-capture
```

## 7. 隐私说明

### 会带出什么

| 成员 | 内容 |
|---|---|
| `runtime.json` | 运行的提交 SHA、Catalog 的版本标签与 SHA-256、`APP_ENV` 标签、Go 版本与平台、生成时间 |
| `settings.json` | Catalog 声明的每个运行设置的名称、对应的环境变量名、是否机密、是否已设置、哪个变量在起作用，以及仍被设置的已退役变量名。**不含任何值** |
| `failures.json` | 时间范围内最新的失败任务（至多 200 个）：任务 ID、能力、Offering、Provider、失败分类、可否重试、公开的失败说明、上游 HTTP 状态码与错误码、各次尝试的结局；owner 只以占位符（`id-1`……）出现；另有一段截到 240 字符并再次脱敏的上游错误摘录，以及按分类、状态码、错误码的计数 |
| `logs/art-ai.jsonl` | 时间范围内的日志行（含 DEBUG），逐值脱敏后一行一条 JSON |
| `captures/*.exchange.json` | 第 6.2 节的录制记录，原样放入（录制时已脱敏）：字段名与类型、HTTP 状态码、错误码、状态枚举词、模型名 |
| `manifest.json` | 每类的状态、条数、是否被体积上限截断、被丢弃的条数、字节数与 SHA-256，以及隐私扫描结论 |

为了定位问题，任务 ID、请求 ID（`req_…`）、Offering ID、状态码、错误码、时间与耗时会原样保留。它们不含用户内容，但维护者能从中看出你们在用哪些模型与 Provider。

### 设计上不带出什么

- **密钥与凭据**：不读取 `.env` 文件，`settings.json` 只记「是否已设置」。日志与摘录里的鉴权头、令牌、`Bearer`/`Basic` 凭据、`sk-` 一类的 Provider key 形状、签名 URL 参数，以及名为 `authorization`、`password`、`session_id` 等字段的值，被替换为 `[redacted]`。录制里不保留任何鉴权头（只留 `Content-Type`、`Accept`、`X-API-Version`、`Retry-After`），URL 里的签名与 query 值也不保留（只记 query 的键名）。
- **提示词全文**：失败任务的输入只在内存里用来识别「哪些文字不能出现」，从不输出；日志与摘录里出现的同样文字被替换为 `[input]`；名为 `prompt`、`instruction` 一类字段的值整个替换；录制里提示词类字段与任何多词文本都替换为占位符。
- **图片、模型等媒体**：诊断包不读取资产存储（`art-ai-storage`）。录制里的媒体只记字节数，内容换成固定的合成文件；长 base64 与 `data:` URI 替换为 `[blob]`。
- **用户数据**：诊断包不读取用户、会话与聊天内容，数据库里只读失败任务与尝试两张表。用户名、邮箱、用户 ID 等被换成 `id-1`、`id-2`……（同一个人在包内始终是同一个占位符）或 `[redacted]`；URL 变成 `[url]`，IP 变成 `[ip]`，内网主机名变成 `[host]`，邮箱变成 `[email]`；录制里的上游地址一律写成 `example.test`。

服务写日志时本来就对所有属性做了一道脱敏（键名含 password/secret/token/api_key/authorization/cookie 的值、`Bearer`/`Basic` 凭据、`key=`/`token=` 一类赋值、Provider key 形状、URL 的 query），导出时再逐值脱敏一次。

### 两道机制

- **脱敏（替换）**：日志行与失败摘录是自由文本，逐值改写后才放进包。改写后仍被隐私扫描命中的日志行整行丢弃，按规则名计入清单的 `dropped`，不会出现在包里。
- **拒绝（不写文件）**：打包前，对每个成员按最终要写出的字节再跑一遍独立的隐私扫描（与录制器、仓库里已提交的样本用的是同一份规则）。任何命中，命令都不写文件，退出码为 `3`，只说文件、JSON 路径和规则名，不回显值。已知的用户标识或提示词仍留在日志与失败摘要里时同样拒绝。

### 残余风险

- 长度不足 8 字节的用户名，只在键名为 `username` 等的字段里被替换；自由文本里的短用户名与普通词没法区分，不替换。同理，失败任务输入里短于 8 个字符的文字不会被当作要去掉的提示词。
- 不在已知键名下的自由文本（昵称、自造词、业务词），只经正则规则处理，认不出来。
- 录制里：用户把一个词写在未被识别为自由文本的字段里时，单个短词会被当作枚举保留。
- 诊断包之外的输出没有这些保护：`docker compose logs`、`docker inspect`、`docker compose config` 与 `.env` 都不要发出。

所以诊断包仍按受保护文件对待：发出前自己打开 `logs/art-ai.jsonl` 与 `failures.json` 看一眼，只发给维护者，用完即删。

## 8. 相关文档

- [README](../README.md#docker-compose-自部署)：自部署的基础说明、启动失败的排查、同机部署多套。
- [部署入口](deployment.md)：录制与诊断包的完整参数、字段、包内容与维护者收包后的处理流程（第 6 节）。
- [自部署安全配置](self-deploy-security.md)：HTTPS、反向代理、限流。
- [Provider 配置](provider-configuration.md)：各 Provider 与 Agent 对话模型的环境变量。
- [架构文档](architecture.md)：「可选的上游调用脱敏录制（#233）」与「诊断包导出（#234）」两节。
- [SECURITY.md](../SECURITY.md)：安全漏洞的私下报告渠道。

**Partner deployment guide (#235):** this page is for outside partners who self-host Plinth with Docker Compose (the root `docker-compose.yml` and `.env`; Docker Compose v2.24+, no Go or Node needed). `SESSION_SECRET` and `ADMIN_PASSWORD` are required; the uniform settings are `LOG_LEVEL=debug` (the self-deploy Compose file reads `LOG_LEVEL`; `GO_LOG_LEVEL` belongs to the internal release stack only) and `LOG_DIR=/app/data/logs`. Capture of upstream calls is off by default: set `UPSTREAM_CAPTURE_DIR` (for example `/app/data/upstream-capture`; optional `UPSTREAM_CAPTURE_MAX_RECORDS`, default 2000, and `UPSTREAM_CAPTURE_MAX_MEGABYTES`, default 64), run `docker compose up -d --wait`, then reproduce the problem. Upgrade with `ART_AI_SOURCE_COMMIT=$(git rev-parse HEAD) docker compose up -d --build --wait` after stopping the service and backing up both named volumes; database migrations run at start-up and only go forward, and an older binary refuses a database that a newer one migrated, so rolling back needs the pre-upgrade backup. Health checks: `/readyz`, `/healthz`, `/api/version`. When something fails, run `docker compose exec art-ai art-ai diagnose --out /tmp/plinth-diagnose.tar.gz` and stream the file out with `docker compose exec -T art-ai cat` (`docker cp` cannot read tmpfs); if the service will not start, use a one-off `docker compose run` container instead. Exit codes: 0 success, 1 failure, 2 usage error, 3 privacy scan hit (no file is written). A bundle holds the running revision, the names (never the values) of the runtime settings, a summary of recent failed tasks, scrubbed logs and the captured exchanges, and never keys, prompts, media or user data; every member is scanned again as it will be written, and any finding refuses the export. Residual risk: user names shorter than 8 bytes and prompt fragments shorter than 8 characters are not replaced inside free text, so open `logs/art-ai.jsonl` and `failures.json` before sending, and send the bundle only to the Plinth maintainer you work with, never in a public Issue.
