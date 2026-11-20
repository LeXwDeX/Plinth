# 工作台设计文档

本目录收录工作台重新设计的方案、原则及实施记录。截图、原型 HTML、验收截图和下载包不在仓库。其中的实施记录与审核报告是历史证据，不代表当前部署或验收状态。当前进度统一在原生 Milestones、Issues 和 MRs 跟踪。

## 文件

按阅读顺序：

| 文件 | 内容 |
|---|---|
| [studio-ui-spec.md](studio-ui-spec.md) | UI 规范、代码框架及合并记录 |
| [right-panel-principles.md](right-panel-principles.md) | 右侧栏与工具条原则 R1–R18 |
| [result-card-principles.md](result-card-principles.md) | 结果卡原则 |
| [redesign-spec.md](redesign-spec.md) | “对话 + 舞台”主布局方案 |
| [ux-validation-prototype.md](ux-validation-prototype.md) | 美术视角的原型 UX 验证（F1–F36） |
| [frontend-inventory.md](frontend-inventory.md) | 改版前的前端盘点（50 条问题；行号对应当时源码） |
| [requirements-final-review-2026-10-10.md](requirements-final-review-2026-10-10.md) | PR #19–#42 的历史需求审核报告 |

## 现行界面契约

- **动效时长（#237）**：界面交互动效（hover、选中反馈、按钮、菜单/弹层、Tooltip、对话框、抽屉、面板切换、展开收起、进度条等进入/退出与状态切换）一律在 150ms 内完成，不用延迟或串联动画拉长视觉完成时间。Tailwind 默认过渡与 `tw-animate-css` 的 `animate-in/out` 均为 150ms；`src/__tests__/motion-duration-limit.test.ts` 扫描源码中的时长、延迟工具类与内联 transition，超过 150ms 即失败。持续循环的加载指示（`animate-spin`、`animate-pulse`）是状态提示而非交互过渡，不受此限；生成任务耗时和用户 3D 动画资产的播放时长也不属于界面动效。`prefers-reduced-motion` 的减少动画行为保持不变。
- **选择列表的选中态（#236）**：选中行在 hover/聚焦时仍保留主色底、内描边与勾选标记，hover 不能把它刷成普通行；未选中行的 hover 只用中性底色。
- **表单字段与筛选栏（#268）**：`src/components/ui/field.tsx` 的纵向字段是 flex 列，标签、控件、帮助与错误之间统一 8px 间距（`gap-2`）；不再用 `space-y-*` 子元素外边距，因为行内 `<label>` 的下外边距不生效，而 Base UI Select 在触发器后渲染的脱离文档流的隐藏 input 会让触发器多出外边距，使 Select 字段比同排日期字段高 8px。同排筛选栏（资产审查、操作日志、生成记录）用 `flex flex-wrap items-end gap-3`，字段与操作按钮都是该行的直接子项：标签顶边、控件上下边与按钮底边各自对齐，窗口变窄时按钮整体换行，不另加包裹层或占位。对齐修正改结构，不用负外边距、`translate` 或空白字符补偿。
- **模型列表积分（#235）**：模型选择表的积分单元格只显示数值，不带“积分”或供应商积分单位；无估算仍显示 `—`。
- **模型表的供应商顺序与隐藏项（#100）**：2D/3D 模型选择表先按 Schema `provider_order` 把同一供应商的行排在一起，组内可用模型在前、保持目录原有顺序；前端不写死供应商顺序。`enabled:false` 的 Offering（含全部 Meshy Creative Lab）不出现在任何选择列表里，也不显示为“未启用”；“未启用”只用于已登记但运行配置不可用的 Offering。
- **「无法使用」提示（#93、#94）**：凡是说明“不可用 / 被拒 / 超限 / 禁用原因”的地方，页面上只出现警告图标 + 「无法使用，<几个字的短原因>」，限制值与实际值、上游要求和下一步做法只放在悬停提示（及无障碍描述）里，不在页面铺长段落。持续状态用 `src/components/ui/unavailable-notice.tsx`；禁用控件把短原因交给 `HintTooltip` 的 `reason`，气泡第一行自动画同一图标与「无法使用，…」。短原因放在 Schema `ui.unavailable.reason.<区域>.<名>`（中英文齐全、占位符一致，Catalog 校验），前端不另建文案。请求进行中的瞬时禁用、分页到头等边界态、动作失败的 toast 和说明性文字不套用此形态。
- **输入区的设置入口（#405；源码已实现，未部署验收）**：输入框下方不再放生成模型、Skills、生成参数、对话模型与思考档位四个选择器入口，只留状态行（上下文用量、压缩上下文、工具调用次数）；发送按钮右侧是一个齿轮（`composer-settings-open`），是这四类设置的唯一入口。齿轮打开居中的大模态页（宽 `min(76rem, 100vw-2rem)`、高 `min(48rem, 100dvh-3rem)`，标题与 TAB 固定、内容区滚动，不随窗口变窄而溢出），顶部用 `ui/tabs` 分四页：生成模型、Skills、生成参数、对话模型与思考档位（后者在没有可选的模型或档位时不出现）。TAB 是 `role=tablist/tab/tabpanel`，roving tabindex，左右方向键切换并选中；Esc 关闭，焦点回到齿轮；打开时回到上次的 TAB（只记在本次访问里）；其他代码用 `openComposerSettings(tab)` 打开并定位。每页内容与行为沿用原选择器，选择即时生效、不关设置页；不可用原因仍按「无法使用，<短原因>」（#93、#94）。齿轮在有设置不同于默认时显示小圆点（选了 Skill、选了生成模型、手动改过参数、对话模型或档位非默认），所选 Skill 不可用或对话模型读取失败时为警告色；名称与悬停写明改了哪几类和当前值。TAB 名、齿轮名称与提示、状态提示在 Schema `ui.studio.composer.settings.*`（中英文齐全），前端不内置文案。
- **输入区的图片条（#405 范围补充；源码已实现，未部署验收）**：当前对象（含「当前：画布」）、蒙版、分支、Skill、上传的图与来自画板的图都是同一个 `ContextChip`，放在同一个两列网格里（`composer-chips`，`grid grid-cols-2 gap-1.5`）：每条都填满自己的一格（`w-full min-w-0`），所以所有条一样宽——半行减去半个 6px 间距，不随内容变；个数是奇数时最后一条仍占一格，不拉满整行；高度统一 40px，关闭按钮在条的右缘；缩略图、类型标记保留；第二行只是重复名称（「当前：画布」下的「画布」）时合成一行；文字放不下时截断，悬停或键盘聚焦显示完整内容。窄栏（输入区宽度小于 24rem）里类型标记只留「2D / 3D」不显示图标。图片下面每张图的用途行与整体的「用途」行共用一个两列网格（`composer-attachments`，`grid-cols-[fit-content(40%)_minmax(0,1fr)]`）：标签列按最长的标签定宽（至多一行的 40%，放不下截断，悬停显示全文），分段控件左缘对齐、宽度一致、三段等宽（`grid-cols-3`，文字放不下同样截断并在悬停时显示全文）。

## 当前跟踪

本目录不再维护开发计划或验收状态。阅读相应 原生 Milestones、Issues 和 MRs 获取当前进度；设计文档仅保留产品与界面契约和验收记录。
