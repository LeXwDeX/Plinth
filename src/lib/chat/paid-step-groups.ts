import type { ChatPaidStep } from '@/store/chat-store';

/**
 * #546: one line per kind of paid step in a message instead of one per step. Parallel steps of one turn used to repeat
 * "本步消耗 6 积分 · 已按批准方式自动批准" four times; now the completed steps priced in credits are summed per approval
 * source ("本轮消耗 24 积分（4 步）· …"). A step that did not complete keeps its own line (its note and retry hint are
 * per task), and so does a step with a native quote (its unit and amount are the server's, not summed here).
 */
export type PaidStepRow =
  | { kind: 'round'; key: string; taskIds: string[]; credits: number; steps: number; approvedByPolicy: boolean }
  | { kind: 'step'; key: string; step: ChatPaidStep };

export function groupPaidSteps(steps: readonly ChatPaidStep[], failed: (taskId: string) => boolean = () => false): PaidStepRow[] {
  const rows: Array<PaidStepRow | { kind: 'group'; approvedByPolicy: boolean; members: ChatPaidStep[] }> = [];
  const groups = new Map<boolean, ChatPaidStep[]>();
  for (const step of steps) {
    if (step.nativeQuote !== undefined || failed(step.taskId)) {
      rows.push({ kind: 'step', key: step.taskId, step });
      continue;
    }
    const members = groups.get(step.approvedByPolicy);
    if (members) {
      members.push(step);
      continue;
    }
    const created = [step];
    groups.set(step.approvedByPolicy, created);
    rows.push({ kind: 'group', approvedByPolicy: step.approvedByPolicy, members: created });
  }
  return rows.map((row): PaidStepRow => {
    if (row.kind !== 'group') return row;
    if (row.members.length === 1) return { kind: 'step', key: row.members[0].taskId, step: row.members[0] };
    return {
      kind: 'round',
      key: row.members.map((step) => step.taskId).join(' '),
      taskIds: row.members.map((step) => step.taskId),
      credits: row.members.reduce((sum, step) => sum + step.credits, 0),
      steps: row.members.length,
      approvedByPolicy: row.approvedByPolicy,
    };
  });
}
