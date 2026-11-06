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

it('restores per-picture purposes and saves them by URL after an image is removed', () => {
  useChatStore.setState({ activeSessionId: 'draft-test', activeSessionRevision: 1 });
  window.localStorage.setItem(draftKey('draft-test'), JSON.stringify({ text: '', images: ['/a', '/b'], purpose: 'style', purposes: { '/a': 'style', '/b': 'layout' } }));
  const setPurposes = vi.fn();
  const state = { sessionId: 'draft-test', text: '', setText: vi.fn(), images: [] as string[], sources: {}, purpose: null,
    setPurpose: vi.fn(), purposes: [] as ('style' | 'layout' | null)[], setPurposes, droppedNote: () => '' };
  const { rerender } = renderHook((props) => useComposerDraft(props), { initialProps: state });
  expect(setPurposes).toHaveBeenCalledWith(['style', 'layout']);
  expect(useWorkspaceStore.getState().pendingChatAttachments).toEqual(expect.arrayContaining(['/a', '/b']));
  rerender({ ...state, images: ['/b'], purposes: ['layout'] });
  expect(JSON.parse(window.localStorage.getItem(draftKey('draft-test'))!).purposes).toEqual({ '/b': 'layout' });
});
