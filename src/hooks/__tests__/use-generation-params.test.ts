import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
const runtime = vi.hoisted(() => ({
  choice: undefined as any, selectedModel: 'fixture', sessionValues: {} as Record<string, Record<string, unknown>>,
  loading: false, error: undefined as unknown, writes: vi.fn(), edits: vi.fn(),
}));
vi.mock('react', () => ({ useMemo: (factory: any) => factory(), useState: (initial: any) => { runtime.choice ??= initial; return [runtime.choice, (value: any) => { runtime.choice = value; }]; } }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog.application }) }));
vi.mock('@/hooks/use-session-agent-scope', () => ({ useActiveAgentScope: () => 'auto' }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (select: any) => select({ modelByDomain: { image: runtime.selectedModel, 'model-3d': runtime.selectedModel } }) }));
const text: OfferingWire = { id: 'fixture/text', revision: 'r', provider_id: 'fixture', provider_name: 'fixture', model_id: 'fixture', model_name: 'fixture', capability: 'text-to-image', available: true, estimated_credits: 2, credits_metered: true, estimated_credits_unit_parameter: 'n', parameters: [
  { name: 'n', label: 'Count', type: 'number', default: 1, minimum: 1, maximum: 4 },
  { name: 'text_only', label: 'Text specific', type: 'boolean', default: false },
] };
const image: OfferingWire = { ...text, id: 'fixture/image', capability: 'image-to-image', estimated_credits: 7, parameters: [
  { name: 'n', label: 'Image Count', type: 'number', default: 1, minimum: 1, maximum: 2 },
  { name: 'reference_assets', label: 'Ref', type: 'asset-array', semantic: 'reference-image' },
  { name: 'image_only', label: 'Image specific', type: 'boolean', default: true },
] };
vi.mock('@/hooks/use-workspace-offerings', () => ({ useWorkspaceOfferings: () => ({ offeringsByCapability: { text: [text], image: [image] } }) }));
vi.mock('@/hooks/use-session-agent-params', () => ({ useSessionAgentParams: (session: string, _schemas: unknown, offering: string) => ({
  enteredValues: runtime.sessionValues[offering] ?? {}, loading: runtime.loading, error: runtime.error, saveError: null, ownerId: 'owner',
  setValue: (name: string, value: unknown) => runtime.edits(session, offering, name, value),
  flush: async (values: unknown) => { runtime.writes(session, offering, values); },
}) }));
import { useGenerationParams } from '../use-generation-params';
beforeEach(() => { runtime.choice = undefined; runtime.selectedModel = 'fixture'; runtime.sessionValues = {}; runtime.loading = false; runtime.error = undefined; vi.clearAllMocks(); });
describe('inspected capability versus actual next-turn parameters', () => {
  it('keeps capability-specific fields and estimates exact rather than intersecting them', () => {
    const params = useGenerationParams('image', 's', false);
    expect(params.model.moreParams.map((param) => param.name)).toContain('text_only');
    expect(params.nextTurn.offering?.id).toBe(text.id);
    params.selectOffering(image.id);
    const alternate = useGenerationParams('image', 's', false);
    expect(alternate.model.moreParams.map((param) => param.name)).toContain('image_only');
    expect(alternate.model.moreParams.map((param) => param.name)).not.toContain('text_only');
    expect(alternate.nextTurn.offering?.id).toBe(text.id);
    expect(alternate.estimate.unit).toBe(7); expect(alternate.nextTurn.estimate.unit).toBe(2);
  });
  it('flushes only the actual next-turn dirty scope without re-pinning effective defaults', async () => {
    runtime.sessionValues[text.id] = { n: 1 }; runtime.sessionValues[image.id] = { n: 2 };
    const params = useGenerationParams('image', 's', false); params.selectOffering(image.id);
    const alternate = useGenerationParams('image', 's', false);
    expect(runtime.writes).not.toHaveBeenCalled(); expect(alternate.values.n).toBe(2); expect(alternate.nextTurn.values.n).toBe(1);
    await alternate.nextTurn.flush();
    expect(runtime.writes).toHaveBeenCalledWith('s', text.id, undefined);
  });
  it('recomputes context when reference applicability changes without carrying alternate values', () => {
    const params = useGenerationParams('image', 's', false); params.selectOffering(text.id);
    const reference = useGenerationParams('image', 's', true);
    expect(reference.offering?.id).toBe(image.id); expect(reference.nextTurn.offering?.id).toBe(image.id);
    expect(reference.values).not.toHaveProperty('text_only');
  });
  it('refuses submission before authoritative values have loaded', async () => {
    runtime.loading = true; const params = useGenerationParams('image', 's', false);
    await expect(params.nextTurn.flush()).rejects.toThrow('not ready'); expect(runtime.writes).not.toHaveBeenCalled();
  });
});
