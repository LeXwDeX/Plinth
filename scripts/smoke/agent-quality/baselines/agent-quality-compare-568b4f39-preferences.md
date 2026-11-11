> #241 的 preferences off/on 对比：同一 revision 与同一请求集。off 的 2d-preference-multiturn 在 reject 步骤即被 400 拒绝，因此比 on 少 4 次工具与付费调用；比较用 --tolerance 4 记录这一预期差异。

# Agent 产出质量评测对比

- 基线: `568b4f3980b1dc1a2180a864fe0038c9bc2b966b`
- 候选: `568b4f3980b1dc1a2180a864fe0038c9bc2b966b`
- 结论: 无回退

## 改善

- `2d-preference-multiturn` / `preference:after_compaction`: fail → pass
- `2d-preference-multiturn` / `preference:captured`: fail → pass
- `2d-preference-multiturn` / `preference:deleted`: fail → pass
- `2d-preference-multiturn` / `preference:edited`: fail → pass
- `2d-preference-multiturn` / `preference:injected`: fail → pass

## 汇总指标

| 指标 | 基线 | 候选 | 变化 |
|---|---|---|---|
| tool_calls | 55 | 59 | +4 |
| paid_calls | 21 | 25 | +4 |
| interruptions | 18 | 18 | +0 |
| inspect_model | 7 | 7 | +0 |
| render_model_views | 6 | 6 | +0 |
| compare_model_reference | 1 | 1 | +0 |
| self_check_records | 1 | 1 | +0 |
| self_check_passed | 1 | 1 | +0 |
