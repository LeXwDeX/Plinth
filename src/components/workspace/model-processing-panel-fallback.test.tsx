// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProcessingPlan } from '@/lib/workspace/processing-plan';
import { ModelProcessingPanel } from './model-processing-panel';

const messages = defaultCatalog.application.content.messages as Record<string, string>;
const offerings = { remesh: [{ id: 'remesh', capability: 'remesh', revision: 'v2', available: true, estimated_credits: 12, credits_metered: true }] };
const mock = vi.hoisted(() => ({
  plan: undefined as ProcessingPlan | undefined,
  schema: undefined as unknown,
  fetch: vi.fn(),
}));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema }) }));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: object) => unknown) => select({ activeSessionId: 'session', sessions: [], sessionListRevision: 0 }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (select: (state: object) => unknown) => select({ registerTask: vi.fn() }) }));
vi.mock('@/hooks/use-model-processing-capabilities', () => ({ useModelProcessingCapabilities: () => ({
  available: [], offerings, refresh: async () => offerings,
}) }));
vi.mock('swr', () => ({ default: (key: string | unknown[]) => Array.isArray(key)
  ? { data: { id: 'source', session_id: 'session', provider_id: 'meshy', state: 'SUCCEEDED', assets: [{ id: 'asset', kind: 'model' }] } }
  : { data: mock.plan ? [mock.plan] : [], mutate: vi.fn() },
}));
// The tooltip opens on hover; the reason it would show is what this file checks.
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ reason, children }: { reason?: string; children: React.ReactNode }) => <span data-testid="hint" data-reason={reason}>{children}</span>,
}));

function show() {
  return render(<ModelProcessingPanel selection={{ kind: 'model', taskId: 'source', assetId: 'asset', url: '/model.glb', sessionId: 'session' }} />);
}

beforeEach(() => {
  mock.schema = defaultCatalog.application;
  mock.fetch.mockReset();
  vi.stubGlobal('fetch', mock.fetch);
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

// #670: a refused request without a server sentence falls back to the Schema copy, never "HTTP 500".
describe('processing plan failure fallbacks', () => {
  it('says the submission failed instead of the HTTP status when a retry is refused without a reason', async () => {
    mock.plan = { id: 'plan', source_task_id: 'source', source_asset_id: 'asset', state: 'FAILED', version: 3, failure: 'plan_retry_saved_file',
      steps: [{ capability: 'remesh', offering_id: 'remesh', revision: 'v2', estimated_credits: 12, state: 'FAILED', failure: 'plan_retry_saved_file', task_id: 'failed-task', retryable: true }] };
    mock.fetch.mockResolvedValue({ ok: false, status: 500, json: async () => ({}) });
    show();
    fireEvent.click(screen.getByText((_, element) => element?.tagName === 'BUTTON' && (element.textContent ?? '').startsWith(messages['ui.workspace.processing.plan_retry'])));
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toBe(messages['ui.workspace.processing.submit_failed']);
    expect(alert.textContent).not.toContain('HTTP');
  });

  it('names a reason for the checkpoint continue when the resume route is missing', () => {
    const application = JSON.parse(JSON.stringify(defaultCatalog.application));
    application.runtime.http.routes.processing_plan_resume = '';
    mock.schema = application;
    mock.plan = {
      id: 'plan', source_task_id: 'source', source_asset_id: 'asset', state: 'PAUSED', version: 8, failure: 'plan_checkpoint_failed',
      steps: [{ capability: 'remesh', offering_id: 'remesh', revision: 'v2', estimated_credits: 5, state: 'SUCCEEDED', failure: 'plan_checkpoint_failed',
        checkpoint: { state: 'failed', checks: [{ check: 'triangles', maximum: 60000 }], results: [{ check: 'triangles', status: 'fail', actual: 100000 }] } }],
    };
    show();
    const button = screen.getByText(messages['ui.workspace.processing.plan_continue_checkpoint']).closest('button')!;
    const hint = button.closest('[data-testid="hint"]');
    expect(hint?.getAttribute('data-reason')).toBe(messages['ui.workspace.processing.unavailable_reason.unavailable']);
  });
});
