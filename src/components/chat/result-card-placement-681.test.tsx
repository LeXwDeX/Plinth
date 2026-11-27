// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChatMessage } from '@/components/chat/chat-message';
import { imagesToPlace } from '@/lib/workspace/canvas-placement';
import { useChatStore, type ChatAttachment, type ChatMessage as ChatMessageType } from '@/store/chat-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;
vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }),
}));

const assistant = (images: Array<{ url: string; taskId: string }>, attachments: ChatAttachment[]): ChatMessageType => ({
  id: 'm1', role: 'assistant', content: 'done', images, attachments, createdAt: 1,
});
const image = (n: number) => ({ url: `/api/v1/tasks/t${n}/assets/a${n}`, taskId: `t${n}` });
const generated = (n: number): ChatAttachment => ({ handle: `@img${n}`, kind: 'image', role: 'generated', taskId: `t${n}`, assetId: `a${n}`, imageSlot: n });

/** What the board's placement effect would put on the board for the current object (a fresh event each time). */
const placedNow = (): string[] => imagesToPlace(useWorkspaceStore.getState().currentObject as CurrentObject | null, 's', new WeakSet());

function renderCard(onPick = vi.fn(async () => true)) {
  render(<ChatMessage message={assistant([image(1), image(2)], [generated(1), generated(2)])} sessionId="s" pick={null} onPick={onPick} />);
  return onPick;
}

describe('#681: a click on a result card never changes the board', () => {
  beforeEach(() => {
    useChatStore.setState({ activeSessionId: 's' });
    useWorkspaceStore.getState().setCurrentObject(null);
  });
  afterEach(cleanup);

  it('selects the clicked candidate without putting it, or its group, on the board', () => {
    renderCard();
    fireEvent.click(screen.getAllByTestId('chat-result-picture')[0], { detail: 1 });
    const current = useWorkspaceStore.getState().currentObject;
    expect(current).toMatchObject({ taskId: 't1', placement: 'view' });
    expect(placedNow()).toEqual([]);
  });

  it('选用 marks the pick and selects it, but puts nothing on the board', async () => {
    const onPick = renderCard();
    fireEvent.click(screen.getAllByTestId('candidate-pick')[1]);
    await waitFor(() => expect(useWorkspaceStore.getState().currentObject).toMatchObject({ taskId: 't2', placement: 'view' }));
    expect(onPick).toHaveBeenCalledOnce();
    expect(placedNow()).toEqual([]);
  });

  it('a double click inserts exactly the double-clicked picture, not the other candidate', () => {
    renderCard();
    const second = screen.getAllByTestId('chat-result-picture')[1];
    fireEvent.click(second, { detail: 1 });
    fireEvent.click(second, { detail: 2 });
    fireEvent.doubleClick(second);
    expect(useWorkspaceStore.getState().currentObject).toMatchObject({ taskId: 't2', placement: 'insert' });
    expect(placedNow()).toEqual([image(2).url]);
  });

  it('「放入画板」 inserts only the highlighted picture', () => {
    renderCard();
    fireEvent.click(screen.getAllByTestId('chat-result-picture')[1], { detail: 1 });
    fireEvent.click(screen.getByTestId('chat-result-insert-canvas'));
    expect(screen.getByTestId('chat-result-insert-canvas').textContent).toContain(messages['ui.studio.result.insert_canvas']);
    expect(useWorkspaceStore.getState().currentObject).toMatchObject({ taskId: 't2', placement: 'insert' });
    expect(placedNow()).toEqual([image(2).url]);
  });
});
