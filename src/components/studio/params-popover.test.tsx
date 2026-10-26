import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { GenerationParams } from '@/hooks/use-generation-params';
import type { OfferingWire } from '@/lib/agent-offerings';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { buildGenerationParams, effectiveOfferingValues, estimateCredits } from '@/lib/workspace/generation-params';
const schema = catalog.application as unknown as ProductSchema;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema, text: (key: string) => schemaText(schema, key) }) }));
import { ParamsDomainSection } from './params-popover';
const offering = (domain: string): OfferingWire => ({ id: `${domain}/selected`, revision: 'changed', provider_id: 'fixture', provider_name: 'Fixture', model_id: domain, model_name: `Selected ${domain}`, capability: domain === 'image' ? 'text-to-image' : 'text-to-3d', available: true, parameters: [
  { name: 'count', label: `${domain} count`, type: 'number', default: 1, minimum: 1, maximum: 5 },
  { name: 'quality', label: `${domain} definition`, type: 'select', fixed: true, default: 'ultra', options: [{ value: 'ultra', label: `${domain} exact maximum` }], presentation: { surface: 'readonly', visible_when: [{ parameter: 'count', operator: 'equals', value: 1 }] } },
  { name: 'secret', label: 'DO_NOT_EXPOSE', type: 'text', fixed: true, default: 'private', presentation: { surface: 'hidden' } },
] });
const params = (wire: OfferingWire, entered: Record<string, unknown> = {}): GenerationParams => {
  const model = buildGenerationParams([wire]); const values = effectiveOfferingValues(wire, entered);
  const context = { offering: wire, model, values, estimate: estimateCredits(wire, values), setValue: vi.fn(), flush: vi.fn(async () => {}) };
  return { ...context, domain: wire.model_id === 'image' ? 'image' : 'model-3d', modelName: wire.model_name, ready: true, allowed: true, restrictedReason: '', failed: false, saveError: null, offerings: [wire], selectOffering: vi.fn(), nextTurn: context };
};
describe('Schema-driven domain parameter sections', () => {
  it('renders both domain identities and changed fixed labels/enum values without a quality input', () => {
    const html = renderToStaticMarkup(<><ParamsDomainSection params={params(offering('image'))} hasSession skill={null} /><ParamsDomainSection params={params(offering('model'))} hasSession skill={null} /></>);
    expect(html).toContain('Selected image'); expect(html).toContain('Selected model');
    expect(html).toContain('image definition'); expect(html).toContain('model exact maximum');
    expect(html).not.toContain('DO_NOT_EXPOSE'); expect(html).not.toMatch(/<(?:input|select)[^>]*quality/);
    expect(html.match(/data-testid="params-readonly"/g)).toHaveLength(2);
  });
  it('obeys public readonly visible_when and keeps unrelated domain rows independent', () => {
    const html = renderToStaticMarkup(<><ParamsDomainSection params={params(offering('image'), { count: 2 })} hasSession skill={null} /><ParamsDomainSection params={params(offering('model'))} hasSession skill={null} /></>);
    expect(html).not.toContain('image definition'); expect(html).toContain('model definition');
    expect(html.match(/data-testid="params-readonly"/g)).toHaveLength(1);
  });
  it('keeps Agent scope restrictions visible and disables that domain edit controls', () => {
    const context = params({ ...offering('model'), estimated_credits_unit_parameter: 'count' }); context.allowed = false; context.restrictedReason = 'Schema scope reason';
    const html = renderToStaticMarkup(<ParamsDomainSection params={context} hasSession skill={null} />);
    expect(html).toContain('Schema scope reason'); expect(html).toContain('params-scope-restriction');
    expect(html).toMatch(/<button[^>]*disabled=""/); expect(html).toContain('model exact maximum');
  });
  it('reports a domain error and unavailable operation without borrowing other model settings', () => {
    const context = params({ ...offering('model'), available: false }); context.failed = true;
    const html = renderToStaticMarkup(<ParamsDomainSection params={context} hasSession skill={null} />);
    expect(html).toContain('role="alert"'); expect(html).toContain(schemaText(schema, 'ui.studio.params.unavailable'));
    expect(html).not.toContain('Selected image');
  });
});


it('shows the Agent source and reason on an advised readonly value without enabling a user control', () => {
  const wire = offering('model');
  wire.parameters = [
    { name: 'quality', label: 'Advised quality', type: 'select', default: 'ultra', adjustment_policy: 'advised', adjustment_reason: 'Schema quality advice', options: [{ value: 'ultra', label: 'Ultra' }, { value: 'draft', label: 'Draft' }], presentation: { surface: 'readonly' } },
    { name: 'secret', label: 'DO_NOT_EXPOSE', type: 'text', fixed: true, default: 'private', presentation: { surface: 'hidden' } },
  ];
  const context = params(wire, { quality: 'draft' });
  context.parameterStates = { quality: { value: 'draft', source: 'agent', reason: 'This frozen step needs a draft' } };
  expect(context.model.paramSchemas).toEqual([]);
  expect(context.model.readonlyParams.map(param => param.name)).toEqual(['quality']);
  const html = renderToStaticMarkup(<ParamsDomainSection params={context} hasSession skill={null} />);
  expect(html).toContain(schemaText(schema, 'ui.studio.params.not_recommended'));
  expect(html).toContain(schemaText(schema, 'ui.studio.params.source_agent'));
  expect(html).toContain('This frozen step needs a draft');
  expect(html).toContain('Schema quality advice');
  expect(html).toContain('Draft');
  expect(html).not.toContain('parameter-reset-auto');
  expect(html).not.toMatch(/<(?:input|select)/);
  expect(html).not.toContain('DO_NOT_EXPOSE');
});

// #243: the panel opens above its trigger; a content-sized height moved its top
// edge (title, operation select) whenever the operation changed.
describe('params panel frame', () => {
  it('has a fixed height bounded by the available space', async () => {
    const { PARAMS_PANEL_CLASS } = await import('./params-popover');
    const classes = PARAMS_PANEL_CLASS.split(/\s+/);
    expect(classes).toContain('h-[min(34rem,var(--available-height))]');
    expect(classes.some((name) => name.startsWith('max-h-'))).toBe(false);
    expect(classes).toContain('overflow-hidden');
  });
});
