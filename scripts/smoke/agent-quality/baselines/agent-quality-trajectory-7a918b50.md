# Agent 产出质量评测（trajectory-fake）

- revision: `7a918b5019d9ee5d3f719f74d98b34c1743c92d5`
- 请求集: version 3, sha256 `3405175000e2807ca1ecafd83f56def819126d09e4e18aa082fc6ba462b946d1`
- 用例: 12；硬失败: 0
- 合计: 工具调用 51，付费调用 17，打断 18，inspect 7，render 6，对照 1，自检记录 1，计费 {'legacy.credits': '349'}

| 用例 | 结果 | 工具 | 付费 | 打断 | inspect | render | 对照 | 自检 | 耗时 |
|---|---|---|---|---|---|---|---|---|---|
| 3d-humanoid-character | PASS | 5 | 1 | 2 | 1 | 1 | 0 | 0 | 7.1s |
| 3d-hard-surface-prop | PASS | 5 | 1 | 2 | 1 | 1 | 0 | 0 | 7.4s |
| 3d-scene-piece | PASS | 5 | 1 | 2 | 1 | 1 | 0 | 0 | 7.6s |
| 3d-creature | PASS | 5 | 1 | 2 | 1 | 1 | 0 | 0 | 7.3s |
| 3d-print-figurine | PASS | 6 | 2 | 2 | 1 | 1 | 0 | 0 | 7.3s |
| 3d-ar-product | PASS | 6 | 2 | 2 | 1 | 1 | 0 | 0 | 7.5s |
| 2d-character-illustration | PASS | 2 | 1 | 1 | 0 | 0 | 0 | 0 | 0.3s |
| 2d-icon-set | PASS | 2 | 1 | 1 | 0 | 0 | 0 | 0 | 0.3s |
| 2d-series-consistency | PASS | 4 | 3 | 1 | 0 | 0 | 0 | 0 | 0.7s |
| 2d-series-project | PASS | 4 | 2 | 1 | 0 | 0 | 0 | 0 | 0.5s |
| 2d-3d-input-concept | PASS | 3 | 1 | 1 | 0 | 0 | 0 | 0 | 3.5s |
| 3d-image-reference | PASS | 4 | 1 | 1 | 1 | 0 | 1 | 1 | 3.8s |

## 各用例指标

### 3d-humanoid-character — 人形角色：赛博朋克女刺客（游戏角色）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'create_model_3d': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('text-to-3d', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-hard-surface-prop — 硬表面道具：低多边形科幻能量步枪（游戏道具）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'create_model_3d': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('text-to-3d', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-scene-piece — 场景件：石砌水井（游戏场景小件）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'create_model_3d': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('text-to-3d', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-creature — 生物：发光深海水母（展示渲染）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'create_model_3d': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('text-to-3d', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-print-figurine — 打印手办：15 厘米桌游骑士（先概念图再图生 3D）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'refine_image': 1, 'create_model_3d_from_image': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('image-to-3d', 'SUCCEEDED'), ('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-ar-product — AR 商品：运动水壶（先商品图再图生 3D）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'refine_image': 1, 'create_model_3d_from_image': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('image-to-3d', 'SUCCEEDED'), ('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 2d-character-illustration — 2D 角色立绘：赛博朋克女刺客

- 工具：{'ask_choice': 1, 'refine_image': 1}
- 自检：{'inspect_model': 0, 'render_model_views': 0, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 2d-icon-set — 2D 图标/道具图：三色药水瓶图标

- 工具：{'ask_choice': 1, 'refine_image': 1}
- 自检：{'inspect_model': 0, 'render_model_views': 0, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 2d-series-consistency — 2D 系列一致性：同一角色的立绘、半身像与侧面像

- 工具：{'ask_choice': 1, 'refine_image': 3}
- 自检：{'inspect_model': 0, 'render_model_views': 0, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('text-to-image', 'SUCCEEDED'), ('text-to-image', 'SUCCEEDED'), ('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 2d-series-project — 2D 项目系列复用：项目里定稿角色 A，新会话画同系列角色 B

- 工具：{'ask_choice': 1, 'refine_image': 2, 'list_project_assets': 1}
- 自检：{'inspect_model': 0, 'render_model_views': 0, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('text-to-image', 'SUCCEEDED'), ('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 2d-3d-input-concept — 2D 到 3D：单主体、纯色背景的概念图

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'refine_image': 1}
- 自检：{'inspect_model': 0, 'render_model_views': 0, 'compare_model_reference': 0, 'self_check_records': 0, 'self_check_passed': 0, 'self_check_issues': 0}
- 任务：[('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-image-reference — 参考图贴合：图生 3D 的并排对照与自检记录

- 工具：{'create_model_3d_from_image': 1, 'inspect_model': 1, 'compare_model_reference': 1, 'record_self_check': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 0, 'compare_model_reference': 1, 'self_check_records': 1, 'self_check_passed': 1, 'self_check_issues': 0}
- 任务：[('image-to-3d', 'SUCCEEDED')]
- 最终回复：20 字符

