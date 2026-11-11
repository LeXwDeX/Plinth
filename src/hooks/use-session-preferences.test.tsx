// @vitest-environment jsdom

import React from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ChatAttachmentClaim } from '@/lib/chat/attachments';
import type { ChatBrowserClient } from '@/lib/chat/browser-client';
import type { ChatPreference } from '@/lib/chat/preferences';
import { useSessionPreferences } from './use-session-preferences';

vi.mock('@/components/product-schema-provider', () => {
  const schema = defaultCatalog.application;
  return { useProductSchema: () => ({ schema }) };
});

afterEach(cleanup);

const preference = (overrides: Partial<ChatPreference> = {}): ChatPreference => ({
  id: 'p1', kind: 'selected', source: 'pick', direction: 'a watercolor fox',
  reasonPreset: '', reasonText: '', handle: '@img1', taskId: 't1', assetId: 'a1',
  alternatives: ['@img2'], explicit: true, active: true, userEdited: false,
  createdAt: '2026-07-26T10:00:00Z',
  ...overrides,
});

const claim: ChatAttachmentClaim = { kind: 'image', taskId: 't1', assetId: 'a1' };

function client(overrides: Partial<ChatBrowserClient> = {}) {
  return {
    listPreferences: vi.fn(async () => [preference()]),
    rejectCandidate: vi.fn(async () => [preference()]),
    recordPreferenceSignal: vi.fn(async () => []),
    updatePreference: vi.fn(async () => preference()),
    deletePreference: vi.fn(async () => undefined),
    ...overrides,
  } as unknown as ChatBrowserClient;
}

function wrapper(cache: Map<string, any>) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return (
      <SWRConfig value={{ provider: () => cache, dedupingInterval: 0, shouldRetryOnError: false }}>
        {children}
      </SWRConfig>
    );
  };
}

describe('useSessionPreferences', () => {
  it('replaces a record the server rewrote under the same ID instead of dropping it', async () => {
    const cache = new Map<string, any>();
    const selected = preference();
    const rejected = preference({ kind: 'rejected', source: 'reject', reasonPreset: 'style', reasonText: 'too realistic' });
    const api = client({
      listPreferences: vi.fn(async () => [selected]),
      rejectCandidate: vi.fn(async () => [rejected]),
    });
    const { result } = renderHook(() => useSessionPreferences(api, 's1'), { wrapper: wrapper(cache) });
    await waitFor(() => expect(result.current.preferences).toEqual([selected]));
    await act(async () => {
      expect(await result.current.reject(claim, 'style', 'too realistic')).toBe(true);
    });
    // The selected record became a rejection with the same ID: one record, new kind.
    expect(result.current.preferences).toEqual([rejected]);
    expect(result.current.failed).toBe(false);
  });

  it('publishes a replacement to every mounted reader of the same session', async () => {
    const cache = new Map<string, any>();
    const rejected = preference({ kind: 'rejected', source: 'reject' });
    const api = client({ rejectCandidate: vi.fn(async () => [rejected]) });
    const first = renderHook(() => useSessionPreferences(api, 's1'), { wrapper: wrapper(cache) });
    const second = renderHook(() => useSessionPreferences(api, 's1'), { wrapper: wrapper(cache) });
    await waitFor(() => expect(first.result.current.preferences).toHaveLength(1));
    await act(async () => {
      expect(await first.result.current.reject(claim, '', '')).toBe(true);
    });
    await waitFor(() => expect(second.result.current.preferences).toEqual([rejected]));
  });

  it('writes weak signals into the shared cache', async () => {
    const cache = new Map<string, any>();
    const signal = preference({ id: 'p-signal', kind: 'signal', source: 'regenerate', explicit: false });
    const api = client({
      listPreferences: vi.fn(async () => []),
      recordPreferenceSignal: vi.fn(async () => [signal]),
    });
    const { result } = renderHook(() => useSessionPreferences(api, 's1'), { wrapper: wrapper(cache) });
    await waitFor(() => expect(result.current.loaded).toBe(true));
    await act(async () => {
      await result.current.signal('regenerate', [claim]);
    });
    expect(result.current.preferences).toEqual([signal]);
  });

  it('refreshes the cache after a pick recorded a selected preference on the server', async () => {
    const cache = new Map<string, any>();
    const selected = preference();
    const api = client({
      listPreferences: vi.fn()
        .mockResolvedValueOnce([])
        .mockResolvedValueOnce([selected]),
    });
    const { result } = renderHook(() => useSessionPreferences(api, 's1'), { wrapper: wrapper(cache) });
    await waitFor(() => expect(result.current.loaded).toBe(true));
    expect(result.current.preferences).toEqual([]);
    await act(async () => {
      expect(await result.current.refresh()).toBe(true);
    });
    await waitFor(() => expect(result.current.preferences).toEqual([selected]));
  });

  it('keeps the read failure instead of showing an empty list', async () => {
    const cache = new Map<string, any>();
    const api = client({
      listPreferences: vi.fn(async () => { throw new Error('offline'); }),
    });
    const { result } = renderHook(() => useSessionPreferences(api, 's1'), { wrapper: wrapper(cache) });
    await waitFor(() => expect(result.current.failed).toBe(true));
    expect(result.current.preferences).toEqual([]);
    expect(result.current.loaded).toBe(false);
  });
});