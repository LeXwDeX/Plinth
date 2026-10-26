import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from './composer';
import { agentUnavailableReason, type ProductSchema } from '@/lib/product-schema';

// #253: the public Schema reports a deployment without Agent settings; the composer then keeps the
// draft writable but turns the send off with the Schema's reason.
const mock = vi.hoisted(() => ({ schema: null as unknown }));
const notConfigured = catalog.application.chat.presentation.messages.agent_not_configured;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: null, busy: false, error: null }) }));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: object) => unknown) => select({ activeSessionId: 's1' }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/studio/model-picker', () => ({ ModelPicker: () => null, GenerationModelNotice: () => null }));
vi.mock('@/components/studio/skill-picker', () => ({ SkillPicker: () => null }));
vi.mock('@/components/studio/agent-scope-picker', () => ({ AgentScopePicker: () => null }));
vi.mock('@/components/studio/params-popover', () => ({ ParamsPopover: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ name, children }: { name: string; children: React.ReactNode }) => <span data-hint-name={name}>{children}</span> }));
const noop = () => undefined;
const props: ComposerProps = {
  value: 'Continue', onChange: noop, onSend: noop, onStop: noop, streaming: false, locked: false, canSend: true,
  chip: null, chipSkipped: false, onToggleChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
  acceptedImages: undefined, onFiles: noop, onPasteImages: noop, editing: false, onCancelEdit: noop, sendCredits: null, mentions: [],
  params: {} as ComposerProps['params'],
  skill: { sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
};
const withAgentStatus = (available?: boolean) => ({
  ...catalog.application,
  chat: { ...catalog.application.chat, ...(available === undefined ? {} : { agent_status: { available } }) },
}) as unknown as ProductSchema;
const sendButton = (html: string) => html.match(/<button[^>]*data-testid="composer-send"[^>]*>/)?.[0] ?? '';
const textarea = (html: string) => html.match(/<textarea[^>]*>/)?.[0] ?? '';

beforeEach(() => { mock.schema = withAgentStatus(); });

describe('composer while the Agent is not configured', () => {
  it('declares the reason in the Schema', () => {
    expect(notConfigured).toBeTruthy();
    expect(agentUnavailableReason(withAgentStatus(false))).toBe(notConfigured);
  });
  it('shows the reason and turns the send off, keeping the draft writable', () => {
    mock.schema = withAgentStatus(false);
    const html = renderToStaticMarkup(<Composer {...props} />);
    expect(html).toContain('data-testid="composer-agent-unavailable"');
    expect(html).toContain(notConfigured);
    expect(sendButton(html)).toContain('disabled=""');
    expect(html).toContain(`data-hint-name="${notConfigured}"`);
    expect(textarea(html)).not.toContain(' disabled=""');
  });
  it.each([true, undefined])('keeps the send on while the Agent is available (agent_status %s)', available => {
    mock.schema = withAgentStatus(available);
    expect(agentUnavailableReason(mock.schema as ProductSchema)).toBeNull();
    const html = renderToStaticMarkup(<Composer {...props} />);
    expect(html).not.toContain('data-testid="composer-agent-unavailable"');
    expect(sendButton(html)).not.toContain(' disabled=""');
  });
});

// #329 follow-up A: with more pictures attached than the next turn's 3D model takes, the composer says only the
// first is used, in the Schema's words, and keeps every picture attached and the send on.
describe('composer reference limit', () => {
  it('says which pictures the model uses and keeps them all attached', () => {
    vi.stubGlobal('React', React);
    const images = ['data:image/png;base64,AAAA', 'data:image/png;base64,BBBB'];
    const html = renderToStaticMarkup(<Composer {...props} images={images} referenceLimit={{ model: 'Meshy 7.1', count: 1, total: 2 }} />);
    // #329 note 99647: the same Schema text as the task API, the approval card and the finished message.
    const text = catalog.application.runtime.generation_messages.reference_limit_one.replace('{model}', 'Meshy 7.1').replace('{total}', '2');
    expect(html).toContain('data-testid="composer-reference-limit"');
    expect(html).toContain(text);
    expect(html.match(/data-testid="composer-attachment-chip"/g)).toHaveLength(2);
    expect(sendButton(html)).not.toContain(' disabled=""');
    expect(renderToStaticMarkup(<Composer {...props} images={images} />)).not.toContain('composer-reference-limit');
  });
});
