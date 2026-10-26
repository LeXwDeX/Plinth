import { beforeEach, describe, expect, it } from 'vitest';

import type { TrackedTaskProjection } from '@/lib/workspace/task-projection';
import { useCanvasStore } from './canvas-store';
import { useMaskStore } from './mask-store';
import { domainCapabilities, useWorkspaceStore } from './workspace-store';

it('keeps capabilities opted out of the generic workspace model selector separate', () => {
  const schema = { capabilities: [
    { id: 'mesh-model', domain: 'model-3d', order: 1 },
    { id: 'image-to-splat', domain: 'model-3d', order: 2, workspace_model_selector: false },
  ] } as any;
  expect(domainCapabilities(schema, 'model-3d').map((item) => item.id)).toEqual(['mesh-model']);
});

const completedModel: TrackedTaskProjection = {
  state: 'SUCCEEDED', progress: 100, terminal: true, failure: null,
  assets: [{ id: 'output', role: 'model', variant: 'output.glb', delivery: 'required',
    ordinal: 0, mediaType: 'model/gltf-binary', size: 12,
    url: '/api/v1/tasks/step/assets/output', kind: 'model' }],
  downloadables: [{ id: 'output', role: 'model', variant: 'output.glb', delivery: 'required',
    ordinal: 0, mediaType: 'model/gltf-binary', size: 12,
    url: '/api/v1/tasks/step/assets/output', kind: 'model' }],
  resources: [], resourcesPending: false,
};

beforeEach(() => {
  useWorkspaceStore.setState({ trackedTasks: [], currentObject: null, pendingPreviewTaskId: null });
});

describe('workspace task registration', () => {
  it('tracks completed plan steps while preserving the model being processed', () => {
    const source = { taskId: 'source', kind: 'model' as const,
      url: '/api/v1/tasks/source/assets/model', assetId: 'model' };
    useWorkspaceStore.getState().setCurrentObject(source);
    useWorkspaceStore.getState().registerTask({ taskId: 'step', capability: 'rigging',
      progressMaximum: 100, autoPreview: false });
    useWorkspaceStore.getState().applyTaskDetail('step', completedModel, null);

    const state = useWorkspaceStore.getState();
    expect(state.currentObject).toEqual(source);
    expect(state.pendingPreviewTaskId).toBeNull();
    expect(state.trackedTasks[0]).toMatchObject({ taskId: 'step', state: 'SUCCEEDED', terminal: true });
    expect(state.trackedTasks[0].downloadables.map((asset) => asset.id)).toEqual(['output']);
  });

  it('continues to preview completed tasks registered by the Agent', () => {
    useWorkspaceStore.getState().registerTask({ taskId: 'step', sessionId: 'session-a', capability: 'text-to-3d', progressMaximum: 100 });
    useWorkspaceStore.getState().applyTaskDetail('step', completedModel, 'session-a');
    expect(useWorkspaceStore.getState().currentObject).toMatchObject({ taskId: 'step', assetId: 'output' });
  });

  it('previews the model, not its thumbnail, of a finished 3D result', () => {
    const thumbnail = { id: 'thumb', role: 'thumbnail', variant: '', delivery: 'required', ordinal: 0,
      mediaType: 'image/png', size: 1, url: '/api/v1/tasks/step/assets/thumb', kind: 'image' as const };
    useWorkspaceStore.getState().registerTask({ taskId: 'step', sessionId: 'session-a', capability: 'retexture', progressMaximum: 100 });
    useWorkspaceStore.getState().applyTaskDetail('step', {
      ...completedModel, downloadables: [thumbnail, ...completedModel.downloadables] }, 'session-a');
    expect(useWorkspaceStore.getState().currentObject).toMatchObject({ taskId: 'step', kind: 'model', assetId: 'output' });
  });

  it('does not preview a task that belongs to another or an unknown session', () => {
    useWorkspaceStore.getState().registerTask({ taskId: 'step', sessionId: 'session-a', capability: 'text-to-3d', progressMaximum: 100 });
    useWorkspaceStore.getState().applyTaskDetail('step', completedModel, 'session-b');
    expect(useWorkspaceStore.getState().currentObject).toBeNull();
    useWorkspaceStore.getState().applyTaskDetail('step', completedModel, null);
    expect(useWorkspaceStore.getState().currentObject).toBeNull();
  });
});

describe('canvas reference and chat attachment queue', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      canvasReference: null,
      pendingChatAttachments: [],
    });
  });

  it('pins the canvas reference until explicitly cleared', () => {
    const state = useWorkspaceStore.getState();
    state.setCanvasReference('/api/v1/assets/a1');
    expect(useWorkspaceStore.getState().canvasReference).toBe('/api/v1/assets/a1');
    useWorkspaceStore.getState().setCurrentObject({ taskId: 't', kind: 'image', url: '/api/v1/assets/a2' });
    // 选中变化不影响已钉住的参考图。
    expect(useWorkspaceStore.getState().canvasReference).toBe('/api/v1/assets/a1');
    useWorkspaceStore.getState().setCanvasReference(null);
    expect(useWorkspaceStore.getState().canvasReference).toBeNull();
  });

  it('queues send-to-chat attachments and consumes them once', () => {
    useWorkspaceStore.getState().queueChatAttachments(['/a.png']);
    useWorkspaceStore.getState().queueChatAttachments(['/b.png', '/c.png']);
    expect(useWorkspaceStore.getState().consumeChatAttachments()).toEqual([
      '/a.png', '/b.png', '/c.png',
    ]);
    expect(useWorkspaceStore.getState().pendingChatAttachments).toEqual([]);
    expect(useWorkspaceStore.getState().consumeChatAttachments()).toEqual([]);
  });

  it('keeps the platform source of a queued capture until it is sent', () => {
    useWorkspaceStore.setState({ attachmentSources: {} });
    useWorkspaceStore.getState().queueChatAttachments([
      '/plain.png',
      { url: '/api/v1/assets/shot', assetId: 'shot', taskId: 'model-1', captureId: 'capture-1' },
      { url: '/bare.png' },
    ]);
    expect(useWorkspaceStore.getState().consumeChatAttachments()).toEqual(['/plain.png', '/api/v1/assets/shot', '/bare.png']);
    expect(useWorkspaceStore.getState().attachmentSources).toEqual({
      '/api/v1/assets/shot': { assetId: 'shot', taskId: 'model-1', captureId: 'capture-1' },
    });
    useWorkspaceStore.getState().forgetAttachmentSources(['/api/v1/assets/shot']);
    expect(useWorkspaceStore.getState().attachmentSources).toEqual({});
  });

  it('drops queued sources with the session context', () => {
    useWorkspaceStore.getState().queueChatAttachments([{ url: '/x.png', captureId: 'capture-1' }]);
    useWorkspaceStore.getState().clearSessionContext();
    expect(useWorkspaceStore.getState().attachmentSources).toEqual({});
    expect(useWorkspaceStore.getState().pendingChatAttachments).toEqual([]);
  });

  it('puts the mask tool down with the session context', () => {
    useCanvasStore.getState().setActiveTool('mask');
    useWorkspaceStore.getState().clearSessionContext();
    expect(useCanvasStore.getState().activeTool).toBe('select');
  });

  describe('new results while painting', () => {
    const imageResult: TrackedTaskProjection = {
      ...completedModel,
      assets: [], resources: [],
      downloadables: [{ id: 'picture', role: 'image', variant: '', delivery: 'required', ordinal: 0,
        mediaType: 'image/png', size: 1, url: '/api/v1/tasks/pic/assets/picture', kind: 'image' }],
    };
    const other = { taskId: 'other', kind: 'image' as const, url: '/api/v1/tasks/other/assets/x', assetId: 'x', sessionId: 'session-a' };

    it('takes the stage when no paint tool is active', () => {
      useWorkspaceStore.setState({ currentObject: other, paintToolActive: false, pendingResult: null });
      useWorkspaceStore.getState().registerTask({ taskId: 'pic', sessionId: 'session-a', capability: 'text-to-image', progressMaximum: 100 });
      useWorkspaceStore.getState().applyTaskDetail('pic', imageResult, 'session-a');
      expect(useWorkspaceStore.getState().currentObject).toMatchObject({ taskId: 'pic' });
      expect(useWorkspaceStore.getState().pendingResult).toBeNull();
    });

    it('waits behind a pill while a paint tool is active on another object', () => {
      useWorkspaceStore.setState({ currentObject: other, paintToolActive: true, pendingResult: null });
      useWorkspaceStore.getState().registerTask({ taskId: 'pic', sessionId: 'session-a', capability: 'text-to-image', progressMaximum: 100 });
      useWorkspaceStore.getState().applyTaskDetail('pic', imageResult, 'session-a');
      expect(useWorkspaceStore.getState().currentObject).toEqual(other);
      expect(useWorkspaceStore.getState().pendingResult).toMatchObject({ taskId: 'pic' });
      useWorkspaceStore.getState().openPendingResult();
      expect(useWorkspaceStore.getState().currentObject).toMatchObject({ taskId: 'pic' });
      expect(useWorkspaceStore.getState().pendingResult).toBeNull();
    });

    it('keeps the mask when the pill opens the new result: it belongs to the canvas', () => {
      useMaskStore.setState({ draft: null, question: null });
      useCanvasStore.getState().setActiveTool('mask');
      useWorkspaceStore.setState({ currentObject: other, paintToolActive: true, pendingResult: null });
      useMaskStore.getState().setDraft({ sessionId: 'session-a', strokes: [{ points: [0, 0, 1, 1], size: 12 }], redo: [], current: null });
      useWorkspaceStore.getState().registerTask({ taskId: 'pic', sessionId: 'session-a', capability: 'text-to-image', progressMaximum: 100 });
      useWorkspaceStore.getState().applyTaskDetail('pic', imageResult, 'session-a');
      useWorkspaceStore.getState().openPendingResult();
      expect(useWorkspaceStore.getState().currentObject).toMatchObject({ taskId: 'pic' });
      expect(useMaskStore.getState().draft?.strokes).toHaveLength(1);
      expect(useCanvasStore.getState().activeTool).toBe('mask');
      useMaskStore.setState({ draft: null });
      useCanvasStore.getState().setActiveTool('select');
    });

    it('clears the pill when the user picks something explicitly', () => {
      useWorkspaceStore.setState({ currentObject: other, paintToolActive: true, pendingResult: { ...other, taskId: 'late' } });
      useWorkspaceStore.getState().setCurrentObject({ ...other, taskId: 'chosen' });
      expect(useWorkspaceStore.getState().pendingResult).toBeNull();
    });
  });
});

describe('retired generation model notice (#329)', () => {
  it('records the switch per domain, is not persisted, and clears when the person picks again', async () => {
    const { partializeWorkspacePrefs } = await import('./workspace-store');
    const store = useWorkspaceStore.getState();
    store.switchRetiredModel('model-3d', 'meshy-7.1', { from: 'Meshy 7', to: 'Meshy 7.1' });
    let state = useWorkspaceStore.getState();
    expect(state.modelByDomain['model-3d']).toBe('meshy-7.1');
    expect(state.retiredModelNotice['model-3d']).toEqual({ from: 'Meshy 7', to: 'Meshy 7.1' });
    expect('retiredModelNotice' in partializeWorkspacePrefs(state)).toBe(false);
    state.setModel('model-3d', 'meshy-t2.5');
    state = useWorkspaceStore.getState();
    expect(state.retiredModelNotice['model-3d']).toBeUndefined();
    state.setModel('model-3d', null);
  });
});
