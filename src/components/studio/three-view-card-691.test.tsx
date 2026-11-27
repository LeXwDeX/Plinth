// @vitest-environment jsdom

import React from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act, cleanup, fireEvent, render, renderHook, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ThreeViewCardView, useTurnButtonsPending, type ThreeViewCardViewProps } from '@/components/studio/three-view-card';
import { useModel3DConfirmStore } from '@/store/model3d-confirm-store';
import { useStudioStore } from '@/store/studio-store';
import type { ProductSchema } from '@/lib/product-schema';
import { parseThreeViews, threeViewAutoContinuing, type MultiviewModel, type ThreeViewGroup } from '@/lib/workspace/three-view';

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
  it('shows each view whole in its square thumbnail instead of cropping it', () => {
    card({ thumbnail: (slot) => `/thumb/${slot.key}` });
    const picture = slotOf('front').querySelector('[data-testid="three-view-thumb"] img') as HTMLImageElement;
    expect(picture.className).toContain('object-contain');
    expect(picture.className).not.toContain('object-cover');
  });

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

  it('keeps the two turn buttons off while a turn is asked for or runs, and usable again once neither holds', () => {
    const onMultiview = vi.fn(() => true);
    const onToThreeD = vi.fn(() => true);
    const view = card({ onMultiview, onToThreeD, turnPending: true });
    fireEvent.click(screen.getByTestId('three-view-multiview'));
    fireEvent.click(screen.getByTestId('three-view-to-3d'));
    expect(onMultiview).not.toHaveBeenCalled();
    expect(onToThreeD).not.toHaveBeenCalled();
    view.rerender(<ThreeViewCardView schema={schema} group={ready} busy={null} queued={new Set()} thumbnail={() => ''} onAdvance={() => {}} onView={() => {}}
      onToThreeD={onToThreeD} model={model} onMultiview={onMultiview} turnRunning />);
    expect((screen.getByTestId('three-view-to-3d') as HTMLButtonElement).disabled).toBe(true);
    view.rerender(<ThreeViewCardView schema={schema} group={ready} busy={null} queued={new Set()} thumbnail={() => ''} onAdvance={() => {}} onView={() => {}}
      onToThreeD={onToThreeD} model={model} onMultiview={onMultiview} turnRunning={false} />);
    fireEvent.click(screen.getByTestId('three-view-to-3d'));
    expect(onToThreeD).toHaveBeenCalledOnce();
  });

  it('never stays off on its own: a press whose turn never starts leaves the buttons usable', () => {
    // The request was dropped or refused before a turn started (no session, no Agent, a model no longer offered):
    // nothing is pending and no turn runs, so the card does not keep the buttons off by itself.
    const onToThreeD = vi.fn(() => true);
    card({ onToThreeD });
    fireEvent.click(screen.getByTestId('three-view-to-3d'));
    fireEvent.click(screen.getByTestId('three-view-to-3d'));
    expect(onToThreeD).toHaveBeenCalledTimes(2);
    expect((screen.getByTestId('three-view-to-3d') as HTMLButtonElement).disabled).toBe(false);
  });
});

describe('#691: what keeps the turn buttons off', () => {
  afterEach(() => {
    act(() => {
      useStudioStore.setState({ turnRequest: null, stageTurnsSending: 0 });
      useModel3DConfirmStore.getState().close();
    });
  });

  it('a stage request not yet sent, a send in progress, and an open 3D model confirmation', () => {
    const { result } = renderHook(() => useTurnButtonsPending());
    expect(result.current).toBe(false);
    act(() => { useStudioStore.getState().requestTurn('转 3D'); });
    expect(result.current).toBe(true);
    // The request is dropped (no session): usable again.
    act(() => { useStudioStore.getState().clearTurnRequest(); });
    expect(result.current).toBe(false);
    // The send runs, then returns without a turn (no Agent, model no longer offered): usable again.
    act(() => { useStudioStore.getState().beginStageTurn(); });
    expect(result.current).toBe(true);
    act(() => { useStudioStore.getState().endStageTurn(); });
    expect(result.current).toBe(false);
    // Confirming the model: a second press would ask again, so the buttons wait for the answer.
    act(() => { useModel3DConfirmStore.getState().ask({ modelId: 'm', modelName: 'M', cost: '', run: () => {} }); });
    expect(result.current).toBe(true);
    act(() => { useModel3DConfirmStore.getState().close(); });
    expect(result.current).toBe(false);
  });
});

describe('#693: while the automatic continuation carries the group on', () => {
  const running = four([
    { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
    { status: 'running', task_id: 't2' },
    { status: 'not_started', startable: true, estimated_credits: 9, credits_metered: true },
    { status: 'not_started', startable: true, estimated_credits: 9, credits_metered: true },
  ]);
  const props = { group: running, onStartRemaining: () => {} };

  it('says the required views come later and offers no manual start for them; the optional right view keeps its own', () => {
    card({ ...props, autoContinuing: true });
    expect(slotOf('back').querySelector('[data-testid="three-view-start"]')).toBeNull();
    expect(slotOf('back').querySelector('[data-testid="three-view-auto-later"]')?.textContent).toBe(messages['ui.studio.three_view.auto_later']);
    expect(screen.queryByTestId('three-view-start-remaining')).toBeNull();
    expect(slotOf('right').querySelector('[data-testid="three-view-start"]')).not.toBeNull();
  });

  it('keeps every manual start where the session asks for each one', () => {
    card({ ...props, autoContinuing: false });
    expect(slotOf('back').querySelector('[data-testid="three-view-start"]')).not.toBeNull();
    expect(screen.queryByTestId('three-view-auto-later')).toBeNull();
    expect(screen.getByTestId('three-view-start-remaining')).toBeTruthy();
  });
});

describe('#693: the card says 稍后自动生成 only while the session\'s turn runs', () => {
  it('reads the approval mode and the turn, not whether one of its views runs', () => {
    const source = readFileSync(resolve(process.cwd(), 'src/components/studio/three-view-card.tsx'), 'utf8');
    expect(source).toMatch(/const autoContinuing = threeViewAutoContinuing\(approvalMode, turnRunning\);/);
    expect(source).not.toMatch(/autoContinuing[\s\S]{0,200}slot\.status === 'running'/);
  });

  it('an automatic-approval session with a view running and no turn offers the manual starts', () => {
    const running = four([
      { status: 'succeeded', task_id: 't1', output_asset_id: 'x1' },
      { status: 'running', task_id: 't2' },
      { status: 'not_started', startable: true, estimated_credits: 9, credits_metered: true },
      { status: 'not_started', startable: true, estimated_credits: 9, credits_metered: true },
    ]);
    card({ group: running, onStartRemaining: () => {}, autoContinuing: threeViewAutoContinuing('auto', false) });
    expect(slotOf('back').querySelector('[data-testid="three-view-start"]')).not.toBeNull();
    expect(screen.queryByTestId('three-view-auto-later')).toBeNull();
    expect(screen.getByTestId('three-view-start-remaining')).toBeTruthy();
  });
});
