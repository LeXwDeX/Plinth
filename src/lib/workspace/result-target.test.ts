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
import { blankTile, type VersionTile, type VersionView } from './versions';
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

it('opens an upload or an Agent crop as a file with no Task that still reaches the stage by its object id (#587)', () => {
  const crop = blankTile({ key: 'u:crop-1', taskId: 'upload:crop-1', taskIds: ['upload:crop-1'], domain: 'image', status: 'ready', origin: 'upload', operation: 'crop',
    assets: [{ id: 'crop-1', role: 'image', variant: '', ordinal: 0, mediaType: 'image/png', url: '/api/v1/assets/crop-1', kind: 'image' }] });
  const target = targetOfTile(crop, 's1', '裁图');
  expect(target.taskId).toBeUndefined();
  expect(target).toMatchObject({ stageObjectId: 'upload:crop-1', assetId: 'crop-1', url: '/api/v1/assets/crop-1', kind: 'image' });
  expect(objectOfTarget(target)).toMatchObject({ taskId: 'upload:crop-1', assetId: 'crop-1', kind: 'image', sessionId: 's1' });
  // A Task result keeps its Task and carries no object id of its own.
  const task = targetOfTile({ ...crop, key: 'n:t', taskId: 't', taskIds: ['t'], origin: 'task' }, 's1', '2D v1');
  expect(task.taskId).toBe('t'); expect(task.stageObjectId).toBeUndefined();
  // An object id never makes a model or a file a stage object.
  expect(objectOfTarget({ ...target, kind: 'other' })).toBeNull();
});

describe('three-view batch detail paging (#690)', () => {
  const picture = (id: string, ordinal = 0): TrackedAsset => ({ id, role: 'image', variant: '', delivery: 'required', ordinal, mediaType: 'image/png', size: 10, url: `/api/${id}`, kind: 'image' });
  const view = (key: string, label: string, taskId: string, assetId: string, status: VersionView['status'] = 'succeeded'): VersionView => ({ key, label, optional: key === 'right', status, taskId, assetId });
  // Slots arrive in finish order; views carry the group's own order.
  const batch = (overrides: Partial<VersionTile> = {}) => blankTile({
    key: 'g:run', taskId: 't-front', taskIds: ['t-right', 't-front', 't-back', 't-left'], domain: 'image', status: 'ready', groupLabel: '三视图',
    assets: [picture('a-right'), picture('a-front'), picture('a-back'), picture('a-left')],
    slots: [
      { slot: 'right', label: '右侧', taskId: 't-right', assetId: 'a-right' },
      { slot: 'front', label: '正面', taskId: 't-front', assetId: 'a-front' },
      { slot: 'back', label: '背面', taskId: 't-back', assetId: 'a-back' },
      { slot: 'left', label: '左侧', taskId: 't-left', assetId: 'a-left' },
    ],
    views: [
      view('front', '正面', 't-front', 'a-front'), view('left', '左侧', 't-left', 'a-left'),
      view('back', '背面', 't-back', 'a-back'), view('right', '右侧', 't-right', 'a-right'),
    ],
    ...overrides,
  });

  it('pages through the finished views in view order with their names in the title', () => {
    const target = targetOfTile(batch(), 's1', '2D v2 · 三视图');
    expect(target.group?.map((item) => [item.taskId, item.assetId, item.url, item.title])).toEqual([
      ['t-front', 'a-front', '/api/a-front', '2D v2 · 三视图 · 正面'],
      ['t-left', 'a-left', '/api/a-left', '2D v2 · 三视图 · 左侧'],
      ['t-back', 'a-back', '/api/a-back', '2D v2 · 三视图 · 背面'],
      ['t-right', 'a-right', '/api/a-right', '2D v2 · 三视图 · 右侧'],
    ]);
    // It opens on the front when nothing is being looked at.
    expect(target).toMatchObject({ taskId: 't-front', assetId: 'a-front', title: '2D v2 · 三视图 · 正面' });
    expect(groupPosition(target)).toEqual({ index: 0, count: 4 });
  });

  it('opens on the view being looked at', () => {
    const target = targetOfTile(batch(), 's1', '三视图', { taskId: 't-right', assetId: 'a-right' });
    expect(target).toMatchObject({ taskId: 't-right', assetId: 'a-right', url: '/api/a-right', title: '三视图 · 右侧' });
    expect(groupPosition(target)).toEqual({ index: 3, count: 4 });
    expect(stepResultTarget(target, -1)?.taskId).toBe('t-back');
  });

  it('leaves out unfinished views and views replaced by an upload', () => {
    const tile = batch({
      slots: [
        { slot: 'front', label: '', taskId: 't-front', assetId: 'a-front' },
        { slot: 'back', label: '', taskId: 't-back', assetId: 'a-back' },
      ],
      assets: [picture('a-front'), picture('a-back')],
      views: [
        view('front', '', 't-front', 'a-front'), view('left', '左侧', '', 'upload-left'),
        view('back', '背面', 't-back', 'a-back'), view('right', '右侧', 't-right', '', 'running'),
      ],
    });
    const target = targetOfTile(tile, 's1', '三视图');
    // A view without its own label falls back to the slot's name.
    expect(target.group?.map((item) => [item.taskId, item.title])).toEqual([['t-front', '三视图 · front'], ['t-back', '三视图 · 背面']]);
  });

  it('gives a batch left with a single view of its own no group', () => {
    // The other views were replaced by uploads: their slots remain, but the views no longer name a Task.
    const tile = batch({ views: [
      view('front', '正面', 't-front', 'a-front'), view('left', '左侧', '', 'upload-left'),
      view('back', '背面', '', 'upload-back'), view('right', '右侧', '', 'upload-right'),
    ] });
    const target = targetOfTile(tile, 's1', '三视图');
    expect(target.group).toBeUndefined();
    expect(target).toMatchObject({ taskId: 't-front', assetId: 'a-front', title: '三视图' });
  });

  it('keeps an ordinary candidate group and a single picture as they were', () => {
    const candidates = blankTile({ key: 'n:t1', taskId: 't1', taskIds: ['t1'], domain: 'image', status: 'ready', assets: [picture('c1', 0), picture('c2', 1)] });
    const target = targetOfTile(candidates, 's1', '2D v1', { taskId: 't1', assetId: 'c2' });
    expect(target.group?.map((item) => [item.assetId, item.title])).toEqual([['c1', '2D v1'], ['c2', '2D v1']]);
    expect(target.assetId).toBe('c2');
    const single = blankTile({ key: 'n:t2', taskId: 't2', taskIds: ['t2'], domain: 'image', status: 'ready', assets: [picture('only')] });
    expect(targetOfTile(single, 's1', '2D v2').group).toBeUndefined();
  });
});
