// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChatMessage } from '@/components/chat/chat-message';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatMessage as ChatMessageType } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({
    previewAssetUrl: (url: string) => (url ? `preview:${url}` : ''),
    proxyAssetUrl: (url: string) => `proxy:${url}`,
  }),
}));
// The server is the only source of records; the test answers each read from
// whatever the fake server currently holds.
const server = vi.hoisted(() => ({ calls: 0, records: [] as unknown[], fail: false }));
vi.mock('@/lib/chat/browser-client', () => ({
  createChatBrowserClient: () => ({
    getSelfChecks: async () => {
      server.calls += 1;
      if (server.fail) throw new Error('self-check read failed');
      return server.records;
    },
  }),
}));

const result = (extra: Partial<ChatMessageType> = {}): ChatMessageType => ({
  id: 'm1', role: 'assistant', content: '结果完成。', createdAt: 1,
  attachments: [{ handle: '@model1', kind: 'model', taskId: 't1', imageSlot: 1 }],
  images: [{ url: '/api/v1/tasks/t1/assets/preview', taskId: 't1' }],
  ...extra,
});
const laterTurn = (streaming: boolean): ChatMessageType => ({
  id: 'm2', role: 'assistant', content: streaming ? '自检中。' : '对照参考图后已写结论。',
  createdAt: 2, isStreaming: streaming,
});

const record = {
  taskId: 't1', status: 'passed', roundsUsed: 1, roundsLimit: 3, rendersInRound: 1, rendersLimit: 6,
  correctionsUsed: 0, correctionsLimit: 2,
  checks: [{ round: 1, kind: 'reference', tool: 'compare_model_reference', views: ['front'], handles: [], metrics: null, ok: true, error: '', at: '' }],
  conclusion: { status: 'passed', summary: 'matches the reference', findings: [], fixes: [], remaining: [], at: '' },
  updatedAt: '',
};

function Conversation({ laterStreaming }: { laterStreaming: boolean }) {
  return (
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0, revalidateOnFocus: false }}>
      <ChatMessage message={result()} sessionId="s1" />
      <ChatMessage message={laterTurn(laterStreaming)} sessionId="s1" />
    </SWRConfig>
  );
}

beforeEach(() => {
  server.calls = 0;
  server.records = [];
  server.fail = false;
  // jsdom has no media queries; the streaming spinner reads prefers-reduced-motion.
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined }),
  });
  useStudioStore.setState({ stageImage: null });
  useWorkspaceStore.setState({ trackedTasks: [] });
});
afterEach(cleanup);

describe('the result card follows records written by a later turn (#246)', () => {
  it('updates the earlier reply when the later, card-less reply settles and its tools wrote a record', async () => {
    const view = render(<Conversation laterStreaming />);
    const badge = () => view.getAllByTestId('chat-model-self-check-status')[0];
    // The first read is the empty session; the result is unchecked.
    await waitFor(() => expect(server.calls).toBe(1));
    expect(badge().getAttribute('data-role')).toBe('unchecked');
    expect(badge().textContent).toBe(messages['ui.chat.self_check.status_unchecked']);

    // The later turn's tools wrote task t1's record; the same reply settles.
    server.records = [record];
    view.rerender(<Conversation laterStreaming={false} />);

    await waitFor(() => expect(badge().getAttribute('data-role')).toBe('passed'));
    expect(badge().textContent).toBe(messages['ui.chat.self_check.status_passed']);
    // The earlier reply also shows the folded record card, without a reload; opening it shows the record.
    fireEvent.click(view.getByTestId('chat-self-check-record-toggle'));
    expect(view.getByTestId('chat-self-check-record').textContent).toContain('matches the reference');
  });

  // #246: a failed read is said out loud; the card never presents the failure
  // as a real "unchecked" state.
  it('says the session records could not be read', async () => {
    server.fail = true;
    const view = render(<Conversation laterStreaming={false} />);
    await waitFor(() => expect(view.getByTestId('chat-self-check-load-failed')).toBeTruthy());
    expect(view.getByTestId('chat-self-check-load-failed').textContent).toBe(messages['ui.chat.self_check.load_failed']);
  });
});