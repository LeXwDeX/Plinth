import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';
import defaultCatalog from '../../internal/catalog/default_catalog.json';
import { TaskCenterButton, TaskDrawer } from '@/components/studio/task-center';
import { projectNativeTaskDetail } from '@/lib/workspace/task-projection';
import type { ProductSchema } from '@/lib/product-schema';
import type { TrackedTask } from '@/store/workspace-store';

/**
 * 任务中心的取消入口：只对运行中任务渲染，文案只认 Schema
 * ui.workspace.tasks.cancel；终态任务只保留“移除”。
 */

const catalogMessages = (
  defaultCatalog.application as {
    content?: { messages?: Record<string, string> };
  }
).content?.messages;

const msg = (key: string) => catalogMessages?.[key] ?? key;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({
    schema: defaultCatalog.application,
    text: (key: string) => catalogMessages?.[key] ?? '',
  }),
}));

// The drawer is open for these renders; the real store would start closed.
const studioState = { taskDrawerOpen: true, setTaskDrawerOpen: () => {} };
vi.mock('@/store/studio-store', () => ({
  useStudioStore: (selector: (state: typeof studioState) => unknown) => selector(studioState),
}));

// SSR 渲染下 zustand 只读初始快照，因此以选择器桩注入固定任务集。
const trackedTask = (taskId: string, state: string, terminal: boolean): TrackedTask => ({
  taskId,
  sessionId: 'session-a',
  capability: 'text-to-image',
  prompt: taskId,
  state,
  progress: terminal ? 100 : 40,
  progressMaximum: 100,
  terminal,
  failure: null,
  liveError: '',
  assets: [],
  downloadables: [],
  resources: [],
  resourcesPending: false,
  notFound: false,
  updatedAt: 0,
});

const storeState = {
  trackedTasks: [
    trackedTask('running', 'RUNNING', false),
    trackedTask('done', 'SUCCEEDED', true),
    { ...trackedTask('foreign-running', 'RUNNING', false), sessionId: 'session-b' },
    { ...trackedTask('unknown-running', 'RUNNING', false), sessionId: undefined },
  ],
  setCurrentObject: () => {},
  dismissTask: () => {},
  applyTaskDetail: () => {},
};

vi.mock('@/store/workspace-store', () => ({
  useWorkspaceStore: (selector: (state: typeof storeState) => unknown) =>
    selector(storeState),
}));

const chatState = {
  activeSessionId: 'session-a',
  streamingBuffers: {} as Record<string, unknown>,
  messagesBySession: {} as Record<string, unknown>,
};
let skillRunResult: { runs: Array<{ id: string; sessionId: string; steps: Array<{ key: string; label: string; phase: string; taskId: string | null }> }>; error: Error | undefined; isLoading: boolean } = {
  runs: [], error: undefined, isLoading: false,
};

vi.mock('@/store/chat-store', () => ({
  useChatStore: (selector: (state: { activeSessionId: string; streamingBuffers: Record<string, unknown>; messagesBySession: Record<string, unknown> }) => unknown) =>
    selector(chatState),
}));

vi.mock('@/hooks/use-session-skill-runs', () => ({
  useSessionSkillRuns: () => skillRunResult,
}));

it('declares cancel copy and route in the schema', () => {
  expect(msg('ui.workspace.tasks.open')).not.toBe('ui.workspace.tasks.open');
  expect(msg('ui.workspace.tasks.close')).not.toBe('ui.workspace.tasks.close');
  expect(msg('ui.workspace.tasks.cancel')).not.toBe('ui.workspace.tasks.cancel');
  expect(msg('ui.workspace.tasks.cancel_confirm')).not.toBe(
    'ui.workspace.tasks.cancel_confirm',
  );
  expect(msg('ui.workspace.tasks.cancel_failed')).not.toBe(
    'ui.workspace.tasks.cancel_failed',
  );
  expect(defaultCatalog.application.runtime.http.routes.task_cancel).toContain(
    '{taskID}',
  );
});

it('declares task completion notification controls in the schema', () => {
  for (const key of [
    'ui.workspace.notice.enable',
    'ui.workspace.notice.requesting',
    'ui.workspace.notice.enabled',
    'ui.workspace.notice.denied',
  ]) {
    expect(msg(key)).not.toBe(key);
  }
});

it('renders cancel only for running tasks', () => {
  const html = renderToStaticMarkup(<><TaskCenterButton /><TaskDrawer /></>);
  // #670: the label counts the running task the badge shows (one of the two listed), with its unit.
  const running = msg('ui.workspace.tasks.running_count').replace('{count}', '1');
  const openLabel = `${msg('ui.workspace.tasks.open')}（${running}，${msg('ui.workspace.tasks.progress')} 40${defaultCatalog.application.runtime.frontend.percentage_suffix}）`;
  expect(html).toContain(`aria-label="${openLabel}"`);
  // The icon's progress track reflects the active task's normalized progress.
  expect(html).toContain('style="width:40%"');
  expect(html).toContain('data-testid="workspace-active-count"');
  const cancelLabel = `aria-label="${msg('ui.workspace.tasks.cancel')}"`;
  const dismissLabel = `aria-label="${msg('ui.workspace.tasks.dismiss')}"`;
  expect(html.split(cancelLabel).length - 1).toBe(1);
  expect(html.split(dismissLabel).length - 1).toBe(1);
  expect(html).not.toContain('foreign-running');
  expect(html).not.toContain('unknown-running');
});

it('shows unbound SkillRun steps, and no row for the pending Agent turn (#546)', () => {
  chatState.messagesBySession = { 'session-a': [{ role: 'assistant', isPending: true }] };
  skillRunResult = {
    runs: [{ id: 'run-a', sessionId: 'session-a', steps: [
      { key: 'front', label: '正面', phase: 'grant_pending', taskId: null },
      { key: 'side', label: '侧面', phase: 'task_bound', taskId: 'running' },
    ] }],
    error: undefined,
    isLoading: false,
  };
  try {
    const html = renderToStaticMarkup(<><TaskCenterButton /><TaskDrawer /></>);
    expect(html).not.toContain('workspace-agent-progress');
    expect(html).toContain('正面');
    expect(html).toContain(msg('ui.workspace.tasks.skill_phase.grant_pending'));
    expect(html).toContain(msg('ui.workspace.tasks.skill_checkpoint'));
    expect(html).not.toContain('侧面');
    expect(html.split('role="progressbar"').length - 1).toBe(2);
  } finally {
    chatState.messagesBySession = {};
    skillRunResult = { runs: [], error: undefined, isLoading: false };
  }
});

// #546: the drawer lists the providers' Tasks only. An Agent turn that is thinking is the chat's own "generating"
// line; it is no row here, with or without a tracked Task, and it adds nothing to the count on the button.
it('shows no row for a thinking Agent turn without a tracked Task', () => {
  const previous = storeState.trackedTasks;
  storeState.trackedTasks = [{ ...trackedTask('foreign-running', 'RUNNING', false), sessionId: 'session-b' }];
  chatState.streamingBuffers = { 'session-a': { genState: { statusText: 'configured tool status', thinking: true } } };
  chatState.messagesBySession = { 'session-a': [{ role: 'assistant', isPending: true }] };
  try {
    const html = renderToStaticMarkup(<><TaskCenterButton /><TaskDrawer /></>);
    expect(html).not.toContain('workspace-agent-progress');
    expect(html).not.toContain('configured tool status');
    expect(html).not.toContain('workspace-agent-no-task');
    expect(html).toContain('data-testid="workspace-task-empty"');
    expect(html).not.toContain('role="progressbar"');
    // The button names no listed item either.
    expect(html).toContain(`aria-label="${msg('ui.workspace.tasks.open')}"`);
  } finally {
    storeState.trackedTasks = previous;
    chatState.streamingBuffers = {};
    chatState.messagesBySession = {};
  }
});

it('lists the Task rows alone while the Agent turn thinks', () => {
  chatState.streamingBuffers = { 'session-a': { genState: { statusText: 'configured tool status' } } };
  try {
    const html = renderToStaticMarkup(<TaskDrawer />);
    expect(html).not.toContain('workspace-agent-progress');
    expect(html).not.toContain('configured tool status');
    expect(html).toContain('data-testid="workspace-task-card"');
  } finally {
    chatState.streamingBuffers = {};
  }
});

it('marks a task that waits in a queue, as the Schema states and the server notice say, and offers the queue filter', () => {
  const previous = storeState.trackedTasks;
  storeState.trackedTasks = [
    trackedTask('queued', 'QUEUED', false),
    trackedTask('retry', 'RETRY_PENDING', false),
    { ...trackedTask('provider-queue', 'RUNNING', false), queueNotice: { reason: 'upstream_queued', detail: msg('task.queue_reason.upstream_queued') } },
    trackedTask('running', 'RUNNING', false),
  ];
  try {
    const html = renderToStaticMarkup(<TaskDrawer />);
    const queued = [...html.matchAll(/data-testid="workspace-task-card" data-queued="true"/g)];
    expect(queued).toHaveLength(3);
    expect(html).toContain(msg('task.queue_reason.upstream_queued'));
    expect(html).toContain('data-testid="workspace-task-filter-queued"');
    expect(html).toContain(`>${msg('ui.workspace.tasks.filter.queued')}<`);
  } finally {
    storeState.trackedTasks = previous;
  }
});

it('declares the queue filter in the schema in Chinese and English, and no Agent-row texts any more', () => {
  const bundles = (defaultCatalog.application as unknown as {
    localization: { bundles: Record<string, { messages: Record<string, string>; fields: Record<string, string> }> };
  }).localization.bundles;
  expect(msg('ui.workspace.tasks.filter.queued')).toBe('排队中');
  expect(bundles.en.messages['ui.workspace.tasks.filter.queued']).toBe('Queued');
  expect(catalogMessages?.['ui.workspace.tasks.agent_no_task']).toBeUndefined();
  expect(catalogMessages?.['ui.workspace.tasks.agent_preparing']).toBeUndefined();
});

it('keeps durable SkillRun checkpoints visible without an activity count or invented percentage', () => {
  const originalTasks = storeState.trackedTasks;
  storeState.trackedTasks = [];
  skillRunResult = {
    runs: [{ id: 'run-a', sessionId: 'session-a', steps: [
      { key: 'front', label: '正面', phase: 'grant_pending', taskId: null },
      { key: 'side', label: '侧面', phase: 'task_bound', taskId: 'task-missing-from-tray' },
    ] }],
    error: undefined,
    isLoading: false,
  };
  try {
    const html = renderToStaticMarkup(<><TaskCenterButton /><TaskDrawer /></>);
    expect(html).toContain('正面');
    expect(html).toContain('侧面');
    expect(html.split(msg('ui.workspace.tasks.skill_checkpoint')).length - 1).toBe(2);
    expect(html).toContain(`aria-label="${msg('ui.workspace.tasks.open')}"`);
    expect(html).not.toContain('data-testid="workspace-active-count"');
    expect(html).not.toContain('role="progressbar"');
  } finally {
    storeState.trackedTasks = originalTasks;
    skillRunResult = { runs: [], error: undefined, isLoading: false };
  }
});


it('keeps a report-only successful Task as a file row, with no empty image preview', () => {
  const previous = storeState.trackedTasks;
  const file = { id: 'report', role: 'report', variant: 'json', delivery: 'inline', ordinal: 0, mediaType: 'application/vnd.art-ai.print-report+json', size: 900, url: '/api/v1/tasks/report-task/assets/report', kind: 'other' as const };
  storeState.trackedTasks = [{ ...trackedTask('report-task', 'SUCCEEDED', true), capability: 'print-analysis', assets: [file], downloadables: [file] }];
  try {
    const html = renderToStaticMarkup(<TaskDrawer />);
    expect(html).toContain('workspace-task-attachment-icon'); expect(html).toContain('report-task');
    expect(html).not.toContain('<img'); expect(html).not.toContain('src=""');
  } finally { storeState.trackedTasks = previous; }
});


it.each([['clip','model/fbx'],['print-archive','model/3mf']])('shows projected %s as an attachment Task without a model or empty image preview', (role, media_type) => {
  const previous = storeState.trackedTasks;
  const projection = projectNativeTaskDetail({ state: 'SUCCEEDED', progress: 100, assets: [{ id: 'file', role, media_type, size: 400, url: '/api/v1/tasks/file-task/assets/file' }] }, (defaultCatalog.application as unknown as ProductSchema).task_states, 100)!;
  storeState.trackedTasks = [{ ...trackedTask('file-task','SUCCEEDED',true), ...projection }];
  try {
    expect(projection.assets[0].kind).toBe('other');
    const html = renderToStaticMarkup(<TaskDrawer />);
    expect(html).toContain('workspace-task-attachment-icon'); expect(html).not.toContain('<img'); expect(html).not.toContain('src=""');
  } finally { storeState.trackedTasks = previous; }
});

it('says on the task row what a succeeded task did not deliver, and says nothing for a complete or failed one (#284)', () => {
  const previous = storeState.trackedTasks;
  const detail = '第 2 张、第 4 张没有处理成功，其余已完成。';
  storeState.trackedTasks = [
    { ...trackedTask('partial-task', 'SUCCEEDED', true), notices: [{ code: 'partial_inputs', detail }] },
    trackedTask('complete-task', 'SUCCEEDED', true),
    { ...trackedTask('failed-task', 'FAILED', true), failure: { detail: '上游拒绝了请求', retryable: false, failureClass: 'rejected' } as TrackedTask['failure'], notices: [{ code: 'partial_inputs', detail }] },
  ];
  try {
    const html = renderToStaticMarkup(<TaskDrawer />);
    expect(html).toContain('data-testid="workspace-task-notice"');
    expect(html).toContain('data-notice-code="partial_inputs"');
    // Only the succeeded task that holds the notice says it; the failed one carries one too but is a failure.
    expect(html.split(detail).length - 1).toBe(1);
    expect(html).toContain('partial-task');
    expect(html).toContain('complete-task');
  } finally { storeState.trackedTasks = previous; }
});
