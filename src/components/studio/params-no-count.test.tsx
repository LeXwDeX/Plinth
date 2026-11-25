import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { GenerationParams } from '@/hooks/use-generation-params';
import type { OfferingWire } from '@/lib/agent-offerings';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { buildGenerationParams, effectiveOfferingValues, estimateCredits, paramsChipText } from '@/lib/workspace/generation-params';
const schema = catalog.application as unknown as ProductSchema;
vi.mock('react', async (original) => ({ ...await original<typeof import('react')>(), useId: () => 'count-test', useState: (initial: unknown) => [initial, vi.fn()] }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema, text: (key: string) => schemaText(schema, key) }) }));
import { ParamsDomainSection } from './params-popover';

function find(node: React.ReactNode, predicate: (props: Record<string, any>) => boolean): Record<string, any> | undefined {
  for (const child of React.Children.toArray(node)) {
    if (!React.isValidElement(child)) continue;
    const props = child.props as Record<string, any>;
    if (predicate(props)) return props;
    const nested = find(props.children, predicate);
    if (nested) return nested;
  }
}

// #586: how many images a turn makes follows what the person says. The count is an advised, read-only parameter
// the Agent sets, so the panel shows its value and the estimate it gives, with no control to change it.
const wire: OfferingWire = { id: 'fixture/image', revision: '1', provider_id: 'fixture', provider_name: 'Fixture', model_id: 'fixture', model_name: 'Fixture', capability: 'text-to-image', available: true,
  estimated_credits_unit_parameter: 'n', credits_metered: true, estimated_credits: 6,
  parameters: [
    { name: 'size', label: 'Size', type: 'select', semantic: 'output-size', default: '1024x1024', options: [{ value: '1024x1024', label: '1024x1024' }], presentation: { surface: 'provider', control: 'select' } },
    { name: 'n', label: 'Count', type: 'number', default: 1, minimum: 1, maximum: 10, integer: true, adjustment_policy: 'advised', presentation: { surface: 'readonly', control: 'number', step: 1 } },
  ],
};

function render(entered: Record<string, unknown>) {
  const values = effectiveOfferingValues(wire, entered), model = buildGenerationParams([wire]);
  const current = { offering: wire, model, values, estimate: estimateCredits(wire, values), setValue: vi.fn(), flush: async () => {} };
  const params: GenerationParams = { ...current, parameterStates: {}, domain: 'image', modelName: wire.model_name, ready: true, allowed: true, restrictedReason: '', failed: false, saveError: null, offerings: [wire], selectOffering: vi.fn(), nextTurn: current };
  return { node: ParamsDomainSection({ params, hasSession: true, skill: null }), model, values };
}

describe('the image count is not a panel control', () => {
  it('shows the count read-only and keeps it out of the settings and the chip', () => {
    const { node, model, values } = render({});
    expect(find(node, (p) => p['data-testid'] === 'params-count')).toBeUndefined();
    expect(model.paramSchemas.map((param) => param.name)).not.toContain('n');
    expect(find(node, (p) => p['data-testid'] === 'params-readonly' && p['data-parameter'] === 'n')?.children).toBe('1');
    expect(paramsChipText(model, values)).toBe('1:1 · 1024x1024');
  });

  it('prices the count the Agent set for the next turn', () => {
    const { node } = render({ n: 3 });
    expect(find(node, (p) => p['data-testid'] === 'params-readonly' && p['data-parameter'] === 'n')?.children).toBe('3');
    expect(find(node, (p) => p['data-testid'] === 'params-cost')?.children)
      .toBe(formatSchemaText(schemaText(schema, 'ui.studio.params.cost_many'), { unit: 6, total: 18 }));
  });
});
