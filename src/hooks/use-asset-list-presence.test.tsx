// @vitest-environment jsdom

import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { useConnectivityStore } from '@/store/connectivity-store';
import {
  PRESENCE_TAB_STORAGE_KEY,
  useAssetListPresence,
  type UseAssetListPresenceOptions,
} from './use-asset-list-presence';

const base = defaultCatalog.application as unknown as ProductSchema;
const schema: ProductSchema = {
  ...base,
  asset_list: { ...base.asset_list!, presence: { heartbeat_seconds: 5 } },
};

function harness(overrides: Partial<UseAssetListPresenceOptions> = {}) {
  const presence = vi.fn(async () => {});
  const options: UseAssetListPresenceOptions = {
    client: { presence },
    projectId: 'prj_1',
    enabled: true,
    schema,
    tabId: 'tab-1',
    ...overrides,
  };
  return { presence, options };
}

beforeEach(() => {
  vi.useFakeTimers();
  useConnectivityStore.setState({ sessionExpired: false });
  window.sessionStorage.clear();
  // jsdom has no sendBeacon; every test starts on the client's leave path.
  Object.defineProperty(navigator, 'sendBeacon', { configurable: true, value: undefined });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('use-asset-list-presence (#328)', () => {
  it('posts present immediately and again on every heartbeat', async () => {
    const { presence, options } = harness();
    renderHook(() => useAssetListPresence(options));
    expect(presence).toHaveBeenCalledTimes(1);
    expect(presence).toHaveBeenCalledWith('prj_1', 'tab-1', 'present');

    await act(async () => { await vi.advanceTimersByTimeAsync(4999); });
    expect(presence).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(presence).toHaveBeenCalledTimes(2);
    expect(presence).toHaveBeenLastCalledWith('prj_1', 'tab-1', 'present');
  });

  // #511 (#385): presence is a repeating write, so it obeys the page's polling rules: a hidden tab keeps its
  // heartbeat (the server's grace covers a throttled tab), an ended login stops it and sends no 401-bound leave.
  it('keeps heartbeating in a hidden tab', async () => {
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    const { presence, options } = harness();
    renderHook(() => useAssetListPresence(options));
    act(() => { document.dispatchEvent(new Event('visibilitychange')); });
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(presence).toHaveBeenCalledTimes(3);
  });

  it('stops once the login has ended and heartbeats again after signing in', async () => {
    const { presence, options } = harness();
    const { unmount } = renderHook(() => useAssetListPresence(options));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(presence).toHaveBeenCalledTimes(1);

    act(() => { useConnectivityStore.setState({ sessionExpired: true }); });
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(presence).toHaveBeenCalledTimes(1);

    act(() => { useConnectivityStore.setState({ sessionExpired: false }); });
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(presence).toHaveBeenCalledTimes(2);
    expect(presence).toHaveBeenLastCalledWith('prj_1', 'tab-1', 'present');

    act(() => { useConnectivityStore.setState({ sessionExpired: true }); });
    presence.mockClear();
    unmount();
    expect(presence).not.toHaveBeenCalled();
  });

  it('sends nothing while disabled or without a project', () => {
    const disabled = harness({ enabled: false });
    renderHook(() => useAssetListPresence(disabled.options));
    const empty = harness({ projectId: null });
    renderHook(() => useAssetListPresence(empty.options));
    act(() => { vi.advanceTimersByTime(60_000); });
    expect(disabled.presence).not.toHaveBeenCalled();
    expect(empty.presence).not.toHaveBeenCalled();
  });

  it('leaves the old project before presenting the new one', () => {
    const presence = vi.fn(async () => {});
    const { rerender } = renderHook(
      ({ projectId }: { projectId: string }) => useAssetListPresence({
        client: { presence }, projectId, enabled: true, schema, tabId: 'tab-1',
      }),
      { initialProps: { projectId: 'prj_1' } },
    );
    presence.mockClear();

    rerender({ projectId: 'prj_2' });

    expect(presence).toHaveBeenNthCalledWith(1, 'prj_1', 'tab-1', 'leave');
    expect(presence).toHaveBeenNthCalledWith(2, 'prj_2', 'tab-1', 'present');
    expect(presence).toHaveBeenCalledTimes(2);
  });

  it('leaves on unmount', () => {
    const { presence, options } = harness();
    const { unmount } = renderHook(() => useAssetListPresence(options));
    presence.mockClear();
    unmount();
    expect(presence).toHaveBeenCalledWith('prj_1', 'tab-1', 'leave');
  });

  it('leaves with a beacon on pagehide', () => {
    // jsdom's Blob cannot be read back; record what the hook hands to the beacon.
    class RecordedBlob {
      readonly type: string;
      constructor(readonly parts: BlobPart[], options?: BlobPropertyBag) {
        this.type = options?.type ?? '';
      }
    }
    vi.stubGlobal('Blob', RecordedBlob);
    const beacon = vi.fn(() => true);
    Object.defineProperty(navigator, 'sendBeacon', { configurable: true, value: beacon });
    const { presence, options } = harness();
    renderHook(() => useAssetListPresence(options));
    presence.mockClear();

    act(() => { window.dispatchEvent(new Event('pagehide')); });

    expect(beacon).toHaveBeenCalledTimes(1);
    const [url, body] = beacon.mock.calls[0] as unknown as [string, RecordedBlob];
    expect(url).toBe('/api/v1/projects/prj_1/presence');
    expect(body.type).toBe('application/json');
    expect(body.parts[0]).toBe(JSON.stringify({ tab_id: 'tab-1', state: 'leave' }));
    expect(presence).not.toHaveBeenCalled();
  });

  it('stays silent on a failed present and retries on the next tick', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    const presence = vi.fn(async () => { throw new Error('offline'); });
    renderHook(() => useAssetListPresence({
      client: { presence }, projectId: 'prj_1', enabled: true, heartbeatSeconds: 5, tabId: 'tab-1',
    }));
    await act(async () => { await Promise.resolve(); });
    expect(presence).toHaveBeenCalledTimes(1);
    expect(error).not.toHaveBeenCalled();

    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(presence).toHaveBeenCalledTimes(2);
    await act(async () => { await Promise.resolve(); });
    expect(error).not.toHaveBeenCalled();
  });

  it('keeps one id per tab in sessionStorage across reloads', () => {
    const first = harness({ tabId: undefined });
    const { unmount } = renderHook(() => useAssetListPresence(first.options));
    const id = window.sessionStorage.getItem(PRESENCE_TAB_STORAGE_KEY);
    expect(id).toBeTruthy();
    expect(first.presence).toHaveBeenCalledWith('prj_1', id, 'present');
    unmount();

    // Another mount in the same tab, as after a reload.
    const second = harness({ tabId: undefined });
    renderHook(() => useAssetListPresence(second.options));
    expect(second.presence).toHaveBeenCalledWith('prj_1', id, 'present');
  });

  it('reads the id each tab has stored', () => {
    window.sessionStorage.setItem(PRESENCE_TAB_STORAGE_KEY, 'tab-a');
    const a = harness({ tabId: undefined });
    const first = renderHook(() => useAssetListPresence(a.options));
    expect(a.presence).toHaveBeenCalledWith('prj_1', 'tab-a', 'present');
    first.unmount();

    window.sessionStorage.setItem(PRESENCE_TAB_STORAGE_KEY, 'tab-b');
    const b = harness({ tabId: undefined });
    renderHook(() => useAssetListPresence(b.options));
    expect(b.presence).toHaveBeenCalledWith('prj_1', 'tab-b', 'present');
  });
});