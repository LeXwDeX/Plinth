import { describe, expect, it } from 'vitest';
import { knownBalances } from '@/components/workspace/workspace-balance';
import { projectSessionSkillRuns } from '@/hooks/use-session-skill-runs';

describe('workspace balance', () => {
  it('selects zero as the minimum and excludes unknown balances', () => {
    const accounts = [
      { provider: 'unknown', label: 'Unknown', estimates: [] },
      { provider: 'high', label: 'High', balance: 1000, estimates: [] },
      { provider: 'zero', label: 'Zero', balance: 0, estimates: [] },
    ];
    expect(knownBalances(accounts).map((account) => account.provider)).toEqual(['zero', 'high']);
    expect(knownBalances([{ ...accounts[1], balance: 7 }, { ...accounts[2], balance: 3 }])[0].provider).toBe('zero');
  });
});

describe('session SkillRun projection', () => {
  it('keeps only server steps for the requested session and known phases', () => {
    const response = { runs: [
      { id: 'a', session_id: 'session-a', steps: [
        { spec: { key: 'front', label: '正面' }, phase: 'grant_pending' },
        { spec: { key: 'side', label: '侧面' }, phase: 'task_bound', task_id: 'task-1' },
        { spec: { key: 'other', label: '未知' }, phase: 'invented' },
      ] },
      { id: 'b', session_id: 'session-b', steps: [{ spec: { key: 'back', label: '背面' }, phase: 'planned' }] },
    ] };
    expect(projectSessionSkillRuns(response, 'session-a')).toEqual([{
      id: 'a', sessionId: 'session-a', steps: [
        { key: 'front', label: '正面', phase: 'grant_pending', taskId: null },
        { key: 'side', label: '侧面', phase: 'task_bound', taskId: 'task-1' },
      ],
    }]);
    expect(projectSessionSkillRuns(response, 'session-c')).toEqual([]);
  });
});
