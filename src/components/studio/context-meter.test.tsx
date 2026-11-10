import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { formatSchemaText, type ProductSchema } from '@/lib/product-schema';
import type { ChatContextUsage } from '@/lib/chat/browser-client';
import { ContextMeter, contextLevel, toolCallLevel } from '@/components/studio/context-meter';
import { PICKER_UPPERCASE_CLASS } from '@/components/studio/table-popover';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

const catalog = defaultCatalog.application as unknown as ProductSchema;
const messages = catalog.content.messages;
const mock = vi.hoisted(() => ({ schema: null as unknown }));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ name, hint, reason, className, children }: { name: string; hint?: string; reason?: string; className?: string; children: React.ReactNode }) => (
    <span data-hint-name={name} data-hint={hint ?? ''} data-reason={reason ?? ''} data-wrapper-class={className ?? ''}>{children}</span>
  ),
}));
vi.mock('@/store/chat-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/chat-store')>();
  return { ...actual, useChatStore: Object.assign((selector: (state: ReturnType<typeof actual.useChatStore.getState>) => unknown) => selector(actual.useChatStore.getState()), actual.useChatStore) };
});
vi.mock('@/store/session-context-store', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/session-context-store')>();
  return { ...actual, useSessionContextStore: Object.assign((selector: (state: ReturnType<typeof actual.useSessionContextStore.getState>) => unknown) => selector(actual.useSessionContextStore.getState()), actual.useSessionContextStore) };
});

function open(context: ChatContextUsage | undefined, options: { streaming?: boolean; compacting?: boolean } = {}) {
  useChatStore.setState({
    activeSessionId: 's1',
    streamingBuffers: options.streaming ? { s1: { isStreaming: true } as never } : {},
  });
  useSessionContextStore.setState({ context: context ? { s1: context } : {}, compacting: options.compacting ? { s1: true } : {} });
}

const tag = (html: string, testId: string) => html.match(new RegExp(`<[a-z]+[^>]*data-testid="${testId}"[^>]*>`))?.[0] ?? '';
/** The tooltip wrapper right before an element. */
const hintOf = (html: string, testId: string) => {
  const at = html.indexOf(`data-testid="${testId}"`);
  const before = html.slice(0, at);
  return before.slice(before.lastIndexOf('<span data-hint-name='));
};

beforeEach(() => { mock.schema = catalog; });
afterEach(() => {
  useChatStore.setState({ activeSessionId: null, streamingBuffers: {}, toolCallCounts: {}, messagesBySession: {} });
  useSessionContextStore.setState({ context: {}, compacting: {} });
});

describe('the context meter (#322)', () => {
  it('shows used / window with K and M and says in its hover text when it compacts automatically', () => {
    open({ tokens: 356_000, window: 1_000_000, compactions: [] });
    const html = renderToStaticMarkup(<ContextMeter />);
    expect(html).toContain('>356K / 1M<');
    expect(tag(html, 'composer-context-meter')).toContain('tabular-nums');
    expect(tag(html, 'composer-context-meter')).toContain(PICKER_UPPERCASE_CLASS);
    expect(hintOf(html, 'composer-context-meter')).toContain(`data-hint="${formatSchemaText(messages['ui.studio.context.hint_window'], { used: '356K', window: '1M', threshold: 80 })}"`);
  });

  it('names the vision estimate and its budget in the hover text (#247)', () => {
    open({ tokens: 356_000, visionTokens: 1_105, visionBudget: 10, window: 1_000_000, compactions: [] });
    const html = renderToStaticMarkup(<ContextMeter />);
    const hint = hintOf(html, 'composer-context-meter');
    expect(hint).toContain(`data-hint="${formatSchemaText(messages['ui.studio.context.hint_window'], { used: '356K', window: '1M', threshold: 80 })} · ${formatSchemaText(messages['ui.studio.context.hint_vision'], { vision: '1.1K', budget: '10' })}"`);
    // No declared vision estimate leaves the hint unchanged.
    open({ tokens: 356_000, window: 1_000_000, compactions: [] });
    expect(hintOf(renderToStaticMarkup(<ContextMeter />), 'composer-context-meter'))
      .not.toContain(messages['ui.studio.context.hint_vision']);
  });

  it('shows only the count when no window is declared', () => {
    open({ tokens: 12_345, window: null, compactions: [] });
    const html = renderToStaticMarkup(<ContextMeter />);
    expect(html).toContain('>12.3K<');
    expect(hintOf(html, 'composer-context-meter')).toContain(`data-hint="${formatSchemaText(messages['ui.studio.context.hint_used'], { used: '12.3K' })}"`);
  });

  it('shows the unknown mark, never an estimate, when the upstream reported nothing', () => {
    open({ tokens: null, window: 1_000_000, compactions: [] });
    const html = renderToStaticMarkup(<ContextMeter />);
    expect(html).toContain(`>${messages['ui.studio.context.unknown']} / 1M<`);
    expect(hintOf(html, 'composer-context-meter')).toContain(`data-hint="${messages['ui.studio.context.hint_unknown']}"`);
    open(undefined);
    expect(renderToStaticMarkup(<ContextMeter />)).toContain(`>${messages['ui.studio.context.unknown']}<`);
  });

  it('turns to the warning color at the warning ratio and to the destructive color at the compaction point', () => {
    const settings = catalog.chat.context_compaction!;
    expect(contextLevel(690_000, 1_000_000, settings)).toBe('normal');
    expect(contextLevel(700_000, 1_000_000, settings)).toBe('warning');
    expect(contextLevel(800_000, 1_000_000, settings)).toBe('critical');
    expect(contextLevel(null, 1_000_000, settings)).toBe('normal');
    expect(contextLevel(900_000, null, settings)).toBe('normal');
    open({ tokens: 750_000, window: 1_000_000, compactions: [] });
    expect(tag(renderToStaticMarkup(<ContextMeter />), 'composer-context-meter')).toContain('text-warning');
    open({ tokens: 850_000, window: 1_000_000, compactions: [] });
    expect(tag(renderToStaticMarkup(<ContextMeter />), 'composer-context-meter')).toContain('text-destructive');
  });

  it('offers the compaction with the Schema hint naming the rounds that stay', () => {
    open({ tokens: 356_000, window: 1_000_000, compactions: [] });
    const html = renderToStaticMarkup(<ContextMeter />);
    expect(tag(html, 'composer-context-compact')).not.toContain('disabled=""');
    expect(html).toContain(messages['ui.studio.context.compact']);
    const rounds = catalog.chat.context_compaction!.keep_recent_rounds;
    expect(hintOf(html, 'composer-context-compact')).toContain(`data-hint="${formatSchemaText(messages['ui.studio.context.compact_hint'], { rounds })}"`);
  });

  it('refuses the compaction while a reply runs and says why', () => {
    open({ tokens: 356_000, window: 1_000_000, compactions: [] }, { streaming: true });
    const html = renderToStaticMarkup(<ContextMeter />);
    expect(tag(html, 'composer-context-compact')).toContain('disabled=""');
    expect(hintOf(html, 'composer-context-compact')).toContain(`data-reason="${messages['ui.studio.context.compact_busy']}"`);
  });

  it('says it is compacting and stays off until the compaction answers', () => {
    open({ tokens: 356_000, window: 1_000_000, compactions: [] }, { compacting: true });
    const html = renderToStaticMarkup(<ContextMeter />);
    expect(tag(html, 'composer-context-compact')).toContain('disabled=""');
    expect(html).toContain(messages['ui.studio.context.compacting']);
  });

  it('hides the compaction when the deployment turned it off, and everything on an older Schema', () => {
    open({ tokens: 356_000, window: 1_000_000, compactions: [] });
    const off = structuredClone(catalog);
    off.chat.context_compaction = { ...off.chat.context_compaction!, enabled: false };
    mock.schema = off;
    let html = renderToStaticMarkup(<ContextMeter />);
    expect(html).toContain('data-testid="composer-context-meter"');
    expect(html).not.toContain('data-testid="composer-context-compact"');
    const older = structuredClone(catalog);
    delete older.runtime.http.routes.chat_session_context;
    mock.schema = older;
    html = renderToStaticMarkup(<ContextMeter />);
    expect(html).toBe('');
  });
});

describe('the tool call counter (#275)', () => {
  const withLimit = (limit: number | undefined): ProductSchema => {
    const next = structuredClone(catalog);
    if (limit !== undefined) next.chat.agent_limits = { max_tool_calls_per_reply: limit };
    return next;
  };
  const assistant = (toolCallCount: number | undefined) => ({ id: 'm1', role: 'assistant' as const, content: 'ok', createdAt: 1, ...(toolCallCount === undefined ? {} : { toolCallCount }) });
  const render = () => renderToStaticMarkup(<ContextMeter />);
  const label = messages['ui.studio.tool_calls.label'];

  beforeEach(() => {
    mock.schema = withLimit(20);
    open({ tokens: 22_500, window: 1_000_000, compactions: [] });
  });

  it('shows 0 / limit while the session has no assistant reply', () => {
    expect(render()).toContain(`>${label} 0 / 20<`);
  });

  it('counts the newest assistant reply and keeps its final value after the reply ended', () => {
    useChatStore.setState({ messagesBySession: { s1: [assistant(3), { id: 'u2', role: 'user', content: 'again', createdAt: 2 }] } });
    expect(render()).toContain(`>${label} 3 / 20<`);
    useChatStore.setState({ messagesBySession: { s1: [assistant(3), assistant(7)] } });
    expect(render()).toContain(`>${label} 7 / 20<`);
  });

  it('grows with the reply: a new reply starts at 0 and observed counts only rise', () => {
    useChatStore.setState({ messagesBySession: { s1: [assistant(9)] } });
    useChatStore.getState().initStreaming('s1', { content: 'go' });
    expect(render()).toContain(`>${label} 0 / 20<`);
    useChatStore.getState().observeToolCallCount('s1', 2);
    expect(render()).toContain(`>${label} 2 / 20<`);
    useChatStore.getState().observeToolCallCount('s1', 1);
    expect(render()).toContain(`>${label} 2 / 20<`);
    useChatStore.getState().observeToolCallCount('s1', 5);
    expect(render()).toContain(`>${label} 5 / 20<`);
    useChatStore.getState().finishStreaming('s1');
    expect(render()).toContain(`>${label} 5 / 20<`);
    // The final message read replaces the live value once the reply is over.
    useChatStore.getState().applyNewestMessagePage('s1', { messages: [assistant(6)], nextCursor: '' });
    expect(render()).toContain(`>${label} 6 / 20<`);
  });

  it('turns warning from 80% of the limit and destructive when it is spent', () => {
    expect(toolCallLevel(15, 20)).toBe('normal');
    expect(toolCallLevel(16, 20)).toBe('warning');
    expect(toolCallLevel(19, 20)).toBe('warning');
    expect(toolCallLevel(20, 20)).toBe('critical');
    expect(toolCallLevel(1, 0)).toBe('normal');
    const level = (count: number) => tag((useChatStore.setState({ messagesBySession: { s1: [assistant(count)] } }), render()), 'composer-tool-calls');
    expect(level(15)).toContain('text-muted-foreground');
    expect(level(16)).toContain('text-warning');
    expect(level(20)).toContain('text-destructive');
    expect(level(20)).toContain('data-level="critical"');
  });

  it('takes the limit from the Schema, never a built-in 20', () => {
    mock.schema = withLimit(35);
    useChatStore.setState({ messagesBySession: { s1: [assistant(28)] } });
    const html = render();
    expect(html).toContain(`>${label} 28 / 35<`);
    expect(tag(html, 'composer-tool-calls')).toContain('text-warning');
    expect(hintOf(html, 'composer-tool-calls')).toContain(`data-hint="${messages['ui.studio.tool_calls.hint']}"`);
  });

  it('hides the counter on a Schema without the limit', () => {
    mock.schema = withLimit(undefined);
    expect(render()).not.toContain('composer-tool-calls');
  });

  it('lays out the figure, the compact action and the counter in that order, the counter on the right edge', () => {
    const html = render();
    const order = ['composer-context-meter', 'composer-context-compact', 'composer-tool-calls'].map((id) => html.indexOf(`data-testid="${id}"`));
    expect(order.every((at) => at >= 0)).toBe(true);
    expect(order).toEqual([...order].sort((a, b) => a - b));
    // The counter's wrapper takes the free space to its left; the figure and the counter give way (truncate), the action does not.
    const wrapperClass = (id: string) => hintOf(html, id).match(/data-wrapper-class="([^"]*)"/)?.[1].split(' ') ?? [];
    expect(wrapperClass('composer-tool-calls')).toEqual(expect.arrayContaining(['ml-auto', 'min-w-0', 'shrink']));
    expect(wrapperClass('composer-context-meter')).toEqual(expect.arrayContaining(['min-w-0', 'shrink']));
    expect(wrapperClass('composer-context-compact')).toContain('shrink-0');
    expect(tag(html, 'composer-context')).not.toContain('justify-between');
    expect(tag(html, 'composer-tool-calls')).toContain('truncate');
  });
});
