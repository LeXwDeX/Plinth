// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ThreeViewCardView, type ThreeViewCardViewProps } from '@/components/studio/three-view-card';
import type { ProductSchema } from '@/lib/product-schema';
import { parseThreeViews, type MultiviewModel, type ThreeViewGroup } from '@/lib/workspace/three-view';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

afterEach(cleanup);

type SlotWire = Record<string, unknown> & { status: string };
const model: MultiviewModel = { modelId: 'mv', modelName: 'MV', views: ['front', 'left', 'back', 'right'].map((value) => ({ value, label: value })), minimumViews: 2, mediaTypes: [] };
const four = (slots: SlotWire[]): ThreeViewGroup => parseThreeViews({ items: [{
  run_id: 'run1', label: '三视图', source_asset_id: 'a0', created_at: '2026-11-27T10:00:00Z', complete: false,
  slots: slots.map((slot, index) => ({ key: ['front', 'left', 'back', 'right'][index], label: ['正面', '左侧', '背面', '右侧'][index],
    optional: index === 3, attempt: 0, startable: false, retryable: false, ...slot })),
  model_source: { slot: 'front', available: slots[0].status === 'succeeded', task_id: 't1', asset_id: 'x1', note: 'n' },
}] })[0];
const ready = four([
  { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
  { status: 'succeeded', task_id: 't2', output_asset_id: 'x2' },
  { status: 'succeeded', task_id: 't3', output_asset_id: 'x3' },
  { status: 'not_started', startable: true, estimated_credits: 9, credits_metered: true },
]);
const card = (props: Partial<ThreeViewCardViewProps> = {}) => render(
  <ThreeViewCardView schema={schema} group={ready} busy={null} queued={new Set()} thumbnail={() => ''} onAdvance={() => {}} onView={() => {}}
    onToThreeD={() => true} model={model} onMultiview={() => true} onReplace={() => {}} {...props} />,
);
const slotOf = (key: string) => document.querySelector(`[data-slot="${key}"]`) as HTMLElement;

describe('#691: the three-view card', () => {
  it('words the optional right view as an add-on a three-view does not need, with a quieter button', () => {
    card();
    const button = slotOf('right').querySelector('[data-testid="three-view-start"]') as HTMLButtonElement;
    expect(button.textContent).toBe('加做右侧（可选，约 9 积分）');
    expect(button.getAttribute('data-optional')).toBe('true');
    expect(button.getAttribute('aria-label')).toBe('右侧 · 加做右侧（可选，约 9 积分）');
    expect(slotOf('right').querySelector('[data-testid="three-view-optional-hint"]')?.textContent).toBe(messages['ui.studio.three_view.optional_hint']);
    // A required view keeps its own words.
    expect(messages['ui.studio.three_view.start_with_cost']).toContain('继续生成');
  });

  it('names the view in every button a screen reader reaches, and warns that replacing the front view leaves the others', () => {
    card();
    expect(slotOf('back').querySelector('[data-testid="three-view-open"]')?.getAttribute('aria-label')).toBe(`背面 · ${messages['ui.studio.three_view.view']}`);
    expect(slotOf('front').querySelector('[data-testid="three-view-replace"]')?.getAttribute('aria-description')).toBe(messages['ui.studio.three_view.replace_front_hint']);
    expect(slotOf('back').querySelector('[data-testid="three-view-replace"]')?.getAttribute('aria-description')).toBe(messages['ui.studio.three_view.replace_hint']);
    expect(screen.getByRole('list').getAttribute('aria-label')).toBe('视图');
  });

  it('keeps 用三视图生成 3D off, saying why, when the picked 3D model takes one picture; without a pick a press still asks', () => {
    const onMultiview = vi.fn(() => false);
    card({ multiviewState: 'single', onMultiview });
    const button = screen.getByTestId('three-view-multiview') as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(screen.getByTestId('three-view-multiview-views').textContent).toContain(messages['ui.studio.three_view.multiview_single_model']);
    cleanup();
    card({ multiviewState: 'none', onMultiview });
    fireEvent.click(screen.getByTestId('three-view-multiview'));
    expect(onMultiview).toHaveBeenCalledOnce();
    // Nothing was asked, so the button stays usable.
    expect((screen.getByTestId('three-view-multiview') as HTMLButtonElement).disabled).toBe(false);
  });

  it('asks for one turn per press: the two turn buttons stay off until the turn starts and ends', () => {
    const onMultiview = vi.fn(() => true);
    const onToThreeD = vi.fn(() => true);
    const view = card({ onMultiview, onToThreeD });
    fireEvent.click(screen.getByTestId('three-view-multiview'));
    fireEvent.click(screen.getByTestId('three-view-multiview'));
    fireEvent.click(screen.getByTestId('three-view-to-3d'));
    expect(onMultiview).toHaveBeenCalledOnce();
    expect(onToThreeD).not.toHaveBeenCalled();
    view.rerender(<ThreeViewCardView schema={schema} group={ready} busy={null} queued={new Set()} thumbnail={() => ''} onAdvance={() => {}} onView={() => {}}
      onToThreeD={onToThreeD} model={model} onMultiview={onMultiview} turnRunning />);
    expect((screen.getByTestId('three-view-to-3d') as HTMLButtonElement).disabled).toBe(true);
    view.rerender(<ThreeViewCardView schema={schema} group={ready} busy={null} queued={new Set()} thumbnail={() => ''} onAdvance={() => {}} onView={() => {}}
      onToThreeD={onToThreeD} model={model} onMultiview={onMultiview} turnRunning={false} />);
    fireEvent.click(screen.getByTestId('three-view-to-3d'));
    expect(onToThreeD).toHaveBeenCalledOnce();
  });
});
