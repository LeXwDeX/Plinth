import { describe, expect, it } from 'vitest';

import { groupPosition, stepResultTarget, type ResultTarget } from './result-target';

const member = (assetId: string): ResultTarget => ({ taskId: 't1', sessionId: 's1', kind: 'image', url: `/a/${assetId}`, assetId, title: 'v1', status: 'ready' });

describe('result groups', () => {
  const group = ['a', 'b', 'c'].map(member);

  it('knows the position inside the group and ignores a lone result', () => {
    expect(groupPosition({ ...group[1], group })).toEqual({ index: 1, count: 3 });
    expect(groupPosition(group[1])).toBeNull();
    expect(groupPosition({ ...group[0], group: [group[0]] })).toBeNull();
  });

  it('steps to the neighbour and keeps the group', () => {
    const next = stepResultTarget({ ...group[0], group }, 1);
    expect(next?.assetId).toBe('b');
    expect(next?.group).toBe(group);
    expect(stepResultTarget(next, -1)?.assetId).toBe('a');
  });

  it('stops at the ends', () => {
    expect(stepResultTarget({ ...group[0], group }, -1)).toBeNull();
    expect(stepResultTarget({ ...group[2], group }, 1)).toBeNull();
  });
});

import { targetOfTaskAssets, targetOfTile, objectOfTarget } from './result-target';
import { blankTile } from './versions';
import type { TrackedAsset } from './task-projection';
import type { ProductSchema } from '@/lib/product-schema';

const attachment: TrackedAsset = { id: 'report', role: 'report', variant: 'json', delivery: 'inline', ordinal: 0, mediaType: 'application/vnd.art-ai.print-report+json', size: 120, url: '/api/v1/tasks/t1/assets/report', kind: 'other' };
it('orders a four-view image Task by public slot identity and keeps a partial edit partial', () => {
  const slots = [{ variant: 'front', label: '正面' }, { variant: 'left', label: '左侧' }, { variant: 'back', label: '背面' }, { variant: 'right', label: '右侧' }];
  const schema = { capabilities: [{ id: 'image-to-multiview', image_result_group: { slots } }, { id: 'edit-multiview', image_result_group: { slots } }] } as unknown as ProductSchema;
  const asset = (variant: string, ordinal: number): TrackedAsset => ({ id: variant, role: 'image', variant, delivery: 'required', ordinal, mediaType: 'image/jpeg', size: 10, url: `/api/${variant}`, kind: 'image' });
  const tile = blankTile({ key: 'group', taskId: 'generated', capability: 'image-to-multiview', domain: 'image', status: 'ready', assets: [asset('right', 0), asset('back', 1), asset('front', 2), asset('left', 3)] });
  const target = targetOfTile(tile, 's1', '四视角', {}, '', schema);
  expect(target.assetVariant).toBe('front');
  expect(target.group?.map((item) => [item.assetVariant, item.title])).toEqual([
    ['front', '四视角 · 正面'], ['left', '四视角 · 左侧'], ['back', '四视角 · 背面'], ['right', '四视角 · 右侧'],
  ]);
  expect(target.group?.every((item) => item.taskId === 'generated')).toBe(true);
  const edit = blankTile({ key: 'edit', taskId: 'edited', capability: 'edit-multiview', domain: 'image', status: 'ready', assets: [asset('front', 0)] });
  expect(targetOfTile(edit, 's1', '编辑', {}, '', schema).group).toBeUndefined();
});
it('opens report-only Tasks as files with real authenticated URL, never as an image or stage object', () => {
  const target = targetOfTaskAssets({ taskId: 't1', sessionId: 's1', title: 'Print analysis', status: 'ready' }, [attachment]);
  expect(target.kind).toBe('other'); expect(target.url).toBe(attachment.url); expect(target.mediaType).toBe(attachment.mediaType); expect(objectOfTarget(target)).toBeNull();
});
it('retains a real model preview and leaves report attachments outside the media target', () => {
  const model = { ...attachment, id: 'model', role: 'model', mediaType: 'model/gltf-binary', url: '/api/model', kind: 'model' as const };
  expect(targetOfTaskAssets({ taskId: 't1', title: 'Repair' }, [attachment, model])).toMatchObject({ kind: 'model', url: '/api/model', assetId: 'model' });
});
it('identifies Gaussian Splat as a previewable representation that never becomes a stage object', () => {
  const splat: TrackedAsset = {
    id: 'splat', role: 'gaussian-splat', variant: 'splat', delivery: 'inline', deliveryProfile: 'gaussian-splat-32-v1',
    ordinal: 0, mediaType: 'application/vnd.art-ai.gaussian-splat', size: 32, url: '/api/splat', kind: 'splat',
  };
  const target = targetOfTaskAssets({ taskId: 't1', sessionId: 's1', title: 'Splat', status: 'ready' }, [splat]);
  expect(target).toMatchObject({ kind: 'splat', url: splat.url, assetId: 'splat', deliveryProfile: 'gaussian-splat-32-v1' });
  expect(objectOfTarget(target)).toBeNull();
  expect(targetOfTaskAssets({ taskId: 't1', title: 'Splat' }, [{ ...splat, kind: 'other', deliveryProfile: 'wrong' }]).kind).toBe('other');
});
it('does not give a report-only historical tile an image or model identity', () => {
  const tile = blankTile({ key: 'report-only', taskId: 't1', domain: 'model', status: 'ready', assets: [attachment] });
  const target = targetOfTile(tile, 's1', 'Report');
  expect(target.kind).toBe('other'); expect(objectOfTarget(target)).toBeNull();
});
it('opens an exact Splat version tile in detail and never on the stage', () => {
  const tile = blankTile({ key: 'splat', taskId: 't1', domain: 'model', status: 'ready', assets: [{
    id: 'splat', role: 'gaussian-splat', variant: 'splat', deliveryProfile: 'gaussian-splat-32-v1',
    mediaType: 'application/vnd.art-ai.gaussian-splat', ordinal: 0, url: '/api/splat', kind: 'splat',
  }] });
  const target = targetOfTile(tile, 's1', 'Splat');
  expect(target).toMatchObject({ kind: 'splat', taskId: 't1', url: '/api/splat', assetId: 'splat' });
  expect(objectOfTarget(target)).toBeNull();
});
