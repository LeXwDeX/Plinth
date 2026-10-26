import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import { processingSourceSteps, sourceRefusals, type ProcessingSourceTask } from './processing-source';

// #330: Meshy Convert joins the 加工 panel's 格式转换 services from the shipped Schema. It is listed only for a
// model it can take (a Meshy task by its original task, any saved model file up to 70 MiB), and it never becomes
// the default: with no choice the panel keeps the service it used before.

type CatalogOffering = (typeof defaultCatalog.offerings)[number] & { priority?: number; recommended?: boolean; enabled?: boolean };

/** The enabled format-conversion Offerings in the server's order (priority, then recommended, then ID). */
function conversions(): OfferingWire[] {
  return (defaultCatalog.offerings as CatalogOffering[])
    .filter((offering) => offering.capability === 'format-conversion' && offering.enabled)
    .sort((left, right) => (right.priority ?? 0) - (left.priority ?? 0)
      || Number(Boolean(right.recommended)) - Number(Boolean(left.recommended)) || left.id.localeCompare(right.id))
    .map((offering) => ({ ...(offering as unknown as OfferingWire), available: true }));
}

const CONVERT = 'meshy/processing/convert';
const glb = (size: number) => ({ id: 'model-glb', role: 'model', variant: 'glb', delivery: 'required', ordinal: 0,
  mediaType: 'model/gltf-binary', size, url: '/assets/model-glb', kind: 'model' as const, required: true });

function step(task: ProcessingSourceTask, offerings: OfferingWire[], chosen: Record<string, string> = {}) {
  const [found] = processingSourceSteps([{ id: 'format-conversion' }], ['format-conversion'], { 'format-conversion': offerings },
    task, task.assets[0], chosen);
  return found;
}

describe('Meshy Convert in the 格式转换 step', () => {
  const now = new Date().toISOString();
  const meshyTask: ProcessingSourceTask = { id: 'meshy-task', capability: 'text-to-3d', provider_id: 'meshy',
    offering_id: 'meshy/meshy-7.1/text-to-3d', state: 'SUCCEEDED', created_at: now, assets: [glb(5 << 20)] };
  const rodinTask: ProcessingSourceTask = { id: 'rodin-task', capability: 'text-to-3d', provider_id: 'hyper3d',
    offering_id: 'hyper3d/rodin-gen-2.5/text-to-3d', state: 'SUCCEEDED', created_at: now, assets: [glb(5 << 20)] };

  it('is listed after the existing services and leaves the default unchanged', () => {
    const all = conversions();
    expect(all.at(-1)?.id).toBe(CONVERT);
    for (const task of [meshyTask, rodinTask]) {
      const withConvert = step(task, all);
      const without = step(task, all.filter((offering) => offering.id !== CONVERT));
      expect(withConvert.choices.map((offering) => offering.id)).toContain(CONVERT);
      expect(withConvert.choices.at(-1)?.id).toBe(CONVERT);
      expect(withConvert.offering.id).toBe(without.offering.id);
    }
  });

  it('takes a Meshy result by its original task and another provider by its saved file', () => {
    expect(step(meshyTask, conversions(), { 'format-conversion': CONVERT }).route).toEqual({ sourceRole: 'source_task', value: 'meshy-task' });
    expect(step(rodinTask, conversions(), { 'format-conversion': CONVERT }).route).toEqual({ sourceRole: 'source_asset', value: 'model-glb' });
    const expired = { ...meshyTask, created_at: new Date(Date.now() - 3 * 86400_000).toISOString() };
    expect(step(expired, conversions(), { 'format-conversion': CONVERT }).route).toEqual({ sourceRole: 'source_asset', value: 'model-glb', taskExpired: true });
  });

  it('is not listed for a file over its 70 MiB limit, and says why', () => {
    const big = { ...rodinTask, assets: [glb(71 << 20)] };
    const found = step(big, conversions(), { 'format-conversion': CONVERT });
    expect(found.choices.map((offering) => offering.id)).not.toContain(CONVERT);
    expect(found.offering.id).not.toBe(CONVERT);
    const convert = conversions().find((offering) => offering.id === CONVERT)!;
    expect(sourceRefusals([convert], big, big.assets[0])).toEqual([{ offering: convert, kind: 'too_large', limit: 70 << 20 }]);
  });
});
