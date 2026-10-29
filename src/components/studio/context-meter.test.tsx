import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { formatSchemaText, type ProductSchema } from '@/lib/product-schema';
import type { ChatContextUsage } from '@/lib/chat/browser-client';
import { ContextMeter, contextLevel } from '@/components/studio/context-meter';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

const catalog = defaultCatalog.application as unknown as ProductSchema;
const messages = catalog.content.messages;
const mock = vi.hoisted(() => ({ schema: null as unknown }));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ name, hint, reason, children }: { name: string; hint?: string; reason?: string; children: React.ReactNode }) => (
    <span data-hint-name={name} data-hint={hint ?? ''} data-reason={reason ?? ''}>{children}</span>
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
  useChatStore.setState({ activeSessionId: null, streamingBuffers: {} });
  useSessionContextStore.setState({ context: {}, compacting: {} });
});

describe('the context meter (#322)', () => {
  it('shows used / window with K and M and says in its hover text when it compacts automatically', () => {
    open({ tokens: 356_000, window: 1_000_000, compactions: [] });
    const html = renderToStaticMarkup(<ContextMeter />);
    expect(html).toContain('>356K / 1M<');
    expect(tag(html, 'composer-context-meter')).toContain('tabular-nums');
    expect(tag(html, 'composer-context-meter')).toContain('uppercase tracking-wide');
    expect(hintOf(html, 'composer-context-meter')).toContain(`data-hint="${formatSchemaText(messages['ui.studio.context.hint_window'], { used: '356K', window: '1M', threshold: 80 })}"`);
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

  it('takes a full row of the picker grid when narrow and two tracks when wide, one picker high', () => {
    open({ tokens: 356_000, window: 1_000_000, compactions: [] });
    const row = tag(renderToStaticMarkup(<ContextMeter />), 'composer-context');
    expect(row).toContain('col-span-full');
    expect(row).toContain('@[40rem]:col-span-2');
    expect(row).toContain('h-7');
    expect(row).toContain('justify-between');
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
