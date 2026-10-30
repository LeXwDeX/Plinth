# 1.0 验收自动化脚本（scripts/acceptance）

`docs/acceptance-1.0.md` 的清单有一部分可以脚本化。本目录放 #62 验收中调通、可复用的脚本，换机器重新 clone 后即可再跑。
只收脚本和说明：截图、日志、数据库、`.env`、seed、证据都不入库。脚本产出的 PASS/FAIL 是**证据线索**，逐项结果仍按
`docs/acceptance-1.0.md`「证据格式」写在 GitHub #62 的 comment 里；脚本没覆盖的部分（界面观感、真实上游）仍须人工验收。

English summary: reusable scripts from the #62 acceptance run. API checks against a named environment, an isolated local
Mock stack, a Playwright browser matrix, cross-user checks on a throwaway local stack, and a large-GLB Chrome spot check.
No credentials or host details live in the scripts; environments are reached through `scripts/release-env.py smoke <env>`
(injects `BASE`, `ADMIN_USERNAME`, `ADMIN_PASSWORD`, `EXPECT_SHA`) and local stacks use random loopback ports and random
passwords. Output goes to the system temp dir (or `--out`/`OUT`), never into the repository.

## 总览

| 脚本 | 目标 | 覆盖编号（`docs/acceptance-1.0.md`） | 耗时 | 需要 |
|---|---|---|---|---|
| `api_free.py` | 已部署环境（默认 dev-test） | AUTH-01、AUTH-02（抽查）、AUTH-03（匿名半）、AUTH-05、AUTH-06（抽查）、DLV-03（匿名半）、DLV-04（抽查）、SEC-03、SEC-05、ADM-02、PRV-01、LOG-01、LOG-02、LOG-03 | 约 1 分钟 | `release-env.py smoke`；LOG-02/03 另需该环境的只读 ssh |
| `mock_stack.py` | 本机隔离 Mock 栈（零 key） | LOG-05、PRV-02、IMG-01…06、M3D-01…04（M3D-04 为单步计划 PARTIAL）、DLV-02、TSK-02…05、AGT-05…07、SEC-04（均为 API 半边） | 约 2 分钟 | go、`build/go-web` |
| `cross_user.py` | 本机隔离栈（生产形态、会话登录） | AUTH-03、AUTH-09、DLV-03、ADM-01、AGT-11；`--ui` 加跑界面半边 | 约 2 分钟（`--ui` 约 3 分钟） | go、`build/go-web`；`--ui` 另需 Playwright |
| `browser_matrix/` | 已部署环境（默认 dev-test） | I18N-01…04、I18N-06、PRV-05、TSK-01、SKL-01…03、CNV-02…08、M3D-05、M3D-06、AGT-08、AGT-09、AGT-11（界面半边）、DLV-05、ADM-01（界面半边） | 完整矩阵（2 引擎 x 2 语言 x 3 视口）约 1–2 小时；单格约 5–10 分钟 | Playwright（chromium、webkit）、`release-env.py smoke` |
| `chrome_viewer.py` / `chrome_viewer.mjs` | 已部署环境里已有的大 GLB | M3D-01/03（查看器旋转缩放、视图模式）、DLV-01/02（大文件字节与 ZIP）、查看器加载失败与重试 | 约 5–15 分钟（取决于模型大小） | 系统 Chrome 或 Playwright Chromium、`release-env.py smoke` |
| `studio-scenarios.mjs` | 已有脚本，Mock 栈上的 Studio 四个场景 | 见文件头注释 | — | — |

`acceptance_lib.py` 是 `mock_stack.py` 与 `cross_user.py` 共用的标准库小工具（带 Cookie 的 HTTP 客户端、PNG 编解码、multipart、空闲端口），`browser_matrix/lib.mjs` 与 `fixtures.mjs` 是浏览器脚本共用的 Playwright 解析、登录和 PNG/GLB 夹具生成。

Mock 模式（`mock_stack.py`、`cross_user.py`）不需要任何 Provider key。针对 dev-test 的脚本都是**免费**操作，不触发付费生成，
也不做付费同意。

## 运行方式

针对已部署环境，一律经 `release-env.py smoke <环境>` 注入地址与凭据（凭据只在子进程环境里，不落盘）：

```bash
python3 scripts/release-env.py smoke dev-test -- python3 scripts/acceptance/api_free.py

# 浏览器矩阵（完整）；子集用环境变量
bash scripts/acceptance/browser_matrix/run.sh dev-test
B3_ITEMS=CNV-02 B3_ENGINES=chromium B3_LOCALES=zh-CN B3_VIEWPORTS=1440x900 \
  bash scripts/acceptance/browser_matrix/run.sh dev-test

# 大 GLB 的 Chrome 抽查（会话与任务 ID 来自测试账号里已有的模型）
python3 scripts/release-env.py smoke dev-test -- python3 scripts/acceptance/chrome_viewer.py \
  --session <会话ID> --big-task <大模型任务ID> --big-tile '3D v5' --small-task <小模型任务ID> --small-tile '3D v6'
```

本机 localhost 栈不需要环境注入，脚本自己编译、起栈、清理：

```bash
pnpm build                                   # 一次即可，产出 build/go-web
python3 scripts/acceptance/mock_stack.py
python3 scripts/acceptance/cross_user.py [--ui]
```

公共选项：`--repo`（默认本检出）、`--web-root`（默认 `<repo>/build/go-web`）、`--work`（工作目录）、`--keep`（成功后也保留
工作目录），`mock_stack.py` 另有 `--base-port`。端口默认自动选空闲的回环端口，密码每次随机。任何 FAIL 都使退出码为 1；
`PENDING`、`BLOCKED`、`PARTIAL`、`NOT-COVERED` 不改变退出码，但必须在验收记录里如实写明。

`browser_matrix` 的环境变量：`B3_ITEMS`（编号子集）、`B3_ENGINES`（`chromium,webkit`）、`B3_LOCALES`（`zh-CN,en`）、
`B3_VIEWPORTS`（`1366x768,1440x900,1920x1080`）、`B3_PARALLEL=1`（两引擎并行；默认串行以免触发 `/api/` 限额）、`B3_LLM=0`
（跳过每个 1440 格里的那一轮聊天，不耗 LLM token）、`B3_REF_SESSIONS`（逗号分隔的会话标题搜索词，指向测试账号里已有任务/3D
模型的会话，供 TSK-01、M3D-05、AGT-09 使用；不设则取最近的会话，找不到时记 BLOCKED）、`OUT`（输出目录）。
Playwright 按 `PLAYWRIGHT_MODULE_PATH`、仓库依赖、默认查找的顺序解析；缺浏览器时执行
`pnpm exec playwright install chromium webkit`。

## 副作用（会在被测账号下创建什么）

| 脚本 | 副作用 |
|---|---|
| `api_free.py` | 管理员登录/登出若干次；两次故意失败的登录；一次 1 KiB PNG 上传（体积对照）、一次“HTML 字节冒充 PNG”的上传（服务端接受，按 `image/png` + `sandbox` 下发，留在账号资源里）以及被拒绝的超限与 `text/html` 上传；读服务端日志（只读 ssh）。不建账号、不建任务 |
| `mock_stack.py` | 只在工作目录里：临时数据库、存储、日志。进程全部在脚本结束时关闭 |
| `cross_user.py` | 同上；另外在本机栈里建管理员和两个普通用户（随机口令，仅存在于该次运行） |
| `browser_matrix/` | 管理员账号下：每个格子一个可复用会话 `b3-accept <引擎>-<语言>-<宽度>`；临时个人 Skill `b3-*`（脚本结束时删除）；微型 GLB 与 PNG 上传；每格一次画布合并；每个 1440 格一次 persona + 一轮聊天（只耗 LLM token）；对已有会话做一次 3D 截图转 2D。不做付费生成与批准，不建用户，不发布/重置 Agent 配置 |
| `chrome_viewer.py` | 不建任务。下载大 GLB（和一次 ZIP）到临时文件后立即删除；负面检查靠浏览器请求拦截篡改响应，不改服务端数据 |

## 输出位置

默认写到系统临时目录（`plinth-acceptance-*`），也可用 `--out` 或 `OUT` 指到 `build/` 下。截图、`results.json`、日志都在那里；
**不要**把它们提交进仓库。要贴进 #62 的证据先脱敏：不贴 Cookie、签名 URL、口令。

## 已知限制

- `api_free.py` 的跨用户半边（AUTH-03、AUTH-09、DLV-03）需要第二个账号，dev-test 不建，所以标 `PENDING`；这些由 `cross_user.py`
  在本机栈上完成。
- `mock_stack.py` 的 M3D-04 只走单步计划；暂停/恢复/重试仍须人工或 Go 集成测试。IMG-02 里 Mock 渲染把长边限制在 1536。
- `browser_matrix` 中 WebKit 读取超大 blob 的已知限制记为 `KNOWN-LIMITATION`（见 #71），用 `chrome_viewer.py` 在 Chrome 上复核。
- 脚本依赖界面 `data-testid` 与 Schema 文案；界面改版后先跑单格子集确认选择器仍有效。
