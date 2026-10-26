import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import { ProcessingCostSummary } from './processing-cost-summary';

const messages = defaultCatalog.application.content.messages;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));

const label = (id: string) => defaultCatalog.application.capabilities.find((item) => item.id === id)!.label_cn;
const quote = { version: 1, unit: 'hyper3d.credits', unit_label: 'Hyper3D credits', base_amount: '2', extra_amount: '0', total_amount: '2' };
const hyper3dTexture = { id: 'hyper3d/texture/local-model', capability: 'retexture', available: true, price_mode: 'native', default_native_quote: quote } as unknown as OfferingWire;
const bang = { id: 'hyper3d/bang/local-model', capability: 'mesh-segmentation', available: true, price_mode: 'native', default_native_quote: { ...quote, base_amount: '0.5', total_amount: '0.5' } } as unknown as OfferingWire;
const meshyRemesh = { id: 'meshy/processing/remesh', capability: 'remesh', available: true, credits_metered: true, estimated_credits: 5 } as unknown as OfferingWire;
const meshyRigging = { id: 'meshy/processing/rigging', capability: 'rigging', available: true, credits_metered: true, estimated_credits: 5, maximum_estimated_credits: 15 } as unknown as OfferingWire;
const runs = { kind: 'task' };
const estimate = (amount: string) => messages['ui.pricing.default_estimate'].replace('{amount}', amount);
const render = (steps: Parameters<typeof ProcessingCostSummary>[0]['steps']) => renderToStaticMarkup(<ProcessingCostSummary steps={steps} />)
  .replace(/<!-- -->/g, '');

// #335: Meshy and Hyper3D chosen together. A step priced in its provider's own unit used to replace the
// credit total, and steps that cannot run were listed among the native estimates.
describe('ProcessingCostSummary (#335)', () => {
  it('shows the Hyper3D estimate and the Meshy credit total together', () => {
    const html = render([{ capability: 'retexture', offering: hyper3dTexture, route: runs }, { capability: 'remesh', offering: meshyRemesh, route: runs }]);
    expect(html).toContain('data-testid="processing-cost-native"');
    expect(html).toContain(messages['ui.pricing.totals_heading']);
    expect(html).toContain(`${label('retexture')}: ${estimate('2 Hyper3D credits')}`);
    expect(html).not.toContain(`${label('remesh')}: `);
    expect(html).toMatch(/data-testid="processing-cost" data-kind="total"/);
    expect(html).toContain(messages['ui.studio.processing.cost_total'].replace('{credits}', '5'));
  });

  it('keeps the credit range and its note beside the native estimates', () => {
    const html = render([{ capability: 'mesh-segmentation', offering: bang, route: runs }, { capability: 'rigging', offering: meshyRigging, route: runs }]);
    expect(html).toContain(`${label('mesh-segmentation')}: ${estimate('0.5 Hyper3D credits')}`);
    expect(html).toContain(messages['ui.studio.processing.cost_range'].replace('{low}', '5').replace('{high}', '15'));
    expect(html).toContain(messages['ui.workspace.processing.plan_cost_extra']);
  });

  it('lists no native estimate for a step that cannot run, and totals the rest', () => {
    const html = render([
      { capability: 'retexture', offering: hyper3dTexture, route: null },
      { capability: 'mesh-segmentation', offering: bang, route: null, blocked: true },
      { capability: 'remesh', offering: meshyRemesh, route: runs },
    ]);
    expect(html).not.toContain('data-testid="processing-cost-native"');
    expect(html).not.toContain(label('retexture'));
    expect(html).toContain(messages['ui.studio.processing.cost_total'].replace('{credits}', '5'));
  });

  it('says nothing is priced when no chosen step can run, even a native one', () => {
    const html = render([{ capability: 'retexture', offering: hyper3dTexture, route: null }, { capability: 'remesh', offering: meshyRemesh, route: null }]);
    expect(html).not.toContain('data-testid="processing-cost-native"');
    expect(html).toMatch(/data-testid="processing-cost" data-kind="none"/);
    expect(html).toContain(messages['ui.studio.processing.cost_none']);
  });

  it('shows only the native estimates when every step that can run is native-priced', () => {
    const html = render([{ capability: 'retexture', offering: hyper3dTexture, route: runs }]);
    expect(html).toContain(`${label('retexture')}: ${estimate('2 Hyper3D credits')}`);
    expect(html).not.toContain('data-testid="processing-cost"');
  });

  it('asks for a step when none is chosen', () => {
    expect(render([])).toContain(messages['ui.studio.processing.cost_empty']);
  });

  it('is the only price the 加工 panel footer shows', async () => {
    const { readFileSync } = await import('node:fs');
    const panel = readFileSync(new URL('./model-processing-panel.tsx', import.meta.url), 'utf8');
    const footer = panel.slice(panel.indexOf('<footer'), panel.indexOf('</footer>'));
    expect(footer).toContain('<ProcessingCostSummary steps={steps} />');
    expect(footer).not.toContain('price_mode');
    expect(footer).not.toContain('processing-cost');
  });
});
