import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ThreeViewCardView } from '@/components/studio/three-view-card';
import type { ProductSchema } from '@/lib/product-schema';
import { parseThreeViews, type ThreeViewGroup } from '@/lib/workspace/three-view';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

type SlotWire = { native_quote?: unknown; price_mode?: string; price_source?: string; credits_metered?: boolean; status: string; attempt?: number; startable?: boolean; retryable?: boolean; task_id?: string; output_asset_id?: string };

const group = (slots: SlotWire[], extra: Record<string, unknown> = {}): ThreeViewGroup => parseThreeViews({ items: [{
  run_id: 'run1', label: '三视图', source_asset_id: 'a0', created_at: '2026-10-08T10:00:00Z', complete: slots.every((slot) => slot.status === 'succeeded'),
  slots: slots.map((slot, index) => ({ key: ['front', 'side', 'back'][index], label: ['正面', '侧面', '背面'][index], attempt: 1, startable: false, retryable: false, ...slot })),
  model_source: { slot: 'front', available: slots[0].status === 'succeeded', task_id: 't1', asset_id: 'x1', note: '用三视图做 3D 模型时只使用正面图。' },
  ...extra,
}] })[0];

const render = (value: ThreeViewGroup, busy: string | null = null, queued: ReadonlySet<string> = new Set()) => renderToStaticMarkup(
  <ThreeViewCardView schema={schema} group={value} busy={busy} queued={queued} thumbnail={(slot) => (slot.outputAssetId ? `/api/v1/tasks/${slot.taskId}/assets/${slot.outputAssetId}` : '')} onAdvance={() => {}} onView={() => {}} onToThreeD={() => {}} />,
);

const slotHtml = (html: string, key: string) => {
  const start = html.indexOf(`data-slot="${key}"`);
  return html.slice(start, html.indexOf('</li>', start));
};

/** The opening tag of the button with this test id (a disabled attribute, not the disabled: utility classes). */
const buttonTag = (html: string, id: string) => html.match(new RegExp(`<button[^>]*data-testid="${id}"[^>]*>`))?.[0] ?? '';
const isDisabled = (tag: string) => / disabled(=""| )/.test(tag);

describe('three-view card', () => {
  it('shows three slots, each with its own status', () => {
    const html = render(group([
      { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
      { status: 'not_started', startable: true },
      { status: 'not_started', startable: true },
    ]));
    expect(html.match(/data-testid="three-view-slot"/g)).toHaveLength(3);
    expect(html).toContain('三视图');
    expect(html).toContain(messages['ui.studio.three_view.status.succeeded']);
    expect(html).toContain(messages['ui.studio.three_view.status.not_started']);
    expect(html).toContain('已完成 1 / 3');
    expect(html).toContain('/api/v1/tasks/t1/assets/x1');
  });

  it('offers "continue" only on views the server says can start, and "view" on the finished one', () => {
    const html = render(group([
      { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
      { status: 'not_started', startable: true },
      { status: 'running', task_id: 't3' },
    ]));
    expect(slotHtml(html, 'front')).toContain('data-testid="three-view-open"');
    expect(slotHtml(html, 'front')).not.toContain('three-view-start');
    expect(slotHtml(html, 'side')).toContain('data-testid="three-view-start"');
    expect(slotHtml(html, 'side')).toContain('继续生成');
    expect(slotHtml(html, 'back')).not.toContain('three-view-start');
    expect(slotHtml(html, 'back')).not.toContain('three-view-retry');
    expect(slotHtml(html, 'back')).toContain(messages['ui.studio.three_view.status.running']);
  });

  it('offers retry on a failed view that may be retried and shows the attempt', () => {
    const html = render(group([
      { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
      { status: 'failed', attempt: 2, retryable: true, task_id: 't2' },
      { status: 'failed', attempt: 1, retryable: false, task_id: 't3' },
    ]));
    expect(slotHtml(html, 'side')).toContain('data-testid="three-view-retry"');
    expect(slotHtml(html, 'side')).toContain('重试');
    expect(slotHtml(html, 'side')).toContain('第 2 次');
    expect(slotHtml(html, 'back')).not.toContain('three-view-retry');
    expect(slotHtml(html, 'back')).toContain('data-testid="three-view-no-retry"');
    expect(slotHtml(html, 'front')).not.toContain('three-view-attempt');
  });

  it('retries a denied or canceled view and says a view is waiting for approval', () => {
    const html = render(group([
      { status: 'denied', retryable: true },
      { status: 'canceled', retryable: true },
      { status: 'awaiting_approval' },
    ]));
    expect(slotHtml(html, 'front')).toContain('three-view-retry');
    expect(slotHtml(html, 'side')).toContain('three-view-retry');
    expect(slotHtml(html, 'back')).toContain(messages['ui.studio.three_view.waiting_approval']);
    expect(slotHtml(html, 'back')).not.toContain('<button');
  });

  it('disables every button while one request is in flight', () => {
    const html = render(group([
      { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
      { status: 'not_started', startable: true },
      { status: 'not_started', startable: true },
    ]), 'run1:side');
    expect(isDisabled(buttonTag(slotHtml(html, 'side'), 'three-view-start'))).toBe(true);
    expect(isDisabled(buttonTag(slotHtml(html, 'back'), 'three-view-start'))).toBe(true);
  });

  it('says only the front view is used for 3D and enables 3D once the front view is done', () => {
    const waiting = render(group([{ status: 'running', task_id: 't1' }, { status: 'not_started' }, { status: 'not_started' }]));
    expect(waiting).toContain('用三视图做 3D 模型时只使用正面图。');
    expect(waiting).toContain(messages['ui.studio.three_view.to_3d_wait']);
    // #94: icon plus "无法使用，<短原因>"; the full sentence is the hover detail only.
    expect(waiting).toContain('data-testid="unavailable-icon"');
    expect(waiting).toContain(`data-testid="unavailable-label">无法使用，${messages['ui.unavailable.reason.three_view.front_wait']}<`);
    expect(waiting).toContain(`data-testid="unavailable-detail">${messages['ui.studio.three_view.to_3d_wait']}<`);
    expect(isDisabled(buttonTag(waiting, 'three-view-to-3d'))).toBe(true);
    const ready = render(group([{ status: 'succeeded', task_id: 't1', output_asset_id: 'x1' }, { status: 'not_started' }, { status: 'not_started' }]));
    expect(ready).not.toContain(messages['ui.studio.three_view.to_3d_wait']);
    expect(ready).not.toContain('three-view-to-3d-wait');
    expect(isDisabled(buttonTag(ready, 'three-view-to-3d'))).toBe(false);
  });

  it('marks a finished group complete', () => {
    const html = render(group([
      { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
      { status: 'succeeded', task_id: 't2', output_asset_id: 'x2' },
      { status: 'succeeded', task_id: 't3', output_asset_id: 'x3' },
    ]));
    expect(html).toContain('data-complete="true"');
    expect(html).toContain(messages['ui.studio.three_view.complete']);
  });
});

it('shows native start/retry prices and keeps unknown native prices disabled',()=>{
 const quote={version:1,unit:'vendor.credit',unit_label:'Vendor credits',base_amount:'0.5',extra_amount:'0',total_amount:'0.5'};
 const html=render(group([{status:'not_started',startable:true,credits_metered:true,native_quote:quote,price_source:'default'},{status:'failed',retryable:true,task_id:'t',credits_metered:true,native_quote:{...quote,base_amount:'1',total_amount:'1'},price_source:'frozen'},{status:'not_started',startable:true,credits_metered:true,price_mode:'native',native_quote:null}]));
 expect(slotHtml(html,'front')).toContain('默认估算：0.5 Vendor credits');
 expect(slotHtml(html,'side')).toContain('已冻结估算：1 Vendor credits');
 expect(slotHtml(html,'back')).toContain('报价未知');
 expect(isDisabled(buttonTag(slotHtml(html,'back'),'three-view-start'))).toBe(true);
});

describe('three-view card queue', () => {
  it('shows the queued line on a queued slot and keeps its button from being clicked again', () => {
    const html = render(group([
      { status: 'not_started', startable: true },
      { status: 'not_started', startable: true },
      { status: 'not_started', startable: true },
    ]), null, new Set(['run1:front']));
    expect(slotHtml(html, 'front')).toContain(messages['ui.studio.three_view.queued']);
    expect(isDisabled(buttonTag(slotHtml(html, 'front'), 'three-view-start'))).toBe(true);
    expect(slotHtml(html, 'side')).not.toContain(messages['ui.studio.three_view.queued']);
    expect(isDisabled(buttonTag(slotHtml(html, 'side'), 'three-view-start'))).toBe(false);
  });
});

// #65: the views follow the multiview model's view keys; the card makes one model from the finished views.
describe('three-view card with a multiview model', () => {
  const model = { modelId: 'mv-model', modelName: 'MV', views: [
    { value: 'front', label: '正面' }, { value: 'left', label: '左侧' }, { value: 'back', label: '背面' }, { value: 'right', label: '右侧' },
  ], minimumViews: 2, mediaTypes: ['image/png', 'image/jpeg'] };
  const four = (slots: (SlotWire & { replaced?: boolean })[]): ThreeViewGroup => parseThreeViews({ items: [{
    run_id: 'run1', label: '三视图', source_asset_id: 'a0', created_at: '2026-11-03T10:00:00Z', complete: false,
    slots: slots.map((slot, index) => ({ key: ['front', 'left', 'back', 'right'][index], label: ['正面', '左侧', '背面', '右侧'][index],
      optional: index === 3, attempt: 0, startable: false, retryable: false, ...slot })),
    model_source: { slot: 'front', available: slots[0].status === 'succeeded', task_id: 't1', asset_id: 'x1', note: 'n' },
  }] })[0];
  const renderFour = (value: ThreeViewGroup, busy: string | null = null) => renderToStaticMarkup(
    <ThreeViewCardView schema={schema} group={value} busy={busy} queued={new Set()} thumbnail={() => ''} onAdvance={() => {}} onView={() => {}}
      onToThreeD={() => {}} model={model} onMultiview={() => {}} onStartRemaining={() => {}} onReplace={() => {}} />,
  );

  it('enables 用三视图生成 3D only with the front view and one more view, and names the views it uses', () => {
    const onlyFront = renderFour(four([
      { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
      { status: 'not_started', startable: true, estimated_credits: 4, credits_metered: true } as SlotWire,
      { status: 'not_started', startable: true, estimated_credits: 4, credits_metered: true } as SlotWire,
      { status: 'not_started', startable: true },
    ]));
    expect(isDisabled(buttonTag(onlyFront, 'three-view-multiview'))).toBe(true);
    expect(onlyFront).toContain(`data-testid="unavailable-detail">${messages['ui.studio.three_view.multiview_wait']}<`);
    expect(onlyFront).toContain(`无法使用，${messages['ui.unavailable.reason.three_view.multiview_wait']}`);
    // The front view alone still converts.
    expect(isDisabled(buttonTag(onlyFront, 'three-view-to-3d'))).toBe(false);
    expect(onlyFront).toContain(messages['ui.studio.three_view.to_3d_front_only']);
    // The remaining required views (not the optional right one) start together, priced as the sum.
    expect(onlyFront).toContain('生成剩余视图（约 8 积分）');
    expect(onlyFront).toContain(messages['ui.studio.three_view.optional']);

    const ready = renderFour(four([
      { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
      { status: 'running', task_id: 't2' },
      { status: 'succeeded', output_asset_id: 'up3', replaced: true } as SlotWire,
      { status: 'not_started', startable: true },
    ]));
    expect(isDisabled(buttonTag(ready, 'three-view-multiview'))).toBe(false);
    expect(ready).toContain('用三视图生成 3D（MV）');
    expect(ready).toContain('将使用：正面 / 背面');
    expect(ready).not.toContain('data-testid="three-view-start-remaining"');
    expect(slotHtml(ready, 'back')).toContain(messages['ui.studio.three_view.replaced']);

    const noFront = renderFour(four([
      { status: 'failed', task_id: 't1', retryable: true },
      { status: 'succeeded', task_id: 't2', output_asset_id: 'x2' },
      { status: 'succeeded', task_id: 't3', output_asset_id: 'x3' },
      { status: 'not_started', startable: true },
    ]));
    expect(isDisabled(buttonTag(noFront, 'three-view-multiview'))).toBe(true);
  });

  it('offers 替换 on every view except one in flight', () => {
    const html = renderFour(four([
      { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
      { status: 'running', task_id: 't2' },
      { status: 'awaiting_approval', task_id: '' },
      { status: 'not_started', startable: true },
    ]));
    expect(slotHtml(html, 'front')).toContain('data-testid="three-view-replace"');
    expect(slotHtml(html, 'right')).toContain('data-testid="three-view-replace"');
    expect(slotHtml(html, 'left')).not.toContain('three-view-replace"');
    expect(slotHtml(html, 'back')).not.toContain('three-view-replace"');
    expect(html).toContain('accept="image/png,image/jpeg"');
  });

  it('hides the multiview button without a multiview model', () => {
    const html = renderToStaticMarkup(
      <ThreeViewCardView schema={schema} group={four([{ status: 'succeeded', task_id: 't1', output_asset_id: 'x1' }, { status: 'succeeded', task_id: 't2', output_asset_id: 'x2' }, { status: 'not_started' }, { status: 'not_started' }])}
        busy={null} thumbnail={() => ''} onAdvance={() => {}} onView={() => {}} onToThreeD={() => {}} model={null} onMultiview={() => {}} />,
    );
    expect(html).not.toContain('three-view-multiview"');
    expect(html).toContain('data-testid="three-view-to-3d"');
  });
});
