// @vitest-environment jsdom

import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const requestRetry = vi.fn();
vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: { runtime: { frontend: { percentage_multiplier: 100 } } }, text: (key: string) => key }),
}));
vi.mock('@/components/workspace/canvas-image-actions', () => ({ submitDirectTask: vi.fn() }));
vi.mock('@/lib/workspace/task-retry', () => ({
  requestRetry: (...args: unknown[]) => requestRetry(...args),
  RetryRefused: class RetryRefused extends Error {},
}));
vi.mock('sonner', () => ({ toast: { success: vi.fn(), message: vi.fn(), error: vi.fn() } }));

import { useTaskRetry, useTaskRetryStore } from './use-task-retry';
import type { TrackedTask } from '@/store/workspace-store';

const failed = { taskId: 't1', sessionId: 's1', capability: 'image-to-3d', prompt: 'p' } as TrackedTask;

afterEach(() => {
  cleanup();
  requestRetry.mockReset();
  useTaskRetryStore.setState({ retrying: {}, awaiting: {}, retried: {} });
});

describe('the retry of a failed task shared by the task center and the chat card (#665)', () => {
  it('a double click, or a click in both places, submits one retry', async () => {
    let answer!: (value: unknown) => void;
    requestRetry.mockReturnValue(new Promise((resolve) => { answer = resolve; }));
    const center = renderHook(() => useTaskRetry());
    const card = renderHook(() => useTaskRetry());
    await act(async () => {
      void center.result.current.retryTask(failed);
      void center.result.current.retryTask(failed);
      void card.result.current.retryTask(failed);
    });
    expect(requestRetry).toHaveBeenCalledTimes(1);
    expect(card.result.current.retrying.t1).toBe(true);
    await act(async () => { answer({ status: 'submitted', taskId: 't2' }); });
    // The chat card sees the retry the task center started and no longer offers it.
    expect(card.result.current.retried.t1).toBe('t2');
    await act(async () => { await card.result.current.retryTask(failed); });
    expect(requestRetry).toHaveBeenCalledTimes(1);
  });
});
