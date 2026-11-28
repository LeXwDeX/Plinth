import { describe, expect, it } from 'vitest';

import type { OfferingParameterWire, OfferingWire } from '@/lib/agent-offerings';
import { assetFromURL, buildMultiviewRequest, chosenViewLabels, defaultViewChoices, multiviewEntryOffered, multiviewLockNote, multiviewOffering, validViewChoices } from '@/lib/workspace/multiview-upload';

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
const named = (modelId: string, modelName: string, parameters: OfferingParameterWire[], available = true): OfferingWire => (
  { ...offering(modelId, parameters, available), model_name: modelName } as OfferingWire
);

// #721: the 用作多视角 entry shows for 2–4 attached pictures with a multiview model and outside editing; never for one.
describe('multiviewEntryOffered', () => {
  const target = { offering: multi('tripo-h3'), views, minPictures: 2, maxPictures: 4 };
  it('offers the entry for two to four pictures', () => {
    expect([0, 1, 2, 3, 4, 5].map((count) => multiviewEntryOffered(target, count, false))).toEqual([false, false, true, true, true, false]);
  });
  it('does not offer it without a multiview model or while editing a sent message', () => {
    expect(multiviewEntryOffered(null, 2, false)).toBe(false);
    expect(multiviewEntryOffered(target, 2, true)).toBe(false);
  });
});

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

// #373: the panel makes the model the person picked for 3D when it takes several views, and otherwise the first
// multiview model; the request carries that target, whatever the session locked, and the panel says which.
describe('the multiview target against the picked 3D model', () => {
  const rodin = named('rodin', 'Rodin Gen-2.5', [{ name: 'reference_assets', label: 'r', type: 'asset-array', semantic: 'reference-image', max_items: 5 }]);
  const p2 = named('p2-multiview', 'Tripo P2 多视图', [
    { name: 'reference_assets', label: 'r', type: 'asset-array', semantic: 'reference-image', min_items: 2, max_items: 4 },
    { name: 'views', label: 'v', type: 'string-array', semantic: 'reference-views', options: views },
  ]);
  const v31 = named('v31-multiview', 'Tripo v3.1 多视图', p2.parameters);

  it('uses the picked model when it is a multiview model, with no note', () => {
    const offerings = [rodin, v31, p2];
    const target = multiviewOffering(offerings, 'p2-multiview')!;
    expect(target.offering.model_id).toBe('p2-multiview');
    expect(multiviewLockNote(offerings, target, 'p2-multiview')).toBeNull();
  });

  it('falls back to the first multiview model when the pick takes no views, and names both', () => {
    const offerings = [rodin, v31, p2];
    const target = multiviewOffering(offerings, 'rodin')!;
    expect(target.offering.model_id).toBe('v31-multiview');
    expect(multiviewLockNote(offerings, target, 'rodin')).toEqual({ locked: 'Rodin Gen-2.5', model: 'Tripo v3.1 多视图' });
    // The request the panel sends names the target, not the pick.
    const request = buildMultiviewRequest(routes, target.offering.model_id, ['/api/v1/assets/a1', '/api/v1/assets/a2'], ['front', 'left']);
    expect(request?.model).toBe('v31-multiview');
  });

  it('explains a picked multiview model that cannot run now by naming it', () => {
    const offerings = [{ ...p2, available: false } as OfferingWire, v31];
    const target = multiviewOffering(offerings, 'p2-multiview')!;
    expect(target.offering.model_id).toBe('v31-multiview');
    expect(multiviewLockNote(offerings, target, 'p2-multiview')).toEqual({ locked: 'Tripo P2 多视图', model: 'Tripo v3.1 多视图' });
  });

  it('says nothing without a pick and names a pick no service carries by its id', () => {
    const target = multiviewOffering([rodin, p2])!;
    expect(multiviewLockNote([rodin, p2], target, undefined)).toBeNull();
    expect(multiviewLockNote([rodin, p2], target, null)).toBeNull();
    expect(multiviewLockNote([rodin, p2], target, '')).toBeNull();
    expect(multiviewLockNote([rodin, p2], target, 'retired-model')).toEqual({ locked: 'retired-model', model: 'Tripo P2 多视图' });
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
