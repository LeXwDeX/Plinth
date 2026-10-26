import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { NO_CREDITS, type OfferingWire } from '@/lib/agent-offerings';
import type { ProductSchema } from '@/lib/product-schema';

import { offeringQuoteText, processingCostLine, processingStepsCost, processingStepsCostLine } from './processing-cost';

it('says to choose a step rather than pricing zero steps', () => {
  expect(processingCostLine(0, true, 0, 0)).toEqual({ kind: 'empty' });
});

it('reports a total, a range, or that the price is not declared', () => {
  expect(processingCostLine(2, true, 30, 30)).toEqual({ kind: 'total', credits: 30 });
  expect(processingCostLine(2, true, 30, 45)).toEqual({ kind: 'range', low: 30, high: 45 });
  expect(processingCostLine(1, false, 0, 0)).toEqual({ kind: 'unknown' });
});

it('shows no credits when no chosen step bills in credits', () => {
  expect(processingCostLine(2, true, 0, 0, 0)).toEqual({ kind: 'unmetered' });
  expect(processingCostLine(2, true, 5, 5, 1)).toEqual({ kind: 'total', credits: 5 });
});

describe('offeringQuoteText', () => {
  const schema = defaultCatalog.application as unknown as ProductSchema;
  const messages = defaultCatalog.application.content.messages;
  const base: OfferingWire = { id: 'o', revision: '1', provider_id: 'p', provider_name: 'P', model_id: 'm', model_name: 'M', capability: 'remesh', available: true, parameters: [] };

  it('states the Schema quote of one Offering the same way the step line does', () => {
    expect(offeringQuoteText(schema, { ...base, credits_metered: true, estimated_credits: 12 })).toBe(`${messages['ui.workspace.harness.credits']}: 12`);
    expect(offeringQuoteText(schema, { ...base, credits_metered: true, estimated_credits: 50, maximum_estimated_credits: 100 })).toBe(`${messages['ui.workspace.harness.credits']}: 50–100`);
    expect(offeringQuoteText(schema, { ...base, credits_metered: true })).toBe(messages['ui.workspace.harness.credits_unknown']);
    expect(offeringQuoteText(schema, { ...base, credits_metered: false, estimated_credits: 3 })).toBe(`${messages['ui.workspace.harness.credits']}: ${NO_CREDITS}`);
    expect(offeringQuoteText(schema, { ...base, price_mode: 'native', default_native_quote: { version: 1, unit: 'hyper3d.credits', unit_label: 'Hyper3D credits', base_amount: '0.5', extra_amount: '0', total_amount: '0.5' } }))
      .toBe(messages['ui.pricing.default_estimate'].replace('{amount}', '0.5 Hyper3D credits'));
    expect(offeringQuoteText(schema, { ...base, price_mode: 'native' })).toBe(messages['ui.pricing.unknown']);
  });
});

// #324: Tripo segmentation's five exports are fixed, so the step costs 90 and the line says 90, not
// 40–90; a step that cannot run on this model (#321 note 99042: retexture on a 105 MB Rodin model)
// is not priced, and when no chosen step can run the line says so instead of a total.
describe('processingStepsCostLine (#324)', () => {
  const tripo = { id: 'tripo/geometry/segment', available: true, credits_metered: true, estimated_credits: 90, maximum_estimated_credits: 90 } as unknown as OfferingWire;
  const meshy = { id: 'meshy/processing/retexture', available: true, credits_metered: true, estimated_credits: 15 } as unknown as OfferingWire;
  const runs = { kind: 'task' } as const;

  it('states one price for a step whose billed options are all fixed', () => {
    expect(processingStepsCostLine([{ offering: tripo, route: runs }])).toEqual({ kind: 'total', credits: 90 });
  });

  it('leaves out a step that cannot run on the model', () => {
    expect(processingStepsCostLine([{ offering: tripo, route: runs }, { offering: meshy, route: null }])).toEqual({ kind: 'total', credits: 90 });
    expect(processingStepsCostLine([{ offering: meshy, route: null }])).toEqual({ kind: 'none' });
    expect(processingStepsCostLine([])).toEqual({ kind: 'empty' });
  });
});

// #335: one step priced in its provider's own unit used to hide every credit-priced step from the footer,
// and that branch listed steps that cannot run. Both parts now come from the steps that can run only.
describe('processingStepsCost (#335)', () => {
  const quote = { version: 1, unit: 'hyper3d.credits', unit_label: 'Hyper3D credits', base_amount: '2', extra_amount: '0', total_amount: '2' };
  const hyper3d = { id: 'hyper3d/texture/local-model', capability: 'retexture', available: true, price_mode: 'native', default_native_quote: quote } as unknown as OfferingWire;
  const bang = { id: 'hyper3d/bang/local-model', capability: 'mesh-segmentation', available: true, price_mode: 'native', default_native_quote: quote } as unknown as OfferingWire;
  const remesh = { id: 'meshy/processing/remesh', capability: 'remesh', available: true, credits_metered: true, estimated_credits: 5 } as unknown as OfferingWire;
  const rig = { id: 'meshy/processing/rigging', capability: 'rigging', available: true, credits_metered: true, estimated_credits: 5, maximum_estimated_credits: 15 } as unknown as OfferingWire;
  const runs = { kind: 'task' } as const;

  it('lists the native-priced steps and totals the credit-priced ones side by side', () => {
    const steps = [{ capability: 'retexture', offering: hyper3d, route: runs }, { capability: 'remesh', offering: remesh, route: runs }];
    expect(processingStepsCost(steps)).toEqual({ native: [steps[0]], credits: { kind: 'total', credits: 5 } });
  });

  it('keeps a credit range next to the native steps', () => {
    const steps = [{ capability: 'mesh-segmentation', offering: bang, route: runs }, { capability: 'rigging', offering: rig, route: runs }];
    expect(processingStepsCost(steps)).toEqual({ native: [steps[0]], credits: { kind: 'range', low: 5, high: 15 } });
  });

  it('leaves out every step that cannot run, native or not', () => {
    const blocked = { capability: 'mesh-segmentation', offering: bang, route: null, blocked: true };
    const unrouted = { capability: 'retexture', offering: hyper3d, route: null };
    const meshyUnrouted = { capability: 'remesh', offering: remesh, route: null };
    const rigging = { capability: 'rigging', offering: rig, route: runs };
    expect(processingStepsCost([unrouted, rigging])).toEqual({ native: [], credits: { kind: 'range', low: 5, high: 15 } });
    expect(processingStepsCost([{ ...blocked, route: runs }, meshyUnrouted, { capability: 'retexture', offering: hyper3d, route: runs }]))
      .toEqual({ native: [{ capability: 'retexture', offering: hyper3d, route: runs }], credits: null });
    expect(processingStepsCost([unrouted, meshyUnrouted])).toEqual({ native: [], credits: { kind: 'none' } });
    expect(processingStepsCost([])).toEqual({ native: [], credits: { kind: 'empty' } });
  });

  it('prices only native steps with no credit line when no credit-priced step can run', () => {
    const steps = [{ capability: 'retexture', offering: hyper3d, route: runs }];
    expect(processingStepsCost(steps)).toEqual({ native: steps, credits: null });
  });
});
