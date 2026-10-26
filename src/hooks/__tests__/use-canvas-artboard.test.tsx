import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasDelivery } from '../use-canvas-artboard';
const runtime = vi.hoisted(() => ({
  context: null as { sessionId: string; hasReference: boolean; objectSkipped?: boolean } | null,
  nodes: {} as Record<string, Array<Record<string, unknown>>>,
  object: null as { kind: string; sessionId: string; taskId: string } | null,
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
describe('canvas geometry follows the actual composer Offering context', () => {
  it('uses text-to-image sizes on the empty board and image-to-image sizes when attachments supply a reference', () => {
    expect(board().pixels).toEqual({ width: 1536, height: 1024 });
    runtime.context = { sessionId: 's', hasReference: true };
    expect(board().pixels).toEqual({ width: 1024, height: 1536 });
  });
  it('uses direct composer context immediately when a skipped picture changes applicability', () => {
    runtime.context = { sessionId: 's', hasReference: true };
    expect(board('s', false).pixels).toEqual({ width: 1536, height: 1024 });
  });
  it('does not inherit another session reference context or stage object', () => {
    runtime.context = { sessionId: 'other', hasReference: true };
    runtime.object = { kind: 'image', sessionId: 'other', taskId: 't' };
    expect(board().pixels).toEqual({ width: 1536, height: 1024 });
    runtime.object = { kind: 'image', sessionId: 's', taskId: 't' };
    expect(board().pixels).toEqual({ width: 1024, height: 1536 });
  });
  it('a saved canvas size is the frame; the model output only decides the initial size and the fit (#246)', () => {
    expect(board()).toMatchObject({ known: false, pixels: { width: 1536, height: 1024 }, initial: { width: 1536, height: 1024 }, fit: { kind: 'match' } });
    runtime.saved = { s: { width: 3072, height: 2048 } };
    const delivery = board();
    expect(delivery).toMatchObject({ known: true, pixels: { width: 3072, height: 2048 }, initial: { width: 1536, height: 1024 } });
    // Same ratio, twice the pixels: the frame is twice as large in world units, around the same centre.
    expect(delivery.artboard).toEqual({ x: 600 - 1536, y: 400 - 1024, width: 3072, height: 2048 });
    expect(delivery.fit).toEqual({ kind: 'conflict', output: { width: 1536, height: 1024 } });
  });

  it('says what a turn sends when the artboard is its source, not the remembered size (#284)', () => {
    runtime.saved = { s: { width: 2048, height: 2048 } };
    // Nothing in the frame: the turn does not carry the artboard.
    expect(board().turn).toBeNull();
    // A picture inside the frame (centre 600,400): the artboard is the source and the canvas sets the size.
    runtime.nodes = { s: [{ id: 'p', type: 'image', imageUrl: '/api/v1/assets/a', x: 0, y: 0, width: 512, height: 512, zIndex: 1 }] };
    const delivery = board();
    expect(delivery.fit.kind).toBe('conflict');
    expect(delivery.turn).toMatchObject({ value: '1536x1024', nearest: true });
    // A dismissed chip, or a 3D object on the stage, does not send the artboard.
    runtime.context = { sessionId: 's', hasReference: false, objectSkipped: true };
    expect(board().turn).toBeNull();
    runtime.context = null;
    runtime.object = { kind: 'model', sessionId: 's', taskId: 't' };
    expect(board().turn).toBeNull();
    // Content outside the frame only is not sent either.
    runtime.object = null;
    runtime.nodes = { s: [{ id: 'p', type: 'image', imageUrl: '/api/v1/assets/a', x: 5000, y: 0, width: 512, height: 512, zIndex: 1 }] };
    expect(board().turn).toBeNull();
  });
});
