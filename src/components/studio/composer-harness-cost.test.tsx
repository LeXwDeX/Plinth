import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from './composer';
import type { HarnessView } from '@/lib/harness';
import { formatSchemaText } from '@/lib/product-schema';

const mock = vi.hoisted(() => ({ sessionId: 's1' as string | null, ownerId: 'owner', view: null as HarnessView | null, busy: false, error: null as unknown, hook: vi.fn() }));
const messages = catalog.application.content.messages;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog.application }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: mock.ownerId ? { id: mock.ownerId } : null }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: (sessionId: string | null, ownerId: string) => { mock.hook(sessionId, ownerId); return { view: mock.view, busy: mock.busy, error: mock.error }; } }));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: object) => unknown) => select({ activeSessionId: mock.sessionId }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/studio/model-picker', () => ({ ModelPicker: () => null, GenerationModelNotice: () => null }));
vi.mock('@/components/studio/skill-picker', () => ({ SkillPicker: () => null }));
vi.mock('@/components/studio/agent-scope-picker', () => ({ AgentScopePicker: () => null }));
vi.mock('@/components/studio/params-popover', () => ({ ParamsPopover: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
// Render the tooltip payload as data so its hidden cost is verified with the visible button and aria label.
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ name, cost, children }: { name: string; cost?: { text: string }; children: React.ReactNode }) => <span data-hint-name={name} data-hint-cost={cost?.text}>{children}</span> }));
const noop = () => undefined;
const props: ComposerProps = {
  value: 'Continue', onChange: noop, onSend: noop, onStop: noop, streaming: false, locked: false, canSend: true,
  chip: null, chipSkipped: false, onToggleChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
  acceptedImages: undefined, onFiles: noop, onPasteImages: noop, editing: false, onCancelEdit: noop, sendCredits: 20, mentions: [],
  params: {} as ComposerProps['params'],
  skill: { sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
};
const render = () => renderToStaticMarkup(<Composer {...props} />);
const sendButton = (html: string) => html.match(/<button[^>]*data-testid="composer-send"[\s\S]*?<\/button>/)?.[0] ?? '';
beforeEach(() => {
  mock.sessionId = 's1'; mock.ownerId = 'owner'; mock.busy = false; mock.error = null; mock.hook.mockClear();
  mock.view = { selection: { session_id: 's1', revision: 1, digest: 'd', enabled_operations: [], sources: [], fixed_parameters: {}, fillable_parameters: [], allow_prerequisites: false, updated_at: '' }, status: 'free', reason: '', missing_parameters: [], can_propose: false, estimated_credits: 0 };
});

describe('composer generation cost with saved Harness settings', () => {
  it.each(['ready', 'needs_input', 'unavailable'] as const)('hides generation cost from the button, aria and tooltip for an active %s function', status => {
    mock.view = { ...mock.view!, selection: { ...mock.view!.selection, operation_id: 'remesh' }, status, estimated_credits: 5 };
    const html = render();
    expect(sendButton(html)).toContain(`aria-label="${messages['ui.workspace.agent.send']}"`);
    expect(html).not.toContain('data-testid="composer-send-cost"');
    expect(html).not.toContain('data-hint-cost=');
    expect(mock.hook).toHaveBeenCalledWith('s1', 'owner');
  });
  it('restores the generation estimate for the current owner/session after the function is cleared', () => {
    const html = render();
    expect(sendButton(html)).toContain(`aria-label="${formatSchemaText(messages['ui.studio.composer.send_with_cost'], { credits: 20 })}"`);
    expect(html).toContain('data-testid="composer-send-cost"');
    expect(html).toContain(`data-hint-cost="${formatSchemaText(messages['ui.studio.composer.send_cost_hint'], { credits: 20 })}"`);
  });
  it.each(['loading', 'error', 'missing', 'other-session', 'no-owner'] as const)('hides estimates while the Harness context is %s', state => {
    if (state === 'loading') mock.busy = true;
    if (state === 'error') mock.error = new Error('load failed');
    if (state === 'missing') mock.view = null;
    if (state === 'other-session') mock.sessionId = 's2';
    if (state === 'no-owner') mock.ownerId = '';
    const html = render();
    expect(html).not.toContain('data-testid="composer-send-cost"');
    expect(html).not.toContain('data-hint-cost=');
    expect(sendButton(html)).toContain(`aria-label="${messages['ui.workspace.agent.send']}"`);
  });
});
