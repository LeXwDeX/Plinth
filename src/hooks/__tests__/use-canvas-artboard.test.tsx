import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasDelivery } from '../use-canvas-artboard';
const runtime = vi.hoisted(() => ({
  context: null as { sessionId: string; hasReference: boolean; objectDeselected?: boolean; boardTurn?: boolean } | null,
  nodes: {} as Record<string, Array<Record<string, unknown>>>,
  object: null as { kind: string; sessionId: string; taskId: string; url?: string } | null,
  selected: vi.fn(),
  saved: {} as Record<string, { width: number; height: number }>,
}));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: catalog.application }) }));
vi.mock('@/store/studio-store', () => ({ useStudioStore: (select: any) => select({ generationReferenceContext: runtime.context }) }));
vi.mock('@/store/workspace-store', () => ({ useWorkspaceStore: (select: any) => select({ currentObject: runtime.object }) }));
vi.mock('@/hooks/use-generation-params', () => ({ useGenerationParams: (domain: string, sessionId: string, hasReference: boolean) => {
  runtime.selected(domain, sessionId, hasReference);
  const size = hasReference ? '1024x1536' : '1536x1024';
  const sizeParam = { name: 'size', default: size, options: [{ value: size, label: size }] };
  return {
    ready: true, failed: false,
    nextTurn: { offering: { id: 'o', parameters: [{ ...sizeParam, semantic: 'output-size' }] }, model: { paramSchemas: [], sizeParams: [sizeParam] }, values: { size }, setValue: vi.fn() },
  };
} }));
vi.mock('@/store/canvas-store', () => ({ useCanvasStore: (select: any) => select({ canvasSizeBySession: runtime.saved, nodesBySession: runtime.nodes }) }));
import { useCanvasArtboard } from '../use-canvas-artboard';
function board(sessionId = 's', explicit?: boolean) {
  let delivery!: CanvasDelivery;
  function Probe() { delivery = useCanvasArtboard(sessionId, explicit); return null; }
  renderToStaticMarkup(<Probe />); return delivery;
}
beforeEach(() => { runtime.context = null; runtime.object = null; runtime.saved = {}; runtime.nodes = {}; vi.clearAllMocks(); });
// #653: the canvas starts at the Schema default (1:1) whatever the model; the model decides only the output size of
// a canvas turn (`plan`), so the Offering the composer would run is read from `plan` here.
const planned = (delivery: CanvasDelivery) => delivery.plan?.value;
describe('the canvas output size follows the actual composer Offering context', () => {
  it('uses text-to-image sizes on the empty board and image-to-image sizes when attachments supply a reference', () => {
    expect(planned(board())).toBe('1536x1024');
    runtime.context = { sessionId: 's', hasReference: true };
    expect(planned(board())).toBe('1024x1536');
  });
  it('uses direct composer context immediately when a skipped picture changes applicability', () => {
    runtime.context = { sessionId: 's', hasReference: true };
    expect(planned(board('s', false))).toBe('1536x1024');
  });
  it('does not inherit another session reference context or stage object', () => {
    runtime.context = { sessionId: 'other', hasReference: true };
    runtime.object = { kind: 'image', sessionId: 'other', taskId: 't' };
    expect(planned(board())).toBe('1536x1024');
    runtime.object = { kind: 'image', sessionId: 's', taskId: 't' };
    expect(planned(board())).toBe('1024x1536');
  });
  it('a new canvas is 1:1 whatever the model; a saved canvas keeps its pixels and reads its ratio from them (#653)', () => {
    expect(board()).toMatchObject({ known: false, pixels: { width: 2048, height: 2048 }, initial: { width: 2048, height: 2048 }, ratio: { label: '1:1' } });
    runtime.saved = { s: { width: 3072, height: 2048 } };
    const delivery = board();
    expect(delivery).toMatchObject({ known: true, pixels: { width: 3072, height: 2048 }, initial: { width: 2048, height: 2048 }, ratio: { label: '3:2' } });
    // The frame is the canvas pixels in world units, around the same centre.
    expect(delivery.artboard).toEqual({ x: 600 - 1536, y: 400 - 1024, width: 3072, height: 2048 });
    // The model has a 3:2 size: the canvas turn takes it.
    expect(delivery.plan).toMatchObject({ value: '1536x1024', nearest: false, ratio: '3:2' });
  });

  it('says what a turn sends when the artboard is its source, not the remembered size (#284)', () => {
    runtime.saved = { s: { width: 2048, height: 2048 } };
    // The composer's own context keeps the text-to-image sizes while a picture is the current object.
    runtime.context = { sessionId: 's', hasReference: false };
    runtime.object = { kind: 'image', sessionId: 's', taskId: 't', url: '/api/v1/tasks/t/assets/a' };
    // #644: the current picture inside the frame does not make the board the source while 以画板为主 is off.
    runtime.nodes = { s: [{ id: 'p', type: 'image', imageUrl: '/api/v1/assets/a', taskId: 't', x: 0, y: 0, width: 512, height: 512, zIndex: 1 }] };
    expect(board().source).toBe(false);
    expect(board().turn).toBeNull();
    // The plan is known anyway: a model without the canvas ratio is said before a canvas turn.
    expect(board().plan).toMatchObject({ value: '1536x1024', nearest: true, ratio: '1:1', outputRatio: '3:2' });
    // A canvas turn (the toggle, a selection or mask, @画布): the artboard is the source and the canvas sets the size.
    runtime.context = { sessionId: 's', hasReference: false, boardTurn: true };
    const delivery = board();
    expect(delivery.source).toBe(true);
    expect(delivery.turn).toMatchObject({ value: '1536x1024', nearest: true });
    // Another session's canvas turn is not this one's; without a composer context nothing is sent from the board.
    runtime.context = { sessionId: 'other', hasReference: false, boardTurn: true };
    expect(board().turn).toBeNull();
    runtime.context = null;
    expect(board().turn).toBeNull();
  });
});
