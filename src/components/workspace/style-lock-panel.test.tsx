// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { StyleLockPanel } from './style-lock-panel';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;
const mock = vi.hoisted(() => ({
  get: vi.fn(async () => ({ enabled: true, text: 'ink', references: [] as string[] })),
  set: vi.fn(async (_session: string, value: { enabled: boolean; text: string; references: string[] }) => value),
  upload: vi.fn(async () => ({ id: 'upload-1', url: '/api/v1/assets/upload-1' })),
}));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }) }));
vi.mock('@/lib/chat/browser-client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/chat/browser-client')>();
  return { ...actual, createChatBrowserClient: () => ({ getStyleLock: mock.get, setStyleLock: mock.set }) };
});
vi.mock('@/lib/workspace/input-asset-upload', () => ({ uploadWorkspaceInputAssetResult: mock.upload }));
vi.mock('@/lib/image-upload', () => ({ prepareImageUploadBlob: vi.fn(async () => new Blob(['image'], { type: 'image/png' })) }));
vi.mock('@/store/chat-store', () => ({
  useChatStore: (selector: (state: object) => unknown) => selector({
    messagesBySession: {
      s1: [{
        id: 'm1', role: 'assistant', content: 'done', createdAt: 1,
        images: [{ url: '/api/v1/tasks/t1/assets/a1', taskId: 't1' }, { url: '/api/v1/tasks/t2/assets/a2', taskId: 't2' }],
        attachments: [
          { handle: '@img1', kind: 'image', role: 'generated', taskId: 't1', assetId: 'a1', imageSlot: 1 },
          { handle: '@img2', kind: 'image', role: 'generated', taskId: 't2', assetId: 'a2', imageSlot: 2 },
        ],
      }],
    },
  }),
}));

afterEach(cleanup);
beforeEach(() => {
  mock.get.mockResolvedValue({ enabled: true, text: 'ink', references: [] });
  mock.set.mockImplementation(async (_session, value) => value);
  mock.upload.mockClear();
});

describe('StyleLockPanel', () => {
  it('loads the Schema bound, keeps text when disabled, and blocks an overlong save', async () => {
    render(<StyleLockPanel sessionId="s1" />);
    const textarea = await screen.findByTestId('style-lock-text');
    expect((textarea as HTMLTextAreaElement).value).toBe('ink');
    fireEvent.change(textarea, { target: { value: 'x'.repeat(schema.chat.style_lock.maximum_text_characters + 1) } });
    expect(screen.getByTestId('style-lock-counter').textContent).toContain(`${schema.chat.style_lock.maximum_text_characters + 1}`);
    expect((screen.getByRole('button', { name: messages['ui.studio.style_lock.save'] }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(textarea, { target: { value: 'keep this text' } });
    fireEvent.click(screen.getByTestId('style-lock-enabled'));
    expect((textarea as HTMLTextAreaElement).value).toBe('keep this text');
  });

  it('adds, removes, reorders and saves only current-session image assets', async () => {
    render(<StyleLockPanel sessionId="s1" />);
    await screen.findByTestId('style-lock-text');
    fireEvent.click(screen.getByRole('button', { name: messages['ui.studio.style_lock.choose_session_reference'] }));
    fireEvent.click(screen.getByTestId('style-lock-session-reference-a2'));
    fireEvent.click(screen.getByRole('button', { name: messages['ui.studio.style_lock.choose_session_reference'] }));
    fireEvent.click(screen.getByTestId('style-lock-session-reference-a1'));
    expect(screen.getAllByRole('img')).toHaveLength(2);
    fireEvent.click(screen.getAllByRole('button', { name: messages['ui.studio.style_lock.move_up'] })[1]);
    fireEvent.click(screen.getByRole('button', { name: messages['ui.studio.style_lock.save'] }));
    await waitFor(() => expect(mock.set).toHaveBeenCalledWith('s1', { enabled: true, text: 'ink', references: ['a1', 'a2'] }));
    fireEvent.click(screen.getAllByRole('button', { name: messages['ui.studio.style_lock.remove_reference'] })[0]);
    expect(screen.getAllByRole('button', { name: messages['ui.studio.style_lock.remove_reference'] })).toHaveLength(1);
  });

  it('uploads a validated image as an input asset and preserves it on save failure', async () => {
    mock.set.mockRejectedValue(new Error('server detail'));
    render(<StyleLockPanel sessionId="s1" />);
    await screen.findByTestId('style-lock-text');
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [new File(['png'], 'style.png', { type: 'image/png' })] } });
    await waitFor(() => expect(mock.upload).toHaveBeenCalledOnce());
    expect(screen.getAllByRole('img')).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: messages['ui.studio.style_lock.save'] }));
    await waitFor(() => expect(mock.set).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('server detail'));
    expect(screen.getAllByRole('img')).toHaveLength(1);
  });
});
