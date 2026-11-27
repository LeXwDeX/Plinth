import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { formatSchemaText, type ProductSchema } from '@/lib/product-schema';
import type { ChatContextUsage } from '@/lib/chat/browser-client';
import { ContextMeter, contextLevel } from '@/components/studio/context-meter';
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
  useSessionContextStore.setState({ context: {}, compacting: {}, reasoning: {} });
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

  // #670: the meter's name joins its label and value with the Schema's label/value words, not an ASCII colon.
  it('names the meter through the Schema label and value pattern', () => {
    open({ tokens: 356_000, window: 1_000_000, compactions: [] });
    const html = renderToStaticMarkup(<ContextMeter />);
    const name = formatSchemaText(messages['ui.common.label_value'], { label: messages['ui.studio.context.label'], value: '356K / 1M' });
    expect(tag(html, 'composer-context-meter')).toContain(`aria-label="${name}"`);
  });

  it('names the vision token estimate against its declared token budget and the picture places (#247)', () => {
    open({ tokens: 356_000, visionTokens: 1_105, visionTokenBudget: 12_000, visionBudget: 10, window: 1_000_000, compactions: [] });
    const html = renderToStaticMarkup(<ContextMeter />);
    const hint = hintOf(html, 'composer-context-meter');
    expect(hint).toContain(`data-hint="${formatSchemaText(messages['ui.studio.context.hint_window'], { used: '356K', window: '1M', threshold: 80 })} · ${formatSchemaText(messages['ui.studio.context.hint_vision'], { vision: '1.1K', token_budget: '12K', images: 10 })}"`);
    // The picture places are a count, never the token budget.
    expect(hint).toContain('图片名额上限 10 张');
    expect(hint).not.toContain('上限 10 token');
    // No declared vision estimate leaves the hint unchanged.
    open({ tokens: 356_000, window: 1_000_000, compactions: [] });
    expect(hintOf(renderToStaticMarkup(<ContextMeter />), 'composer-context-meter'))
      .not.toContain(messages['ui.studio.context.hint_vision']);
  });

  it('never presents the picture places as a token budget (#247 regression)', () => {
    // The legacy fallback declares image places but no vision token bound: the hint must stay silent
    // rather than call the places a "10-token budget".
    open({ tokens: 356_000, visionTokens: 1_105, visionBudget: 10, window: 1_000_000, compactions: [] });
    const hint = hintOf(renderToStaticMarkup(<ContextMeter />), 'composer-context-meter');
    expect(hint).not.toContain(messages['ui.studio.context.hint_vision']);
    expect(hint).not.toContain('10 token');
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

describe('the tool call counter (#553: this turn only, no limit)', () => {
  it('names the thinking level the next turn sends before the count, in capitals, without "this turn" (#639)', () => {
    open({ tokens: 22_500, window: 1_000_000, compactions: [] });
    useChatStore.setState({ messagesBySession: { s1: [{ id: 'm1', role: 'assistant' as const, content: 'ok', createdAt: 1, toolCallCount: 11 }] } });
    useSessionContextStore.setState({ reasoning: { s1: { effort: 'high', levels: ['low', 'high'], defaultLevel: 'low' } } });
    const html = renderToStaticMarkup(<ContextMeter />);
    const shown = html.match(/data-testid="composer-tool-calls"[^>]*>([^<]*)</)?.[1] ?? '';
    expect(shown).toBe('思考强度：high，工具调用次数：11');
    expect(tag(html, 'composer-tool-calls')).toContain(PICKER_UPPERCASE_CLASS);
    expect(shown).not.toContain('本轮对话');
    // No pick falls back to the model's own default; a model without levels (or levels not read yet) says so.
    useSessionContextStore.setState({ reasoning: { s1: { effort: '', levels: ['low', 'high'], defaultLevel: 'low' } } });
    expect(renderToStaticMarkup(<ContextMeter />)).toContain('思考强度：low，工具调用次数：11');
    useSessionContextStore.setState({ reasoning: { s1: { effort: '', levels: [], defaultLevel: '' } } });
    expect(renderToStaticMarkup(<ContextMeter />)).toContain('>思考强度：无档位，工具调用次数：11<');
    // Levels not read yet: the same words, never a bare count.
    useSessionContextStore.setState({ reasoning: {} });
    expect(renderToStaticMarkup(<ContextMeter />)).toContain('>思考强度：无档位，工具调用次数：11<');
  });

  const assistant = (toolCallCount: number | undefined) => ({ id: 'm1', role: 'assistant' as const, content: 'ok', createdAt: 1, ...(toolCallCount === undefined ? {} : { toolCallCount }) });
  const render = () => renderToStaticMarkup(<ContextMeter />);
  const text = (count: number) => messages['ui.studio.tool_calls.turn_with_effort'].replace('{effort}', messages['ui.studio.reasoning.none_value']).replace('{count}', String(count));
  const counter = (html: string) => html.match(/data-testid="composer-tool-calls"[^>]*>([^<]*)</)?.[1] ?? '';

  beforeEach(() => {
    mock.schema = structuredClone(catalog);
    open({ tokens: 22_500, window: 1_000_000, compactions: [] });
  });

  it('reads "this turn" from the Schema with the count alone: no slash, no limit', () => {
    expect(messages['ui.studio.tool_calls.turn']).toBe('工具调用次数：{count}');
    expect(defaultCatalog.application.localization.bundles.en.messages['ui.studio.tool_calls.turn']).toBe('Tool calls: {count}');
    useChatStore.setState({ messagesBySession: { s1: [assistant(120)] } });
    const shown = counter(render());
    expect(shown).toBe(text(120));
    expect(shown).not.toContain('/');
    expect(shown).not.toContain('100');
  });

  it('shows 0 while the session has no assistant reply', () => {
    expect(counter(render())).toBe(text(0));
  });

  it('counts the newest assistant reply and keeps its final value after the reply ended', () => {
    useChatStore.setState({ messagesBySession: { s1: [assistant(3), { id: 'u2', role: 'user', content: 'again', createdAt: 2 }] } });
    expect(counter(render())).toBe(text(3));
    useChatStore.setState({ messagesBySession: { s1: [assistant(3), assistant(7)] } });
    expect(counter(render())).toBe(text(7));
  });

  it('starts at 0 with a new turn and only grows while it runs', () => {
    useChatStore.setState({ messagesBySession: { s1: [assistant(9)] } });
    useChatStore.getState().initStreaming('s1', { content: 'go' });
    expect(counter(render())).toBe(text(0));
    useChatStore.getState().observeToolCallCount('s1', 2);
    expect(counter(render())).toBe(text(2));
    useChatStore.getState().observeToolCallCount('s1', 1);
    expect(counter(render())).toBe(text(2));
    useChatStore.getState().observeToolCallCount('s1', 150);
    expect(counter(render())).toBe(text(150));
    useChatStore.getState().finishStreaming('s1');
    expect(counter(render())).toBe(text(150));
    // The final message read replaces the live value once the turn is over.
    useChatStore.getState().applyNewestMessagePage('s1', { messages: [assistant(151)], nextCursor: '' });
    expect(counter(render())).toBe(text(151));
  });

  it('never turns warning or destructive, however many calls the turn made', () => {
    useChatStore.setState({ messagesBySession: { s1: [assistant(500)] } });
    const html = tag(render(), 'composer-tool-calls');
    expect(html).toContain('text-muted-foreground');
    expect(html).not.toContain('text-warning');
    expect(html).not.toContain('text-destructive');
    expect(html).not.toContain('data-level');
  });

  it('lays out the figure, the compact action and the counter in that order, the counter on the right edge', () => {
    const html = render();
    const order = ['composer-context-meter', 'composer-context-compact', 'composer-tool-calls'].map((id) => html.indexOf(`data-testid="${id}"`));
    expect(order.every((at) => at >= 0)).toBe(true);
    expect(order).toEqual([...order].sort((a, b) => a - b));
    // The counter's wrapper takes the free space to its left; the figure and the counter give way (truncate), the action does not.
    const wrapperClass = (id: string) => hintOf(html, id).match(/data-wrapper-class="([^"]*)"/)?.[1].split(' ') ?? [];
    // The counter has no tooltip: the span itself takes the free space to its left and gives way.
    const counterClass = tag(html, 'composer-tool-calls').match(/class="([^"]*)"/)?.[1].split(' ') ?? [];
    expect(counterClass).toEqual(expect.arrayContaining(['ml-auto', 'min-w-0', 'shrink']));
    expect(wrapperClass('composer-context-meter')).toEqual(expect.arrayContaining(['min-w-0', 'shrink']));
    expect(wrapperClass('composer-context-compact')).toContain('shrink-0');
    expect(tag(html, 'composer-context')).not.toContain('justify-between');
    expect(tag(html, 'composer-tool-calls')).toContain('truncate');
  });
});
