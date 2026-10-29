import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');
const stage = read('./stage-3d.tsx');
const viewer = read('../viewer/model-viewer-3d.tsx');
const catalog = readFileSync(new URL('../../../internal/catalog/default_catalog.json', import.meta.url), 'utf8');

describe('3D stage: capture is refused while the model is not ready (#126)', () => {
  it('decides each request with the model state, drops it on failure and on unmount', () => {
    expect(stage).toContain('captureStepOf(captureRequest, object.taskId, modelState, capturing)');
    expect(stage).toContain("if (step === 'drop') { useStudioStore.getState().clearCaptureRequest(); return; }");
    expect(stage).toContain('if (store.captureRequest?.taskId === modelTask) store.clearCaptureRequest();');
    // The control of a model just switched away from is not taken for the new one.
    expect(stage).toContain('held?.key === viewerKey ? held.control : null');
  });

  it('publishes the state the capture entries grey out with, and the result card and detail use it', () => {
    expect(stage).toContain('setStageModelState({ taskId: modelTask, state: modelState })');
    for (const file of ['../chat/result-card.tsx', '../result/result-detail.tsx']) {
      const source = read(file);
      expect(source, file).toContain('useModelCaptureBlock(');
      expect(source, file).toContain('disabled={captureBlock !== null}');
    }
  });
});

describe('3D viewer: incomplete textures are a notice, not a failure (#127)', () => {
  it('reads the failed textures from the loaded model and shows the Schema notice over the model', () => {
    expect(viewer).toContain('incompleteTextureCountOf((element as unknown as { originalGltfJson?: unknown }).originalGltfJson)');
    expect(viewer).toContain("{ready && incompleteTextures > 0 ? (");
    expect(viewer).toContain("schemaCountMessage(schema, 'ui.viewer.textures_incomplete', incompleteTextures, { count: incompleteTextures })");
    expect(viewer).toContain('data-testid="model-viewer-textures-incomplete"');
  });

  it('keeps capture and part export off for an incomplete model', () => {
    expect(viewer).toContain('incompleteTexturesRef.current > 0) return;');
    expect(viewer).toContain("incompleteTexturesRef.current > 0) throw new Error('model is not loaded');");
    expect(viewer).toContain('disabled={!ready || incompleteTextures > 0}');
    expect(stage).toContain("if (modelState === 'incomplete') {");
  });

  it('declares the texts in both languages', () => {
    for (const key of ['ui.viewer.textures_incomplete', 'ui.viewer.textures_incomplete_one', 'ui.studio.stage.action.capture_textures_incomplete']) {
      expect(catalog.split(`"${key}"`).length - 1, key).toBe(2);
    }
  });
});

describe('canvas picture limit: capture and every add entry say so (#149)', () => {
  const board = read('../workspace/workspace-canvas-board.tsx');
  const canvasBoard = read('../canvas/canvas-board.tsx');

  it('tells the person a full canvas is not a failed screenshot, and warns before they capture', () => {
    expect(stage).toContain('if (error instanceof CanvasBitmapLimitError) {');
    expect(stage).toContain("bitmapLimitMessage(schema, 'reached'");
    expect(stage).toContain('data-testid="stage-canvas-capacity"');
    expect(stage).toContain('capacity.full || capacity.near');
    expect(viewer).toContain('onCaptureError?.(error)');
    // The limit is the Schema's, never a literal.
    expect(stage + read('../../hooks/use-model-capture.ts') + read('../../lib/canvas/bitmap-limit.ts')).not.toMatch(/\b50\b/);
  });

  it('keeps the board, its uploads, merges, inpaint layers and brush layers off a full canvas', () => {
    expect(board).toContain('useBitmapLimitNotice();');
    expect(board).toContain('})) return null;');
    expect(board).toContain('if (!store.stampNodes(');
    expect(board).toContain('layerAdded');
    expect(canvasBoard).toContain('if (!store.getState().addCanvasLayer(sessionId, layer)) return;');
    // Every picture-adding action of the store takes the limit into account.
    const store = read('../../store/canvas-store.ts');
    for (const action of ['addNode', 'addCanvasLayer', 'stampNodes', 'duplicateSelected', 'pasteNodes']) {
      const body = store.slice(store.indexOf(`  ${action}: (`, store.indexOf('export const useCanvasStore')));
      expect(body.slice(0, 900), action).toMatch(/overBitmapLimit|bitmapRefused/);
    }
  });
});
