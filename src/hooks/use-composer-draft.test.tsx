// @vitest-environment jsdom
import React from 'react';
import { cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { useComposerDraft } from './use-composer-draft';
import { draftKey } from '@/lib/chat/composer-draft';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore } from '@/store/workspace-store';

beforeEach(() => {
  const data = new Map<string, string>();
  vi.stubGlobal('localStorage', { getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => data.set(key, value), removeItem: (key: string) => data.delete(key), clear: () => data.clear() });
  useWorkspaceStore.setState({ pendingChatAttachments: [], attachmentSources: {} });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

// #697: an older draft's purposes are ignored; the pictures come back and the next save writes no purpose.
it('restores an older draft with purposes and saves it without them', () => {
  useChatStore.setState({ activeSessionId: 'draft-test', activeSessionRevision: 1 });
  window.localStorage.setItem(draftKey('draft-test'), JSON.stringify({ text: 'hi', images: ['/a', '/b'], purpose: 'style', purposes: { '/a': 'style', '/b': 'layout' } }));
  const setText = vi.fn();
  const state = { sessionId: 'draft-test', text: '', setText, images: [] as string[], sources: {}, droppedNote: () => '' };
  const { rerender } = renderHook((props) => useComposerDraft(props), { initialProps: state });
  expect(setText).toHaveBeenCalledWith('hi');
  expect(useWorkspaceStore.getState().pendingChatAttachments).toEqual(expect.arrayContaining(['/a', '/b']));
  rerender({ ...state, text: 'hi', images: ['/b'] });
  const stored = JSON.parse(window.localStorage.getItem(draftKey('draft-test'))!);
  expect(stored.images).toEqual(['/b']);
  expect(stored).not.toHaveProperty('purpose');
  expect(stored).not.toHaveProperty('purposes');
});
