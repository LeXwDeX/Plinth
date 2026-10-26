import React, { type ReactElement, type ReactNode } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { HarnessSelection, HarnessView } from '@/lib/harness';
import type { ProductSchema } from '@/lib/product-schema';
const mock = vi.hoisted(() => ({ effects: [] as Array<() => void | (() => void)>, cells: [] as unknown[], cursor: 0, revision: 1, schema: null as unknown as ProductSchema, view: null as unknown as HarnessView, controls: new Map<string, (value: string) => void>(), actions: new Map<string, () => unknown>(), save: vi.fn(), changed: vi.fn(), refresh: vi.fn(async () => undefined) }));
// A bounded hook runner retains the actual component state across deliberate rerenders.
vi.mock('react', async original => ({ ...await original<typeof import('react')>(), useId: () => 'source', useEffect: (effect: () => void | (() => void)) => { mock.effects.push(effect); }, useMemo: (factory: () => unknown) => factory(), useState: (initial: unknown) => { const index = mock.cursor++; if (!(index in mock.cells)) mock.cells[index] = typeof initial === 'function' ? (initial as () => unknown)() : initial; return [mock.cells[index], (value: unknown) => { mock.cells[index] = typeof value === 'function' ? (value as (current: unknown) => unknown)(mock.cells[index]) : value; }]; }, useRef: (initial: unknown) => { const index = mock.cursor++; if (!(index in mock.cells)) mock.cells[index] = { current: initial }; return mock.cells[index]; } }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema, text: (key: string) => key }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: mock.view, save: mock.save, busy: false }) }));
vi.mock('@/hooks/use-session-versions', () => ({ useSessionVersions: () => ({ tiles: [], isLoading: false }) }));
vi.mock('@/hooks/use-owned-model-sources', () => ({ useOwnedModelSources: () => ({ assets: [{ id: 'owned', original_filename: 'owned.glb' }], page: 1, hasNext: false, isLoading: false, refresh: mock.refresh }) }));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ activeSessionRevision: mock.revision }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (select: (state: unknown) => unknown) => select({ modelByDomain: { image: 'left', 'model-3d': 'left' } }) }));
vi.mock('swr', () => ({ default: () => ({ data: { remesh: [offering] }, mutate: vi.fn() }) }));
vi.mock('@/components/ui/option-select', () => ({ OptionSelect: ({ id, value, options, onValueChange }: { id: string; value: string; options: Array<{ value: string; label: string }>; onValueChange: (value: string) => void }) => { mock.controls.set(id, onValueChange); return <select value={value} onChange={() => {}}>{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>; } }));
vi.mock('@/components/ui/button', async original => ({ ...await original<typeof import('@/components/ui/button')>(), Action: ({ children, onClick, disabled }: { children: ReactNode; onClick: () => unknown; disabled: boolean }) => { if (!disabled) mock.actions.set(String(children), onClick); return <button disabled={disabled}>{children}</button>; } }));
import { HarnessPanel } from './harness-panel';
import { HarnessModelSourcePicker } from './harness-model-source-picker';
const offering: OfferingWire = { id: 'public-offering', revision: '1', provider_id: 'service', provider_name: 'Service', model_id: 'model', model_name: 'Model', capability: 'remesh', available: true, parameters: [{ name: 'source', type: 'asset', label: 'Source', semantic: 'source-asset', uploaded_model: { profile_id: 'static-glb-v1' } }] };
const selection: HarnessSelection = { session_id: 'session-a', revision: 3, digest: 'original', operation_id: 'remesh', offering_id: offering.id, offering_revision: '1', enabled_operations: ['remesh'], sources: [], fixed_parameters: {}, fillable_parameters: [], allow_prerequisites: false, updated_at: '' };
const panel = () => { mock.cursor = 0; return renderToStaticMarkup(<HarnessPanel sessionId="session-a" object={null} />); };
const picker = (session: string, assetId = 'old') => { mock.cursor = 0; return HarnessModelSourcePicker({ ownerId: 'owner', sessionId: session, object: null, offering, source: { kind: 'input_asset', parameter: 'source', asset_id: assetId }, sourceViews: [], disabled: false, onChange: mock.changed }); };
function inputIn(node: ReactNode): ReactElement<{ type?: string; onChange: (event: unknown) => void }> | undefined {
  if (!React.isValidElement(node)) return undefined;
  const element = node as ReactElement<{ type?: string; children?: ReactNode; onChange: (event: unknown) => void }>;
  if (element.type === 'input' && element.props.type === 'file') return element;
  for (const child of React.Children.toArray(element.props.children)) { const found = inputIn(child); if (found) return found; }
}
beforeEach(() => { mock.effects = []; mock.cells = []; mock.cursor = 0; mock.revision = 1; mock.controls.clear(); mock.actions.clear(); mock.changed.mockReset(); mock.save.mockReset(); mock.refresh.mockClear(); mock.schema = structuredClone(catalog.application) as unknown as ProductSchema; mock.schema.runtime.input_assets.model_profile = { id: 'static-glb-v1', revision: '1', asset_type: 'model', label: 'GLB', description: 'GLB', extensions: ['.glb'], media_types: ['model/gltf-binary'], upload_media_type: 'model/gltf-binary', limits: { max_bytes: 1024 } } as NonNullable<ProductSchema['runtime']['input_assets']['model_profile']>; mock.schema.chat.harness!.source_kinds = [{ id: 'task_asset', label: 'Task', description: 'Task' }, { id: 'input_asset', label: 'Upload', description: 'Upload' }]; mock.view = { selection, status: 'needs_input', reason: '', missing_parameters: [], can_propose: false, estimated_credits: 5 }; });
afterEach(() => vi.unstubAllGlobals());
describe('Harness upload draft and save flow', () => {
  it('selects an owned uploaded model and saves only an asset-only Harness payload', async () => {
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher);
    panel(); mock.controls.get('source-kind')!('input_asset'); panel(); mock.controls.get('source-uploaded')!('owned'); panel();
    await mock.actions.get('ui.harness.save')!();
    expect(mock.save).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ sources: [{ kind: 'input_asset', parameter: 'source', asset_id: 'owned' }] }));
    expect(fetcher).not.toHaveBeenCalled();
    expect(selection.sources).toEqual([]);
  });
  it('preserves a legacy omitted-kind Task source when saving another bounded setting', async () => {
    mock.view = { ...mock.view, selection: { ...selection, sources: [{ parameter: 'source', task_id: 'legacy', asset_id: 'asset' }] } };
    // The serialization helper must preserve existing source bytes on unrelated parameter edits.
    const { harnessSelectionInput } = await import('./harness-panel');
    const payload = harnessSelectionInput({ ...mock.view.selection, allow_prerequisites: true }, offering);
    expect(payload.sources).toEqual([{ parameter: 'source', task_id: 'legacy', asset_id: 'asset' }]);
    expect(payload.sources[0]).not.toHaveProperty('kind');
  });
  it.each(['session-b', 'session-a'])('does not attach a late upload after a session boundary returning to %s', async destination => {
    let resolve!: (response: Response) => void;
    const fetcher = vi.fn((_url: RequestInfo | URL, _options?: RequestInit) => new Promise<Response>(yes => { resolve = yes; })); vi.stubGlobal('fetch', fetcher);
    inputIn(picker('session-a'))!.props.onChange({ currentTarget: { files: [new File(['glb-bytes'], 'hero.glb')], value: 'hero.glb' } });
    mock.revision += 1; picker('session-b');
    if (destination === 'session-a') { mock.revision += 1; picker('session-a'); }
    resolve(new Response(JSON.stringify({ id: 'late-asset' }), { status: 201 }));
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledOnce()); await new Promise(yes => setTimeout(yes, 0));
    expect(mock.changed).not.toHaveBeenCalled(); expect(mock.refresh).not.toHaveBeenCalled();
    expect(fetcher.mock.calls[0][0]).toBe(mock.schema.runtime.http.routes.assets);
  });
  it('uploads original bytes then updates only the local source draft, without saving or creating work', async () => {
    const fetcher = vi.fn(async (_url: RequestInfo | URL, _options?: RequestInit) => new Response(JSON.stringify({ id: 'new-asset' }), { status: 201 })); vi.stubGlobal('fetch', fetcher);
    inputIn(picker('session-a'))!.props.onChange({ currentTarget: { files: [new File(['glb-bytes'], 'hero.glb')], value: 'hero.glb' } });
    await vi.waitFor(() => expect(mock.changed).toHaveBeenCalled());
    expect(mock.changed).toHaveBeenCalledExactlyOnceWith([{ kind: 'input_asset', parameter: 'source', asset_id: 'new-asset' }]);
    expect(mock.save).not.toHaveBeenCalled(); expect(fetcher).toHaveBeenCalledOnce();
    const html = renderToStaticMarkup(picker('session-a', 'new-asset'));
    expect(html).toContain('ui.harness.upload.uploaded');
    expect(html).toContain('role="status"');
    expect(html).not.toContain('role="alert"');
  });
  it('keeps the prior draft and filename when a replacement upload is unsupported', async () => {
    const fetcher = vi.fn(async (_url: RequestInfo | URL, _options?: RequestInit) => new Response(JSON.stringify({ id: 'first' }), { status: 201 })); vi.stubGlobal('fetch', fetcher);
    inputIn(picker('session-a'))!.props.onChange({ currentTarget: { files: [new File(['bytes'], 'original.glb')], value: '' } });
    await vi.waitFor(() => expect(mock.changed).toHaveBeenCalledOnce());
    fetcher.mockImplementation(async () => new Response(JSON.stringify({ error: { code: 'model_unsupported_profile', message: '格式不支持' } }), { status: 422 }));
    inputIn(picker('session-a', 'first'))!.props.onChange({ currentTarget: { files: [new File(['other'], 'replacement.glb')], value: '' } });
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2)); await new Promise(yes => setTimeout(yes, 0));
    const html = renderToStaticMarkup(picker('session-a', 'first'));
    expect(html).toContain('original.glb'); expect(html).toContain('格式不支持'); expect(html).not.toContain('replacement.glb');
    expect(mock.changed).toHaveBeenCalledOnce(); expect(mock.save).not.toHaveBeenCalled();
  });

  it('uploads a restored source after StrictMode runs effect setup, cleanup and setup again', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ id: 'strict-upload' }), { status: 201 })); vi.stubGlobal('fetch', fetcher);
    const tree = picker('session-a');
    const effects = mock.effects.splice(0); const cleanups = effects.map(effect => effect());
    cleanups.forEach(cleanup => { if (typeof cleanup === 'function') cleanup(); });
    effects.forEach(effect => effect());
    inputIn(tree)!.props.onChange({ currentTarget: { files: [new File(['bytes'], 'strict.glb')], value: '' } });
    await vi.waitFor(() => expect(mock.changed).toHaveBeenCalledExactlyOnceWith([{ kind: 'input_asset', parameter: 'source', asset_id: 'strict-upload' }]));
    expect(fetcher).toHaveBeenCalledOnce();
  });

});
