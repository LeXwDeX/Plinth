import { describe, expect, it } from 'vitest';

import { parseMultiviewRequest } from '@/lib/chat/browser-client';
import { buildMultiviewRequest, multiviewLockNote, multiviewOffering } from '@/lib/workspace/multiview-upload';
import type { OfferingWire } from '@/lib/agent-offerings';

// #65: a user message carries the structured multiview request it was sent with, so regenerating sends it again.
describe('parseMultiviewRequest', () => {
  it('reads the stored request back', () => {
    expect(parseMultiviewRequest({ group: 'run1', model: 'mv', views: [
      { view: 'front', task_id: 't1', asset_id: 'x1' }, { view: 'back', asset_id: 'up3' },
    ] })).toEqual({ group: 'run1', model: 'mv', views: [{ view: 'front', taskId: 't1', assetId: 'x1' }, { view: 'back', assetId: 'up3' }] });
    expect(parseMultiviewRequest({ model: 'mv', views: [{ view: 'front', asset_id: 'u1' }] })).toEqual({ model: 'mv', views: [{ view: 'front', assetId: 'u1' }] });
  });

  it('treats anything malformed as no request', () => {
    for (const value of [null, 'x', [], { views: [] }, { model: 'mv', views: [] }, { model: 'mv', views: [{ view: 'front' }] }, { model: 'mv', views: 'no' }]) {
      expect(parseMultiviewRequest(value)).toBeNull();
    }
  });
});

// #373: the model the person picked on the panel is the one the turn carries and the one a regeneration sends again,
// even when the session's 3D pick is another model that takes no views.
describe('the request of a panel whose target is not the session pick', () => {
  const routes = { asset_item: '/api/v1/assets/{assetID}', task_asset: '/api/v1/tasks/{taskID}/assets/{assetID}' };
  const views = [{ value: 'front', label: 'Front' }, { value: 'left', label: 'Left' }];
  const make = (modelId: string, modelName: string, parameters: object[]) => ({ id: modelId, model_id: modelId, model_name: modelName, available: true, parameters }) as unknown as OfferingWire;
  const rodin = make('rodin-gen-2.5', 'Rodin Gen-2.5', [{ name: 'reference_assets', label: 'r', type: 'asset-array', semantic: 'reference-image', max_items: 5 }]);
  const p2 = make('tripo-p2-multiview', 'Tripo P2 Multiview', [
    { name: 'reference_assets', label: 'r', type: 'asset-array', semantic: 'reference-image', min_items: 2, max_items: 4 },
    { name: 'views', label: 'v', type: 'string-array', semantic: 'reference-views', options: views },
  ]);

  it('carries the target model through the stored copy, and the panel names the pick it replaces', () => {
    const offerings = [rodin, p2];
    const target = multiviewOffering(offerings, 'rodin-gen-2.5')!;
    expect(multiviewLockNote(offerings, target, 'rodin-gen-2.5')).toEqual({ locked: 'Rodin Gen-2.5', model: 'Tripo P2 Multiview' });
    const request = buildMultiviewRequest(routes, target.offering.model_id, ['/api/v1/assets/a1', '/api/v1/tasks/t2/assets/a2'], ['front', 'left'])!;
    expect(request.model).toBe('tripo-p2-multiview');
    const stored = { model: request.model, views: request.views.map((view) => ({ view: view.view, asset_id: view.assetId, ...(view.taskId ? { task_id: view.taskId } : {}) })) };
    expect(parseMultiviewRequest(stored)).toEqual(request);
  });
});
