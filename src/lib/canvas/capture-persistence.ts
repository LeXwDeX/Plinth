import { formatSchemaText, type CanvasProductSchema } from '@/lib/product-schema';
import { useCanvasStore, type CanvasNode } from '@/store/canvas-store';
import { canvasArtboard, samePixels, type CanvasPixels } from './artboard';
import { CanvasBitmapLimitError } from './bitmap-limit';
import { CanvasBrowserError, type CanvasBrowserClient, type CanvasProject, type CanvasNodeInput } from './browser-client';
import { resolveCanvasFrame } from './canvas-frame';
import { imageNodePlacement } from './image-placement';
import { computeNodesHash, nodeToRecord, recordToNode } from './node-persistence';
import { enqueueSessionCanvasSave } from './session-persistence';

export interface PersistCapturedImageOptions {
  client: CanvasBrowserClient;
  schema: CanvasProductSchema;
  ownerID: string;
  sessionID: string;
  taskID: string;
  captureID: string;
  imageURL: string;
  naturalSize: { width: number; height: number };
  /**
   * #246: the size a canvas without one starts with (what the chosen 2D model produces). The capture is
   * placed in pixel coordinates, so the save always carries the canvas frame.
   */
  initialSize: CanvasPixels;
  isCurrent: () => boolean;
}

function conflict(message: string): never {
  throw new CanvasBrowserError(409, message);
}

function current(options: PersistCapturedImageOptions): void {
  if (!options.isCurrent()) conflict('capture scope changed');
}

function validateProject(project: CanvasProject, options: PersistCapturedImageOptions, expectedID?: string): void {
  if (!project.id || project.userId !== options.ownerID || project.sessionId !== options.sessionID ||
      (expectedID !== undefined && project.id !== expectedID) ||
      !Number.isSafeInteger(project.revision) || (project.revision ?? 0) < 1) {
    conflict('invalid capture canvas project');
  }
}

function validateFullProject(project: CanvasProject, options: PersistCapturedImageOptions, expectedID: string): void {
  validateProject(project, options, expectedID);
  if (!Array.isArray(project.nodes) || project.nodes.some((node) => node.projectId !== expectedID)) {
    conflict('invalid capture canvas project nodes');
  }
}

function canonicalJSON(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(',')}]`;
  if (value !== null && typeof value === 'object') {
    return `{${Object.entries(value).sort(([a], [b]) => a.localeCompare(b))
      .map(([key, entry]) => `${JSON.stringify(key)}:${canonicalJSON(entry)}`).join(',')}}`;
  }
  return JSON.stringify(value);
}

/** Refuse unsupported records before a bulk save can erase their information. */
function restoreNodes(records: CanvasNodeInput[]): CanvasNode[] {
  const ids = new Set<string>();
  return records.map((record) => {
    if (!record.id || ids.has(record.id) || !['image', 'text', 'frame', 'shape', 'pencil'].includes(record.type)) {
      conflict('unsupported capture canvas node');
    }
    ids.add(record.id);
    let data: unknown;
    try { data = JSON.parse(record.data || '{}'); } catch { conflict('invalid capture canvas node data'); }
    if (data === null || typeof data !== 'object' || Array.isArray(data)) conflict('invalid capture canvas node data');
    const node = recordToNode(record);
    if (!node || canonicalJSON(data) !== canonicalJSON(JSON.parse(nodeToRecord(node).data))) {
      conflict('capture canvas node cannot be preserved');
    }
    return node;
  });
}

function existingCapture(nodes: CanvasNode[], options: PersistCapturedImageOptions): CanvasNode | undefined {
  const matches = nodes.filter((node) => node.id === `capture-${options.captureID}` || node.captureId === options.captureID);
  if (matches.length > 1) conflict('duplicate capture canvas identity');
  const node = matches[0];
  if (node && (node.type !== 'image' || node.captureId !== options.captureID ||
      node.taskId !== options.taskID || node.imageUrl !== options.imageURL)) {
    conflict('capture canvas identity mismatch');
  }
  return node;
}

export function persistCapturedImageToCanvas(options: PersistCapturedImageOptions): Promise<void> {
  return enqueueSessionCanvasSave(options.sessionID, async () => {
    const { client, sessionID } = options;
    if (!options.ownerID || !sessionID || !options.captureID || !options.taskID || !options.imageURL ||
        !Number.isFinite(options.naturalSize.width) || !Number.isFinite(options.naturalSize.height) ||
        options.naturalSize.width <= 0 || options.naturalSize.height <= 0) {
      throw new Error('invalid captured image');
    }
    current(options);
    const knownProject = useCanvasStore.getState().getProjectId(sessionID);
    const canonical = await client.createProject(undefined, sessionID);
    validateProject(canonical, options, knownProject);
    let project = await client.getProject(canonical.id);
    validateFullProject(project, options, canonical.id);

    for (let attempt = 0; attempt < 2; attempt += 1) {
      current(options);
      const store = useCanvasStore.getState();
      const localNodes = store.getNodes(sessionID);
      const localHash = computeNodesHash(localNodes);
      const savedHash = store._savedHashBySession[sessionID];
      const savedRevision = store._savedRevisionBySession[sessionID];
      const projectID = store.getProjectId(sessionID);
      if (projectID !== knownProject) conflict('capture canvas project changed');
      const dirty = savedHash === undefined ? localNodes.length > 0 : localHash !== savedHash;
      if (dirty && (projectID !== project.id || savedRevision !== project.revision)) {
        conflict('capture canvas dirty baseline changed');
      }
      const serverRecords = project.nodes ?? [];
      // #246: resolve the frame exactly as the board does. A legacy canvas (no version) is migrated before the
      // capture is appended, and the save carries the frame, so the board never scales these nodes again.
      const localSize = store.canvasSizeBySession[sessionID];
      const server = resolveCanvasFrame(options.schema, project, restoreNodes(serverRecords),
        project.frame ? project.frame : localSize ?? options.initialSize);
      const serverNodes = server.nodes;
      // Local nodes are pixels once the board knew a size; before that they mirror the server's coordinates.
      const local = localSize || !server.migrated ? localNodes
        : resolveCanvasFrame(options.schema, project, localNodes, server.size).nodes;
      // A resize not saved yet is the canvas the person sees; it is saved with the capture.
      const size = localSize && store._canvasSizeDirtyBySession[sessionID] ? localSize : server.size;
      const baseNodes = dirty ? local : serverNodes;
      const found = existingCapture(baseNodes, options);
      // A local edit must not silently change a persisted capture's provenance.
      existingCapture(serverNodes, options);
      const captureNode: CanvasNode = found ?? {
        id: `capture-${options.captureID}`, type: 'image', imageUrl: options.imageURL,
        taskId: options.taskID, captureId: options.captureID,
        ...imageNodePlacement(baseNodes, options.naturalSize, options.schema.frontend, canvasArtboard(options.schema.frontend.artboard, size)),
        zIndex: (baseNodes.length ? Math.max(...baseNodes.map((node) => node.zIndex)) : 0) + 1,
      };
      const writtenNodes = found ? baseNodes : [...baseNodes, captureNode];
      // #149: the server refuses a canvas above its picture limit; say so before sending the save.
      const limit = options.schema.limits.maximum_bitmap_nodes;
      const pictures = writtenNodes.filter((node) => node.type === 'image').length;
      if (!found && pictures > limit) throw new CanvasBitmapLimitError(pictures - 1, limit);
      // Clean appends retain the original record bytes and all stored node fields (a migrated canvas cannot).
      const records = dirty || server.migrated ? writtenNodes.map(nodeToRecord) : [
        ...serverRecords,
        ...(found ? [] : [nodeToRecord(captureNode)]),
      ];
      try {
        if (!found || dirty || server.initialized) {
          current(options);
          const result = await client.replaceNodes(project.id, records, project.revision, undefined, {
            ...size, coordinateVersion: options.schema.defaults.coordinate_version,
          });
          if (!result.ok || result.count !== records.length) throw new Error('capture canvas save was not acknowledged');
        }
      } catch (error) {
        // Another window filled the canvas meanwhile: the server's own refusal reads the same way (#149).
        if (error instanceof CanvasBrowserError && error.status === 400 &&
            error.message === formatSchemaText(options.schema.presentation.messages.bitmap_limit_exceeded, { limit })) {
          throw new CanvasBitmapLimitError(limit, limit);
        }
        if (!(error instanceof CanvasBrowserError) || error.status !== 409 || dirty || attempt > 0) throw error;
        // Only a clean append can rebase once. New local edits retain their old
        // baseline and must be handled by the normal canvas conflict flow.
        if (computeNodesHash(useCanvasStore.getState().getNodes(sessionID)) !== localHash) throw error;
        current(options);
        project = await client.getProject(project.id);
        validateFullProject(project, options, canonical.id);
        continue;
      }
      if (!options.isCurrent()) return;
      let addCapture = false;
      useCanvasStore.setState((latest) => {
        if (!options.isCurrent() || latest.getProjectId(sessionID) !== projectID ||
            latest._savedHashBySession[sessionID] !== savedHash ||
            latest._savedRevisionBySession[sessionID] !== savedRevision) return latest;
        const latestNodes = latest.getNodes(sessionID);
        let nextNodes = writtenNodes;
        // Edits made meanwhile on legacy coordinates cannot be merged into migrated ones: leave the baseline,
        // so the board's next save conflicts and reloads the migrated canvas.
        if (server.migrated && !localSize && computeNodesHash(latestNodes) !== localHash) return latest;
        if (computeNodesHash(latestNodes) !== localHash) {
          const originalIDs = new Set(localNodes.map((node) => node.id));
          // Keep edits made during the request, including deletions, and append
          // server-only nodes plus the committed capture to their session.
          nextNodes = [...latestNodes, ...writtenNodes.filter((node) =>
            !originalIDs.has(node.id) && !latestNodes.some((entry) => entry.id === node.id))];
          existingCapture(nextNodes, options);
        }
        // Reuse the store's add action so capture has the same undo behavior as
        // ordinary image placement. Replays must not create another undo entry.
        addCapture = !found && !existingCapture(latestNodes, options);
        if (addCapture) nextNodes = nextNodes.filter((node) => node.id !== captureNode.id);
        return {
          nodesBySession: { ...latest.nodesBySession, [sessionID]: nextNodes },
          projectBySession: { ...latest.projectBySession, [sessionID]: project.id },
          _savedHashBySession: { ...latest._savedHashBySession, [sessionID]: computeNodesHash(writtenNodes) },
          _savedRevisionBySession: { ...latest._savedRevisionBySession, [sessionID]: (project.revision ?? 0) + (!found || dirty || server.initialized ? 1 : 0) },
          // The saved frame is the session's canvas size, also when it came from the server (another window
          // resized) and nothing was pending here; only a newer unsaved local size stays pending.
          ...(!latest._canvasSizeDirtyBySession[sessionID] || samePixels(latest.canvasSizeBySession[sessionID], size) ||
              !latest.canvasSizeBySession[sessionID] ? {
            canvasSizeBySession: { ...latest.canvasSizeBySession, [sessionID]: size },
            _canvasSizeDirtyBySession: { ...latest._canvasSizeDirtyBySession, [sessionID]: false },
          } : {}),
        };
      });
      if (addCapture) useCanvasStore.getState().addNode(sessionID, captureNode);
      return;
    }
  });
}
