// @vitest-environment jsdom

import React from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import { useChatStore } from '@/store/chat-store';
import { usePurposeParameters } from './use-purpose-parameters';

// #306: the values the session purpose gives the parameters that follow it, read for the 加工 panel. They are read
// again whenever the brief may have changed: when the stored brief of the session changes and when a turn ends (the
// Agent may have set the purpose in it); an earlier answer stays until the new one arrives, but is never shown for
// another session and never outlives a failed read.

const mock = vi.hoisted(() => ({ schema: null as unknown, route: true }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema }) }));

const rigging = offering('tripo/processing/rigging-v3');
const decimation = offering('tripo/geometry/decimate-triangle');
const remesh = offering('meshy/processing/remesh');

function offering(id: string): OfferingWire {
  return { ...defaultCatalog.offerings.find((entry) => entry.id === id)!, available: true } as unknown as OfferingWire;
}

/** The server as the hook sees it: what each Offering answers now, and every request made. */
const server = {
  answers: {} as Record<string, Record<string, unknown>>,
  failing: false,
  hold: null as Promise<void> | null,
  requests: [] as string[],
};

function serve() {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    server.requests.push(url);
    if (server.hold) await server.hold;
    const id = decodeURIComponent(url.split('offering_id=')[1]);
    if (server.failing) return { ok: false, status: 500, json: async () => ({}) } as unknown as Response;
    return { ok: true, json: async () => ({ session_id: 's', offering_id: id, parameters: server.answers[id] ?? {} }) } as unknown as Response;
  });
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0, shouldRetryOnError: false }}>{children}</SWRConfig>;
}

const session = (id: string, brief?: Record<string, unknown>) => ({
  id, title: id, createdAt: '2026-11-16T00:00:00Z', updatedAt: '2026-11-16T00:00:00Z', ...(brief ? { brief } : {}),
});

function render(sessionId: string | null, offerings: readonly OfferingWire[]) {
  return renderHook((props: { session: string | null; offerings: readonly OfferingWire[] }) => usePurposeParameters(props.session, props.offerings), {
    initialProps: { session: sessionId, offerings }, wrapper,
  });
}

const mobile = { purpose: 'game_character', target_platform: 'mobile' };
const pc = { purpose: 'game_character', target_platform: 'pc_console' };

beforeEach(() => {
  mock.schema = structuredClone(defaultCatalog.application);
  server.answers = { [rigging.id]: { export_texture_size: '2k' }, [decimation.id]: { export_texture_size: '2k' } };
  server.failing = false;
  server.hold = null;
  server.requests = [];
  vi.stubGlobal('fetch', serve());
  useChatStore.setState({ sessions: [session('s1', mobile), session('s2')], activeSessionId: 's1', sessionListRevision: 0 });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe('reading what the session purpose gives', () => {
  it('asks once for each Offering that follows the purpose, and for no other', async () => {
    const { result } = render('s1', [rigging, decimation, remesh]);
    await waitFor(() => expect(result.current).toEqual({
      [rigging.id]: { export_texture_size: '2k' },
      [decimation.id]: { export_texture_size: '2k' },
    }));
    expect(server.requests.map((url) => decodeURIComponent(url.split('offering_id=')[1])).sort()).toEqual([decimation.id, rigging.id]);
    expect(server.requests.every((url) => url.startsWith('/api/v1/chat/sessions/s1/purpose-parameters?offering_id='))).toBe(true);
  });

  it('asks nothing when no Offering follows the purpose, there is no session, or the Schema has no route', async () => {
    const none = render('s1', [remesh]);
    const noSession = render(null, [rigging]);
    const bare = structuredClone(defaultCatalog.application) as unknown as { runtime: { http: { routes: Record<string, string> } } };
    delete bare.runtime.http.routes.chat_session_purpose_parameters;
    mock.schema = bare;
    const noRoute = render('s1', [rigging]);
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
    expect(server.requests).toEqual([]);
    for (const view of [none, noSession, noRoute]) expect(view.result.current).toEqual({});
  });

  it('gives the other value once the session\'s brief says another purpose or platform', async () => {
    const { result } = render('s1', [rigging]);
    await waitFor(() => expect(result.current[rigging.id]).toEqual({ export_texture_size: '2k' }));
    server.answers[rigging.id] = { export_texture_size: '4k' };
    act(() => useChatStore.getState().updateSessionBrief('s1', pc));
    await waitFor(() => expect(result.current[rigging.id]).toEqual({ export_texture_size: '4k' }));
    server.answers[rigging.id] = { export_texture_size: '8k' };
    act(() => useChatStore.getState().updateSessionBrief('s1', {}));
    await waitFor(() => expect(result.current[rigging.id]).toEqual({ export_texture_size: '8k' }));
  });

  it('does not ask again for a brief that only changed its blank members or its object', async () => {
    const { result } = render('s1', [rigging]);
    await waitFor(() => expect(result.current[rigging.id]).toBeDefined());
    expect(server.requests).toHaveLength(1);
    act(() => useChatStore.getState().updateSessionBrief('s1', { target_platform: 'mobile', purpose: 'game_character', notes: '' }));
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
    expect(server.requests).toHaveLength(1);
  });

  it('asks again when a turn ends, which is when the Agent may have set the purpose', async () => {
    useChatStore.setState({ sessions: [session('s1'), session('s2')] });
    server.answers[rigging.id] = { export_texture_size: '8k' };
    const { result } = render('s1', [rigging]);
    await waitFor(() => expect(result.current[rigging.id]).toEqual({ export_texture_size: '8k' }));
    // The Agent's tool call wrote the purpose; the stored copy of the brief in the page is not told.
    server.answers[rigging.id] = { export_texture_size: '2k' };
    act(() => useChatStore.setState((state) => ({ sessionListRevision: state.sessionListRevision + 1 })));
    await waitFor(() => expect(result.current[rigging.id]).toEqual({ export_texture_size: '2k' }));
  });

  it('keeps the earlier value while the new answer is on its way', async () => {
    const { result } = render('s1', [rigging]);
    await waitFor(() => expect(result.current[rigging.id]).toEqual({ export_texture_size: '2k' }));
    let release: () => void = () => {};
    server.hold = new Promise<void>((resolve) => { release = resolve; });
    server.answers[rigging.id] = { export_texture_size: '4k' };
    act(() => useChatStore.getState().updateSessionBrief('s1', pc));
    await waitFor(() => expect(server.requests).toHaveLength(2));
    expect(result.current[rigging.id]).toEqual({ export_texture_size: '2k' });
    await act(async () => { release(); });
    await waitFor(() => expect(result.current[rigging.id]).toEqual({ export_texture_size: '4k' }));
  });

  it('shows nothing for another session, then that session\'s own values', async () => {
    const { result, rerender } = render('s1', [rigging]);
    await waitFor(() => expect(result.current[rigging.id]).toEqual({ export_texture_size: '2k' }));
    let release: () => void = () => {};
    server.hold = new Promise<void>((resolve) => { release = resolve; });
    server.answers[rigging.id] = { export_texture_size: '8k' };
    rerender({ session: 's2', offerings: [rigging] });
    await waitFor(() => expect(server.requests).toHaveLength(2));
    expect(result.current).toEqual({});
    await act(async () => { release(); });
    await waitFor(() => expect(result.current[rigging.id]).toEqual({ export_texture_size: '8k' }));
    expect(server.requests[1]).toContain('/sessions/s2/purpose-parameters');
  });

  it('shows nothing for an Offering the server could not answer, and not the value it gave before', async () => {
    const { result } = render('s1', [rigging, decimation]);
    await waitFor(() => expect(Object.keys(result.current)).toHaveLength(2));
    server.failing = true;
    act(() => useChatStore.getState().updateSessionBrief('s1', pc));
    await waitFor(() => expect(result.current).toEqual({}));
    // The next read that works shows its values again.
    server.failing = false;
    server.answers[rigging.id] = { export_texture_size: '4k' };
    act(() => useChatStore.setState((state) => ({ sessionListRevision: state.sessionListRevision + 1 })));
    await waitFor(() => expect(result.current[rigging.id]).toEqual({ export_texture_size: '4k' }));
  });

  it('asks again for the Offering the person switches a step to', async () => {
    const { result, rerender } = render('s1', [rigging]);
    await waitFor(() => expect(result.current[rigging.id]).toBeDefined());
    server.answers[decimation.id] = { export_texture_size: '4k' };
    rerender({ session: 's1', offerings: [decimation] });
    await waitFor(() => expect(result.current[decimation.id]).toEqual({ export_texture_size: '4k' }));
  });
});
