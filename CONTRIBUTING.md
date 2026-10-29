# 参与开发 / Contributing

中文在前，[English below](#english)。

## 仓库与 CI

- 主仓库在 GitHub：<https://github.com/LeXwDeX/Plinth>。Issue 与 Pull Request 都在这里提交。
- 维护者另有一个内网镜像，只从 GitHub 单向同步（GitHub → 镜像），不接受直接提交。
- 每个 PR 在 GitHub Actions（`.github/workflows/ci.yml`）上跑公共门禁，共三个作业：`frontend`
  （类型检查、单元测试、构建、外链检查）、`go check`（`go vet`、`go build`、公开快照扫描）与
  `go test race`。CI 不构建镜像（#257），只用公共依赖源，不需要任何密钥。
- 零 Key 的 Mock 自部署冒烟（`scripts/smoke/smoke-public-mock.sh`）不是公共 CI 作业；需要时按
  README「Docker Compose 自部署」在本机运行。改了 README 的部署步骤、`docker-compose.yml` 或
  `.env.example` 时，同步检查该脚本。
- 真实 Provider 付费验收等较重的测试由维护者在内部测试环境执行，不是外部 PR 的
  合并条件。分工细节见 `dev_test.md` §2「CI 分工」。

## 开发环境

| 工具 | 版本 |
|---|---|
| Go | 1.26+（CI 用 1.27） |
| Node.js | 22 |
| pnpm | `package.json` 的 `packageManager`，用 `npm install -g pnpm@12.5.1` 安装（Node 22.21.1 自带的 corepack 0.34.0 经 `corepack enable` 取不到该版本；已启用 corepack shim 时先 `corepack disable`） |
| Python | 3（本地门禁脚本） |
| git | 公开树检查（`public tree`）需要在 Git 仓库内运行 |
| bash、jq | 部分前端测试会调用；缺失时 `pnpm test` 报 `jq: command not found` |
| sqlite3 | `scripts/tests` 的割接验收测试调用 `sqlite3` 命令行；缺失时 31 个用例报 `FileNotFoundError: 'sqlite3'` |
| C 编译器（`gcc` 或 `cc`） | 仅 `--race` 需要（CGO） |
| Docker | 可选，用于构建运行镜像 |

```bash
git clone https://github.com/LeXwDeX/Plinth.git
cd Plinth
pnpm install --frozen-lockfile
pnpm build                                   # 前端输出到 build/go-web
WEB_ASSETS_ROOT=build/go-web go run ./cmd/art-ai
```

不设置任何环境变量时，服务使用 Mock Provider、Mock Agent 和开发身份，监听
`127.0.0.1:13333`，数据写在当前目录的 `data/` 与 `storage-go/`。开发身份只用于本机，
`APP_ENV=production` 时会拒绝启动。

## 测试与门禁

提交 PR 前在本机运行：

```bash
python3 scripts/verify-local.py           # 八项本地门禁
python3 scripts/verify-local.py --race    # 再加上 race 测试（CI 必跑，较慢）
```

八项依次是：`pnpm exec tsc --noEmit`、`pnpm test`、`pnpm build`、`go vet ./...`、
`go test -count=1 ./...`、`CGO_ENABLED=0 go build -trimpath ./cmd/art-ai`、
`bash scripts/check-public-tree.sh`（已跟踪文件不得含内部主机、地址或服务器路径）、
`python3 -m unittest discover -s scripts/tests -p 'test_*.py'`（发布与门禁脚本的单测）；race 项是
`CGO_ENABLED=1 go test -race -count=1 ./...`。门禁清单的唯一真源是 `dev_test.md` §2「自动化门禁项」，
`src/__tests__/ci-gate-parity.test.ts` 保证本地入口、GitHub Actions 与该清单一致：增删门禁时要
同一提交里一起改。

- 行为改动先写能失败的测试，再改实现；修 bug 时补回归测试。
- 涉及 Provider 上游协议的测试，依据该端点当前的官方文档；测试不得调用真实付费接口，
  用离线 fixture 与 Mock。
- 不要为了让测试通过而放宽断言、跳过测试或改 Mock 期望。

## 代码约定

- **Product Schema 是唯一真源**：Provider、模型、参数、费用、文案和界面配置写在
  `internal/catalog/default_catalog.json`，不要在前端或 HTTP Handler 里另写一份。新增界面文案要同时
  提供中文与英文译文（Catalog 校验会检查已纳入覆盖范围的文案）。
- **统一任务内核**：生成与处理任务的状态只由 `internal/generation` 推进。
- 改动模块职责、依赖方向、数据契约或部署结构时，同步更新 `docs/architecture.md`。
- 不提交密钥、`.env`、数据库或生成的资产；文档和代码里不写具体服务器地址。

## 提交与 PR 规范

- 提交信息用 `类型(范围): 摘要`，类型取 `feat`、`fix`、`docs`、`test`、`refactor`、`ci`、`chore`
  等，例如 `fix(i18n): ...`、`ci: ...`。中文或英文均可，摘要说明改了什么。
- 一个 PR 只做一件可以独立验证的事；在描述里写清动机、改动、验证方法和结果
  （例如 `verify-local.py` 的输出）以及未验证的部分，并关联对应 Issue。
- PR 需要 CI 全部通过，并经维护者审查后合并。

## 许可证

MIT，见 [LICENSE](LICENSE)。提交贡献即表示你同意按该许可证发布你的贡献。

---

<a id="english"></a>

## Repository and CI

- The main repository is on GitHub: <https://github.com/LeXwDeX/Plinth>. Open issues and pull
  requests there.
- The maintainers keep an internal mirror that is synced one way from GitHub (GitHub →
  mirror) and accepts no direct commits.
- Every PR runs the public checks on GitHub Actions (`.github/workflows/ci.yml`) in three jobs:
  `frontend` (type check, unit tests, build, external-link check), `go check` (`go vet`,
  `go build`, public-tree scan) and `go test race`. CI does not build images (#257). They use
  public package sources only and need no secrets.
- The zero-key Mock self-deploy smoke (`scripts/smoke/smoke-public-mock.sh`) is not a public CI
  job; run it locally after following the README "Self-deploy with Docker Compose" steps when
  needed. When you change the README deployment steps, `docker-compose.yml` or `.env.example`,
  check that script too.
- Heavier testing, such as paid acceptance against real Providers, is run by the maintainers
  in internal test environments and is not a merge requirement for external PRs. The split is described in `dev_test.md` §2 (in Chinese).

## Development setup

| Tool | Version |
|---|---|
| Go | 1.26+ (CI uses 1.27) |
| Node.js | 22 |
| pnpm | the `packageManager` in `package.json`; install with `npm install -g pnpm@12.5.1` (the corepack 0.34.0 bundled with Node 22.21.1 cannot fetch it through `corepack enable`; if the corepack shims are already enabled, run `corepack disable` first) |
| Python | 3 (local check script) |
| git | the `public tree` check must run inside a Git repository |
| bash, jq | used by some frontend tests; without them `pnpm test` fails with `jq: command not found` |
| sqlite3 | the cutover acceptance tests in `scripts/tests` call the `sqlite3` command line; without it 31 cases fail with `FileNotFoundError: 'sqlite3'` |
| C compiler (`gcc` or `cc`) | needed only by `--race` (CGO) |
| Docker | optional, to build the runtime image |

```bash
git clone https://github.com/LeXwDeX/Plinth.git
cd Plinth
pnpm install --frozen-lockfile
pnpm build                                   # the frontend goes to build/go-web
WEB_ASSETS_ROOT=build/go-web go run ./cmd/art-ai
```

With no environment variables set, the service uses the Mock Provider, the Mock Agent and
a development identity, listens on `127.0.0.1:13333`, and writes data to `data/` and
`storage-go/` in the current directory. The development identity is for local use only;
the service refuses to start with it when `APP_ENV=production`.

## Tests and checks

Before opening a PR, run:

```bash
python3 scripts/verify-local.py           # the seven local checks
python3 scripts/verify-local.py --race    # plus the optional race test (always run in CI, slower)
```

The seven checks are `pnpm exec tsc --noEmit`, `pnpm test`, `pnpm build`, `go vet ./...`,
`go test -count=1 ./...`, `CGO_ENABLED=0 go build -trimpath ./cmd/art-ai` and
`bash scripts/check-public-tree.sh`; the race check is
`CGO_ENABLED=1 go test -race -count=1 ./...`. The single source of truth for the list is
`dev_test.md` §2; `src/__tests__/ci-gate-parity.test.ts` keeps the local script and GitHub
Actions in line with it, so change them together in one commit when you add or remove a
check.

- For behavior changes, write a failing test first, then change the code; add a regression
  test when fixing a bug.
- Tests of a Provider's upstream protocol follow that endpoint's current official
  documentation. Tests must not call real paid APIs; use offline fixtures and the Mock.
- Do not loosen assertions, skip tests or change Mock expectations just to make tests pass.

## Code conventions

- **The Product Schema is the single source of truth**: Providers, models, parameters,
  prices, UI text and UI settings live in `internal/catalog/default_catalog.json`; do not
  duplicate them in the frontend or HTTP handlers. New UI text needs both Chinese and
  English translations (the Catalog check enforces this for the covered sections).
- **One task kernel**: only `internal/generation` advances the state of generation and
  processing tasks.
- When you change module responsibilities, dependency direction, data contracts or the
  deployment layout, update `docs/architecture.md` in the same change.
- Never commit secrets, `.env`, databases or generated assets, and do not write concrete
  server addresses into docs or code.

## Commits and pull requests

- Commit messages use `type(scope): summary`, with types such as `feat`, `fix`, `docs`,
  `test`, `refactor`, `ci` and `chore`, for example `fix(i18n): ...` or `ci: ...`. Chinese or
  English is fine; the summary says what changed.
- Keep one independently verifiable change per PR. In the description, give the motivation,
  the change, how you verified it and the result (for example the `verify-local.py`
  output), what is not verified, and the related issue.
- A PR is merged after all CI checks pass and a maintainer has reviewed it.

## License

MIT, see [LICENSE](LICENSE). By contributing you agree that your contribution is released
under that license.
