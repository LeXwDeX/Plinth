# 外部 MCP 使用文档

本文面向外部接入方（脚本、桌面工具或别的 Agent），说明怎样用本人的个人访问 Key 调用 Plinth 的**对外 MCP 监听**：连接地址与鉴权、无状态 JSON-RPC 协议、工具与参数、Tasks 扩展、状态块、签名下载、限流与错误码，最后一节给出完整调用示例。

> 平台内 Agent 使用的 `POST /api/v1/mcp` 是另一个端点，只服务平台自己的 Agent，不接受访问 Key。本文说的监听是进程内独立开启的第二个 HTTP 端口，只挂 MCP 路由。

## 1. 概述

- **独立监听、无状态 HTTP。** 对外 MCP 由部署方单独开启，地址与主 Web 服务不同；只挂三条路由：JSON-RPC 端点、鉴权产物下载、短时签名下载。其余路径一律 JSON 404，主 Web 服务也不挂这三条路由。
- **协议固定为 2026-07-28。** JSON-RPC 2.0，每个请求自带版本、方法与客户端元数据；没有 `initialize`，没有会话 ID，不提供 SSE 流，任何请求都可以由任意实例处理。
- **只认个人访问 Key。** `Authorization: Bearer plk_…`；Cookie、部署令牌、开发身份一律不认。Key 只代表**持有人本人**的范围，管理员的 Key 也没有管理员权限。
- **工具分两类。** 只读工具（查询服务、状态、任务、产物、模型检查、图片检查）要 `read` 范围；发起工作的工具（上传、生图、去背景、生成 3D、加工模型）要 `generate` 范围，`generate` 包含 `read`。
- **每次工具调用都带状态块。** 响应里的 `structuredContent.status` 给出 3D 步数模式、已用步数、供应商余额、每步大约费用、失败归因与平台自检结论，外部 Agent 在默认的「Agent 自己管理」模式下自行决定是否继续。
- **可选 Tasks 扩展。** 客户端在请求里声明 `io.modelcontextprotocol/tasks` 后，发起工作的工具返回 `CreateTaskResult`，用 `tasks/get` 轮询、用 `tasks/cancel` 取消。
- **没有 TLS。** 监听定位是内网或本机直连。Bearer Key 与签名地址都不能在公网上走明文；要对公网开放，须由部署方在进程里配置证书或在前方放 TLS 终结（本仓库不提供）。

## 2. 连接与鉴权

### 2.1 监听地址与路由

监听地址由部署方给出（运行设置 `MCP_PUBLIC_LISTEN_ADDR`，缺省为空；为空就不监听）。接入方拿到的是 `http://<your-host>:<port>` 或 TLS 终结后的 `https://<your-host>`。

默认路由（路径以部署方 Catalog 为准）：

| 方法与路径 | 用途 |
|---|---|
| `POST /mcp` | JSON-RPC 端点（`server/discover`、`tools/list`、`tools/call`、`tasks/get`、`tasks/cancel`） |
| `GET /mcp/tasks/{taskID}/assets/{assetID}` | 需要同一把 Key 的产物下载 |
| `GET /mcp/signed-assets/{token}` | 短时签名产物下载，不要求 `Authorization` |

约定：

- JSON-RPC 端点只接受 `POST`。`GET`（探测 SSE 流）与 `DELETE`（探测会话）在要 Key 之前就返回 `405` 与 `Allow: POST`；其他路径返回 JSON 404。
- 两条下载路由只接受 `GET`。
- 监听不信任任何代理：客户端地址就是到达监听的 TCP 对端，`X-Forwarded-For` 之类的转发头既不能让被节流的调用方脱身，也不能把别人的地址拖进节流。同一 NAT 或经 Docker 网关到达的调用方共用一个地址、一起计数。

### 2.2 请求头

每个请求都要带：

| 请求头 | 值 |
|---|---|
| `Authorization` | `Bearer plk_<id>_<secret>`（`Bearer ` 前缀区分大小写） |
| `MCP-Protocol-Version` | `2026-07-28`，必须与请求 `_meta` 里的版本一致 |
| `Mcp-Method` | 本次 JSON-RPC 方法名，如 `tools/call` |
| `Mcp-Name` | 仅 `tools/call` 需要：与 `params.name` 完全相同的工具名 |
| `Content-Type` | `application/json`（建议总是带上） |

- 没有 Key、Key 形态不对、Key 错误/吊销/过期、持有人已删除或停用，一律 `401`，带 `WWW-Authenticate: Bearer realm="Plinth"` 与 JSON-RPC 错误体（码 `-32001`）。调用方分不出是哪一种，探测不到 Key 是否存在。
- **所有方法都要 Key**，包括 `server/discover` 与 `tools/list`；没有匿名发现入口。
- 管理员可以整体关闭访问 Key 的对外使用。关闭期间，端点、鉴权下载与签名下载在检查 Key 之前一律返回 `403`，JSON-RPC 码 `-32005`；这与单把 Key 无效（`401`）不同，等管理员重新开启即可，不必换 Key。

Key 的持有人必须是平台里真实存在且可用的用户；只存在于开发/Mock 身份里、没有落到用户目录的「用户」不能签发可用的 Key。

### 2.3 范围

| 范围 | 含义 |
|---|---|
| `read` | 查询与下载：只读工具、`tasks/get`、鉴权下载路由（签名下载不查 Key） |
| `generate` | 发起生成与加工，同时包含 `read` |

- Key 的存储与列表保留签发时的范围；认证出的身份会补上包含的范围（`generate` 自动带 `read`），所以只有 `generate` 的 Key 也能调只读工具和下载。只有 `read` 的 Key 不会获得 `generate`。
- `tools/list` 只列当前 Key 范围允许的工具。范围不够的 `tools/call` 与鉴权下载返回 `403`，带 `WWW-Authenticate: Bearer error="insufficient_scope", scope="read"`（或 `generate`），工具码 `-32003`。
- `upload_image` 属于发起工作的工具，需要 `generate`；只有 `read` 的 Key 不能上传。

### 2.4 权限边界

访问 Key **不是**管理员凭证，也不等于浏览器里的本人：

- Key 只代表持有人的所有者范围：用户的角色不随 Key 走，管理员的 Key 也读不到别人的数据、列不出全部任务；别人的任务对它就是「不存在」。
- Key 不能管理 Key：创建、列出、吊销访问 Key 只认真实的浏览器登录会话。
- Key 不能切换批准方式、不能批准或拒绝批准卡、不能修改 3D 步数模式；这些只认浏览器会话。
- Key 发起的付费步骤没有批准卡：2D 直接准备执行，3D 按持有人的 **MCP 步数模式**当场批准或拒绝（见 §6.1）。
- Key 不能给自己调高步数上限；需要本人在浏览器里修改。

### 2.5 Key 的创建、作用域与吊销

访问 Key 由本人在浏览器登录后，通过账户接口管理（默认路由；需要真实浏览器会话）：

| 方法与路径 | 用途 |
|---|---|
| `POST /api/v1/account/access-keys` | 创建 Key，请求体 `{"name": "...", "scopes": ["read"] 或 ["generate"], "ttl_days": 90}` |
| `GET /api/v1/account/access-keys` | 列出本人 Key（只有前缀与状态，不含明文） |
| `DELETE /api/v1/account/access-keys/{id}` | 立即吊销 |

默认 Catalog 的约束：每人最多 10 个有效 Key；有效期默认 90 天、最长 365 天；名称最长 80 个字符；范围只能是 `read` 与 `generate`。明文 Key **只在创建响应里返回一次**（字段 `key`），服务端只存 SHA-256；列表不再返回明文或摘要。吊销立即生效；重复吊销仍成功，不写新的审计。

## 3. 协议约定

### 3.1 无状态信封

每个请求是一个 JSON 对象：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list",
  "params": {
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0"},
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

规则：

- `jsonrpc` 必须是 `"2.0"`；`id` 必须是非空字符串或整数（不能是 `null`），响应原样回显。
- `params` 必须是对象，且**每个方法**都要带 `_meta`；`_meta` 必须同时含 `io.modelcontextprotocol/protocolVersion`、`io.modelcontextprotocol/clientInfo`（对象）与 `io.modelcontextprotocol/clientCapabilities`（对象，可以为空）。
- `MCP-Protocol-Version` 必须等于 `_meta` 里的版本；两者不一致是 `-32020`。当前只接受 `2026-07-28`，其他版本是 `-32022`，`data.supported` 给出支持的版本、`data.requested` 给出请求的版本。
- `Mcp-Method` 必须等于 JSON-RPC 方法名；`tools/call` 还必须让 `Mcp-Name` 等于 `params.name`，否则是 `-32020`。
- 没有 `initialize`、没有会话 ID、没有 `Mcp-Session-Id`；`initialize` 与其他未知方法都返回 `404` 与 `-32601`。
- 服务端只实现 `tools` 能力，以及可选的 Tasks 扩展；没有 prompts、resources、sampling、roots 等能力。

### 3.2 方法

| 方法 | 作用 |
|---|---|
| `server/discover` | 返回服务名、版本、能力（含可选的 Tasks 扩展）与一段使用说明 |
| `tools/list` | 列出当前 Key 范围允许的工具、说明、输入 Schema 与输出 Schema |
| `tools/call` | 调用一个工具 |
| `tasks/get` | 查询一个 Task（需要 `read`） |
| `tasks/cancel` | 请求取消一个 Task（需要 `generate`） |

`server/discover` 的结果：

```json
{
  "resultType": "complete",
  "serverInfo": {"name": "Plinth", "version": "1"},
  "capabilities": {
    "tools": {},
    "extensions": {"io.modelcontextprotocol/tasks": {}}
  },
  "instructions": "…"
}
```

`capabilities.extensions` 只在部署启用了 Tasks 扩展时出现；`instructions` 随请求语言。

`tools/list` 的结果：

```json
{
  "resultType": "complete",
  "tools": [
    {
      "name": "get_task",
      "description": "…",
      "inputSchema": {"type": "object", "properties": {"task_id": {"type": "string"}}, "required": ["task_id"], "additionalProperties": false},
      "outputSchema": {"type": "object", "properties": {"task": {}, "status": {}}}
    }
  ],
  "ttlMs": 0,
  "cacheScope": "private"
}
```

- 工具按 Catalog 声明顺序返回；列表只含当前 Key 范围允许的工具。
- 工具参数对象是封闭的：未声明的参数会被拒绝，不会「声明了却没执行」。唯一明确声明的开放对象是 `parameters`（服务的可调参数自由映射），它的键仍受服务的可设置参数清单约束。
- `outputSchema` 已包含每次响应都会带的 `status` 状态块，并把它列为必填。

### 3.3 `tools/call` 的结果形状

普通成功结果：

```json
{
  "resultType": "complete",
  "content": [
    {"type": "text", "text": "{\"task\":{…},\"status\":{…}}"},
    {"type": "text", "text": "状态：3D 步数模式「Agent 自己管理」；本资产已用 2 步；经 MCP 累计 5 步；余额：…"}
  ],
  "isError": false,
  "structuredContent": {
    "task": {…},
    "status": {…}
  }
}
```

- `structuredContent` 是机器可读结果；`content` 的第一个文本块是它的 JSON 文本（业务错误时换成错误文案），第二个文本块是一行状态摘要。
- **业务错误也是成功的调用**：HTTP `200`，`isError: true`，`structuredContent.error = {code, message}`（部分错误带 `retry_after_seconds`），并且同样带状态块。原因写给调用的 Agent 看，可直接转告用户。
- 调用方无法处理的内部错误才是协议错误：HTTP `500`、JSON-RPC `error` 码 `-32603`。
- 客户端声明了 Tasks 扩展且调用的是会创建 Task 的工具时，结果换成 `CreateTaskResult`（`resultType: "task"`），不含 `content`/`structuredContent`/状态块，见 §5。

监听层拒绝（例如鉴权、限流、未知路径）也是 JSON-RPC 错误体，但 `id` 通常为 `null`；错误 `data.detail` 是按请求语言写的文案，`data.request_id` 在服务端记录了访问日志时给出。

### 3.4 语言

服务端文案（工具说明、`server/discover` 的 `instructions`、业务错误、状态摘要、`state_label`、`statusMessage`）跟随请求的界面语言：默认 `zh-CN`，请求带 `Accept-Language: en` 时用英文。JSON Schema 里的参数 `description` 不翻译。

图片检查的 `actual` / `expected` / `fix` 目前固定为英文，`hint` / `note` 取平台 Catalog 的界面语言文案；英文客户端可能看到混排。这是现状，不影响字段语义。

## 4. 工具

工具名、说明、参数与输出都由 Product Schema 的 `application.mcp_public.tools` 声明；`list_services` 与 `tools/list` 是运行时真源，部署可能按自己的 Catalog 调整工具清单与说明。默认 Catalog 提供以下工具：

| 工具 | 范围 | 用途 |
|---|---|---|
| `list_services` | `read` | 列出可用的服务（供给项）、是否可用、每步费用、输入与可调参数 |
| `get_status` | `read` | 查看步数模式、各 MCP 资产已用步数、累计步数与 3D 供应商余额 |
| `get_task` | `read` | 查询任务状态、进度、失败归因与建议动作 |
| `list_tasks` | `read` | 按资产或最近任务分页列出任务 |
| `get_task_assets` | `read` | 列出任务产物与下载地址 |
| `inspect_model` | `read` | 只读检查已成功 3D 任务的 GLB 模型 |
| `inspect_image` | `read` | 只读检查上传图或任务图片 |
| `upload_image` | `generate` | 以 base64 上传参考图 |
| `generate_image` | `generate` | 文生图 / 图生图（可带蒙版） |
| `remove_background` | `generate` | 去背景 |
| `create_model` | `generate` | 文生 3D / 图生 3D / 多视图生 3D |
| `process_model` | `generate` | 换贴图、重拓扑、绑骨、动画、格式转换、拆分 |

### 4.1 只读工具

#### `list_services`

参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| `capability` | string，可选 | 只看某个能力，如 `image-to-3d`；未知能力按 `invalid_arguments` 拒绝 |
| `available_only` | boolean，可选 | 为 `true` 时只列当前可用的服务 |

结果 `groups[]` 按服务分组（图片、3D 模型、模型加工），每个能力下列出服务：`id`、`provider`、`model`、`available`、`recommended`、`step_cost`（原生单位与名称）、`inputs[]`、`parameters[]`。`inputs` 里含 `mask` 的服务支持图生图蒙版；`parameters` 只列调用方能设置的参数，标了 `advised` 的参数另给 `advice`。

服务不可用时 `available: false` 会明说，调用时明确拒绝，从不替换成别的模型。

最小 `arguments`：`{}`（或 `{"capability": "image-to-3d", "available_only": true}`）。

#### `get_status`

无参数。结果：

- `assets[]`：本人的 MCP 资产（每个资产是一个对话会话），最近活动的在前；每项 `asset_id`、`name`（默认资产为 `null`）、`title`、`used_steps`、`last_activity_at`。
- `assets_truncated`：为 `true` 时还有更早的资产未列出（默认最多 20 个）。
- 另有每次响应都带的状态块。

读取不计步、不扣费；资产列表读取失败时按空列表返回，不让调用失败。

最小 `arguments`：`{}`。

#### `get_task`

参数 `task_id`（必填）。结果 `task`：

| 字段 | 说明 |
|---|---|
| `id` | 任务 ID |
| `asset_id` | 任务所属的 MCP 资产；任务不在 MCP 资产里（例如在网页里发起）为 `null` |
| `capability` / `offering_id` / `provider` / `model` | 能力、服务与模型 |
| `state` / `state_label` / `terminal` | 公开状态、状态名称、是否终态 |
| `outcome` | `pending`、`running`、`success`、`failure`、`canceled` |
| `progress` | 0–100；状态表不展示进度的状态为 `null` |
| `created_at` / `updated_at` / `finished_at` | RFC 3339 时间 |
| `asset_count` | 已经保存了几个产物文件 |
| `failure` | 失败归因：`category`、`next_action`、`retryable`、`user_decision_required`、`advice`、`detail`；成功为 `null` |
| `poll_after_seconds` | 未结束时建议的轮询间隔（默认 5 秒）；已结束为 `null` |

只能查自己的任务；别人的任务与不存在的任务都给同样的业务错误 `task_not_found`。任务成功时，状态块还带平台自检结论（见 §6.3）。

最小 `arguments`：`{"task_id": "<TASK_ID>"}`。

#### `list_tasks`

参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| `asset_id` | string，可选 | 只看这个资产（必须是本人的 MCP 资产，否则 `asset_not_found`） |
| `limit` | integer，可选 | 每页数量，默认 20，最大 100 |
| `cursor` | string，可选 | 上一页的 `next_cursor` |

结果 `tasks[]`（形状同 `get_task.task`）与 `next_cursor`（没有下一页为 `null`）。不带 `asset_id` 时按本人最近任务列出；网页里发起的任务也在其中，只是 `asset_id` 为 `null`。游标无效是调用方的错误（`invalid_arguments`）。

最小 `arguments`：`{}`（或 `{"asset_id": "<ASSET_ID>", "limit": 20}`）。

#### `get_task_assets`

参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| `task_id` | string，必填 | 任务 ID |
| `signed` | boolean，可选 | 为 `true` 时返回短时签名下载地址；缺省 `false` |

结果：

- `task_id`、`state`、`complete`（任务已成功结束且产物齐全）。
- `download_expires_at`：签名地址的过期时间（RFC 3339）；未签名时为 `null`。
- `assets[]`：`id`、`role`（如 `model`、`thumbnail`、`texture`）、`variant`、`format`、`media_type`、`size`、`sha256`、`required`、`delivery`、`download_url`。

任务没成功时 `complete: false`，产物可能为空。下载地址是同一监听上的绝对地址（按请求的 Host 与协议拼成）：

- `signed` 缺省或 `false`：鉴权下载地址，取字节时必须带同一把 Key 的 `Authorization`。
- `signed: true`：短时签名地址，地址本身即可取回字节，见 §7。

最小 `arguments`：`{"task_id": "<TASK_ID>"}`（签名下载加 `"signed": true`）。

#### `inspect_model`

参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| `task_id` | string，必填 | 本人的任务 |
| `asset_id` | string，可选 | 任务里的模型产物 ID（`role=model`）；缺省用任务的主模型文件 |
| `purpose` | string，可选 | 临时按 `chat.model3d_usage` 里的一个 3D 用途判定；只影响本次，不写入会话 |

结果：

- `task_id`、`asset_id`。
- `report`：模型的确定性事实：三角面、顶点、部件与网格、材质与贴图、UV、骨骼、动画、包围盒、拓扑与水密、警告等。
- `usage_check`：按 `purpose` 参数或任务所属会话的制作简报判定的逐条检查：`purpose`、`purpose_label`、`purpose_source`（`production_brief` / `default_profile` / `argument`）、`platform`、`note`、`checks[]`（`check`、`status` 为 `pass`/`fail`/`unknown`、`actual`、`budget`、`fix_hint`）。没有用途 profile 时为 `null`。
- `parts_total` 与 `parts_truncated`：部件列表按服务端上限截断。

限制与错误：

- 只支持 GLB（`model/gltf-binary`）；其他格式明确拒绝（`model_not_glb`）。
- 任务必须成功并保存了模型文件（`model_not_ready`）；文件过大（`model_too_large`）、读不出或解析失败（`model_unreadable`）、服务端没有启用检查器（`model_unavailable`）。
- 任务不属于调用者按 `task_not_found`；`asset_id` 不在该任务里按 `asset_not_found`，都不泄露存在性。
- 未知 `purpose` 是 `invalid_arguments`，Message 列出可用用途 ID。

它只读：不写自检记录、不占平台自检轮次、不计步、不扣费、不创建任务或授权。`fix_hint` 只点名 MCP 的 `process_model` 能执行且本次运行有可用服务的修复能力（如重拓扑、绑骨、格式转换）；UV 展开、尺寸调整、打印修复、模型合并等 MCP 执行不了的项只说明「没有可用能力」。

最小 `arguments`：`{"task_id": "<TASK_ID>"}`（可加 `"asset_id"`、`"purpose"`）。

#### `inspect_image`

参数（图片来源二选一）：

| 参数 | 类型 | 说明 |
|---|---|---|
| `image` | string，可选 | `upload_image` 返回的输入资产 ID，或本人成功任务的已保存图片产物 ID |
| `task_id` | string，可选 | 任务 ID；与 `image` 二选一 |
| `asset_id` | string，可选 | 任务产物 ID（`role` 必须是 `image`）；只能与 `task_id` 同用，缺省用任务的第一份已保存图片产物 |
| `checklist` | string，可选 | `delivery`（缺省，交付清单）或 `3d_input`（提交图生 3D 前的建议预检） |
| `purpose` | string，可选 | 临时按 `chat.image2d_usage` 里的一个 2D 用途判定；不能与 `checklist=3d_input` 同用 |

结果：

- `image`：来源（`input_asset` / `task_asset`）、`input_asset_id` / `task_id` / `asset_id`、`media_type`、`size`。
- `report`：宽高、短边、透明通道与透明占比、背景扩散、主体占比与包围盒、是否贴边、连通域；贴图/图标用途可能出现 `tiling` / `edge_fringe`。
- `usage_check`：`checklist`、`checks[]`（`check`、`status`、`actual`、`expected`、`fix`、`hint`）、`purpose` 与来源信息（没有用途时为 `null`）。
- `visual_items`：仅 `3d_input` 给出，列出需要亲自看图的项（`id`、`label`、`instruction`、`fix_hint`），不做主观判定。

限制与错误：

- 只支持 PNG / JPEG / WebP；GIF 上传允许，但检查返回 `image_unreadable`，与损坏图片一样。
- 默认像素上限 20,000,000，并受平台图片检查上限约束；超过返回 `image_too_large`（Message 带字节与像素上限）。
- 同一时刻最多解码 2 张图，槽位占满时不排队，返回 `image_busy` 与 `retry_after_seconds`（当前 2 秒）。
- `task_not_found`、`asset_not_found`（产物不是可检查的图片时用 `asset_not_image` 文案）、`image_not_found`、`image_not_ready`、`image_unreadable`、`image_unavailable`；不泄露存在性。

它只读：不写自检记录、不计步、不扣费、不创建任务；平台内工具的区域裁剪、轮次计数与视觉结论不在这里执行，`3d_input` 也不执行平台的提交闸门。

最小 `arguments`：`{"image": "<IMAGE_ID>"}`（或 `{"task_id": "<TASK_ID>"}`）。

### 4.2 上传：`upload_image`

以 base64 上传一张参考图，返回图片 ID，之后可作为图片输入。它需要 `generate` 范围。

```json
{
  "name": "upload_image",
  "arguments": {
    "data": "<BASE64>",
    "filename": "reference.png"
  }
}
```

| 参数 | 说明 |
|---|---|
| `data` | 必填，图片内容的 base64 文本（标准编码，不带 `data:` 前缀） |
| `filename` | 可选，原始文件名，只用于界面显示；超过 120 个字符或路径不合法时会被丢弃 |

默认 Catalog 的上限：解码后 700,000 字节（base64 文本上限 933,336 字符，且整个请求不超过 1 MiB）；类型按字节判断，必须是 PNG / JPEG / WebP / GIF 且在部署允许的上传类型里，调用方声明的类型不参与。

结果：

```json
{"image": {"id": "<IMAGE_ID>", "media_type": "image/png", "size": 123456, "sha256": "…"}}
```

上传不计步、不扣费，也不属于某个资产。错误码：`upload_too_large`、`upload_invalid`、`upload_type_not_allowed`。

### 4.3 生成与加工工具

四个工具共用一条调用流程：冻结输入 → 签发并当场判定 `access_key` 授权 → 内核创建任务 → **立即返回任务 ID**（不等任务完成）。结果统一为：

```json
{
  "task_id": "<TASK_ID>",
  "state": "PENDING",
  "state_label": "等待中",
  "terminal": false,
  "replayed": false,
  "asset": {"id": "<ASSET_ID>", "name": "木桶", "title": "MCP · 木桶"},
  "service": {"id": "<SERVICE_ID>", "provider": "meshy", "model": "…", "capability": "image-to-3d", "defaulted": true},
  "step_counted": true,
  "poll_after_seconds": 5,
  "reference_note": null
}
```

#### 4.3.1 公共参数

| 参数 | 工具 | 说明 |
|---|---|---|
| `idempotency_key` | 四个工具都必填 | 8–128 个字符，建议用 UUID；同一个键 + 完全相同的参数重试会拿到同一个任务 |
| `asset` | 四个工具可选 | 资产名（如「木桶」）；同一个名字总是对应同一个会话 |
| `project` | 四个工具可选 | 本人的项目 ID；只在新建资产时使用，已有资产不能换项目 |
| `service` | 四个工具可选 | 服务 ID，取自 `list_services`；不传则用该能力的默认服务，结果 `service.defaulted` 为 `true` |
| `parameters` | 四个工具可选 | 服务的可调参数对象 `{参数名: 值}`；只能是该服务允许调用方设置的参数 |
| `parameter_reasons` | 四个工具可选 | 改动 `list_services` 里标了 `advised` 的参数时，写 `{参数名: 理由}`；不改时不用传 |

规则：

- **服务选择不回落。** 指定的服务不存在、未启用或能力不对是 `service_mismatch`；当前不可用是 `service_unavailable`；没有可用服务是 `no_service`。都不会自动换成别的模型。
- **参数由内核统一校验。** 固定的、隐藏的、只读的、图片与来源类的、提示词类的参数按名字拒绝；值的类型、范围与组合由内核归一化冻结输入时检查，拒绝是 `input_refused`。
- **额外积分同意由服务端冻结。** 声明了「额外积分同意」参数的服务（隐藏参数，调用方设不了），服务端把同意冻结进输入并按步数模式判定授权；调用方不需要、也不能自己传这个参数。
- **`reference_views`。** 多视图生 3D 时，视图标注放在 `parameters.reference_views`，具体取值与服务是否接受见 `list_services`。

#### 4.3.2 `generate_image`

```json
{
  "name": "generate_image",
  "arguments": {
    "prompt": "一只木桶，游戏道具风格",
    "images": ["<IMAGE_ID>"],
    "asset": "木桶",
    "idempotency_key": "<UUID>"
  }
}
```

| 参数 | 说明 |
|---|---|
| `prompt` | 必填，提示词（最长 8000 字符） |
| `images` | 可选，参考图 ID 数组（1–16 个、不重复）；传了就是图生图，不传是文生图 |
| `mask` | 可选，蒙版图片 ID；仅图生图使用，见 §4.3.6 |

图生图的 `images` 可以是 `upload_image` 返回的图片 ID，也可以是本人成功任务的图片产物 ID。2D 步骤不设 3D 步数限制，`step_counted` 为 `false`。

蒙版相关错误：`mask_invalid`、`mask_size_mismatch`、`mask_format_mismatch`、`mask_not_supported`、`image_not_found`；校验在准备、授权与建任务之前完成，失败不建任务、不计费。

#### 4.3.3 `remove_background`

```json
{
  "name": "remove_background",
  "arguments": {
    "image": "<IMAGE_ID>",
    "asset": "木桶",
    "idempotency_key": "<UUID>"
  }
}
```

`image` 必填，是图片 ID（上传的输入资产或本人成功任务的图片产物）；其余公共参数同上。2D 工具，`step_counted` 为 `false`。

#### 4.3.4 `create_model`

```json
{
  "name": "create_model",
  "arguments": {
    "images": ["<IMAGE_ID>"],
    "asset": "木桶",
    "idempotency_key": "<UUID>"
  }
}
```

| 参数 | 说明 |
|---|---|
| `prompt` | 条件必填：只有提示词是文生 3D；图生 3D 时只有服务接受提示词才能传 |
| `images` | 可选，1–16 张参考图：一张是图生 3D，多张是多视图生 3D |

`prompt` 与 `images` 至少给一个。不传 `service` 时，多视图会选一个支持多视图的服务。计 1 步 3D 步数（能力属于 `step_policy.counted_capabilities`），`step_counted` 为 `true`；一步不论交付几个文件都只算一步。

#### 4.3.5 `process_model`

```json
{
  "name": "process_model",
  "arguments": {
    "operation": "remesh",
    "source_task_id": "<TASK_ID>",
    "asset": "木桶",
    "idempotency_key": "<UUID>"
  }
}
```

| 参数 | 说明 |
|---|---|
| `operation` | 必填：`retexture`、`remesh`、`rigging`、`animation`、`format-conversion`、`model-split` |
| `source_task_id` | 必填：同一个资产里已成功、保存了模型文件的 3D 任务（分支继承的任务也算） |
| `source_asset_id` | 可选：来源任务里的某个模型文件 ID（`role=model`）；缺省用主模型文件 |
| `prompt` / `images` | 可选，只有服务接受时才传；`images` 最多 1 张，例如换贴图的风格参考 |

来源任务必须与本次调用在同一个资产（同一个 `asset` 名）里；不传 `service` 时，服务端从接受这个来源的可用服务里选默认的。计 1 步 3D 步数，**格式转换除外**（`format-conversion` 不计步）；拆分（`model-split`）计步。

#### 4.3.6 蒙版 `mask`

`generate_image` 的图生图支持 PNG 蒙版：

- 必须与 `images` **同传**；`mask` 是图片 ID，来源同 `images`。
- 必须是 PNG；字节数不超过该服务 `mask` 参数声明的 `max_bytes`。
- 与 `images` 的**第一张图**同尺寸。
- 与 `images` 的**第一张图**同格式（#582，图片编辑接口的规则“要编辑的图与蒙版必须同格式同尺寸”）：蒙版只能是 PNG，所以第一张图也必须是 PNG；两者的格式都按文件内容判断，不看文件名或声明的类型。不一致时返回 `mask_format_mismatch`，文案写明两者的格式，并请先把第一张图转成同尺寸的 PNG 重新上传；服务端不静默转码，也不发到上游才失败。
- 至少有一块透明区域；透明区域就是要重绘的区域。
- 只有 `list_services` 里 `inputs` 含 `mask` 的服务支持。不传 `service` 时，服务端会选一个支持蒙版的图生图服务；指定的服务不支持时返回 `mask_not_supported`。
- 校验在准备、授权与建任务之前完成；失败不建任务、不计费。

#### 4.3.7 资产（`asset`）

一个资产就是服务端为一个名字建立的、属于本人的一个对话会话：

- 名字规范化：折叠空白、转小写；不能为空、最长 60 个字符、不能含控制字符。所以 `"Barrel"` 与 `" barrel "` 是同一个资产。
- 标题默认是 `MCP · <名字>`；不传 `asset` 是默认资产，标题 `MCP · 默认`。带 `project` 时，每个项目各有一个默认资产。
- 会话照常出现在本人的会话列表里，人可以在网页界面打开它，看到外部 Agent 做的每一步。
- `project` 把新建的资产建在该项目下，继承项目的风格与约定；已有资产不能换项目（`asset_project_mismatch`）；未知或别人的项目是 `project_not_found`。
- 加工工具必须使用与来源任务相同的资产名；资产从不搬家。

#### 4.3.8 幂等键

- 付费类工具必须带 `idempotency_key`（8–128 个字符）；任务键与授权键是 Schema 前缀 `mcp:` 加调用方的键，所以不会与浏览器、平台内 Agent 的键相撞。
- 同一个键、同样的调用：任务已存在就原样返回（`replayed: true`，不新建、不再计步、不再扣费）；授权还在待决或已批准就继续判定与提交；被拒绝过的会重新判定一次。
- 同一个键配不同的参数、别的工具、别的服务，或这个键已被别的入口用过，是 `idempotency_conflict`，且不建资产、不建任务。
- 两个同键同内容的调用同时到达，结果仍是同一个任务、一步。
- **每个新请求用新的键。** 只有重试同一个请求时才复用。

#### 4.3.9 3D 步数

3D 步数模式按持有人设置，默认「Agent 自己管理」（平台不设上限）：

| 模式 | 行为 |
|---|---|
| `agent` | 平台不设上限，外部 Agent 自己判断是否继续 |
| `unlimited` | 不限步数 |
| `cap` | 每个资产（会话）最多 N 步；达到后下一个 3D 步骤被拒绝（`step_cap_reached`），不建任务 |

- 只有 `step_policy.counted_capabilities` 里的能力计步；默认 Catalog 包含文生 3D、图生 3D、Gaussian Splat、动作生成、换贴图、重拓扑、绑骨、动画、网格分割、网格补全与拆分，格式转换不计。以实际部署的 Catalog 为准。
- 一个公开任务算一步，不论交付几个产物；2D 与 LLM 调用从不计数。
- 确定没有扣费的失败或取消的任务不计；可能已扣费的任务不释放。
- `step_cap_reached` 的 Message 写明已用与上限，可直接转告用户；用户要在浏览器里调高上限后才能继续。
- 状态块里的 `step_mode`、`step_cap`、`asset.steps_left` 与 `total_used_steps` 是判断依据（见 §6）。

#### 4.3.10 业务拒绝

生成类工具的业务拒绝都是 `isError: true` 的工具结果，带文案与状态块，没有任务：

| 错误码 | 含义 |
|---|---|
| `step_cap_reached` | 资产的 3D 步数已用完，这一步没有执行 |
| `needs_person` | 需要真人在网页界面处理（同一资产里更早的同类生成已被上游完成并可能扣费、结果没能保存）；Message 带那次的任务 ID |
| `service_unavailable` / `service_mismatch` / `no_service` | 服务不可用 / 不匹配 / 没有可用服务；不会换模型 |
| `input_refused` | 服务拒绝了这组输入，按 `list_services` 修改后再试 |
| `invalid_arguments` | 参数名、提示词、图片、`parameter_reasons` 等问题，写明 `$.路径`；路径后的原因取 Schema `mcp_public.messages.invalid_details`，随请求语言（#517） |
| `image_not_found` | 图片 ID 不是本人的 |
| `source_not_found` / `source_not_ready` / `source_incompatible` | 来源任务不存在 / 未成功或无模型文件 / 服务不接受这个来源 |
| `asset_name_invalid` / `asset_project_mismatch` / `project_not_found` | 资产名、项目归属问题 |
| `idempotency_conflict` / `in_progress` | 幂等键冲突 / 同键请求还在处理中 |
| `too_many_in_flight` | 同时进行中的任务已达上限；错误对象带 `retry_after_seconds` |

`too_many_in_flight` 时**用同一个 `idempotency_key` 重试**：这一步已获批准，重试会接着提交；不要换新键，否则会重新排队并占住另一步。放弃重试的话，这一步会作为「已批准、还没有任务」的步数一直占着，直到同一个键被重试。内核在写任务之前就确定、且不建议重试的提交（输入无效、服务未知等）会释放已批准的授权，不占步数。

## 5. Tasks 扩展

服务端在 `server/discover` 的 `capabilities.extensions` 里声明 `io.modelcontextprotocol/tasks`（部署默认启用；是否提供 `tasks/get`、`tasks/cancel` 由 Catalog 的 `mcp_public.tasks.methods` 决定）。

### 5.1 客户端声明

扩展按**每个请求**协商：客户端在 `params._meta` 的客户端能力里列出它。这个声明决定发起工作的工具是否返回 `CreateTaskResult`；服务端提供的 `tasks/get` / `tasks/cancel` 按 Catalog 的方法清单提供，不因某个请求没有声明而消失。

```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "method": "tools/call",
  "params": {
    "name": "create_model",
    "arguments": {"images": ["<IMAGE_ID>"], "asset": "木桶", "idempotency_key": "<UUID>"},
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0"},
      "io.modelcontextprotocol/clientCapabilities": {
        "extensions": {"io.modelcontextprotocol/tasks": {}}
      }
    }
  }
}
```

没有声明的客户端调用发起工作的工具时只会收到普通工具结果（`resultType: "complete"`）。

### 5.2 `CreateTaskResult`

声明了扩展的客户端调用 `generate_image`、`remove_background`、`create_model`、`process_model` 时，服务端用 `CreateTaskResult` 代替普通结果：

```json
{
  "resultType": "task",
  "taskId": "<TASK_ID>",
  "status": "working",
  "statusMessage": "等待中",
  "createdAt": "2026-11-19T08:00:00Z",
  "lastUpdatedAt": "2026-11-19T08:00:00Z",
  "ttlMs": 86400000,
  "pollIntervalMs": 5000
}
```

- `ttlMs` 是客户端可以继续用这个 Task ID 轮询的时长（默认 24 小时，来自 `mcp_public.tasks.result_ttl_seconds`）；任务本身仍按平台保留策略处理。
- `pollIntervalMs` 只在非终态时给出（默认 5000，来自 `mcp_public.poll_interval_seconds`）。
- `statusMessage` 是当前公开状态的名称。
- **`CreateTaskResult` 不包含状态块**。要拿状态块，继续用 `tasks/get`，或对同一个任务调 `get_task`。

### 5.3 `tasks/get`

参数 `taskId`（必填）。它需要 `read` 范围；查询别人的任务与不存在的任务一样，返回 `404` 与 `-32004`。

结果在 Task 字段之外：

- 非终态：`resultType: "complete"`，没有 `result`/`error`。
- `completed`：`result` 是 `get_task` 的工具结果（含 `content`、`structuredContent.task`、`structuredContent.status`、`isError`）。
- `failed`：`error = {code: -32000, message: <建议动作>, data: {task: <任务视图>, status: <状态块>, failure?: <归因>}}`。
- `cancelled`：`status: "cancelled"`，没有 `result`/`error`。

### 5.4 `tasks/cancel`

参数 `taskId`（必填）。它需要 `generate` 范围，因为取消会改变可能计费的工作；只有 `read` 的 Key 返回 `403` 与 `-32003`。

取消是协作式的：

- 成功请求取消（任务已变为 `cancelled`）：结果 `{"resultType": "complete"}`，细节在 `_meta` 的扩展键下：`{taskId, status: "cancelled", alreadyTerminal: false, statusMessage}`。
- 任务还在取消中（尚未进入终态）：`_meta` 里 `status` 可能是 `working`；继续用 `tasks/get` 轮询。
- 任务已经是终态：返回 `alreadyTerminal: true` 与当前 `status`，**不改变任何状态**；`statusMessage` 说明「这次取消没有改变任何状态」。
- 别人的任务与不存在的任务：`404` 与 `-32004`。

### 5.5 状态映射

| 平台任务状态（内部状态名；对外 `state` 会投影为 `PENDING`、`IN_PROGRESS` 等） | Tasks `status` |
|---|---|
| `PENDING`、`QUEUED`、`RUNNING`、`IN_PROGRESS`、`PROCESSING`、`MATERIALIZING`、`UPSTREAM_COMPLETED`、`RETRY_PENDING` | `working` |
| `SUCCEEDED` | `completed` |
| `FAILED`、`EXPIRED` | `failed` |
| `CANCELED` | `cancelled` |

扩展 vocabulary 里的 `input_required` 不使用：这个服务从不向调用方追问输入。

### 5.6 未启用时

- `server/discover` 不声明扩展。
- `tasks/get`、`tasks/cancel` 返回 `404` 与 `-32601`（方法不存在）。
- 即使客户端声明了扩展，发起工作的工具仍返回普通结果。

## 6. 状态块

### 6.1 位置与形状

每个 `tools/call` 的响应——包括 `isError: true` 的业务错误——在 `structuredContent` 的固定键 `status` 下带一个状态块，并在文本内容末尾追加一行状态摘要。它只在普通工具结果里出现；`CreateTaskResult` 不带，`tasks/get` 的 `result` 或 `error.data.status` 里带。

示例：

```json
{
  "step_mode": "agent",
  "step_mode_label": "Agent 自己管理",
  "step_cap": null,
  "asset": {
    "id": "<ASSET_ID>",
    "title": "MCP · 木桶",
    "used_steps": 2,
    "steps_left": null
  },
  "total_used_steps": 5,
  "balances": [
    {"provider": "meshy", "label": "Meshy", "state": "ok", "amount": "1200", "unit": "meshy.credit", "unit_label": "Meshy credits", "read_at": "2026-11-19T08:00:00Z"}
  ],
  "step_costs": [
    {"offering_id": "<SERVICE_ID>", "provider": "meshy", "capability": "image-to-3d", "amount": "20", "unit": "meshy.credit", "unit_label": "Meshy credits"}
  ],
  "failure": null,
  "inspection": null
}
```

字段：

| 字段 | 说明 |
|---|---|
| `step_mode` / `step_mode_label` | `agent`（Agent 自己管理，平台不设上限）、`unlimited`（不限）、`cap`（每个资产有硬上限） |
| `step_cap` | 硬上限模式下每个资产最多能用的 3D 步数；其他模式为 `null` |
| `asset` | 本次调用涉及的资产；没有涉及资产时为 `null`。`used_steps` 读取失败为 `null`，不是零；`steps_left` 只在 `cap` 模式给出 |
| `total_used_steps` | Key 持有人经 MCP 累计的 3D 步数（所有资产合计）；读取失败为 `null` |
| `balances[]` | 部署声明的 3D 供应商余额（原生单位）；读不到时 `state: "unknown"`、`amount: null`，**不代表零**，也不让本次调用失败 |
| `step_costs[]` | 本资产用过的服务每步大约消耗多少（现行 Catalog 的原生报价）；没有原生计价的服务不列出 |
| `failure` | 本次调用涉及的任务失败时的归因：`task_id`、`category`、`next_action`、`retryable`、`user_decision_required`、`advice`；否则 `null` |
| `inspection` | 平台对这条已完成 3D 任务的最后一次自检结论；无记录为 `null`，见 §6.3 |

失败归因的五类 `category`：`input_invalid`、`parameter_invalid`、`upstream_capacity`、`upstream_refused`、`internal`。建议动作 `next_action`：`retry`、`retry_later`、`fix_input`、`fix_parameters`、`ask_user`、`contact_admin`。`user_decision_required: true` 时可能已经扣费，必须由用户决定，**不要自动重试**；此时 `retryable` 恒为 `false`、`next_action` 恒为 `ask_user`。归因与平台内 Agent 的任务视图同源（`generationtool.TaskFailureReport`，#517）：上游可能已运行并计费的歧义失败，以及按积分计费的服务在 Schema 声明了「不自动重试」文案的失败类别（如完整性校验失败），都按此处理。`get_task`、`get_task_assets`、`list_tasks` 读取的任务摘要投影也带冻结服务的计费标记（迁移 0113），与生成类工具的结果一致。

读取状态块不计步、不扣费、不建授权或任务。

### 6.2 余额的「未知」

余额读取失败或超时时，该账户状态为 `unknown`；数值为 `null`。部署没有声明的账户不出现在 `balances` 列表里。不要把未知当零，也不要把余额当作平台额度：它只是供应商账户的参考值。

### 6.3 平台自检结论 `inspection`

只在 `get_task` 与 `get_task_assets` 且任务 `SUCCEEDED` 时读取；其他工具、其他状态给出 `null`。字段：

```json
{
  "task_id": "<TASK_ID>",
  "source": "platform_self_check",
  "verdict": "passed",
  "purpose": {"id": "game_character", "label": "游戏角色", "source": "production_brief"},
  "failed": [],
  "waiver": false,
  "checked_at": "2026-11-19T07:59:00Z"
}
```

- `verdict`：`passed`、`issues`、`unchecked`、`uncheckable`。
- `failed`：最近一次成功检查判为失败的用途检查 ID 列表；没有失败项时是空数组，不是 `null`。
- `waiver`：为 `true` 表示带豁免通过，不能读成干净通过。
- `checked_at`：结论时间；没有结论时为最近一次成功检查的时间；都没有为 `null`。

它是**平台最后一次自检结论，不是交付许可**：`passed` 可能带 `waiver`，与任务失败归因是两件事。2D 任务、纯 MCP 任务与平台未审阅过的 3D 任务都是 `null`；MCP 自己的 `inspect_model` / `inspect_image` 不写自检记录，所以不会产生这里的内容。

## 7. 签名下载

`get_task_assets` 传 `signed: true` 时，服务端为每个产物签发短时签名地址：

- 地址形如 `GET /mcp/signed-assets/{token}`，是同一监听上的绝对地址（按请求的 Host 与协议拼成）。
- 有效期内地址本身即可取回字节，**不需要** `Authorization`；结果根级 `download_expires_at` 给出过期时间（RFC 3339）。
- 默认有效期 300 秒，最长 3600 秒（Catalog 的 `signed_download_ttl_seconds` / `signed_download_max_ttl_seconds`）。
- 签名地址不查 Key、不做范围检查，**不计入** Key 的每分钟请求数，也不参与鉴权失败节流。
- 令牌无效、过期、被篡改或所有者不符一律 `404`，不区分原因；服务端不保存令牌明文，访问日志把路径参数脱敏为 `{redacted}`。

安全边界：

- 签名地址是**bearer 能力**：拿到 URL 的任何人在过期前都能下载。不要把它写进日志、聊天记录或公开页面，也不要转发给不该拿到产物的人。
- 监听没有 TLS；签名地址同样只能在内网或本机使用，不得经公网明文传输。

服务端没有配置签名密钥时，`signed: true` 返回 `isError` 的业务错误与错误码 `signed_download_unavailable`，不签发链接；未签名的鉴权下载不受影响。鉴权下载地址要求同一把 Key 且有 `read` 范围，别人的任务 `404`。

## 8. 限制与错误

### 8.1 限流与鉴权失败节流

默认 Catalog 的 `application.access_keys.rate_limit`：

| 计数 | 默认 | 对象 |
|---|---|---|
| `requests_per_minute` | 120 | 每把 Key；JSON-RPC 端点与鉴权下载共用一个窗口 |
| `auth_failures_per_minute` | 20 | 每个客户端地址的鉴权失败次数 |

处理顺序：

1. 先看客户端地址这一分钟的鉴权失败次数；已达上限直接 `429`，**不再查 Key**（被节流的地址连有效 Key 也得不到回答）。
2. 再鉴权；每一次 `401` 给这个地址记一次失败。成功**不**清零——手里有一把有效 Key 不能让猜测变成免费的。认证服务自己出错（`503`）不计。
3. 认证成功后、进入工具或下载前，按 Key ID 计一次请求；超过上限 `429`，被拒的请求不执行任何工具、不读任何文件。

拒绝是 HTTP `429`，带 `Retry-After`（整秒、向上取整、不为 0，等于当前窗口剩余时间）与 JSON-RPC 错误体：

- Key 超限：码 `-32090`。
- 地址被节流：码 `-32091`。
- `data.detail` 是按请求语言写的文案，`data.retry_after_seconds` 与头一致。

签名下载路由不经过这些计数；`404`、非 `POST` 的 `405` 也不计数。计数只在进程内、只对单实例成立：重启后窗口清零，多实例各算各的。固定窗口跨边界时短时间内最多可到两倍；鉴权失败的上限只对逐个发出的请求成立，同时在途的失败请求最多比上限多出它们的个数。

### 8.2 图片检查并发

同一时刻最多解码 2 张图；槽位占满时不排队，直接返回业务错误 `image_busy` 与 `retry_after_seconds`（当前 2 秒）。

### 8.3 请求与响应大小

默认 Catalog：请求体上限 1 MiB，JSON 响应上限 4 MiB。`upload_image` 的 base64 文本与整个请求都必须放得下；更大的图片要先缩小。

### 8.4 HTTP 状态与 JSON-RPC 错误

| HTTP | JSON-RPC 码 | 场景 |
|---|---|---|
| 200 | — | 调用成功；业务失败也在其中（`isError: true`） |
| 400 | `-32600` | 信封不是合法 JSON、`jsonrpc` 不是 `2.0`、`id` 不合法、`params` 不是对象 |
| 400 | `-32020` | 缺少 `_meta`，或版本头/方法头/`Mcp-Name` 与请求不一致 |
| 400 | `-32022` | 协议版本不受支持；`data.supported`、`data.requested` |
| 400 | `-32602` | `clientInfo`/`clientCapabilities` 缺失、参数不是对象、未知工具、缺少 `taskId` |
| 401 | `-32001` | 没有 Key、Key 形态不对、错误、吊销、过期、持有人不可用 |
| 403 | `-32003` | Key 缺少工具或下载需要的范围；带 `WWW-Authenticate` |
| 404 | `-32601` | 未知路径、未知方法、未启用的 `tasks/*` 方法 |
| 404 | `-32004` | `tasks/get`/`tasks/cancel` 查不到这个 Task（含别人的 Task） |
| 405 | `-32600` | JSON-RPC 端点收到非 `POST`；`Allow: POST` |
| 413 | `-32600` | 请求体超过上限 |
| 429 | `-32090` | Key 每分钟请求数超限；`Retry-After` |
| 429 | `-32091` | 客户端地址鉴权失败次数超限；`Retry-After` |
| 500 | `-32603` | 内部错误 |
| 503 | `-32603` | 身份解析服务暂时不可用 |

### 8.5 业务错误码

业务错误是 HTTP `200` 的 `isError: true` 结果，`structuredContent.error.code` 为字符串：

| 错误码 | 说明 |
|---|---|
| `invalid_arguments` | 参数无效，Message 写明 `$.路径` 或可用值 |
| `task_not_found` / `asset_not_found` / `image_not_found` | 任务、资产或图片不存在或不属于本人 |
| `step_cap_reached` / `needs_person` / `too_many_in_flight` | 3D 步数上限 / 需要真人 / 进行中任务已满 |
| `service_unavailable` / `service_mismatch` / `no_service` | 服务不可用 / 不匹配 / 没有可用服务 |
| `input_refused` / `source_not_found` / `source_not_ready` / `source_incompatible` | 输入被服务拒绝 / 来源任务问题 |
| `asset_name_invalid` / `asset_project_mismatch` / `project_not_found` | 资产名与项目问题 |
| `idempotency_conflict` / `in_progress` | 幂等键冲突 / 同键请求处理中 |
| `upload_too_large` / `upload_invalid` / `upload_type_not_allowed` | 上传问题 |
| `mask_invalid` / `mask_size_mismatch` / `mask_format_mismatch` / `mask_not_supported` | 蒙版问题 |
| `signed_download_unavailable` | 服务端没有配置签名密钥 |
| `model_not_ready` / `model_not_glb` / `model_too_large` / `model_unreadable` / `model_unavailable` | 模型检查问题 |
| `image_not_ready` / `image_unreadable` / `image_too_large` / `image_unavailable` / `image_busy` | 图片检查问题（任务产物存在但不是图片时，code 是 `asset_not_found`，Message 用 `asset_not_image` 文案） |

业务错误 Message 是写给调用 Agent 的，可直接转告用户；不要只根据 Message 做程序判断，程序判断用 `error.code`。

## 9. 端到端示例

以下示例用占位地址与占位 Key；把 `<...>` 换成实际值。直连监听且没有 TLS 终结时，地址通常是 `http://<your-host>:<port>`。

```bash
export MCP_BASE='https://<your-host>'
export MCP_KEY='plk_<id>_<secret>'
```

信封里的 `_meta` 每个请求都要有，下面简写为 `<META>`：

```json
{
  "io.modelcontextprotocol/protocolVersion": "2026-07-28",
  "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0"},
  "io.modelcontextprotocol/clientCapabilities": {}
}
```

### 9.1 发现服务

`discover.json`：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "server/discover",
  "params": {
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0"},
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

```bash
curl -sS "$MCP_BASE/mcp" \
  -H 'Content-Type: application/json' \
  -H 'MCP-Protocol-Version: 2026-07-28' \
  -H 'Mcp-Method: server/discover' \
  -H "Authorization: Bearer $MCP_KEY" \
  --data-binary @discover.json
```

### 9.2 上传参考图

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "upload_image",
    "arguments": {"data": "<BASE64>", "filename": "reference.png"},
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0"},
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

请求头额外加 `Mcp-Name: upload_image`。响应里的 `structuredContent.image.id` 就是后面的 `<IMAGE_ID>`。

### 9.3 生成 3D 模型

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "create_model",
    "arguments": {
      "images": ["<IMAGE_ID>"],
      "asset": "木桶",
      "idempotency_key": "<UUID>"
    },
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0"},
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

请求头加 `Mcp-Name: create_model`。响应里的 `task_id` 是 `<TASK_ID>`；`step_counted` 说明这一步是否计了 3D 步数。

### 9.4 轮询

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "tools/call",
  "params": {
    "name": "get_task",
    "arguments": {"task_id": "<TASK_ID>"},
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0"},
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

请求头加 `Mcp-Name: get_task`。按 `task.poll_after_seconds`（默认 5 秒）轮询，直到 `task.terminal` 为 `true`。

### 9.5 取产物

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "tools/call",
  "params": {
    "name": "get_task_assets",
    "arguments": {"task_id": "<TASK_ID>", "signed": true},
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0"},
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

请求头加 `Mcp-Name: get_task_assets`。每个 `assets[].download_url` 都是短时签名地址：

```bash
curl -sS -o model.glb '<SIGNED_DOWNLOAD_URL>'
```

签名地址不需要 `Authorization`；地址过期后重新调一次 `get_task_assets`。

### 9.6 检查模型

```json
{
  "jsonrpc": "2.0",
  "id": 6,
  "method": "tools/call",
  "params": {
    "name": "inspect_model",
    "arguments": {"task_id": "<TASK_ID>"},
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0"},
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

请求头加 `Mcp-Name: inspect_model`。按 `usage_check.checks[].status` 判断；`fail` 项按 `fix_hint` 处理，`unknown` 不等于通过。检查本身不计步、不扣费。

### 9.7 Tasks 扩展写法

在 `_meta` 的客户端能力里加：

```json
{
  "io.modelcontextprotocol/clientCapabilities": {
    "extensions": {"io.modelcontextprotocol/tasks": {}}
  }
}
```

`create_model` 的响应变成 `CreateTaskResult`（`resultType: "task"`、`taskId`、`status`、`ttlMs`、`pollIntervalMs`）。之后用 `tasks/get` 轮询：

```json
{
  "jsonrpc": "2.0",
  "id": 7,
  "method": "tasks/get",
  "params": {
    "taskId": "<TASK_ID>",
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {"name": "my-agent", "version": "1.0"},
      "io.modelcontextprotocol/clientCapabilities": {
        "extensions": {"io.modelcontextprotocol/tasks": {}}
      }
    }
  }
}
```

请求头加 `Mcp-Method: tasks/get`。`status` 为 `working` 时按 `pollIntervalMs` 再查；`completed` 时 `result` 是 `get_task` 的结果（含状态块）；`failed` 时 `error` 给出归因与建议动作；`cancelled` 时没有结果。

`tasks/cancel` 同理，只把方法换成 `tasks/cancel`；它需要 `generate` 范围。

## 10. 平台内端点与外部监听的区别

| 项目 | 平台内 MCP（`POST /api/v1/mcp`） | 外部 MCP 监听 |
|---|---|---|
| 身份 | 浏览器 Principal / 平台内 Agent | 只认个人访问 Key |
| 路由 | 挂主 Web 服务 | 独立端口，只挂三条路由 |
| 工具错误 | 折成一个协议错误 | `isError: true` 的工具结果，写给调用的 Agent |
| 结构化输出 | 无 | `structuredContent` + 声明的 `outputSchema` |
| 工具范围 | 只读预置 | 只读 + 上传 + 生成/加工 |
| Tasks 扩展 | 无 | 可选 |

外部客户端不要使用平台内端点；主 Web 服务也不接受访问 Key。

## 11. 部署方需要确认

接入方能否连上，取决于部署方是否配置了以下各项（细节见 [部署文档](deployment.md) §6 与 [架构文档](architecture.md) 的「对外 MCP 监听」）：

- Catalog 的 `application.mcp_public.enabled` 为真，且运行设置给了监听地址；缺一个都不监听。
- 监听地址与主 Web 服务不同；没有 TLS，需要对公网开放时由部署方加证书或 TLS 终结。
- `application.access_keys` 已声明，访问 Key 才能签发；限流是否声明、额度多少也由这里决定。
- 签名下载需要运行配置同时具备 `PUBLIC_BASE_URL` 与 `ASSET_SIGNING_KEY`；没有时 `signed: true` 返回 `signed_download_unavailable`。
- 自定义 Catalog 可以调整工具清单、参数、文案、分页、限流与签名有效期；以实际部署的 `tools/list`、`list_services` 与错误文案为准。

## 12. 相关文档

- [架构文档](architecture.md) 的「对外 MCP 监听」：监听、身份、限流、协议、状态块与每步实现状态。
- [部署文档](deployment.md) §6「外部 MCP 监听」：监听地址、Compose 映射、签名下载与升级说明。
- [产品文档](product.md) §4 与 §6：外部 MCP 的产品边界、批准方式与访问 Key 的权限边界。
- [冒烟客户端](../scripts/smoke/mcp_client.py)：只用标准库的 JSON-RPC 信封、请求头与下载校验，可作为最小接入示例。