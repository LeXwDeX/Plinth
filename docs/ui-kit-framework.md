# Plinth UI Kit

本规范适用于 Go 托管的 React 19 + Vite 8 SPA。Product Schema 决定显示哪些能力、参数、选项、默认值和文案；UI Kit 决定控件如何组合、如何交互，以及共用的视觉语言。UI Kit 不读取 Schema、store、路由或 Provider 身份。

## 四类职责

| 职责 | 当前实现 | 使用规则 |
|---|---|---|
| Surface | `src/components/ui/surface.tsx`；`Card` 建立在 Surface 上 | 容器的底色、边框、圆角、内边距和阴影由有限变体提供。调用方负责布局、尺寸和响应式位置。`variant` 是视觉属性，不使用无效的 `role="panel"`。 |
| Overlay | `dialog.tsx`、`side-drawer.tsx`、`overlay.ts`，以及 Base UI Select/Menu/Tooltip | 负责遮罩、portal、锚定、层序、焦点、Tab 与 Escape。普通弹出层、模态内弹出层和阻断式确认层使用各自的层序令牌。 |
| Field | `field.tsx` + Input/Select/Textarea 等原生或 Base UI 控件 | 用一个 label/legend、控件 ID、帮助与错误 ID 建立关联。组合控件保留自身类型和键盘语义；`Field` 不解析业务 Schema。 |
| Action | `button.tsx` 的 `Action`（与 `Button` 共用实现） | 普通按钮和图标操作使用统一尺寸、焦点、禁用和 loading 语义。图标按钮必须有可访问名称。导航仍是链接；Tabs/MenuItem/Switch 保留各自角色与键盘协议。 |

画布节点、图片裁切、模型渲染、进度条和颜色样本有自身几何语义，不强行包装成卡片或按钮。语义化 HTML 元素（表格、列表、导航、链接）可以直接组合。实例不以“是否被某组件包住”判断合规，应检查它是否重复实现该职责、是否有键盘和无障碍缺陷。

## 设计语言与边界

- `src/app/globals.css` 定义语义色、半径与层序；业务代码不写固定色值。
- 橙色表示提醒（2026-10-09 用户要求）。提醒标签用橙色底加白字：`bg-reminder` 作底色，`text-reminder-foreground`（白色）作文字色，例如模型选择表里的“推荐”标签（2026-10-09 用户要求改为白字，提高对比度）。底色为 #C2410C，白字对比度 5.2:1，不低于 4.5:1；需要更亮的橙色（如参考线）时另设 token。橙色只用于提醒，不用于错误或警告：错误用 `destructive`，警告用 `warning`。`src/__tests__/design-token-discipline.test.ts` 检查已约束的颜色、圆角、层序及 UI 基元的业务依赖边界。
- 圆角使用 `sm/md/lg/xl/full` 阶梯。消息气泡指向角、媒体裁切和移动抽屉的上边角是有语义的局部形状。间距由 Tailwind 的既有尺度提供；本仓库的 `gap-1.5`、`p-2.5` 等控件密度仍在使用，不能引用一张没有单位的旧表强行判错。
- `Surface` 不负责 z-index；普通容器不设层序。Overlay 使用 `z-float`、`z-scrim`、`z-modal`、`z-modal-float`、`z-blocking`、`z-blocking-float` 等语义层。付费批准以对话里的行内批准卡呈现，只允许人明确批准或拒绝，关闭或忽略不能等于批准。
- 3D 视口的 `ViewportDialog` 是非模态处理面板：它只遮挡右侧视口，左侧 Agent 保持可操作；Escape 仅作用于面板内焦点。历史和付费确认继续由顶层 Base UI Dialog 管理焦点。
- 右键打开的菜单一律用 `ui/context-menu.tsx`（Base UI ContextMenu），不要手工设置普通 `DropdownMenu` 的 `open` 并放一个隐藏触发器：普通 Menu 的浮层在指针离开时以 `trigger-hover` 关闭，打开动画产生的 `mouseleave` 就能让它闪现后消失（#280）；ContextMenu 在指针处锚定，浮层不做悬停关闭，子菜单仍用同一套 Menu 部件。
- 锚定浮层宽度至少容纳触发器，并受可用视口宽度约束；不得用 `w-(--anchor-width)` 将内容锁死到触发器宽度。模态和阻断层中的 Select/Menu 通过 `OverlayLayerContext` 提升。
- `Surface` 的 `className` 用于布局和定位。普通卡片不在业务文件里重复写底色、边框、圆角、阴影；视觉上需要新的语义外观时先扩展 Surface 的有限变体。表单控件的外观继续由 Input/Select 等基元负责。

## 现行覆盖

| 可达区域 | 容器与操作 | 字段与特殊交互 |
|---|---|---|
| 登录 | 登录卡片和错误提示用 Surface；提交、显隐密码用 Action | 用户名、密码用 Field；只提供账号密码登录 |
| 工作区 | 顶栏、Agent 区、画板外壳、任务托盘、历史项、聊天气泡和画布工具条用 Surface/Action | SchemaParameters、上传、历史搜索、聊天输入用 Field；历史使用 Dialog；3D 处理使用 ViewportDialog |
| 后台 | MainLayout/AppTopbar/Sidebar、Dashboard、Users、User Assets、Audit、Generations 的卡片和普通操作用 Surface/Card/Action | 日期、用户属性、后台筛选与查询用 Field；菜单和导航保留 Base UI 与链接语义 |
| 结果与资源 | 任务详情（`ResultDetail`）是版本条、全部产出、对话结果卡、任务中心和我的资源的唯一详情面板，建在 Dialog 上；卡片悬停下载用 Action（media 变体），右键菜单用 `ui/context-menu.tsx` | 字段全部来自 Product Schema（Offering 参数标签与选项标签、`task_states`、`ui.result.*`）；信息区是定义列表，动作区按任务类型和 Agent 模式取舍；上传资源（无任务）只有预览、文件信息和下载 |
| 画布与媒体 | 工具条、图层面板、图片操作的普通容器/操作使用共用基元；画布右键菜单用 `ui/context-menu.tsx`，Konva Stage 是它的触发区（#280） | Konva 节点、动态色块、媒体裁切及模型渲染保持专门实现 |

`SchemaParameters` 只投影 Offering 的公开 `presentation` 与校验信息。布尔、数字、区间、选择、资产和多选字段保留各自值语义；隐藏、禁用、强制默认值仍由 Product Schema 与服务端判定。前端不恢复静态 Provider 参数树。

## 加载边界

路由已按页面 lazy。工作区将 2D 画布与 3D 处理面板分别按实际使用路径 lazy；3D 引擎仅在有模型预览时加载。2D 使用精简的 ReactKonvaCore 并显式注册所需形状；模型仍使用同源 `model-viewer`、Three 与现有 Meshopt 解码。Vite 8 的 Rolldown 按 Three core/renderer 分块，服务于缓存与单包解析；分块不等于进入 3D 时的总传输减少。

包体报告必须同时列出压缩字节和加载闭包：入口、工作区外壳、进入 2D、进入 3D、打开处理面板。构建分块只能推断 import 闭包，浏览器 Network 才能证明请求时机和缓存行为。不得靠提高 chunk warning 阈值或改用外部 CDN 宣称优化完成。

## 验收

发布前门禁命令只以 `dev_test.md` §2 为真源。本规范增加以下交互检查：

1. Chromium 与 WebKit 的桌面、移动视口：登录、工作区、后台导航、Select/Menu 长标签与焦点可见性。
2. Field：点击标签聚焦、描述/错误 `aria-describedby`、必填与禁用、原生 Enter 提交；Schema 各参数类型的值与默认值保持一致。
3. Action：图标名称、loading 期间防重入；生成、取消、重试、下载和上传继续调用原有 API。
4. Overlay：Tab/Shift-Tab、Escape、遮罩关闭、模态内浮层层序；付费同意保持显式操作。工作区没有遮罩和阻断式对话框：任务中心是非模态抽屉，3D 加工是右侧栏里的非模态标签，对话列在布局中而不是浮层，所以打开它们时对话列与舞台的边界矩形不改变（允许亚像素舍入）。
5. 2D/3D：画布绘制、选中、保存、导出，模型旋转、Meshopt 载入、截图转 2D；浏览器 Network 验证 2D 不请求 Three，3D 空态不请求 Konva，处理面板只在打开后请求。

测试环境发布与页面/API 冒烟按 `AGENTS.md`、`dev_ops.md` 和 `scripts/smoke/README.md` 执行。构建成功、单测通过或设计预览本身都不等于测试环境验收。
