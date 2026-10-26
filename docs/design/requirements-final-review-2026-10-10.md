# 本轮最终全量审核报告

- 范围：`22d2e1d..2a99fc6`，PR #19–#42 与 8K 贴图修复（#35），共 141 个文件。
- 审核方：需求审核线程，加一个 fable 独立审核代理。
- 结论：**暂不发布**。有 1 项 P1 必须先返工。

## 已运行的检查

- `go build ./... && go test ./...`：全部通过。
- 前端 vitest、tsc、eslint：本机无法运行（依赖不全，私有源不可达）。需由负责线程或 Mac 构建确认。

## P1：发布前必须修复

**P1-1 Schema 删减了枚举和条件声明，违反 Lex 的规则：“选项和枚举什么都别动，只改参数组合”。**
去掉 `fixed` 后，下列内容无法恢复全参数选择。最小修复：按 22d2e1d 原样恢复，固定值只用 `fixed`、`default`、`hidden` 或 `input_rules` 表达。

1. 即梦 Seedream 文生图、图生图的 `size`：删了 6 个选项（`2K`、`2048x2048`、`2560x1440`、`1440x2560`、`2304x1728`、`1728x2304`），`min_pixels` 从 3686400 改成 14745600。这个参数没有标 `fixed`，用户仍可选，只是枚举被砍。核实：选项计数从 8 降到 2。
2. Tripo 文生 3D、图生 3D 四个 Offering：`texture`、`pbr`、`texture_quality`、`geometry_quality`、`quad`、`smart_low_poly`、`enable_image_autofix` 的 `presentation.visible_when` 被整段删除。核实：全文件 `visible_when` 从 40 处降到 14 处。
3. OpenAI GPT 文生图、图生图：`output_format.forced_when`（透明背景时用 png，否则用 webp）、`output_compression.disabled_when` 和 `step: 1` 被删。Meshy、Tripo 的 `target_polycount`、`face_limit` 的 `step` 也被删。

来源：commit 2451bac（PR #21/#23）。PR #36 和 #42 没有恢复这些。

## P2：可以随后修复

- **P2-1** `internal/providers/meshy/refine.go:42-52`：兼容旧布尔格式时写死了 `mode: refine` 和 `preview_task_id`。这与“适配器不持有 Meshy 参数名”相矛盾，也没有测试。建议删除这个分支，或补测试。
- **P2-2** `scripts/acceptance/studio-scenarios.mjs:555` 要求 GPT Image 显示“生成约 36 积分”。按规则 GPT 不显示积分，这一步必然失败。应改为断言不显示积分。
- **P2-3** 换贴图和发送对虚线框的规则不一致：框里只有未改动的原图时，换贴图用句柄，不做栅格化。应统一按框栅格化，或在注释里写明例外。
- **P2-4** Meshy 处理步骤的来源模型上限从 100 MiB 降到 60 MiB。在 300k 面加 8K PBR 时，Meshy 自己的产物可能超过上限，处理按钮会失效。需在 dev-test 实测。
- **P2-5** 残留无用文案：`ui.studio.picker.image.prefix`、`ui.studio.picker.model.prefix`、`ui.studio.stage.action.process_hint.format-conversion`。

## 已核实合规

1. **积分显示**：只按 `credits_metered` 判断。Meshy（含 Nano Banana）和 Tripo 显示，其他不显示。纯对话不显示。按钮文字是“生成约 N 积分”。
2. **最高质量**：都用 Schema 的 `fixed` + `hidden` + `default` 表达。Meshy 精细化由 `refine_after_preview` 声明。
3. **前端无特判**：`fixedParameterValue` 只读 `fixed`。Agent 工具参数 `resolution`、`enable_pbr`、`convert_format_only` 和 Nano Banana 2 已恢复。删除的文案对应已删的功能。
4. **隔离**：上游文件通过结构检查后原样存入缓存。超出解码上限的图片只做结构检查，单张上限 256 MiB。
5. **架构**：精细化作为私有子作业留在适配器内。架构文档已同步。
6. **UI 需求**：已逐条核对。包括：每页 15 个会话和翻页；刷新后保持当前会话；模型不再重复下载；每个处理步骤一个按钮；@img、@model 可点；发送时虚线框栅格化；结果详情和下载区；拖入画布；Safari 文字选择；短名称；任务进度只显示列表；2D/3D 选框合一；右侧面板常开；Meshy 预览后自动精细化。
