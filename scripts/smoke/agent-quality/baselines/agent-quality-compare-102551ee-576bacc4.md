# #242 开放需求评测对比（fake 轨迹）

- 改前：`102551ee07e781b6813e6f35ec5939626ce3026f`，请求集 v3，12 个用例
- 改后：`576bacc4db8c290e5c20571726e27ff0398495e9`，请求集 v4，14 个用例
- 新增：2d-open-concept, 3d-open-candidates

## 共享用例逐项对比

| 用例 | 改前 | 改后 | 失败检查 | 工具调用 | 付费调用 |
|---|---|---|---|---|---|
| 2d-3d-input-concept | None | None | 0 → 0 | 3 → 3 | 1 → 1 |
| 2d-character-illustration | None | None | 0 → 0 | 2 → 2 | 1 → 1 |
| 2d-icon-set | None | None | 0 → 0 | 2 → 2 | 1 → 1 |
| 2d-series-consistency | None | None | 0 → 0 | 4 → 4 | 3 → 3 |
| 3d-ar-product | None | None | 0 → 0 | 6 → 6 | 2 → 2 |
| 3d-creature | None | None | 0 → 0 | 5 → 5 | 1 → 1 |
| 3d-hard-surface-prop | None | None | 0 → 0 | 5 → 5 | 1 → 1 |
| 3d-humanoid-character | None | None | 0 → 0 | 5 → 5 | 1 → 1 |
| 3d-image-reference | None | None | 0 → 0 | 4 → 4 | 1 → 1 |
| 3d-multistep-rig-checkpoint | None | None | 0 → 0 | 7 → 7 | 4 → 4 |
| 3d-print-figurine | None | None | 0 → 0 | 6 → 6 | 2 → 2 |
| 3d-scene-piece | None | None | 0 → 0 | 5 → 5 | 1 → 1 |

## 新增用例

- `2d-open-concept`：3 个概念方向 + 方向卡 + 选定方向细化；工具/付费调用见 `agent-quality-trajectory-576bacc4.json`。
- `3d-open-candidates`：两个 3D 候选；预算与候选比较见同一报告。

## 说明

- 请求集从 v3 升到 v4，新增两个开放需求用例；共享用例的检查状态与花费可直接比较。
- 全部为 fake 栈轨迹，真实产出质量与 live output 评分按 fake-only 指令 deferred。
