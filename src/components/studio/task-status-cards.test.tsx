import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import type { TrackedTask } from '@/store/workspace-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const state: { tracked: TrackedTask[] } = { tracked: [] };

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema, text: (key: string) => schemaText(schema, key) }),
}));
vi.mock('@/hooks/use-task-retry', () => ({
  useTaskRetry: () => ({ retrying: {}, awaiting: {}, retried: {}, retryTask: async () => undefined }),
}));
// Server rendering reads a store's initial state, so both stores answer from fixed values here.
vi.mock('@/store/chat-store', () => ({
  useChatStore: (selector: (value: { activeSessionId: string }) => unknown) => selector({ activeSessionId: 's1' }),
}));
vi.mock('@/store/workspace-store', () => ({
  useWorkspaceStore: (selector: (value: { trackedTasks: TrackedTask[] }) => unknown) => selector({ trackedTasks: state.tracked }),
}));

import { TaskStatusCards } from './task-status-cards';

const running = (extra: Partial<TrackedTask> = {}): TrackedTask => ({
  taskId: 't1', sessionId: 's1', capability: 'text-to-3d', prompt: 'castle', state: 'RUNNING', progress: 0, progressMaximum: 100,
  terminal: false, failure: null, liveError: '', assets: [], downloadables: [], resources: [], resourcesPending: false,
  notFound: false, updatedAt: Date.now(), ...extra,
});

beforeEach(() => {
  state.tracked = [];
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
});
