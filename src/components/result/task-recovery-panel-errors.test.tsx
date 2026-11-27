// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { SWRConfig, unstable_serialize } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { RequestFailure } from '@/lib/problem-reference';

const schema = defaultCatalog.application as unknown as ProductSchema;
const submitDirectTask = vi.hoisted(() => vi.fn());
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema, text: (key: string) => key }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (selector: (state: { registerTask: () => void }) => unknown) => selector({ registerTask: vi.fn() }) }));
vi.mock('@/components/workspace/canvas-image-actions', () => ({ submitDirectTask }));
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { TaskRecoveryPanel } from './task-recovery-panel';

const text = schema.task_history.asset_recovery!;
const eligible = {
  sourceTaskId: 'source-1', eligible: true, receiptDigest: 'digest', sessionId: 'session-1',
  capability: 'asset-recovery', idempotencyKey: 'key',
};
const grant = {
  preparedInputId: 'prepared-1', requestId: 'request-1', key: 'idem-1', sessionId: 'session-1',
  offeringId: 'offering-1', capability: 'asset-recovery', prompt: 'chair', grantApproved: false,
};

function show() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), fallback: { [unstable_serialize(['task-recovery', 'session-1', 'source-1'])]: eligible } }}>
      <TaskRecoveryPanel taskId="source-1" sessionId="session-1" capability="text-to-3d" prompt="chair" onOpenTask={vi.fn()} />
    </SWRConfig>,
  );
}

beforeEach(() => {
  sessionStorage.setItem(`art-ai:asset-recovery:session-1:source-1`, JSON.stringify(grant));
  submitDirectTask.mockReset();
});
afterEach(() => { cleanup(); sessionStorage.clear(); vi.unstubAllGlobals(); });

// #670: a failed recovery decision or submission shows the Schema copy, never internal English or HTTP codes.
describe('TaskRecoveryPanel failure text', () => {
  it('says the submission failed when the deny decision is refused', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 500, json: async () => ({}) })));
    show();
    fireEvent.click(screen.getByTestId('task-recovery-deny'));
    const alert = await screen.findByTestId('task-recovery-error-message');
    expect(alert.textContent).toBe(text.submit_failed);
    expect(alert.textContent).not.toMatch(/grant decision|500/);
  });

  it('says the submission failed when the new task response is malformed', async () => {
    submitDirectTask.mockResolvedValue('');
    show();
    fireEvent.click(screen.getByTestId('task-recovery-approve'));
    const alert = await screen.findByTestId('task-recovery-error-message');
    expect(alert.textContent).toBe(text.submit_failed);
    expect(alert.textContent).not.toContain('malformed');
  });

  it('hides an HTTP status but keeps a reason the server wrote for people', async () => {
    submitDirectTask.mockRejectedValueOnce(new RequestFailure('HTTP 500'));
    const first = show();
    fireEvent.click(screen.getByTestId('task-recovery-approve'));
    expect((await screen.findByTestId('task-recovery-error-message')).textContent).toBe(text.submit_failed);
    first.unmount();
    submitDirectTask.mockRejectedValueOnce(new RequestFailure('internal', '', '余额不足，请充值后重试'));
    show();
    fireEvent.click(screen.getByTestId('task-recovery-approve'));
    expect((await screen.findByTestId('task-recovery-error-message')).textContent).toBe('余额不足，请充值后重试');
  });
});
