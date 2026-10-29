# Provider 错误取证与分类（2026-09-17）

本文档描述 Go 运行时对上游 Provider 错误的统一处理：错误如何分类、
上游证据如何记录、以及第三方服务问题与我方问题的边界。
错误码真源见各官方文档（检索记录：
`.opencode/workflow-reports/error-contract-research/provider-error-contracts.md`，不入库）。

## 取证字段

`generation.Failure`（`internal/generation/types.go`）在分类字段之外携带三个内部取证字段：

| 字段 | 含义 | 来源 |
|---|---|---|
| `upstream_status` | 上游 HTTP 状态码 | 所有非 2xx/无效 JSON 路径 |
| `upstream_code` | 上游机器错误码 | OpenAI/Ark `error.code`、Tripo 整型 `code`、Meshy `task_error.code` |
| `upstream_excerpt` | 去敏响应体摘录（≤400 个字符，按 rune 截断；URL 换成 `[url]`，签名 query、令牌、邮箱、请求自带的提示词回显被剔除；`generation.UpstreamExcerpt` 是唯一实现，Provider 调用并由内核落库与写日志前统一再执行一遍，#121） | 全部错误路径 |

这些字段：

- **落库**：`generation_attempts.failure_json` 与任务失败快照自动携带
  （结构体整体 JSON 序列化，无需额外列）。
- **日志**：每次尝试终态失败与每次轮询失败都输出结构化日志
  （`generation attempt finished failed` / `generation observation deferred`），
  包含 task_id/attempt_id/provider/offering/class/upstream_* 全量字段。
- **不进用户文案**：`SafeDetail` 与 catalog `generation_messages` 是用户可见文案的
  唯一来源；`submission_unknown` 归一化会替换 SafeDetail 但**保留取证字段**
  （回归测试：`TestSubmissionUnknownPreservesUpstreamEvidence`）。

诊断入口：

```sql
SELECT task_id, state, failure_json FROM generation_attempts
WHERE failure_json LIKE '%upstream_status%';
```

```sh
docker logs <container> 2>&1 | grep 'generation attempt finished failed'
```

## 分类与归属

| FailureClass | 归属 | 典型上游表现 |
|---|---|---|
| `invalid_input` | 我方请求/用户输入 | 400（参数非法）、网宿 403（请求体错误）、Tripo 1003/1004/2002-2004/2009/2013/2015-2017、Meshy `task_error.type=invalid_input`、Ark `InvalidParameter.*`、Gemini 内容拦截码（`safety`、`prohibited_content`、`image_safety` 等 11 个，官方页面未列 HTTP 状态，所以按码声明、不依赖状态；#139） |
| `authentication` | 我方配置 | 401、Tripo 1002/1005、Meshy 401 |
| `rate_limited` | 第三方限流 | 429、Tripo 1007/2000 |
| `timed_out` | 第三方 | 408/504、Meshy `task_error.type=timeout` |
| `upstream_unavailable` | 第三方 | 5xx（非提交）、Meshy `service_unavailable`/`server_error`、Tripo 1000/1001 |
| `insufficient_balance` | 第三方账户（余额不足、欠费） | OpenAI 兼容 `error.code=insufficient_quota`（OpenAI 与 Gemini 图片连接都声明）、Gemini `payment_required` 与 402（Gemini、OpenAI 图片连接）、方舟/即梦 `AccountOverdueError`、Meshy 402、Tripo 2010、Rodin `API_INSUFFICIENT_FUNDS`，以及图片连接上消息含 insufficient balance / account in arrears 的错误；不可重试 |
| `credential_quota` | 第三方 Key 额度（账户有余额，但该 API Key 达到服务商控制台设置的额度上限） | Gemini `quota_exceeded`（429，日额度用尽，等额度重置）、方舟/即梦 `QuotaExceeded` 与 `SetLimitExceeded`（429，试用额度用尽或用户设定的用量上限，等待分钟级限流窗口无用，不可重试，#139）；Meshy 402 且消息含 `credit limit`（连接 `message_matchers` 声明 `before_status_ranges` 与 `http_status: {min: 402, max: 402}`，先于 402 状态区间匹配，别的状态不匹配；#93、#121，待 dev-test 验收） |
| `rejected` | 上游业务拒绝 | Meshy 404/409、Tripo 2001/2005-2008/2011/2012/2014/2018/2019、任务被取消 |
| `submission_unknown` | 歧义保护 | 创建类 408/5xx：无法证明上游拒收，禁止自动重发 |
| `protocol` / `integrity_failure` / `internal` | 我方代码 | 无效 JSON、资产校验失败、内部状态错误 |

分类实现：

- 通用 HTTP 分类：`internal/providers/httpjson/client.go` `classifyHTTP`
  （状态码 → class，错误体 `message/detail/error.message` → SafeDetail）。
- 网宿网关专属：`httpjson.ClassifyGatewayError`（schemaimages 注入；
  403 按网关文档归 `invalid_input` 而非鉴权）。
- Tripo 专属：连接 `error_mapping.upstream_code_map`（官方码表全量映射），由
  `internal/providers/tripo/adapter.go` 的 `businessEnvelopeError`（非 2xx 信封的 `code`）和
  `observation`（#142：状态为 `failed`/`banned`/`expired` 的任务对象里的 `error_code`）共用同一张表。
  任务级读取规则：状态词自己声明了 `class` 就以它为准，否则按 `error_code` 查表，查不到仍是
  `rejected`；`retryable` 只取状态词规则（`failed` 不可重试），不会因为码是 1000/1001 就重试一个已失败任务；
  `error_message`（官方 Python SDK 读作 `error_msg`）经 `generation.UpstreamExcerpt`（去敏并去掉输入回显）
  与 `error_code` 只写入内部取证字段 `UpstreamCode`/`UpstreamExcerpt`，`SafeDetail` 仍是固定的
  “Tripo 任务状态为 failed”，公开投影只用 Schema 文案。真实样本：#89 的 smartsegment 失败任务 `error_code` 1001
  （内部错误，`upstream_unavailable`）；#96 观测的 retarget 在 v2 绑骨结果上 1004（`invalid_input`）；余额不足 2010。
- Tripo `unknown` 状态词（#142）：Schema 状态词表声明为 `running` 相位，带 `max_consecutive: 20` 与
  `class: upstream_unavailable`。依据：官方 Python SDK（VAST-AI-Research/tripo-python-sdk）的 `TaskStatus`
  列出 `unknown`，其 `wait_for_task` 只在 success/failed/cancelled/banned/expired 停止，即 `unknown` 不是终态；
  但官方没有给出它的持续时间，所以连续出现 20 次后适配器以 `upstream_unavailable` 结束（不可重试），
  已知状态词会重置计数。计数在适配器进程内按远端任务保存，进程重启时重新计数。
- 余额不足（`insufficient_balance`，2026-10-08 新增，源码已实现，待 dev-test 验收）：
  各连接在 Schema `error_mapping` 里声明，不在代码里按 Provider 分支。通用算法
  `httpjson.matchErrorMapping` 先看“被声明为 insufficient_balance 的错误码或消息匹配”，
  再看 HTTP 状态区间——因为余额错误的状态码各家不一（402、403、429），
  不能让 `403 → invalid_input` 这样的宽区间盖住它。消息匹配只用英文短语
  （insufficient balance / insufficient funds / account in arrears），不含中文词，
  避免上游回显用户提示词时误判；遇到新的网关措辞，在连接的 `message_matchers`
  里补一条即可。该类不可重试，创建类请求的 402/403/429 不是歧义提交。
  各家的码来自其公开错误文档，真实环境的取证样本待 dev-test 补充。
- Meshy 任务错误：`internal/providers/meshy/adapter.go`（`task_error.type`
  决定 class 与可重试性：timeout/service_unavailable/server_error 可重试）。
- 即梦/火山 Ark：走通用分类 + OpenAI 兼容信封 `error.code` 提取；`QuotaExceeded`、`SetLimitExceeded`（均 429）声明为 `credential_quota`，否则会按 429 读成可重试的限流（#139，码表来自 <https://docs.volcengine.com/docs/82379/1299023>，2026-10-29 核对）。
- Gemini 图片连接（经 OpenAI 兼容网关）：官方 [API errors](https://ai.google.dev/gemini-api/docs/api-errors)（2026-10-29 核对）的 `error.code` 是 snake_case 字符串，未列出的码取 HTTP 状态的 snake_case。连接声明 402 → `insufficient_balance`；码表 `insufficient_quota`、`payment_required` → `insufficient_balance`，`quota_exceeded` → `credential_quota`，`failed_precondition`（如未开通计费）→ `rejected`，`out_of_range` → `invalid_input`，内容拦截码 → `invalid_input`；`rate_limit_exceeded`、`too_many_requests`（429）与 5xx 沿用通用状态分类，创建类请求的 5xx 先成为 `submission_unknown`，不看码表。403 的 `permission_denied` 被连接的 403 → `invalid_input` 区间先匹配，与 OpenAI、即梦连接一致，未改。以上都是按官方页面和网关信封构造的答复，没有从真实服务观测到；`finish_reason` 为内容过滤的 200 答复没有图片，仍按“上游完成但未返回图片”读成 `protocol`（未处理，见 #139）。OpenAI 图片连接同批补了 402 区间，网关对两条路由给同样的余额答复。

## 用户可见文案

用户侧文案唯一真源是 catalog `application.content.generation_messages`
（`failure.invalid_input` … `failure.internal`，中文，含归属表述；
`failure.insufficient_balance` 明确说是该模型服务商账户的问题）。
Agent 工具结果里的失败文案来自 `chat.presentation.messages.generation_failed_by_class`
（按失败分类覆盖通用的 `generation_failed`），因此 Agent 和用户听到的是同一个真实原因。
任何上游原文只进取证字段，不进用户文案。
