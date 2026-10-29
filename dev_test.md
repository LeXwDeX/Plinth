# 测试验收文档

## 1. 文档定位
- 本文档用于统一管理 Plinth 的发布前测试要求、部署后端到端验收流程与分项验收标准；
- `AGENTS.md` 负责定义顶层要求与架构约束；
- 本文档负责定义“怎么测、测什么、通过口径是什么”。

## 2. 发布前自动化门禁

**产品平台（2026-10-08 用户确认）**：本项目仅面向 PC。新增工作区 UI 验收覆盖 Chromium/WebKit 的桌面视口，默认使用 1366×768、1440×900、1920×1080，并检查鼠标、键盘及缩小的 PC 浏览器窗口。历史移动验收记录保留，不作为新功能的目标设备矩阵。浏览器全新上下文、禁用缓存和同源资源检查继续必做。
- 内部环境按名称区分（具体主机、端口与目录只在本地 `deploy/local/environments.json`）：`dev-test` 为多业务共用主机上的测试栈；`legacy-formal` 为同主机隔离的正式栈（#232，部署与验收状态以该 Issue 回执为准；正式发布只晋级测试栈已完成 L0/L1/L1 Agent 及相关验收的精确 `main` revision），在 2026-10-20 新环境布局下的去留待用户确认。
- 部署按环境名进行，入口 `docs/deployment.md`（目标在本地 `deploy/local/environments.json`）。`formal-test`（正式测试环境，#270）只经 `scripts/release-env.py release formal-test --acceptance-note <noteID>` 接收已在 `dev-test` 发布成功且 L0、L1、L1 Agent 与相关验收全部 PASS 的精确 `main` revision；晋级后用 `release-env.py smoke formal-test -- <冒烟脚本>` 跑 L0、L1、L1 Agent 并核对容器 OCI revision。不做付费生成。
- LLM、OpenAI/Gemini 生图与对话的测试接线以 `production/runtime.env`
  的 `LLM_BASE_URL`、`OPENAI_BASE_URL` 为准；均指向 llms 正式服务器
  网关的 `/v1`（地址在 dev-test 运行环境文件 `LLM_BASE_URL`），不使用同机代理测试端口或历史 llms-proxy 端口。
  链路是「测试客户端 → `dev-test` Plinth 测试栈 → llms 正式服务器」，后者
  不限制客户端与模型；测试栈对话模型为字面量 `deepseek`（2026-10-19 起网关目录已无 `qwen-max`、`glm-flash`，直接请求返回 400 `model not supported by any target`；经用户确认改用 `deepseek`，网关当前路由到 `deepseek-flash`）；请求走 OpenAI 兼容 `chat/completions`。思考参数按 Product Schema `chat.agent.thinking.model_profiles` 的模型前缀发送，DeepSeek 用 `thinking.type=enabled`、`reasoning_effort=low`（用户 2026-10-20 决定；官方只有 low/high/max 三档），工具循环回传 `reasoning_content`；其他中国大陆模型仍用官方最高档。
  测试凭据仅从该测试栈已授权的运行环境读取，不用于正式栈。正式栈保持 Provider Key 为空，直至用户明确决定写入；这使远程 Provider Offering 不可用，不影响可用的本地 Offering；
- 真实大模型流量测试从测试栈 `production/runtime.env` 读取接线；正式
  生产凭据不在测试脚本、日志或报告中写出。模型列表 200 只能证明鉴权和
  目录可达；生成验收还须核对
  `Task` 状态、READY 资产、实际图片内容、刷新后的历史版本；
- #232 正式栈来源验收必须先在 test 完成 L0/L1/L1 Agent 与相关检查，再以完整 revision/image tag 写入 #232 原生 note。正式晋级后仅跑正式 L0/L1；`smoke-l1-formal-keyless.py` 显式要求不同的 `BASE`/`TEST_BASE` 与完整正式管理员凭据，`--expect-empty` 仅用于首次空库。它核对正式无 Key Offering 投影、空库（首次）、登录/登出、登出后用登录时捕获的真实 formal Cookie 验证撤销，以及该 Cookie 和改名后的同 secret Cookie 对 test 的只读跨端口拒绝（匿名 session、tasks 401），不调用 Agent、Provider 或生成任务。晋级来源报告的四项检查 `L0`、`L1`、`L1_agent`、`related` 必须各为 pass、`failed=0`、`passed>0`、没有 skip/soft-fail 且附本仓 Issue comment 的完整 URL；正式部署状态仍以 Issue 读回为准，源码或本机 fixture 不能充当线上验收。
- 本系统依赖的背景去除服务与 `dev-test` 同机（接线地址见 dev-test 运行环境文件 `MATTING_*`，项目目录只记在本地环境资料中，不写入仓库）；若该服务异常，先检查现有容器状态、项目配置与 `/health`，再按实际故障修复并验证；

### 自动化门禁项
每项后面的「」是门禁名（本地入口与 CI 作业日志里的名字），（）是执行位置：**本地** 指 `scripts/verify-local.py` 的默认运行（pre-push 即此），**CI** 指 GitHub Actions `.github/workflows/ci.yml`。

1. **TypeScript 类型检查**「frontend typecheck」（本地 + CI）：`pnpm exec tsc --noEmit` 必须零错误；
2. **单元测试**「frontend test」（本地 + CI）：`pnpm test` 必须通过；
3. **生产构建**「frontend build」（本地 + CI）：`pnpm build` 必须成功，且 `node scripts/check-build-external-links.mjs` 通过（构建产物 `build/go-web/assets` 不得含 gstatic/jsdelivr/unpkg/googleapis/字体 CDN 外链；下游内网零互联网。@google/model-viewer 的 CDN 默认值由 `vite.go-web.config.mjs` 在构建时替换为同源路径）；
4. **Go 静态检查**「go vet」（本地 + CI）：`go vet ./...` 必须零报告；
5. **Go 单元测试**「go test」（仅本地）：`go test -count=1 ./...` 必须通过；
6. **Go race 测试**「go test race」（仅 CI）：`CGO_ENABLED=1 go test -race -count=1 ./...` 必须通过（Go race detector 要求启用 cgo）；
7. **Go 构建**「go build」（本地 + CI）：`CGO_ENABLED=0 go build -trimpath ./cmd/art-ai` 必须成功；
8. **公开快照扫描**「public tree」（本地 + CI）：`bash scripts/check-public-tree.sh` 必须通过（#261：已跟踪文件不得含内网地址、内部域名、服务器路径、本机临时路径、内部 Compose 项目名或组织名称；#334 起还拒绝本机用户目录、私钥头与常见凭据前缀；本地另把未纳入 Git 的 `deploy/local/environments.json` 中的全部环境值作为禁用字面量；约 2 秒，需要 git 与 python3）；

**为什么这样分（2026-10-20 用户确认去重）**：第 5、6 项跑的是同一批 Go 测试，第 6 项多开 race detector，因而更严格也更慢（CI 约 21 分钟，本机 8–20 分钟，几个 worktree 同时 push 时还会因负载误报）。原先本地与 CI 各跑全部七项，每个 MR 要跑两次 race，CI 还多跑一次被 race 覆盖的普通 `go test`。现在：

- **race 只在 CI 跑**：它是最慢的一项，且在 CI 上与本地不争资源；本地需要时用 `python3 scripts/verify-local.py --race` 加跑（排在 `go test` 之后，与本节顺序一致）；
- **普通 `go test` 只在本地跑**：pre-push 用它在几分钟内拦住测试失败；CI 不再单独跑，因为 race 作业已执行同一批测试；
- **其余各项两处都跑**：都很快；CI 端是不依赖开发机环境的独立复核。

CI 作业分组：`frontend`（第 1–3 项，只装一次依赖）、`go check`（第 4、7、8 项；第 8 项只需 git 与 python3，放在已有作业里不多占 runner 槽位）、`go test race`（第 6 项）。三个作业放在同一 stage，同时排队、互不等待；前端几项合为一个作业，因为每个作业都要单独等一个 runner 槽位，排队时间远长于这几项本身的运行时间（2026-10-20 流水线 30897/30899：前端各作业运行 7–27 秒，却各排队 18–55 分钟，Go 各作业还要等前端阶段结束）。CI 只有这三个作业，不构建镜像（#257）。

本机门禁对第 5 项（及 `--race` 时的第 6 项）额外加 `-timeout=30m`：同一工作站并行运行多个会话的门禁时，`httpapi`、`sqlite` 等包会超过 Go 默认的单包 10 分钟上限而误报失败（2026-10-19 多次出现，单独重跑均通过）。CI 的 race 作业以作业级 `GOFLAGS=-timeout=30m` 做同样放宽（2026-10 在单台 runner 上 `httpapi` 包 race 约 596 秒，贴近 10 分钟上限）。这些参数只放宽单包超时，不改变测试集合与判定，门禁命令仍按上面所列。

部分 Generation、Agent 工具和 HTTP 集成测试通过 `internal/adapters/sqlite/sqlitetest.Open` 创建独立数据库：该 helper 在每个测试进程内共享一个惰性构建的真实迁移模板库，关闭连接并确认没有 WAL 后缓存其字节，只为不存在的目标文件复制。每次仍调用真实 `sqlite.Open` 检查迁移版本与 checksum；已有数据库原样打开，重启、恢复和篡改场景不覆盖原文件。SQLite 迁移测试继续直接执行真实迁移，不使用该模板。此优化只减少重复 fixture 初始化，不减少测试集合、断言或 race 检测，也不改变 runner 上限。

### CI：GitHub Actions（#257）

本仓库只有一处 CI：`.github/workflows/ci.yml`。2026-10-27 起 GitHub 是唯一仓库，内部 GitLab 与其 runner 不再使用（GitHub #60）。CI 执行上面「自动化门禁项」中标为 CI 的各项，作业分组为 `frontend`、`go check`、`go test race`，不另立清单：

| | GitHub Actions |
|---|---|
| 触发 | 推送到 `main`、所有 Pull Request、手动；自动事件与默认手动使用托管 runner，只有显式 `workflow_dispatch` 的 `use_self_hosted: true` 选择可信自托管 runner |
| 依赖源 | 两种路径均要求仅使用公共 npm registry（`registry.npmjs.org`）与公共 Go proxy，不访问内网主机；须分别核验运行时配置与网络，静态工作流检查不能证明实际访问 |
| 工具链 | Node 22、Go 1.27、pnpm 取 `package.json` 的 `packageManager`（与 `Dockerfile.go-runtime` 一致） |
| 作业 | 三个门禁作业，runner 由事件与 `use_self_hosted` 路由；不新增平行作业，不构建镜像（#257） |
| 用途 | 所有 PR 的门禁；不承担发布。托管 runner 受计费墙影响时，若有已核验安全与精确 ref 的自托管 runner，优先尝试；没有或不可用时按下方「主机自检」执行 |

- **对齐守门**：`src/__tests__/ci-gate-parity.test.ts`（随 `pnpm test` 在本地和 CI 运行）解析本节「自动化门禁项」、`scripts/verify-local.py` 与 `.github/workflows/ci.yml`，要求：标为 CI 的每项命令逐字出现在恰好一个 CI 作业里（同一项的多条命令在同一作业、按本节顺序），CI 作业里没有本节以外的命令；本地入口默认集合的门禁名与顺序等于标为本地的各项，`--race` 加跑的门禁名等于唯一标为仅 CI 的一项；工作流不含已知内网主机字面量且每个 Action 固定到完整提交 SHA；静态断言三个作业使用相同的固定路由表达式，并对已知事件和并发分组建模。静态测试不证明 GitHub 的实际路由、runner 隔离或网络访问。增删、改写或改变执行位置时，同一提交里改这三处，测试才会通过。
- **Action 版本**：工作流中的 Action 固定到发布标签对应的完整提交 SHA，行尾注释写标签；升级时在官方仓库核对标签与 SHA 再一起改。
- **CI 与内部验收的分工**：当前工作流的自动事件与默认手动路径选择托管 runner；PR 可以修改工作流，因此不能据此保证外部 PR 无法选择自托管 runner。自托管只应由维护者在触发前审查完整 SHA 并核验受信任 ref 和运行环境后明确触发；工作流仍以 `persist-credentials: false` 检出。零 Key 的 Mock 自部署冒烟 `scripts/smoke/smoke-public-mock.sh` 保留为本地手动验收（自部署后运行，覆盖登录、健康、一次 Mock 图片与一次 Mock 3D 任务的全部产物下载核对、restart 与 down/up 持久化及 `down -v` 清理）。维护者内部环境（`dev-test`、`formal-test`）的部署与 L0/L1/L1 Agent 冒烟经 `scripts/release-env.py release`/`smoke` 走 `scripts/smoke/` 的脚本，真实模型付费验收也只由维护者在内部测试环境执行（§3、§11），都不进 CI，也不是外部 PR 的合并条件。
- **分支保护**：不设置（2026-10-27 维护者决定）。仓库为私有、免费套餐不支持分支保护；合并前的门禁由本节与「交付硬门禁」的本机预检保证。

### 可信自托管 runner（#79 第一阶段）

`workflow_dispatch` 提供布尔输入 `use_self_hosted`，默认 `false`。只有该输入为 `true` 时，三个门禁作业才用仓库级 runner 的固定标签集 `[self-hosted, Linux, X64, plinth-trusted-ci]`；`push`、`pull_request` 与不带输入或 `false` 的手动运行都保持 `ubuntu-latest`。触发器、门禁命令、`permissions: contents: read`、Action 的完整 SHA 与 `persist-credentials: false` 不随路由改变，也不新增 shell 拼接或作业。

- 维护者须在触发前审查完整提交 SHA，并核验所选 ref 与运行环境可信；分支名不能代替 SHA。运行后逐个作业对照实际 `head_sha`、runner ID、checkout 与步骤结论，只是事后证据，不能阻止已执行的不可信代码。
- YAML 条件与标签不是安全边界：有写权限的人仍可选择分支或标签，PR 也可以修改工作流。仓库当前为私有；转公开前必须证明平台级隔离（包括业务凭据、Docker socket、数据库、资产存储和其他栈），或阻止不可信工作流到达该 runner。尚未证明，不得称其安全。
- 两种路径都要求只访问公共依赖源且不访问内网。工作流文本和静态检查仅能排除列出的已知内网字面量，不能证明 hosted runner 上 Action、测试及依赖的实际网络访问；自托管 runner 的配置、依赖源及网络访问目前也无证据。两种运行环境都须分别核验。
- 显式自托管 dispatch 在 ref 前加固定 `trusted-` 标识，使用独立并发组，避免与其他 ref 的 hosted 运行碰撞；自动路径和默认 dispatch 保持原有分组，PR 仍按编号分组并取消进行中的同组运行。GitHub 默认同组至多一个 running 和一个 pending，新 pending 可替换旧 pending；上述分组目前仅做静态验证，未实测 GitHub 调度。
- runner 在线、标签匹配都不证明工具链与调度可用。缺 Node/pnpm、Go、`CGO_ENABLED=1` 的 race 工具链、磁盘或网络时，作业失败按失败记录，不得跳过门禁或记为通过。
- 自托管 CI 可以作为并行工作流中的独立节点，与不修改待检查 revision 的工作并行；最终集成、发布、真实付费验收和部署后回归仍须串行，不能因 CI 节点并行而并行执行或跳过。真实调度、平台级隔离与工具链须另行核实，不能据静态检查宣称已验收。

### 主机自检替代 CI（#79）

托管 runner 遇到计费墙时，若存在自托管 runner 且已核验其安全与精确 ref，优先尝试该 runner。若没有自托管 runner 或其不可用（包括安全隔离等前提未证明），直接先对本机同一精确提交 SHA 做快速初检；初检失败就停止，不送主机。通过后，在环境文件登记的 `dev-test` 主机对同一 SHA 执行与本节 CI 同集合的完整隔离自检，结果作为 CI 的替代证据。任何实际使用的 runner 和该主机都须先满足安全前提（含平台级隔离、受信任 ref/SHA、公共依赖源与网络）；Git hooks、本机 `--race`、来源身份、隔离和下方独立的合并、发布和部署后门禁不因此跳过。作业因代码原因失败时不进入该替代路径，仍按失败处理，不得放行或记为通过。

- 只运行已审查的精确提交。先核对本机提交 SHA 和实际字节，并执行包含本地环境禁用值的公开树扫描；源代码只经该提交的有界 Git bundle 传输，不包含其他 refs、本地环境、密钥或数据。主机独立检出后，以及检查开始和结束时，核对完整 SHA、Git 树及跟踪字节。仅工作区差异的预演不能替代最终提交的证据。
- 主机与其他业务共机。每次使用新建的私有临时目录和独立容器，不挂业务目录、数据库、资产存储、宿主 HOME、SSH 凭据或 Docker socket，不修改其他栈。执行前核查可用内存、磁盘和运行负载；容器限制 CPU、内存与 swap，测试阶段不接外网。依赖预取阶段只执行已审查的下载命令，使用公共源，不运行项目安装脚本；不可信代码不能以此在共享主机运行。
- 工具链须与 CI 的 Node、Go 和 pnpm 版本对应。复用 `python3 scripts/verify-local.py --race --force` 的完整门禁并单独执行 `node scripts/check-build-external-links.mjs`；不能使用空的 `--pre-push` 输入冒充完成。依赖缺失、源码变化、失败、超时、OOM、失联、缺项或跳过均不得放行。
- 在 Issue 或 PR 记录 SHA、工具链与镜像身份、资源限制、各门禁的实际结果和失败原因；只称“主机自检”，不写成 GitHub Actions success。只清理本次明确创建的临时资源，不做广域 prune，也不清理或重启其他栈。隔离、预检、SHA 绑定与清理的实现见下节「主机 CI」的 `scripts/host-ci/run.py`，结果 JSON 就是要记录的证据；此前临时目录里的手工 stage/launch 流程不再使用。

### 主机 CI：干净容器运行门禁（#97）

在干净容器里对精确提交跑本节全部门禁。GitHub Actions 不可用期间（计费，#79），它是本项目 CI 的正式替代：沿用 #79 的裁定，即上节「主机自检替代 CI」的规则不变，本节是它的可重复实现，取代此前临时目录里的手工 stage/launch 流程。它同时是本地 TDD 的干净复现环境，不受本机工具链、缓存和未提交文件影响。

```
python3 scripts/host-ci/run.py HEAD          # 或任一提交 / 完整 SHA
```

- **做什么**：把该提交（只有该提交可达的历史）打成 Git bundle，传到主机上专用的目录（环境文件 `dev-test` 条目的可选字段 `host_ci.dir`，缺省 `/var/lib/plinth-ci`；与 Plinth 发布根和各环境的受保护路径不得重叠，否则拒绝），在一次性容器里执行 `GOFLAGS=-p=4 python3 scripts/verify-local.py --race --force`，再单独执行 `node scripts/check-build-external-links.mjs`，把 `result.json` 与日志取回 `build/host-ci/<run id>/`（已被 `/build` 忽略）。只测已提交的内容：工作区未提交的改动不在其中，`HEAD` 且有未提交改动时会提示。退出码：0 通过、1 失败、70 主机资源不足、75 另一次运行占用、2 用法或环境文件错误。
- **与本机门禁的关系**：门禁集合和顺序仍只由本节「自动化门禁项」和 `scripts/verify-local.py` 决定，容器里运行的就是它，不另立清单、不改命令。本机 `verify-local.py` 与 Git hooks 照常执行，主机 CI 不替代 pre-push，也不让本机 `--race` 免跑。结果只称“主机 CI 结果”，不写成 GitHub Actions 的成功。
- **镜像 `Dockerfile.ci`**：Node 22 与 Go 1.27（两个基础镜像按摘要固定）、pnpm 取 `package.json` 的 `packageManager`，并把 pnpm store 与 Go 模块缓存预取进镜像，所以容器可以 `--network none`。构建只访问公共来源（Docker Hub、`registry.npmjs.org`、`proxy.golang.org`），不依赖内网，任何有 Docker 和公网的 runner 都能直接构建。`verify-local.py` 不需要浏览器，镜像不含 Playwright 浏览器。在测试主机上标签是 `plinth-ci:<12 位 hash>`，hash 由 `Dockerfile.ci`、`scripts/host-ci/entry.sh` 和被测提交的 `package.json`、`pnpm-lock.yaml`、`pnpm-workspace.yaml`、`go.mod`、`go.sum` 决定；这些不变就复用已有镜像，不重建，变化时才联网构建一次（几分钟）。入口在每次运行时再核对容器里的 Node、Go、pnpm 与 `.github/workflows/ci.yml`/`package.json` 一致，不一致即失败（退出 22）；将来 CI 的版本声明搬家时，这条核对要随之改读新的位置。`python3 scripts/host-ci/run.py <sha> --print-image-tag` 只打印标签。
- **容器**：`--network none`、4 CPU、12 GiB 内存且 swap 等于内存（即无 swap）、非 root（UID 10001）、移除全部 capability、`no-new-privileges`、只读根文件系统；不挂业务目录、数据库、资产存储、宿主 HOME、SSH 凭据或 Docker socket。挂载只有：只读的 `source.bundle`，可写的绑定目录 `/scratch`（HOME、Go 构建缓存、pnpm store 副本）和 `/out`（结果），以及两个 tmpfs：`/work`（容器内从 bundle 新检出，上限 6 GiB）和 `/tmp`（4 GiB）。两个 tmpfs 的占用计入 12 GiB 内存上限。`/work` 和 `/tmp` 不能是绑定挂载：`internal/testutil/wirefixture` 的测试会拒绝位于非根绑定挂载下的工作树或证据目录。“源码只读”指输入 bundle 只读；门禁必须能写 `node_modules` 和构建产物，所以 `/work` 可写，入口在检出后和结束时都核对 HEAD、tree、工作区干净与 `source_identity` 未变，变了即失败（退出 21）。`docker create` 之后，主机再按 `docker inspect` 核对上述参数（网络、内存、swap、CPU、只读根、用户、capability、挂载），与请求不符就不启动。
- **并发与清理（测试主机）**：主机上 `<目录>/.lock` 用 `flock`，同一时刻只允许一个运行，第二个直接失败（退出 75）。开跑前先做资源预检：`MemAvailable` 至少 15 GiB（容器上限 12 GiB 加余量）、目录和 Docker 根目录所在磁盘各至少 10 GiB 空闲，不足就停下并在结果里写明（退出 70），不启动容器。清理只针对本工具创建的对象：带 `plinth.host-ci=1` 标签的容器（崩溃遗留的也在持锁后清掉）、`<目录>/run/<run id>` 下的 bundle 与工作目录（超过 24 小时的遗留也清）、`<目录>/results` 只留最近 10 次。旧镜像保留最新 2 个 `plinth-ci:<hash>`（含本次所用的），其余按精确名称 `docker rmi`，失败只警告。不 prune，不删卷，不动其他 Compose 项目、容器和 Plinth 发布根。
- **结果 JSON**：`sha`、`tree`、`verdict`（pass/fail/not_run）与原因、预检读数（可用内存、swap 用量、磁盘）、镜像（标签、ID、是否本次构建、构建秒数）、每次尝试的容器退出码、`OOMKilled`、入口退出状态、各步骤与各门禁的结果和耗时（未运行的门禁标 `not_run`，不会被当作通过）、cgroup 的 `memory.peak` 与 `memory.events`（`oom_kill`）。通过的条件是：容器退出 0、未被 OOM（`OOMKilled` 与 cgroup `oom_kill` 计数都为 0）、没有超时、入口报告 `passed` 且每个门禁都是 `pass`。
- **抖动**：启动器曾偶发“创建的身份/配置与请求不一致”，检出的提交/tree 与期望不符同属此类。遇到时自动用全新的暂存目录重试一次，首次尝试的证据（`attempt-1/` 下的创建配置、容器日志、结果）保留在结果里；第二次仍不一致就按失败处理，不再重试。门禁本身失败（测试红、类型错误等）不重试。
- **不做 GitHub self-hosted runner（决定）**：公开仓库的 fork PR 可以改工作流，把任意代码送到内网主机上执行，风险不可接受（#79 的结论）；本工具由维护者手动对已审查的提交触发，不监听任何平台事件，也不注册 runner。
- **换 runner 时复用**：门禁逻辑只在 `scripts/verify-local.py` 和 `Dockerfile.ci` 构建出的镜像里，`run.py` 与 `host_run.py` 只是把镜像送到测试主机运行的薄封装。别的 runner 做三件事：`docker build -f Dockerfile.ci -t plinth-ci .`；按 `scripts/host-ci/entry.sh` 文件头列的挂载（只读 bundle、`/work` 与 `/tmp` tmpfs、可写的 `/scratch` 与 `/out`）和 `CI_SHA`、`CI_TREE` 两个变量 `docker run`（`host_run.py` 的 `container_command` 是现成的参数清单；`/scratch`、`/out` 要让 UID 10001 可写）；读 `/out/result.json` 与退出码。把镜像直接用作某平台的 container job 没有验证过：镜像以非 root 用户运行，各平台对容器用户的要求不同。
- **边界**：只证明被测提交在该镜像、该资源上限下通过了本节门禁；不代替 Git hooks、本机 `--race`、独立评审、`dev-test` 发布和发布后 L0/L1/L1 Agent 回归。bundle 带有该提交可达的全部历史，传输前依赖仓库的公开快照检查（`scripts/check-public-tree.sh`）保证历史不含内部标识。镜像构建需要访问公共 npm registry 与 Go proxy，只在依赖或 Dockerfile 变化时发生；运行阶段无网络。纯逻辑的离线单测：`python3 -m unittest scripts/host-ci/test_host_ci.py`（不在门禁清单内）。

### 官方 API 契约驱动的 TDD 与 CI
- 每个受影响的上游端点，在编写或调整该端点的测试前，先查该端点当前官方文档；记录官方 URL、核对日期、端点/API 版本、模型版本（适用时）及已知文档冲突。不要从一个端点推断其他端点使用相同版本。文档无法访问或信息冲突时，记录未知项并暂停对该项的契约推断。
- 测试说明须区分三类依据：官方协议明文、项目为安全/交付设定的更严格要求、真实运行观察；未知项单独标记。真实观察不能冒充官方明文或泛化为完整协议；官方示例与实际响应冲突时分别留证，先核对实际端点与版本，再以经独立复核的有界兼容和对应回归处理，未证实的结构仍标为未知。项目约束也不能冒称上游要求。
- 新行为按 test-first 执行：先写能在当前实现偏离合同或项目约束时失败的测试，再修实现。已有实现的端点补测试时，明确标注为追补覆盖，不得描述为 test-first。测试的预期值必须来自已核对的官方规范、经独立核对并明确标注范围的真实协议证据，或独立审查的项目约束，不能从被测实现、Product Schema 或其输出自动生成后再验证自身。
- 离线契约测试应在 HTTP 边界检查实际发出的 path、method、headers、body 类型与字段/枚举组合，以及阶段和来源；并检查响应状态、输出形状与价格/费用字段（适用时）。覆盖合法组合和有依据的边界，不因 Adapter 返回成功就省略 wire 断言。
- 官方示例须脱敏并记录出处与版本；真实响应 fixture 须经审查、脱敏，并标记端点、API/模型版本、采集日期和适用范围。未知结果、不支持的能力及越权请求用负控覆盖；负控不得通过重放真实付费请求制造样本。Fixture 是离线测试资料，不证明当前上游仍返回相同内容。
- 将这些离线契约测试纳入现有 `go test -count=1 ./...` 与 `CGO_ENABLED=1 go test -race -count=1 ./...`；它们随现有门禁运行（普通测试在本地，race 在 CI），不新增平行门禁清单，也不把 CI 改为联网或逐次付费调用。本文规定应覆盖的方法，不代表目前每个上游端点都已有此类测试；实际覆盖范围和缺口须以关联 Issue/MR 的源码与测试核查结果为准。
- 已提交的真实 wire 样本（`*.candidate.json`）另有两项离线检查，同样随现有 `go test` 运行，不新增门禁项：`internal/testutil/wirefixture` 的 `TestWireCoverageMatrix` 校验由 Catalog 和样本生成的覆盖矩阵 `testdata/wire-coverage.generated.json`（样本或 Catalog 变化后用 `UPDATE_WIRE_COVERAGE=1 go test ./internal/testutil/wirefixture -run Coverage` 重新生成并随同提交，不手改），`TestCommittedWireCorpusHasNoPrivateData` 重扫样本、meta、policy 与矩阵的隐私。真实采集必须在干净工作树进行。缺真实样本的 Offering 在矩阵里列为 `missing`，不用合成数据填充；规则与限制见 `docs/architecture.md`「Wire 黄金样本、覆盖矩阵与全栈离线回放」。
- `dev-test` 测试栈的真实付费验收仍按既有授权和 §11 流程执行，并核对实际请求体、精确 revision、必需产物及鉴权交付/UI；正式栈只运行免费 L0/L1，付费生成须用户明确决定。上游 `service_unavailable` 等失败不能据此排除本地请求错误；先独立复核该端点官方合同和实际请求证据，再定位原因。不得用盲目重试、放宽断言或修改 mock 期望来掩盖失败。

- Fake 上游（#96）的离线检查同样随 `go test -count=1 ./...` 与 race 运行，不新增门禁项，因此已在本节的本地与 CI 门禁里：`internal/testutil/fakeupstream` 下各版本的 `TestDriftFakeResponsesMatchTheRealSamples`（fake 响应的字段集合与类型必须与该版本最新真实样本一致）、`TestEndpointTableIsCompleteAndHonest`、`TestAlignmentReportIsCommittedAndClean`（对齐报告无未说明差异）、每个故障场景的回归测试、产物字节的 Plinth 交付检查，以及 `TestProductionBinariesDoNotLinkFakeUpstream` / `TestRuntimeImageBuildsOnlyListedCommands`（fake 不进任何发布二进制和镜像）。用法见下节「Fake 上游自检」。

### 自动化门禁说明
- **本节是发布前门禁清单的唯一真源**：`AGENTS.md`「提交前门禁」章节及
  发布操作均以本节为准，不得另行维护平行清单；
- 远端 CI 只有 GitHub Actions（`.github/workflows/ci.yml`，见上节「CI」）；指定主机自检是按上节条件提供的替代证据，不是 GitHub 作业。交付须核对最终待发布 `main` 精确 SHA 的完整 CI 或合规主机自检结果；
- `gofmt` 格式检查与 `go mod verify` 不在门禁内：两者属格式/依赖完整性守门，不影响功能正确性，本地与 CI 门禁合起来覆盖 vet/test/race/build 已足以拦截功能性回归，需要时手动执行；
- 任一失败则中止部署，必须修复后重试；
- 单元测试配置：`vitest.config.ts`；
- Mock 基础设施位于 `src/__tests__/mocks/`；
- 本地 Mock 栈（`PROVIDER_MODE=mock`、`AGENT_MODE=mock`，仅开发与测试）按真实形态交付：图片是按宽高比和尺寸档程序化绘制的 PNG（长边不小于 768，颜色随提示词、标签含能力与序号，同一提交键字节相同）；3D 与处理 Offering 交付 Catalog 声明的全部格式（GLB、glTF、OBJ+MTL、STL、FBX、USDZ、3MF、贴图），字节通过 `internal/assets/local` 的交付检查，任务可以真正成功；冻结后 `minimum` 为 0 的条件必需槽位（如四视角编辑里没点名的视角）不交付，四视角编辑的来源（完整的四视角生成 Task）由 Mock 的只读依赖绑定投影解析，其他 `task_source_binding: provider` 来源在 Mock 栈上仍被拒绝；3D 约 8 秒、图片不到 1 秒，让进度和“已完成”提示可见。提示词含 `[mock:insufficient_balance]` 时 Provider 返回余额不足分类，含 `[mock:slow]` 时任务用约 20 秒逐步推进度，含 `[mock:duplicate_remote_id]` 时所有这类提交都拿到同一个上游任务 ID（#298）：第一个正常完成，后来的以 `submission_unknown` 和 Schema 文案 `upstream_job_conflict` 结束，服务不退出。含 `[mock:texture_padding]` 时结果内嵌贴图在 PNG IEND 后多 1 个对齐空格，由它（按上游任务或已存 GLB）转换出的文件也带同一张贴图，交付检查接受；含 `[mock:convert_rejected]` 时结果本身干净，但它的每次转换都带 IEND 后有真实尾随数据的贴图，上游完成并扣费后交付检查拒绝，用于复现免费取回与再次计费重试（#330）；含 `[mock:convert_rejected_once]` 时原转换任务的交付与重试同样被拒绝，之后的免费取回拿到干净文件并成功，用于复现取回成功后的状态（#330 第二轮）。含 `[mock:markdown]` 时 Mock Agent 不调用工具，分段流式回复一段固定的 Markdown（粗体切在两段之间，含列表、代码块、http/https 链接与 `javascript:`/`data:` 链接、`<script>`、`<img onerror>`、HTML 实体和图片语法；句中写了句柄时最后一行回显），用于对话 Markdown 子集与 XSS 的浏览器验收（#333，`scripts/test-chat-markdown-333.mjs`）。Mock Agent 按用户这句话的关键词选工具（3D/模型→`create_model_3d(_from_image)`，三视图→`create_three_view`，四视角→`create_multiview_images`（参考图取句柄、当前选中或随这句话上传的图），四视角加明确的编辑动词（编辑/修改/改成/换成/改为）且句中没有“生成”→`edit_multiview_images`（句中点名的正面/左/背面/右各带一条说明，没点名只改正面），换贴图→`retexture_model`，重拓扑/绑骨/动画→`process_model`，去背景→`remove_background`，记住/设定→`update_session_notes`，其余→`refine_image`），工具名、参数和批准路径沿用真实契约。图改图会读参考图：带蒙版的局部重绘只改蒙版透明的区域，其余像素与原图一致；同比例的参考图改色后交付（构图不变）；白底草图只当构图参考；去背景真的抠掉背景；图生 3D 的模型颜色随输入图的主色。用户话里列了“出 3 个天气：雪夜、正午、雾”这样的变体时，Agent 为每个变体各发一次 `refine_image`。Mock Agent 也遵守会话的 Agent 范围：2D 模式下说“做个 3D 模型”（或 3D 模式下说“修改这张图”）得到“当前模式不允许这个请求”而不是工具调用，自动模式照常。Mock Agent 的收尾句按工具结果写：生成类工具执行过的步骤返回 JSON 结果；服务端没提交就拒绝的步骤（没为该领域选模型、本轮次数上限等）只返回一句文字，这时以 `mock_failure_summary`（“这一步没有完成：…”，按该轮语言）收尾，不说已完成；只保存了设定摘要的一轮以工具自己的确认文字收尾（#251 自部署演练发现：未选 2D 模型时曾回复“图片任务已完成”却没有任务）。这些记号和关键词只在 mock 适配器里生效，live 配置不会构造它们。
- **CSP 门禁分两半，缺一不可**：门禁一
  `internal/transport/webui/shell_csp_test.go` 以 `os.DirFS("../../../web")`
  挂载生产真实 shell（不用内存副本，模板改动必须直接触发断言），断言响应头
  `script-src` 含按响应内容算出的脚本 `sha256`；其中
  `TestRealWebShellScriptIsCoveredByCSP` 直接读 `web/index.html`，守住真实
  shell 只能有**一个内联**脚本、且外链脚本必须同源（引入 CDN 脚本须先在
  Schema 显式声明来源）。门禁二 `scripts/smoke/smoke-l0-browser-boot.sh`
  用真实浏览器加载页面，验证 CSP 未拦截且工作区可渲染——单测只证明头里有
  哈希，不证明浏览器真的执行了该脚本。两半都在 `go test` 与发布后 L0 冒烟内，
  缺门禁二就会漏掉"头看着对、页面实际是白屏"这类故障；
- 以下安全不变量已由 Go 测试固化为门禁，改动相关代码须一并通过：
  `internal/app/compose_environment_test.go`（compose 注入的环境变量必须
  真的有代码读取，防死配置复发）、`internal/transport/httpapi/all_tasks_scope_gate_test.go`
  （`generation.AllTasks()` 只能在显式权限判断之后出现，防跨 owner 越权读）、
  `internal/administration/*_test.go`（最后一个管理员保护、邮箱身份校验）；
- `dev-test` 验收账号与密码只从测试栈 `runtime.env` 读取，不在仓库文档或命令
  输出中复制；
- 部署之后，需要覆盖验证 `MeshyAI Provider 以外` 的自定义接入功能链路。

### Fake 上游自检（#96）

开发阶段的零成本、离线、可重复自检：不付费调用 Tripo、Meshy、Hyper3D 或 LLM 网关，也不用 Mock 绕过 Adapter。fake 只在本机运行，不进入任何部署栈，也不替代真实付费验收（L2 与真实模型验收仍按 `scripts/smoke/README.md`）。设计与边界见 `docs/architecture.md`「Fake 上游服务」。

- **整栈一条命令：** `python3 scripts/smoke/smoke-fake-stack.py`（可加 `--only balance,chain,meshy,hyper3d,agent,faults`、`--keep --work DIR`）。它构建并启动 `cmd/fake-upstream`，再以 live 模式启动本机隔离栈（端口 19410/19411，可用 `FAKE_STACK_FAKE_PORT`、`FAKE_STACK_PORT` 改），栈的 `TRIPO_BASE_URL`、`MESHY_BASE_URL`、`HYPER3D_BASE_URL`、`LLM_BASE_URL` 指向 fake，逐项核对：Tripo、Meshy、Hyper3D 的余额面板；Tripo 生成 → rig-check → rigging → animation、segment → complete 走到 SUCCEEDED；Meshy 文生 3D（preview + refine）→ remesh → rigging → animation、retexture、convert；Hyper3D 文生 3D → Bang，必需槽位、字节大小与 SHA-256、交付头都通过；Agent 对话 → 工具调用 → 批准 → 3D 任务 → 回复；故障场景（`insufficient-balance`、`key-quota`、`task-failed`、`artifact-corrupt`、`artifact-404`）下三家公开 Task 的终态与错误 class。全程只用回环地址和随机临时目录，不读取任何真实 Key，不访问 dev-test 或正式环境。
- **D 类恢复链重放：** `python3 scripts/smoke/replay-recovery-on-fake.py`（`--keep --work DIR` 保留现场）。它先用 fake 栈生成来源 Task，再对隔离夹具运行 `run-controlled-acceptance.py` 的 `chain` 与 `lost-response`（中间 Rigging 失败与显式重试、受理后丢响应与对账），不依赖真实 Meshy 余额，也不替代真实上游验收。端口默认 19412（`FAKE_RECOVERY_PORT`）。
- **手工使用 fake：** `go run ./cmd/fake-upstream -addr 127.0.0.1:19000`，把 `TRIPO_BASE_URL=http://127.0.0.1:19000/tripo/v3`、`MESHY_BASE_URL=http://127.0.0.1:19000/meshy`（Meshy Adapter 自己追加 `openapi/v1`、`openapi/v2`）、`HYPER3D_BASE_URL=http://127.0.0.1:19000/hyper3d/api/v2`、`LLM_BASE_URL=http://127.0.0.1:19000/openai/v1` 交给栈，任意 Key 都被接受。Key 以 `fake-<场景>` 开头或请求头 `X-Fake-Scenario: <场景>` 触发故障（`go run ./cmd/fake-upstream -help` 列出场景）。要让 Agent 调用工具，在用户消息里写 `[[fake:tool:create_model_3d {"prompt":"a cube"}]]`。栈要走 Tripo 原生处理（rigging、segment、complete）必须由 `internal/app` 的 `TestFakeUpstreamStack` 测试二进制承载（原因见架构文档），整栈脚本已经这样做。
- **官方对齐实测（付费，须授权）：** `cmd/fake-upstream-align` 的 `official` 子命令把脚本发给真实 API，每个付费端点只发一次最便宜的请求，没有 `-confirm-paid` 拒绝运行；原始结果只写到工作树外的 0700 目录。随后 `export` 离线导出脱敏样本、`fake` 对 fake 重跑同一脚本、`compare` 生成 `alignment-report.{md,json}`。真实请求使用 dev-test 栈已有的 Key（按 `docs/deployment.md` 定位，不输出），受 `AGENTS.md` 的付费验收次数上限约束；采集必须在干净工作树进行。官方接口变更后重新实测，新版本新增目录，旧版本不动。
- **新增上游 API 版本：** 新建同级目录（如 `tripo/v4`），带自己的实现、样本、`Profile` 与对齐报告，在 `registry.Versions()` 与 `Profiles()` 追加一行；不改旧版本目录。

### 交付硬门禁

- 合并前以本机门禁为准：每个分支推送时照常跑 pre-push（本地七项）；合并到 `main` 前，在待合并的 head 上跑一次 `python3 scripts/verify-local.py --race`，结果写进 PR。
- 交付任何改动到 `dev-test` 前，最终待发布的精确 `main` SHA 应有一次完整的 GitHub Actions `ci` 运行且所有作业 success。可信自托管 `workflow_dispatch` 可提供同一 SHA 的 CI 证据，但须核对实际 runner、`head_sha` 和 checkout。托管 runner 因计费墙不可用，且（没有自托管 runner 或其不可用，包括安全前提未证明）时，按上节先对同一 SHA 做本机初检，再以该 SHA 的完整隔离主机自检作为 CI 替代，并在 PR 或 Issue 记录实际结果。作业因代码原因失败时不得放行；主机自检不能冒称 Actions success。旧 SHA、其他提交的结果、缺项或 skip 都不算。
- 发布到 `dev-test` 前，还要在同一个待发布 `main` revision 上再跑一次本机 `--race` 全量；发布后照常做 L0、L1、L1 Agent 和相关验收。CI 只作门禁，不承担部署。

### 本地统一入口与 pre-push 复用

安装本工作树依赖后，最终源内容使用 `python3 scripts/verify-local.py` 执行上述标为本地的各项（七项，不含 race）。开发过程中可先跑受影响的测试；最终全套和 pre-push 使用同一入口。`--force` 强制完整重跑，`--check` 只检查是否有可复用结果，不执行门禁，`--race` 额外执行仅 CI 的 race 项（可与前两者组合）。CI 仍独立执行标为 CI 的各项（`.github/workflows/ci.yml`），不读取本机凭据。

凭据记录实际通过的门禁名。默认运行的凭据不含 race，`--race`（含 `--check --race`）不会把它当作已覆盖 race，而是完整重跑；`--race` 运行的凭据覆盖默认七项，之后的 pre-push 可直接复用。输出中的复用与完整通过行列出实际覆盖的门禁，未跑 race 时注明它只在 CI 执行。

同一克隆的所有 worktree 共用一个门禁队列：真正执行门禁前，入口在 Git 公共目录（`git rev-parse --git-common-dir`）下的 `artai-verification-queue/` 用 `flock` 取得 N 个槽位之一，门禁结束（成功、失败或中断）即释放，进程退出时由操作系统兜底释放。N 由 `ART_AI_VERIFY_SLOTS` 指定，默认 1；`0` 关闭排队。槽位全忙时输出一行 `[verify] gate queue: ... waiting for another worktree's gates`，并列出各槽位当前持有的 worktree，取得后输出等待时长。只有执行门禁才排队：可复用凭据命中、`--check` 和仅删除分支的 push 不等待。排队期间输入监测与 stat 守卫持续生效，等待中修改了输入同样不保存凭据，push 仍按原规则校验；`ART_AI_VERIFY_SLOTS` 只影响调度，不计入凭据环境摘要。原因：多个 worktree 同时 push 时机器负载可达 40–60，Go 测试（尤其 `--race`）会因资源争用超时或出现 SQLITE_BUSY，失败后又要重跑整轮；排队不改变门禁集合、命令与 CI。

只有本工作树完整成功、24 小时内且输入未变的结果可复用。输入包括源码和相关未跟踪/忽略文件的实际字节、门禁定义、依赖、工具链和环境；仅保留最近一次成功凭据，位于当前 worktree 的 Git 私有目录，不提交也不写入共享台账。输入和环境只保存摘要，不复制配置值或密钥；入口输出各项及整轮耗时。同内容提交可复用，push 还须逐个核对分支 tip、实际文件字节和当前工作树，不能依赖会忽略修改的 Git 标记。不同 worktree、不同 tip、失败、部分结果、过期、无法确认的输入或运行中变更均不能证明可复用；缺少可靠指纹时完整执行所选门禁且不生成可复用凭据。输入不匹配的 push 拒绝，不能用当前工作树测试别的提交。

当前可复用指纹支持 macOS 的标准工具链，并绑定实际 Xcode SDK、Go/Node/pnpm 安装和依赖；其他平台或无法枚举的自定义外部编译/加载配置走完整检查且不保存复用凭据。Git 为 hook 添加的默认工具目录，经安装路径及工具解析校验后从 PATH 前缀移除，指纹与实际门禁使用同一路径；自定义目录或工具遮蔽不复用。运行期间由原生文件事件监测源码、本地依赖目录及缺失外部配置路径的瞬态增删，结束时须收到标记事件并排空当前事件批次；监测不可用、异常或发现变更均不产生复用凭据。缺失外部配置只监测其目标路径及尚缺的上级目录，其他桌面配置的更新不使凭据失效；已纳入输入的目录仍按完整目录检查。仅忽略已识别的 pnpm 根目录写权限探针和声明的生成目录，文件及外部输入树仍核对字节、inode 与变更时间。本地类型检查禁用增量结果以避免旧缓存影响检查；构建产物和有明确生成契约的缓存不作为源文件。复用是受信任本机的重复工作优化，不是抵抗恶意篡改机器或测试缓存的证明。门禁失败继续阻止推送；独立审阅和适用发布/回归不减免；CI 不可用时的替代证据见上文「主机自检替代 CI」。

pnpm 的 package-manager store 仅按 CLI 明确返回的路径绑定；返回空值、`undefined` 或 `null` 时不能证明运行时存储，不猜测 HOME 下的共享缓存，正常完整执行所选门禁但不生成复用凭据。`--check` 此时仅报告不可复用，pre-push 仍完整执行本地各项和原提交校验；没有减少门禁或忽略已绑定外部输入。

Hook 安装默认只预览：`python3 scripts/install-local-verification-hook.py --dry-run` 输出有效 pre-push 文件的精确差异。先执行 `python3 -m unittest discover -s scripts/tests -p 'test_*.py'` 验证相关兼容性，再用 `--apply` 激活；重复激活必须为 unchanged。安装器仅替换已识别的 Plinth 门禁块，保留 SpecGit 及其他 hook 字节、顺序和 stdin；遇到未知/损坏块拒绝改写。原子替换使已经运行的 hook 继续使用原文件。共享 hooks 仅调用已跟踪且声明兼容协议的新入口；旧 worktree、未跟踪或不兼容的草稿继续执行 hook 内置的原七项完整检查。

监控器输出最终结果后须等待传输完成再退出；提前退出、传输错误、超时、协议异常和真实输入变更仍拒绝复用。输出仅含固定原因码，不包含文件名、配置内容或凭据。诊断时区分确定性复现与历史推测：只有旧日志中的“未 clean”不足以判定具体原因；不能用后续测试通过反推某次历史失败已经定因。

回退先用 `python3 scripts/install-local-verification-hook.py --uninstall --dry-run` 预览，再加 `--apply` 恢复原完整七项门禁块；不卸载 SpecGit guard、不关闭检查、不影响其他 worktree 的 checkpoint。相关回归证明安装/重复安装/恢复、原 guard 拒绝与无新脚本工作树的七项失败传播；实际激活记录与复用耗时放在关联 Issue。

SpecGit 初始化区块使用独立安装器 `scripts/install-specgit-required-hooks.py`（#242），规则与上述门禁安装器一致：默认预览，`--apply` 原子替换并保留其他字节与模式，未知/损坏块拒绝改写，`--uninstall` 只移除本区块；另有只读 `--status`、`--mode warn|enforce` 与 `--init-worktrees`。回归为 `python3 -m unittest scripts/tests/test_install_specgit_required_hooks.py`。上线顺序与对已有工作树的影响见 `docs/specgit-project-integration.md`「所有分支与工作树强制初始化」。

## 3. 部署与回归流程
1. 本地代码修改完成；
2. 在待合并 head 上运行本机 `python3 scripts/verify-local.py --race` 全量（清单见 §2，前端三项 + Go 门禁四条），完成独立审核；
3. 将已验收提交合并并推送到 `main`，以 `main` 的精确提交 SHA 生成不可变
   镜像标签；生产不得部署只存在于功能分支或未提交工作区的源码。交付 `dev-test` 前，在最终待发布 `main` 的同一精确 SHA 上运行本机 `python3 scripts/verify-local.py --race`，并取得完整 GitHub Actions CI 或上节允许的主机自检证据；旧 SHA、缺项或有 skip 的结果不算；
4. `dev-test` 测试栈的普通 Go 发布不得重跑 ETL、不得改写
   `production/migration-ready.txt`。
   构建新镜像后，先执行
   `GO_PROD_PROJECT=<compose_project> scripts/cutover-go-production.sh preflight --image-tag <tag>
   --source-commit <main-sha> --no-build` 做隔离预检，再用
   `docker-compose.go-production.yml` 和 dev-test 的 Compose 项目名（环境文件 `compose_project`）更新
   唯一 Go 服务；严禁使用 `deploy.sh` 或 `--remove-orphans`；
5. 更新前记录旧镜像标签并创建一致性备份；新容器未能健康启动时恢复旧
   镜像标签。正式生产的首次迁移另按
   `docs/production-migration-runbook.md` 执行，失败时必须恢复旧 Next；
6. 部署后执行与本次改动相关的无扣费端到端验收；`dev-test` 测试栈的少量真实
   Provider 生成验收已有 `AGENTS.md` 常设授权，按模型和资产链留证，
   测试操作者代点同意仅是测试期操作授权；产品的批准规则见 `docs/product.md`；
   正式栈按 `dev_ops.md` 的 2026-10-19 有界授权执行；写 Key、付费生成、导入数据及 nginx/域名变更仍须用户明确决定；
7. 若依赖服务异常，先修复依赖服务并重新部署，再继续验收。LLM/生图依赖
   走 llms 正式服务器网关的 `/v1`（地址在 dev-test 运行环境文件 `LLM_BASE_URL`；原 `dev-test` 主机上的
   llms-proxy 已迁移至此，不得再使用历史 llms-proxy 端口），不限制客户端与
   模型；测试栈对话模型为字面量 `deepseek`（2026-10-19 起网关目录已无 `qwen-max`、`glm-flash`，直接请求返回 400 `model not supported by any target`；经用户确认改用 `deepseek`，网关当前路由到 `deepseek-flash`）；请求走 OpenAI 兼容 `chat/completions`。思考参数按 Product Schema `chat.agent.thinking.model_profiles` 的模型前缀发送，DeepSeek 用 `thinking.type=enabled`、`reasoning_effort=low`（用户 2026-10-20 决定；官方只有 low/high/max 三档），工具循环回传 `reasoning_content`；其他中国大陆模型仍用官方最高档；上游
   测试凭据只从 `dev-test` 测试栈已授权的运行环境读取，不写入文档；抠图依赖为背景去除（matting）服务（接线地址见 dev-test 运行环境文件 `MATTING_*`）。

### 可控恢复与读屏验收

自然样本缺失时，使用 `scripts/smoke/seed-acceptance-fixture.py` 的只读预检及独立快照，再运行同版测试二进制。操作协议见 `scripts/smoke/README.md`；故障注入、真实出站、任务/Attempt/Grant/冻结输入、实际资产字节须分别留证。隔离进程结果不能冒称 `dev-test` 主实例故障结果，不能替代 §2 自动化门禁、L0/L1 或相关 L2。

真实读屏 smoke 应记录读屏器和语音服务版本、实际浏览器来源/版本、完整部署 SHA、键盘路径、读出的名称/角色/状态、焦点进入与返回，以及前后 Task/待批准数量。AX 树、自动审计和语音文本请求各有证据范围；没有实际音频时不得把空 WAV 或文本日志描述为可听回放。Tooltip 与 Dialog 叠层按实际 Escape 消费顺序验证，不能将首次关闭 Tooltip 误记为 Dialog 关闭失败。

## 4. 架构设计验收
- 改动必须符合 `AGENTS.md` 中的分层架构，不得跨层耦合；
- Provider 产品配置（参数、能力、UI 元数据、默认值、约束、标签、积分
  估算）唯一真源是 `internal/catalog/default_catalog.json`（Product
  Schema）；前端只消费其公开投影：`/api/v1/schema`、Provider discovery
  投影（扩展能力需显式 `projection=extended`）与 `task_history.labels`。
  前端静态配置树 `src/lib/providers/provider-config.ts` 已于 Issue66 W3
  退役删除，禁止以任何形式重新引入平行的前端配置树，禁止在 Handler、
  页面中建立第三份产品配置；
- Provider 接入参数、能力声明、UI 参数、默认值、约束规则必须在配置层收口；
- 页面层不得拼装上游 Provider 私有参数，也不得直连上游私有协议；
- 原生 API 与兼容 API 的任务必须统一走 `internal/generation`，Transport
  不得直接推进生命周期或写任务状态；SPA 从 Product Schema 获取兼容创建路由；
- 若发现历史异常架构代码，本轮应评估是否顺手重构，并补充必要测试覆盖。
- `internal/generation` 的 Provider 边界由 Phase 1 新增的特征化测试
  （`provider_private_context_test.go`）与内核分层门禁
  （`kernel_boundary_test.go`）锁定：门禁按路径段边界禁止直接 import
  `internal/providers`、`internal/adapters`、`internal/transport`、
  `internal/app`，扫描范围为 `internal/generation` 包目录当前 `*.go`
  （不含 `_test.go`，当前无子包）；它只证明直接 import 隔离，不证明不存在
  间接依赖或标识符级 Provider/格式分支。边界语义与当前/目标差异见
  `docs/provider-schema-architecture.md`。§2 门禁命令不变。

## 5. 分层设计要求验收

### 5.1 Provider / 专属配置 / 适配层
- Provider 的接入地址、Token、API Key 等安全配置必须放在 `.env`；
- 适配层只负责兼容上游协议，不承载页面逻辑与中间件逻辑；
- 专属配置层必须体现模型能力差异，不允许把差异硬编码在页面里；

### 5.2 核心业务层 / 任务管理 / 缓存
- 任务状态推进必须统一由任务管理层收口；
- 上游成功不等于最终成功，必须在缓存落盘后才可对外暴露 `SUCCEEDED`；
- 缓存命中应可观测，二次访问必须优先使用本地缓存；
- 3D Offering 按当前官方协议逐项核对可导出格式；上游一次不提供全部格式时，由 Provider 私有补全步骤取得。对每个实际返回的必需格式核对本地 READY、字节、大小和二次读取；一项失败不能借其它格式满足最低数量而把 Task 判成功。转换步骤按当前 Offering、Product Schema 的批准策略及会话批准模式执行：需要真人决定时，必须先为冻结输入取得对应批准 Grant；由策略批准的步骤须保留其批准来源。处理面板直接执行的步骤遵循产品规则中的确认项。
- 对新旧 3D 模型下载字节及压缩包展开内容检查绝对上游地址和未缓存依赖；断开上游后再次通过本服务下载。浏览器开发者工具的请求与响应不得出现上游 URL、签名、remote job ID 或私有检查点；前端 CSP、同源 URL 过滤和服务端内容检查均须生效。

### 5.3 中间件 / 统一接口 / UI
- 权限控制必须通过中间件或统一服务收口，不得在页面内分散判断；
- UI 只消费统一接口层与 Provider 配置；
- UI 展示的任务状态、参数、预览信息必须与统一任务接口保持一致；

## 6. 具体业务代码验收
- 所有新任务入口必须遵守当前付费执行准入：创建用户可见 `generation.Task` 前，输入须形成与 owner、session、offering revision、归一化输入和幂等键绑定的 `PreparedInput`，并取得与该冻结输入绑定的已批准 ExecutionGrant。批准可由真实用户作出，也可由 Product Schema `application.chat.approval_policy` 所声明的会话批准策略作出；策略仅在当前模式和能力允许时批准。Prepared hash 仅标识输入，不等于批准；不得以旧 legacy POST、旧 UI 参数或 `request_consent` 提示绕过此门禁。批准模式与人工决策边界见 `docs/product.md` §6。
- `/api/v1/tasks` 的列表与详情应来自 Generation 的统一任务事实，返回稳定 task ID、session（存在时）、capability/offering/provider/model 标识、公开状态/进度、版本时间、经白名单投影的失败信息、READY 资产和资源状态。输入、provider 私有上下文、原始上游响应、密钥、Attempt/调度内部字段不得作为通用 `providerData` 或任意 `params` 返回；公开字段必须由明确 DTO/Schema 投影控制。
- 任务历史必须按当前 principal 的 owner scope 查询，分页/筛选从服务端执行；验证他人任务与资产拒绝访问。历史记录是只读投影，重新查看不能创建付费任务。
- 新增/修改 Provider 后，按 Offering 输入契约验证上传、格式、数量和大小边界；资产下载/代理需核对 owner、READY 状态、正确 `Content-Type` 与实际字节；Schema/public projection 不得暴露服务端 configuration 或 Provider secrets。
- `SUCCEEDED` 任务的 required 资产须 READY 且可通过受控链接下载；可选资源状态独立展示，不把可选资源失败伪装成任务失败。缩略图字段按 Offering/Schema 声明，不能要求旧 Next 页面固定存在 `thumbnailUrl`。
- 任务详情展示的模型、参数说明和展示文案必须来自公开 Schema/任务投影；不得回显原始 JSON、上游私有字段或 `[object Object]`。只显示实际公开、且当前调用方有权读取的数据。

## 7. 状态机验收
- Generation 公共任务状态按现行契约验证：`QUEUED → RUNNING → MATERIALIZING → SUCCEEDED`，并覆盖 `FAILED` 与 `CANCELED`；合法重试/恢复可按内核转换规则回到可调度态。旧 Next.js 的 `PENDING / IN_PROGRESS` 与客户端 `TaskTracker` providerId 轮询规则不是当前 Go 任务状态机，不作为验收标准。
- Chat 中待批准或已受理、尚未完成的 Agent 生成调用可处于 `WAITING_TASK`，这是 Chat turn 状态，不是一个已创建的 `generation.Task` 状态。批准前不得出现该次收费 Task；批准后恢复同一受理操作并沿统一 Generation 生命周期创建/观察 Task。拒绝不得产生任务。
- `SUCCEEDED` 只在上游已完成且 required 产物已下载、校验并持久化后成立；上游完成到资产物化之间必须保持 `MATERIALIZING`。模糊提交结果不得盲目重复付费提交，恢复沿原 Grant/幂等身份继续。
- Observation、轮询/回调、租约恢复和终态应由 Go Generation/调度器统一管理。前端不按 Provider ID 自行选择轮询器；任务列表、详情、事件/SSE 与资产接口必须对应同一稳定 Task ID 和状态版本。
- UI 任务状态消费公开 Product Schema 的 `task_states` 与统一 API 投影；不得在页面重建第二套 Provider 状态枚举。覆盖成功、失败、取消、素材物化、未授权拒绝、重放和跨 owner 读取拒绝，确保列表/详情/历史状态一致。

## 8. UI 验收
- 功能页面必须 200 正常加载；
- 参数面板中 Schema 声明可编辑的字段必须可交互；显式 `readonly` 的固定值只展示实际有效值，`hidden` 字段不展示；打开面板不能产生参数写入或新任务；
- 提交后任务必须成功进入统一任务链路；
- 任务完成后预览、下载、详情入口必须可见且可用；
- 侧边栏菜单入口必须可点击且跳转正确；
- 多 Provider / 多能力场景下，页面展示必须跟随 Provider 配置变化；
- 不得在 UI 暴露上游私有字段、英文原始结构或不可读对象。

## 9. 错误处理验收
- 面向用户的错误消息必须是中文；
- 参数校验错误必须可读、明确，例如：文件格式不支持、数量超限、大小超限；
- 不得把上游原始英文错误、内部堆栈、私有实现细节直接暴露给前端；
- 输入被 Schema 约束拒绝（参考图超大、像素超限、宽高比、无法读取等，#288）时，HTTP 400 的 `detail` 是 Schema 原因并带 `user_facing: true`，界面显示该原因而非通用「请求参数无效」；没有该标记的 `Detail` 与内部原因仍只进日志。回归：`internal/transport/httpapi/domain_error_reason_test.go`、`internal/app/input_refusal_http_test.go`、`src/components/workspace/canvas-refusal-reason.test.tsx`；
- 异常场景要验证：
  - 未登录；
  - 无权限访问他人任务；
  - 参数非法；
  - 上游 API 失败；
  - 缓存下载失败；
  - 轮询超时 / 状态异常；

## 10. Base UI 组件设计要求验收（避免 `@base-ui/react` 已知问题）

### 10.1 Select 场景
- 页面不使用浏览器原生 `<select>` 弹层；列表选择统一用 `src/components/ui/option-select.tsx`（`OptionSelect`，基于 `components/ui/select.tsx`）；
- `SelectValue` 不能作为“选中后显示文本”的唯一来源；`OptionSelect` 已按 value → label 映射渲染触发按钮，新写法沿用它；
- 选项和文案仍从 Schema 读：值为空时显示 Schema 文案占位，不在组件里写死文字；
- 生成参数里的像素尺寸选项按 Schema 模板 `ui.studio.params.size_option` 同时显示分辨率和比例（如 `3840×2160 · 16:9`），“自动”、比例和命名尺寸保持原文；

### 10.2 日期输入场景
- 页面不使用原生 `<input type="date">`；日期统一用 `src/components/form/schema-date-field.tsx`（`SchemaDateField`，内部为 `components/ui/date-field.tsx` 的弹层月历）；
- 值仍是本地日期 `YYYY-MM-DD` 字符串，空串表示未选；日期计算只用本地日期（`date-field-math.ts`），不经 `toISOString`；
- 由于不再依赖原生日期输入，原先 Safari `onInput + onChange` 双保险不再需要；
- 开始/结束成对的日期筛选统一用 `dateRangeBounds(start, end)` 互相设上下限：开始日期不能选在结束日期之后，结束日期不能选在开始日期之前；

### 10.3 右键菜单
- 右键打开的菜单用 `src/components/ui/context-menu.tsx`（Base UI ContextMenu），触发区是被右键的那块区域本身（画布是 Konva Stage）；不用隐藏触发器去手工打开普通 `DropdownMenu`。普通 Menu 的浮层 `mouseleave` 会以 `trigger-hover` 关闭，打开动画即可触发，表现为菜单闪现后消失（#280）；
- 验收在 Chromium 与 WebKit 的真实浏览器里做：右键后停 2 秒、把指针移进菜单和子菜单都不关闭；选择命令、Esc、点击外部关闭；再次右键移到新位置；窗口角落不出界。画布的回归脚本是 `scripts/test-canvas-menu-inpaint.mjs`（本机 mock 服务，不用凭据、不付费）；

### 10.3a 画布图片内存（#287）
- 改动画布图片加载、缓存或图层删除/替换时，用 `scripts/measure-canvas-image-cache.mjs`（本机 mock 服务，Chromium，不用凭据、不付费）对比改动前后：每轮生成一张图、画一笔并离开画笔、超过 4 张时删最旧的图层，最后删除全部图层；输出每轮垃圾回收后仍存活的资产 `Image`/`createImageBitmap` 位图数及其解码尺寸估计（宽×高×4）、JS 堆、window/document 监听器数和 DOM 节点数。它是测量而不是门禁；平移帧间隔在无头 Chromium 下只作参考。

### 10.3b 大图工作台卡顿（#290）
- 改动画布绘制、画板导出、对话/版本条图片或资产预览时，用 `scripts/measure-image-heavy-workspace.mjs` 在用户规模（会话 A：20 轮出图 + 10 张 3840×2160 图层；会话 B：4 轮 + 9 张 2048² 图层）对比改动前后。服务同 §10.3a，另设固定的 `DEV_MOCK_USER`，让每次测量登录同一个用户；先 `PHASE=seed` 造数据，再复制服务的数据库与存储目录，让每个被测构建都从同一份副本启动后跑 `PHASE=measure`。默认用完整的无头 Chromium（有 GPU），`CPU_THROTTLE=4` 模拟较慢的主线程。输出打开会话 A 的耗时、图层位图（#291 起另列缩小显示副本 `layerReducedCopies`/`layerReducedMiB`）与页面 `<img>` 的解码尺寸、渲染与 GPU 进程内存、平移/缩放/对话滚动的帧间隔与主线程时间、一次发送期间的长任务，以及切到 B 再切回 A 的耗时。它是测量而不是门禁，本机结果不代表用户 Windows/Chrome 上的数值。
- 改动输入区附件、对话里已发送图片的显示或输入资产上传时，用 `scripts/test-composer-attachments.mjs`（#292）在 Chromium 和 WebKit 中回归：服务同 §10.3a（mock Provider、mock Agent、mock 登录，先 `pnpm run build`），`BASE=http://127.0.0.1:<端口> node scripts/test-composer-attachments.mjs`。脚本经接口写入一条 #292 之前那样带 data URI 的旧消息，再从输入区附一张 PNG 发送，检查：附加时不上传、发送时上传一次、请求与存储的消息里只有资产地址并带资产声明；发送后、刷新后、切换会话再切回后，新图经 `?size=preview` 显示、旧 data URI 图也显示；大图查看是原图；上传返回 500 时这一轮不发出、提示带请求 ID、文字和图片留在输入框。截图和 `report.json` 写到 `OUT`。手动运行，不在 §2 门禁内。
- 改动附件文件名缓存（`src/lib/image-upload.ts`）、加工面板底部报价或对话选择卡的提交时，用 `scripts/test-frontend-335.mjs`（#335）在 Chromium 和 WebKit、1366×768 与 1920×1080、中英两种语言回归：服务同 §10.3a（mock Provider、mock Agent、mock 登录，先 `pnpm run build`），`BASE=http://127.0.0.1:<端口> node scripts/test-frontend-335.mjs`。检查 Meshy 与 Hyper3D 混选时底部同时列出原生估算行与积分合计、`[mock:large_model]` 来源上不能运行的步骤不计价；脚本在页面里推迟 `FileReader` 读取（不需要新的 Mock 记号），输入区图片读取期间点选择卡「确认」，卡片显示等待文案、按钮锁定且不发请求，读完后自动提交、图片仍留在输入区。附件文件名缓存的内存上限由 vitest `src/lib/image-upload.test.ts` 证明（40 轮 2 MB 图后不超过 `IMAGE_UPLOAD_NAME_CACHE_MAX_BYTES`）。手动运行，不在 §2 门禁内。

### 10.3c 画布改尺寸后的选择（#311）
- 改动画布尺寸、锚点、图层坐标、命中或选择逻辑时，用 `scripts/test-canvas-resize-select.mjs`（服务同 §10.3a，先 `pnpm run build`；`BASE=http://127.0.0.1:<端口> node scripts/test-canvas-resize-select.mjs`）在 Chromium 和 WebKit 的 1366×768、1440×900、1920×1080 回归「画布已有图片 → 在参数面板改尺寸 → 选择工具点图片」：缩小、放大、左上锚点缩小后图片一半在画布外（点画布内和画布外的部分）、撤销/重做、自动保存后与刷新后、锁定图片改尺寸后仍不可选且解锁后可选、两张成组图片改尺寸后一起选中。每次点击前确认该点没有页面元素或其他图层遮挡；“选中”要求图层面板与舞台上的变换手柄都只指向预期图层。图片部分和完全在画布外时还做像素检查（按绘制顺序合成舞台各图层画布后读回）：选中手柄与画布内选中时一致、不被画布外暗层压暗，图片自身画布外的部分仍被压暗、画布内的部分不变；测试图是脚本生成的纯色图，读数不随缩放变化。截图和 `report.json` 写到 `OUT`。手动运行，不在 §2 门禁内；store 层的同类不变量由 `src/store/canvas-store-size.test.ts`、手柄图层在暗层之上的顺序由 `src/components/canvas/canvas-selection-layer-wiring.test.ts` 随 `pnpm test` 覆盖。

### 10.4 通用规则
- 不使用 `alert()` / `confirm()` / `prompt()` 等浏览器系统弹窗；错误用页面内 `role="alert"` 文字，确认用项目 Dialog；
- 已知仍保留的系统控件：画布取色的 `type="color"`（另有 HEX/RGB 输入）、参数文本框的 `<datalist>` 建议、文件选择对话框；
- 凡涉及 Select、日期、复杂表单显示的 UI 变更，必须主动在 Chromium 和 WebKit 做兼容性检查；
- 不得等用户反馈后才补 Base UI 已知问题。

## 11. 部署后端到端验收清单

### 11.1 依赖服务验收
- 实际配置的 LLM/生图代理入口可用，模型接口请求格式与返回格式正常；
- `matting` 服务（8002）可用，健康检查通过，认证信息正确；
- 若本次改动涉及其他依赖服务，也必须一并验收；

### 11.2 Provider 端到端验收
- 创建任务成功；
- 查询任务成功；
- 任务进入历史记录；
- 缩略图可见；
- **Offering 与模型可回读**（`offering_id`/`provider_id`/`model_id`）；
- 下载成功；
- 产物 `media_type` 与实际字节一致（用 magic bytes 核对，不能只看声明）；
- 资产 READY 且 `resources_pending` 为 false；
- 二次访问命中缓存；
- 失败场景错误提示正确；

### 11.3 2D 图片冒烟与回归
- **每轮随机抽 1–5 张 2D 成品图做冒烟与回归，不超过 5 张**：从当前
  session 的历史版本里随机取样，逐张核对。这条是抽样验收口径，不是固定
  清单——每次都测同样那几张，等于只覆盖已知good case，回归会漏；
- 随机是指不预先挑选、不固定轮换顺序，避免"固定 5 张"退化成"每次都过"；
- 每张需核对（与 §11.2 端到端口径一致）：产物实际可下载、**声明
  `media_type` 与实际字节一致**、缩略图与详情可读、`SUCCEEDED` 且资产 READY
  （`resources_pending` 为 false）、Offering 与模型可回读；
- **生成参数只回给任务所有者本人，其他读者一律看不到**（2026-10-22 按实现核对后修订；此前写成「公开详情一律不返回参数」，与实现不符）。
  `GET /api/v1/tasks/{id}` 的摘要投影不含输入；只有调用者就是该 Task 的 owner 时，`publicTaskInput` 才补回本人的提示词和
  Offering 公开声明的参数，供详情回看和「再次生成」预填。引用图类参数、`*_url` 地址、额外费用同意开关和被扣留的语义不返回；
  管理员读取他人任务时也不返回。上游 wire 参数、Provider 私有字段和 Opaque 检查点在任何前台 API 中都不下发；
  管理端「生成记录」走 `usage_ledger.params_json`。
  - 验收时要核对两件事：owner 本人能回读自己的提示词与公开参数，非 owner（含管理员读他人任务）读不到；
  - 若在任务卡、事件、SSE 载荷或非 owner 的读取中出现参数或提示词，或出现上游 wire/私有字段，一律按**信息泄漏**处理。
- 抽到的图若**失败或异常**，先按缺陷处理并定位到具体那张，不要因为
  "随机"就当预期内波动放过；
- 数量控制在 5 张以内是成本与时延要求：2D 走真实模型计费，多抽无收益；
  覆盖度靠随机性而不是靠加量；
- **满 5 张仍未发现任何 bug 或问题：暂停，不要继续加图**，先停下来分析。
  此时继续抽样的边际收益已经很低——同一批次 5 张都干净，问题多半不在
  "某张图抽到了"这一层，而更可能在 Schema 声明、参数投影、任务状态机、
  资产落盘或模型接线这些**与抽样无关的公共路径**上。应当：
  1. 停止新增生成任务，不要用"再多测几张"代替定位；
  2. 回到 §11.2 逐项核对公共链路（Task 是否 SUCCEEDED、资产是否 READY、
     格式/扩展名/大小、详情参数可回读、下载与二次访问缓存）；
  3. 核对本轮实际使用的 Offering 与参数是否与 Schema 声明一致；
  4. 分析结论写入关联 Issue/MR，再决定是否需要新一轮抽样。
  换言之：**5 张全过是"需要分析"的信号，不是"可以放心"的信号。**
- 本节是 2D 抽样口径的唯一真源，`scripts/smoke/README.md` 与 L1/L2 冒烟
  脚本只引用不另定标准。

### 11.4 Agent 同意挂起验收（AGT-12）
发布含挂起/恢复改造的版本后必须执行。会话批准方式（`auto`/`manual`，默认 `manual`）决定哪些步骤等人：手工方式下只有文生 3D、图生 3D、换贴图需要人工批准，2D、重拓扑、绑骨、动画由策略批准（格式转换 Offering 已停用，规则保留）（`decided_by` 为 `policy:manual`）；带额外积分的步骤（例如额外导出格式）在手工方式下也要在对话里的批准卡上人工批准。因此挂起验收用 3D 请求，先把会话显式设为手工方式：
- 会话 A 触发付费 3D 生成 → 同意请求出现后**不批准**（挂起态）；
- 会话 B 同样触发并保持不批准；
- 会话 C 发送普通消息，必须立即得到 LLM 响应（不得排队或 AgentBusy）；
- 批准会话 A → 待批准服务提交并生成成功（产物正常，结果消息发进会话）。这是一次真实 3D 生成，`smoke-l2-livemodel.sh` 仅在 `L2_APPROVE_3D=1` 时执行，默认停止 A 本轮并断言零任务；
- 拒绝会话 B → B 收到拒绝文案且不产生任务；
- 会话 A 挂起中执行「停止」→ 轮次落停止终态，之后同会话可正常发起新轮次；
- 批准卡一出现，本轮立即以等待文案收束（不再等 `consent_wait_timeout_seconds`），批准留在“待批准”里，不产生任何任务；之后批准由待批准服务提交并把结果发进会话；
- 额外积分的批准卡（Mock 栈可验）：手工方式下发“做一个简单的木凳 3D 模型”，批准卡写明基础积分、附加积分及用途、合计，并有“批准”“不批准”；点“不批准”得到不消耗积分的说明和替代建议（减少附加选项或换模型），不产生任务；点“批准”只产生一个任务。任何一步都不得让用户去勾选“同意额外积分消耗”。`go test ./internal/app -run ExtraCredit` 覆盖手工出卡、自动授予与拒绝路径；
- 手工方式下的 2D 请求不挂起：无待批准同意，任务 SUCCEEDED，批准来源为 `policy`，显示积分（`smoke-l2-livemodel.sh` 第一部分，一张小额文生图）。

### 11.5 Go 候选验收
- 候选对外拓扑必须只有 `art-ai-go` 一个容器、一个 Go Entrypoint 和一个
  HTTP 端口；页面、`/assets/`、其他公开静态文件与 `/api/**` 必须通过
  同一 Handler 验收，最终运行镜像不得包含 Node/npm/pnpm 或 Next Server；
- Node/pnpm 可用于 Docker 多阶段构建中的前端编译；此运行镜像约束不限制
  开发机或 CI 按 §2 安装依赖并运行门禁。Vite 必须复用现有 React
  页面和组件，页面路由、登录重定向、导航、布局模式及显示文本继续由
  Product Schema 投影；
- live 候选必须保持 `CANDIDATE_ALLOW_GENERATION=false`，只运行 `scripts/test-go-candidate-http.sh` 的无扣费契约；没有单独授权不得用有效 Provider 配置生成；
- Mock 候选必须显式设置 `CANDIDATE_ALLOW_GENERATION=true`，依次运行 `scripts/test-go-candidate-http.sh`、`scripts/test-go-candidate-all-generations.sh` 和 `scripts/test-go-candidate-restart.sh`；
- Mock 验收必须覆盖文生图、文生 3D、图生图、图生 3D、重拓扑、重纹理、绑定、动画和去背景的公共 Web 创建、轮询及资产下载；
- 验收创建的任务必须在脚本退出时自动删除；失败时也必须执行清理；
- Go 重启后，持久任务 ID、状态及资产 SHA-256 必须保持一致；
- live 候选未配置候选独立 Webhook 密钥时必须返回 404；Mock 使用虚拟
  `GO_WEBHOOK_*` 与匹配的 `CANDIDATE_WEBHOOK_*`，必须覆盖 GET 健康、
  错误路径、错误 Key、坏 JSON 与未知 Job 幂等确认，不得复用生产密钥；
- Provider 回调必须由 `application.provider_webhooks[]` 集合声明稳定 ID、
  Provider、路由、秘密设置、认证 Header、Job ID 字段与响应线协议；新增
  Provider 回调不得在 Transport 增加 Provider 条件分支；
- Go 外部 API 注册门禁必须比较归一化后的完整路由形状；除当前 Schema
  声明的 Provider Webhook 外，任何 method catch-all 都不得被视为具体
  API 已注册，且必须有反例测试阻止假阳性；
- 任务路由、资产下载路由、multipart 字段名和模型参数名必须从 Product Schema 投影，验收代码不得为特定 Provider 增加路由特例；
- 客户端生产 `app/components/hooks/store/lib/canvas` 的 `.ts/.tsx` 必须通过
  AST 架构门禁：可见 JSX、文本属性、表达式与显示配置对象中的中英文
  副本不得绕过 Product Schema；旧 Next.js `route.ts` 由 Go 公共 HTTP
  Schema 文本门禁负责，不得混入客户端扫描制造假阳性；
- Canvas 客户端导出必须有 Schema 变异测试覆盖背景、边距、MIME、文字与
  图形配置，并有回归测试证明 Shape/Pencil 使用节点当前 fill/stroke/width，
  导出结果不得使用与 UI 不同的另一套默认样式；
- Canvas 服务端合并必须在 Go HTTP 测试及候选容器验收中上传真实 WebP、
  合并为画板 Asset、下载并校验 PNG magic；不得只用扩展名或伪造字节；
- 公共 HTTP 验收必须从 Product Schema 枚举主导航、管理导航和公开登录页，
  后台请求每个页面并确认返回 HTML；不得用本地浏览器、Computer Use 或
  鼠标键盘自动化代替该验证；
- 认证验收必须使用同一主机名重放 `dev-test` 主实例的有效 Cookie，比较
  `/api/auth/session` 的用户 ID 与余额响应；不同端口共享 Cookie 域，
  `SESSION_SECRET` 不一致必须判为失败，不得以候选登录代替；
- 至少一个生成任务必须通过任务历史取得 Schema 投影的 `thumbnailUrl`，
  下载后与原生 Task Asset 字节一致并通过图片 magic 校验；生成后的
  `/api/cache-status` 必须报告缓存文件及类型正确的活动状态字段；
- `dev-test` 主机上的候选验收结束后必须确认旧测试端口健康且旧测试容器没有被重启。

## 12. 通过口径
- §2 的 8 项自动化门禁全部通过；
- 涉及 Go 候选时，Go 门禁与 11.4 的候选验收全部通过；
- 发布含同意挂起改造的版本时，11.3 的挂起验收全部通过；
- 发布含会话批准方式、Agent 3D 工具、格式补齐/ZIP、设定摘要、选定、版本链或重试的版本时，`scripts/smoke/smoke-l1-agent-mode.sh` 全部通过（零费用，设 `EXPECT_SHA`）；该脚本在已获取的有界候选集内检查最新 applicable 3D 任务的 `formats` 契约（自有会话；能力列表取自 Schema `model_delivery.capabilities`，每能力每页 100，最多检查 500 个去重候选，不保证全局最新或穷尽）：`formats` 必须是 Schema 7 种格式 ID 的精确集合、无重复、每项 `id`/`status` 为字符串；缺失、类型错误、格式清单读取失败都立即 FAIL，不静默换任务。`applicable`/`archive_available` 缺失或非布尔也 FAIL。`archive_available=false` 时 archive 必须返回 413/`too_large`（409/empty 或其他拒绝仍 FAIL，不视为可用归档），并在同一有界候选内另找 `archive_available=true` 的既有任务完成真实 ZIP 200/206、README 与字节检查（超过 `ARCHIVE_MAX_BYTES` 时按 `read-archive.sh` 契约做首项部分检查）；找不到可用归档、错误 413 code、或 `archive_available=true` 却返回 413 都 FAIL。只有完全没有自有 SUCCEEDED 3D 任务才 SKIP。离线契约：`python3 scripts/smoke/test-agent-mode-archive.py`（其中 `read-archive.sh` 为控制流 stub；真实 ZIP 解析由 `test-archive-check.py` 覆盖）；
- 部署后依赖服务验证通过；
- 本文档中与本次改动相关的验收项全部通过；
- 未通过前不得 push；
- 若发现跨项目问题（如 matting 服务故障），必须先修复依赖项目并重新发布验证。
