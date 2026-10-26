import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { CanvasProductSchema } from '@/lib/product-schema';
import { useCanvasStore, type CanvasNode } from '@/store/canvas-store';
import { CanvasBrowserError, type CanvasBrowserClient, type CanvasProject } from './browser-client';
import { persistCapturedImageToCanvas, type PersistCapturedImageOptions } from './capture-persistence';
import { computeNodesHash, nodeToRecord } from './node-persistence';
import { enqueueSessionCanvasSave } from './session-persistence';

const schema = catalog.application.canvas as unknown as CanvasProductSchema;
const node: CanvasNode = {
  id: 'existing', type: 'text', text: 'saved work', imageUrl: '', taskId: '',
  x: 10, y: 20, width: 200, height: 100, zIndex: 1,
};
function deferred<T = void>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
function project(nodes: CanvasNode[] = [node], revision = 3): CanvasProject {
  return {
    id: 'project-A', userId: 'owner-A', sessionId: 'session-A', revision,
    name: 'A', viewport: '{}', bgStyle: 'grid', createdAt: '', updatedAt: '',
    nodes: nodes.map((entry) => ({ ...nodeToRecord(entry), projectId: 'project-A', createdAt: '', updatedAt: '' })),
    // A sized pixel canvas (#246); the legacy and unsized cases are tested separately.
    frame: { width: 2048, height: 2048, coordinateVersion: 2 }, coordinateVersion: 2,
  };
}
function fixture(server = project()) {
  const client = {
    createProject: vi.fn(async () => server),
    getProject: vi.fn(async () => server),
    replaceNodes: vi.fn(async (_id, records) => ({ ok: true, count: records.length })),
  } as unknown as CanvasBrowserClient;
  const options: PersistCapturedImageOptions = {
    client, schema, ownerID: 'owner-A', sessionID: 'session-A', taskID: 'model-task',
    captureID: 'capture-A', imageURL: '/api/assets/capture-A/content',
    naturalSize: { width: 800, height: 600 }, initialSize: { width: 1536, height: 1024 }, isCurrent: () => true,
  };
  return { client, options };
}
function baseline(nodes = [node], revision = 3) {
  useCanvasStore.setState({
    nodesBySession: { 'session-A': nodes }, projectBySession: { 'session-A': 'project-A' },
    _savedHashBySession: { 'session-A': computeNodesHash(nodes) },
    _savedRevisionBySession: { 'session-A': revision },
  });
}
const savedCapture: CanvasNode = {
  ...node, id: 'capture-capture-A', type: 'image', text: undefined,
  taskId: 'model-task', captureId: 'capture-A', imageUrl: '/api/assets/capture-A/content', zIndex: 2,
};

beforeEach(() => {
  useCanvasStore.setState({ nodesBySession: {}, projectBySession: {}, _savedHashBySession: {}, _savedRevisionBySession: {},
    _undoStackBySession: {}, _redoStackBySession: {}, selectedNodeIds: [] });
});

describe('persist captured image', () => {
  it('appends after an earlier unmount flush and retains that saved work', async () => {
    baseline();
    const gate = deferred();
    const edited = { ...node, text: 'latest work' };
    const { client, options } = fixture(project([edited], 4));
    const previous = enqueueSessionCanvasSave('session-A', async () => {
      await gate.promise;
      baseline([edited], 4);
    });
    const capture = persistCapturedImageToCanvas(options);
    await Promise.resolve();
    expect(client.createProject).not.toHaveBeenCalled();
    gate.resolve();
    await previous;
    await capture;
    const [projectID, records, revision] = vi.mocked(client.replaceNodes).mock.calls[0];
    expect(projectID).toBe('project-A');
    expect(revision).toBe(4);
    expect(records[0]).toMatchObject(nodeToRecord(edited));
    expect(records.map((entry) => entry.id)).toEqual(['existing', 'capture-capture-A']);
  });

  it('merges dirty local work only at its exact revision baseline', async () => {
    baseline();
    const edited = { ...node, text: 'unsaved edit' };
    useCanvasStore.setState({ nodesBySession: { 'session-A': [edited] } });
    const { client, options } = fixture();
    await persistCapturedImageToCanvas(options);
    const records = vi.mocked(client.replaceNodes).mock.calls[0][1];
    expect(records[0]).toEqual(nodeToRecord(edited));
    expect(JSON.parse(records[1].data)).toMatchObject({ captureId: 'capture-A', taskId: 'model-task', imageUrl: options.imageURL });
    expect(useCanvasStore.getState()._savedRevisionBySession['session-A']).toBe(4);
    expect(useCanvasStore.getState()._savedHashBySession['session-A']).toBe(computeNodesHash(useCanvasStore.getState().getNodes('session-A')));
    expect(useCanvasStore.getState()._undoStackBySession['session-A']).toHaveLength(1);
    useCanvasStore.getState().undo('session-A');
    expect(useCanvasStore.getState().getNodes('session-A')).toEqual([edited]);
  });

  it('replays an already persisted capture without another PUT', async () => {
    baseline([node, savedCapture], 4);
    const { client, options } = fixture(project([node, savedCapture], 4));
    await persistCapturedImageToCanvas(options);
    expect(client.replaceNodes).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().getNodes('session-A')).toHaveLength(2);
    expect(useCanvasStore.getState()._savedRevisionBySession['session-A']).toBe(4);
    expect(useCanvasStore.getState()._undoStackBySession['session-A']).toBeUndefined();
  });

  it('respects a local deletion at the known revision instead of restoring the deleted node', async () => {
    baseline();
    useCanvasStore.setState({ nodesBySession: { 'session-A': [] } });
    const { client, options } = fixture();
    await persistCapturedImageToCanvas(options);
    expect(vi.mocked(client.replaceNodes).mock.calls[0][1].map((entry) => entry.id)).toEqual(['capture-capture-A']);
    expect(useCanvasStore.getState().getNodes('session-A').map((entry) => entry.id)).toEqual(['capture-capture-A']);
  });

  it.each([
    { userId: 'foreign-owner' }, { sessionId: 'foreign-session' }, { id: 'foreign-project' }, { revision: 0 },
  ])('rejects foreign or invalid canonical projects %j', async (change) => {
    baseline();
    const { client, options } = fixture({ ...project(), ...change });
    await expect(persistCapturedImageToCanvas(options)).rejects.toMatchObject({ status: 409 });
    expect(client.replaceNodes).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().getNodes('session-A')).toEqual([node]);
  });

  it('rejects a dirty stale baseline without overwriting or discarding local edits', async () => {
    baseline();
    const edited = { ...node, text: 'unsaved edit' };
    useCanvasStore.setState({ nodesBySession: { 'session-A': [edited] } });
    const { client, options } = fixture(project([node], 4));
    await expect(persistCapturedImageToCanvas(options)).rejects.toMatchObject({ status: 409 });
    expect(client.replaceNodes).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().getNodes('session-A')).toEqual([edited]);
    expect(useCanvasStore.getState()._savedRevisionBySession['session-A']).toBe(3);
    expect(useCanvasStore.getState()._undoStackBySession['session-A']).toBeUndefined();
  });

  it.each(['missing nodes', 'foreign node project', 'changed fetched owner'])('rejects incomplete or foreign full GET state: %s', async (kind) => {
    baseline();
    const { client, options } = fixture();
    const server = project();
    if (kind === 'missing nodes') server.nodes = undefined;
    if (kind === 'foreign node project') server.nodes![0].projectId = 'foreign-project';
    if (kind === 'changed fetched owner') server.userId = 'foreign-owner';
    vi.mocked(client.getProject).mockResolvedValue(server);
    await expect(persistCapturedImageToCanvas(options)).rejects.toMatchObject({ status: 409 });
    expect(client.replaceNodes).not.toHaveBeenCalled();
  });

  it('rebases a clean CAS conflict once and preserves the concurrent server node', async () => {
    baseline();
    const extra = { ...node, id: 'other-window', text: 'external edit', zIndex: 2 };
    const { client, options } = fixture();
    vi.mocked(client.replaceNodes).mockRejectedValueOnce(new CanvasBrowserError(409, 'conflict'));
    vi.mocked(client.getProject).mockResolvedValueOnce(project()).mockResolvedValueOnce(project([node, extra], 4));
    await persistCapturedImageToCanvas(options);
    expect(client.replaceNodes).toHaveBeenCalledTimes(2);
    expect(vi.mocked(client.replaceNodes).mock.calls[1][1].map((entry) => entry.id)).toEqual(['existing', 'other-window', 'capture-capture-A']);
    expect(vi.mocked(client.replaceNodes).mock.calls[1][2]).toBe(4);
    expect(useCanvasStore.getState()._savedRevisionBySession['session-A']).toBe(5);
  });

  it('limits conflict retry and never retries a dirty write', async () => {
    baseline();
    const { client, options } = fixture();
    vi.mocked(client.replaceNodes).mockRejectedValue(new CanvasBrowserError(409, 'conflict'));
    await expect(persistCapturedImageToCanvas(options)).rejects.toMatchObject({ status: 409 });
    expect(client.replaceNodes).toHaveBeenCalledTimes(2);
    vi.mocked(client.replaceNodes).mockClear();
    useCanvasStore.setState({ nodesBySession: { 'session-A': [{ ...node, text: 'dirty' }] } });
    await expect(persistCapturedImageToCanvas(options)).rejects.toMatchObject({ status: 409 });
    expect(client.replaceNodes).toHaveBeenCalledTimes(1);
  });

  it('does not rebase over edits made while a clean PUT was waiting', async () => {
    baseline();
    const { client, options } = fixture();
    vi.mocked(client.replaceNodes).mockImplementation(async () => {
      useCanvasStore.setState({ nodesBySession: { 'session-A': [{ ...node, text: 'new local edit' }] } });
      throw new CanvasBrowserError(409, 'concurrent update');
    });
    await expect(persistCapturedImageToCanvas(options)).rejects.toMatchObject({ status: 409 });
    expect(client.replaceNodes).toHaveBeenCalledTimes(1);
    expect(client.getProject).toHaveBeenCalledTimes(1);
    expect(useCanvasStore.getState().getNodes('session-A')[0].text).toBe('new local edit');
    expect(useCanvasStore.getState()._savedRevisionBySession['session-A']).toBe(3);
  });

  it.each([new Error('network failure'), new CanvasBrowserError(500, 'storage failure')])('reports a failed write honestly', async (error) => {
    baseline();
    const { client, options } = fixture();
    vi.mocked(client.replaceNodes).mockRejectedValue(error);
    await expect(persistCapturedImageToCanvas(options)).rejects.toBe(error);
    expect(useCanvasStore.getState().getNodes('session-A')).toEqual([node]);
    expect(useCanvasStore.getState()._savedRevisionBySession['session-A']).toBe(3);
  });

  it.each([{ ok: false, count: 2 }, { ok: true, count: 1 }])('rejects an incomplete bulk acknowledgement %j', async (response) => {
    baseline();
    const { client, options } = fixture();
    vi.mocked(client.replaceNodes).mockResolvedValue(response);
    await expect(persistCapturedImageToCanvas(options)).rejects.toThrow('not acknowledged');
    expect(useCanvasStore.getState().getNodes('session-A')).toEqual([node]);
  });

  it('does not PUT when scope changes while loading the project', async () => {
    baseline();
    const { client, options } = fixture();
    let active = true;
    options.isCurrent = () => active;
    vi.mocked(client.getProject).mockImplementation(async () => { active = false; return project(); });
    await expect(persistCapturedImageToCanvas(options)).rejects.toMatchObject({ status: 409 });
    expect(client.replaceNodes).not.toHaveBeenCalled();
  });

  it('does not create or write a project after the original scope was left', async () => {
    baseline();
    const { client, options } = fixture();
    options.isCurrent = () => false;
    await expect(persistCapturedImageToCanvas(options)).rejects.toMatchObject({ status: 409 });
    expect(client.createProject).not.toHaveBeenCalled();
    expect(client.replaceNodes).not.toHaveBeenCalled();
  });

  it('a late save cannot mutate a different session UI or old session store', async () => {
    baseline();
    const { client, options } = fixture();
    let active = true;
    options.isCurrent = () => active;
    vi.mocked(client.replaceNodes).mockImplementation(async (_id, records) => {
      active = false;
      useCanvasStore.setState({ nodesBySession: { 'session-A': [node], 'session-B': [{ ...node, id: 'B' }] }, selectedNodeIds: ['B'] });
      return { ok: true, count: records.length };
    });
    await persistCapturedImageToCanvas(options);
    expect(useCanvasStore.getState().getNodes('session-A')).toEqual([node]);
    expect(useCanvasStore.getState().getNodes('session-B')[0].id).toBe('B');
    expect(useCanvasStore.getState().selectedNodeIds).toEqual(['B']);
  });

  it('preserves later local edits and advances only the committed saved hash', async () => {
    baseline();
    const { client, options } = fixture();
    vi.mocked(client.replaceNodes).mockImplementation(async (_id, records) => {
      useCanvasStore.setState({ nodesBySession: { 'session-A': [{ ...node, text: 'typed during upload' }] } });
      return { ok: true, count: records.length };
    });
    await persistCapturedImageToCanvas(options);
    expect(useCanvasStore.getState().getNodes('session-A')[0].text).toBe('typed during upload');
    expect(useCanvasStore.getState().getNodes('session-A')).toHaveLength(2);
    expect(useCanvasStore.getState()._savedHashBySession['session-A']).not.toBe(computeNodesHash(useCanvasStore.getState().getNodes('session-A')));
    expect(useCanvasStore.getState()._savedRevisionBySession['session-A']).toBe(4);
  });

  it('does not overwrite a baseline changed by another loader during the save', async () => {
    baseline();
    const { client, options } = fixture();
    const changed = { ...node, text: 'reloaded state' };
    vi.mocked(client.replaceNodes).mockImplementation(async (_id, records) => {
      baseline([changed], 9);
      return { ok: true, count: records.length };
    });
    await persistCapturedImageToCanvas(options);
    expect(useCanvasStore.getState().getNodes('session-A')).toEqual([changed]);
    expect(useCanvasStore.getState()._savedRevisionBySession['session-A']).toBe(9);
    expect(useCanvasStore.getState()._undoStackBySession['session-A']).toBeUndefined();
  });

  it.each([{ taskId: 'other-task' }, { imageUrl: '/other-image' }, { captureId: 'other-capture' }])('rejects capture identity mismatches %j', async (change) => {
    baseline();
    const { client, options } = fixture(project([node, { ...savedCapture, ...change }]));
    await expect(persistCapturedImageToCanvas(options)).rejects.toMatchObject({ status: 409 });
    expect(client.replaceNodes).not.toHaveBeenCalled();
  });

  it.each(['unknown field', 'unknown type', 'corrupt data'])('does not erase an unsupported server record: %s', async (kind) => {
    baseline();
    const server = project();
    if (kind === 'unknown field') server.nodes![0].data = JSON.stringify({ text: 'saved work', future: { retained: true } });
    if (kind === 'unknown type') server.nodes![0].type = 'future-node';
    if (kind === 'corrupt data') server.nodes![0].data = '{';
    const { client, options } = fixture(server);
    await expect(persistCapturedImageToCanvas(options)).rejects.toMatchObject({ status: 409 });
    expect(client.replaceNodes).not.toHaveBeenCalled();
    expect(useCanvasStore.getState().getNodes('session-A')).toEqual([node]);
  });

  describe('canvas frame (#246)', () => {
    beforeEach(() => useCanvasStore.setState({ canvasSizeBySession: {}, _canvasSizeDirtyBySession: {} }));

    it('saves a capture on a new unsized canvas with the initial size and never scales its pixel nodes', async () => {
      baseline();
      const { client, options } = fixture({ ...project(), frame: null, coordinateVersion: 2 });
      await persistCapturedImageToCanvas(options);
      const [, records, revision, , frame] = vi.mocked(client.replaceNodes).mock.calls[0];
      expect(revision).toBe(3);
      expect(frame).toEqual({ width: 1536, height: 1024, coordinateVersion: 2 });
      expect(records[0]).toMatchObject(nodeToRecord(node));
      // Placed at its own pixels in the 1536x1024 canvas centred on the anchor (600, 400).
      expect(records[1]).toMatchObject({ x: 200, y: 100, width: 800, height: 600 });
      const state = useCanvasStore.getState();
      expect(state.canvasSizeBySession['session-A']).toEqual({ width: 1536, height: 1024 });
      expect(state._canvasSizeDirtyBySession['session-A']).toBe(false);
      expect(state._savedRevisionBySession['session-A']).toBe(4);
    });

    it('migrates a legacy canvas once before appending, so the board does not scale the capture again', async () => {
      baseline();
      const { client, options } = fixture({ ...project(), frame: null, coordinateVersion: null });
      await persistCapturedImageToCanvas(options);
      const [, records, , , frame] = vi.mocked(client.replaceNodes).mock.calls[0];
      expect(frame).toEqual({ width: 1536, height: 1024, coordinateVersion: 2 });
      // Legacy frame for 3:2 is 512 units wide; the pixel frame is 1536: everything scales by 3 around (600, 400).
      expect(records[0]).toMatchObject({ id: 'existing', x: 600 + (10 - 600) * 3, y: 400 + (20 - 400) * 3, width: 600, height: 300 });
      expect(JSON.parse(records[0].data).fontSize).toBe(72);
      expect(records[1]).toMatchObject({ id: 'capture-capture-A', width: 800, height: 600 });
      const stored = useCanvasStore.getState();
      expect(stored.nodesBySession['session-A']?.find((entry) => entry.id === 'existing')?.width).toBe(600);
      expect(stored.canvasSizeBySession['session-A']).toEqual({ width: 1536, height: 1024 });
    });

    it('keeps a sized canvas and sends its frame with the append', async () => {
      baseline();
      const { client, options } = fixture();
      await persistCapturedImageToCanvas(options);
      const [, records, , , frame] = vi.mocked(client.replaceNodes).mock.calls[0];
      expect(frame).toEqual({ width: 2048, height: 2048, coordinateVersion: 2 });
      expect(records[0]).toMatchObject(nodeToRecord(node));
      expect(useCanvasStore.getState().canvasSizeBySession['session-A']).toEqual({ width: 2048, height: 2048 });
    });

    it('rebases onto a canvas another page migrated meanwhile without scaling it a second time', async () => {
      baseline();
      const legacy = { ...project(), frame: null, coordinateVersion: null };
      // Another page migrated the same legacy canvas into 1536x1024 and saved revision 4 first.
      const migratedNode = { ...node, x: -1170, y: -740, width: 600, height: 300, fontSize: 72 };
      const migrated = { ...project([migratedNode], 4), frame: { width: 1536, height: 1024, coordinateVersion: 2 }, coordinateVersion: 2 };
      const { client, options } = fixture(legacy);
      vi.mocked(client.replaceNodes).mockRejectedValueOnce(new CanvasBrowserError(409, 'conflict'));
      vi.mocked(client.getProject).mockResolvedValueOnce(legacy).mockResolvedValueOnce(migrated);
      await persistCapturedImageToCanvas(options);
      expect(client.replaceNodes).toHaveBeenCalledTimes(2);
      const [, records, revision, , frame] = vi.mocked(client.replaceNodes).mock.calls[1];
      expect(revision).toBe(4);
      expect(frame).toEqual({ width: 1536, height: 1024, coordinateVersion: 2 });
      // The migrated server node is appended to as it is: not scaled again.
      expect(records[0]).toMatchObject({ id: 'existing', x: -1170, y: -740, width: 600, height: 300 });
      expect(records.map((entry) => entry.id)).toEqual(['existing', 'capture-capture-A']);
      expect(useCanvasStore.getState().canvasSizeBySession['session-A']).toEqual({ width: 1536, height: 1024 });
      expect(useCanvasStore.getState()._savedRevisionBySession['session-A']).toBe(5);
    });

    it('keeps the old baseline when legacy-coordinate edits arrive while a migrating save is in flight', async () => {
      baseline();
      const { client, options } = fixture({ ...project(), frame: null, coordinateVersion: null });
      const typed = { ...node, text: 'typed on the old page' };
      vi.mocked(client.replaceNodes).mockImplementation(async (_id, records) => {
        useCanvasStore.setState({ nodesBySession: { 'session-A': [typed] } });
        return { ok: true, count: records.length };
      });
      await persistCapturedImageToCanvas(options);
      const state = useCanvasStore.getState();
      // Legacy-unit edits are not merged into migrated nodes, and nothing claims they were saved: the board's
      // next save conflicts on the old revision and reloads the migrated canvas.
      expect(state.getNodes('session-A')).toEqual([typed]);
      expect(state._savedHashBySession['session-A']).toBe(computeNodesHash([node]));
      expect(state._savedRevisionBySession['session-A']).toBe(3);
      expect(state.canvasSizeBySession['session-A']).toBeUndefined();
    });

    it('saves a pending local resize together with the capture', async () => {
      baseline();
      useCanvasStore.setState({ canvasSizeBySession: { 'session-A': { width: 1024, height: 1024 } }, _canvasSizeDirtyBySession: { 'session-A': true } });
      const { client, options } = fixture();
      await persistCapturedImageToCanvas(options);
      const [, , , , frame] = vi.mocked(client.replaceNodes).mock.calls[0];
      expect(frame).toEqual({ width: 1024, height: 1024, coordinateVersion: 2 });
      expect(useCanvasStore.getState()._canvasSizeDirtyBySession['session-A']).toBe(false);
    });

    it('adopts the size the capture saved when the local size had nothing pending (another window resized)', async () => {
      baseline();
      // This page last saw 1024x1024; another window resized the canvas to 2048x2048 on the server.
      useCanvasStore.setState({ canvasSizeBySession: { 'session-A': { width: 1024, height: 1024 } }, _canvasSizeDirtyBySession: { 'session-A': false } });
      const { client, options } = fixture();
      await persistCapturedImageToCanvas(options);
      const [, , , , frame] = vi.mocked(client.replaceNodes).mock.calls[0];
      expect(frame).toEqual({ width: 2048, height: 2048, coordinateVersion: 2 });
      // The saved frame is now the canvas: a later board save must not write 1024x1024 back over it.
      const state = useCanvasStore.getState();
      expect(state.canvasSizeBySession['session-A']).toEqual({ width: 2048, height: 2048 });
      expect(state._canvasSizeDirtyBySession['session-A']).toBe(false);
    });

    it('keeps a newer local resize made while the capture save was in flight', async () => {
      baseline();
      useCanvasStore.setState({ canvasSizeBySession: { 'session-A': { width: 1024, height: 1024 } }, _canvasSizeDirtyBySession: { 'session-A': true } });
      const { client, options } = fixture();
      vi.mocked(client.replaceNodes).mockImplementation(async (_id, records) => {
        useCanvasStore.getState().resizeCanvas('session-A', { width: 512, height: 512 }, { anchor: 'center', center: { x: 600, y: 400 } });
        return { ok: true, count: records.length };
      });
      await persistCapturedImageToCanvas(options);
      const state = useCanvasStore.getState();
      expect(state.canvasSizeBySession['session-A']).toEqual({ width: 512, height: 512 });
      expect(state._canvasSizeDirtyBySession['session-A']).toBe(true);
    });
  });
});
