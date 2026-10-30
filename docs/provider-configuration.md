# Provider 配置 / Provider configuration

本文是 Provider 连接配置的对外契约（内部存档 #252）。变量由 Product Schema `application.runtime.configuration.settings` 声明，`internal/app/provider_environment_doc_test.go` 校验本文与 Schema、`.env.example` 一致。

This page is the public contract for configuring Provider connections (内部存档 #252). Variables are declared in the Product Schema (`application.runtime.configuration.settings`); `internal/app/provider_environment_doc_test.go` checks that this page, the Schema and `.env.example` agree.

## 命名规则 / Naming rule

- 每个 Provider 连接只读两个规范变量：`<ID>_BASE_URL` 与 `<ID>_API_KEY`，`<ID>` 是 Schema 中设置名去掉 `_base_url` / `_api_key` 后的大写形式（`meshy_api_key` → `MESHY_API_KEY`）。Catalog 校验强制这一规则。
- **有 key 才启用**：设置了 `<ID>_API_KEY` 的 Provider 在启动时注册，删除 key 并重启后不再注册；其 Offering 在界面上显示为不可用，不会改用其他模型。API key 不会从其他 Provider 或 Agent 的 key 回退取值。
- `<ID>_BASE_URL` 留空时先读该行列出的已弃用别名，别名也为空才用下表默认地址；没有默认地址的（`GEMINI_BASE_URL`）留空即该连接未配置：有 key 也不注册，启动日志记一条 WARN（`Provider 连接缺少地址，未启用`，`missing_settings` 写缺少的变量名）。
- 每个连接只读自己的地址变量。地址别名不得是其他连接或 Agent 的规范变量（否则本连接的 API key 会发往对方地址），Catalog 校验强制这一点，没有例外。各地址变量的含义见下方“地址变量”。
- 历史别名仍可用但已弃用：用到时启动日志会记一条 WARN（`环境变量使用了已弃用的别名`），写明应改用的规范名。别名将在 1.x 移除。
- 1.x 支持自行添加 Provider 时沿用同一规则。

- Each Provider connection reads two canonical variables, `<ID>_BASE_URL` and `<ID>_API_KEY`, where `<ID>` is the Schema setting name without `_base_url` / `_api_key`, in upper case (`meshy_api_key` → `MESHY_API_KEY`). The Catalog enforces this.
- **Enabled by its key**: a Provider whose `<ID>_API_KEY` is set is registered at startup; remove the key and restart and it is not. Its offerings then show as unavailable in the UI and are never silently replaced by another model. An API key never falls back to another Provider's or the Agent's key.
- An empty `<ID>_BASE_URL` first reads the deprecated alias listed in its row and uses the default below only when that is empty too. A variable with no default (`GEMINI_BASE_URL`) left empty means the connection is not configured: it is not registered even with a key, and startup logs one WARN (`Provider 连接缺少地址，未启用`; `missing_settings` names the missing variable).
- Each connection reads only its own address variable. A base URL alias may not be another connection's or the Agent's canonical variable (this connection's API key would be sent to that address); the Catalog enforces this with no exception. See "Address variables" below for what each one means.
- Historical aliases still work but are deprecated: using one logs a startup WARN naming the canonical variable. Aliases will be removed in 1.x.
- Custom Providers in 1.x will follow the same rule.

## 变量 / Variables

| Provider | 变量 / Variable | 用途 / Purpose | 默认 / Default | 已弃用别名 / Deprecated alias |
|---|---|---|---|---|
| OpenAI 图像 / OpenAI images | `OPENAI_BASE_URL` | API 地址 / API base URL | `https://api.openai.com/v1` | — |
| OpenAI 图像 / OpenAI images | `OPENAI_API_KEY` | 启用并鉴权，**按次计费** / enables and authenticates, **billed per request** | — | — |
| Gemini 图像 / Gemini images | `GEMINI_BASE_URL` | OpenAI 兼容网关地址，**启用 Gemini 时必填** / OpenAI-compatible gateway URL, **required to enable Gemini** | — | — |
| Gemini 图像 / Gemini images | `GEMINI_API_KEY` | 启用并鉴权，**按次计费** / enables and authenticates, **billed per request** | — | — |
| Jimeng（火山方舟 Seedream）/ Jimeng (Volcengine Ark Seedream) | `JIMENG_BASE_URL` | API 地址 / API base URL | `https://ark.cn-beijing.volces.com` | — |
| Jimeng（火山方舟 Seedream）/ Jimeng (Volcengine Ark Seedream) | `JIMENG_API_KEY` | 启用并鉴权，**按次计费** / enables and authenticates, **billed per request** | — | — |
| Meshy | `MESHY_BASE_URL` | API 地址 / API base URL | `https://api.meshy.ai` | `MESHY_API_BASE_URL` |
| Meshy | `MESHY_API_KEY` | 启用并鉴权，**消耗 Meshy 积分** / enables and authenticates, **spends Meshy credits** | — | — |
| Tripo | `TRIPO_BASE_URL` | API 地址 / API base URL | `https://openapi.tripo3d.ai/v3` | `TRIPO_API_BASE_URL` |
| Tripo | `TRIPO_API_KEY` | 启用并鉴权，**消耗 Tripo 积分** / enables and authenticates, **spends Tripo credits** | — | — |
| Hyper3D（Rodin） | `HYPER3D_BASE_URL` | API 地址 / API base URL | `https://api.hyper3d.com/api/v2` | — |
| Hyper3D（Rodin） | `HYPER3D_API_KEY` | 启用并鉴权，**消耗 Hyper3D 积分** / enables and authenticates, **spends Hyper3D credits** | — | — |
| 抠图服务 / Matting service | `MATTING_BASE_URL` | 自部署抠图服务地址 / self-hosted matting service URL | `http://127.0.0.1:8001` | `MATTING_API_URL` |
| 抠图服务 / Matting service | `MATTING_API_KEY` | 启用并鉴权 / enables and authenticates | — | `MATTING_API_TOKEN` |
| Agent 对话模型 / Agent chat model | `LLM_BASE_URL` | OpenAI 兼容 chat completions 地址，**live 必填**；未设置时 Agent 显示“未配置” / OpenAI-compatible chat completions URL, **required for live**; unset shows the Agent as not configured | — | — |
| Agent 对话模型 / Agent chat model | `LLM_API_KEY` | Agent 对话鉴权，**按 token 计费**；未设置时 Agent 显示“未配置” / Agent chat auth, **billed per token**; unset shows the Agent as not configured | — | — |

## 地址变量 / Address variables

每个地址变量只属于一个连接，互不回落：

- `LLM_BASE_URL`：**只**是 Agent 对话模型的 OpenAI 兼容 chat completions 地址，与 `LLM_API_KEY`、`LLM_MODEL` 一起使用。任何图像或 3D Provider 都不读它。
- `OPENAI_BASE_URL`：OpenAI 图像连接的地址，与 `OPENAI_API_KEY` 一起使用；留空用 OpenAI 官方地址。
- `GEMINI_BASE_URL`：Gemini 图像连接的地址，与 `GEMINI_API_KEY` 一起使用。Gemini 图像走网关的 OpenAI 兼容 `chat/completions` 图像契约，不是 Google 原生接口，因此没有默认地址：留空时 Gemini 未配置，不注册、不会把 `GEMINI_API_KEY` 发往任何地址。
- `JIMENG_BASE_URL`、`MESHY_BASE_URL`、`TRIPO_BASE_URL`、`HYPER3D_BASE_URL`、`MATTING_BASE_URL`：各自 Provider 的地址，留空用上表默认地址。

因此只为 Agent 设置 `LLM_BASE_URL`（例如指向 DeepSeek）不会改变任何图像连接的去向。

Each address variable belongs to exactly one connection, and none falls back to another:

- `LLM_BASE_URL` is **only** the Agent chat model's OpenAI-compatible chat completions URL, used with `LLM_API_KEY` and `LLM_MODEL`. No image or 3D Provider reads it.
- `OPENAI_BASE_URL` is the OpenAI images connection's URL, used with `OPENAI_API_KEY`; empty means the official OpenAI URL.
- `GEMINI_BASE_URL` is the Gemini images connection's URL, used with `GEMINI_API_KEY`. Gemini images use a gateway's OpenAI-compatible `chat/completions` image contract, not Google's native API, so there is no default: empty leaves Gemini unconfigured, unregistered, and `GEMINI_API_KEY` is sent nowhere.
- `JIMENG_BASE_URL`, `MESHY_BASE_URL`, `TRIPO_BASE_URL`, `HYPER3D_BASE_URL` and `MATTING_BASE_URL` are each Provider's own URL; empty uses the default in the table above.

Setting `LLM_BASE_URL` for the Agent alone (for example to DeepSeek) therefore never changes where an image connection sends its requests.

## 统一网关 / Unified gateway

所有模型（对话、GPT 图像、Gemini 图像等）都经同一个 LLM API 网关时，把 `LLM_BASE_URL`、`OPENAI_BASE_URL`、`GEMINI_BASE_URL` 都设为该网关地址（例如 `https://gateway.example/v1`），每个变量都要显式写出；以后启用的其他 OpenAI 兼容 Provider 地址同样指向网关。key 仍各自设置：`LLM_API_KEY`、`OPENAI_API_KEY`、`GEMINI_API_KEY` 可以是同一个网关 key，但只有设置了 key 的 Provider 才启用。漏写某个地址变量时，OpenAI 会改连官方地址，Gemini 会显示为未配置，都不会借用 `LLM_BASE_URL`。

When one LLM API gateway serves every model (chat, GPT images, Gemini images and so on), set `LLM_BASE_URL`, `OPENAI_BASE_URL` and `GEMINI_BASE_URL` all to that gateway URL (for example `https://gateway.example/v1`), each one explicitly; point any other OpenAI-compatible Provider address you enable later at the gateway too. Keys are still set per connection: `LLM_API_KEY`, `OPENAI_API_KEY` and `GEMINI_API_KEY` may hold the same gateway key, but only a Provider whose key is set is enabled. A missing address variable is never filled from `LLM_BASE_URL`: OpenAI then calls the official OpenAI URL and Gemini shows as not configured.

## Agent 对话模型 / Agent chat model

`AGENT_MODE=live` 时必须设置 `LLM_BASE_URL` 与 `LLM_API_KEY`，并应设置 `LLM_MODEL`（模型名；留空时用 Schema 默认值，即 llms 网关别名 `deepseek`，只在该网关上有效）。`LLM_BASE_URL` 没有默认地址（#251：容器里没有 127.0.0.1:8000 的网关）。缺少 `LLM_BASE_URL`、`LLM_API_KEY` 或模型名为空时，服务照常启动，启动日志记一条 WARN（`Agent 对话模型未配置`，`missing_settings` 只写缺少的变量名，不写值）；公开 Schema 的 `chat.agent_status.available` 为 `false`，输入区显示 Schema 文案 `chat.presentation.messages.agent_not_configured` 并关闭发送，直接调用对话得到同一文案，提示词增强接口返回 503 与同一文案。补上设置并重启后恢复。`AGENT_MODE=mock` 不受影响。

With `AGENT_MODE=live` you must set `LLM_BASE_URL` and `LLM_API_KEY`, and should set `LLM_MODEL` (the model name; empty uses the Schema default, the llms gateway alias `deepseek`, which only that gateway knows). `LLM_BASE_URL` has no default (#251: a container has no gateway on 127.0.0.1:8000). Without `LLM_BASE_URL` or `LLM_API_KEY`, or with an empty model name, the server still starts and logs one startup WARN (`Agent 对话模型未配置`; `missing_settings` lists only the missing variable names, never values). The public Schema then reports `chat.agent_status.available: false`, the composer shows the Schema text `chat.presentation.messages.agent_not_configured` with sending turned off, a chat turn sent directly gets the same text, and prompt enhancement answers 503 with it. Set the variables and restart to restore the Agent. `AGENT_MODE=mock` is unaffected.

`AGENT_MAX_TOOL_CALLS_PER_REPLY`（#275）限定一次 Agent 回复最多实际执行多少次工具调用，每次新回复从 0 起算；同意后续跑或服务重启恢复时，已执行的调用仍计入同一次回复。留空用 Schema 默认值 `chat.agent.max_tool_calls_per_reply`（20）；取值必须是 1 到 `chat.agent.max_tool_calls_per_reply_maximum`（100）的整数，否则启动失败。用完后 Agent 不再调用工具，只做一次总结，并列出本轮未执行的工具和意图，用户确认后再发一条消息继续。自部署写在 `.env`；测试栈与正式栈写在各自的运行环境文件，Compose 原样透传。

`AGENT_MAX_TOOL_CALLS_PER_REPLY` (#275) caps the tool calls one Agent reply actually runs; every new reply starts at 0, and calls already run before a consent resume or a restart still count toward the same reply. Empty keeps the Schema default `chat.agent.max_tool_calls_per_reply` (20); a value must be an integer from 1 to `chat.agent.max_tool_calls_per_reply_maximum` (100), otherwise the server refuses to start. Once the budget is spent the Agent calls no more tools, writes one summary and lists the tools it still wanted with their intent; send another message to continue. Self-deploy sets it in `.env`; the test and formal stacks set it in their runtime environment files, which Compose passes through.

## 多个对话模型连接 / Multiple chat model connections

（#322）默认连接就是上面的 `LLM_BASE_URL`、`LLM_API_KEY`、`LLM_MODEL`（连接 ID `LLM`，可选 `LLM_CONTEXT_WINDOW_TOKENS`），名字不变。要让用户在输入区选择更多对话模型，在 `LLM_CONNECTIONS` 里列出额外的连接 ID（逗号分隔，大写字母开头，只含大写字母、数字和下划线，最多 32 个字符；连默认连接最多 16 个）。连接 `<ID>` 只读自己的四个变量：

| 变量 / Variable | 说明 / Meaning |
|---|---|
| `<ID>_BASE_URL` | 该连接的 OpenAI 兼容 chat completions 地址，live 必填 / This connection's OpenAI-compatible chat completions URL, required for live |
| `<ID>_API_KEY` | 该连接的 key，live 必填 / This connection's key, required for live |
| `<ID>_MODEL` | 该连接的模型名，必填（一个连接一个模型） / The model name, required (one model per connection) |
| `<ID>_CONTEXT_WINDOW_TOKENS` | 可选，覆盖模型 profile 的上下文窗口 / Optional, overrides the model profile's context window |

没有任何回落：`QWEN` 缺少 `QWEN_BASE_URL` 或 `QWEN_API_KEY` 时只显示“未配置”，不会借用 `LLM_*` 或别的连接。统一网关下每个连接都显式写同一个网关地址和 key，例如：

```env
LLM_BASE_URL=https://gateway.example/v1
LLM_API_KEY=...
LLM_MODEL=deepseek
LLM_CONNECTIONS=QWEN,GLM
QWEN_BASE_URL=https://gateway.example/v1
QWEN_API_KEY=...
QWEN_MODEL=qwen3.8-plus
GLM_BASE_URL=https://gateway.example/v1
GLM_API_KEY=...
GLM_MODEL=glm-5.3
```

连接 ID 推导出的变量名不能与 Schema 已声明的其他变量重名（例如 `OPENAI` 会读到 `OPENAI_BASE_URL`），否则启动失败并说明原因；ID 不合法、重复或与默认连接同名同样启动失败。错误信息与日志只写变量名，不写值。哪些连接对用户开放、默认模型、每个模型开放的思考档位与默认档，由管理员在管理后台「LLM 设置」里决定（见 `docs/product.md` §4.3）；页面只显示每个变量“已设置/未设置”，不显示值。`AGENT_MODE=mock` 时连接只需要 `<ID>_MODEL`。

(#322) The default connection is still `LLM_BASE_URL`, `LLM_API_KEY` and `LLM_MODEL` above (connection ID `LLM`, optional `LLM_CONTEXT_WINDOW_TOKENS`); nothing is renamed. To let users pick more chat models in the composer, list extra connection IDs in `LLM_CONNECTIONS` (comma separated; an upper-case letter first, then upper-case letters, digits and underscores, at most 32 characters; at most 16 connections including the default). Connection `<ID>` reads only its own four variables (table above). Nothing falls back: a `QWEN` without `QWEN_BASE_URL` or `QWEN_API_KEY` shows as not configured and never borrows `LLM_*` or another connection's values. With one gateway, set the same gateway URL and key on every connection explicitly (example above). A connection ID whose variable names clash with another variable the Schema declares (for example `OPENAI` would read `OPENAI_BASE_URL`), an invalid or duplicate ID, or `LLM` itself stops the server at startup with the reason; errors and logs name variables, never values. Which connections users may use, the default model, and the thinking levels each model offers are set by an administrator on the admin page "LLM settings" (see `docs/product.md` §4.3); the page only shows whether each variable is set, never its value. With `AGENT_MODE=mock` a connection needs only `<ID>_MODEL`.

## 计费风险 / Billing risk

只配置你要使用的 Provider 的 key。真实生成、换贴图、绑骨等步骤会消耗对应账户的积分或费用；会话的批准方式（自动/手工）决定付费步骤是否先等人确认。默认 `PROVIDER_MODE=mock` 不调用任何付费上游。

Configure keys only for the Providers you use. Real generation, retexturing, rigging and similar steps spend that account's credits or money; the session approval mode (auto / manual) decides whether a paid step waits for a person. The default `PROVIDER_MODE=mock` calls no paid upstream.
