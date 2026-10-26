import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { GenerationParams } from '@/hooks/use-generation-params';
import type { OfferingWire } from '@/lib/agent-offerings';
import { schemaText, type ProductSchema } from '@/lib/product-schema';
import { buildGenerationParams, effectiveOfferingValues, estimateCredits, paramsChipText, paramsSummary } from '@/lib/workspace/generation-params';
const schema = catalog.application as unknown as ProductSchema;
vi.mock('react', async (original) => ({ ...await original<typeof import('react')>(), useId: () => 'quantity-test', useState: (initial: unknown) => [initial, vi.fn()] }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema, text: (key: string) => schemaText(schema, key) }) }));
import { ParamsDomainSection } from './params-popover';

// Drive the actual button handlers and re-render with the saved parameter value.
// No DOM geometry is inferred here; that is checked against the deployed browser.
function find(node: React.ReactNode, predicate: (props: Record<string, any>) => boolean): Record<string, any> | undefined {
  for (const child of React.Children.toArray(node)) {
    if (!React.isValidElement(child)) continue;
    const props = child.props as Record<string, any>;
    if (predicate(props)) return props;
    const nested = typeof child.type === 'function' && child.type.name === 'ParameterOrigin'
      ? find((child.type as (props: any) => React.ReactNode)(props), predicate)
      : find(props.children, predicate);
    if (nested) return nested;
  }
}
function fixture(mode: 'unmetered' | 'native' | 'metered') {
  const wire: OfferingWire = { id: 'fixture/image', revision: '1', provider_id: 'fixture', provider_name: 'Fixture', model_id: 'fixture', model_name: 'Fixture', capability: 'text-to-image', available: true,
    estimated_credits_unit_parameter: 'n', credits_metered: mode === 'metered', estimated_credits: 6,
    ...(mode === 'native' ? { price_mode: 'native' as const } : {}),
    parameters: [{ name: 'n', label: 'Count', type: 'number', default: 2, minimum: 1, maximum: 4, integer: true, presentation: { control: 'number', step: 1 } }],
  };
  let entered: Record<string, unknown> = {};
  const writes: unknown[] = [];
  const setValue = (name: string, value: unknown) => { writes.push(value); entered = { ...entered, [name]: value }; };
  const context = (): GenerationParams => {
    const values = effectiveOfferingValues(wire, entered), model = buildGenerationParams([wire]);
    const current = { offering: wire, model, values, estimate: estimateCredits(wire, values), setValue, flush: async () => {} };
    return { ...current, parameterStates: { n: { value: values.n, source: entered.n === undefined || entered.n === null ? 'default' : 'user' } }, domain: 'image', modelName: wire.model_name, ready: true, allowed: true, restrictedReason: '', failed: false, saveError: null, offerings: [wire], selectOffering: vi.fn(), nextTurn: current };
  };
  const render = (hasSession = true) => ParamsDomainSection({ params: context(), hasSession, skill: null });
  const value = () => find(render(), p => p['data-testid'] === 'params-count-value')?.children;
  const button = (more: boolean) => find(render(), p => p['aria-label'] === schemaText(schema, more ? 'ui.studio.params.count_more' : 'ui.studio.params.count_less'))!;
  return { wire, context, render, value, button, writes, setValue };
}

describe('quantity controls use saved values independently of pricing', () => {
  it.each(['unmetered', 'native', 'metered'] as const)('%s: steps, boundaries, summaries and reset use effective parameters', mode => {
    const f = fixture(mode);
    expect(f.value()).toBe(2);
    f.button(true).onClick(); expect(f.writes.at(-1)).toBe(3); expect(f.value()).toBe(3);
    f.button(true).onClick(); expect(f.value()).toBe(4); expect(f.button(true).disabled).toBe(true);
    f.button(false).onClick(); expect(f.value()).toBe(3);
    const current = f.context();
    expect(paramsChipText(current.model, current.values, current.estimate, n => `${n} images`)).toBe('3 images');
    expect(paramsSummary(current.model, current.values, current.estimate, n => `${n} images`)).toBe('3 images');
    if (mode !== 'metered') expect(current.estimate).toMatchObject({ count: 1, total: null });
    f.button(false).onClick(); f.button(false).onClick(); expect(f.value()).toBe(1); expect(f.button(false).disabled).toBe(true);
    find(f.render(), p => p['data-testid'] === 'parameter-reset-auto')!.onClick(); expect(f.writes.at(-1)).toBe(null); expect(f.value()).toBe(2);
    f.setValue('n', 3); expect(f.value()).toBe(3); // an effective Agent suggestion uses the same value path
  });
  it('uses Schema constraints and step without the pricing safety ceiling', () => {
    const f = fixture('unmetered');
    f.wire.parameters[0].maximum = 2005; f.wire.parameters[0].presentation!.step = 2;
    f.setValue('n', 2001); expect(f.value()).toBe(2001);
    f.button(true).onClick(); expect(f.value()).toBe(2003);
    f.button(true).onClick(); expect(f.value()).toBe(2005); expect(f.button(true).disabled).toBe(true);
    f.setValue('n', 3.9); expect(f.value()).toBe(3);
    f.setValue('n', 'invalid'); expect(f.value()).toBe(2);
  });
  it('does not turn a conditionally locked saved quantity into a fictitious one', () => {
    const f = fixture('unmetered');
    f.wire.parameters[0].presentation!.disabled_when = [{ parameter: 'mode', operator: 'equals', value: 'locked' }];
    f.wire.parameters.push({ name: 'mode', label: 'Mode', type: 'text', default: 'locked' });
    f.setValue('n', 3);
    expect(f.value()).toBe(3);
    expect(find(f.render(), p => p['data-testid'] === 'params-count')?.['aria-disabled']).toBe(true);
    expect(find(f.render(), p => p['data-testid'] === 'parameter-disabled-reason')).toBeDefined();
  });
  it('explains why controls cannot be edited before a session exists', () => {
    const f = fixture('unmetered');
    expect(find(f.render(false), p => p['data-testid'] === 'params-disabled-reason')).toBeDefined();
    expect(find(f.render(false), p => p['data-testid'] === 'params-count')?.['aria-disabled']).toBe(true);
  });
});
