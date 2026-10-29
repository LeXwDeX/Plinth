# openai v1 对齐实测报告

- 官方 API：`dev-test OpenAI-compatible LLM gateway /v1`
- 核对日期：2026-10-29
- 官方请求数：8（其中付费：4）；fake 请求数：8
- 差异：0 项，其中未说明：0 项

只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。

## 步骤

| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |
|---|---|---|---|---|---|---|---|
| models | `GET /models` | Authorization: Bearer <key> | 200/200 | 16 |  |  | 0 (0) |
| models-no-auth | `GET /models` | none | 401/401 | 0 |  |  | 0 (0) |
| chat-no-auth | `POST /chat/completions` | none | 401/401 | 0 |  |  | 0 (0) |
| chat-invalid | `POST /chat/completions` | Authorization: Bearer <key> | 400/400 | 6 |  |  | 0 (0) |
| chat-completion | `POST /chat/completions` | Authorization: Bearer <key> | 200/200 | 25 |  |  | 0 (0) |
| chat-stream | `POST /chat/completions` | Authorization: Bearer <key> | 200/200 | 25 |  |  | 0 (0) |
| chat-tool-call | `POST /chat/completions` | Authorization: Bearer <key> | 200/200 | 33 |  |  | 0 (0) |
| chat-tool-call-stream | `POST /chat/completions` | Authorization: Bearer <key> | 200/200 | 33 |  |  | 0 (0) |

## 请求差异说明

- 工具调用步骤（chat-tool-call、chat-tool-call-stream）：发给 fake 的请求在最后一条 user 消息末尾多了 fake 专用的工具标记 `[[fake:tool:lookup_note {"topic":"cube"}]]`，其余字节相同；官方模型自行决定调用工具，fake 只在标记出现时返回工具调用。
- fake 的流式分片按官方 2026-10-29 实测的分片模式生成（角色、推理、正文或工具调用头与参数、结束分片）；已提交样本经 wirefixture 规范化后合并了中间分片，字段集合一致、中间分片的 null 类型不保留，所以漂移测试对 SSE 只比字段集合。

## 端点证据

`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。

| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |
|---|---|---|---|---|---|
| `GET /models` | real | GET /v1/models | https://platform.openai.com/docs/api-reference/models/list | 2026-10-29 |  |
| `POST /chat/completions` | real | POST /v1/chat/completions | https://platform.openai.com/docs/api-reference/chat/create | 2026-10-29 | non-streaming, SSE streaming, tool_calls, reasoning_content and the model_required error are all real samples |

## 差异

无。
