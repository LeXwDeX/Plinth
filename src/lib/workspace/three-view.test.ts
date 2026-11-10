import { describe, expect, it } from 'vitest';

import type { OfferingWire } from '@/lib/agent-offerings';

import {
  groupIsActive, groupMultiview, groupTileStatus, interleaveGroups, multiviewModelOf, parseThreeViewGroup, parseThreeViews, pickMultiviewModel,
  remainingSlots, slotAction, succeededCount, type ThreeViewGroup,
} from './three-view';

const wire = (statuses: Array<[string, Record<string, unknown>]>, extra: Record<string, unknown> = {}) => ({
  run_id: 'run-1', kind: 'three_view', label: '三视图', source_asset_id: 'a0', created_at: '2026-10-08T10:00:00Z', complete: false,
  slots: statuses.map(([status, rest], index) => ({ key: ['front', 'side', 'back'][index], label: ['正面', '侧面', '背面'][index], status, attempt: 1, startable: false, retryable: false, ...rest })),
  model_source: { slot: 'front', available: false, note: '只用正面图' },
  ...extra,
});

const parse = (statuses: Array<[string, Record<string, unknown>]>): ThreeViewGroup => parseThreeViews({ items: [wire(statuses)] })[0];

describe('parseThreeViews', () => {
  it('reads the projection and drops what is not a group', () => {
    const groups = parseThreeViews({ items: [wire([['succeeded', { task_id: 't1', output_asset_id: 'x1' }], ['not_started', { startable: true }], ['not_started', { startable: true }]]), { nope: true }, null] });
    expect(groups).toHaveLength(1);
    expect(groups[0].label).toBe('三视图');
    expect(groups[0].slots.map((slot) => slot.key)).toEqual(['front', 'side', 'back']);
    expect(groups[0].slots[0]).toMatchObject({ taskId: 't1', outputAssetId: 'x1', status: 'succeeded' });
    expect(groups[0].modelSource).toMatchObject({ slot: 'front', available: false, note: '只用正面图' });
    expect(parseThreeViews(null)).toEqual([]);
    expect(parseThreeViews({ items: 'x' })).toEqual([]);
  });

  it('keeps an unrecognised status as unknown instead of guessing', () => {
    const group = parse([['teleporting', {}], ['succeeded', {}], ['failed', {}]]);
    expect(group.slots[0].status).toBe('unknown');
    expect(slotAction(group.slots[0])).toBeNull();
  });
});

describe('estimated credits', () => {
  it('reads what starting or retrying a view costs, and 0 when the server does not say', () => {
    const group = parse([['succeeded', {}], ['not_started', { startable: true, estimated_credits: 6 }], ['not_started', { startable: true, estimated_credits: -3 }]]);
    expect(group.slots.map((slot) => slot.estimatedCredits)).toEqual([0, 6, 0]);
  });
});

describe('slotAction', () => {
  it('follows the projection: start when startable, retry when retryable, nothing otherwise', () => {
    const group = parse([
      ['succeeded', {}],
      ['not_started', { startable: true }],
      ['failed', { retryable: true }],
    ]);
    expect(slotAction(group.slots[0])).toBeNull();
    expect(slotAction(group.slots[1])).toBe('start');
    expect(slotAction(group.slots[2])).toBe('retry');
    const stuck = parse([['failed', { retryable: false }], ['running', {}], ['awaiting_approval', {}]]);
    expect(stuck.slots.map(slotAction)).toEqual([null, null, null]);
    const denied = parse([['denied', { retryable: true }], ['canceled', { retryable: true }], ['starting', {}]]);
    expect(denied.slots.map(slotAction)).toEqual(['retry', 'retry', null]);
    const rejected = parse([['rejected', { retryable: true }], ['rejected', { retryable: false }], ['succeeded', {}]]);
    expect(rejected.slots.map((slot) => slot.status)).toEqual(['rejected', 'rejected', 'succeeded']);
    expect(rejected.slots.map(slotAction)).toEqual(['retry', null, null]);
    expect(groupIsActive(rejected)).toBe(false);
  });
});

describe('group state', () => {
  it('is active while a view is starting, running or waiting for approval', () => {
    expect(groupIsActive(parse([['succeeded', {}], ['running', {}], ['not_started', {}]]))).toBe(true);
    expect(groupIsActive(parse([['succeeded', {}], ['awaiting_approval', {}], ['not_started', {}]]))).toBe(true);
    expect(groupIsActive(parse([['succeeded', {}], ['failed', {}], ['not_started', {}]]))).toBe(false);
  });

  it('gives the strip tile a status when no view has a picture yet', () => {
    expect(groupTileStatus(parse([['succeeded', {}], ['not_started', {}], ['not_started', {}]]))).toBe('ready');
    expect(groupTileStatus(parse([['running', {}], ['not_started', {}], ['not_started', {}]]))).toBe('running');
    expect(groupTileStatus(parse([['awaiting_approval', {}], ['not_started', {}], ['not_started', {}]]))).toBe('pending');
    expect(groupTileStatus(parse([['failed', {}], ['not_started', {}], ['not_started', {}]]))).toBe('failed');
    expect(succeededCount(parse([['succeeded', {}], ['succeeded', {}], ['failed', {}]]))).toBe(2);
  });
});

describe('interleaveGroups', () => {
  const messages = [
    { id: 'm1', createdAt: Date.parse('2026-10-08T09:59:00Z') },
    { id: 'm2', createdAt: Date.parse('2026-10-08T10:00:05Z') },
    { id: 'm3', createdAt: Date.parse('2026-10-08T10:05:00Z') },
  ];

  it('puts a group card before the first newer message', () => {
    const group = parse([['succeeded', {}], ['not_started', {}], ['not_started', {}]]);
    const items = interleaveGroups(messages, [group]);
    expect(items.map((item) => (item.kind === 'group' ? 'group' : item.message.id))).toEqual(['m1', 'group', 'm2', 'm3']);
  });

  it('closes the list with groups newer than every message, in time order', () => {
    const late = { ...parse([['running', {}], ['not_started', {}], ['not_started', {}]]), runId: 'late', createdAt: '2026-10-08T11:00:00Z' };
    const later = { ...late, runId: 'later', createdAt: '2026-10-08T12:00:00Z' };
    const items = interleaveGroups(messages, [later, late]);
    expect(items.slice(3).map((item) => (item.kind === 'group' ? item.group.runId : ''))).toEqual(['late', 'later']);
  });

  it('keeps a group with an unreadable time after the messages', () => {
    const odd = { ...parse([['running', {}], ['not_started', {}], ['not_started', {}]]), createdAt: 'nonsense' };
    expect(interleaveGroups(messages, [odd]).at(-1)?.kind).toBe('group');
  });
});

describe('native slot prices', () => {
 const quote={version:1,unit:'vendor.credit',unit_label:'Vendor credits',base_amount:'0.5',extra_amount:'0',total_amount:'0.5'};
 it('preserves the server quote and distinguishes estimates from frozen prices',()=>{
 const g=parse([['not_started',{startable:true,native_quote:quote,price_source:'default'}],['failed',{retryable:true,task_id:'t',native_quote:{...quote,base_amount:'1',total_amount:'1'},price_source:'frozen'}]]);
 expect(g.slots[0]).toMatchObject({nativeQuote:quote,priceSource:'default'});
 expect(g.slots[1]).toMatchObject({nativeQuote:{total_amount:'1'},priceSource:'frozen'});
 });
 it.each([null,{...quote,total_amount:'broken'},undefined])('keeps malformed or missing native prices unknown (%s)',q=>{
 const g=parse([['not_started',{startable:true,price_mode:'native',native_quote:q,estimated_credits:99}]]);
 expect(g.slots[0]).toMatchObject({nativeQuote:null});
 expect(g.slots[0].estimatedCredits).toBe(0);
 });
});

describe('multiview from a three-view group (#65)', () => {
  const offering = (parameters: unknown[], extra: Record<string, unknown> = {}) => ({
    id: 'o', revision: 'r', provider_id: 'p', provider_name: 'P', model_id: 'mv', model_name: 'MV', capability: 'image-to-3d', available: true,
    parameters, ...extra,
  }) as unknown as OfferingWire;
  const views = { name: 'reference_views', label: 'v', type: 'string-array', semantic: 'reference-views',
    options: [{ value: 'front', label: '正面' }, { value: 'left', label: '左侧' }, { value: 'back', label: '背面' }, { value: 'right', label: '右侧' }] };
  const pictures = { name: 'reference_assets', label: 'p', type: 'asset-array', semantic: 'reference-image', min_items: 2, max_items: 4, media_types: ['image/png'] };

  it('reads a multiview model only from the reference-views declaration and prefers the person\'s pick', () => {
    expect(multiviewModelOf(offering([pictures]))).toBeNull();
    const model = multiviewModelOf(offering([pictures, views]));
    expect(model).toMatchObject({ modelId: 'mv', minimumViews: 2, mediaTypes: ['image/png'] });
    expect(model?.views.map((view) => view.value)).toEqual(['front', 'left', 'back', 'right']);
    const other = offering([pictures, views], { model_id: 'other' });
    const unavailable = offering([pictures, views], { model_id: 'off', available: false });
    expect(pickMultiviewModel({ 'image-to-3d': [unavailable, offering([pictures, views]), other] }, 'other')?.modelId).toBe('other');
    expect(pickMultiviewModel({ 'image-to-3d': [unavailable, other] }, 'single')?.modelId).toBe('other');
    expect(pickMultiviewModel({ 'image-to-3d': [offering([pictures])] }, undefined)).toBeNull();
  });

  it('builds the structured request from the finished views the model takes', () => {
    const model = multiviewModelOf(offering([pictures, views]))!;
    const group = parseThreeViewGroup({
      run_id: 'run1', slots: [
        { key: 'front', label: '正面', status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
        { key: 'side', label: '侧面', status: 'succeeded', task_id: 't2', output_asset_id: 'x2' },
        { key: 'back', label: '背面', status: 'succeeded', task_id: 't3', output_asset_id: 'up3', replaced: true },
        { key: 'right', label: '右侧', status: 'not_started', optional: true, startable: true },
      ],
      model_source: { slot: 'front', available: true, task_id: 't1', asset_id: 'x1' },
    })!;
    const result = groupMultiview(group, model);
    expect(result.ready).toBe(true);
    expect(result.request).toEqual({ group: 'run1', model: 'mv', views: [
      { view: 'front', assetId: 'x1', taskId: 't1' }, { view: 'back', assetId: 'up3' },
    ] });
    // The optional right view is not part of "generate remaining views".
    expect(remainingSlots(group)).toEqual([]);
    const onlyFront = parseThreeViewGroup({ run_id: 'r', slots: [{ key: 'front', status: 'succeeded', task_id: 't', output_asset_id: 'x' }], model_source: { slot: 'front' } })!;
    expect(groupMultiview(onlyFront, model).ready).toBe(false);
  });
});
