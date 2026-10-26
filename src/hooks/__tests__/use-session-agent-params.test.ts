import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { ParamSchema } from '@/lib/providers/types';
const runtime = vi.hoisted(() => ({
  slots: [] as any[], cursor: 0, owner: 'owner1', loading: false, error: undefined as unknown,
  servers: {} as Record<string, Record<string, unknown>>, keys: [] as any[],
  put: vi.fn(async (_session: string, _patch: Record<string, unknown>, _signal?: AbortSignal, _offering?: string) => ({} as Record<string, unknown>)), mutate: vi.fn(),
  mutateGlobal: vi.fn(async (_key: unknown, _data: unknown, _options?: unknown) => undefined),
}));
vi.mock('react', () => ({
  useMemo: (factory: () => unknown) => factory(),
  useRef: (initial: unknown) => { const index = runtime.cursor++; return runtime.slots[index] ??= { current: initial }; },
  useState: (initial: unknown) => {
    const index = runtime.cursor++; if (!(index in runtime.slots)) runtime.slots[index] = typeof initial === 'function' ? (initial as () => unknown)() : initial;
    return [runtime.slots[index], (value: any) => { runtime.slots[index] = typeof value === 'function' ? value(runtime.slots[index]) : value; }];
  },
  useSyncExternalStore: (_subscribe: unknown, snapshot: () => unknown) => snapshot(),
  useEffect: (effect: () => void | (() => void), dependencies: unknown[]) => {
    const index = runtime.cursor++; const previous = runtime.slots[index];
    if (!previous || dependencies.some((value, position) => value !== previous.dependencies[position])) {
      previous?.cleanup?.(); runtime.slots[index] = { dependencies, cleanup: effect() };
    }
  },
}));
const schema = structuredClone(catalog.application) as unknown as ProductSchema;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: runtime.owner } }) }));
vi.mock('sonner', () => ({ toast: { error: vi.fn() } }));
vi.mock('swr', () => ({
  default: (key: any) => { runtime.keys.push(key); return { data: { params: runtime.servers[key?.[3]] ?? {}, parameterStates: Object.fromEntries(Object.entries(runtime.servers[key?.[3]] ?? {}).map(([name,value]) => [name,{value,source:'user'}])) }, isLoading: runtime.loading, error: runtime.error, mutate: runtime.mutate }; },
  mutate: (...args: Parameters<typeof runtime.mutateGlobal>) => runtime.mutateGlobal(...args),
}));
vi.mock('@/lib/agent-params', async (original) => ({ ...(await original<typeof import('@/lib/agent-params')>()), createAgentHarnessClient: () => ({ getParamsWithState: vi.fn(), putParamsWithState: async (...args: Parameters<typeof runtime.put>) => ({ params: await runtime.put(...args), parameterStates: {} }) }) }));
import { resetPendingAgentParams, useSessionAgentParams, sessionAgentParamsKey } from '../use-session-agent-params';
const parameters: ParamSchema[] = [{ name: 'size', label: 'Size', type: 'number', default: 1 }];
const render = (session = 's1', offering = 'image/offering') => { runtime.cursor = 0; return useSessionAgentParams(session, parameters, offering); };
beforeEach(() => {
  runtime.slots = []; runtime.cursor = 0; runtime.owner = 'owner1'; runtime.loading = false; runtime.error = undefined; runtime.servers = {}; runtime.keys = [];
  runtime.put.mockReset().mockResolvedValue({ size: 4 }); runtime.mutate.mockReset(); runtime.mutateGlobal.mockClear();
  resetPendingAgentParams(); vi.useRealTimers();
});
describe('authoritative scoped parameter lifecycle', () => {
  it('mounts and rereads effective defaults without any PUT', () => {
    expect(render().values.size).toBe(1); expect(render().values.size).toBe(1);
    expect(runtime.put).not.toHaveBeenCalled();
  });
  it('writes an explicit edit only to its exact owner/session/Offering cache and API context', async () => {
    const hook = render(); hook.setValue('size', 4); await hook.flush();
    expect(runtime.put).toHaveBeenCalledWith('s1', { size: 4 }, undefined, 'image/offering');
    expect(runtime.keys[0]).toEqual(sessionAgentParamsKey(schema, 'owner1', 's1', 'image/offering'));
  });
  it('does not inherit edits when the owner, session, Offering or Schema revision changes', () => {
    let hook = render(); hook.setValue('size', 7);
    expect(render().values.size).toBe(7);
    expect(render('s2').values.size).toBe(1);
    expect(render('s2', 'model/offering').values.size).toBe(1);
    runtime.owner = 'owner2'; expect(render('s2', 'model/offering').values.size).toBe(1);
    hook = render('s2', 'model/offering'); hook.setValue('size', 9);
    const original = schema.revision; schema.revision = 'another-schema';
    expect(render('s2', 'model/offering').values.size).toBe(1); schema.revision = original;
  });
  it('refuses submit while its authoritative GET is loading or failed', async () => {
    runtime.loading = true; await expect(render().flush()).rejects.toThrow();
    runtime.loading = false; runtime.error = new Error('offline'); await expect(render().flush()).rejects.toThrow();
    expect(runtime.put).not.toHaveBeenCalled();
  });
  it('a save that finishes after a session switch updates only the scope it was made for (#246 note 96916)', async () => {
    let answer!: (value: Record<string, unknown>) => void;
    runtime.put.mockImplementationOnce(() => new Promise((resolve) => { answer = resolve; }));
    const hook = render(); hook.setValue('size', 4); const submitted = hook.flush();
    await Promise.resolve(); await Promise.resolve(); render('s2'); answer({ size: 4 });
    await submitted;
    expect(runtime.mutateGlobal).toHaveBeenCalledTimes(1);
    expect(runtime.mutateGlobal.mock.calls[0][0]).toEqual(sessionAgentParamsKey(schema, 'owner1', 's1', 'image/offering'));
    expect(runtime.mutate).not.toHaveBeenCalled(); expect(render('s2').values.size).toBe(1);
  });
  it('keeps newer pending edits when an earlier response arrives', async () => {
    let answer!: (value: Record<string, unknown>) => void;
    runtime.put.mockImplementationOnce(() => new Promise((resolve) => { answer = resolve; }));
    const hook = render(); hook.setValue('size', 4); const submitted = hook.flush();
    await Promise.resolve(); await Promise.resolve(); render().setValue('size', 8); answer({ size: 4 }); await submitted;
    expect(render().values.size).toBe(8);
    await render().flush(); expect(runtime.put.mock.calls[1][1]).toEqual({ size: 8 });
  });
});

it('never pins effective defaults during submit flush, and reset sends only null for its exact Offering', async () => {
  await render().flush(); expect(runtime.put).not.toHaveBeenCalled();
  runtime.servers['image/offering']={size:4};
  const hook=render(); expect(hook.parameterStates.size.source).toBe('user');
  hook.setValue('size',null); expect(render().values.size).toBe(1); expect(render().parameterStates.size.source).toBe('default');
  await render().flush(); expect(runtime.put).toHaveBeenCalledWith('s1',{size:null},undefined,'image/offering');
});
it('preserves explicit false and empty strings instead of converting them into auto', async () => {
 const hook=render();hook.setValue('size','');await hook.flush();expect(runtime.put.mock.calls[0][1]).toEqual({size:''});
});

/*
 * #246 note 96916: the 参数 panel and the canvas board read the same Offering through two hook instances. An edit
 * that was still waiting for its delayed save, or whose save was running, when the Offering changed used to be
 * dropped from saving but kept in that instance, so the two showed different sizes for good.
 */
describe('unsaved edits are one shared truth and are saved for the Offering they were made for', () => {
  const gpt = 'openai/gpt-image-2/text-to-image';
  const seedream = 'jimeng/seedream-4-5/text-to-image';

  it('every reader of the scope sees an edit at once', () => {
    render('s1', gpt).setValue('size', 7);
    // Another component (another hook call) reading the same scope.
    runtime.slots = []; expect(render('s1', gpt).values.size).toBe(7);
    expect(render('s1', seedream).values.size).toBe(1);
  });

  it('switching Offering inside the save delay still saves the edit for its own Offering', async () => {
    vi.useFakeTimers();
    runtime.put.mockResolvedValueOnce({ size: 1024 });
    render('s1', gpt).setValue('size', 1024);
    render('s1', seedream); render('s1', 'gemini/gemini-3-pro-image/text-to-image');
    expect(runtime.put).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(schema.runtime.frontend.harness_save_debounce_milliseconds || 500);
    expect(runtime.put).toHaveBeenCalledWith('s1', { size: 1024 }, undefined, gpt);
    expect(runtime.mutateGlobal.mock.calls[0][0]).toEqual(sessionAgentParamsKey(schema, 'owner1', 's1', gpt));
    // Back on GPT: the value shown is the server's, which now holds the edit; nothing local is left over.
    runtime.servers[gpt] = { size: 1024 };
    expect(render('s1', gpt).values.size).toBe(1024);
    runtime.servers[gpt] = { size: 2048 };
    expect(render('s1', gpt).values.size).toBe(2048);
  });

  it('switching Offering during a running save neither aborts it nor leaves the edit behind', async () => {
    let answer!: (value: Record<string, unknown>) => void;
    runtime.put.mockImplementationOnce(() => new Promise((resolve) => { answer = resolve; }));
    const hook = render('s1', gpt); hook.setValue('size', 1024); const saving = hook.flush();
    await Promise.resolve(); await Promise.resolve();
    render('s1', seedream);
    answer({ size: 1024 }); await saving;
    expect(runtime.mutateGlobal.mock.calls[0][0]).toEqual(sessionAgentParamsKey(schema, 'owner1', 's1', gpt));
    runtime.servers[gpt] = { size: 2048 };
    expect(render('s1', gpt).values.size).toBe(2048);
  });

  it('a failed save drops the edits it carried, so no reader shows a value the server does not have', async () => {
    runtime.put.mockRejectedValueOnce(new Error('offline'));
    runtime.servers[gpt] = { size: 2048 };
    const hook = render('s1', gpt); hook.setValue('size', 1024);
    expect(render('s1', gpt).values.size).toBe(1024);
    await expect(hook.flush()).rejects.toThrow('offline');
    const after = render('s1', gpt);
    expect(after.values.size).toBe(2048);
    expect(after.saveError).toBe(schema.content.messages['ui.workspace.harness.save_failed']);
    // The next edit clears the error.
    after.setValue('size', 1536); expect(render('s1', gpt).saveError).toBeNull();
  });

  it('the delayed save survives the component that made the edit going away', async () => {
    vi.useFakeTimers();
    render('s1', gpt).setValue('size', 1024);
    runtime.slots = []; runtime.cursor = 0; // the component is gone; nothing of it remains
    await vi.advanceTimersByTimeAsync(schema.runtime.frontend.harness_save_debounce_milliseconds || 500);
    expect(runtime.put).toHaveBeenCalledWith('s1', { size: 1024 }, undefined, gpt);
  });
});
