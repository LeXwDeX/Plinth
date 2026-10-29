# 工作台设计文档

本目录收录 2026-10-08 至 10-11 工作台重新设计的方案、原则及当时的实施记录。原件在项目共享文件夹 `design/`；仓库副本形成于 2026-10-11。截图、原型 HTML、验收截图和下载包没有放进仓库。文中日期和实施记录仅表示历史事实，不代表当前部署或验收状态。当前进度统一在 原生 Milestones、Issues 和 MRs 跟踪。

## 文件

按阅读顺序：

| 文件 | 内容 |
|---|---|
| [studio-ui-spec.md](studio-ui-spec.md) | UI 规范、代码框架及 2026-10-11 的合并记录 |
| [right-panel-principles.md](right-panel-principles.md) | 右侧栏与工具条原则 R1–R18 |
| [result-card-principles.md](result-card-principles.md) | 结果卡原则 |
| [redesign-spec.md](redesign-spec.md) | “对话 + 舞台”主布局方案 |
| [ux-validation-prototype.md](ux-validation-prototype.md) | 美术视角的原型 UX 验证（F1–F36） |
| [frontend-inventory.md](frontend-inventory.md) | 改版前的前端盘点（50 条问题；行号对应当时源码） |
| [requirements-final-review-2026-10-10.md](requirements-final-review-2026-10-10.md) | PR #19–#42 的历史需求审核报告 |

## 现行界面契约

- **动效时长（#237，2026-10-19 用户要求）**：界面交互动效（hover、选中反馈、按钮、菜单/弹层、Tooltip、对话框、抽屉、面板切换、展开收起、进度条等进入/退出与状态切换）一律在 150ms 内完成，不用延迟或串联动画拉长视觉完成时间。Tailwind 默认过渡与 `tw-animate-css` 的 `animate-in/out` 均为 150ms；`src/__tests__/motion-duration-limit.test.ts` 扫描源码中的时长、延迟工具类与内联 transition，超过 150ms 即失败。持续循环的加载指示（`animate-spin`、`animate-pulse`）是状态提示而非交互过渡，不受此限；生成任务耗时和用户 3D 动画资产的播放时长也不属于界面动效。`prefers-reduced-motion` 的减少动画行为保持不变。
- **选择列表的选中态（#236）**：选中行在 hover/聚焦时仍保留主色底、内描边与勾选标记，hover 不能把它刷成普通行；未选中行的 hover 只用中性底色。
- **表单字段与筛选栏（#268）**：`src/components/ui/field.tsx` 的纵向字段是 flex 列，标签、控件、帮助与错误之间统一 8px 间距（`gap-2`）；不再用 `space-y-*` 子元素外边距，因为行内 `<label>` 的下外边距不生效，而 Base UI Select 在触发器后渲染的脱离文档流的隐藏 input 会让触发器多出外边距，使 Select 字段比同排日期字段高 8px。同排筛选栏（资产审查、操作日志、生成记录）用 `flex flex-wrap items-end gap-3`，字段与操作按钮都是该行的直接子项：标签顶边、控件上下边与按钮底边各自对齐，窗口变窄时按钮整体换行，不另加包裹层或占位。对齐修正改结构，不用负外边距、`translate` 或空白字符补偿。
- **模型列表积分（#235）**：模型选择表的积分单元格只显示数值，不带“积分”或供应商积分单位；无估算仍显示 `—`。
- **模型表的供应商顺序与隐藏项（#100）**：2D/3D 模型选择表先按 Schema `provider_order` 把同一供应商的行排在一起，组内可用模型在前、保持目录原有顺序；前端不写死供应商顺序。`enabled:false` 的 Offering（含全部 Meshy Creative Lab）不出现在任何选择列表里，也不显示为“未启用”；“未启用”只用于已登记但运行配置不可用的 Offering。

## 当前跟踪

本目录不再维护开发计划或验收状态。阅读相应 原生 Milestones、Issues 和 MRs 获取当前进度；设计文档仅保留产品与界面契约、历史决策和验收记录。已迁出的历史材料可从固定快照查阅：Agent 工作区实现台账（历史文件 `docs/agent-workspace-implementation-ledger.md`（已移出仓库，见内部 Git 历史 `b43d4018`））、Provider/Schema 实施计划（历史文件 `docs/provider-schema-implementation-plan.md`（已移出仓库，见内部 Git 历史 `b43d4018`））。
