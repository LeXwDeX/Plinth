import { describe, expect, it } from 'vitest';

import type { OfferingParameterWire, OfferingWire } from '@/lib/agent-offerings';
import { assetFromURL, buildMultiviewRequest, chosenViewLabels, defaultViewChoices, multiviewOffering, validViewChoices } from '@/lib/workspace/multiview-upload';

const routes = { asset_item: '/api/v1/assets/{assetID}', task_asset: '/api/v1/tasks/{taskID}/assets/{assetID}' };
const views = [{ value: 'front', label: '正面' }, { value: 'left', label: '左侧' }, { value: 'back', label: '背面' }, { value: 'right', label: '右侧' }];

function offering(modelId: string, parameters: OfferingParameterWire[], available = true): OfferingWire {
  return { id: modelId, model_id: modelId, model_name: modelId, available, parameters } as unknown as OfferingWire;
}
const multi = (modelId: string, available = true) => offering(modelId, [
  { name: 'reference_assets', label: 'r', type: 'asset-array', semantic: 'reference-image', min_items: 2, max_items: 4 },
  { name: 'views', label: 'v', type: 'string-array', semantic: 'reference-views', options: views },
], available);
const single = offering('single', [{ name: 'reference_assets', label: 'r', type: 'asset-array', semantic: 'reference-image' }]);

describe('multiviewOffering', () => {
  it('detects by the reference-views semantic only, among available offerings', () => {
    expect(multiviewOffering([single])).toBeNull();
    expect(multiviewOffering([multi('m1', false)])).toBeNull();
    expect(multiviewOffering([single, multi('m1')])?.offering.model_id).toBe('m1');
  });
  it('prefers the picked model and reads the picture limits', () => {
    const found = multiviewOffering([multi('m1'), multi('m2')], 'm2')!;
    expect(found.offering.model_id).toBe('m2');
    expect([found.minPictures, found.maxPictures]).toEqual([2, 4]);
    expect(multiviewOffering([multi('m1'), multi('m2')], 'single')?.offering.model_id).toBe('m1');
  });
});

describe('view choices', () => {
  it('defaults to the views in order', () => {
    expect(defaultViewChoices(3, views)).toEqual(['front', 'left', 'back']);
    expect(defaultViewChoices(2, views.slice(0, 1))).toEqual(['front', '']);
  });
  it('validates front, uniqueness and count', () => {
    expect(validViewChoices(['front', 'left'])).toBe(true);
    expect(validViewChoices(['front', '', 'back'])).toBe(true);
    expect(validViewChoices(['left', 'back'])).toBe(false);
    expect(validViewChoices(['front', 'front'])).toBe(false);
    expect(validViewChoices(['front', '', ''])).toBe(false);
    expect(validViewChoices(['front', 'left', 'back'], 2, 2)).toBe(false);
  });
  it('words the chosen views in picture order', () => {
    expect(chosenViewLabels(['front', '', 'back'], views)).toEqual(['正面', '背面']);
  });
});

describe('asset addresses', () => {
  it('reads input assets and generated pictures, ignoring query strings and origins', () => {
    expect(assetFromURL(routes, '/api/v1/assets/a1?x=1')).toEqual({ assetId: 'a1' });
    expect(assetFromURL(routes, 'https://h/api/v1/tasks/t1/assets/a2')).toEqual({ assetId: 'a2', taskId: 't1' });
    expect(assetFromURL(routes, 'data:image/png;base64,AAAA')).toBeNull();
  });
  it('builds the request from chosen pictures only', () => {
    const urls = ['/api/v1/assets/a1', '/api/v1/tasks/t1/assets/a2', '/api/v1/assets/a3'];
    expect(buildMultiviewRequest(routes, 'm1', urls, ['front', 'left', ''])).toEqual({
      model: 'm1', views: [{ view: 'front', assetId: 'a1' }, { view: 'left', taskId: 't1', assetId: 'a2' }],
    });
    expect(buildMultiviewRequest(routes, 'm1', ['data:image/png;base64,AA', urls[0]], ['front', 'left'])).toBeNull();
  });
});
