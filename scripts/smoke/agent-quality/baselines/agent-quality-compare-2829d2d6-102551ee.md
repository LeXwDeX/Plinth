# Agent 产出质量评测对比

- 基线: `2829d2d63917869c16ed47170687b4e96a8128cf`
- 候选: `102551ee07e781b6813e6f35ec5939626ce3026f`
- 结论: 存在回退或不可比项

## 回退

| 用例 | 检查 | 基线 | 候选 |
|---|---|---|---|
| * | metric:tool_calls | 53 | 54 |
| * | metric:paid_calls | 18 | 19 |

## 改善

- `3d-multistep-rig-checkpoint` / `capability:remesh`: fail → pass
- `3d-multistep-rig-checkpoint` / `order:plan_model_processing<revise_processing_plan`: fail → pass
- `3d-multistep-rig-checkpoint` / `order:revise_processing_plan<inspect_model`: fail → pass
- `3d-multistep-rig-checkpoint` / `tool_group:revise`: fail → pass

## 汇总指标

| 指标 | 基线 | 候选 | 变化 |
|---|---|---|---|
| tool_calls | 53 | 54 | +1 |
| paid_calls | 18 | 19 | +1 |
| interruptions | 19 | 19 | +0 |
| inspect_model | 8 | 8 | +0 |
| render_model_views | 6 | 6 | +0 |
| compare_model_reference | 1 | 1 | +0 |
| self_check_records | 1 | 1 | +0 |
| self_check_passed | 1 | 1 | +0 |

## 质量指标（仅记录，不参与回退判定）

| 指标 | 基线 | 候选 | 变化 |
|---|---|---|---|
| success_rate | 91.7 | 100.0 | +8.3 |
| rework_count | 0 | 1 | +1 |
| revision_count | 0 | 1 | +1 |
| checkpoint_count | 0 | 1 | +1 |
| checkpoint_failed | 0 | 1 | +1 |
| checkpoint_waived | 0 | 0 | +0 |
