import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { ProcessingSourceStep } from '@/lib/workspace/processing-source';
import { ProcessingStepService } from './processing-step-service';

const messages = defaultCatalog.application.content.messages;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));
// The real dropdown renders its list in a portal; list every option inline so the markup shows them.
vi.mock('@/components/ui/option-select', () => ({
  OptionSelect: ({ value, options, id }: { value: string; id?: string; options: { value: string; label: string }[] }) => <ul id={id} data-value={value}>
    {options.map((option) => <li key={option.value} data-option={option.value}>{option.label}</li>)}
  </ul>,
}));

const local: OfferingWire = { id: 'hyper3d/bang/local-model', revision: '1', provider_id: 'hyper3d', provider_name: 'Hyper3D', model_id: 'bang', model_name: 'Bang（本地模型）',
  capability: 'mesh-segmentation', available: true, parameters: [], price_mode: 'native',
  default_native_quote: { version: 1, unit: 'hyper3d.credits', unit_label: 'Hyper3D credits', base_amount: '0.5', extra_amount: '0', total_amount: '0.5' } };
const rodin: OfferingWire = { ...local, id: 'hyper3d/bang/rodin-task', model_name: 'Bang（Rodin 来源）',
  default_native_quote: { ...local.default_native_quote!, base_amount: '1', total_amount: '1' } };
const step = (offering: OfferingWire, choices: OfferingWire[]): ProcessingSourceStep => ({ capability: 'mesh-segmentation', offering, route: { sourceRole: 'source_asset', value: 'asset' }, choices });
const render = (value: ProcessingSourceStep) => renderToStaticMarkup(<ProcessingStepService step={value} fieldId="field" disabled={false} onChoose={() => {}} />);

describe('ProcessingStepService (#321)', () => {
  it('names the only compatible service in gray text, as before, without a selector', () => {
    const html = render(step(local, [local]));
    expect(html).toContain('data-testid="processing-provider-mesh-segmentation"');
    expect(html).toContain('Hyper3D · Bang（本地模型）');
    expect(html).not.toContain(messages['ui.workspace.processing.offering']);
    expect(html).not.toContain('data-option');
  });

  it('lets the person choose among several compatible services, each with its provider, model and Schema quote', () => {
    const html = render(step(rodin, [local, rodin]));
    expect(html).toContain(`<label for="field" class="text-sm font-medium">${messages['ui.workspace.processing.offering']}</label>`);
    expect(html).toContain('data-value="hyper3d/bang/rodin-task"');
    const estimate = (amount: string) => messages['ui.pricing.default_estimate'].replace('{amount}', amount);
    expect(html).toContain(`<li data-option="hyper3d/bang/local-model">Hyper3D · Bang（本地模型） · ${estimate('0.5 Hyper3D credits')}</li>`);
    expect(html).toContain(`<li data-option="hyper3d/bang/rodin-task">Hyper3D · Bang（Rodin 来源） · ${estimate('1 Hyper3D credits')}</li>`);
  });

  it('still names the shown service when no Offering accepts the source', () => {
    const html = render({ ...step(local, []), route: null });
    expect(html).toContain('Hyper3D · Bang（本地模型）');
    expect(html).not.toContain('data-option');
  });
});
