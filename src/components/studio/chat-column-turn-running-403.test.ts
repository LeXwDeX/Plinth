import { readFileSync } from 'node:fs';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { branchUnavailableReason } from '@/lib/chat/session-branch';
import { refreshTasksAfterDone } from '@/lib/chat/turn-settle';
import type { TaskStateSchema } from '@/lib/product-schema';
import { refreshSessionTasks } from '@/lib/workspace/task-read';
import { useWorkspaceStore } from '@/store/workspace-store';

// #403: wiring of the running-turn behaviour in the chat column. The behaviour itself is proved in
// src/lib/chat/{turn-follow,turn-replace-error,turn-running}-403.test.ts and session-recovery tests; this
// pins that the column uses those pieces where the bug was.

function source(relative: string): string {
  return readFileSync(new URL(relative, import.meta.url), 'utf8');
}

const column = source('./chat-column.tsx');
const send = column.slice(column.indexOf('const send = useCallback('), column.indexOf('const sendRef = useRef(send);'));

describe('a failed replace says why (#403)', () => {
  it('shows the server reason of a 409 and the general text for anything else', () => {
    const loop = send.indexOf('for (const [index, id] of replaceIds.entries()) {');
    expect(loop).toBeGreaterThan(0);
    const replace = send.slice(loop);
    // #645: each deletion names the turn's messages still to delete, so the server refuses a stale page with a 409.
    expect(replace).toContain('await chatClient.deleteMessage(sessionId, id, replaceIds.slice(index));');
    expect(replace.indexOf('replaceFailureMessage(')).toBeGreaterThan(0);
    expect(replace).toMatch(/\} catch \(error\) \{[\s\S]{0,400}replaceFailureMessage\(error, schemaText\(schema, 'ui\.chat\.toast\.turn_replace_failed'\)\)/);
    // The list is still read again and the old round kept as it was.
    expect(replace.indexOf('applyNewestMessagePage(sessionId, page)')).toBeLessThan(replace.indexOf('replaceFailureMessage('));
  });
});

describe('a broken stream does not end a turn the server still runs (#403)', () => {
  it('follows the turn through the one bounded helper, with the Schema bound', () => {
    expect(column).toContain("import { followTurnStream, type TurnFollowEnd } from '@/lib/chat/turn-follow';");
    expect(send).toContain('followed = await followTurnStream({');
    expect(send).toContain('attempts: schema.chat.frontend.stream_reconnect_attempts,');
    expect(send).toContain('rewind: () => rewindStreaming(sessionId),');
    // The stream is replayed from the first event into a buffer that was rewound, never into the old one.
    expect(send).toMatch(/connect: \(callbacks\) => \{[\s\S]{0,200}connectEventSource\(sessionId, \{ fromId: 0, \.\.\.callbacks \}\)/);
    // What the server says decides: a message read, not a guess from the error.
    expect(send).toMatch(/stillRunning: async \(signal\) => \{[\s\S]{0,240}chatClient\.listMessagePage\(sessionId, \{ limit: messagePageSize \}, signal\)[\s\S]{0,120}some\(\(message\) => message\.isPending\)/);
    // Only an interrupted follow is reported as the interrupted stream it is.
    expect(send).toMatch(/if \(followed === 'interrupted'\) throw new Error\(schema\.chat\.presentation\.messages\.sse_interrupted\);/);
  });

  it('hands a turn that still runs to the session recovery when the stream is given up', () => {
    const settle = send.slice(send.indexOf('let settled = false;'), send.indexOf('void refreshApproval();', send.indexOf('let settled = false;')));
    expect(settle).toContain('settleStreaming(sessionId, page);');
    // The recovery reconnects the stream once more and otherwise keeps reading until the turn ends.
    // A stream that said `done` is not reconnected; a read that landed between the event and the turn's final
    // write is only read again (#597), and recovery is asked only when the reply still runs after that.
    expect(settle).toMatch(/if \(!page\.messages\.some\(\(message\) => message\.isPending\)\) return;\s*if \(followed !== 'done'\) \{\s*setServerTurnTick\(\(tick\) => tick \+ 1\);/);
    expect(settle.indexOf('settleStreaming(sessionId, page);')).toBeLessThan(settle.indexOf('setServerTurnTick('));
  });
});

describe('the reply of a running turn is not offered for replacing (#403)', () => {
  it('opens the turn actions only when nothing is streaming and no message is pending', () => {
    expect(column).toContain('const turnActionsOpen = Boolean(activeSessionId) && !isStreaming && !preparing && !uploading;');
    // lastExchange() refuses a tail with a pending message, which is what the server marks for a running turn.
    expect(source('../../lib/chat/turn-actions.ts')).toContain('return !message.isPending && !message.isStreaming');
  });
});

describe('a stream joined after a reload shows no blank message (#403)', () => {
  it('skips the optimistic user message when there are no sent words and no pictures', () => {
    expect(column).toContain("import { hasSentWords, sessionTurnRunning } from '@/lib/chat/turn-running';");
    expect(column).toMatch(/const optimisticUser = useMemo\(\(\): ChatMessageType \| null => \(streamUserMessage && hasSentWords\(streamUserMessage\) \? \{/);
  });
});

describe('an approved plan whose turn is over says its steps were not run (#403)', () => {
  it('tells the approved plan card whether a step may still run, from the running projection', () => {
    // #410: the server projects every step's standing and only counts a turn as
    // running, so an open approval card no longer keeps a step "waiting".
    expect(column).toContain("const planStepsMayStillRun = sessionTurnRunning(messagesBySession[activeSessionId ?? ''], isStreaming);");
    const approved = column.slice(column.indexOf('plan={sessionBudget.plan}'));
    expect(approved.slice(0, 320)).toContain('stepsMayStillRun={planStepsMayStillRun}');
  });

  it('leaves the plan that still waits for approval alone: it waits for the person, not for a turn', () => {
    const start = column.indexOf('plan={item.plan}');
    expect(start).toBeGreaterThan(0);
    expect(column.slice(start, start + 520)).not.toContain('stepsMayStillRun');
  });

  it('gives the top-bar budget summary the same rule from the messages', () => {
    const badge = source('./budget-badge.tsx');
    expect(badge).toContain("import { sessionTurnRunning } from '@/lib/chat/turn-running';");
    expect(badge).toMatch(/const stepsMayStillRun = useChatStore\(\(state\) => sessionTurnRunning\(/);
    expect(badge).toContain('<PlanCard plan={budget.plan} tasks={tasks} progress={budget} stepsMayStillRun={stepsMayStillRun} />');
  });
});

describe('a reply read just before its turn ended does not stay running (#597)', () => {
  it('reads the page again after done while the reply is still pending, and recovery takes a turn that really runs', () => {
    expect(column).toContain("import { messagesBesideStream, readUntilSettled, refreshTasksAfterDone } from '@/lib/chat/turn-settle';");
    const settle = send.slice(send.indexOf('settleStreaming(sessionId, page);'));
    expect(settle.indexOf("if (followed !== 'done') {")).toBeGreaterThan(0);
    expect(settle).toMatch(/void readUntilSettled\(\{[\s\S]{0,400}delays: SETTLE_REREAD_MILLISECONDS,[\s\S]{0,200}outcome === 'running' && shown\(\)\) setServerTurnTick/);
  });

  it('reads the session\'s running tasks at done and with the rereads, not only at the next task poll (#605)', () => {
    expect(column).toContain("import { refreshSessionTasks } from '@/lib/workspace/task-read';");
    expect(send).toContain('timeoutMilliseconds: schema.runtime.frontend.task_read_timeout_milliseconds,');
    const settle = send.slice(send.indexOf('settleStreaming(sessionId, page);'));
    // Before the early return for a reply that is no longer pending: the tasks are read either way.
    expect(settle).toMatch(/if \(followed === 'done'\) \{[\s\S]{0,500}void refreshTasksAfterDone\(\{\s*refresh: \(\) => refreshSessionTasks\(sessionId, taskRead\),\s*isCurrent: shown,\s*delays: SETTLE_REREAD_MILLISECONDS,/);
    expect(settle.indexOf('void refreshTasksAfterDone(')).toBeLessThan(settle.indexOf('if (!page.messages.some((message) => message.isPending)) return;'));
  });

  it('hides only the streaming turn\'s own pending reply next to the live buffer', () => {
    expect(column).toContain('const dbMessages = messagesBesideStream(messages, streamBuf);');
    expect(column).not.toContain('!(streamBuf && item.isPending)');
  });
});

describe('the branch is available again once the reads after done find the turn\'s task ended (#605)', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    useWorkspaceStore.setState({ trackedTasks: [] });
  });

  it('runs the column\'s reads after done and derives the branch state the way the column does', async () => {
    const state = (internal: string, terminal: boolean, outcome: TaskStateSchema['outcome']): TaskStateSchema => ({
      internal_state: internal, public_state: internal, label: internal, color: '', terminal, outcome, progress: { mode: 'reported' },
    });
    const detail = (taskState: string) => new Response(JSON.stringify({ id: 'task_1', state: taskState, progress: 100, assets: [] }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    });
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(detail('RUNNING')).mockResolvedValueOnce(detail('SUCCEEDED'));
    vi.stubGlobal('fetch', fetchMock);
    useWorkspaceStore.setState({ trackedTasks: [] });
    useWorkspaceStore.getState().registerTask({ taskId: 'task_1', sessionId: 's1', capability: 'image', progressMaximum: 100, autoPreview: false });
    // As the column: the shown session's tasks, a branch blocked while one of them has not ended.
    const branchReason = () => branchUnavailableReason({
      running: useWorkspaceStore.getState().trackedTasks.some((task) => task.sessionId === 's1' && !task.notFound && !task.terminal),
    });
    expect(branchReason()).not.toBe('');

    const taskRead = {
      route: '/api/v1/tasks/{taskID}',
      taskStates: [state('RUNNING', false, 'running'), state('SUCCEEDED', true, 'success')],
      progressMaximum: 100,
      timeoutMilliseconds: 15_000,
      isCurrent: () => true,
    };
    await refreshTasksAfterDone({
      refresh: () => refreshSessionTasks('s1', taskRead),
      isCurrent: () => true,
      delays: [400, 1_200],
      wait: () => Promise.resolve(),
    });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(branchReason()).toBe('');
  });
});
