// @vitest-environment jsdom
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { cleanup, render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import type { TrackedTask } from '@/store/workspace-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const state: { tracked: TrackedTask[]; session: string } = { tracked: [], session: 's1' };

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema, text: (key: string) => schemaText(schema, key) }),
}));
vi.mock('@/hooks/use-task-retry', () => ({
  useTaskRetry: () => ({ retrying: {}, awaiting: {}, retried: {}, retryTask: async () => undefined }),
}));
// Server rendering reads a store's initial state, so both stores answer from fixed values here.
vi.mock('@/store/chat-store', () => ({
  useChatStore: (selector: (value: { activeSessionId: string }) => unknown) => selector({ activeSessionId: state.session }),
}));
vi.mock('@/store/workspace-store', () => ({
  useWorkspaceStore: (selector: (value: { trackedTasks: TrackedTask[] }) => unknown) => selector({ trackedTasks: state.tracked }),
}));

import { TaskStatusCards } from './task-status-cards';
import { PaidStepNotes } from '@/components/chat/paid-step-notes';
import { roundCreditsText, stepCreditsText } from '@/lib/chat/approval-modes';

const running = (extra: Partial<TrackedTask> = {}): TrackedTask => ({
  taskId: 't1', sessionId: 's1', capability: 'text-to-3d', prompt: 'castle', state: 'RUNNING', progress: 0, progressMaximum: 100,
  terminal: false, failure: null, liveError: '', assets: [], downloadables: [], resources: [], resourcesPending: false,
  notFound: false, updatedAt: Date.now(), ...extra,
});

beforeEach(() => {
  state.tracked = [];
  state.session = 's1';
  cleanup();
});

describe('TaskStatusCards', () => {
  it('shows on the progress card that the provider still holds the task in its queue (#131)', () => {
    // The server resolves the Schema text with the count; the card shows it as is.
    const detail = formatSchemaText(schema.content.messages['task.queue_reason.upstream_queued_ahead'], { ahead: 1313 });
    expect(detail).toBe('上游排队中（前方约 1313 个任务）');
    state.tracked = [running({ queueNotice: { reason: 'upstream_queued', detail } })];
    const html = renderToStaticMarkup(<TaskStatusCards part="running" />);
    expect(html).toContain('data-testid="chat-task-progress"');
    expect(html).toContain('data-testid="chat-task-queue-notice"');
    expect(html).toContain('上游排队中（前方约 1313 个任务）');
  });

  it('shows the plain queue text when the provider reports no count', () => {
    const detail = schema.content.messages['task.queue_reason.upstream_queued'];
    expect(detail).toBe('上游排队中');
    state.tracked = [running({ queueNotice: { reason: 'upstream_queued', detail } })];
    const html = renderToStaticMarkup(<TaskStatusCards part="running" />);
    expect(html).toContain('data-testid="chat-task-queue-notice"');
    expect(html).toContain('>上游排队中<');
  });

  it('shows no queue line once the upstream runs the task', () => {
    state.tracked = [running({ progress: 20 })];
    const html = renderToStaticMarkup(<TaskStatusCards part="running" />);
    expect(html).toContain('data-testid="chat-task-progress"');
    expect(html).not.toContain('chat-task-queue-notice');
  });

  it('draws one light block: a single heading with the batch count, one thin bar per running task (#546)', () => {
    state.tracked = ['a', 'b', 'c'].map((taskId, index) => running({ taskId, capability: 'text-to-image', progress: [0, 50, 90][index] }));
    const html = renderToStaticMarkup(<TaskStatusCards part="running" />);
    expect(html.match(/data-testid="chat-task-batch-title"/g)).toHaveLength(1);
    expect(html).toContain(formatSchemaText(schema.content.messages['ui.studio.task.batch_title'], { done: 0, total: 3 }));
    expect(html.match(/role="progressbar"/g)).toHaveLength(3);
    for (const value of [0, 50, 90]) expect(html).toContain(`aria-valuenow="${value}"`);
    // No card frame, no spinner, no floating layer.
    expect(html).not.toContain('<article');
    expect(html).not.toMatch(/animate-spin|absolute|fixed|sticky|shadow-md|border-border/);
  });

  it('folds an ended task of the batch into one line of text, with no bar', () => {
    const failure = { failureClass: 'provider', detail: '' } as unknown as TrackedTask['failure'];
    state.tracked = [running({ taskId: 'a' }), running({ taskId: 'b' }), running({ taskId: 'c' })];
    const view = render(<TaskStatusCards part="running" />);
    // All three seen running (one batch); then one succeeds and one fails.
    state.tracked = [running({ taskId: 'a', state: 'SUCCEEDED', terminal: true, progress: 100 }), running({ taskId: 'b' }), running({ taskId: 'c', state: 'FAILED', terminal: true, failure })];
    view.rerender(<TaskStatusCards part="running" />);
    const html = view.container.innerHTML;
    expect(html).toContain(formatSchemaText(schema.content.messages['ui.studio.task.batch_title'], { done: 2, total: 3 }));
    expect(html.match(/data-testid="chat-task-ended"/g)).toHaveLength(2);
    expect(html.match(/role="progressbar"/g)).toHaveLength(1);
    expect(html).toContain('data-testid="chat-task-ended"');
    expect(html).toContain(formatSchemaText(schema.content.messages['ui.studio.task.batch_ended'], {
      capability: schema.capabilities.find((item) => item.id === 'text-to-3d')?.label_cn ?? '',
      result: schema.task_states.find((item) => item.internal_state === 'SUCCEEDED')?.label ?? '',
    }));
  });

  it('keeps each session its own batch: switching while both run neither counts nor lists the other session (#565)', () => {
    state.tracked = [running({ taskId: 'a' }), running({ taskId: 'b' })];
    const view = render(<TaskStatusCards part="running" />);
    expect(view.container.innerHTML).toContain(formatSchemaText(schema.content.messages['ui.studio.task.batch_title'], { done: 0, total: 2 }));
    // s1 still runs a and b; the user switches to s2, which runs c.
    state.tracked = [running({ taskId: 'a' }), running({ taskId: 'b' }), running({ taskId: 'c', sessionId: 's2' })];
    state.session = 's2';
    view.rerender(<TaskStatusCards part="running" />);
    let html = view.container.innerHTML;
    expect(html).toContain(formatSchemaText(schema.content.messages['ui.studio.task.batch_title'], { done: 0, total: 1 }));
    expect(html).not.toContain('data-testid="chat-task-ended"');
    expect(html.match(/data-testid="chat-task-progress"/g)).toHaveLength(1);
    // While s2 is in view, a of s1 ends; back on s1, its batch is still a and b.
    state.tracked = [running({ taskId: 'a', state: 'SUCCEEDED', terminal: true }), running({ taskId: 'b' }), running({ taskId: 'c', sessionId: 's2' })];
    view.rerender(<TaskStatusCards part="running" />);
    state.session = 's1';
    view.rerender(<TaskStatusCards part="running" />);
    html = view.container.innerHTML;
    expect(html).toContain(formatSchemaText(schema.content.messages['ui.studio.task.batch_title'], { done: 1, total: 2 }));
    expect(html.match(/data-testid="chat-task-ended"/g)).toHaveLength(1);
    expect(html).toContain('data-task-id="a"');
    expect(html).not.toContain('data-task-id="c"');
  });

  it('shows the queue reason in place of the bar while the provider holds the task', () => {
    state.tracked = [running({ queueNotice: { reason: 'upstream_queued', detail: '上游排队中' } })];
    const html = renderToStaticMarkup(<TaskStatusCards part="running" />);
    expect(html).toContain('>上游排队中<');
    expect(html).not.toContain('role="progressbar"');
  });

  it('renders nothing while no task runs', () => {
    state.tracked = [running({ state: 'SUCCEEDED', terminal: true })];
    expect(renderToStaticMarkup(<TaskStatusCards part="running" />)).toBe('');
  });

  it('with three parallel tasks and their credits, shows one heading, one count, no spinner and one credit line', () => {
    state.tracked = ['a', 'b', 'c'].map((taskId) => running({ taskId }));
    const html = renderToStaticMarkup(
      <>
        <TaskStatusCards part="running" />
        <PaidStepNotes
          steps={['a', 'b', 'c'].map((taskId) => ({ taskId, credits: 6, approvedByPolicy: true }))}
          creditsText={(credits) => stepCreditsText(schema, credits)}
          roundCreditsText={(credits, steps) => roundCreditsText(schema, credits, steps)}
          autoApprovedText={schema.content.messages['ui.workspace.approval.auto_approved_note']}
        />
      </>,
    );
    expect(html.match(/data-testid="chat-task-batch-title"/g)).toHaveLength(1);
    expect(html.match(/0\/3/g)).toHaveLength(1);
    expect(html).not.toMatch(/animate-spin|chat-status-bubble/);
    expect(html.match(/data-testid="paid-step-round"/g)).toHaveLength(1);
    expect(html).not.toContain(stepCreditsText(schema, 6));
    // The task block comes first, the credit line under it.
    expect(html.indexOf('chat-task-running')).toBeLessThan(html.indexOf('paid-step-notes'));
  });
});
