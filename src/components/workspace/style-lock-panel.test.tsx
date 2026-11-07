// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { useStudioStore } from '@/store/studio-store';
import { StyleLockPanel, StyleLockSection, clearStyleLockDraftCache } from './style-lock-panel';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;
const mock = vi.hoisted(() => ({
  get: vi.fn(async () => ({ enabled: true, text: 'ink', references: [] as string[] })),
  set: vi.fn(async (_session: string, value: { enabled: boolean; text: string; references: string[] }) => value),
  upload: vi.fn(async () => ({ id: 'upload-1', url: '/api/v1/assets/upload-1' })),
  exportPicture: vi.fn(async () => new Blob(['layer'], { type: 'image/png' })),
  nodes: [] as Array<{ id: string; type: string; imageUrl: string; taskId: string; x: number; y: number; width: number; height: number; zIndex: number; name?: string; visible?: boolean }>,
}));

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }) }));
vi.mock('@/lib/chat/browser-client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/chat/browser-client')>();
  return { ...actual, createChatBrowserClient: () => ({ getStyleLock: mock.get, setStyleLock: mock.set }) };
});
vi.mock('@/lib/workspace/input-asset-upload', () => ({ uploadWorkspaceInputAssetResult: mock.upload }));
vi.mock('@/lib/image-upload', () => ({ prepareImageUploadBlob: vi.fn(async () => new Blob(['image'], { type: 'image/png' })) }));
vi.mock('@/store/canvas-store', () => ({
  useCanvasStore: (selector: (state: { nodesBySession: Record<string, unknown[]> }) => unknown) => selector({ nodesBySession: { s1: mock.nodes } }),
}));
vi.mock('@/lib/canvas/pending-edits', () => ({ flushPendingEdits: vi.fn(async () => true) }));
vi.mock('@/lib/canvas/export-canvas', () => ({ exportCanvasToBlob: mock.exportPicture }));
vi.mock('@/lib/canvas/send-snapshot', () => ({
  snapshotFilename: () => 'canvas.png',
  exportAndUploadSnapshot: async (steps: {
    flush?: () => Promise<boolean>;
    exportPicture: () => Promise<Blob>;
    upload: (picture: Blob) => Promise<string>;
    maximumBytes: number;
  }) => {
    if (steps.flush) await steps.flush();
    const picture = await steps.exportPicture();
    if (steps.maximumBytes > 0 && picture.size > steps.maximumBytes) throw new Error('too large');
    return { picture, url: await steps.upload(picture) };
  },
}));
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
  clearStyleLockDraftCache();
  useStudioStore.setState({ styleLockRequest: null, styleLockOpen: false });
  mock.nodes = [];
  mock.get.mockResolvedValue({ enabled: true, text: 'ink', references: [] });
  mock.set.mockImplementation(async (_session, value) => value);
  mock.upload.mockClear();
  mock.exportPicture.mockClear();
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

  it('offers the session canvas layers and says when there are none', async () => {
    render(<StyleLockPanel sessionId="s1" />);
    await screen.findByTestId('style-lock-text');
    fireEvent.click(screen.getByRole('button', { name: messages['ui.studio.style_lock.canvas_layers'] }));
    expect(screen.getByTestId('style-lock-canvas-choices').textContent).toContain(messages['ui.studio.style_lock.no_canvas_layers']);
  });

  it('rasterises a chosen canvas layer, uploads it and adds the input asset as a reference', async () => {
    mock.nodes = [{
      id: 'layer-1', type: 'image', imageUrl: '/api/v1/assets/source', taskId: 't1',
      x: 0, y: 0, width: 64, height: 64, zIndex: 1, name: 'Ink layer',
    }];
    render(<StyleLockPanel sessionId="s1" />);
    await screen.findByTestId('style-lock-text');
    fireEvent.click(screen.getByRole('button', { name: messages['ui.studio.style_lock.canvas_layers'] }));
    fireEvent.click(screen.getByTestId('style-lock-canvas-layer-layer-1'));
    await waitFor(() => expect(mock.exportPicture).toHaveBeenCalledOnce());
    await waitFor(() => expect(mock.upload).toHaveBeenCalledOnce());
    expect(screen.getAllByRole('img')).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: messages['ui.studio.style_lock.save'] }));
    await waitFor(() => expect(mock.set).toHaveBeenCalledWith('s1', { enabled: true, text: 'ink', references: ['upload-1'] }));
  });
});

describe('StyleLockSection', () => {
  it('starts collapsed inside the 功能 tab and expands from its title', () => {
    render(<StyleLockSection sessionId="s1" />);
    const body = document.getElementById('style-lock-body');
    expect(body?.hidden).toBe(true);
    const toggle = screen.getByRole('button', { name: messages['ui.studio.style_lock.title'] });
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(toggle);
    expect(body?.hidden).toBe(false);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByTestId('style-lock-panel')).toBeTruthy();
    fireEvent.click(toggle);
    expect(body?.hidden).toBe(true);
  });
});

describe('style-lock draft persistence', () => {
  it('keeps an unsaved draft across a tab switch (unmount and remount)', async () => {
    const first = render(<StyleLockPanel sessionId="s1" />);
    const textarea = await screen.findByTestId('style-lock-text');
    // Type after the saved lock has loaded: this covers the draft, not a race with the first read.
    await waitFor(() => expect((textarea as HTMLTextAreaElement).value).toBe('ink'));
    fireEvent.change(textarea, { target: { value: 'draft text' } });
    await waitFor(() => expect((textarea as HTMLTextAreaElement).value).toBe('draft text'));
    first.unmount();
    render(<StyleLockPanel sessionId="s1" />);
    await waitFor(() => expect((screen.getByTestId('style-lock-text') as HTMLTextAreaElement).value).toBe('draft text'));
  });
});
