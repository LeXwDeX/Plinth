import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { HarnessBrowserError, type HarnessSelection, type HarnessView } from '@/lib/harness';

const runtime = vi.hoisted(() => ({
  slots: [] as any[], cursor: 0,
  views: {} as Record<string, HarnessView>,
  mutates: {} as Record<string, ReturnType<typeof vi.fn>>,
  get: vi.fn(), put: vi.fn(),
  effects: [] as Array<() => unknown>, effectSetups: [] as Array<() => unknown>,
  streams: {} as Record<string, { assistantDbId: string | null; isStreaming: boolean; textContent?: string }>,
}));
vi.mock('react', () => ({
  useMemo: (factory: () => unknown) => factory(),
  useRef: (initial: unknown) => { const i = runtime.cursor++; return runtime.slots[i] ??= { current: initial }; },
  useState: (initial: unknown) => {
    const i = runtime.cursor++;
    if (!(i in runtime.slots)) runtime.slots[i] = typeof initial === 'function' ? (initial as () => unknown)() : initial;
    return [runtime.slots[i], (value: unknown) => {
      runtime.slots[i] = typeof value === 'function' ? (value as (previous: unknown) => unknown)(runtime.slots[i]) : value;
    }];
  },
  useEffect: (effect: () => unknown, dependencies: unknown[]) => {
    const i = runtime.cursor++;
    const previous = runtime.slots[i] as unknown[] | undefined;
    if (!previous || dependencies.some((value, index) => !Object.is(value, previous[index]))) runtime.effects.push(effect);
    runtime.slots[i] = dependencies;
  },
  useCallback: (fn: unknown) => fn,
}));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: object) => unknown) => select({ streamingBuffers: runtime.streams }) }));
const schema = structuredClone(catalog.application) as unknown as ProductSchema;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('swr', () => ({
  default: (key: readonly unknown[] | null, fetcher: (key: readonly unknown[]) => Promise<HarnessView>) => {
    if (!key) return { data: undefined, error: undefined, isLoading: false, mutate: vi.fn() };
    const id = JSON.stringify(key);
    runtime.mutates[id] ??= vi.fn(async (value?: HarnessView) => {
      const updated = typeof value === 'function' ? (value as (current: HarnessView | undefined) => HarnessView | undefined)(runtime.views[id]) : value ?? await fetcher(key);
      if (updated) runtime.views[id] = updated;
      return updated;
    });
    return { data: runtime.views[id], error: undefined, isLoading: runtime.views[id] === undefined, mutate: runtime.mutates[id] };
  },
}));
vi.mock('@/lib/harness', async (original) => ({
  ...(await original<typeof import('@/lib/harness')>()),
  createHarnessClient: () => ({ get: runtime.get, put: runtime.put }),
}));
import { sessionHarnessKey, useSessionHarness } from '../use-session-harness';

const baseSelection: HarnessSelection = {
  session_id: 's1', revision: 4, digest: 'd4', operation_id: 'generate', offering_id: 'o1', offering_revision: 'o1-r1',
  enabled_operations: ['generate'], sources: [], fixed_parameters: {}, fillable_parameters: ['prompt'],
  allow_prerequisites: false, updated_at: '2026-10-15T00:00:00Z',
};
function view(selection: HarnessSelection): HarnessView {
  return { selection, status: 'ready', reason: '', missing_parameters: [], can_propose: true, active_operation: null, estimated_credits: 0, turn: null };
}
const render = (session = 's1', owner = 'u1') => {
  runtime.cursor = 0;
  const result = useSessionHarness(session, owner);
  const effects = runtime.effects.splice(0);
  runtime.effectSetups.push(...effects);
  for (const effect of effects) effect();
  return result;
};
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((yes) => { resolve = yes; }); return { resolve, promise }; }
const flush = async () => { await Promise.resolve(); await Promise.resolve(); };

beforeEach(() => {
  runtime.slots = []; runtime.cursor = 0; runtime.views = {}; runtime.mutates = {}; runtime.effects = []; runtime.effectSetups = []; runtime.streams = {};
  runtime.get.mockReset(); runtime.put.mockReset();
});

describe('session Harness hook', () => {
  it('keys cache by owner, session, Schema revision and route', () => {
    expect(sessionHarnessKey('v1', 'u1', 's1', '/h/{sessionID}')).not.toEqual(sessionHarnessKey('v1', 'u2', 's1', '/h/{sessionID}'));
    expect(sessionHarnessKey('v1', 'u1', 's1', '/h/{sessionID}')).not.toEqual(sessionHarnessKey('v1', 'u1', 's2', '/h/{sessionID}'));
    expect(sessionHarnessKey('v1', 'u1', 's1', '/h/{sessionID}')).not.toEqual(sessionHarnessKey('v2', 'u1', 's1', '/h/{sessionID}'));
  });

  it('re-reads and shows the current server selection after a CAS 409 without retrying', async () => {
    const current = view(baseSelection);
    const serverSelection = { ...baseSelection, revision: 5, digest: 'server-digest-5', operation_id: 'other' };
    const serverView = view(serverSelection);
    const id = JSON.stringify(sessionHarnessKey(schema.revision, 'u1', 's1', schema.runtime.http.routes.chat_session_harness));
    runtime.views[id] = current;
    runtime.put.mockRejectedValueOnce(new HarnessBrowserError('error.harness_conflict', 409));
    runtime.get.mockResolvedValueOnce(serverView);
    const hook = render();
    await expect(hook.save({ ...baseSelection, operation_id: 'changed' })).rejects.toMatchObject({ status: 409 });
    expect(runtime.put).toHaveBeenCalledTimes(1);
    expect(runtime.put).toHaveBeenCalledWith('s1', 4, expect.objectContaining({ operation_id: 'changed' }));
    expect(runtime.get).toHaveBeenCalledWith('s1');
    expect(runtime.mutates[id]).toHaveBeenCalledWith(expect.any(Function), { revalidate: false });
    expect(runtime.views[id]).toEqual(serverView);
  });

  it('does not let a late save response populate a different session cache', async () => {
    const oldView = view(baseSelection);
    const nextView = view({ ...baseSelection, session_id: 's2', revision: 1 });
    const oldId = JSON.stringify(sessionHarnessKey(schema.revision, 'u1', 's1', schema.runtime.http.routes.chat_session_harness));
    const nextId = JSON.stringify(sessionHarnessKey(schema.revision, 'u1', 's2', schema.runtime.http.routes.chat_session_harness));
    runtime.views[oldId] = oldView; runtime.views[nextId] = nextView;
    const pending = deferred<HarnessView>(); runtime.put.mockReturnValueOnce(pending.promise);
    const first = render('s1');
    const saving = first.save({ ...baseSelection, operation_id: 'changed' });
    render('s2');
    pending.resolve(view({ ...baseSelection, operation_id: 'changed', revision: 5 }));
    await saving; await flush();
    expect(runtime.mutates[oldId]).not.toHaveBeenCalled();
    expect(runtime.views[nextId]).toEqual(nextView);
  });

  it('refreshes the frozen A binding when A ends and B starts, without token polling', async () => {
    const id = JSON.stringify(sessionHarnessKey(schema.revision, 'u1', 's1', schema.runtime.http.routes.chat_session_harness));
    const nextSelection = { ...baseSelection, revision: 5, digest: 'd5', operation_id: 'next' };
    const duringA = { ...view(nextSelection), turn: { assistant_message_id: 'assistant-a', selection: baseSelection } };
    runtime.views[id] = duringA;
    runtime.streams.s1 = { assistantDbId: 'assistant-a', isStreaming: true };
    expect(render().view?.turn?.assistant_message_id).toBe('assistant-a');
    expect(runtime.get).not.toHaveBeenCalled();
    const finished = view(nextSelection);
    runtime.get.mockResolvedValueOnce(finished);
    delete runtime.streams.s1;
    render(); await flush();
    expect(runtime.get).toHaveBeenCalledExactlyOnceWith('s1');
    expect(render().view?.turn).toBeNull();
    const duringB = { ...view(nextSelection), turn: { assistant_message_id: 'assistant-b', selection: nextSelection } };
    runtime.get.mockResolvedValueOnce(duringB);
    runtime.streams.s1 = { assistantDbId: 'assistant-b', isStreaming: true };
    render(); await flush();
    const updated = render().view!;
    expect(updated.turn?.assistant_message_id).toBe('assistant-b');
    expect(updated.turn?.selection).toEqual(updated.selection);
    runtime.streams.s1 = { ...runtime.streams.s1, textContent: 'another streamed token' };
    render(); await flush();
    expect(runtime.get).toHaveBeenCalledTimes(2);
  });

  it('keeps turn revalidation and late responses in their owner/session cache', async () => {
    const firstId = JSON.stringify(sessionHarnessKey(schema.revision, 'u1', 's1', schema.runtime.http.routes.chat_session_harness));
    const nextId = JSON.stringify(sessionHarnessKey(schema.revision, 'u1', 's2', schema.runtime.http.routes.chat_session_harness));
    runtime.views[firstId] = view(baseSelection);
    const nextView = view({ ...baseSelection, session_id: 's2', digest: 's2' });
    runtime.views[nextId] = nextView;
    render('s1');
    const pending = deferred<HarnessView>(); runtime.get.mockReturnValueOnce(pending.promise);
    runtime.streams.s1 = { assistantDbId: 'late-a', isStreaming: true };
    render('s1');
    expect(runtime.get).toHaveBeenCalledWith('s1');
    render('s2');
    runtime.streams.s1 = { assistantDbId: 'different-a', isStreaming: true };
    render('s2');
    pending.resolve({ ...view(baseSelection), turn: { assistant_message_id: 'late-a', selection: baseSelection } });
    await flush();
    expect(render('s2').view).toEqual(nextView);
    expect(runtime.mutates[nextId]).not.toHaveBeenCalled();
    expect(runtime.get).toHaveBeenCalledTimes(1);
  });
  it.each(['put', 'conflict-get'])('discards an old %s response after leaving and returning to the same session', async responseKind => {
    const id = JSON.stringify(sessionHarnessKey(schema.revision, 'u1', 's1', schema.runtime.http.routes.chat_session_harness));
    runtime.views[id] = view(baseSelection);
    const delayed = deferred<HarnessView>();
    if (responseKind === 'put') runtime.put.mockReturnValueOnce(delayed.promise);
    else { runtime.put.mockRejectedValueOnce(new HarnessBrowserError('conflict', 409)); runtime.get.mockReturnValueOnce(delayed.promise); }
    const saving = render('s1').save({ ...baseSelection, sources: [{ kind: 'input_asset', parameter: 'source', asset_id: 'old-upload' }] }).catch(error => error);
    await flush(); render('s2');
    const newer = view({ ...baseSelection, revision: 6, digest: 'd6', sources: [{ kind: 'input_asset', parameter: 'source', asset_id: 'new-upload' }] });
    runtime.views[id] = newer; render('s1');
    delayed.resolve(view({ ...baseSelection, revision: 5, digest: 'd5', sources: [{ kind: 'input_asset', parameter: 'source', asset_id: 'old-upload' }] }));
    await saving;
    expect(render('s1').view).toEqual(newer);
    expect(runtime.put).toHaveBeenCalledOnce(); expect(runtime.mutates[id]).not.toHaveBeenCalled();
  });
  it('keeps a newer cached revision when a delayed same-session save settles', async () => {
    const id = JSON.stringify(sessionHarnessKey(schema.revision, 'u1', 's1', schema.runtime.http.routes.chat_session_harness));
    runtime.views[id] = view(baseSelection); const delayed = deferred<HarnessView>(); runtime.put.mockReturnValueOnce(delayed.promise);
    const saving = render().save(baseSelection);
    const newer = view({ ...baseSelection, revision: 6, digest: 'd6' }); runtime.views[id] = newer;
    // The cache may advance before React has rendered it; the functional cache update must compare revisions.
    delayed.resolve(view({ ...baseSelection, revision: 5, digest: 'd5' })); await saving;
    expect(render().view).toEqual(newer); expect(runtime.put).toHaveBeenCalledOnce();
  });

  it('restores save liveness after the StrictMode effect lifecycle is replayed', async () => {
    const id = JSON.stringify(sessionHarnessKey(schema.revision, 'u1', 's1', schema.runtime.http.routes.chat_session_harness));
    runtime.views[id] = view(baseSelection); const hook = render();
    const setups = [...runtime.effectSetups]; const cleanups = setups.map(setup => setup());
    cleanups.forEach(cleanup => { if (typeof cleanup === 'function') cleanup(); }); setups.forEach(setup => setup());
    const next = view({ ...baseSelection, revision: 5, digest: 'd5' }); runtime.put.mockResolvedValueOnce(next);
    await hook.save(baseSelection);
    expect(render().view).toEqual(next); expect(runtime.put).toHaveBeenCalledOnce();
  });

});
