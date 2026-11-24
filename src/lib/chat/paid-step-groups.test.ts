import { describe, expect, it } from 'vitest';

import { groupPaidSteps } from './paid-step-groups';

const step = (taskId: string, credits: number, approvedByPolicy = true, extra: object = {}) => ({ taskId, credits, approvedByPolicy, ...extra });

describe('one line per kind of paid step (#546)', () => {
  it('sums the credits and counts the steps of parallel steps approved the same way', () => {
    const rows = groupPaidSteps(['a', 'b', 'c', 'd'].map((id) => step(id, 6)));
    expect(rows).toEqual([{ kind: 'round', key: 'a b c d', taskIds: ['a', 'b', 'c', 'd'], credits: 24, steps: 4, approvedByPolicy: true }]);
  });

  it('keeps steps approved by the person apart from those the approval mode approved', () => {
    const rows = groupPaidSteps([step('a', 6), step('b', 40, false), step('c', 6), step('d', 10, false)]);
    expect(rows.map((row) => row.kind === 'round' ? [row.approvedByPolicy, row.credits, row.steps] : null)).toEqual([[true, 12, 2], [false, 50, 2]]);
  });

  it('leaves a lone step as it was', () => {
    expect(groupPaidSteps([step('a', 6)])).toEqual([{ kind: 'step', key: 'a', step: step('a', 6) }]);
    expect(groupPaidSteps([])).toEqual([]);
  });

  it('keeps a step that did not complete, and a natively priced one, on a line of its own', () => {
    const quote = { version: 1, unit: 'vendor.credit', unit_label: 'Vendor credits', base_amount: '1', extra_amount: '0', total_amount: '1' };
    const rows = groupPaidSteps([step('a', 6), step('b', 6), step('c', 6), step('n', 0, true, { nativeQuote: quote })], (id) => id === 'b');
    expect(rows.map((row) => row.kind === 'round' ? `round:${row.taskIds.join(',')}` : `step:${row.step.taskId}`)).toEqual(['round:a,c', 'step:b', 'step:n']);
  });
});
