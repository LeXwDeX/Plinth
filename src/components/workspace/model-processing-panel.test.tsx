import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProcessingPlan } from '@/lib/workspace/processing-plan';
import { ModelProcessingPanel } from './model-processing-panel';

const mock = vi.hoisted(() => ({
  plan: undefined as ProcessingPlan | undefined,
  available: [] as Array<{ id: string; label_cn: string }>,
  actions: new Map<string, () => unknown>(),
  fetch: vi.fn(),
  reload: vi.fn(),
}));
const messages = defaultCatalog.application.content.messages;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: object) => unknown) => select({ activeSessionId: 'session' }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (select: (state: object) => unknown) => select({ registerTask: vi.fn() }) }));
vi.mock('@/hooks/use-model-processing-capabilities', () => ({ useModelProcessingCapabilities: () => ({
  available: mock.available, offerings: { remesh: [{ id: 'remesh', capability: 'remesh', revision: 'v2', available: true, estimated_credits: 12, credits_metered: true }] }, refresh: vi.fn(),
}) }));
vi.mock('swr', () => ({ default: (key: string | unknown[]) => Array.isArray(key)
  ? { data: { id: 'source', session_id: 'session', provider_id: 'meshy', state: 'SUCCEEDED', assets: [{ id: 'asset', kind: 'model' }] } }
  : { data: mock.plan ? [mock.plan] : [], mutate: mock.reload },
}));
vi.mock('@/components/ui/button', () => ({ Action: ({ children, onClick, disabled, loading }: { children: React.ReactNode; onClick: () => unknown; disabled?: boolean; loading?: boolean }) => {
  mock.actions.set(String(children), onClick);
  return <button disabled={disabled || loading}>{children}</button>;
} }));

function locked(): ProcessingPlan {
  return { id: 'plan', source_task_id: 'source', source_asset_id: 'asset', state: 'PAUSED', version: 7, failure: 'plan_submit_unreconciled',
    steps: [{ capability: 'remesh', offering_id: 'remesh', revision: 'v1', estimated_credits: 5, state: 'PAUSED', failure: 'plan_submit_unreconciled', resumable: false }] };
}
function render() {
  return renderToStaticMarkup(<ModelProcessingPanel selection={{ kind: 'model', taskId: 'source', assetId: 'asset', url: '/model.glb', sessionId: 'session' }} />);
}
beforeEach(() => {
  mock.plan = locked();
  mock.available = [];
  mock.actions.clear();
  mock.fetch.mockReset();
  mock.reload.mockReset();
  mock.reload.mockImplementation(async (update?: (plans: ProcessingPlan[]) => ProcessingPlan[]) => { if (update && mock.plan) mock.plan = update([mock.plan])[0]; });
  vi.stubGlobal('fetch', mock.fetch);
});

describe('processing panel reconcile action states', () => {
  it('offers 核对, not a paid resume, for the locked step', () => {
    const html = render();
    expect(html).toContain(`>${messages['ui.workspace.processing.plan_reconcile_action']}</button>`);
    expect(html).toContain(messages['ui.workspace.processing.plan_submit_unreconciled']);
    expect(html).not.toContain(messages['ui.workspace.processing.plan_resume']);
    expect(html).toMatch(/disabled=""[^>]*>确认步骤与费用/);
  });
  it.each(['linked', 'cleared'])('the action shows the %s result, without a paid API call', async (kind) => {
    const result = locked(); result.version++;
    if (kind === 'linked') {
      result.state = 'RUNNING'; result.failure = undefined;
      result.steps[0] = { ...result.steps[0], state: 'RUNNING', failure: undefined, task_id: 'original-task' };
    } else {
      result.failure = 'plan_reconciled_absent';
      result.steps[0] = { ...result.steps[0], failure: result.failure, resumable: true };
    }
    mock.fetch.mockResolvedValue({ ok: true, json: async () => result });
    render();
    mock.actions.get(messages['ui.workspace.processing.plan_reconcile_action'])!();
    await vi.waitFor(() => expect(mock.plan?.version).toBe(8));
    const html = render();
    expect(html).not.toContain(`>${messages['ui.workspace.processing.plan_reconcile_action']}</button>`);
    // 静态渲染不保留点击后的组件状态；“已关联原任务”仅在本面板核对后显示，下方另测普通运行中计划不显示。
    if (kind === 'cleared') expect(html).toContain(messages['ui.workspace.processing.plan_reconciled_absent']);
    else expect(html).toContain(messages['ui.workspace.processing.plan_step_running']);
    if (kind === 'cleared') expect(html).toContain(`${messages['ui.workspace.processing.plan_resume']} · 12`);
    expect(mock.fetch).toHaveBeenCalledExactlyOnceWith(defaultCatalog.application.runtime.http.routes.processing_plan_reconcile.replace('{planID}', 'plan'), {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ expected_plan_version: 7 }),
    });
  });
  it('an ordinarily submitted running plan does not claim a reconciliation link', () => {
    mock.plan = { ...locked(), state: 'RUNNING', failure: undefined,
      steps: [{ ...locked().steps[0], state: 'RUNNING', failure: undefined, task_id: 'fresh-task' }] };
    const html = render();
    expect(html).not.toContain(messages['ui.workspace.processing.plan_reconcile_linked']);
    expect(html).toContain(messages['ui.workspace.processing.plan_step_running']);
  });
  it('a frozen mismatch shows the observation-only warning and no resume', () => {
    mock.plan!.failure = 'plan_reconciled_mismatch';
    mock.plan!.steps[0].failure = 'plan_reconciled_mismatch';
    mock.plan!.steps[0].task_id = 'original-task';
    const html = render();
    expect(html).toContain(messages['ui.workspace.processing.plan_reconciled_mismatch']);
    expect(html).not.toContain(messages['ui.workspace.processing.plan_resume']);
    expect(html).not.toContain(`>${messages['ui.workspace.processing.plan_reconcile_action']}</button>`);
  });
  it('an undetermined answer stays locked and re-reads the plan, never submits', async () => {
    mock.fetch.mockResolvedValue({ ok: false, json: async () => ({ detail: messages['ui.workspace.processing.plan_reconcile_undetermined'] }) });
    render();
    mock.actions.get(messages['ui.workspace.processing.plan_reconcile_action'])!();
    await vi.waitFor(() => expect(mock.reload).toHaveBeenCalled());
    expect(mock.plan).toEqual(locked());
    expect(render()).toContain(`>${messages['ui.workspace.processing.plan_reconcile_action']}</button>`);
    expect(mock.fetch).toHaveBeenCalledOnce();
  });
});

describe('an expired original task (#325)', () => {
  it('names the reason and offers no paid retry', () => {
    mock.plan = { id: 'plan', source_task_id: 'source', source_asset_id: 'asset', state: 'FAILED', version: 3, failure: 'plan_source_expired',
      steps: [{ capability: 'remesh', offering_id: 'remesh', revision: 'v2', estimated_credits: 12, state: 'FAILED', failure: 'plan_source_expired', task_id: 'failed-task', retryable: false }] };
    const html = render();
    expect(html).toContain(messages['ui.workspace.processing.plan_source_expired']);
    expect(html).not.toContain(messages['ui.workspace.processing.plan_retry']);
  });
});

// #326: a plan that failed before #325 is judged again under today's rules by the server.
describe('an old failed plan judged again (#326)', () => {
  const failed = (failure: string, retryable: boolean): ProcessingPlan => ({ id: 'plan', source_task_id: 'source', source_asset_id: 'asset', state: 'FAILED', version: 3, failure,
    steps: [{ capability: 'remesh', offering_id: 'remesh', revision: 'v2', estimated_credits: 12, state: 'FAILED', failure, task_id: 'failed-task', retryable }] });
  it('a retry certain to fail is not offered, and says why', () => {
    mock.plan = failed('plan_retry_source_expired', false);
    const html = render();
    expect(html).toContain(messages['ui.workspace.processing.plan_retry_source_expired']);
    expect(html).not.toContain(messages['ui.workspace.processing.plan_retry']);
  });
  it('a retry that now uses the saved file is offered with that reason', () => {
    mock.plan = failed('plan_retry_saved_file', true);
    const html = render();
    expect(html).toContain(messages['ui.workspace.processing.plan_retry_saved_file']);
    expect(html).toContain(messages['ui.workspace.processing.plan_retry']);
  });
});

// #326 P: a paused plan is judged by the server before a paid continue is offered.
describe('a paused plan judged again (#326)', () => {
  const paused = (state: string, failure: string, resumable: boolean): ProcessingPlan => ({ id: 'plan', source_task_id: 'source', source_asset_id: 'asset', state, version: 4, failure,
    steps: [{ capability: 'remesh', offering_id: 'remesh', revision: 'v2', estimated_credits: 12, state: state === 'FAILED' ? 'FAILED' : 'PAUSED', failure, resumable,
      prepared_input_id: 'prepared', plan_step_id: '0:0' }] });
  const checkbox = (html: string) => html.match(/<input[^>]*type="checkbox"[^>]*>/)?.[0] ?? '';
  beforeEach(() => { mock.available = [{ id: 'remesh', label_cn: '重拓扑' }]; });

  it('a continue certain to fail is not offered, says why, and the model is free for another plan', () => {
    mock.plan = paused('FAILED', 'plan_resume_source_expired', false);
    const html = render();
    expect(html).toContain(messages['ui.workspace.processing.plan_resume_source_expired']);
    expect(html).not.toContain(messages['ui.workspace.processing.plan_prepare_step']);
    expect(html).not.toContain(messages['ui.workspace.processing.plan_resume']);
    expect(html).not.toContain(messages['ui.workspace.processing.plan_retry']);
    expect(checkbox(html)).not.toContain('disabled');
  });
  it('a step to freeze again on the saved file says so and continues at the current price, without holding the model', () => {
    mock.plan = paused('PAUSED', 'plan_resume_saved_file', true);
    const html = render();
    expect(html).toContain(messages['ui.workspace.processing.plan_resume_saved_file']);
    expect(html).toContain(`${messages['ui.workspace.processing.plan_resume']} · 12`);
    expect(html).not.toContain(messages['ui.workspace.processing.plan_prepare_step']);
    expect(html).toContain(messages['ui.workspace.processing.plan_waiting_alternative']);
    expect(checkbox(html)).not.toContain('disabled');
  });
  it('a step waiting for consent can be confirmed, or left for another plan', () => {
    mock.plan = paused('PAUSED', 'plan_consent_required', true);
    const html = render();
    expect(html).toContain(messages['ui.workspace.processing.plan_prepare_step']);
    expect(html).toContain(messages['ui.workspace.processing.plan_waiting_alternative']);
    expect(checkbox(html)).not.toContain('disabled');
  });
  it('a pause with paid work possibly outstanding still holds the model', () => {
    mock.plan = locked();
    const html = render();
    expect(checkbox(html)).toContain('disabled');
    expect(html).not.toContain(messages['ui.workspace.processing.plan_waiting_alternative']);
  });
});
