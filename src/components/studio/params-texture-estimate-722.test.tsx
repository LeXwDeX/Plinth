import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { GenerationParams } from '@/hooks/use-generation-params';
import type { OfferingWire } from '@/lib/agent-offerings';
import { createAgentHarnessClient, parseAgentParameterProjection } from '@/lib/agent-params';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import type { NativeQuote } from '@/lib/pricing';
import { buildGenerationParams, effectiveOfferingValues, estimateCredits } from '@/lib/workspace/generation-params';
const schema = catalog.application as unknown as ProductSchema;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema, text: (key: string) => schemaText(schema, key) }) }));
import { ParamsDomainSection } from './params-popover';

// #722: the 生成参数 panel estimate follows the saved texture switch. The numbers are the Tripo H3 v3.1 text quotes
// of the real run in #704 (E4/E5): 90 with texture, 60 without and no texture component.
const unit = { version: 1 as const, unit: 'tripo.credit', unit_label: 'Tripo credits' };
const defaults: NativeQuote = { ...unit, base_amount: '10', extra_amount: '80', total_amount: '90', components: [
  { id: 'detailed_geometry', label: '精细几何', amount: '20' }, { id: 'extreme_texture', label: '极致纹理', amount: '20' },
  { id: 'format_conversions', label: '格式转换', amount: '30' }, { id: 'standard_texture', label: '标准纹理', amount: '10' }] };
const textureOff: NativeQuote = { ...unit, base_amount: '10', extra_amount: '50', total_amount: '60', components: [
  { id: 'detailed_geometry', label: '精细几何', amount: '20' }, { id: 'format_conversions', label: '格式转换', amount: '30' }] };
const tripo: OfferingWire = { id: 'tripo/tripo-v3.1-full/text-to-3d', revision: 'r', provider_id: 'tripo', provider_name: 'Tripo', model_id: 'tripo-v3.1-full', model_name: 'Tripo H3 v3.1', capability: 'text-to-3d', available: true, price_mode: 'native', default_native_quote: defaults, upper_native_quote: defaults, parameters: [
  { name: 'texture', label: '生成纹理', type: 'boolean', default: true, presentation: { surface: 'provider', control: 'boolean' } },
] } as unknown as OfferingWire;
const meshy: OfferingWire = { id: 'meshy/meshy-7.1/image-to-3d', revision: 'r', provider_id: 'meshy', provider_name: 'Meshy', model_id: 'meshy-7.1', model_name: 'Meshy 7.1', capability: 'image-to-3d', available: true, parameters: [
  { name: 'should_texture', label: '生成纹理', type: 'boolean', default: true, presentation: { surface: 'provider', control: 'boolean' } },
] } as unknown as OfferingWire;

function section(wire: OfferingWire, payload: Record<string, unknown>) {
  const projection = parseAgentParameterProjection(payload);
  const model = buildGenerationParams([wire]); const values = effectiveOfferingValues(wire, projection.params);
  const context = { offering: wire, model, values, parameterStates: projection.parameterStates, estimate: estimateCredits(wire, values, projection.nativeQuote), setValue: vi.fn(), flush: vi.fn(async () => {}) };
  const params: GenerationParams = { ...context, domain: 'model-3d', modelName: wire.model_name, ready: true, allowed: true, restrictedReason: '', failed: false, saveError: null, offerings: [wire], selectOffering: vi.fn(), nextTurn: context };
  return renderToStaticMarkup(<ParamsDomainSection params={params} hasSession skill={null} />);
}
const estimateLine = (amount: string) => schemaText(schema, 'ui.pricing.default_estimate').replace('{amount}', `${amount} Tripo credits`);

describe('the panel estimate follows the saved texture switch (#722)', () => {
  it('reads the server quote of the saved parameters from the params projection, and drops a malformed one', async () => {
    expect(parseAgentParameterProjection({ params: { texture: false }, native_quote: textureOff }).nativeQuote).toEqual(textureOff);
    expect(parseAgentParameterProjection({ params: {}, native_quote: { ...textureOff, total_amount: '61' } }).nativeQuote).toBeUndefined();
    expect(parseAgentParameterProjection({ params: {} }).nativeQuote).toBeUndefined();
    const fetcher = vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ params: { texture: false }, parameter_states: { texture: { source: 'user', value: false } }, native_quote: textureOff }) })) as unknown as typeof fetch;
    const client = createAgentHarnessClient({ fetcher, routes: { params: schema.runtime.http.routes.agent_params, consent: schema.runtime.http.routes.agent_consent } });
    await expect(client.putParamsWithState('session', { texture: false }, undefined, tripo.id)).resolves.toMatchObject({ nativeQuote: textureOff });
  });

  it('shows the texture-off estimate and its components instead of the default 90', () => {
    const html = section(tripo, { params: { texture: false }, parameter_states: { texture: { source: 'user', value: false } }, native_quote: textureOff });
    expect(html).toContain(estimateLine('60'));
    expect(html).not.toContain(estimateLine('90'));
    expect(html).not.toContain('极致纹理'); expect(html).not.toContain('标准纹理');
    // The upper bound still differs from the shown estimate, so it is still said.
    expect(html).toContain(schemaText(schema, 'ui.pricing.upper_estimate').replace('{amount}', '90 Tripo credits'));
  });

  it('keeps the Offering default estimate until the server has quoted the saved parameters', () => {
    const html = section(tripo, { params: {} });
    expect(html).toContain(estimateLine('90'));
    expect(estimateCredits(tripo, {}, undefined).nativeDefaultQuote).toEqual(defaults);
  });

  it('shows no native figure for Meshy single image, which has no native quote, with texture off', () => {
    const html = section(meshy, { params: { should_texture: false }, parameter_states: { should_texture: { source: 'user', value: false } } });
    expect(html).not.toContain(schemaText(schema, 'ui.pricing.default_estimate').replace('{amount}', ''));
    expect(estimateCredits(meshy, { should_texture: false }, undefined)).toEqual({ unit: null, count: 1, total: null, metered: false });
  });
});
