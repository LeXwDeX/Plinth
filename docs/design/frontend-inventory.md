# 前端盘点：art-ai-wp7

- 日期：2026-10-08
- 仓库：本仓库（当时为 git worktree `art-ai-wp7`，基于 2026-10-08 的 main）
- 方法：只读。静态阅读源码与 Schema 目录 internal/catalog/default_catalog.json。没有在浏览器里运行页面。
- 没有修改任何仓库文件，没有提交。scripts/ 未触碰。
- 标记：[推断] 表示由代码推测，未运行验证。[未核对] 表示本次没有确认。
- 行号指盘点当时的文件。文件:行 写法里，行号是起点。

---

## 0. 总览

- 产品是一个单页应用（SPA）。
- 核心页面是创作工作台 `/`。工作台有两个域：图片（2D 画布）和 3D 模型。
- 其他页面：登录页、我的资源 `/account`、管理后台 6 页、404 页。
- Agent 对话不是独立页面。它是叠在工作台上的浮层。
- 文案来自 Schema。Schema 里有 1021 个文案 key，其中 `ui.*` 有 943 个。
- 组件、页面和 hooks 里没有硬编码中文（注释除外）。
- 硬编码的全角标点有 4 处（见第 9 节）。
- 只有暗色主题。`:root` 是暗色调色板，`main.tsx:25` 强制加 `dark` 类。
- 技术栈：React 19、Vite、Tailwind 4、zustand（带 persist）、SWR、Konva / react-konva（2D 画布）、@google/model-viewer（3D）、Base UI、Radix（只用于 slider）、lucide-react 图标、sonner 提示。
- 服务器是主存储。浏览器保存偏好，也保存一份画布副本（见第 4 节）。
- 断点：`lg` 以上是桌面端（1024px）。`sm` 是 640px。`xl` 按 Tailwind 默认值。

---

## 1. 路由与页面

### 1.1 页面表

| 路径 | 页面入口 | 谁能看到 | 说明 |
|---|---|---|---|
| `/` | src/app/workspace/page.tsx（111 行） | 登录用户 | 创作工作台。主导航项 `content.navigation` 的 `workspace`，文案“创作工作台”。 |
| `/login` | src/app/login/page.tsx（210 行） | 公开（`authentication.public_paths`） | 账号密码登录页。回跳参数来自 `authentication.login.redirect_query`，只接受同源相对路径。 |
| `/account` | src/app/account/page.tsx（185 行） | 登录用户 | 我的资源：用量、上传列表、任务列表。主导航项文案“我的资源”。 |
| `/admin` | src/app/admin/page.tsx（418 行） | 仅管理员 | 数据概览。精确匹配。 |
| `/admin/users` | src/app/admin/users/page.tsx（508 行） | 仅管理员 | 用户管理。 |
| `/admin/audit` | src/app/admin/audit/page.tsx；面板 src/components/admin/audit-log-panel.tsx（557 行） | 仅管理员 | 操作日志。 |
| `/admin/generations` | src/app/admin/generations/page.tsx；面板 src/components/admin/generations-panel.tsx（917 行） | 仅管理员 | 生成记录。 |
| `/admin/user-assets` | src/app/admin/user-assets/page.tsx（981 行） | 仅管理员 | 资产审查。 |
| `/admin/agent-configuration` | src/app/admin/agent-configuration/page.tsx（107 行） | 仅管理员 | Agent 管理。 |
| 其他路径 | src/app/not-found.tsx（24 行） | 所有人 | 404 页。按钮“返回首页”（not-found.tsx:19-21）。 |

主导航里的“管理后台”项为 `/admin`，`admin_only`，放在底部（Schema `content.navigation`）。

### 1.2 路由机制

- 自定义路由，没有用 react-router。
- `web/src/router.tsx` 懒加载 WorkspacePage、AccountPage、LoginPage（13-15 行）。
- 管理页映射表在 17-27 行。路由类型在 43-63 行。
- 无权限或不存在的路由渲染 NotFound（58-62 行）。
- 路由投影在 `src/lib/administration/route-access.ts`。`/` 固定为工作台（29-31 行）。管理路由按 Schema 导航匹配（49-62 行）。
- 页面外壳：`/account` 和 `/admin/*` 用 `MainLayout`（router.tsx:46、52-56）。工作台和登录页不用。
- `MainLayout` 在 `src/components/layout/main-layout.tsx`。登录页不显示外壳（17-24 行）。
- 浏览器导航在 `src/lib/browser-navigation.ts`（history API 加自定义事件）[未核对细节]。
- 登录门：`web/src/authentication-gate.tsx`，20-47 行。公开路径来自 Schema。未登录时跳到登录页，并带 redirect 参数。

### 1.3 管理后台子导航

来源：`src/app/admin/layout.tsx:24-41`，读自 `schema.administration.navigation`。

| 顺序 | 文案 | 路径 | 匹配方式 |
|---|---|---|---|
| 1 | 数据概览 | `/admin` | 精确 |
| 2 | 用户管理 | `/admin/users` | 前缀 |
| 3 | 操作日志 | `/admin/audit` | 前缀 |
| 4 | 生成记录 | `/admin/generations` | 前缀 |
| 5 | 资产审查 | `/admin/user-assets` | 前缀 |
| 6 | Agent 管理 | `/admin/agent-configuration` | 前缀 |

### 1.4 入口

- 工作台用户菜单里有“管理后台”项（workspace-topbar.tsx:199-209）。只对管理员显示。
- 工作台用户菜单里有“我的资源”项（workspace-topbar.tsx:210-216）。
- 管理员和账户页的侧栏（`sidebar.tsx`）也有导航。侧栏只在 `MainLayout` 下出现。

---

## 2. 工作台布局

工作台页面根节点是 `h-dvh` 的 Surface（page.tsx:54）。顶栏在上，画布在下。Agent 对话、历史抽屉、任务托盘都是浮层。

### 2.1 区域表

| 区域 | 位置与尺寸 | 打开 / 关闭 | 文件:行 |
|---|---|---|---|
| 顶栏 WorkspaceTopbar | 页面顶部，高 `h-14`（56px），满宽，`shrink-0`，有底边线 | 常驻 | workspace-topbar.tsx:96；page.tsx:55 |
| 画布区 CanvasStage | `main` 内 `absolute inset-0`，占顶栏以下全部空间 | 常驻 | page.tsx:57-59 |
| Agent 对话浮层（桌面） | 左上 `lg:left-4 lg:top-4`；宽 `lg:w-[min(420px,40vw)]`；高自适应。底部距离：图片域 `lg:bottom-20`，3D 域 `lg:bottom-4`。层级 `lg:z-float`。 | 桌面：宽度 ≥1024px 时自动打开（page.tsx:34-41）。关闭：顶部关闭按钮（agent-dialog.tsx:749-753）。 | page.tsx:68-90, 82-83 |
| Agent 对话浮层（移动） | 底部 `inset-x-2 bottom-2`；高 `h-[min(72dvh,640px)]` | 打开：顶部触发按钮。关闭：关闭按钮、点遮罩、Escape（只在移动端，page.tsx:72-76）。 | page.tsx:68-90 |
| 移动遮罩 scrim | 全屏，`lg:hidden`，`bg-black/50`，是一个按钮 | 点击关闭 Agent | page.tsx:60-67 |
| Agent 触发按钮 | 画布上方的 Action；图片域时 `bottom-20` | 打开 Agent；`aria-expanded` 写死为 `false` | page.tsx:91-106（97、100 行） |
| 历史抽屉 HistoryDrawer | 实际是居中的 Dialog。`sm:max-w-2xl`，高 `min(85vh,640px)` | 打开：顶栏“历史任务”按钮（topbar 147-162）。关闭：关闭按钮、Escape、遮罩 | history-drawer.tsx:127-242 |
| 任务托盘 TaskTray | 顶栏右侧按钮（ListTodo 图标，带进度和数量角标）。点击打开 Popover；展开按钮打开 Dialog。 | 点击切换；展开后有 Dialog | topbar 146；task-tray.tsx:514-561、524-541 |
| 用户菜单 | 顶栏右上角；`align="end"`，宽 `w-64` | 点击触发器 | workspace-topbar.tsx:166-230 |
| 改名对话框 | 居中 Dialog，无右上关闭按钮（`showCloseButton={false}`） | 点击会话标题打开；取消或保存关闭 | workspace-topbar.tsx:232-257 |
| 画布底部工具条 CanvasToolbar | 画布底部的 Surface 条（footer strip） | 常驻（图片域） | canvas-board.tsx:1348-1363；canvas-toolbar.tsx |
| 样式浮层（工具栏内） | Popover，portal 到 body | 点样式按钮打开 | canvas-toolbar.tsx:333-426、104-149 |
| 背景样式浮层 | Popover | 点背景按钮打开 | canvas-toolbar.tsx:456-499 |
| 形状浮层 | Popover | 点形状按钮打开 | canvas-toolbar.tsx:240-269 |
| 图层面板 LayersPanel | `absolute right-3 top-3 z-float w-56` | 工具栏“图层面板”切换 | layers-panel.tsx:62；toolbar 500-508 |
| 单图动作条 CanvasImageActionBar | `absolute right-3 top-3 z-20` | 只在选中单张图片且工具为选择时显示 | canvas-image-actions.tsx:475-511；workspace-canvas-board.tsx:465-479 |
| 画布参数条 CanvasSurfaceParams | 顶部居中，`absolute top-3 left-1/2 z-20 w-44` | 常驻（图片域挂载，workspace-canvas-board.tsx:444） | canvas-surface-params.tsx:59 |
| 蒙版条 | 提示在 `top-3`；工具条在 `top-12` | 进入蒙版模式时 | canvas-board.tsx:1174-1284 |
| 裁剪条 | `top-3` | 进入裁剪模式时 | canvas-board.tsx:1285-1326 |
| 文字编辑框 | 固定 `left 16 / top 16` | 双击文字节点时 | canvas-board.tsx:1327-1342。[推断] 不跟随节点位置。 |
| 空画布提示 | 画布中部 | 画布无节点时 | canvas-board.tsx:728-736 |
| 3D 画布区 | `section`，`p-6 pb-24`，居中 | 选中 3D 模型时 | canvas-stage.tsx:126-150 |
| 3D 格式卡 | 左上 `absolute left-5 top-5 z-10`，`max-h-[60%]`，宽 `min(calc(100%-2.5rem),280px)` | 选中 3D 模型时常驻 | canvas-stage.tsx:142-145 |
| “处理模型”按钮 | 底部居中 `absolute bottom-5 left-1/2` | 常驻（有模型时） | canvas-stage.tsx:146 |
| 3D 处理面板 | 右侧，`absolute inset-y-0 right-0 z-modal w-[min(100%,420px)]`；背后有遮罩 `bg-black/50` | 点“处理模型”打开；关闭按钮或 Escape | canvas-stage.tsx:147；viewport-dialog.tsx:33-36、48、57 |
| 截图按钮 | 3D 查看器右下角，raised 变体 | 常驻（有 assetId 时） | model-viewer-3d.tsx:174-179 |
| 同意对话框 ConsentDialog | 居中，blocking 层，无关闭按钮 | 有待确认的同意请求时自动出现 | consent-dialog.tsx:82-88、88 |
| Skill 选择对话框 | 居中 Dialog，`w-[min(1120px,calc(100vw-80px))]`，高 `min(768px,...)` | 点 Skill 按钮 | skill-selector.tsx:35-69 |
| Skill 创建对话框 | 居中 Dialog，无右上 X | 点“创建 Skill” | skill-creator.tsx:64-81 |
| 局部重绘对话框 InpaintDialog | 居中 Dialog | 单图动作条或菜单触发 | canvas-image-actions.tsx:161-334 |
| 去背景对话框 | 居中 Dialog | 单图动作条“去背景” | canvas-image-actions.tsx:335-473 |
| 切换蒙版模型对话框 | 居中 Dialog | 点蒙版工具但当前模型不支持 | workspace-canvas-board.tsx:480-526 |
| 聊天图片灯箱 Lightbox | 全屏覆盖层 | 点聊天图片 | chat-message.tsx:81-224 |
| 提示 Toast | 右上角（Schema `runtime.frontend.toaster`） | 自动 | main.tsx（Toaster）；Schema |
| 隐藏的文件选择 | `input type=file` | 由工具栏“上传”或拖拽、粘贴触发 | workspace-canvas-board.tsx:543-547 |

### 2.2 层级

- 层级用命名 token：`z-float` 20、`z-scrim` 30、`z-modal` 40、`z-modal-float` 45、`z-blocking` 50、`z-blocking-float` 55、`z-toast` 60。
- 定义在 `src/app/globals.css:60-68` 与 `src/components/ui/overlay.ts:8-12`。
- 同意对话框在 blocking 层，高于 Agent 浮层。
- 3D 处理面板用 `z-modal`，遮罩用 `z-scrim`。
- [推断] 桌面上 Agent 浮层（`lg:z-float`）和 3D 处理面板（`z-modal`，全高遮罩）会叠在一起。遮罩可能盖住 Agent。`docs/ui-kit-framework.md` 说 3D 面板是非模态，左侧 Agent 仍可用。代码里的遮罩与这个说法不一致。[未在浏览器验证]

---

## 3. 用户操作清单

格式：操作 | Schema key | 中文文案 | 行为 | 文件:行

### 3.1 顶栏

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 切换到图片域 | `ui.workspace.tab.image` | 图片创作 | `setDomain('image')`。按钮是原生 `button`，带 `aria-pressed`。窄屏只显示图标。 | workspace-topbar.tsx:53-56、104-127（112） |
| 切换到 3D 域 | `ui.workspace.tab.model_3d` | 3D 模型 | `setDomain('model-3d')`。同上。 | 同上 |
| 重命名会话 | `ui.workspace.session.rename` | 重命名主题 | 点会话标题打开改名对话框。 | workspace-topbar.tsx:130-143（135）；232-257（234） |
| 改名输入框 | `ui.workspace.session.title` | 主题名称 | 输入新标题。聚焦时全选。 | topbar 237-245 |
| 保存改名 | `ui.workspace.session.save` | 保存 | 调用 `renameSession`，成功后更新会话标题。空标题不可保存。 | topbar 70-84、252 |
| 取消改名 | `ui.workspace.session.cancel` | 取消 | 关闭对话框。 | topbar 249 |
| 改名失败提示 | `ui.workspace.session.rename_failed` | 重命名失败，请重试 | Toast 错误。 | topbar 79 |
| 任务托盘 | `ui.workspace.tasks.open` | 查看任务进度 | 打开托盘 Popover。 | task-tray.tsx:524-541、103 |
| 历史任务 | `ui.workspace.history.all` | 历史任务 | 切换历史抽屉。窄屏隐藏文字（`hidden lg:inline`）。 | topbar 147-162（150）；history-drawer.tsx:57 |
| 余额 | `ui.dashboard.balance` | 账户余额 | 悬停或聚焦时弹出余额列表。显示最低余额加“点”。 | workspace-balance.tsx:60-92（50、51、69、73、84） |
| 余额无数据 | `ui.workspace.balance.unavailable` | 余额暂无数据 | 无数据时显示。 | workspace-balance.tsx:51 |
| 余额单位 | `ui.workspace.balance.points` | 点 | 余额后缀。 | workspace-balance.tsx:50 |
| 用户菜单 | （名字 / `ui.dashboard.fallback_initial`） | 名字首字；无名字时显示“·” | 打开用户菜单。 | topbar 166-177、67 |
| 用户菜单：余额行 | `ui.dashboard.balance` / `ui.dashboard.balance_unavailable` | 账户余额 / — | 禁用项，只显示。 | topbar 179-198（183、194） |
| 管理后台入口 | 导航项 `administration.navigation`（精确匹配） | 数据概览（取第一项精确匹配） | `<a href>` 到 `/admin`。只对管理员显示。 | topbar 199-209 |
| 我的资源入口 | 导航项 `content.navigation` `account` | 我的资源 | 到 `/account`。 | topbar 210-216 |
| 退出登录 | `ui.workspace.user.logout` | 退出登录 | 调用 `authentication.logout()` 删除当前 session，然后跳到 `runtime.frontend.client_routes.login`。失败也跳转。 | topbar 85-93、217-227（58） |

### 3.2 2D / 3D 切换

- 唯一的切换入口是顶栏的两个 tab（见 3.1）。
- 详细行为见第 6 节。

### 3.3 画布工具栏（图片域）

工具栏为 `CanvasToolbar`，在 `src/components/canvas/canvas-toolbar.tsx`。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 选择工具 | `ui.canvas.toolbar.select_tooltip` | 选择工具 (V) | 切换为 select 工具。快捷键 V。 | 218-226（219-220） |
| 平移 | `ui.canvas.toolbar.hand_tooltip` | 平移画布 (H) | 切换为 hand 工具。快捷键 H。 | 227-235（228-229） |
| 上传图片 | `ui.canvas.toolbar.upload` | 上传图片 | 打开隐藏的文件选择。 | 237-239；board 543-547 |
| 形状工具 | `ui.canvas.toolbar.shapes` | 形状工具 | 点击设置形状工具并切换浮层。 | 240-269（242） |
| 矩形 | `ui.canvas.toolbar.rectangle` | 矩形 | 形状浮层项。 | 201 |
| 箭头 | `ui.canvas.toolbar.arrow` | 箭头 | 形状浮层项。 | 202 |
| 直线 | `ui.canvas.toolbar.line` | 直线 | 形状浮层项。快捷键 L。 | 203 |
| 椭圆 | `ui.canvas.toolbar.ellipse` | 椭圆 | 形状浮层项。快捷键 O。 | 204 |
| 文字工具 | `ui.canvas.toolbar.text` | 文字工具 (T) | 切换文字工具。点画布创建文字节点。 | 270-276（271） |
| 铅笔 | `ui.canvas.toolbar.pencil` | 矢量铅笔 (P) | 切换铅笔工具。 | 277-283（278） |
| 画笔 | `ui.canvas.toolbar.brush_tooltip` | 像素画笔 (B) | 切换画笔工具。 | 284-292（285-286） |
| 橡皮 | `ui.canvas.toolbar.eraser_tooltip` | 橡皮 (E) | 切换橡皮工具。 | 293-301（294-295） |
| 裁剪 | `ui.canvas.toolbar.crop_tooltip` | 裁剪 (K) | 切换裁剪工具。进入裁剪条。 | 302-310（303-304） |
| 蒙版（局部重绘） | `ui.canvas.toolbar.mask_tooltip` | 局部重绘蒙版 (M) | 当前模型支持蒙版时进入蒙版模式。不支持时打开“切换模型”对话框。 | 311-331（314-315） |
| 蒙版不支持提示 | `ui.canvas.mask.unsupported_tooltip` | 当前模型不支持局部重绘，点击切换 | 工具提示。 | 317 |
| 样式 | `ui.canvas.toolbar.style` | 样式 | 打开样式浮层。 | 333-426（335） |
| 线色 | `ui.canvas.style.line_color` | 线色 | 颜色选择。 | 344（343-362） |
| 填充 | `ui.canvas.style.fill` | 填充 | 颜色选择。 | 364（363-395） |
| 无填充 | `ui.canvas.style.no_fill` | 无填充 | 清除填充。 | 378 |
| 笔刷大小 | `ui.canvas.toolbar.brush_size` | 笔刷大小 | 标题。实际值为 `线宽 × 3`（见第 9 节）。 | 398（396-423，410、414） |
| 线宽 | `ui.canvas.style.line_width` | 线宽 | 线宽档位（预设 `[1,2,3,5,8]`）。 | 399 |
| 撤销 | `ui.canvas.toolbar.undo` | 撤销 | 撤销。快捷键 Ctrl/Cmd+Z。 | 427-429 |
| 重做 | `ui.canvas.toolbar.redo` | 重做 | 重做。快捷键 Shift+Ctrl/Cmd+Z 或 Ctrl/Cmd+Y。 | 430-432 |
| 缩小 | `ui.canvas.toolbar.zoom_out` | 缩小 | 缩放，步长 0.1，范围 0.1 到 3。 | 434-436 |
| 缩放百分比 | （无 key） | — | 只显示当前比例。 | 437-439 |
| 放大 | `ui.canvas.toolbar.zoom_in` | 放大 | 缩放。 | 440-442 |
| 适应屏幕 | `ui.canvas.toolbar.fit` | 适应屏幕 | 适应所有节点。图标为 Minus。 | 443-451（444） |
| 重置视图 | `ui.canvas.toolbar.reset` | 重置视图 | 重置视口。图标为 Square。 | 452-454 |
| 背景样式 | `ui.canvas.toolbar.background` | 背景样式 | 打开背景浮层（点阵、网格、纯色、深色；来自 Schema `canvas.frontend.backgrounds`）。只写本地（见第 9 节）。 | 456-499（458） |
| 图层面板 | `ui.canvas.toolbar.layers` | 图层面板 | 切换图层面板。 | 500-508（502） |
| 导出 | `ui.canvas.toolbar.export` | 导出画板为 PNG | 下载 PNG。失败提示 `ui.workspace.canvas.export_failed`。 | 509-513（510）；workspace-canvas-board.tsx:380-406 |
| 发送到对话 | `ui.canvas.action.send_to_chat` | 发送到对话 | 只在选中节点数 > 0 时显示。把选中图加入对话参考并打开 Agent。 | 514-521（516）；board 380-406 |
| 图片生成 | `ui.canvas.toolbar.generate` | 图片生成 (I) | 调用 `onImageGenerator`。快捷键 I。具体动作未逐行核对。 | 522-524（522）；workspace-canvas-board.tsx:452 [未核对] |

### 3.4 画布键盘快捷键

来源：canvas-board.tsx:621-704；快捷键表在 Schema `canvas.frontend.keyboard`。

| 按键 | 行为 |
|---|---|
| 空格（按住） | 临时平移画布 |
| Ctrl/Cmd+Z | 撤销 |
| Shift+Ctrl/Cmd+Z 或 Ctrl/Cmd+Y | 重做 |
| Ctrl/Cmd+C | 复制选中 |
| Ctrl/Cmd+V | 粘贴 |
| Ctrl/Cmd+A | 全选 |
| G | 编组 |
| Shift+G | 取消编组 |
| Delete / Backspace | 删除选中 |
| V | 选择工具 |
| H | 平移 |
| T | 文字工具 |
| R | 矩形（工具栏没有标出该键，见第 9 节） |
| P | 铅笔 |
| B | 画笔 |
| E | 橡皮 |
| K | 裁剪 |
| M | 蒙版 |
| L | 直线 |
| O | 椭圆 |
| I | 图片生成 |

### 3.5 画布节点操作

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 空画布提示 | `ui.canvas.board.empty` | 在 Agent 对话中描述你想生成的图片，发送后图片会出现在这里 | 无节点时显示。 | canvas-board.tsx:728-736（733） |
| 文字节点默认文案 | `ui.canvas.node.default_text` | 双击编辑文字 | 新建文字节点时的占位。 | canvas-board.tsx:433 |
| 双击编辑文字 | （无） | — | 双击文字节点打开文字编辑框。 | canvas-board.tsx:884-888 |
| 变换手柄 | （无） | — | 四角缩放，旋转开启。 | canvas-board.tsx:1120-1141 |
| 裁剪最小尺寸 | （无） | — | 裁剪框最小 8px，硬编码。 | canvas-board.tsx:1147 |
| 上传失败 | `ui.workspace.canvas.upload_failed` | 上传图片失败，请重试 | Toast 错误。 | canvas-board.tsx:397、1259；workspace-canvas-board.tsx:141 |

### 3.6 蒙版与裁剪模式

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 蒙版提示 | `ui.canvas.mask.hint` | 涂抹需要重绘的区域，提交后由图生图模型处理 | 蒙版模式顶部提示。 | canvas-board.tsx:1177 |
| 提交蒙版 | `ui.canvas.mask.submit` | 确认区域并提交 | 提交蒙版，进入局部重绘。 | canvas-board.tsx:1268 |
| 退出蒙版 | `ui.canvas.mask.cancel` | 退出蒙版 | 退出蒙版模式。 | canvas-board.tsx:1280；workspace-canvas-board.tsx:505 |
| 应用裁剪 | `ui.canvas.crop.apply` | 应用裁剪 | 应用裁剪。 | canvas-board.tsx:1314 |
| 取消裁剪 | `ui.canvas.crop.cancel` | 取消裁剪 | 取消裁剪。 | canvas-board.tsx:1323 |
| 切换到支持蒙版的模型 | `ui.canvas.mask.switch_title` | 切换到支持局部重绘的模型 | 对话框标题。 | workspace-canvas-board.tsx:489 |
| 切换说明 | `ui.canvas.mask.switch_confirm` | 当前所选模型「{model}」不支持局部重绘。切换到支持蒙版的模型「{target}」并进入涂抹？ | 对话框正文。 | workspace-canvas-board.tsx:494 |
| 切换并涂抹 | `ui.canvas.mask.switch_accept` | 切换并涂抹 | 确认切换模型。 | workspace-canvas-board.tsx:515 |
| 没有候选模型 | `ui.canvas.mask.no_candidate` | 没有可用的高质量图像模型支持局部重绘 | 没有候选模型时显示。 | workspace-canvas-board.tsx:521 |
| 蒙版不支持 | `ui.canvas.mask.unavailable` | 当前模型不支持局部重绘，请选择支持蒙版的模型 | 局部重绘对话框内提示。 | canvas-image-actions.tsx:288 |

### 3.7 图层面板

来源：src/components/canvas/layers-panel.tsx（159 行）。面板为 `absolute right-3 top-3 z-float w-56`（62 行）。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 标题 | `ui.canvas.layers.title` | 图层 | 面板标题。 | 64 |
| 关闭面板 | `ui.canvas.layers.close` | 关闭图层面板 | 关闭。 | 68 |
| 空状态 | `ui.canvas.layers.empty` | 暂无图层 | 无节点时显示。 | 76 |
| 选中图层 | （无） | — | 点行选中节点。 | 96-103 |
| 显示 / 隐藏 | `ui.canvas.layers.visibility_show` / `_hide` | 显示图层 / 隐藏图层 | 切换可见性。按钮只在悬停时显示（`opacity-0 group-hover`）。 | 104-112（106-107） |
| 锁定 / 解锁 | `ui.canvas.layers.lock` / `unlock` | 锁定图层 / 解锁图层 | 切换锁定。悬停显示。 | 113-121（115-116） |
| 上移一层 | `ui.canvas.layers.up` | 上移一层 | 调整层级。悬停显示。 | 122-131（124-125） |
| 下移一层 | `ui.canvas.layers.down` | 下移一层 | 调整层级。悬停显示。 | 132-141（134-135） |
| 删除图层 | `ui.canvas.action.delete` | 删除 | 删除节点。悬停显示。无确认。 | 142-150（144-145） |

### 3.8 单图动作条

来源：src/components/workspace/canvas-image-actions.tsx（511 行）。只在选中一张图片且工具为选择时显示（workspace-canvas-board.tsx:465-479）。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 作为参考图 | `ui.canvas.action.reference` | 作为参考图 | 把图片加入对话参考。 | 488（487-492） |
| 去背景 | `ui.canvas.action.bgremoval` | 去背景 | 打开去背景对话框。 | 489 |
| 下载图片 | `ui.canvas.action.download` | 下载图片 | 下载原图。 | 490 |
| 删除 | `ui.canvas.action.delete` | 删除 | 删除节点。 | 491 |
| 装饰按钮 | （无） | — | Wand2 图标，没有点击行为。 | 506-508 |

#### 3.8.1 局部重绘对话框（InpaintDialog）

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 标题 | `ui.canvas.inpaint.title` | 局部重绘 | 对话框标题。 | 284 |
| 重绘描述 | `ui.canvas.inpaint.prompt_label` | 重绘描述 | 提示词标签。 | 292（textarea 292-304） |
| 占位文字 | `ui.canvas.inpaint.prompt_placeholder` | 描述涂抹区域应变成什么… | 输入框占位。 | 301 |
| 预计积分 | `ui.canvas.inpaint.cost` | 预计消耗 {credits} 积分 | 显示预计费用。 | 293 |
| 费用未知 | `ui.canvas.inpaint.cost_unknown` | 该模型未声明预计积分，费用以实际结算为准 | 费用未知时显示。 | 296 |
| 已冻结提示 | `ui.canvas.inpaint.confirm_prepared` | 输入已冻结。请核对当前图片、遮罩、提示词、模型和预计费用，再确认执行。 | 准备完成后的说明。 | 305 |
| 取消 | `ui.canvas.inpaint.cancel` | 取消 | 关闭。 | 318 |
| 准备 | `ui.canvas.inpaint.prepare` | 准备本次重绘 | 第一步：调用 prepare，冻结输入。 | 325 |
| 确认执行 | `ui.canvas.inpaint.confirm_execution` | 确认并执行本次重绘 | 第二步：提交执行。 | 325 |
| 失败 | `ui.canvas.inpaint.failed` | 局部重绘提交失败 | Toast 错误。 | 182、270 |
| 关闭 | `ui.dialog.close` | 关闭 | 右上关闭文案。 | 283 |

流程：先 prepare（冻结输入），再 confirm_execution（执行）。提交走 `prepareDirectTask` 与 `submitDirectTask`（128-159）。使用 sessionStorage 保存待处理任务（60-107）。

#### 3.8.2 去背景对话框（BackgroundRemovalDialog）

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 标题 | `ui.canvas.bgremoval.title` | 去除背景 | 对话框标题。 | 418 |
| 无服务 | `ui.canvas.bgremoval.unavailable` | 当前没有可用的去背景服务 | 没有服务时显示。 | 423 |
| 确认说明（有费用） | `ui.canvas.bgremoval.confirm` | 将把所选图片发送到抠图服务，预计消耗 {credits} 积分。是否继续？ | 有预计费用时。 | 440 |
| 确认说明（费用未知） | `ui.canvas.bgremoval.confirm_unknown` | 将把所选图片发送到抠图服务。该服务未声明预计积分，费用以实际结算为准。是否继续？ | 费用未知时。 | 443 |
| 已冻结提示 | `ui.canvas.bgremoval.confirm_prepared` | 输入已冻结。请核对所选图片和预计费用，再确认执行。 | 准备完成后。 | 445 |
| 取消 | `ui.canvas.bgremoval.cancel` | 取消 | 关闭。 | 432、458 |
| 准备 | `ui.canvas.bgremoval.prepare` | 准备去背景 | 第一步。 | 465 |
| 确认执行 | `ui.canvas.bgremoval.confirm_execution` | 确认并执行去背景 | 第二步。 | 465 |
| 失败 | `ui.canvas.bgremoval.failed` | 去背景提交失败 | Toast 错误。 | 404 |
| 关闭 | `ui.dialog.close` | 关闭 | 右上关闭文案。 | 417 |

任务类型 `background-removal` 在组件中硬编码（见第 9 节）。

### 3.9 Agent 对话（AgentDialog）

来源：src/components/workspace/agent-dialog.tsx（1018 行）。

#### 3.9.1 头部与会话

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 标题 | `ui.workspace.agent.title` | AI Agent | 头部标题。 | 214（page.tsx:30） |
| 新建任务 | `ui.workspace.agent.new_task` | 新建任务 | 清空已跟踪任务（`clearTrackedTasks`），新建会话。 | 215；746-748；createTask 726-734（729） |
| 收起 | `ui.workspace.agent.close` | 收起 Agent 对话 | 关闭 Agent 浮层。 | 215、750；749-753 |
| 会话创建失败 | `ui.workspace.agent.session_failed` | 创建 Agent 会话失败 | Toast。 | 220 |
| 加载消息失败 | `ui.workspace.agent.load_failed` | 加载会话消息失败 | Toast。 | 222 |
| 空状态 | `ui.workspace.agent.empty` | 选择生成模型后，描述想创作的内容或上传图片。Agent 会判断输入方式并执行。 | 没有消息时显示。 | 219；783-806 |
| 示例提示词 | `ui.workspace.agent.example_1` … `example_3`；3D 用 `example_3d_1` … `example_3d_3` | 示例文案（例如“一只青铜机械鸟，工作室光效”） | 点示例填入输入框。 | agent-dialog.tsx:783-806（790-791） |
| 当前画布图参考 | `ui.workspace.agent.use_canvas_image` | 将当前画布图片作为本轮参考图 | 复选框，把当前画布图加入本轮参考。 | 224；867-880 |

#### 3.9.2 输入区

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 输入框 | `ui.workspace.agent.placeholder` | 告诉 Agent 你想创作什么… | 多行输入。Enter 发送（938-943）。没有检查输入法组合状态（见第 9 节）。 | 216；928-945 |
| 发送 | `ui.workspace.agent.send` | 发送 | 发送。禁用条件见 1005 行。 | 217；1000-1011 |
| 停止 | `ui.workspace.agent.stop` | 停止 | 调用 `cancelTurn`（989-991），并中止请求（993）。 | 218；978-998 |
| 上传参考图 | `ui.chat.input.upload_title` | 上传参考图片 | 打开隐藏文件选择。 | 890、951-952；file input 916-927；按钮 947-958 |
| 上传数量上限 | `ui.chat.input.upload_limit` | 最多上传 {count} 张图片 | 超出时提示。 | 180；attachFiles 172-210 |
| 附件缩略图 | `ui.chat.input.attachment_alt` | 附件 {index} | 图片替代文字。 | 895 |
| 移除附件 | `ui.chat.input.remove_attachment` | 移除附件 {index} | 悬停时显示的删除按钮。 | 905；889-915 |
| 模型选择 | `ui.workspace.harness.offering` | 模型 | 模型选择器触发器，`data-testid="workspace-model-picker"`。弹层可搜索，支持方向键。 | model-select.tsx:44-82（44、47、56-60、67-80、71、32-42）；agent-dialog 959 |
| 请选模型 | `ui.workspace.harness.choose_model` | 请选择生成模型 | 没选模型时发送被拒。 | model-select.tsx:47；agent-dialog.tsx:467 |
| 参数与模型 | `ui.workspace.harness.settings` | 参数与模型 | 展开或收起参数区（HarnessPanel）。 | 960-976；964-965 |
| 参数加载失败 | `ui.workspace.harness.load_failed` | 加载模型参数失败 | Toast。 | 462 |
| 保存参数失败 | `ui.workspace.harness.save_failed` | 保存参数失败 | Toast（hook 中也有）。 | hooks/use-session-agent-params.ts:89 |

#### 3.9.3 编辑、重发、再生成、删除

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 编辑上一条 | `ui.chat.message.editing` | 正在编辑上一条消息：发送后会替换这一轮的消息和回复 | 编辑提示条。 | 883；881-888 |
| 取消编辑 | `ui.chat.message.edit_cancel` | 取消编辑 | 退出编辑。 | 885 |
| 编辑后重发 | `ui.chat.message.edit_resend` | 编辑后重发 | 把上一条消息放入输入框，发送时替换这一轮（先删后发）。 | chat-message.tsx:504-516（511-512）；agent `editLastMessage` 674-680 |
| 重新发送 | `ui.chat.message.resend` | 重新发送 | 再发一次同样的消息。 | chat-message.tsx:491-503（498-499） |
| 重新生成 | `ui.chat.message.regenerate` | 重新生成 | 替换这一轮后重新发送。 | chat-message.tsx:517-529（524-525）；agent `regenerate` 667-671 |
| 删除消息 | `ui.chat.message.delete` | 删除此消息 | 删除消息。无确认（见第 9 节）。失败提示 `ui.chat.toast.delete_message_retry`。 | chat-message.tsx:530-542（537-538）；agent `removeMessage` 689-705（700） |
| 替换失败 | `ui.chat.toast.turn_replace_failed` | 替换上一轮失败，请重试 | Toast。 | agent-dialog.tsx:521 |
| 操作区 | （无） | — | 消息操作按钮只在悬停时显示（chat-message.tsx:479-544）。 | 479-544 |

#### 3.9.4 发送流程相关提示

| 场景 | Schema key | 中文文案 | 文件:行 |
|---|---|---|---|
| 当前模型不支持该 Skill | `ui.workspace.skills.unavailable` | 当前模型不支持该 Skill | agent-dialog.tsx:489 |
| Skill 加载失败 | `ui.workspace.skills.load_failed` | Skill 加载失败 | agent-dialog.tsx:491 |
| 发送失败 | `ui.workspace.agent.send_failed` | 发送失败 | agent-dialog.tsx:221 |
| 同意结果提交失败 | `ui.workspace.agent.consent_failed` | 提交同意结果失败 | agent-dialog.tsx:223 |
| 切换批准方式失败 | `ui.workspace.approval.change_failed` | 切换批准方式失败，请重试 | agent-dialog.tsx:226 |
| 设定摘要保存失败 | `ui.workspace.notes.save_failed` | 设定摘要保存失败，请重试 | agent-dialog.tsx:227 |
| 选用失败 | `ui.chat.image.pick_failed` | 选用失败，请重试 | agent-dialog.tsx:228 |

发送流程要点（代码）：模型检查（460-470）、Skill 预检（482-498）、claims（501-504）、替换式重发或编辑（515-527）、startTurn（537-550）、SSE 等待（557-574）、出错时恢复（584-587）。

#### 3.9.5 批准方式

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 标签 | `ui.workspace.approval.label` | 批准方式 | 标签。 | approval-mode-switch.tsx；agent-dialog 225 |
| 选项 | Schema `chat.approval_policy.modes`（auto、manual） | 来自 Schema | 单选组，原生按钮实现。默认 manual。 | approval-mode-switch.tsx:14-22、61-86 |
| 会话累计积分 | `ui.workspace.approval.session_credits` | 本会话累计约 {credits} 积分（估算） | 显示估算，`data-testid="approval-session-credits"`。 | approval-mode-switch.tsx:26、87-89 |
| 本步积分 | `ui.workspace.approval.step_credits` | 本步消耗 {credits} 积分 | 显示单步费用。 | approval-mode-switch.tsx:31 |
| 自动批准说明 | `ui.workspace.approval.auto_approved_note` | 已按批准方式自动批准 | 消息中的提示。 | chat-message.tsx:599 |

切换会调用 `changeApprovalMode`（722-724）。

#### 3.9.6 设定摘要（会话备注）

来源：src/components/workspace/session-notes-panel.tsx（124 行）。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 折叠按钮 | `ui.workspace.notes.label` | 设定摘要 | 展开或收起（原生按钮）。 | 67-80（23） |
| 说明 | `ui.workspace.notes.hint` | Agent 每一轮都会参考这份摘要。你可以直接修改；你们敲定新设定时，Agent 也会更新它。 | 说明文字。 | 24 |
| 占位 | `ui.workspace.notes.placeholder` | 例如：主角是戴红围巾的少女；整体水墨风；输出 1024×1024；不要出现文字。 | 文本框占位。 | 25、84-92 |
| 已有设定 | `ui.workspace.notes.filled` | 已有设定 | 状态文字。 | 26 |
| 保存 | `ui.workspace.notes.save` | 保存 | 保存摘要。 | 27、110-117 |
| 还原 | `ui.workspace.notes.revert` | 还原 | 还原为服务器值。 | 28、102-109 |
| 超长 | `ui.workspace.notes.too_long` | 超出上限，请精简后再保存 | 超出字数时显示。 | 29 |
| 计数 | `ui.workspace.notes.counter` | {count} / {maximum} | 字数计数。 | 30 |

`notesDraftState` 在 35-40 行。

#### 3.9.7 同意对话框（ConsentDialog）

来源：src/components/workspace/consent-dialog.tsx（130 行）。blocking 层，无右上关闭按钮（88 行）。Escape 不做决定（82-84）。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 标题 | `ui.workspace.consent.title` | 需要你确认 | 标题。 | 54 |
| 正文 | `ui.workspace.consent.body` | 请核对这次已冻结的模型、输入和预计费用。只有批准这次操作后，Agent 才会提交；拒绝不会执行。 | 正文。 | 55 |
| 批准 | `ui.workspace.consent.approve` | 批准并继续 | 调用决定接口（decide），批准。 | 56、118-125；agent 707-720 |
| 拒绝 | `ui.workspace.consent.deny` | 拒绝 | 调用决定接口，拒绝。 | 57、109-117 |
| 原因 | `ui.workspace.consent.reason` | 原因 | 字段名（已知字段）。 | 25 |
| 预计积分 | `ui.workspace.consent.credits` | 预计消耗积分 | 字段名。 | 26 |
| 来源任务 | `ui.workspace.consent.source_task` | 要处理的模型任务 | 字段名。 | 27 |
| 未知字段 | （无） | — | 未知键原样显示。 | 70-75 |

#### 3.9.8 Skill

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 打开 Skill 选择 | `ui.workspace.skills.title` | 选择 Skill | 打开选择对话框。 | skill-selector.tsx:19、30 |
| 搜索 | `ui.workspace.skills.search` | 搜索 Skill | 搜索框。 | skill-selector.tsx:39 |
| 清除 | `ui.workspace.skills.clear`（按 `text('clear')` 动态取） | — | 清除已选 Skill。 | skill-selector.tsx:32 |
| 预览 | `ui.workspace.skills.preview`（动态） | — | 预览卡片。 | skill-selector.tsx:52 |
| 选择 | `ui.workspace.skills.select` | 选择 | 选中 Skill。 | skill-selector.tsx:52、64 |
| 关闭 | `ui.workspace.history.close` | 关闭历史面板 | 对话框关闭文案复用历史面板的文字。 | skill-selector.tsx:36 |
| 创建 Skill | `ui.workspace.skills.create` | 创建 Skill | 打开创建对话框。 | skill-creator.tsx:61-63 |
| 保存 | （动态 `ui.workspace.skills.*`） | — | 调用 `createPersonalSkill`。 | skill-creator.tsx:38-58 |
| 保存成功 | `ui.workspace.skills.saved`（动态） | — | Toast 成功。 | skill-creator.tsx:52 |
| 内容不合法 | `ui.workspace.skills.content_invalid`（动态） | — | 表单错误。 | skill-creator.tsx:42 |

Skill 文案统一用动态 key：`schemaText(schema, \`ui.workspace.skills.${key}\`)`（skill-selector.tsx:19；skill-creator.tsx:20）。Schema 里有 29 个 `ui.workspace.skills.*` key。

#### 3.9.9 请求参数面板（HarnessPanel）

来源：src/components/workspace/harness-panel.tsx（111 行）。

| 操作 | Schema key | 中文文案 | 文件:行 |
|---|---|---|---|
| 标题 | `ui.workspace.harness.title` | 请求参数 | 57 |
| 说明 | `ui.workspace.harness.agent_decides` | 你选择模型与可选参数，Agent 判断输入方式并执行任务。 | 58 |
| 加载中 | `ui.workspace.harness.loading` | 参数加载中… | 59 |
| 空态 | `ui.workspace.harness.empty` | 选择模型后可设置参数 | 60 |
| 会话未就绪 | `ui.workspace.harness.session_pending` | Agent 会话就绪后可编辑参数 | 61 |
| 同步 | `ui.workspace.harness.synced` | 与 Agent 共享 · 双向同步 | 62 |

- 生效配置区（75-83）读取 `agent_configuration.ui` 的键，并列出原始工具名和描述（81）。
- 参数控件由 `SchemaParameters` 渲染。

#### 3.9.10 聊天消息中的图片与模型结果

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 灯箱关闭 | `ui.chat.lightbox.close` | 关闭预览 | 关闭灯箱。键 Escape。 | chat-message.tsx:93、107-124 |
| 上一张 | `ui.chat.lightbox.previous` | 上一张 | 键 ArrowLeft；悬停时显示箭头（330、340）。 | 94、254 |
| 下一张 | `ui.chat.lightbox.next` | 下一张 | 键 ArrowRight。 | 95、255 |
| 图片序号 | `ui.chat.lightbox.image_alt` | 图片 {current} / {total} | 替代文字。 | 186 |
| 跳到第几张 | `ui.chat.lightbox.jump` | 跳到第 {index} 张 | 跳转按钮。 | 205、359 |
| 发送到画布 | `ui.chat.image.send_to_canvas` | 发送到画布 | 把图加入画布。不切换域（见第 6、9 节）。 | 256、276-286；按钮 396-407（PictureInPicture2） |
| 仅结果可发送 | `ui.chat.image.send_to_canvas_unavailable` | 仅生成结果可发送到画布 | 禁用说明。 | 257 |
| 选用 | `ui.chat.image.pick` | 选用 | 选这张图作为后续步骤的准绳。 | 258、373-395 |
| 已选用 | `ui.chat.image.picked` | 已选用 | 选中后的状态。 | 259 |
| 选用说明 | `ui.chat.image.pick_hint` | 选用这张：后续步骤默认以它为准 | 提示。 | 260 |
| 仅结果可选用 | `ui.chat.image.pick_unavailable` | 仅生成结果可选用 | 禁用说明。 | 261 |
| 在 3D 查看器中查看 | `ui.chat.model.open_in_viewer` | 在 3D 查看器中查看 | 选中模型，但不切换域（见第 6、9 节）。 | 428-438（437） |
| 可用格式 | `ui.chat.model.formats` | 可用格式 | 格式面板标题。 | 439-441（440） |
| 生成中 | `ui.chat.message.generating` | 正在生成 | 状态气泡。 | 566 |
| 处理中 | `ui.chat.message.processing` | 处理中 | 状态气泡。 | 630 |
| 上传图 / 生成图 | `ui.chat.message.uploaded_image` / `generated_image` | 上传图片 / 生成图片 | 图片标签。 | 586-587 |

### 3.10 历史抽屉（HistoryDrawer）

来源：src/components/workspace/history-drawer.tsx（244 行）。实际是 Dialog（127-242）。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 标题 | `ui.workspace.history.all` | 历史任务 | 对话框标题。 | 57 |
| 空列表 | `ui.workspace.history.empty` | 暂无匹配的任务会话 | 无结果时。 | 58 |
| 关闭 | `ui.workspace.history.close` | 关闭历史面板 | 右上关闭文案。 | 59、133 |
| 返回 | `ui.workspace.history.back` | 返回任务列表 | 从详情返回列表。 | 60 |
| 搜索 | `ui.workspace.history.search_sessions` | 按任务名称查找 | 搜索框（225）。 | 61、224-239 |
| 会话项 | （无） | — | 点击进入详情。 | 231 |
| 加载更多会话 | `ui.workspace.history.load_more` | 加载更多版本 | 会话列表的“加载更多”按钮（237）。文案是“版本”，不对（见第 9 节）。 | 63、237 |
| 继续这个任务 | `ui.workspace.history.continue_task` | 继续这个任务 | 切换到该会话。如果当前会话不同，清空已跟踪任务（144）。 | 62、144 |
| 加载失败 | `ui.workspace.history.load_failed` | 加载任务会话失败 | 错误。 | 64 |
| 没有产出 | `ui.workspace.history.output_empty` | 这个任务还没有产出 | 空态。 | 65 |
| 当前任务 | `ui.workspace.history.current` | 当前任务 | 标记。 | 67 |
| 版本 | `ui.workspace.history.version` | 版本 | 版本标签。 | 68 |
| 版本链 | `ui.workspace.history.view_chain` | 版本链 | 切换标签。 | 69、146-152 |
| 全部产出 | `ui.workspace.history.view_list` | 全部产出 | 切换标签。 | 70、146-152 |
| 加载更多版本 | `ui.workspace.history.load_more` | 加载更多版本 | 版本列表加载更多。 | 63、219 |
| 资产点击 | （无） | — | 图片在画布中打开；非图片或模型在新标签页打开。 | 189-215（202-204） |
| 已加入对话参考 | `ui.workspace.lineage.sent_chat` | 已加入对话参考 | 提示。 | 71 |

#### 3.10.1 版本链（LineageView）

来源：src/components/workspace/lineage-view.tsx（125 行）。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 版本名 | `ui.workspace.lineage.version_template` | {domain} v{number} | 版本标题。 | 23 |
| 来源未知 | `ui.workspace.lineage.unknown_parent` | 来源未知 | 来源不可得。 | 32 |
| 来源 | `ui.workspace.lineage.from_template` | 来自 {parent} | 来源说明。 | 34 |
| 3D 截图来源 | `ui.workspace.lineage.from_capture_template` | 来自 {parent} 的 3D 视图截图 | 截图来源。 | 34 |
| 依据 1 | `ui.workspace.lineage.evidence_same_bytes` | 依据：文件内容与它完全相同 | 来源依据。 | 37 |
| 依据 2 | `ui.workspace.lineage.evidence_recorded` | 依据：任务记录的来源 | 来源依据。 | 37 |
| 加载失败 | `ui.workspace.lineage.load_failed` | 版本链加载失败 | 错误。 | 53 |
| 空 | `ui.workspace.lineage.empty` | 这个任务还没有可显示的版本 | 空态。 | 68 |
| 截断 | `ui.workspace.lineage.truncated` | 更早的版本没有载入，部分来源可能显示为未知 | 版本链太长时的提示。 | 72 |
| 发到画布 | `ui.workspace.lineage.send_canvas` | 发到画布 | 每个版本都有。 | 104-107（106） |
| 作为参考发到对话 | `ui.workspace.lineage.send_chat` | 作为参考发到对话 | 只对图片。 | 108-113（111） |

### 3.11 任务托盘（TaskTray）

来源：src/components/workspace/task-tray.tsx（564 行）。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 托盘标题 | `ui.workspace.tasks.title` | 任务进度 | Popover 标题。 | 85 |
| 空态 | `ui.workspace.tasks.empty` | Agent 创建的任务会显示在这里 | 无任务时。 | 86 |
| 进度 | `ui.workspace.tasks.progress` | 进度 | 进度标签。 | 87 |
| 下载 | `ui.workspace.tasks.download` | 下载 | 下载链接。 | 88、487-495 |
| 查看 | `ui.workspace.tasks.view` | 查看 | 选中资产，并切换域（`viewAsset` 206-227）。 | 89、473-483 |
| 附加产物生成中 | `ui.workspace.tasks.resources_pending` | 附加产物生成中… | 任务还有附件未完成。 | 90 |
| 任务失败 | `ui.workspace.tasks.error_title` | 任务失败 | 错误标题。 | 91 |
| 可重试 | `ui.workspace.tasks.retryable` | 可重试 | 标记。 | 92 |
| 从列表移除 | `ui.workspace.tasks.dismiss` | 从列表移除 | 移除已结束任务。 | 93、401-413 |
| 任务不存在 | `ui.workspace.tasks.not_found` | 任务不存在或已删除 | 404 时显示。 | 94 |
| 取消任务 | `ui.workspace.tasks.cancel` | 取消任务 | 取消。先 `window.confirm`（231），再 POST `task_cancel`。 | 95、387-400；cancelTask 229-257 |
| 取消确认 | `ui.workspace.tasks.cancel_confirm` | 取消后本地停止跟进；已提交到上游的任务可能仍会执行并计费。确定取消吗？ | `window.confirm` 文案。 | 96、231 |
| 取消失败 | `ui.workspace.tasks.cancel_failed` | 取消任务失败，请稍后重试 | Toast。 | 97 |
| 重试 | `ui.workspace.tasks.retry` | 重试 | 重试按钮，`data-testid="workspace-task-retry"`。 | 98、445-461；retryTask 259-303 |
| 确认重试 | `ui.workspace.tasks.retry_confirm` | 确认重试 | 手动批准时的第二步。 | 99 |
| 重试需确认 | `ui.workspace.tasks.retry_awaiting` | 重试需要你确认后才会提交，可能产生费用。 | 手动批准时的说明。 | 100 |
| 已重新提交 | `ui.workspace.tasks.retry_started` | 已重新提交 | Toast。 | 101 |
| 重试失败 | `ui.workspace.tasks.retry_failed` | 重试失败，请稍后再试 | Toast。 | 102 |
| 展开 | `ui.workspace.tasks.expand` | 展开任务详情 | 展开为 Dialog（Maximize2，547-549）。 | 104、547-549、556-561 |
| 收起详情 | `ui.workspace.tasks.close` | 关闭任务详情 | 关闭 Dialog。 | 105 |
| Agent 准备中 | `ui.workspace.tasks.agent_preparing` | Agent 正在准备 | 状态。 | 106 |
| Skill 步骤失败 | `ui.workspace.tasks.skill_runs_load_failed` | Skill 步骤加载失败，请稍后重试 | 错误。 | 107 |
| 流程记录 | `ui.workspace.tasks.skill_checkpoint` | 流程记录 | 标题。 | 328 |
| 模型归档与格式 | （见 3.12） | — | 任务内显示。 | 500-505 |

任务轮询：3000 ms（Schema）。刷新后从服务器重新载入未结束的任务（`src/lib/workspace/task-hydration.ts`），只接受 Schema 里的 `task_states`。

### 3.12 模型交付与格式

来源：src/components/workspace/model-formats.tsx（285 行）。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 下载全部（ZIP） | `ui.workspace.model_formats.download_all` | 下载全部（ZIP） | 下载归档。`data-testid="model-archive-link"`。 | 50（ModelArchiveLink 38-53） |
| 标题 | `ui.workspace.model_formats.title` | 交付格式 | 格式面板标题。 | 256 |
| 读取中 | `ui.workspace.model_formats.loading` | 正在读取格式清单… | 加载中。 | 262 |
| 读取失败 | `ui.workspace.model_formats.load_failed` | 读取格式清单失败，请稍后重试。 | 错误。 | 260 |
| 积分 | `ui.workspace.model_formats.credits` | 约 {credits} 积分 | 补齐费用。 | 95 |
| 转换方 | `ui.workspace.model_formats.via` | 由 {provider} 转换 | 来源服务商。 | 102 |
| 下载 | `ui.workspace.model_formats.download` | 下载 | 单个格式下载。 | 113-114 |
| 补齐格式 | `ui.workspace.model_formats.complete` | 补齐格式（约 {credits} 积分） | 收费。点击直接 POST，无确认（见第 9 节）。 | 124-135（133）；POST 216-234 |
| 补齐中 | `ui.workspace.model_formats.completing` | 正在补齐格式… | 进行中。 | 139 |
| 补齐失败 | `ui.workspace.model_formats.complete_failed` | 补齐格式失败，请重试。 | Toast。 | 223-248 |
| 转换暂停 | `ui.workspace.model_formats.plan_paused` | 格式转换已暂停，请在处理面板中查看并继续。 | 状态。 | 144 |
| 转换失败 | `ui.workspace.model_formats.plan_failed` | 有格式转换未能完成。 | 状态。 | 149 |
| 重试失败转换 | `ui.workspace.model_formats.retry_named` / `retry_charged_named` / `refetch_named` | 重试 {format} / 重试 {format}（再次计费）/ 免费取回 {format} | 每个失败的转换一组（`failed_step`），按钮写出所属格式；POST 重试或免费取回（#330 第二轮）。 | 438 |
| 状态 | `ui.workspace.model_formats.status.*`（4 个） | — | 行状态。 | 73-122 |

- 折叠按钮（269-281）。
- 有转换进行中时，每 3000 ms 轮询。

### 3.13 3D 查看器

来源：src/components/viewer/model-viewer-3d.tsx（182 行）。canvas-stage.tsx 里挂载。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 替代文字 | `ui.workspace.canvas.model_alt` | 3D 模型预览 | 从 Schema 取。组件默认值是英文 `3D Model`（model-viewer-3d.tsx:66）。 | canvas-stage.tsx:132 |
| 拖动旋转、缩放 | （无） | — | `camera-controls`。 | model-viewer-3d.tsx:151-181 |
| 自动旋转 | （无） | — | 默认开启（`autoRotate`）。 | canvas-stage.tsx:134 |
| 截图到 2D | `ui.workspace.canvas.capture_to_2d` | 高清截图送入 2D 画布 | 按钮（右下角）。上传截图，记录截图，加入画布，然后切换到图片域。 | model-viewer-3d.tsx:174-179；canvas-stage.tsx:60-119（118） |
| 截图中 | `ui.workspace.canvas.capturing` | 正在生成高清截图… | 按钮加载中。 | canvas-stage.tsx:137 |
| 截图失败 | `ui.workspace.canvas.capture_failed` | 高清截图未保存，请重试；若持续失败，请检查设备图形内存或上传限制 | Toast。 | canvas-stage.tsx:140 |
| 空态 | `ui.workspace.canvas.empty_model` | 生成的 3D 模型会显示在这里，也可以从历史面板选择查看 | 没有模型时。 | canvas-stage.tsx:155 |
| 加载中（画布） | `ui.workspace.harness.loading` | 参数加载中… | 画布懒加载的占位文字（见第 9 节）。 | canvas-stage.tsx:121 |

截图条件：有 `assetId` 时才显示截图按钮和回调（canvas-stage.tsx:139）。

### 3.14 3D 处理面板（ModelProcessingPanel）

来源：src/components/workspace/model-processing-panel.tsx（379 行）。ViewportDialog 为外壳（245 行）。

| 操作 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 打开 | `ui.workspace.processing.open` | 处理模型 | 打开面板（canvas-stage.tsx:146）。切换模型时自动关闭（canvas-stage.tsx:58）。 | canvas-stage.tsx:146-147 |
| 标题 | `ui.workspace.processing.title` | 处理当前模型 | 面板标题。 | 247 |
| 关闭 | `ui.workspace.processing.close` | 关闭处理面板 | 关闭按钮（右上）。 | 245、248 |
| 读取失败 | `ui.workspace.processing.load_failed` | 读取模型或处理能力失败，请重试。 | 错误。 | 254 |
| 当前来源 | `ui.workspace.processing.source` | 当前来源 | 来源信息标签。 | 257-261 |
| 会话不匹配 | `ui.workspace.processing.session_mismatch` | 请先切换到该模型所属的创作任务。 | 错误。 | 259 |
| 资产不可用 | `ui.workspace.processing.asset_unavailable` | 当前模型没有已保存、可用的资产。 | 错误。 | 260 |
| 步骤选择 | `ui.workspace.processing.plan_steps` | 选择处理步骤 | 步骤标题（复选框：重拓扑、重贴图、骨骼绑定、动画生成、抠图、格式转换）。 | 293；303-307 |
| 处理步骤与模型 | `ui.workspace.processing.target` | 处理步骤与模型 | 供给项（offering）原生下拉。 | 309；310-320 |
| 输入方式 | `ui.workspace.processing.route` | 输入方式 | 路由标签。 | 323 |
| 同服务商任务 | `ui.workspace.processing.task_route` | 同服务商任务 | 路由选项。 | 325 |
| 本地模型资产 | `ui.workspace.processing.asset_route` | 本地模型资产 | 路由选项。 | 326 |
| 不可用 | `ui.workspace.processing.unavailable` | 不可用 | 路由选项。 | 327 |
| 参数 | （来自 Schema） | — | 参数表单。 | 333-342 |
| 输入规则 | `ui.workspace.processing.input_rule_violation` | 以下输入需要且仅需要填写一项 | 校验。 | 344 |
| 动作 | `ui.workspace.processing.choose_action` | 请选择动作 | 原生下拉。 | 347-357（352） |
| 不兼容 | `ui.workspace.processing.plan_incompatible` | 所选步骤没有可用的输入路径，请调整步骤或模型。 | 错误。 | 362 |
| 预计积分 | `ui.workspace.harness.credits` | 预计积分 | 单项费用。 | 331 |
| 费用未知 | `ui.workspace.harness.credits_unknown` | 该服务未声明预计积分，费用以实际结算为准 | 费用未知时。 | 332、369 |
| 预计总积分 | `ui.workspace.processing.plan_cost` | 预计总积分 | 总费用。 | 368 |
| 附加选项说明 | `ui.workspace.processing.plan_cost_extra` | 区间的上限包含你选择的附加选项产生的额外积分；确认后以实际选项的预计积分为准。 | 说明。 | 371 |
| 提交 | `ui.workspace.processing.plan_confirm` | 确认步骤与费用，开始处理 | 提交（POST `processing_plans`，带 Idempotency-Key，161）。 | 373-376、375 |
| 提交中 | `ui.workspace.processing.submitting` | 正在提交… | 加载中。 | 375 |
| 准备步骤 | `ui.workspace.processing.plan_prepare_step` | 准备本步骤确认 | 分步的第一步。 | 287 |
| 确认步骤 | `ui.workspace.processing.plan_confirm_step` | 确认本步骤并执行 | 分步的第二步。 | 287 |
| 继续 | `ui.workspace.processing.plan_resume` | 确认当前费用，继续处理 | 暂停后继续。 | 290；178-225（grant 186-188，decision 205） |
| 核对上次提交 | `ui.workspace.processing.plan_reconcile` | 核对上次提交结果 | 提交结果未知时。 | 289 |
| 重试 | `ui.workspace.processing.plan_retry` | 确认费用，重试失败步骤 | 重试（280-283）；227-243。 | 282 |
| 费用未知（计划） | `ui.workspace.processing.plan_credits_unknown` | 费用以实际结算为准 | 计划费用未知。 | 282、290 |
| 供给项变化 | `ui.workspace.processing.plan_offering_changed` | 处理供给项的版本或费用已变化，请按当前费用重新确认。 | 提交前检测到变化。 | 149、213、234 |
| 提交失败 | `ui.workspace.processing.submit_failed` | 提交失败，请重试。 | Toast 错误。 | 164、223、241 |

能力名（来自 Schema `capabilities`）：文生 3D、图生 3D、文生图、图生图、重拓扑、重贴图、骨骼绑定、动画生成、抠图、格式转换。

### 3.15 账户页（我的资源）

来源：src/app/account/page.tsx（185 行）。文案 key 前缀是 `ui.account.*`（29 个）。

| 操作 | Schema key | 文件:行 |
|---|---|---|
| 刷新 | `ui.account.refresh`（显示“刷新”） | 84-86 |
| 用量日期范围 | 日期输入（`ui.account.*`，动态取） | 93-98 |
| 用量指标 | 指标卡 | 103-109 |
| 按类型用量 | 用量范围标签，显示按类型的分解 | 107 |
| 上传列表 | 列表项，带“查看”链接 | 118-131 |
| 上传分页 | 上一页、下一页 | 132-140（136-137） |
| 任务列表 | 列表项，带下载链接 | 148-171 |
| 加载更多任务 | 按钮 | 172 |

账户页的文案 key 是动态拼接（`ui.account.${key}`，40 行）。

### 3.16 登录页

来源：src/app/login/page.tsx（210 行）。

| 操作 | 来源 | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 标题 | Schema `authentication.login.title` | Plinth（当时为旧组织名） | 页面标题。 | 116 |
| 说明 | Schema `authentication.login.description` | 多模型创作与资产平台 | 页面说明。 | 119 |
| 用户名 | 表单字段 | — | 自动聚焦。 | 133-169 |
| 显示 / 隐藏密码 | Action | — | 切换密码可见。 | 157-167 |
| 登录 | Schema `authentication.login.submit` | 登录 | 提交。 | 181-183 |
| 加载中 | `login.loading_label`（Schema） | — | 加载状态。 | 92-94 |
| Logo | 样式 | — | 渐变 `from-primary to-info`。 | 112 |

### 3.17 错误与 404

| 操作 | Schema key | 中文文案 | 文件:行 |
|---|---|---|---|
| 错误边界标题 | `ui.boundary.error.title` | 出错了 | app-error-boundary.tsx:57 |
| 错误说明 | `ui.boundary.error.description` | 发生了意外错误，请重试或联系管理员。 | 60 |
| 重试 | `ui.boundary.error.retry` | 重试 | 62-63 |
| 404 标题 | `ui.boundary.not_found.title` | 页面未找到 | not-found.tsx:14 |
| 404 说明 | `ui.boundary.not_found.description` | 抱歉，您访问的页面不存在或已被移除。 | 17 |
| 返回首页 | `ui.boundary.not_found.home` | 返回首页 | 20 |

### 3.18 任务完成提醒（后台）

来源：src/hooks/use-task-completion-notice.ts（90 行）。

| 场景 | Schema key | 中文文案 | 行为 | 文件:行 |
|---|---|---|---|---|
| 完成 | `ui.workspace.notice.done` | 任务已完成 | 系统通知标题。 | 55 |
| 失败 | `ui.workspace.notice.failed` | 任务失败 | 系统通知标题。 | 55 |
| 标签页标题 | `ui.workspace.notice.tab_title` | （{count}）有任务已结束 · {title} | 页面在后台时，标题加计数。回到前台时恢复。 | 70 |
| 通知正文兜底 | `ui.workspace.notice.body_fallback` | {capability} | 任务没有提示词时用能力名。 | 75 |

- 只在桌面端提醒（`DESKTOP_QUERY` 为宽度 ≥1024px、精细指针、可悬停）。定义在 `src/lib/workspace/completion-notice.ts:67`。
- 第一次开始任务时请求一次通知权限（57-66）。已问过的标记存在 localStorage 的 `task-notice-permission-asked`。

---

## 4. 状态与持久化

### 4.1 Zustand 与其他状态

| Store / 状态 | 文件 | 持久化位置 | 内容 | 备注 |
|---|---|---|---|---|
| workspace store | src/store/workspace-store.ts（339 行） | localStorage `art-ai-workspace-prefs`（version 1，319-326） | 只持久化 `domain` 和 `modelByDomain`（partialize 165-167） | 其余字段在内存中：`trackedTasks`、`canvasSelection`、`canvasReference`、`pendingChatAttachments`、`attachmentSources`、`historyOpen`、`agentOpen`。刷新后重置。 |
| canvas store | src/store/canvas-store.ts（1093 行） | localStorage `canvas-store`（1078） | `nodesBySession`、`viewportBySession`、`activeTool`、`activeShapeType`、`lastStyle`、`projectBySession`、`_savedHashBySession`、`_savedRevisionBySession`、`bgStyleBySession`（partialize 1079-1090） | 画布节点与项目副本都在 localStorage 里。与服务器双写（见第 9 节）。 |
| chat store | src/store/chat-store.ts（274 行） | 无 persist | `sessions`、`activeSessionId`、`activeSessionRevision`、`messagesBySession`、`streamingBuffers`（84-129） | 只在内存中。消息来自服务器。 |
| preference store | src/store/preference-store.ts（51 行） | localStorage `art-ai-preferences`（当时为旧前缀）（10 行） | `hideFailures`，默认 `true`（44 行） | 只有 `header.tsx` 读取（141-156）。Header 只在 `/account` 和 `/admin/*` 显示。 |
| Skill 选择 | agent-dialog.tsx:83 | React 状态 | 每个会话的 Skill | 刷新后重置。 |
| 会话草稿 | src/hooks/use-session-state.ts | [未核对] | 输入框、编辑状态、附件（按会话） | 存储位置未核对。 |
| 待处理直接任务 | src/components/workspace/canvas-image-actions.tsx:60-107 | sessionStorage `art-ai:pending-direct-task:<session>:<hash>` | 待确认的局部重绘或去背景输入 | 只在当前标签页。 |
| 任务完成提醒权限 | src/hooks/use-task-completion-notice.ts:22 | localStorage `task-notice-permission-asked` | 是否问过权限 | |
| 余额 | `useBalance` | SWR，每 60 s 刷新（Schema 中的刷新间隔，60000 ms） | 账户余额 | |
| 缓存状态 | Header | SWR，每 5 s 刷新 | 缓存中的资源 | 只在 `/account`、`/admin/*` 的 Header 显示。 |
| 任务 | task store / hydration | 轮询，每 3000 ms | 任务列表 | 只接受 Schema `task_states`。 |

### 4.2 服务器状态（主存储）

- 聊天会话、消息、SSE 流。
- Agent 参数、绑定、同意请求。
- 批准策略（`chat.approval_policy`）、会话备注。
- 画布项目与节点。使用版本号（revision）做 CAS，防止冲突。自动保存延迟 1500 ms（Schema `canvas.frontend.auto_save_delay_milliseconds`）。409 冲突时重新加载，并提示 `ui.workspace.canvas.conflict`（board 184-251）。
- 任务（创建、取消、重试、列表）。
- 处理计划（processing plans）与步骤。
- 模型交付格式、血缘（lineage）。
- 账户用量、余额。
- 缓存状态。

### 4.3 浏览器与服务器的分工

| 数据 | 服务器 | 浏览器 | 问题 |
|---|---|---|---|
| 画布节点 | 有（项目和节点，带 revision） | 有（`canvas-store.nodesBySession`，localStorage） | 双份。浏览器副本可能与服务器不同步，容量也有限 [推断]。 |
| 画布背景样式 | 项目字段 `bgStyle` 可读 | 有（`bgStyleBySession`，localStorage） | 只读。前端没有任何地方把它写回服务器（见第 9 节）。 |
| 工作台域和模型选择 | 会话读取返回该会话各域最近一次发送的模型（`generationModels`，#318） | 有（`art-ai-workspace-prefs`） | 选择属于本机；本机某域为空时打开会话按服务端记录补齐（仍可用才补，不覆盖本机选择）。 |
| 隐藏失败任务 | 无 | 有（`art-ai-preferences`（当时为旧前缀）） | 只有 Header 读取。 |

---

## 5. 视觉系统

### 5.1 Token 与全局 CSS

- 文件：src/app/globals.css（180 行）。
- `@theme inline` 定义 token（11-78 行）。
- `--font-sans` 是 PingFang SC 字体栈（globals.css 中）。没有加载 Web 字体。[推断] `web/index.html` 与 `src` 里没有 Google Fonts 链接。[未完全核对]
- 圆角 token：`sm`、`md`、`lg`、`xl`（封闭）。
- z-index token：`--z-index-*`（69-77 行）。
- `:root` 暗色调色板（83-140 行）：background、surface、panel、panel-raised、canvas、primary（暖金 `oklch(0.827 0.141 92)`）、send（紫 `oklch(0.518 0.229 280)`，仅发送按钮）、destructive、success、warning、info、border（10% 透明度）。
- base 层（142-159 行）。
- shimmer 关键帧（162-180 行）。

### 5.2 暗色与浅色

- 只有暗色。
- `:root` 就是暗色。`color-scheme: dark`。
- `web/src/main.tsx:25` 强制加 `dark` 类。
- 没有浅色主题，也没有切换。

### 5.3 字体

- 无限定的字体加载。使用系统 PingFang SC 栈（globals.css）。

### 5.4 间距与尺寸

- 使用 Tailwind 4 默认间距刻度。没有自定义间距 token [推断]。
- 圆角只用 `sm`、`md`、`lg`、`xl`。

### 5.5 UI 组件（src/components/ui）

| 组件 | 文件 | 说明 |
|---|---|---|
| Surface | surface.tsx（82 行） | 容器。`as` 可换标签。 |
| Button / Action | button.tsx（89 行） | `Action` 是 `Button` 的别名（50-100）。 |
| Dialog | dialog.tsx | `DialogContent` 有 `showCloseButton`、`closeLabel`、`layer`（47-96）。Base UI。 |
| Dropdown | dropdown-menu.tsx | Base UI。 |
| Select | select.tsx | Base UI。页面里有原生 select 也有这个组件。 |
| Popover、Tooltip、Tabs、Switch、Checkbox、Input、Badge、Progress、Separator | 各自的文件 | Base UI。 |
| Slider | slider.tsx | Radix（`@radix-ui/react-slider`）。唯一的 Radix 组件。 |
| Overlay | overlay.ts（30 行） | `OverlayLayer` 类型，`floatingLayer` 映射（8-12），`useAnchoredPositioner`（14-16），`anchoredPopup` 样式（18-29）。 |
| ViewportDialog | viewport-dialog.tsx（63 行） | 右侧面板。遮罩 `absolute inset-0 z-scrim bg-black/50`（48）。面板 `absolute inset-y-0 right-0 z-modal w-[min(100%,420px)]`（57）。Escape 关闭（33-36）。 |
| Field | （ui kit 角色） | 表单字段包装。 |
| BrowserLink | browser-link.tsx | 链接组件。 |

按钮变体：default、outline、secondary、raised、media、ghost、destructive、link。
按钮尺寸：default、xs、sm、lg、icon、icon-xs、icon-sm、icon-lg。
`loading` 属性显示加载中。

Surface 变体：plain、base、panel、inset、card、canvas、muted、soft、raised、frosted、toolbar、danger、warning、info、success、message-user、message-agent。
Surface 圆角：sm、md、lg、xl、none。
Surface 内边距：none 到 xl。
Surface 阴影：none、sm、lg、xl。

### 5.6 图标

- 使用 lucide-react。
- 常见图标：Box（3D 或 logo）、Image（图片域）、History、Pencil（改名）、LogOut、Settings2（管理后台）、Coins（余额）、ListTodo（任务）、Maximize2（展开）、Wand2（生成与装饰）、Minus（适应屏幕，见第 9 节）、Square（重置视图，见第 9 节）。

### 5.7 设计规则测试

- `src/__tests__/design-token-discipline.test.ts`。规则：
  - 不能在 OWNED_ROOTS 里使用调色板工具类（如 `bg-yellow-100`）。
  - 圆角只能用封闭刻度。
  - 只能用命名的 overlay 层。
  - 不能有固定的侧栏或自建遮罩（存活名单之外）。
  - 画布渲染器不能有字面 hex 颜色。
- [未核对] 本次没有运行测试。

### 5.8 与规则不一致的地方（观察）

- 顶栏 tab、审批切换、设定摘要折叠按钮、处理面板的 select 用的是原生元素，不是 UI kit 组件（见第 9 节）。
- Schema 的 `task_states` 里有调色板工具类（如 `bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30`）。规则测试只查代码，不查 Schema 内容。[推断] 如果这些类被渲染，就会绕过 token。[未核对渲染位置]
- 3D 处理面板的遮罩（`bg-black/50`）是自建的，文件在 viewport-dialog.tsx:48。它是否在存活名单里 [未核对]。

---

## 6. 模式切换（图片 / 3D）

### 6.1 状态

- 域存在 workspace store 的 `domain` 中：`'image' | 'model-3d'`。默认 `image`（store 182）。
- `domain` 持久化到 localStorage（`art-ai-workspace-prefs`）。
- 渲染分支在 `CanvasStage`：
  - `domain === 'image'` 渲染 `WorkspaceCanvasBoard`（懒加载，canvas-stage.tsx:124）。
  - `domain === 'model-3d'` 渲染 3D 查看器（canvas-stage.tsx:126-150）。
  - 3D 域下没有选中模型时显示空态（152-157）。

### 6.2 切换入口

| 入口 | 行为 | 文件:行 |
|---|---|---|
| 顶栏“图片创作”“3D 模型” tab | `setDomain(tab.id)` | workspace-topbar.tsx:112 |
| 任务托盘“查看”（`viewAsset`） | 设置选中资产，并切换域 | task-tray.tsx:206-227 |
| 任务完成后自动切换 | `applyTaskDetail` 根据结果切换域 | workspace-store.ts:284；`domainOfResult` 135-139 |
| 3D 截图送入 2D 画布 | 切换到图片域 | canvas-stage.tsx:118 |
| 聊天里“在 3D 查看器中查看” | 设置选中模型，**不切换域** | chat-message.tsx:428-438；model-results.ts:33-38 |
| 聊天里“发送到画布” | 加入画布，**不切换域** | chat-message.tsx:276-286 |
| 历史抽屉“发到画布” | [未核对] | lineage-view.tsx:104-107 |

### 6.3 选中状态

- `canvasSelection` 是内存中的状态（不持久化）。
- `setCanvasSelection` 也会清空 `pendingPreviewTaskId`（store）。
- 选中模型时，画布区根据 `selection.kind === 'model'` 显示 3D 查看器，而不是按域。[推断] 所以：如果当前域是 `image`，即使选中模型，3D 查看器也不会出现。这与 6.2 中“不切换域”的入口相连。

### 6.4 观察到的问题（代码推断，未在浏览器验证）

- 聊天里的 3D 结果不切换域。用户在图片域时点“在 3D 查看器中查看”，画布不会切到 3D。
- 聊天里的图片“发送到画布”不切换域。用户在 3D 域时，图片加入画布，但看不到。

---

## 7. 管理后台（简述）

| 页面 | 文件 | 行数 | 说明（由文件名与导航推断，未逐页核对） |
|---|---|---|---|
| 数据概览 | src/app/admin/page.tsx | 418 | 数据汇总页。 |
| 用户管理 | src/app/admin/users/page.tsx | 508 | 用户列表与操作。关闭按钮用 `ui.dialog.close`（387、481）。 |
| 操作日志 | src/components/admin/audit-log-panel.tsx；页面 src/app/admin/audit/page.tsx | 557 | 日志列表。详情对话框关闭按钮（153）。 |
| 生成记录 | src/components/admin/generations-panel.tsx；页面 src/app/admin/generations/page.tsx | 917 | 生成记录列表。详情关闭按钮（231）。 |
| 资产审查 | src/app/admin/user-assets/page.tsx | 981 | 用户资产审查。是最大的页面文件之一。 |
| Agent 管理 | src/app/admin/agent-configuration/page.tsx | 107 | Agent 配置。 |
| 管理布局 | src/app/admin/layout.tsx | 45 | 子导航（24-41），外面套 `MainLayout`。 |

- 管理页不在工作台里，走 `MainLayout`（侧栏 + Header）。
- 非管理员访问时渲染 NotFound（router.tsx:58-62）。
- 管理接口另有 RBAC 兜底（router.tsx 注释）。

---

## 8. 大组件行数

行数来自 `wc -l`（本次盘点时）。

| 文件 | 行数 |
|---|---|
| src/lib/product-schema.ts | 1703 |
| src/components/canvas/canvas-board.tsx | 1367 |
| src/store/canvas-store.ts | 1093 |
| src/components/workspace/agent-dialog.tsx | 1018 |
| src/app/admin/user-assets/page.tsx | 981 |
| src/components/admin/generations-panel.tsx | 917 |
| src/lib/chat/browser-client.ts | 916 |
| src/components/chat/chat-message.tsx | 656 |
| src/components/workspace/task-tray.tsx | 564 |
| src/components/admin/audit-log-panel.tsx | 557 |
| src/components/workspace/workspace-canvas-board.tsx | 550 |
| src/components/canvas/canvas-toolbar.tsx | 527 |
| src/components/workspace/canvas-image-actions.tsx | 511 |
| src/app/admin/users/page.tsx | 508 |
| src/app/admin/page.tsx | 418 |
| src/components/workspace/model-processing-panel.tsx | 379 |
| src/store/workspace-store.ts | 339 |
| src/components/workspace/model-formats.tsx | 285 |
| src/components/form/image-upload.tsx | 279 |
| src/components/workspace/workspace-topbar.tsx | 260 |
| src/components/workspace/history-drawer.tsx | 244 |
| src/components/layout/header.tsx | 235 |
| src/app/login/page.tsx | 210 |
| src/app/account/page.tsx | 185 |
| src/components/viewer/model-viewer-3d.tsx | 182 |
| src/components/layout/sidebar.tsx | 137 |
| src/components/workspace/lineage-view.tsx | 125 |
| src/components/workspace/session-notes-panel.tsx | 124 |
| src/components/workspace/consent-dialog.tsx | 130 |
| src/components/canvas/layers-panel.tsx | 159 |
| src/components/workspace/canvas-stage.tsx | 158 |
| src/app/workspace/page.tsx | 111 |
| src/components/workspace/harness-panel.tsx | 111 |
| src/app/admin/agent-configuration/page.tsx | 107 |
| src/components/workspace/workspace-balance.tsx | 94 |
| src/components/ui/button.tsx | 89 |
| src/components/workspace/approval-mode-switch.tsx | 92 |
| src/components/workspace/model-select.tsx | 85 |
| src/components/workspace/skill-creator.tsx | 83 |
| src/components/ui/surface.tsx | 82 |
| src/hooks/use-task-completion-notice.ts | 90 |
| src/components/workspace/skill-selector.tsx | 71 |
| src/components/layout/main-layout.tsx | 55 |
| src/components/common/app-error-boundary.tsx | 67 |
| src/components/ui/viewport-dialog.tsx | 63 |
| src/store/chat-store.ts | 274 |
| src/store/preference-store.ts | 51 |
| src/app/admin/layout.tsx | 45 |
| src/app/not-found.tsx | 24 |
| src/app/globals.css | 154 |
| src/lib/chat/model-results.ts | 41 |

---

## 9. 奇怪的地方（按类别）

### 9.1 功能与逻辑

1. 聊天里的 3D 结果不切换域。`chat-message.tsx:428-438` 调用 `setCanvasSelection`，但不调用 `setDomain`。`model-results.ts:33-38` 返回的 selection 没有 domain。画布只按 `domain` 分支（`canvas-stage.tsx:124-126`）。用户在图片域时，3D 查看器不会出现。[推断，未在浏览器验证]
2. 聊天里“发送到画布”不切换域（`chat-message.tsx:276-286`）。[推断]
3. 背景样式只写本地。`canvas-toolbar.tsx:190` 调用 `setBgStyleForSession`，只写 canvas store。`src/lib/canvas/browser-client.ts:208-217` 的 `updateProject` 接受 `bgStyle`，但 `src` 中没有任何调用者（本次 grep 确认）。服务器只读（`workspace-canvas-board.tsx:174`）。所以背景样式只留在本机 localStorage。
4. 画布节点双份存储。`canvas-store.ts:1079-1090` 把 `nodesBySession` 和 `projectBySession` 持久化到 localStorage，同时服务器也有。[推断] 容量有限，且两份可能不同步。
5. 历史抽屉的会话列表“加载更多”（`history-drawer.tsx:237`）用的是 `ui.workspace.history.load_more`，文案是“加载更多版本”（`history-drawer.tsx:63`）。会话列表显示“版本”，文案不对。
6. 退出登录失败时，Toast 显示的是“退出登录”（`workspace-topbar.tsx:220-222`，用 `logoutLabel`）。Header 用的是“登出失败”（`header.tsx:119`）。两处文案不一致。
7. 窄屏（<640px）没有会话标题，也不能改名。顶栏的标题与改名区域整体是 `hidden sm:flex`（topbar 130-131）。
8. 隐藏失败任务开关只在 `header.tsx:141-156` 中读取。Header 只挂在 `/account` 和 `/admin/*`（`router.tsx:46、52-56`）。工作台没有这个开关。该偏好对工作台没有效果 [推断]。
9. 删除消息无确认（`chat-message.tsx:530-542`；`agent-dialog.tsx:689-705`）。
10. 补齐格式直接收费，无确认（`model-formats.tsx:124-135`，POST 216-234）。
11. 取消任务使用原生 `window.confirm`（`task-tray.tsx:231`）。与 UI kit 的对话框不一致。
12. 工作台画布加载占位文字是“参数加载中…”（`canvas-stage.tsx:121`，key `ui.workspace.harness.loading`）。画布加载时文字不准确。
13. Agent 对话的 Enter 发送，没有检查输入法组合状态（`agent-dialog.tsx:938-943`）。[推断] 中文输入法选字时可能误发。[未在浏览器验证]
14. 桌面 Agent 默认打开（`page.tsx:34-41`，宽度 ≥1024px）。移动端默认关闭。
15. Agent 触发按钮的 `aria-expanded` 写死为 `false`（`page.tsx:97`）。
16. Escape 只在移动端关闭 Agent（`page.tsx:72-76`）。桌面端 Escape 不关闭。
17. 同意对话框：无关闭按钮，Escape 不做决定（`consent-dialog.tsx:82-88`）。这是设计选择，但用户只能点“批准”或“拒绝”。
18. 装饰按钮：`canvas-image-actions.tsx:506-508` 的 Wand2 按钮没有点击行为。
19. 画布上 `r` 键是矩形快捷键（`canvas-board.tsx:621-704`），但工具栏不显示这个快捷键。
20. 撤销栈、`bringToFront`、`updateNodeStyle` 等在 canvas store 里，但没有 UI 调用者（[未全量核对]）。
21. `chat/browser-client.ts:127` 的 `deleteSession` 存在，没有 UI 调用者。

### 9.2 硬编码与文案

22. 硬编码的全角标点（4 处）：`workspace-balance.tsx:69`、`:73`（全角冒号）；`task-tray.tsx:440`、`:525`。
23. 硬编码的任务类型 `background-removal`：`canvas-image-actions.tsx` 的 BackgroundRemovalDialog（约 335-473 行）。
24. 硬编码的任务类型 `image-to-image`：`workspace-canvas-board.tsx:81`。
25. 硬编码的 taskId 前缀 `canvas:`：`workspace-canvas-board.tsx:276、323、396`。
26. 硬编码的英文默认替代文字 `3D Model`：`model-viewer-3d.tsx:66`。
27. Header 的菜单按钮文案是英文 `Toggle menu`（`header.tsx:129-135`；Schema `ui.header.menu_toggle` 的值就是英文）。
28. 技能选择器的关闭按钮复用了历史面板的文字（`skill-selector.tsx:36`，`ui.workspace.history.close`）。
29. 历史抽屉的名字叫 drawer，实际是居中 Dialog（`history-drawer.tsx:127-242`）。

### 9.3 设计体系偏离

30. 顶栏模式切换用原生 `button`（`workspace-topbar.tsx:109-126`）。
31. 审批切换用原生按钮做单选（`approval-mode-switch.tsx:61-86`）。
32. 设定摘要折叠按钮是原生按钮（`session-notes-panel.tsx:67-80`）。
33. 处理面板用原生 `select`（`model-processing-panel.tsx:310-320`、`:347-357`）。
34. 草稿与 Schema 里的调色板类（`task_states`）。见第 5.8 节。
35. 工具栏“适应屏幕”用 Minus 图标，“重置视图”用 Square 图标（`canvas-toolbar.tsx:443-454`）。图标含义与行为不对应。
36. 笔刷大小按钮的数值是线宽乘 3（`canvas-toolbar.tsx:410、414`）。显示的线宽与实际笔刷大小不一致 [推断]。
37. 裁剪最小尺寸 8px 硬编码（`canvas-board.tsx:1147`）。
38. 文字编辑框固定在 `left 16 / top 16`（`canvas-board.tsx:1327-1342`）。不跟随节点 [推断]。

### 9.4 可访问性与可见性

39. 图层面板的五个操作按钮只在悬停时显示（`layers-panel.tsx:109、118、128、138、147`，`opacity-0 group-hover`）。触屏设备不可用 [推断]。键盘焦点是否可见 [未核对]。
40. 聊天消息的操作区只在悬停时显示（`chat-message.tsx:479-544`）。
41. 附件的移除按钮只在悬停时显示（`agent-dialog.tsx:889-915`）。
42. 聊天图片轮播的箭头只在悬停时显示（`chat-message.tsx:330、340`）。
43. 顶栏的 tab 在窄屏只显示图标（`workspace-topbar.tsx:123`）。有 `aria-label`。
44. 顶栏历史按钮窄屏只显示图标（`workspace-topbar.tsx:161`）。
45. 侧栏在移动端默认打开（`main-layout.tsx:28`，`useState(true)`）。只影响 `/account` 和 `/admin/*`。

### 9.5 层级与重叠

46. 桌面 Agent 浮层（`lg:z-float`）与 3D 处理面板（`z-modal` + `bg-black/50` 遮罩）可能叠在一起。遮罩可能盖住 Agent。与 `docs/ui-kit-framework.md` 的说法（非模态，Agent 仍可用）不一致。[推断，未在浏览器验证]
47. 画布上多处使用 `top-3`：蒙版条（canvas-board 1174-1284）、裁剪条（1285-1326）、图层面板（`layers-panel.tsx:62`，`right-3`）、单图动作条（`canvas-image-actions.tsx:475-511`，`right-3`）、参数条（`canvas-surface-params.tsx:59`，`left-1/2`）。有可能重叠 [推断]。

### 9.6 其他

48. 头像字为 `ui.dashboard.fallback_initial` 的 `·`（`workspace-topbar.tsx:67`）。无名字时显示一个点。
49. 3D 空态文字提到“历史面板”（`canvas-stage.tsx:155`）。历史面板是 Dialog，不是抽屉。
50. 审批切换的积分是估算（`approval-mode-switch.tsx:26`）。

---

## 附录 A：Schema 文案 key 使用清单（自动生成）

来源：对 `src/components`、`src/app`、`src/hooks` 中的字面量 `'ui.*'` 做 grep，并从 Schema `application.content.messages` 取中文。
- 共 369 处引用，309 个唯一 key。
- Skill、账户、处理面板等模块用动态拼接 key（`ui.workspace.skills.${key}`、`ui.account.${key}` 等），不在本表内。
- 每行一处引用。同一 key 可能出现多次。

| 文件:行 | Schema key | 中文文案 |
|---|---|---|
| app/admin/users/page.tsx:387 | `ui.dialog.close` | 关闭 |
| app/admin/users/page.tsx:481 | `ui.dialog.close` | 关闭 |
| app/not-found.tsx:14 | `ui.boundary.not_found.title` | 页面未找到 |
| app/not-found.tsx:17 | `ui.boundary.not_found.description` | 抱歉，您访问的页面不存在或已被移除。 |
| app/not-found.tsx:20 | `ui.boundary.not_found.home` | 返回首页 |
| app/workspace/page.tsx:30 | `ui.workspace.agent.title` | AI Agent |
| app/workspace/page.tsx:31 | `ui.workspace.agent.close` | 收起 Agent 对话 |
| components/admin/audit-log-panel.tsx:153 | `ui.dialog.close` | 关闭 |
| components/admin/generations-panel.tsx:231 | `ui.dialog.close` | 关闭 |
| components/canvas/canvas-board.tsx:397 | `ui.workspace.canvas.upload_failed` | 上传图片失败，请重试 |
| components/canvas/canvas-board.tsx:433 | `ui.canvas.node.default_text` | 双击编辑文字 |
| components/canvas/canvas-board.tsx:733 | `ui.canvas.board.empty` | 在 Agent 对话中描述你想生成的图片，发送后图片会出现在这里 |
| components/canvas/canvas-board.tsx:1177 | `ui.canvas.mask.hint` | 涂抹需要重绘的区域，提交后由图生图模型处理 |
| components/canvas/canvas-board.tsx:1259 | `ui.workspace.canvas.upload_failed` | 上传图片失败，请重试 |
| components/canvas/canvas-board.tsx:1268 | `ui.canvas.mask.submit` | 确认区域并提交 |
| components/canvas/canvas-board.tsx:1280 | `ui.canvas.mask.cancel` | 退出蒙版 |
| components/canvas/canvas-board.tsx:1314 | `ui.canvas.crop.apply` | 应用裁剪 |
| components/canvas/canvas-board.tsx:1323 | `ui.canvas.crop.cancel` | 取消裁剪 |
| components/canvas/canvas-toolbar.tsx:201 | `ui.canvas.toolbar.rectangle` | 矩形 |
| components/canvas/canvas-toolbar.tsx:202 | `ui.canvas.toolbar.arrow` | 箭头 |
| components/canvas/canvas-toolbar.tsx:203 | `ui.canvas.toolbar.line` | 直线 |
| components/canvas/canvas-toolbar.tsx:204 | `ui.canvas.toolbar.ellipse` | 椭圆 |
| components/canvas/canvas-toolbar.tsx:219 | `ui.canvas.toolbar.select_tooltip` | 选择工具 ({key}) |
| components/canvas/canvas-toolbar.tsx:220 | `ui.canvas.toolbar.select_key` | V |
| components/canvas/canvas-toolbar.tsx:228 | `ui.canvas.toolbar.hand_tooltip` | 平移画布 ({key}) |
| components/canvas/canvas-toolbar.tsx:229 | `ui.canvas.toolbar.hand_key` | H |
| components/canvas/canvas-toolbar.tsx:237 | `ui.canvas.toolbar.upload` | 上传图片 |
| components/canvas/canvas-toolbar.tsx:242 | `ui.canvas.toolbar.shapes` | 形状工具 |
| components/canvas/canvas-toolbar.tsx:271 | `ui.canvas.toolbar.text` | 文字工具 (T) |
| components/canvas/canvas-toolbar.tsx:278 | `ui.canvas.toolbar.pencil` | 矢量铅笔 (P) |
| components/canvas/canvas-toolbar.tsx:285 | `ui.canvas.toolbar.brush_tooltip` | 像素画笔 ({key}) |
| components/canvas/canvas-toolbar.tsx:286 | `ui.canvas.toolbar.brush_key` | B |
| components/canvas/canvas-toolbar.tsx:294 | `ui.canvas.toolbar.eraser_tooltip` | 橡皮 ({key}) |
| components/canvas/canvas-toolbar.tsx:295 | `ui.canvas.toolbar.eraser_key` | E |
| components/canvas/canvas-toolbar.tsx:303 | `ui.canvas.toolbar.crop_tooltip` | 裁剪 ({key}) |
| components/canvas/canvas-toolbar.tsx:304 | `ui.canvas.toolbar.crop_key` | K |
| components/canvas/canvas-toolbar.tsx:314 | `ui.canvas.toolbar.mask_tooltip` | 局部重绘蒙版 ({key}) |
| components/canvas/canvas-toolbar.tsx:315 | `ui.canvas.toolbar.mask_key` | M |
| components/canvas/canvas-toolbar.tsx:317 | `ui.canvas.mask.unsupported_tooltip` | 当前模型不支持局部重绘，点击切换 |
| components/canvas/canvas-toolbar.tsx:335 | `ui.canvas.toolbar.style` | 样式 |
| components/canvas/canvas-toolbar.tsx:344 | `ui.canvas.style.line_color` | 线色 |
| components/canvas/canvas-toolbar.tsx:364 | `ui.canvas.style.fill` | 填充 |
| components/canvas/canvas-toolbar.tsx:378 | `ui.canvas.style.no_fill` | 无填充 |
| components/canvas/canvas-toolbar.tsx:398 | `ui.canvas.toolbar.brush_size` | 笔刷大小 |
| components/canvas/canvas-toolbar.tsx:399 | `ui.canvas.style.line_width` | 线宽 |
| components/canvas/canvas-toolbar.tsx:427 | `ui.canvas.toolbar.undo` | 撤销 |
| components/canvas/canvas-toolbar.tsx:430 | `ui.canvas.toolbar.redo` | 重做 |
| components/canvas/canvas-toolbar.tsx:434 | `ui.canvas.toolbar.zoom_out` | 缩小 |
| components/canvas/canvas-toolbar.tsx:440 | `ui.canvas.toolbar.zoom_in` | 放大 |
| components/canvas/canvas-toolbar.tsx:444 | `ui.canvas.toolbar.fit` | 适应屏幕 |
| components/canvas/canvas-toolbar.tsx:452 | `ui.canvas.toolbar.reset` | 重置视图 |
| components/canvas/canvas-toolbar.tsx:458 | `ui.canvas.toolbar.background` | 背景样式 |
| components/canvas/canvas-toolbar.tsx:502 | `ui.canvas.toolbar.layers` | 图层面板 |
| components/canvas/canvas-toolbar.tsx:510 | `ui.canvas.toolbar.export` | 导出画板为 PNG |
| components/canvas/canvas-toolbar.tsx:516 | `ui.canvas.action.send_to_chat` | 发送到对话 |
| components/canvas/canvas-toolbar.tsx:522 | `ui.canvas.toolbar.generate` | 图片生成 (I) |
| components/canvas/layers-panel.tsx:64 | `ui.canvas.layers.title` | 图层 |
| components/canvas/layers-panel.tsx:68 | `ui.canvas.layers.close` | 关闭图层面板 |
| components/canvas/layers-panel.tsx:76 | `ui.canvas.layers.empty` | 暂无图层 |
| components/canvas/layers-panel.tsx:106 | `ui.canvas.layers.visibility_show` | 显示图层 |
| components/canvas/layers-panel.tsx:106 | `ui.canvas.layers.visibility_hide` | 隐藏图层 |
| components/canvas/layers-panel.tsx:107 | `ui.canvas.layers.visibility_show` | 显示图层 |
| components/canvas/layers-panel.tsx:107 | `ui.canvas.layers.visibility_hide` | 隐藏图层 |
| components/canvas/layers-panel.tsx:115 | `ui.canvas.layers.unlock` | 解锁图层 |
| components/canvas/layers-panel.tsx:115 | `ui.canvas.layers.lock` | 锁定图层 |
| components/canvas/layers-panel.tsx:116 | `ui.canvas.layers.unlock` | 解锁图层 |
| components/canvas/layers-panel.tsx:116 | `ui.canvas.layers.lock` | 锁定图层 |
| components/canvas/layers-panel.tsx:124 | `ui.canvas.layers.up` | 上移一层 |
| components/canvas/layers-panel.tsx:125 | `ui.canvas.layers.up` | 上移一层 |
| components/canvas/layers-panel.tsx:134 | `ui.canvas.layers.down` | 下移一层 |
| components/canvas/layers-panel.tsx:135 | `ui.canvas.layers.down` | 下移一层 |
| components/canvas/layers-panel.tsx:144 | `ui.canvas.action.delete` | 删除 |
| components/canvas/layers-panel.tsx:145 | `ui.canvas.action.delete` | 删除 |
| components/chat/chat-message.tsx:93 | `ui.chat.lightbox.close` | 关闭预览 |
| components/chat/chat-message.tsx:94 | `ui.chat.lightbox.previous` | 上一张 |
| components/chat/chat-message.tsx:95 | `ui.chat.lightbox.next` | 下一张 |
| components/chat/chat-message.tsx:186 | `ui.chat.lightbox.image_alt` | 图片 {current} / {total} |
| components/chat/chat-message.tsx:205 | `ui.chat.lightbox.jump` | 跳到第 {index} 张 |
| components/chat/chat-message.tsx:254 | `ui.chat.lightbox.previous` | 上一张 |
| components/chat/chat-message.tsx:255 | `ui.chat.lightbox.next` | 下一张 |
| components/chat/chat-message.tsx:256 | `ui.chat.image.send_to_canvas` | 发送到画布 |
| components/chat/chat-message.tsx:257 | `ui.chat.image.send_to_canvas_unavailable` | 仅生成结果可发送到画布 |
| components/chat/chat-message.tsx:258 | `ui.chat.image.pick` | 选用 |
| components/chat/chat-message.tsx:259 | `ui.chat.image.picked` | 已选用 |
| components/chat/chat-message.tsx:260 | `ui.chat.image.pick_hint` | 选用这张：后续步骤默认以它为准 |
| components/chat/chat-message.tsx:261 | `ui.chat.image.pick_unavailable` | 仅生成结果可选用 |
| components/chat/chat-message.tsx:359 | `ui.chat.lightbox.jump` | 跳到第 {index} 张 |
| components/chat/chat-message.tsx:437 | `ui.chat.model.open_in_viewer` | 在 3D 查看器中查看 |
| components/chat/chat-message.tsx:440 | `ui.chat.model.formats` | 可用格式 |
| components/chat/chat-message.tsx:498 | `ui.chat.message.resend` | 重新发送 |
| components/chat/chat-message.tsx:499 | `ui.chat.message.resend` | 重新发送 |
| components/chat/chat-message.tsx:511 | `ui.chat.message.edit_resend` | 编辑后重发 |
| components/chat/chat-message.tsx:512 | `ui.chat.message.edit_resend` | 编辑后重发 |
| components/chat/chat-message.tsx:524 | `ui.chat.message.regenerate` | 重新生成 |
| components/chat/chat-message.tsx:525 | `ui.chat.message.regenerate` | 重新生成 |
| components/chat/chat-message.tsx:537 | `ui.chat.message.delete` | 删除此消息 |
| components/chat/chat-message.tsx:538 | `ui.chat.message.delete` | 删除此消息 |
| components/chat/chat-message.tsx:566 | `ui.chat.message.generating` | 正在生成 |
| components/chat/chat-message.tsx:586 | `ui.chat.message.uploaded_image` | 上传图片 |
| components/chat/chat-message.tsx:587 | `ui.chat.message.generated_image` | 生成图片 |
| components/chat/chat-message.tsx:599 | `ui.workspace.approval.auto_approved_note` | 已按批准方式自动批准 |
| components/chat/chat-message.tsx:630 | `ui.chat.message.processing` | 处理中 |
| components/common/app-error-boundary.tsx:57 | `ui.boundary.error.title` | 出错了 |
| components/common/app-error-boundary.tsx:60 | `ui.boundary.error.description` | 发生了意外错误，请重试或联系管理员。 |
| components/common/app-error-boundary.tsx:63 | `ui.boundary.error.retry` | 重试 |
| components/common/thumbnail-cell.tsx:39 | `ui.common.preview` | 预览 |
| components/form/image-upload.tsx:85 | `ui.form.image_upload.maximum` | 最多只能上传 {count} 张图片 |
| components/form/image-upload.tsx:195 | `ui.form.image_upload.release` | 松开以上传 |
| components/form/image-upload.tsx:196 | `ui.form.image_upload.drop` | 点击或拖拽图片到此处 |
| components/form/image-upload.tsx:199 | `ui.form.image_upload.supported` | 支持 JPG、PNG、WebP、GIF、BMP，BMP 自动转为 PNG，最大 {size}MB |
| components/form/image-upload.tsx:204 | `ui.form.image_upload.maximum_hint` | ，最多 {count} 张 |
| components/form/image-upload.tsx:207 | `ui.form.image_upload.multiple_hint` | ，可多选 |
| components/form/image-upload.tsx:240 | `ui.form.image_upload.preview_alt` | 图片预览 {index} |
| components/form/image-upload.tsx:256 | `ui.form.image_upload.remove` | 移除图片 {index} |
| components/form/image-upload.tsx:274 | `ui.form.image_upload.multi_role_hint` | 第1张为主图，后续图为风格参考。可在提示词中用「第1张图」「第2张图」明确指定角色。 |
| components/layout/header.tsx:34 | `ui.header.unknown_asset` | 资源 |
| components/layout/header.tsx:55 | `ui.header.unknown_task` | 未知任务 |
| components/layout/header.tsx:57 | `ui.header.cache_task_id` | #{id} |
| components/layout/header.tsx:60 | `ui.header.unknown_task_id` | #未知 |
| components/layout/header.tsx:61 | `ui.header.cache_item` | [{capability} {task}] {asset} |
| components/layout/header.tsx:74 | `ui.header.cache_tooltip` | 正在缓存 {count} 个资源 |
| components/layout/header.tsx:76 | `ui.header.cache_tooltip_details` | 正在缓存 {count} 个资源： |
| components/layout/header.tsx:114 | `ui.header.logout_success` | 已退出登录 |
| components/layout/header.tsx:119 | `ui.header.logout_failed` | 登出失败 |
| components/layout/header.tsx:132 | `ui.header.menu_toggle` | Toggle menu |
| components/layout/header.tsx:146 | `ui.header.failures_show_title` | 点击显示失败任务 |
| components/layout/header.tsx:147 | `ui.header.failures_hide_title` | 点击隐藏失败任务 |
| components/layout/header.tsx:153 | `ui.header.failures_hidden` | 已隐藏失败 |
| components/layout/header.tsx:154 | `ui.header.failures_visible` | 显示失败 |
| components/layout/header.tsx:170 | `ui.header.cache_count` | 缓存中 {count} |
| components/layout/header.tsx:203 | `ui.dashboard.balance_unavailable` | — |
| components/layout/header.tsx:221 | `ui.header.default_user_initial` | U |
| components/layout/header.tsx:228 | `ui.header.logout` | 退出登录 |
| components/layout/sidebar.tsx:71 | `ui.dialog.close` | 关闭 |
| components/workspace/agent-dialog.tsx:180 | `ui.chat.input.upload_limit` | 最多上传 {count} 张图片 |
| components/workspace/agent-dialog.tsx:214 | `ui.workspace.agent.title` | AI Agent |
| components/workspace/agent-dialog.tsx:215 | `ui.workspace.agent.new_task` | 新建任务 |
| components/workspace/agent-dialog.tsx:216 | `ui.workspace.agent.placeholder` | 告诉 Agent 你想创作什么… |
| components/workspace/agent-dialog.tsx:217 | `ui.workspace.agent.send` | 发送 |
| components/workspace/agent-dialog.tsx:218 | `ui.workspace.agent.stop` | 停止 |
| components/workspace/agent-dialog.tsx:219 | `ui.workspace.agent.empty` | 选择生成模型后，描述想创作的内容或上传图片。Agent 会判断输入方式并执行。 |
| components/workspace/agent-dialog.tsx:220 | `ui.workspace.agent.session_failed` | 创建 Agent 会话失败 |
| components/workspace/agent-dialog.tsx:221 | `ui.workspace.agent.send_failed` | 发送失败 |
| components/workspace/agent-dialog.tsx:222 | `ui.workspace.agent.load_failed` | 加载会话消息失败 |
| components/workspace/agent-dialog.tsx:223 | `ui.workspace.agent.consent_failed` | 提交同意结果失败 |
| components/workspace/agent-dialog.tsx:224 | `ui.workspace.agent.use_canvas_image` | 将当前画布图片作为本轮参考图 |
| components/workspace/agent-dialog.tsx:225 | `ui.workspace.approval.label` | 批准方式 |
| components/workspace/agent-dialog.tsx:226 | `ui.workspace.approval.change_failed` | 切换批准方式失败，请重试 |
| components/workspace/agent-dialog.tsx:227 | `ui.workspace.notes.save_failed` | 设定摘要保存失败，请重试 |
| components/workspace/agent-dialog.tsx:228 | `ui.chat.image.pick_failed` | 选用失败，请重试 |
| components/workspace/agent-dialog.tsx:462 | `ui.workspace.harness.load_failed` | 加载模型参数失败 |
| components/workspace/agent-dialog.tsx:467 | `ui.workspace.harness.choose_model` | 请选择生成模型 |
| components/workspace/agent-dialog.tsx:489 | `ui.workspace.skills.unavailable` | 当前模型不支持该 Skill |
| components/workspace/agent-dialog.tsx:491 | `ui.workspace.skills.load_failed` | Skill 加载失败 |
| components/workspace/agent-dialog.tsx:521 | `ui.chat.toast.turn_replace_failed` | 替换上一轮失败，请重试 |
| components/workspace/agent-dialog.tsx:700 | `ui.chat.toast.delete_message_retry` | 删除消息失败，请重试 |
| components/workspace/agent-dialog.tsx:750 | `ui.workspace.agent.close` | 收起 Agent 对话 |
| components/workspace/agent-dialog.tsx:750 | `ui.workspace.agent.close` | 收起 Agent 对话 |
| components/workspace/agent-dialog.tsx:883 | `ui.chat.message.editing` | 正在编辑上一条消息：发送后会替换这一轮的消息和回复 |
| components/workspace/agent-dialog.tsx:885 | `ui.chat.message.edit_cancel` | 取消编辑 |
| components/workspace/agent-dialog.tsx:890 | `ui.chat.input.upload_title` | 上传参考图片 |
| components/workspace/agent-dialog.tsx:895 | `ui.chat.input.attachment_alt` | 附件 {index} |
| components/workspace/agent-dialog.tsx:905 | `ui.chat.input.remove_attachment` | 移除附件 {index} |
| components/workspace/agent-dialog.tsx:951 | `ui.chat.input.upload_title` | 上传参考图片 |
| components/workspace/agent-dialog.tsx:952 | `ui.chat.input.upload_title` | 上传参考图片 |
| components/workspace/agent-dialog.tsx:964 | `ui.workspace.harness.settings` | 参数与模型 |
| components/workspace/agent-dialog.tsx:965 | `ui.workspace.harness.settings` | 参数与模型 |
| components/workspace/approval-mode-switch.tsx:26 | `ui.workspace.approval.session_credits` | 本会话累计约 {credits} 积分（估算） |
| components/workspace/approval-mode-switch.tsx:31 | `ui.workspace.approval.step_credits` | 本步消耗 {credits} 积分 |
| components/workspace/canvas-image-actions.tsx:182 | `ui.canvas.inpaint.failed` | 局部重绘提交失败 |
| components/workspace/canvas-image-actions.tsx:270 | `ui.canvas.inpaint.failed` | 局部重绘提交失败 |
| components/workspace/canvas-image-actions.tsx:283 | `ui.dialog.close` | 关闭 |
| components/workspace/canvas-image-actions.tsx:284 | `ui.canvas.inpaint.title` | 局部重绘 |
| components/workspace/canvas-image-actions.tsx:288 | `ui.canvas.mask.unavailable` | 当前模型不支持局部重绘，请选择支持蒙版的模型 |
| components/workspace/canvas-image-actions.tsx:292 | `ui.canvas.inpaint.prompt_label` | 重绘描述 |
| components/workspace/canvas-image-actions.tsx:293 | `ui.canvas.inpaint.cost` | 预计消耗 {credits} 积分 |
| components/workspace/canvas-image-actions.tsx:296 | `ui.canvas.inpaint.cost_unknown` | 该模型未声明预计积分，费用以实际结算为准 |
| components/workspace/canvas-image-actions.tsx:301 | `ui.canvas.inpaint.prompt_placeholder` | 描述涂抹区域应变成什么… |
| components/workspace/canvas-image-actions.tsx:305 | `ui.canvas.inpaint.confirm_prepared` | 输入已冻结。请核对当前图片、遮罩、提示词、模型和预计费用，再确认执行。 |
| components/workspace/canvas-image-actions.tsx:318 | `ui.canvas.inpaint.cancel` | 取消 |
| components/workspace/canvas-image-actions.tsx:325 | `ui.canvas.inpaint.confirm_execution` | 确认并执行本次重绘 |
| components/workspace/canvas-image-actions.tsx:325 | `ui.canvas.inpaint.prepare` | 准备本次重绘 |
| components/workspace/canvas-image-actions.tsx:404 | `ui.canvas.bgremoval.failed` | 去背景提交失败 |
| components/workspace/canvas-image-actions.tsx:417 | `ui.dialog.close` | 关闭 |
| components/workspace/canvas-image-actions.tsx:418 | `ui.canvas.bgremoval.title` | 去除背景 |
| components/workspace/canvas-image-actions.tsx:423 | `ui.canvas.bgremoval.unavailable` | 当前没有可用的去背景服务 |
| components/workspace/canvas-image-actions.tsx:432 | `ui.canvas.bgremoval.cancel` | 取消 |
| components/workspace/canvas-image-actions.tsx:440 | `ui.canvas.bgremoval.confirm` | 将把所选图片发送到抠图服务，预计消耗 {credits} 积分。是否继续？ |
| components/workspace/canvas-image-actions.tsx:443 | `ui.canvas.bgremoval.confirm_unknown` | 将把所选图片发送到抠图服务。该服务未声明预计积分，费用以实际结算为准。是否继续？ |
| components/workspace/canvas-image-actions.tsx:445 | `ui.canvas.bgremoval.confirm_prepared` | 输入已冻结。请核对所选图片和预计费用，再确认执行。 |
| components/workspace/canvas-image-actions.tsx:458 | `ui.canvas.bgremoval.cancel` | 取消 |
| components/workspace/canvas-image-actions.tsx:465 | `ui.canvas.bgremoval.confirm_execution` | 确认并执行去背景 |
| components/workspace/canvas-image-actions.tsx:465 | `ui.canvas.bgremoval.prepare` | 准备去背景 |
| components/workspace/canvas-image-actions.tsx:488 | `ui.canvas.action.reference` | 作为参考图 |
| components/workspace/canvas-image-actions.tsx:489 | `ui.canvas.action.bgremoval` | 去背景 |
| components/workspace/canvas-image-actions.tsx:490 | `ui.canvas.action.download` | 下载图片 |
| components/workspace/canvas-image-actions.tsx:491 | `ui.canvas.action.delete` | 删除 |
| components/workspace/canvas-stage.tsx:121 | `ui.workspace.harness.loading` | 参数加载中… |
| components/workspace/canvas-stage.tsx:122 | `ui.workspace.harness.loading` | 参数加载中… |
| components/workspace/canvas-stage.tsx:132 | `ui.workspace.canvas.model_alt` | 3D 模型预览 |
| components/workspace/canvas-stage.tsx:136 | `ui.workspace.canvas.capture_to_2d` | 高清截图送入 2D 画布 |
| components/workspace/canvas-stage.tsx:137 | `ui.workspace.canvas.capturing` | 正在生成高清截图… |
| components/workspace/canvas-stage.tsx:140 | `ui.workspace.canvas.capture_failed` | 高清截图未保存，请重试；若持续失败，请检查设备图形内存或上传限制 |
| components/workspace/canvas-stage.tsx:146 | `ui.workspace.processing.open` | 处理模型 |
| components/workspace/canvas-stage.tsx:155 | `ui.workspace.canvas.empty_model` | 生成的 3D 模型会显示在这里，也可以从历史面板选择查看 |
| components/workspace/consent-dialog.tsx:25 | `ui.workspace.consent.reason` | 原因 |
| components/workspace/consent-dialog.tsx:26 | `ui.workspace.consent.credits` | 预计消耗积分 |
| components/workspace/consent-dialog.tsx:27 | `ui.workspace.consent.source_task` | 要处理的模型任务 |
| components/workspace/consent-dialog.tsx:54 | `ui.workspace.consent.title` | 需要你确认 |
| components/workspace/consent-dialog.tsx:55 | `ui.workspace.consent.body` | 请核对这次已冻结的模型、输入和预计费用。只有批准这次操作后，Agent 才会提交；拒绝不会执行。 |
| components/workspace/consent-dialog.tsx:56 | `ui.workspace.consent.approve` | 批准并继续 |
| components/workspace/consent-dialog.tsx:57 | `ui.workspace.consent.deny` | 拒绝 |
| components/workspace/harness-panel.tsx:57 | `ui.workspace.harness.title` | 请求参数 |
| components/workspace/harness-panel.tsx:58 | `ui.workspace.harness.agent_decides` | 你选择模型与可选参数，Agent 判断输入方式并执行任务。 |
| components/workspace/harness-panel.tsx:59 | `ui.workspace.harness.loading` | 参数加载中… |
| components/workspace/harness-panel.tsx:60 | `ui.workspace.harness.empty` | 选择模型后可设置参数 |
| components/workspace/harness-panel.tsx:61 | `ui.workspace.harness.session_pending` | Agent 会话就绪后可编辑参数 |
| components/workspace/harness-panel.tsx:62 | `ui.workspace.harness.synced` | 与 Agent 共享 · 双向同步 |
| components/workspace/history-drawer.tsx:57 | `ui.workspace.history.all` | 历史任务 |
| components/workspace/history-drawer.tsx:58 | `ui.workspace.history.empty` | 暂无匹配的任务会话 |
| components/workspace/history-drawer.tsx:59 | `ui.workspace.history.close` | 关闭历史面板 |
| components/workspace/history-drawer.tsx:60 | `ui.workspace.history.back` | 返回任务列表 |
| components/workspace/history-drawer.tsx:61 | `ui.workspace.history.search_sessions` | 按任务名称查找 |
| components/workspace/history-drawer.tsx:62 | `ui.workspace.history.continue_task` | 继续这个任务 |
| components/workspace/history-drawer.tsx:63 | `ui.workspace.history.load_more` | 加载更多版本 |
| components/workspace/history-drawer.tsx:64 | `ui.workspace.history.load_failed` | 加载任务会话失败 |
| components/workspace/history-drawer.tsx:65 | `ui.workspace.history.output_empty` | 这个任务还没有产出 |
| components/workspace/history-drawer.tsx:66 | `ui.workspace.harness.loading` | 参数加载中… |
| components/workspace/history-drawer.tsx:67 | `ui.workspace.history.current` | 当前任务 |
| components/workspace/history-drawer.tsx:68 | `ui.workspace.history.version` | 版本 |
| components/workspace/history-drawer.tsx:69 | `ui.workspace.history.view_chain` | 版本链 |
| components/workspace/history-drawer.tsx:70 | `ui.workspace.history.view_list` | 全部产出 |
| components/workspace/history-drawer.tsx:71 | `ui.workspace.lineage.sent_chat` | 已加入对话参考 |
| components/workspace/history-drawer.tsx:133 | `ui.dialog.close` | 关闭 |
| components/workspace/lineage-view.tsx:23 | `ui.workspace.lineage.version_template` | {domain} v{number} |
| components/workspace/lineage-view.tsx:32 | `ui.workspace.lineage.unknown_parent` | 来源未知 |
| components/workspace/lineage-view.tsx:34 | `ui.workspace.lineage.from_capture_template` | 来自 {parent} 的 3D 视图截图 |
| components/workspace/lineage-view.tsx:34 | `ui.workspace.lineage.from_template` | 来自 {parent} |
| components/workspace/lineage-view.tsx:37 | `ui.workspace.lineage.evidence_same_bytes` | 依据：文件内容与它完全相同 |
| components/workspace/lineage-view.tsx:37 | `ui.workspace.lineage.evidence_recorded` | 依据：任务记录的来源 |
| components/workspace/lineage-view.tsx:52 | `ui.workspace.harness.loading` | 参数加载中… |
| components/workspace/lineage-view.tsx:53 | `ui.workspace.lineage.load_failed` | 版本链加载失败 |
| components/workspace/lineage-view.tsx:68 | `ui.workspace.lineage.empty` | 这个任务还没有可显示的版本 |
| components/workspace/lineage-view.tsx:72 | `ui.workspace.lineage.truncated` | 更早的版本没有载入，部分来源可能显示为未知 |
| components/workspace/lineage-view.tsx:106 | `ui.workspace.lineage.send_canvas` | 发到画布 |
| components/workspace/lineage-view.tsx:111 | `ui.workspace.lineage.send_chat` | 作为参考发到对话 |
| components/workspace/model-formats.tsx:50 | `ui.workspace.model_formats.download_all` | 下载全部（ZIP） |
| components/workspace/model-formats.tsx:95 | `ui.workspace.model_formats.credits` | 约 {credits} 积分 |
| components/workspace/model-formats.tsx:102 | `ui.workspace.model_formats.via` | 由 {provider} 转换 |
| components/workspace/model-formats.tsx:113 | `ui.workspace.model_formats.download` | 下载 |
| components/workspace/model-formats.tsx:114 | `ui.workspace.model_formats.download` | 下载 |
| components/workspace/model-formats.tsx:133 | `ui.workspace.model_formats.complete` | 补齐格式（约 {credits} 积分） |
| components/workspace/model-formats.tsx:139 | `ui.workspace.model_formats.completing` | 正在补齐格式… |
| components/workspace/model-formats.tsx:144 | `ui.workspace.model_formats.plan_paused` | 格式转换已暂停，请在处理面板中查看并继续。 |
| components/workspace/model-formats.tsx:149 | `ui.workspace.model_formats.plan_failed` | 有格式转换未能完成。 |
| components/workspace/model-formats.tsx:438 | `ui.workspace.model_formats.failed_step` / `retry_named` / `retry_charged_named` / `refetch_named` | {formats} 的转换（{provider}）没有完成。/ 按钮写出所属格式 |
| components/workspace/model-formats.tsx:223 | `ui.workspace.model_formats.complete_failed` | 补齐格式失败，请重试。 |
| components/workspace/model-formats.tsx:226 | `ui.workspace.model_formats.complete_failed` | 补齐格式失败，请重试。 |
| components/workspace/model-formats.tsx:245 | `ui.workspace.model_formats.complete_failed` | 补齐格式失败，请重试。 |
| components/workspace/model-formats.tsx:248 | `ui.workspace.model_formats.complete_failed` | 补齐格式失败，请重试。 |
| components/workspace/model-formats.tsx:256 | `ui.workspace.model_formats.title` | 交付格式 |
| components/workspace/model-formats.tsx:260 | `ui.workspace.model_formats.load_failed` | 读取格式清单失败，请稍后重试。 |
| components/workspace/model-formats.tsx:262 | `ui.workspace.model_formats.loading` | 正在读取格式清单… |
| components/workspace/model-processing-panel.tsx:149 | `ui.workspace.processing.plan_offering_changed` | 处理供给项的版本或费用已变化，请按当前费用重新确认。 |
| components/workspace/model-processing-panel.tsx:164 | `ui.workspace.processing.submit_failed` | 提交失败，请重试。 |
| components/workspace/model-processing-panel.tsx:213 | `ui.workspace.processing.plan_offering_changed` | 处理供给项的版本或费用已变化，请按当前费用重新确认。 |
| components/workspace/model-processing-panel.tsx:223 | `ui.workspace.processing.submit_failed` | 提交失败，请重试。 |
| components/workspace/model-processing-panel.tsx:234 | `ui.workspace.processing.plan_offering_changed` | 处理供给项的版本或费用已变化，请按当前费用重新确认。 |
| components/workspace/model-processing-panel.tsx:241 | `ui.workspace.processing.submit_failed` | 提交失败，请重试。 |
| components/workspace/model-processing-panel.tsx:245 | `ui.workspace.processing.close` | 关闭处理面板 |
| components/workspace/model-processing-panel.tsx:247 | `ui.workspace.processing.title` | 处理当前模型 |
| components/workspace/model-processing-panel.tsx:248 | `ui.workspace.processing.close` | 关闭处理面板 |
| components/workspace/model-processing-panel.tsx:254 | `ui.workspace.processing.load_failed` | 读取模型或处理能力失败，请重试。 |
| components/workspace/model-processing-panel.tsx:257 | `ui.workspace.processing.source` | 当前来源 |
| components/workspace/model-processing-panel.tsx:259 | `ui.workspace.processing.session_mismatch` | 请先切换到该模型所属的创作任务。 |
| components/workspace/model-processing-panel.tsx:260 | `ui.workspace.processing.asset_unavailable` | 当前模型没有已保存、可用的资产。 |
| components/workspace/model-processing-panel.tsx:282 | `ui.workspace.processing.plan_retry` | 确认费用，重试失败步骤 |
| components/workspace/model-processing-panel.tsx:282 | `ui.workspace.processing.plan_credits_unknown` | 费用以实际结算为准 |
| components/workspace/model-processing-panel.tsx:287 | `ui.workspace.processing.plan_confirm_step` | 确认本步骤并执行 |
| components/workspace/model-processing-panel.tsx:287 | `ui.workspace.processing.plan_prepare_step` | 准备本步骤确认 |
| components/workspace/model-processing-panel.tsx:289 | `ui.workspace.processing.plan_reconcile` | 核对上次提交结果 |
| components/workspace/model-processing-panel.tsx:290 | `ui.workspace.processing.plan_resume` | 确认当前费用，继续处理 |
| components/workspace/model-processing-panel.tsx:290 | `ui.workspace.processing.plan_credits_unknown` | 费用以实际结算为准 |
| components/workspace/model-processing-panel.tsx:293 | `ui.workspace.processing.plan_steps` | 选择处理步骤 |
| components/workspace/model-processing-panel.tsx:309 | `ui.workspace.processing.target` | 处理步骤与模型 |
| components/workspace/model-processing-panel.tsx:323 | `ui.workspace.processing.route` | 输入方式 |
| components/workspace/model-processing-panel.tsx:325 | `ui.workspace.processing.task_route` | 同服务商任务 |
| components/workspace/model-processing-panel.tsx:326 | `ui.workspace.processing.asset_route` | 本地模型资产 |
| components/workspace/model-processing-panel.tsx:327 | `ui.workspace.processing.unavailable` | 不可用 |
| components/workspace/model-processing-panel.tsx:331 | `ui.workspace.harness.credits` | 预计积分 |
| components/workspace/model-processing-panel.tsx:332 | `ui.workspace.harness.credits_unknown` | 该服务未声明预计积分，费用以实际结算为准 |
| components/workspace/model-processing-panel.tsx:344 | `ui.workspace.processing.input_rule_violation` | 以下输入需要且仅需要填写一项 |
| components/workspace/model-processing-panel.tsx:352 | `ui.workspace.processing.choose_action` | 请选择动作 |
| components/workspace/model-processing-panel.tsx:362 | `ui.workspace.processing.plan_incompatible` | 所选步骤没有可用的输入路径，请调整步骤或模型。 |
| components/workspace/model-processing-panel.tsx:368 | `ui.workspace.processing.plan_cost` | 预计总积分 |
| components/workspace/model-processing-panel.tsx:368 | `ui.workspace.harness.credits` | 预计积分 |
| components/workspace/model-processing-panel.tsx:369 | `ui.workspace.harness.credits_unknown` | 该服务未声明预计积分，费用以实际结算为准 |
| components/workspace/model-processing-panel.tsx:371 | `ui.workspace.processing.plan_cost_extra` | 区间的上限包含你选择的附加选项产生的额外积分；确认后以实际选项的预计积分为准。 |
| components/workspace/model-processing-panel.tsx:375 | `ui.workspace.processing.submitting` | 正在提交… |
| components/workspace/model-processing-panel.tsx:375 | `ui.workspace.processing.plan_confirm` | 确认步骤与费用，开始处理 |
| components/workspace/model-select.tsx:44 | `ui.workspace.harness.offering` | 模型 |
| components/workspace/model-select.tsx:47 | `ui.workspace.harness.choose_model` | 请选择生成模型 |
| components/workspace/session-notes-panel.tsx:23 | `ui.workspace.notes.label` | 设定摘要 |
| components/workspace/session-notes-panel.tsx:24 | `ui.workspace.notes.hint` | Agent 每一轮都会参考这份摘要。你可以直接修改；你们敲定新设定时，Agent 也会更新它。 |
| components/workspace/session-notes-panel.tsx:25 | `ui.workspace.notes.placeholder` | 例如：主角是戴红围巾的少女；整体水墨风；输出 1024×1024；不要出现文字。 |
| components/workspace/session-notes-panel.tsx:26 | `ui.workspace.notes.filled` | 已有设定 |
| components/workspace/session-notes-panel.tsx:27 | `ui.workspace.notes.save` | 保存 |
| components/workspace/session-notes-panel.tsx:28 | `ui.workspace.notes.revert` | 还原 |
| components/workspace/session-notes-panel.tsx:29 | `ui.workspace.notes.too_long` | 超出上限，请精简后再保存 |
| components/workspace/session-notes-panel.tsx:30 | `ui.workspace.notes.counter` | {count} / {maximum} |
| components/workspace/skill-selector.tsx:36 | `ui.workspace.history.close` | 关闭历史面板 |
| components/workspace/task-tray.tsx:85 | `ui.workspace.tasks.title` | 任务进度 |
| components/workspace/task-tray.tsx:86 | `ui.workspace.tasks.empty` | Agent 创建的任务会显示在这里 |
| components/workspace/task-tray.tsx:87 | `ui.workspace.tasks.progress` | 进度 |
| components/workspace/task-tray.tsx:88 | `ui.workspace.tasks.download` | 下载 |
| components/workspace/task-tray.tsx:89 | `ui.workspace.tasks.view` | 查看 |
| components/workspace/task-tray.tsx:90 | `ui.workspace.tasks.resources_pending` | 附加产物生成中… |
| components/workspace/task-tray.tsx:91 | `ui.workspace.tasks.error_title` | 任务失败 |
| components/workspace/task-tray.tsx:92 | `ui.workspace.tasks.retryable` | 可重试 |
| components/workspace/task-tray.tsx:93 | `ui.workspace.tasks.dismiss` | 从列表移除 |
| components/workspace/task-tray.tsx:94 | `ui.workspace.tasks.not_found` | 任务不存在或已删除 |
| components/workspace/task-tray.tsx:95 | `ui.workspace.tasks.cancel` | 取消任务 |
| components/workspace/task-tray.tsx:96 | `ui.workspace.tasks.cancel_confirm` | 取消后本地停止跟进；已提交到上游的任务可能仍会执行并计费。确定取消吗？ |
| components/workspace/task-tray.tsx:97 | `ui.workspace.tasks.cancel_failed` | 取消任务失败，请稍后重试 |
| components/workspace/task-tray.tsx:98 | `ui.workspace.tasks.retry` | 重试 |
| components/workspace/task-tray.tsx:99 | `ui.workspace.tasks.retry_confirm` | 确认重试 |
| components/workspace/task-tray.tsx:100 | `ui.workspace.tasks.retry_awaiting` | 重试需要你确认后才会提交，可能产生费用。 |
| components/workspace/task-tray.tsx:101 | `ui.workspace.tasks.retry_started` | 已重新提交 |
| components/workspace/task-tray.tsx:102 | `ui.workspace.tasks.retry_failed` | 重试失败，请稍后再试 |
| components/workspace/task-tray.tsx:103 | `ui.workspace.tasks.open` | 查看任务进度 |
| components/workspace/task-tray.tsx:104 | `ui.workspace.tasks.expand` | 展开任务详情 |
| components/workspace/task-tray.tsx:105 | `ui.workspace.tasks.close` | 关闭任务详情 |
| components/workspace/task-tray.tsx:106 | `ui.workspace.tasks.agent_preparing` | Agent 正在准备 |
| components/workspace/task-tray.tsx:107 | `ui.workspace.tasks.skill_runs_load_failed` | Skill 步骤加载失败，请稍后重试 |
| components/workspace/task-tray.tsx:328 | `ui.workspace.tasks.skill_checkpoint` | 流程记录 |
| components/workspace/workspace-balance.tsx:50 | `ui.workspace.balance.points` | 点 |
| components/workspace/workspace-balance.tsx:51 | `ui.workspace.balance.unavailable` | 余额暂无数据 |
| components/workspace/workspace-balance.tsx:84 | `ui.dashboard.balance` | 账户余额 |
| components/workspace/workspace-canvas-board.tsx:136 | `ui.workspace.harness.loading` | 参数加载中… |
| components/workspace/workspace-canvas-board.tsx:137 | `ui.workspace.history.load_failed` | 加载任务会话失败 |
| components/workspace/workspace-canvas-board.tsx:138 | `ui.workspace.harness.save_failed` | 保存参数失败 |
| components/workspace/workspace-canvas-board.tsx:139 | `ui.workspace.canvas.conflict` | 画布已在其他窗口被修改，已加载服务器最新内容；本窗口未保存的改动已被替换 |
| components/workspace/workspace-canvas-board.tsx:140 | `ui.workspace.canvas.export_failed` | 导出失败 |
| components/workspace/workspace-canvas-board.tsx:141 | `ui.workspace.canvas.upload_failed` | 上传图片失败，请重试 |
| components/workspace/workspace-canvas-board.tsx:487 | `ui.dialog.close` | 关闭 |
| components/workspace/workspace-canvas-board.tsx:489 | `ui.canvas.mask.switch_title` | 切换到支持局部重绘的模型 |
| components/workspace/workspace-canvas-board.tsx:494 | `ui.canvas.mask.switch_confirm` | 当前所选模型「{model}」不支持局部重绘。切换到支持蒙版的模型「{target}」并进入涂抹？ |
| components/workspace/workspace-canvas-board.tsx:505 | `ui.canvas.mask.cancel` | 退出蒙版 |
| components/workspace/workspace-canvas-board.tsx:515 | `ui.canvas.mask.switch_accept` | 切换并涂抹 |
| components/workspace/workspace-canvas-board.tsx:521 | `ui.canvas.mask.no_candidate` | 没有可用的高质量图像模型支持局部重绘 |
| components/workspace/workspace-topbar.tsx:54 | `ui.workspace.tab.image` | 图片创作 |
| components/workspace/workspace-topbar.tsx:55 | `ui.workspace.tab.model_3d` | 3D 模型 |
| components/workspace/workspace-topbar.tsx:57 | `ui.workspace.history.all` | 历史任务 |
| components/workspace/workspace-topbar.tsx:58 | `ui.workspace.user.logout` | 退出登录 |
| components/workspace/workspace-topbar.tsx:59 | `ui.dashboard.balance` | 账户余额 |
| components/workspace/workspace-topbar.tsx:67 | `ui.dashboard.fallback_initial` | · |
| components/workspace/workspace-topbar.tsx:79 | `ui.workspace.session.rename_failed` | 重命名失败，请重试 |
| components/workspace/workspace-topbar.tsx:135 | `ui.workspace.session.rename` | 重命名主题 |
| components/workspace/workspace-topbar.tsx:183 | `ui.dashboard.balance_unavailable` | — |
| components/workspace/workspace-topbar.tsx:194 | `ui.dashboard.balance_unavailable` | — |
| components/workspace/workspace-topbar.tsx:234 | `ui.workspace.session.rename` | 重命名主题 |
| components/workspace/workspace-topbar.tsx:237 | `ui.workspace.session.title` | 主题名称 |
| components/workspace/workspace-topbar.tsx:249 | `ui.workspace.session.cancel` | 取消 |
| components/workspace/workspace-topbar.tsx:252 | `ui.workspace.session.save` | 保存 |
| hooks/use-session-agent-params.ts:89 | `ui.workspace.harness.save_failed` | 保存参数失败 |
| hooks/use-task-completion-notice.ts:55 | `ui.workspace.notice.done` | 任务已完成 |
| hooks/use-task-completion-notice.ts:55 | `ui.workspace.notice.failed` | 任务失败 |
| hooks/use-task-completion-notice.ts:70 | `ui.workspace.notice.tab_title` | （{count}）有任务已结束 · {title} |
| hooks/use-task-completion-notice.ts:75 | `ui.workspace.notice.body_fallback` | {capability} |

---

## 附录 B：未核对与需要运行验证的项

- 运行页面，验证 6.4 节和 9.1 节 1、2、13、14 项。
- 验证 9.5 节的遮罩与叠层。
- 运行 `design-token-discipline.test.ts`。
- 核对 `use-session-state.ts` 的草稿存储位置。
- 核对 `browser-navigation.ts` 的实现。
- 核对历史抽屉“发到画布”是否切换域。
- 核对图片生成按钮（`onImageGenerator`）的完整行为。
- 核对 Schema 中 `ui.*` 未使用 key 的数量（本次静态估计约 520 个，`ui.*` 共 943 个；动态拼接的 key 已部分计入）。
- 核对 `model-formats` 状态 key（4 个）的具体值。
