import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { BoardDelivery } from '@/lib/canvas/board-delivery';
import type { CurrentObject } from '@/store/workspace-store';

const mocks = vi.hoisted(() => ({
  delivery: null as BoardDelivery | null,
  rasterise: vi.fn(async () => new Blob(['png'])),
  upload: vi.fn(async () => '/api/v1/assets/board-1'),
  toastError: vi.fn(),
  toastInfo: vi.fn(),
  report: vi.fn(),
  offerings: undefined as Record<string, unknown[]> | undefined,
}));

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }) }));
vi.mock('@/lib/canvas/board-delivery', () => ({
  boardDeliveryOf: () => mocks.delivery,
  rasteriseSessionBoard: mocks.rasterise,
  boardPixelsFor3D: () => ({ width: 1024, height: 1024 }),
}));
vi.mock('@/lib/workspace/input-asset-upload', () => ({ uploadWorkspaceInputAsset: mocks.upload }));
vi.mock('sonner', () => ({ toast: { error: mocks.toastError, info: mocks.toastInfo, success: vi.fn() } }));
vi.mock('@/hooks/use-workspace-offerings', () => ({ useWorkspaceOfferings: () => ({ offeringsByCapability: mocks.offerings }) }));
vi.mock('@/lib/client-error-report', () => ({ reportClientError: mocks.report }));

import { useObjectActions } from '@/hooks/use-object-actions';
import { CanvasExportError } from '@/lib/canvas/export-canvas';
import { registerPendingEdits } from '@/lib/canvas/pending-edits';
import { configureCanvasFrontend, useCanvasStore, type CanvasNode } from '@/store/canvas-store';
import { useChatStore } from '@/store/chat-store';
import { useComposerSettingsStore } from '@/store/composer-settings-store';
import { useModel3DConfirmStore } from '@/store/model3d-confirm-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';

const frontend = (defaultCatalog as unknown as { application: { canvas: { frontend: Parameters<typeof configureCanvasFrontend>[0] } } }).application.canvas.frontend;

const SESSION = 's1';
const object: CurrentObject = { taskId: 't1', kind: 'image', url: '/api/v1/tasks/t1/assets/a1', handle: '@img3', sessionId: SESSION };
const picture = (over: Partial<CanvasNode> = {}): CanvasNode => ({
  id: 'p1', type: 'image', taskId: 't1', imageUrl: object.url, x: 0, y: 0, width: 100, height: 100, zIndex: 1, ...over,
});

function actions() {
  let captured: ReturnType<typeof useObjectActions> | null = null;
  function Probe() {
    captured = useObjectActions();
    return null;
  }
  renderToStaticMarkup(<Probe />);
  return captured as unknown as ReturnType<typeof useObjectActions>;
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('转 3D on a result', () => {
  beforeEach(() => {
    configureCanvasFrontend(frontend);
    useChatStore.setState({ activeSessionId: SESSION });
    useWorkspaceStore.setState({ modelByDomain: { ...useWorkspaceStore.getState().modelByDomain, 'model-3d': 'm3d' } });
    mocks.delivery = { artboard: { x: 0, y: 0, width: 100, height: 100 }, pixels: null };
    useStudioStore.setState({ turnRequest: null });
  });
  afterEach(() => {
    useCanvasStore.setState({ nodesBySession: {} });
    mocks.rasterise.mockClear();
    mocks.upload.mockClear();
    mocks.toastError.mockClear();
  });

  it('rasterises the board even when it holds just the object\'s own untouched picture', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture()] } });
    expect(actions().toThreeD(object)).toBe(true);
    await flush();
    expect(mocks.rasterise).toHaveBeenCalledTimes(1);
    expect(useStudioStore.getState().turnRequest?.text).not.toContain('@img3');
    expect(useStudioStore.getState().turnRequest?.selection).toEqual([{ kind: 'image', url: '/api/v1/assets/board-1' }]);
  });

  it('keeps the handle when the board holds nothing', () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture({ x: 2000 })] } });
    expect(actions().toThreeD(object)).toBe(true);
    expect(useStudioStore.getState().turnRequest?.text).toContain('@img3');
    expect(useStudioStore.getState().turnRequest?.selection).toBeUndefined();
    expect(mocks.rasterise).not.toHaveBeenCalled();
  });

  it('#691: 只用正面转 3D converts the picture itself even while the board holds its layer, and never places it', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture(), picture({ id: 'p2', imageUrl: '/other.png' })] } });
    expect(actions().toThreeD(object, { pictureOnly: true })).toBe(true);
    await flush();
    expect(mocks.rasterise).not.toHaveBeenCalled();
    expect(useStudioStore.getState().turnRequest?.text).toContain('@img3');
    expect(useWorkspaceStore.getState().currentObject).toMatchObject({ taskId: 't1', placement: 'view' });
  });

  it('requests a dedicated Gaussian Splat Agent turn with exactly the selected owned image and no board rasterization', () => {
    const source = { ...object, assetId: 'asset-image' };
    expect(actions().toGaussianSplat(source)).toBe(true);
    expect(useStudioStore.getState().turnRequest?.text).toBe(messages['ui.studio.action.splat_turn'].replace('{handle}', '@img3'));
    expect(useStudioStore.getState().turnRequest?.selection).toEqual([{ kind: 'image', taskId: 't1', assetId: 'asset-image' }]);
    expect(mocks.rasterise).not.toHaveBeenCalled();
    expect(mocks.upload).not.toHaveBeenCalled();
  });

  it('rasterises the board when it holds several layers, and sends that picture instead of the handle', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture(), picture({ id: 'p2', imageUrl: '/other', taskId: 't2', zIndex: 2 })] } });
    expect(actions().toThreeD(object)).toBe(true);
    await flush();
    expect(mocks.rasterise).toHaveBeenCalledTimes(1);
    expect(mocks.upload).toHaveBeenCalledTimes(1);
    const request = useStudioStore.getState().turnRequest;
    expect(request?.text).toBe(messages['ui.studio.action.to3d_turn_board']);
    expect(request?.text).not.toContain('@img3');
    expect(request?.selection).toEqual([{ kind: 'image', url: '/api/v1/assets/board-1' }]);
  });

  it('rasterises the board when the object\'s picture was edited on it', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture({ rotation: 15 })] } });
    actions().toThreeD(object);
    await flush();
    expect(mocks.rasterise).toHaveBeenCalledTimes(1);
  });

  it('asks for a 3D model first, and does nothing for a model object', () => {
    useWorkspaceStore.setState({ modelByDomain: { ...useWorkspaceStore.getState().modelByDomain, 'model-3d': null } });
    // #678: with no model list to offer one from, the settings open on the models tab and the toast says why.
    mocks.offerings = undefined;
    useComposerSettingsStore.setState({ open: false, tab: 'params' });
    expect(actions().toThreeD(object)).toBe(false);
    expect(mocks.toastInfo).toHaveBeenCalledWith(messages['ui.studio.action.choose_3d_model']);
    expect(useComposerSettingsStore.getState()).toMatchObject({ open: true, tab: 'models' });
    expect(actions().toThreeD({ ...object, kind: 'model' })).toBe(false);
  });

  describe('#678: 转 3D without a 3D model picked', () => {
    const offering = (model_id: string, extra: Record<string, unknown> = {}) => ({
      id: `o-${model_id}`, model_id, model_name: model_id.toUpperCase(), capability: 'image-to-3d', available: true,
      credits_metered: true, estimated_credits: 30, parameters: [], provider_id: 'p', provider_name: 'P', ...extra,
    });
    beforeEach(() => {
      useWorkspaceStore.setState({ modelByDomain: { ...useWorkspaceStore.getState().modelByDomain, 'model-3d': null } });
      useModel3DConfirmStore.setState({ request: null });
      useComposerSettingsStore.setState({ open: false, tab: 'params' });
      mocks.offerings = { 'image-to-3d': [offering('first'), offering('picked-by-schema', { recommended: true }), offering('off', { available: false, recommended: true })] };
    });
    afterEach(() => { mocks.offerings = undefined; });

    it('asks to use the recommended available model, with its cost, and requests nothing yet', () => {
      expect(actions().toThreeD(object)).toBe(false);
      expect(useStudioStore.getState().turnRequest).toBeNull();
      expect(useModel3DConfirmStore.getState().request).toMatchObject({ modelId: 'picked-by-schema', modelName: 'PICKED-BY-SCHEMA', cost: '约 30 积分' });
      expect(useComposerSettingsStore.getState().open).toBe(false);
    });

    it('confirming converts once with that model for this turn only: no pick, no lock', () => {
      useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture({ x: 2000 })] } });
      actions().toThreeD(object);
      const request = useModel3DConfirmStore.getState().request!;
      request.run(request.modelId);
      const turn = useStudioStore.getState().turnRequest;
      expect(turn?.text).toContain('@img3');
      expect(turn?.models).toEqual({ 'model-3d': 'picked-by-schema' });
      expect(useWorkspaceStore.getState().modelByDomain['model-3d']).toBeNull();
    });

    it('asks the same for an uploaded front view and for the multiview button, naming the multiview model', () => {
      expect(actions().inputPictureToThreeD('/api/v1/assets/up-front')).toBe(false);
      expect(useModel3DConfirmStore.getState().request?.modelId).toBe('picked-by-schema');
      useModel3DConfirmStore.setState({ request: null });
      const model = { modelId: 'mv', modelName: 'MV', views: [{ value: 'front', label: '正面' }, { value: 'left', label: '左侧' }], minimumViews: 2, mediaTypes: [] };
      const slot = (key: string, label: string, extra: Record<string, unknown>) => ({
        key, label, optional: false, replaced: false, status: 'succeeded' as const, attempt: 0, taskId: '', outputAssetId: '', requestId: '',
        startable: false, retryable: false, estimatedCredits: 0, creditsMetered: false, ...extra,
      });
      const group = {
        runId: 'run1', label: '三视图', sourceAssetId: '', createdAt: '', complete: false,
        slots: [slot('front', '正面', { taskId: 't1', outputAssetId: 'x1' }), slot('left', '左侧', { taskId: 't2', outputAssetId: 'x2' })],
        modelSource: { slot: 'front', available: true, taskId: 't1', assetId: 'x1', note: '' },
      };
      expect(actions().multiviewToThreeD(group, model)).toBe(false);
      const request = useModel3DConfirmStore.getState().request!;
      expect(request).toMatchObject({ modelId: 'mv', modelName: 'MV' });
      request.run(request.modelId);
      expect(useStudioStore.getState().turnRequest?.multiview?.model).toBe('mv');
      expect(useStudioStore.getState().turnRequest?.models).toEqual({ 'model-3d': 'mv' });
    });
  });
});

describe('发送到 3D from the board', () => {
  beforeEach(() => {
    configureCanvasFrontend(frontend);
    useChatStore.setState({ activeSessionId: SESSION });
    useWorkspaceStore.setState({ modelByDomain: { ...useWorkspaceStore.getState().modelByDomain, 'model-3d': 'm3d' } });
    mocks.delivery = { artboard: { x: 0, y: 0, width: 100, height: 100 }, pixels: null };
    useStudioStore.setState({ turnRequest: null });
    useWorkspaceStore.setState({ currentObject: null });
  });
  afterEach(() => {
    useCanvasStore.setState({ nodesBySession: {} });
    mocks.rasterise.mockClear();
    mocks.upload.mockClear();
    mocks.toastError.mockClear();
  });

  it('does not convert an empty board', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [] } });
    await expect(actions().boardToThreeD(SESSION)).resolves.toBe(false);
    expect(mocks.toastError).toHaveBeenCalledWith(messages['ui.studio.action.board_empty']);
    expect(useStudioStore.getState().turnRequest).toBeNull();
  });

  it('rasterises with the single rasteriser, uploads, and requests the turn with that picture', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture()] } });
    await expect(actions().boardToThreeD(SESSION)).resolves.toBe(true);
    expect(mocks.rasterise).toHaveBeenCalledWith(defaultCatalog.application, SESSION, { ...mocks.delivery, pixels: { width: 1024, height: 1024 } });
    expect(useStudioStore.getState().turnRequest?.selection).toEqual([{ kind: 'image', url: '/api/v1/assets/board-1' }]);
  });

  it('rasterises the board even while it holds only the current object\'s untouched picture', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture()] } });
    useWorkspaceStore.setState({ currentObject: object });
    await expect(actions().boardToThreeD(SESSION)).resolves.toBe(true);
    expect(mocks.rasterise).toHaveBeenCalledTimes(1);
    expect(mocks.upload).toHaveBeenCalledTimes(1);
    expect(useStudioStore.getState().turnRequest?.text).not.toContain('@img3');
    expect(useStudioStore.getState().turnRequest?.selection).toEqual([{ kind: 'image', url: '/api/v1/assets/board-1' }]);
  });

  it('lands the strokes still on their way before rasterising', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture()] } });
    useWorkspaceStore.setState({ currentObject: object });
    const unregister = registerPendingEdits(SESSION, async () => {
      useCanvasStore.getState().updateNode(SESSION, 'p1', { imageUrl: '/api/v1/assets/painted' }, { recordHistory: false });
    });
    try {
      await expect(actions().boardToThreeD(SESSION)).resolves.toBe(true);
    } finally {
      unregister();
    }
    expect(mocks.rasterise).toHaveBeenCalledTimes(1);
    expect(useStudioStore.getState().turnRequest?.selection).toEqual([{ kind: 'image', url: '/api/v1/assets/board-1' }]);
  });

  it('rasterises when the board is edited or holds more than one layer', async () => {
    useWorkspaceStore.setState({ currentObject: object });
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture({ rotation: 15 })] } });
    await expect(actions().boardToThreeD(SESSION)).resolves.toBe(true);
    expect(mocks.rasterise).toHaveBeenCalledTimes(1);
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture(), picture({ id: 'p2', imageUrl: '/other', zIndex: 2 })] } });
    await expect(actions().boardToThreeD(SESSION)).resolves.toBe(true);
    expect(mocks.rasterise).toHaveBeenCalledTimes(2);
  });

  it('reports a failed upload and requests nothing', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture()] } });
    mocks.upload.mockRejectedValueOnce(new Error('network'));
    mocks.report.mockClear();
    await expect(actions().boardToThreeD(SESSION)).resolves.toBe(false);
    expect(mocks.toastError).toHaveBeenCalledWith(messages['ui.workspace.canvas.snapshot.upload_failed']);
    expect(mocks.report).toHaveBeenCalledWith('canvas-board-to-3d-upload', expect.anything(), { requestId: '' });
    expect(useStudioStore.getState().turnRequest).toBeNull();
  });

  it('#286: says reading the board failed, and does not upload', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture()] } });
    mocks.rasterise.mockRejectedValueOnce(new CanvasExportError('导出失败', 'decode', { bytes: 12 }));
    mocks.report.mockClear();
    mocks.upload.mockClear();
    await expect(actions().boardToThreeD(SESSION)).resolves.toBe(false);
    expect(mocks.upload).not.toHaveBeenCalled();
    expect(mocks.toastError).toHaveBeenCalledWith(messages['ui.workspace.canvas.snapshot.read_failed']);
    expect(mocks.report.mock.calls[0][0]).toBe('canvas-board-to-3d-export');
    expect(useStudioStore.getState().turnRequest).toBeNull();
  });

  it('#286: does not send a board whose brush strokes failed to upload', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture()] } });
    const unregister = registerPendingEdits(SESSION, async () => false);
    mocks.report.mockClear();
    mocks.rasterise.mockClear();
    try {
      await expect(actions().boardToThreeD(SESSION)).resolves.toBe(false);
    } finally {
      unregister();
    }
    expect(mocks.rasterise).not.toHaveBeenCalled();
    expect(mocks.toastError).toHaveBeenCalledWith(messages['ui.workspace.canvas.snapshot.flush_failed']);
    expect(mocks.report.mock.calls[0][0]).toBe('canvas-board-to-3d-flush');
  });
});

describe('用图片重贴图', () => {
  beforeEach(() => {
    configureCanvasFrontend(frontend);
    useChatStore.setState({ activeSessionId: SESSION });
    mocks.delivery = { artboard: { x: 0, y: 0, width: 100, height: 100 }, pixels: { width: 512, height: 512 } };
    useStudioStore.setState({ turnRequest: null });
  });
  afterEach(() => {
    useCanvasStore.setState({ nodesBySession: {} });
    mocks.rasterise.mockClear();
    mocks.upload.mockClear();
    mocks.toastError.mockClear();
  });

  it('names the untouched picture by its handle, with nothing else riding along', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture()] } });
    await expect(actions().retextureWithImage(object, '@model2', '@img3')).resolves.toBe(true);
    expect(mocks.rasterise).not.toHaveBeenCalled();
    const request = useStudioStore.getState().turnRequest;
    expect(request?.text).toContain('@img3');
    expect(request?.text).toContain('@model2');
    expect(request?.selection).toEqual([]);
  });

  it('sends the rasterised frame once the picture was edited or joined by another layer', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture({ rotation: 15 })] } });
    await expect(actions().retextureWithImage(object, '@model2', '@img3')).resolves.toBe(true);
    expect(mocks.rasterise).toHaveBeenCalledWith(defaultCatalog.application, SESSION, mocks.delivery);
    let request = useStudioStore.getState().turnRequest;
    expect(request?.text).not.toContain('@img3');
    expect(request?.text).toContain('@model2');
    expect(request?.selection).toEqual([{ kind: 'image', url: '/api/v1/assets/board-1' }]);
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture(), picture({ id: 'p2', imageUrl: '/other', zIndex: 2 })] } });
    await actions().retextureWithImage(object, '@model2', '@img3');
    expect(mocks.rasterise).toHaveBeenCalledTimes(2);
    request = useStudioStore.getState().turnRequest;
    expect(request?.selection).toEqual([{ kind: 'image', url: '/api/v1/assets/board-1' }]);
  });

  it('lands the strokes still on their way before deciding, so a freshly painted picture is rasterised', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture()] } });
    const unregister = registerPendingEdits(SESSION, async () => {
      useCanvasStore.getState().updateNode(SESSION, 'p1', { imageUrl: '/api/v1/assets/painted' }, { recordHistory: false });
    });
    try {
      await actions().retextureWithImage(object, '@model2', '@img3');
    } finally {
      unregister();
    }
    expect(mocks.rasterise).toHaveBeenCalledTimes(1);
  });

  it('keeps the handle when the frame is empty, and reports a failed upload without a turn', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [] } });
    await actions().retextureWithImage(object, '@model2', '@img3');
    expect(useStudioStore.getState().turnRequest?.text).toContain('@img3');
    expect(useStudioStore.getState().turnRequest?.selection).toBeUndefined();
    expect(mocks.rasterise).not.toHaveBeenCalled();
    useStudioStore.setState({ turnRequest: null });
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture({ opacity: 0.5 })] } });
    mocks.upload.mockRejectedValueOnce(new Error('network'));
    mocks.report.mockClear();
    await expect(actions().retextureWithImage(object, '@model2', '@img3')).resolves.toBe(false);
    expect(mocks.toastError).toHaveBeenCalledWith(messages['ui.workspace.canvas.snapshot.upload_failed']);
    expect(mocks.report.mock.calls[0][0]).toBe('canvas-retexture-upload');
    expect(useStudioStore.getState().turnRequest).toBeNull();
  });

  it('#286: does not decide or send while brush strokes failed to upload', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture({ rotation: 15 })] } });
    const unregister = registerPendingEdits(SESSION, async () => false);
    mocks.report.mockClear();
    try {
      await expect(actions().retextureWithImage(object, '@model2', '@img3')).resolves.toBe(false);
    } finally {
      unregister();
    }
    expect(mocks.rasterise).not.toHaveBeenCalled();
    expect(mocks.toastError).toHaveBeenCalledWith(messages['ui.workspace.canvas.snapshot.flush_failed']);
    expect(mocks.report.mock.calls[0][0]).toBe('canvas-retexture-flush');
    expect(useStudioStore.getState().turnRequest).toBeNull();
  });

  it('rasterises an untouched picture that has no handle, and refuses an object of another session', async () => {
    useCanvasStore.setState({ nodesBySession: { [SESSION]: [picture()] } });
    await actions().retextureWithImage({ ...object, handle: undefined }, '@model2', '');
    expect(mocks.rasterise).toHaveBeenCalledTimes(1);
    expect(useStudioStore.getState().turnRequest?.selection).toEqual([{ kind: 'image', url: '/api/v1/assets/board-1' }]);
    useStudioStore.setState({ turnRequest: null });
    useChatStore.setState({ activeSessionId: 'other' });
    await expect(actions().retextureWithImage(object, '@model2', '@img3')).resolves.toBe(false);
    expect(mocks.upload).toHaveBeenCalledTimes(1);
    expect(useStudioStore.getState().turnRequest).toBeNull();
  });
});

// #65: the three-view card's two conversions.
describe('three-view to 3D', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({ modelByDomain: { ...useWorkspaceStore.getState().modelByDomain, 'model-3d': 'm3d' } });
    useStudioStore.setState({ turnRequest: null });
    mocks.toastError.mockClear();
  });

  it('converts a front view the person replaced with an upload as the turn\'s picture', () => {
    expect(actions().inputPictureToThreeD('/api/v1/assets/up-front')).toBe(true);
    const request = useStudioStore.getState().turnRequest;
    expect(request?.text).toBe(messages['ui.studio.action.to3d_turn_current']);
    expect(request?.selection).toEqual([{ kind: 'image', url: '/api/v1/assets/up-front' }]);
    expect(request?.multiview).toBeUndefined();
  });

  it('asks for a 3D model before converting an uploaded front view', () => {
    useWorkspaceStore.setState({ modelByDomain: { ...useWorkspaceStore.getState().modelByDomain, 'model-3d': null } });
    expect(actions().inputPictureToThreeD('/api/v1/assets/up-front')).toBe(false);
    expect(useStudioStore.getState().turnRequest).toBeNull();
    expect(mocks.toastInfo).toHaveBeenCalledWith(messages['ui.studio.action.choose_3d_model']);
  });

  it('sends the group\'s finished views as the structured multiview request', () => {
    const model = { modelId: 'mv', modelName: 'MV', views: [{ value: 'front', label: '正面' }, { value: 'left', label: '左侧' }], minimumViews: 2, mediaTypes: [] };
    const slot = (key: string, label: string, extra: Record<string, unknown>) => ({
      key, label, optional: false, replaced: false, status: 'succeeded' as const, attempt: 0, taskId: '', outputAssetId: '', requestId: '',
      startable: false, retryable: false, estimatedCredits: 0, creditsMetered: false, ...extra,
    });
    const group = {
      runId: 'run1', label: '三视图', sourceAssetId: '', createdAt: '', complete: false,
      slots: [slot('front', '正面', { taskId: 't1', outputAssetId: 'x1' }), slot('left', '左侧', { outputAssetId: 'up2', replaced: true })],
      modelSource: { slot: 'front', available: true, taskId: 't1', assetId: 'x1', note: '' },
    };
    expect(actions().multiviewToThreeD(group, model)).toBe(true);
    const request = useStudioStore.getState().turnRequest;
    expect(request?.selection).toEqual([]);
    expect(request?.multiview).toEqual({ group: 'run1', model: 'mv', views: [{ view: 'front', assetId: 'x1', taskId: 't1' }, { view: 'left', assetId: 'up2' }] });
    expect(request?.text).toContain('正面 / 左侧');
  });
});
