import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { HarnessSelection, HarnessView } from '@/lib/harness';
import { HarnessSummary, harnessTurnDiffers } from './harness-summary';
const mock = vi.hoisted(() => ({ view: null as HarnessView | null }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog.application, text: (key: string) => ({ 'ui.harness.current_summary': '当前：{operation}', 'ui.harness.turn_summary': '本轮：{operation}', 'ui.harness.next_turn': '下轮生效' }[key] ?? key) }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: mock.view, busy: false }) }));
const selection: HarnessSelection = { session_id: 's1', revision: 2, digest: 'current', operation_id: 'remesh', enabled_operations: [], sources: [], fixed_parameters: {}, fillable_parameters: [], allow_prerequisites: false, updated_at: '' };
beforeEach(() => { mock.view = { selection, status: 'ready', reason: '', missing_parameters: [], can_propose: false, estimated_credits: 0 }; });
describe('Harness summary', () => {
  it('uses the public operation label', () => {
    const html = renderToStaticMarkup(<HarnessSummary sessionId="s1" />);
    expect(html).toContain('当前：重拓扑');
    expect(html).not.toContain('current');
    expect(html).not.toContain('本轮');
  });
  it('keeps the frozen turn separate when a new selection is saved', () => {
    mock.view!.turn = { assistant_message_id: 'private-message-id', selection: { ...selection, digest: 'old', operation_id: 'rigging' } };
    expect(harnessTurnDiffers(mock.view!)).toBe(true);
    const html = renderToStaticMarkup(<HarnessSummary sessionId="s1" />);
    expect(html).toContain('当前：重拓扑');
    expect(html).toContain('本轮：绑骨');
    expect(html).toContain('下轮生效');
    expect(html).not.toContain('private-message-id');
  });
  it('keeps an older turn binding visible after identical-content CAS or A-to-B-to-A updates', () => {
    mock.view!.turn = { assistant_message_id: 'm', selection: { ...selection, revision: 1 } };
    expect(harnessTurnDiffers(mock.view!)).toBe(true);
    const html = renderToStaticMarkup(<HarnessSummary sessionId="s1" />);
    expect(html).toContain('当前：重拓扑');
    expect(html).toContain('本轮：重拓扑');
    expect(html).toContain('下轮生效');
  });
  it('drops the A-turn summary once refreshed Harness data binds the next B turn', () => {
    mock.view!.turn = { assistant_message_id: 'assistant-a', selection: { ...selection, revision: 1, digest: 'old', operation_id: 'rigging' } };
    expect(renderToStaticMarkup(<HarnessSummary sessionId="s1" />)).toContain('本轮：绑骨');
    mock.view!.turn = null;
    expect(renderToStaticMarkup(<HarnessSummary sessionId="s1" />)).not.toContain('本轮：绑骨');
    mock.view!.turn = { assistant_message_id: 'assistant-b', selection: { ...selection } };
    const html = renderToStaticMarkup(<HarnessSummary sessionId="s1" />);
    expect(html).toContain('当前：重拓扑');
    expect(html).not.toContain('本轮：绑骨');
    expect(html).not.toContain('下轮生效');
  });

  it('does not show a stale-turn notice for the same revision and digest', () => {
    mock.view!.turn = { assistant_message_id: 'm', selection: { ...selection } };
    expect(harnessTurnDiffers(mock.view!)).toBe(false);
    expect(renderToStaticMarkup(<HarnessSummary sessionId="s1" />)).not.toContain('下轮生效');
  });
});
