# Agent 产出质量评测（trajectory-fake）

- revision: `1f01387c898165a62dc721a0167d22dd27c2e1f8`
- 请求集: version 1, sha256 `118eacc992f5b54546df3e8bf1f1a3321f76a18be6175cc44098cd4d69ca2036`
- 用例: 9；硬失败: 0
- 合计: 工具调用 37，付费调用 13，打断 12，inspect 6，render 6

| 用例 | 结果 | 工具 | 付费 | 打断 | inspect | render | 耗时 |
|---|---|---|---|---|---|---|---|
| 3d-humanoid-character | PASS | 5 | 1 | 2 | 1 | 1 | 7.1s |
| 3d-hard-surface-prop | PASS | 5 | 1 | 2 | 1 | 1 | 7.1s |
| 3d-scene-piece | PASS | 5 | 1 | 2 | 1 | 1 | 7.1s |
| 3d-creature | PASS | 5 | 1 | 2 | 1 | 1 | 7.1s |
| 3d-print-figurine | PASS | 6 | 2 | 2 | 1 | 1 | 7.4s |
| 3d-ar-product | PASS | 6 | 2 | 2 | 1 | 1 | 7.5s |
| 2d-character-illustration | PASS | 1 | 1 | 0 | 0 | 0 | 0.5s |
| 2d-icon-set | PASS | 1 | 1 | 0 | 0 | 0 | 0.5s |
| 2d-series-consistency | PASS | 3 | 3 | 0 | 0 | 0 | 1.5s |

## 各用例指标

### 3d-humanoid-character — 人形角色：赛博朋克女刺客（游戏角色）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'create_model_3d': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1}
- 任务：[('text-to-3d', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-hard-surface-prop — 硬表面道具：低多边形科幻能量步枪（游戏道具）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'create_model_3d': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1}
- 任务：[('text-to-3d', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-scene-piece — 场景件：石砌水井（游戏场景小件）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'create_model_3d': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1}
- 任务：[('text-to-3d', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-creature — 生物：发光深海水母（展示渲染）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'create_model_3d': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1}
- 任务：[('text-to-3d', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-print-figurine — 打印手办：15 厘米桌游骑士（先概念图再图生 3D）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'refine_image': 1, 'create_model_3d_from_image': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1}
- 任务：[('image-to-3d', 'SUCCEEDED'), ('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 3d-ar-product — AR 商品：运动水壶（先商品图再图生 3D）

- 工具：{'ask_choice': 1, 'update_production_brief': 1, 'refine_image': 1, 'create_model_3d_from_image': 1, 'inspect_model': 1, 'render_model_views': 1}
- 自检：{'inspect_model': 1, 'render_model_views': 1}
- 任务：[('image-to-3d', 'SUCCEEDED'), ('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 2d-character-illustration — 2D 角色立绘：赛博朋克女刺客

- 工具：{'refine_image': 1}
- 自检：{'inspect_model': 0, 'render_model_views': 0}
- 任务：[('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 2d-icon-set — 2D 图标/道具图：三色药水瓶图标

- 工具：{'refine_image': 1}
- 自检：{'inspect_model': 0, 'render_model_views': 0}
- 任务：[('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

### 2d-series-consistency — 2D 系列一致性：同一角色的立绘、半身像与侧面像

- 工具：{'refine_image': 3}
- 自检：{'inspect_model': 0, 'render_model_views': 0}
- 任务：[('text-to-image', 'SUCCEEDED'), ('text-to-image', 'SUCCEEDED'), ('text-to-image', 'SUCCEEDED')]
- 最终回复：20 字符

