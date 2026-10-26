'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { toast } from 'sonner';
import { useShallow } from 'zustand/react/shallow';

import { CanvasBoard } from '@/components/canvas/canvas-board';
import { CanvasSizeNotice } from '@/components/canvas/canvas-size-notice';
import { focusChatComposer } from '@/lib/workspace/chat-composer';
import {
  BackgroundRemovalDialog,
  CanvasImageActionBar,
  InpaintDialog,
  type InpaintRequest,
} from '@/components/workspace/canvas-image-actions';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import { useCanvasArtboard } from '@/hooks/use-canvas-artboard';
import { Action } from '@/components/ui/button';
import { useProductSchema } from '@/components/product-schema-provider';
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useStageViewer } from '@/hooks/use-stage-viewer';
import { publishBoardDelivery, rasteriseSessionBoard } from '@/lib/canvas/board-delivery';
import { artboardIsFree, canvasNodeSize } from '@/lib/canvas/image-placement';
import { uploadPlacement, type UploadRect } from '@/lib/canvas/upload-placement';
import { InpaintSizeMismatchError, readInpaintJobs, removeInpaintJob, renderInpaintLayer } from '@/lib/canvas/inpaint-layer';
import { removeBackgroundTarget } from '@/lib/canvas/remove-background-target';
import { currentObjectLayer, inpaintEntry } from '@/lib/canvas/inpaint-entry';
import { layerBarShown, selectedImageLayer } from '@/lib/canvas/selected-layer';
import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { isInpaintSource, maskAssetIdFromURL } from '@/lib/canvas/inpaint-source';
import {
  CanvasBrowserError,
  createCanvasBrowserClient,
  type CanvasFrame,
} from '@/lib/canvas/browser-client';
import { resolveCanvasFrame } from '@/lib/canvas/canvas-frame';
import { canvasArtboard } from '@/lib/canvas/artboard';
import { initialCanvasNodes } from '@/lib/canvas/initial-layer';
import { settingsAfterModelSwitch } from '@/lib/workspace/canvas-size';
import { artboardContent, exportArtboardToBlob } from '@/lib/canvas/export-canvas';
import { chooseClipboardPaste, pasteAnchor } from '@/lib/canvas/clipboard';
import { flushPendingEdits, localBitmapNodeIds } from '@/lib/canvas/pending-edits';
import { exportAndUploadSnapshot, reportSnapshotFailure, saveBoardEdits, snapshotFilename } from '@/lib/canvas/send-snapshot';
import { imageNodePlacement } from '@/lib/canvas/image-placement';
import { exportBoardFilename } from '@/lib/canvas/export-filename';
import { dropNodeRect, dropPointToWorld } from '@/lib/canvas/drop-placement';
import { dataURIFile, decodeCanvasDrag, hasCanvasDrag, CANVAS_ITEM_MIME } from '@/lib/workspace/canvas-drag';
import { computeNodesHash, nodeToRecord, recordToNode } from '@/lib/canvas/node-persistence';
import { boardAutosaveTrigger, conflictReloadApplies } from '@/lib/canvas/board-autosave';
import { beginSessionCanvasCreate, enqueueSessionCanvasSave, finishSessionCanvasCreate, waitForSessionCanvasSaves } from '@/lib/canvas/session-persistence';
import { canRestoreFirstCanvasImage, firstCanvasImage } from '@/lib/canvas/first-open';
import { triggerBlobDownload } from '@/lib/canvas/compliant-export';
import { ImageUploadError, imageUploadAccept, prepareImageUploadBlob } from '@/lib/image-upload';
import { schemaMessage, schemaText } from '@/lib/product-schema';
import { mergedObjectId } from '@/lib/workspace/merge-version';
import { reconcileObjectWithNodes } from '@/lib/workspace/object-sync';
import { MERGED_FROM_CAPTURE } from '@/lib/workspace/versions';
import { inpaintParameters } from '@/lib/workspace/model-picker';
import { uploadWorkspaceInputAsset } from '@/lib/workspace/input-asset-upload';
import { formatSizeLimit } from '@/lib/size-text';
import { captureSessionScope, isCurrentSessionScope } from '@/lib/workspace/session-scope';
import { fitRectViewport, viewportUnchanged, type FitRect } from '@/lib/canvas/viewport';
import { objectFitStep, type ObjectFitState } from '@/lib/canvas/object-fit';
import { stageFitInsets } from '@/lib/workspace/tool-rail-layout';
import { useCanvasStore, generateId, type CanvasNode, type CanvasViewport } from '@/store/canvas-store';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useMaskStore } from '@/store/mask-store';
import { handleOfObject } from '@/lib/workspace/object-handle';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';
import { imagesToPlace } from '@/lib/workspace/canvas-placement';
import { planAutoJoin, resultKey, withPlaced } from '@/lib/workspace/canvas-auto-join';
import { useSessionVersions } from '@/hooks/use-session-versions';
import type { VersionTile } from '@/lib/workspace/versions';

const EMPTY_NODES: CanvasNode[] = [];

/** Pictures the user can see; the unpainted initial layer is not content (#312). */
function imageLayerCount(nodes: readonly CanvasNode[]): number {
  return nodes.filter((node) => node.type === 'image' && node.blank !== true).length;
}

/** Tools that draw on the picture: a result that arrives meanwhile must not pull the stage away. */
const PAINT_TOOLS = new Set(['pencil', 'brush', 'eraser', 'mask', 'crop', 'shape', 'text']);

function loadImageElement(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.crossOrigin = 'anonymous';
    image.onload = () => resolve(image);
    image.onerror = () => reject(new Error('image load failed'));
    image.src = url;
  });
}

function loadImageSize(url: string): Promise<{ width: number; height: number }> {
  return new Promise((resolve) => {
    const image = new Image();
    image.onload = () => resolve({ width: image.naturalWidth, height: image.naturalHeight });
    image.onerror = () => resolve({ width: 0, height: 0 });
    image.src = url;
  });
}

export function WorkspaceCanvasBoard() {
  const { schema } = useProductSchema();
  const { proxyAssetUrl } = useAssetURLRewriter();
  const { openStageLightbox, current: stageCurrent } = useStageViewer();
  // A Space tap opens or closes the large view when there is one to open or close
  // (see useStudioShortcuts); only otherwise does it recenter the canvas.
  const stageCurrentRef = useRef(stageCurrent);
  stageCurrentRef.current = stageCurrent;
  const spaceTapTaken = useCallback(
    () => Boolean(useStudioStore.getState().lightbox) || stageCurrentRef.current?.kind === 'image',
    [],
  );
  const sessionID = useChatStore((state) => state.activeSessionId);
  const selection = useWorkspaceStore((state) => state.currentObject);
  const setCurrentObject = useWorkspaceStore((state) => state.setCurrentObject);
  const activeTool = useCanvasStore((state) => state.activeTool);
  const setActiveTool = useCanvasStore((state) => state.setActiveTool);
  const selectedNodeIds = useCanvasStore((state) => state.selectedNodeIds);
  const removeNode = useCanvasStore((state) => state.removeNode);
  const [inpaintRequest, setInpaintRequest] = useState<InpaintRequest | null>(null);
  const [bgTarget, setBgTarget] = useState<string | null>(null);
  const modelByDomain = useWorkspaceStore((state) => state.modelByDomain);
  const setModel = useWorkspaceStore((state) => state.setModel);
  const { offeringsByCapability } = useWorkspaceOfferings(schema, 'image');
  // 虚线画板 = 画布（#246）：像素尺寸随画布项目保存，世界坐标 1 单位 = 1 像素；导出与发给模型的图片都是这个尺寸。
  const canvasDelivery = useCanvasArtboard(sessionID);
  const { artboard, pixels: deliveryPixels } = canvasDelivery;
  // Actions that live elsewhere (the result detail's 转 3D) rasterise the same frame through this registry.
  useEffect(() => {
    if (!sessionID) return;
    publishBoardDelivery(sessionID, artboard ? { artboard, pixels: deliveryPixels } : null);
    return () => publishBoardDelivery(sessionID, null);
  }, [artboard, deliveryPixels, sessionID]);
  // 蒙版局部重绘按当前所选模型的 Offering 参数声明判断（不写死模型名）：
  // 三个输入语义齐全才可用；否则工具条按钮变暗，点击弹确认切换。
  const maskOffering = useMemo(
    () =>
      (offeringsByCapability?.['image-to-image'] ?? []).find(
        (item) =>
          item.available &&
          item.model_id === modelByDomain.image &&
           inpaintParameters(item) !== null,
      ),
    [offeringsByCapability, modelByDomain.image],
  );
  const maskSupported = Boolean(maskOffering);
  const maskCandidates = useMemo(
    () =>
      (offeringsByCapability?.['image-to-image'] ?? []).filter(
        (item) =>
          item.available &&
           inpaintParameters(item) !== null,
      ),
    [offeringsByCapability],
  );
  const maskSwitchTarget = maskCandidates[0];
  const currentModelName = useMemo(
    () =>
      Object.values(offeringsByCapability ?? {})
        .flat()
        .find((item) => item.available && item.model_id === modelByDomain.image)
        ?.model_name ?? '',
    [offeringsByCapability, modelByDomain.image],
  );
  const [maskSwitchOpen, setMaskSwitchOpen] = useState(false);
  const nodes = useCanvasStore((state) => state.nodesBySession[sessionID ?? ''] ?? EMPTY_NODES);
  const [loadedSessionID, setLoadedSessionID] = useState<string | null>(null);
  // The project has been read; the board shows once its pixel size is resolved (#246: a new or legacy
  // canvas is sized from the chosen 2D model, so that waits for the model's settings).
  const [fetchedSessionID, setFetchedSessionID] = useState<string | null>(null);
  // The coordinate version each fetched project reported (null: a legacy canvas to migrate once).
  const fetchedVersion = useRef<Record<string, number | null>>({});
  const [projectID, setProjectID] = useState<string | null>(null);
  const uploadInput = useRef<HTMLInputElement>(null);
  const boardContainer = useRef<HTMLDivElement>(null);
  const [cardOver, setCardOver] = useState(false);
  const drawingArea = useRef<HTMLDivElement>(null);
  const fittedImageCountBySession = useRef<Record<string, number>>({});
  // A persisted viewport belongs to the user: restored pictures must not trigger the arrival fit (#312).
  // Only a session without one gets the first-content fit, as before.
  const viewportAtLoad = useRef<Record<string, boolean>>({});
  const hasObjectLayer = useRef(false);
  const pendingSave = useRef<{ sessionID: string; flush: () => void } | null>(null);
  // The loaded board's save function together with its session: a job that finished after a session switch
  // must never flush the newly shown canvas, or a receipt for the old one would be dropped unpersisted.
  const flushSave = useRef<{ sessionID: string; flush: () => Promise<boolean> }>({
    sessionID: '', flush: async () => false,
  });
  const firstOpen = useRef(new Map<string, { projectID: string; nodesHash?: string }>());
  const client = useMemo(() => createCanvasBrowserClient({
    schema: schema.canvas,
    routes: {
      projects: schema.runtime.http.legacy_routes.canvas_projects,
      project: schema.runtime.http.legacy_routes.canvas_project,
      nodes: schema.runtime.http.legacy_routes.canvas_nodes,
      merge: schema.runtime.http.legacy_routes.canvas_merge,
    },
  }), [schema]);
  const ui = useMemo(() => ({
    loading: schemaText(schema, 'ui.workspace.harness.loading'),
    loadFailed: schemaText(schema, 'ui.workspace.history.load_failed'),
    saveFailed: schemaText(schema, 'ui.workspace.harness.save_failed'),
    conflict: schemaText(schema, 'ui.workspace.canvas.conflict'),
    exportFailed: schemaText(schema, 'ui.workspace.canvas.export_failed'),
    inpaintBoardEmpty: schemaText(schema, 'ui.studio.stage.action.inpaint_board_empty'),
    removeBackgroundNoLayer: schemaText(schema, 'ui.studio.stage.action.remove_background_no_layer'),
    uploadFailed: schemaText(schema, 'ui.workspace.canvas.upload_failed'),
    mergeDone: schemaText(schema, 'ui.studio.merge.done'),
    mergeFailed: schemaText(schema, 'ui.studio.merge.failed'),
    dropHint: schemaText(schema, 'ui.studio.canvas.drop_hint'),
    historyReloaded: schemaText(schema, 'ui.canvas.history.reloaded'),
  }), [schema]);

  useEffect(() => {
    if (!sessionID) return;
    let cancelled = false;
    viewportAtLoad.current[sessionID] = Boolean(useCanvasStore.getState().viewportBySession[sessionID]);
    setLoadedSessionID(null);
    setFetchedSessionID(null);
    setProjectID(null);
    const creation = beginSessionCanvasCreate(sessionID, () => client.createProject(undefined, sessionID));
    creation
      .then(async (created) => {
        // 切回同一会话时，先等上一次已发出的保存结束，避免加载到旧
        // revision 后覆盖刚保存的节点与乐观锁基线。
        await waitForSessionCanvasSaves(sessionID);
        return cancelled ? null : { created, project: await client.getProject(created.id) };
      })
      .then((loaded) => {
        if (cancelled || !loaded) return;
        const { created, project } = loaded;
        const restored = (project.nodes ?? []).flatMap((record) => {
          const node = recordToNode(record);
          return node ? [node] : [];
        });
        const store = useCanvasStore.getState();
        const cached = store.getNodes(sessionID);
        const savedHash = store._savedHashBySession[sessionID];
        const hasUnsavedLocalChanges = savedHash !== undefined && computeNodesHash(cached) !== savedHash;
        const localDirty = hasUnsavedLocalChanges || Boolean(store._placedDirtyBySession[sessionID]) ||
          Boolean(store._canvasSizeDirtyBySession[sessionID]) || localBitmapNodeIds(sessionID).size > 0;
        if (canRestoreFirstCanvasImage(created, project, cached.length, localDirty)) {
          firstOpen.current.set(sessionID, { projectID: project.id });
        } else firstOpen.current.delete(sessionID);
        finishSessionCanvasCreate(sessionID, creation);
        if (!hasUnsavedLocalChanges) {
          store.loadNodesFromDB(sessionID, restored);
          store._setSavedHash(sessionID, computeNodesHash(restored));
          store._setSavedRevision(sessionID, project.revision ?? 0);
        }
        // 已放置结果清单（#239/#262）：本地没有待保存的登记时以服务端为准。
        if (!store._placedDirtyBySession[sessionID]) store.loadPlacedResults(sessionID, project.placedResults ?? null);
        // 画布像素尺寸（#246）：本地没有待保存的尺寸时以服务端为准；null（旧画布）由下方一次性迁移。
        if (!store._canvasSizeDirtyBySession[sessionID]) store.loadCanvasSize(sessionID, project.frame ?? null);
        // 本地有未保存改动时保留该 session 的 revision 基线；服务端
        // 有更新会在保存时返回 409，避免静默覆盖其他窗口。
        store.setProjectForSession(sessionID, project.id);
        store.setBgStyleForSession(sessionID, project.bgStyle);
        fetchedVersion.current[sessionID] = project.coordinateVersion ?? null;
        setProjectID(project.id);
        setFetchedSessionID(sessionID);
      })
      .catch(() => {
        if (!cancelled) toast.error(ui.loadFailed);
      });
    return () => { cancelled = true; };
  }, [client, sessionID, ui.loadFailed]);

  /**
   * #246: a canvas without a saved pixel size gets one before the board shows. A new canvas starts at the
   * size the chosen 2D model produces (the Schema default without one). A legacy canvas (saved before
   * coordinate version 2) is migrated once: its old 512-unit frame becomes the pixel frame around the same
   * centre and every node scales with it, then the save below records the size and version.
   */
  const settled = canvasDelivery.settled;
  const initialWidth = canvasDelivery.initial.width;
  const initialHeight = canvasDelivery.initial.height;
  useEffect(() => {
    if (!sessionID || fetchedSessionID !== sessionID || loadedSessionID === sessionID) return;
    const store = useCanvasStore.getState();
    if (!store.canvasSizeBySession[sessionID]) {
      if (!settled) return;
      const resolved = resolveCanvasFrame(schema.canvas, { coordinateVersion: fetchedVersion.current[sessionID] },
        store.getNodes(sessionID), { width: initialWidth, height: initialHeight });
      // #246: a brand-new empty canvas starts with its bitmap layer, so the brush works at once.
      const nodes = initialCanvasNodes(resolved, canvasArtboard(schema.canvas.frontend.artboard, resolved.size),
        schema.canvas, schemaText(schema, 'ui.canvas.layers.initial_name'), generateId);
      store.initializeCanvas(sessionID, nodes, resolved.size);
      // The old view framed the old geometry; the board fits the new canvas when it has no view.
      if (resolved.migrated) {
        useCanvasStore.setState((state) => {
          const { [sessionID]: _view, ...rest } = state.viewportBySession;
          return { viewportBySession: rest };
        });
      }
    }
    // Preserve the exact initialized board while history selects its current image. Any intervening edit
    // (including deleting the initial layer) invalidates this one-time restoration.
    const first = firstOpen.current.get(sessionID);
    if (first) first.nodesHash = computeNodesHash(store.getNodes(sessionID));
    // Pictures restored with the project are not arrivals. Without a saved viewport (or when a legacy
    // migration removed it) the first content still gets the existing initial fit below.
    const savedViewport = viewportAtLoad.current[sessionID] &&
      Boolean(useCanvasStore.getState().viewportBySession[sessionID]);
    fittedImageCountBySession.current[sessionID] = savedViewport
      ? imageLayerCount(store.getNodes(sessionID)) : 0;
    setLoadedSessionID(sessionID);
  }, [fetchedSessionID, initialHeight, initialWidth, loadedSessionID, schema, sessionID, settled]);

  /**
   * #246: switching the 2D model keeps the canvas size. When the new model offers that size, its setting
   * follows the canvas; otherwise the notice over the board says what the model will produce instead.
   */
  const fitOffering = canvasDelivery.generation.offering?.id ?? '';
  const seenOffering = useRef<{ sessionId: string | null; offering: string }>({ sessionId: null, offering: '' });
  const setGenerationValue = canvasDelivery.generation.setValue;
  const fit = canvasDelivery.fit;
  useEffect(() => {
    if (!sessionID || loadedSessionID !== sessionID || !settled) return;
    const seen = seenOffering.current;
    seenOffering.current = { sessionId: sessionID, offering: fitOffering };
    const settings = settingsAfterModelSwitch(seen, { sessionId: sessionID, offering: fitOffering }, fit);
    for (const [name, value] of Object.entries(settings ?? {})) setGenerationValue(name, value);
  }, [fit, fitOffering, loadedSessionID, sessionID, setGenerationValue, settled]);

  // Besides the nodes, the save also runs for each new size or registration, even while an earlier one is
  // still unsaved and its dirty flag therefore does not change (#246, see boardAutosaveTrigger).
  const [placedDirty, sizeDirty, pendingWidth, pendingHeight, pendingPlaced] = useCanvasStore(
    useShallow((state) => boardAutosaveTrigger(state, sessionID ?? '')));
  useEffect(() => {
    if (!sessionID || !projectID || loadedSessionID !== sessionID) return;
    const enqueueSave = (): Promise<boolean> => {
      const request = enqueueSessionCanvasSave(sessionID, async (): Promise<boolean> => {
        const store = useCanvasStore.getState();
        const currentNodes = store.getNodes(sessionID);
        const currentHash = computeNodesHash(currentNodes);
        const placedToSave = store.placedResultsBySession[sessionID];
        const sizeToSave = store.canvasSizeBySession[sessionID];
        const sent = { nodesHash: currentHash, size: sizeToSave, placed: placedToSave };
        if (currentHash === store._savedHashBySession[sessionID] && !store._placedDirtyBySession[sessionID] &&
            !store._canvasSizeDirtyBySession[sessionID]) return true;
        if (store.getProjectId(sessionID) !== projectID) return false;
        const revision = store._savedRevisionBySession[sessionID] ?? 0;
        try {
          if (revision < 1) {
            // 基线未知（旧缓存迁移）：无法安全乐观提交，以服务端为准。
            throw new CanvasBrowserError(409, 'unknown baseline');
          }
          // The pixel size always travels with the nodes whose coordinates it gives meaning to.
          const frame: CanvasFrame | undefined = sizeToSave
            ? { ...sizeToSave, coordinateVersion: schema.canvas.defaults.coordinate_version }
            : undefined;
          await client.replaceNodes(
            projectID,
            currentNodes.map(nodeToRecord),
            revision,
            placedToSave ?? undefined,
            frame,
          );
          // 网络请求期间可能已切换会话或重新加载项目；只推进原项目
          // 对应的基线，绝不把 A 的保存结果写入 B。
          const latestStore = useCanvasStore.getState();
          if (latestStore.getProjectId(sessionID) === projectID && latestStore._savedRevisionBySession[sessionID] === revision) {
            latestStore._setSavedHash(sessionID, currentHash);
            latestStore._setSavedRevision(sessionID, revision + 1);
            if (placedToSave) latestStore._markPlacedSaved(sessionID, placedToSave);
            if (sizeToSave) latestStore._markCanvasSizeSaved(sessionID, sizeToSave);
          }
          return true;
        } catch (error) {
          const conflict = error instanceof CanvasBrowserError && error.status === 409;
          if (useChatStore.getState().activeSessionId === sessionID) {
            toast.error(conflict ? ui.conflict : ui.saveFailed);
          }
          // 网络故障保留本地 dirty 节点，用户继续编辑、改尺寸或再次进入会话时可重试。
          // 冲突只在原请求的节点、尺寸与登记仍为当前状态时采用服务端状态，避免异步
          // 重载覆盖请求后新做的编辑、新选的尺寸或已经切走的会话（它们各自的保存会再遇到冲突）。
          if (!conflict || useChatStore.getState().activeSessionId !== sessionID ||
              !conflictReloadApplies(useCanvasStore.getState(), sessionID, sent)) return false;
          try {
            const fresh = await client.getProject(projectID);
            const latestStore = useCanvasStore.getState();
            if (useChatStore.getState().activeSessionId !== sessionID ||
                latestStore.getProjectId(sessionID) !== projectID ||
                !conflictReloadApplies(latestStore, sessionID, sent)) return false;
            const freshNodes = (fresh.nodes ?? []).flatMap((record) => {
              const node = recordToNode(record);
              return node ? [node] : [];
            });
            const freshStore = useCanvasStore.getState();
            // The server's canvas replaces the local one, so older undo records no longer describe it.
            const droppedHistory = freshStore.loadNodesFromDB(sessionID, freshNodes);
            if (droppedHistory) toast.info(ui.historyReloaded);
            freshStore._setSavedHash(sessionID, computeNodesHash(freshNodes));
            freshStore._setSavedRevision(sessionID, fresh.revision ?? 0);
            freshStore.loadPlacedResults(sessionID, fresh.placedResults ?? null);
            // A server copy without a size keeps the local size; a legacy one has its nodes migrated into it.
            const localSize = freshStore.canvasSizeBySession[sessionID];
            if (fresh.frame) freshStore.loadCanvasSize(sessionID, fresh.frame);
            else if (localSize) {
              const resolved = resolveCanvasFrame(schema.canvas, { coordinateVersion: fresh.coordinateVersion }, freshNodes, localSize);
              freshStore.initializeCanvas(sessionID, resolved.nodes, resolved.size);
            }
          } catch {
            // 重载也失败时仅保留提示
          }
          return false;
        }
      });
      return request.catch(() => {
        if (useChatStore.getState().activeSessionId === sessionID) toast.error(ui.saveFailed);
        return false;
      });
    };
    // A board with nothing to save still has to answer a flush: it is already durable.
    flushSave.current = { sessionID, flush: enqueueSave };
    const savedHash = useCanvasStore.getState()._savedHashBySession[sessionID];
    if (computeNodesHash(nodes) === savedHash && !placedDirty && !sizeDirty) {
      return () => {
        if (flushSave.current.flush === enqueueSave) flushSave.current = { sessionID: '', flush: async () => false };
      };
    }
    pendingSave.current = { sessionID, flush: enqueueSave };
    const timer = window.setTimeout(() => {
      if (pendingSave.current?.flush === enqueueSave) pendingSave.current = null;
      enqueueSave();
    }, schema.canvas.frontend.auto_save_delay_milliseconds);
    return () => {
      window.clearTimeout(timer);
      if (flushSave.current.flush === enqueueSave) flushSave.current = { sessionID: '', flush: async () => false };
    };
  }, [client, loadedSessionID, nodes, pendingHeight, pendingPlaced, pendingWidth, placedDirty, projectID, schema.canvas, sessionID,
    sizeDirty, ui.conflict, ui.historyReloaded, ui.saveFailed]);

  useEffect(() => () => {
    // 会话切换或卸载时不能丢掉尚未到点的防抖保存。
    if (pendingSave.current?.sessionID !== sessionID) return;
    const pending = pendingSave.current;
    pendingSave.current = null;
    pending.flush();
  }, [sessionID]);

  useEffect(() => {
    if (!sessionID || loadedSessionID !== sessionID) return;
    const imageCount = imageLayerCount(nodes);
    const fitted = fittedImageCountBySession.current[sessionID] ?? 0;
    if (imageCount <= fitted) {
      // A deletion lowers the bar, so the next picture still counts as an arrival.
      fittedImageCountBySession.current[sessionID] = imageCount;
      return;
    }
    // The picture on the stage is centred by its own effect below.
    if (hasObjectLayer.current) { fittedImageCountBySession.current[sessionID] = imageCount; return; }
    const frame = window.requestAnimationFrame(() => {
      const container = drawingArea.current;
      if (container) {
        useCanvasStore.getState().fitToScreen(sessionID, container.clientWidth, container.clientHeight);
        fittedImageCountBySession.current[sessionID] = imageCount;
      }
    });
    return () => window.cancelAnimationFrame(frame);
  }, [loadedSessionID, nodes, sessionID]);

  // #239：放置当前对象是一次性事件（见 imagesToPlace）。这个 effect 会因 artboard 等依赖在
  // 每次重渲染时重跑；同一个当前对象不会被再次放置，用户删除后也不会复活。
  const placedSelections = useRef(new WeakSet<CurrentObject>());
  // Share byte loading, not the placement event. If the user reselects the same image while its restored
  // selection is loading, the new selection must still finish after the old selection is cancelled.
  const placementLoads = useRef(new Map<string, Promise<{ width: number; height: number }>>());
  const placementSize = useCallback((url: string) => {
    const pending = placementLoads.current.get(url);
    if (pending) return pending;
    const loading = loadImageSize(url);
    placementLoads.current.set(url, loading);
    void loading.then(() => {
      if (placementLoads.current.get(url) === loading) placementLoads.current.delete(url);
    });
    return loading;
  }, []);
  const placementSession = useRef(sessionID);
  placementSession.current = sessionID;
  const placementArtboard = useRef(artboard);
  placementArtboard.current = artboard;
  const sessionVersions = useSessionVersions(sessionID);
  // The saved placement receipt is also loaded before the delayed restored selection arrives.
  const placedResults = useCanvasStore((state) => (sessionID ? state.placedResultsBySession[sessionID] : undefined));
  // Both sources suppress an automatic raw inpaint: the local job (before its lineage settles) and the
  // server's frozen operation (a retry has a new task but no local frame or mask).
  const inpaintSuppressedTaskIds = (tiles: readonly VersionTile[]): Set<string> => {
    const ids = new Set(readInpaintJobs(sessionID ?? '').map((job) => job.taskId));
    for (const tile of tiles) {
      if (tile.operation !== 'inpaint') continue;
      ids.add(tile.taskId);
      for (const taskId of tile.taskIds) ids.add(taskId);
    }
    return ids;
  };
  useEffect(() => {
    if (!sessionID || loadedSessionID !== sessionID) return;
    const first = firstOpen.current.get(sessionID);
    // Wait for the session's actual lineage and restored selection, not another session's stale object.
    const readyForFirst = first && !sessionVersions.isLoading && selection?.sessionId === sessionID;
    const firstImage = readyForFirst ? firstCanvasImage(selection, sessionID, sessionVersions.tiles) : null;
    if (readyForFirst) firstOpen.current.delete(sessionID);
    const suppressedTaskIds = inpaintSuppressedTaskIds(sessionVersions.tiles);
    // The receipt may already hold this task because the lineage seeded it before the restored selection
    // arrived; only the inpaint operation keeps a picture from being the first layer (a task-centre choice
    // remains a new, explicit selection).
    const firstRestore = firstImage && first && !suppressedTaskIds.has(firstImage.taskId) &&
      first.projectID === useCanvasStore.getState().getProjectId(sessionID) &&
      localBitmapNodeIds(sessionID).size === 0 &&
      first.nodesHash === computeNodesHash(useCanvasStore.getState().getNodes(sessionID)) ? firstImage : null;
    // FEA-02：产物归属其他会话时不落画布（切会话后旧轮次完成场景）。
    const urls = firstRestore ? [firstRestore.url]
      : imagesToPlace(selection, sessionID, placedSelections.current, suppressedTaskIds, placedResults);
    if (!selection || urls.length === 0) return;
    placedSelections.current.add(selection);
    // 重渲染不取消这次放置（它只发生一次）；只有切走会话或换了当前对象时才放弃。
    const stillCurrent = () => useWorkspaceStore.getState().currentObject === selection && placementSession.current === sessionID;
    const addReadyImages = async () => {
      for (const originalURL of urls) {
        const url = proxyAssetUrl(originalURL, selection.taskId);
        if (!url) continue;
        const store = useCanvasStore.getState();
        if (store.getNodes(sessionID).some((node) => node.type === 'image' && node.imageUrl === url)) continue;
        const natural = await placementSize(url);
        if (!stillCurrent()) return;
        if (store.getNodes(sessionID).some((node) => node.type === 'image' && node.imageUrl === url)) continue;
        if (firstRestore) {
          // Loading bytes must not overwrite drawing or deletion performed while that request waited.
          if (natural.width <= 0 || natural.height <= 0 || localBitmapNodeIds(sessionID).size > 0 ||
              first?.nodesHash !== computeNodesHash(store.getNodes(sessionID))) return;
          const registered = useCanvasStore.getState().placedResultsBySession[sessionID] ?? null;
          const placed = registered === null ? planAutoJoin(sessionVersions.tiles, null).placed
            : withPlaced(registered, [firstRestore.key]);
          if (placed) store.setPlacedResults(sessionID, placed);
        }
        const placement = imageNodePlacement(store.getNodes(sessionID), natural,
          schema.canvas.frontend, placementArtboard.current);
        store.addNode(sessionID, {
          id: generateId(), type: 'image', taskId: selection.taskId, imageUrl: url,
          captureId: selection.captureId,
          x: placement.x, y: placement.y,
          width: placement.width, height: placement.height,
        });
      }
    };
    void addReadyImages();
  }, [artboard, loadedSessionID, placementSize, proxyAssetUrl, schema.canvas.frontend, selection, sessionID,
    placedResults, sessionVersions.isLoading, sessionVersions.tiles]);

  // #262/#239：本会话新完成的图片结果自动加入画布一次（见 planAutoJoin）；用户之后删除也不再放回。
  // 旧画布第一次加载只登记不放置。放置前按 URL 去重，与“选中结果”放置互不重复。
  useEffect(() => {
    if (!sessionID || loadedSessionID !== sessionID || placedResults === undefined || sessionVersions.isLoading) return;
    // A different visit of the same session (A-B-A) is not the owner of this plan either.
    const scope = captureSessionScope();
    const stillOwned = () => scope.sessionId === sessionID && isCurrentSessionScope(scope);
    const suppressedTaskIds = inpaintSuppressedTaskIds(sessionVersions.tiles);
    const plan = planAutoJoin(sessionVersions.tiles, placedResults, { suppressTaskIds: suppressedTaskIds });
    // The legacy seed and raw inpaint receipts are registrations without a picture; normal results are
    // registered below only once the node is on the board (or already was), so a failed load or a session
    // switch cannot leave a permanent receipt with no picture (#313).
    const registeredNow = placedResults === null ? plan.placed : withPlaced(placedResults, plan.register);
    if (registeredNow && stillOwned()) useCanvasStore.getState().setPlacedResults(sessionID, registeredNow);
    if (plan.place.length === 0) return;
    // The plan was made from the receipts at the top of this effect. A receipt written meanwhile (another
    // effect placing this result) wins over this plan even if the person deleted the picture since (#313).
    const receipted = (key: string) =>
      useCanvasStore.getState().placedResultsBySession[sessionID]?.includes(key) === true;
    const register = (key: string) => {
      if (!stillOwned()) return;
      const store = useCanvasStore.getState();
      const next = withPlaced(store.placedResultsBySession[sessionID] ?? null, [key]);
      if (next) store.setPlacedResults(sessionID, next);
    };
    void (async () => {
      for (const result of plan.place) {
        if (!stillOwned()) return;
        if (receipted(result.key)) continue;
        const url = proxyAssetUrl(result.url, result.taskId);
        if (!url) continue;
        if (useCanvasStore.getState().getNodes(sessionID).some((node) => node.type === 'image' && node.imageUrl === url)) {
          register(result.key);
          continue;
        }
        const natural = await placementSize(url);
        if (!stillOwned()) return;
        // Another plan may have placed and registered this result while the picture loaded; its receipt,
        // not this stale list, decides whether it may return after the person deleted it.
        if (receipted(result.key)) continue;
        // A failed load keeps the key unregistered, so a later mount retries it; nothing changes here
        // and this mount does not loop.
        if (natural.width <= 0 || natural.height <= 0) continue;
        const latest = useCanvasStore.getState();
        if (!latest.getNodes(sessionID).some((node) => node.type === 'image' && node.imageUrl === url)) {
          const placement = imageNodePlacement(latest.getNodes(sessionID), natural, schema.canvas.frontend, placementArtboard.current);
          latest.addNode(sessionID, {
            id: generateId(), type: 'image', taskId: result.taskId, imageUrl: url,
            x: placement.x, y: placement.y, width: placement.width, height: placement.height,
          });
        }
        register(result.key);
      }
    })();
  }, [loadedSessionID, placedResults, placementSize, proxyAssetUrl, schema.canvas.frontend, sessionID, sessionVersions.isLoading, sessionVersions.tiles]);

  const uploadBlob = useCallback((blob: Blob, filename: string) =>
    uploadWorkspaceInputAsset(schema, blob, filename), [schema]);

  const downloadImage = useCallback((imageUrl: string) => {
    const safeURL = safeAssetURL(imageUrl);
    if (!safeURL) return;
    const link = document.createElement('a');
    link.href = safeURL;
    link.download = `${schema.canvas.frontend.export_filename_fallback}${schema.canvas.frontend.export_extension}`;
    link.click();
  }, [schema.canvas.frontend.export_filename_fallback, schema.canvas.frontend.export_extension]);

  /**
   * Adds one uploaded picture and returns where it landed (null when nothing was added). Where it lands is
   * uploadPlacement's rule: under the pointer for a drop or paste (`at`), after the previous picture of the
   * same batch, inside a still-empty frame or at the middle of the view for the upload button.
   */
  const handleUpload = useCallback(async (file: File, options: { at?: { x: number; y: number }; previous?: UploadRect | null; useFrame?: boolean } = {}): Promise<UploadRect | null> => {
    if (!sessionID || loadedSessionID !== sessionID) return null;
    const scope = captureSessionScope();
    if (scope.sessionId !== sessionID) return null;
    try {
      const blob = await prepareImageUploadBlob(file, {
        accept: schema.chat.frontend.image_file_accept,
        maximumBytes: schema.runtime.input_assets.upload_max_bytes,
        policy: schema.runtime.frontend.image_upload,
      });
      if (!isCurrentSessionScope(scope)) return null;
      const filename = blob.type === file.type ? file.name : `${schema.canvas.frontend.export_filename_fallback}${schema.canvas.frontend.export_extension}`;
      const url = await uploadBlob(blob, filename);
      if (!isCurrentSessionScope(scope)) return null;
      const natural = await loadImageSize(url);
      if (!isCurrentSessionScope(scope)) return null;
      if (natural.width <= 0 || natural.height <= 0) throw new ImageUploadError('read_failed');
      const frontend = schema.canvas.frontend;
      // #246: a picture keeps its own pixels, only shrunk to fit inside the canvas when it is larger.
      const size = canvasNodeSize(natural, frontend, artboard);
      const store = useCanvasStore.getState();
      const existing = store.getNodes(sessionID);
      const area = drawingArea.current?.getBoundingClientRect();
      const viewport = store.getViewport(sessionID);
      const rect = uploadPlacement({
        size,
        previous: options.previous,
        at: options.at,
        frameSlot: options.useFrame && artboard && artboardIsFree(existing, artboard)
          ? imageNodePlacement(existing, natural, frontend, artboard)
          : null,
        viewCenter: area ? { x: (area.width / 2 - viewport.panX) / viewport.zoom, y: (area.height / 2 - viewport.panY) / viewport.zoom } : null,
        gap: frontend.node_gap_pixels,
        fallback: { x: frontend.initial_node_x, y: frontend.initial_node_y },
      });
      store.addNode(sessionID, {
        id: generateId(), type: 'image', taskId: frontend.upload_task_id, imageUrl: url, ...rect,
      });
      return rect;
    } catch (error) {
      if (!isCurrentSessionScope(scope)) return null;
      if (!(error instanceof ImageUploadError)) {
        toast.error(ui.uploadFailed);
        return null;
      }
      toast.error(schemaMessage(schema, `ui.form.image_upload.${error.code}`, {
        filename: file.name,
        size: formatSizeLimit(schema.runtime.input_assets.upload_max_bytes),
        pixels: schema.runtime.frontend.image_upload.maximum_pixels,
      }));
      return null;
    }
  }, [artboard, loadedSessionID, schema, sessionID, ui.uploadFailed, uploadBlob]);

  /** Several pictures at once land in a row, one after another, in the order they were given. */
  const uploadFiles = useCallback(async (files: File[], options: { at?: { x: number; y: number }; useFrame?: boolean } = {}) => {
    let previous: UploadRect | null = null;
    for (const file of files) {
      const rect = await handleUpload(file, { ...options, previous });
      if (rect) previous = rect;
    }
  }, [handleUpload]);

  const selectedImageNode = useMemo(() => selectedImageLayer(nodes, selectedNodeIds), [nodes, selectedNodeIds]);

  // File name: the session title and the artboard ratio ("sunset-3x2.png"); the schema fallback when untitled.
  const sessionTitle = useChatStore((state) => state.sessions.find((item) => item.id === sessionID)?.title ?? '');
  const exportFilename = useMemo(
    () => exportBoardFilename(sessionTitle, artboard, schema.canvas.frontend),
    [artboard, schema.canvas.frontend, sessionTitle],
  );

  /**
   * The one way a picture leaves the board: everything visible inside the dashed artboard, rasterised
   * into one picture at the delivery size (exportArtboardToBlob). "Export board", "send to chat", the
   * merge below, inpainting and the Agent's current object all call it; none keeps a private copy.
   */
  const rasteriseBoard = useCallback(
    (sessionId: string) => rasteriseSessionBoard(schema, sessionId, artboard ? { artboard, pixels: deliveryPixels } : null),
    [artboard, deliveryPixels, schema],
  );

  const handleExport = useCallback(async (asReference: boolean) => {
    if (!sessionID || loadedSessionID !== sessionID) return;
    const scope = captureSessionScope();
    if (scope.sessionId !== sessionID) return;
    const filename = exportFilename;
    if (!asReference) {
      try {
        triggerBlobDownload(await rasteriseBoard(sessionID), filename);
      } catch {
        if (isCurrentSessionScope(scope)) toast.error(ui.exportFailed);
      }
      return;
    }
    let url: string;
    try {
      // #286: saved edits, export and upload each say when they fail, and are reported with their step.
      ({ url } = await exportAndUploadSnapshot({
        flush: () => flushPendingEdits(sessionID),
        exportPicture: () => rasteriseBoard(sessionID),
        upload: (picture) => uploadBlob(picture, filename),
        maximumBytes: schema.runtime.input_assets.upload_max_bytes,
      }));
    } catch (error) {
      const text = reportSnapshotFailure(schema, 'board-to-chat', error);
      if (isCurrentSessionScope(scope)) toast.error(text);
      return;
    }
    if (!isCurrentSessionScope(scope)) return;
    // Sending to the chat only attaches the picture to the next message; the stage object stays.
    useWorkspaceStore.getState().queueChatAttachments([url]);
    useStudioStore.getState().setChatCollapsed(false);
  }, [exportFilename, loadedSessionID, rasteriseBoard, schema, sessionID, ui.exportFailed, uploadBlob]);

  /**
   * Stamp the visible layers (Photoshop's "stamp visible"): the artboard is rasterised like any other
   * delivery, the result becomes a new top layer and a new version, and the layers it covers are hidden,
   * not deleted. One undo step; undoing it makes the base picture the working picture again.
   */
  const handleMerge = useCallback(async () => {
    if (!sessionID || loadedSessionID !== sessionID || !artboard) return;
    const scope = captureSessionScope();
    if (scope.sessionId !== sessionID) return;
    // Pending brush strokes first, so the layers hashed below are the ones that get rasterised.
    // #286: a stamp without the strokes on screen would hide them; each failing step says so and is reported.
    try {
      await saveBoardEdits(() => flushPendingEdits(sessionID));
    } catch (error) {
      const text = reportSnapshotFailure(schema, 'merge', error);
      if (isCurrentSessionScope(scope)) toast.error(text);
      return;
    }
    if (!isCurrentSessionScope(scope)) return;
    const content = artboardContent(useCanvasStore.getState().getNodes(sessionID), artboard);
    if (content.length === 0) { toast.error(ui.mergeFailed); return; }
    const contentHash = computeNodesHash(content);
    let url: string;
    try {
      ({ url } = await exportAndUploadSnapshot({
        exportPicture: () => rasteriseBoard(sessionID),
        upload: (picture) => uploadBlob(picture, snapshotFilename(schema)),
        maximumBytes: schema.runtime.input_assets.upload_max_bytes,
      }));
    } catch (error) {
      const text = reportSnapshotFailure(schema, 'merge', error);
      if (isCurrentSessionScope(scope)) toast.error(text);
      return;
    }
    try {
      if (!isCurrentSessionScope(scope)) return;
      const store = useCanvasStore.getState();
      // The layers changed while the picture uploaded: stamping would hide layers the picture does not show.
      if (computeNodesHash(artboardContent(store.getNodes(sessionID), artboard)) !== contentHash) {
        toast.error(ui.mergeFailed);
        return;
      }
      const base = selection?.kind === 'image' && selection.sessionId === sessionID ? selection : null;
      const topPicture = [...content].filter((node) => node.type === 'image').sort((a, b) => b.zIndex - a.zIndex)[0];
      const id = generateId();
      const objectId = mergedObjectId(id);
      const origin = base
        ? (base.captureId ? `${MERGED_FROM_CAPTURE}${base.captureId}` : base.taskId)
        : (topPicture?.taskId || `canvas:${sessionID}`);
      store.stampNodes(sessionID, content.map((node) => node.id), {
        id, type: 'image', taskId: objectId, imageUrl: url,
        x: artboard.x, y: artboard.y, width: artboard.width, height: artboard.height,
        mergedFrom: origin, mergedAt: new Date().toISOString(),
      });
      if (base) useWorkspaceStore.getState().rememberMergeBase(objectId, base);
      setCurrentObject({ taskId: objectId, sessionId: sessionID, kind: 'image', url });
      // The merged picture is what the next turn continues from.
      useWorkspaceStore.getState().setCanvasReference(url);
      toast.success(ui.mergeDone);
    } catch {
      if (isCurrentSessionScope(scope)) toast.error(ui.mergeFailed);
    }
  }, [artboard, loadedSessionID, rasteriseBoard, schema, selection, sessionID, setCurrentObject, ui.mergeDone, ui.mergeFailed, uploadBlob]);

  // Undo, delete or a conflict reload can take the picture the workspace points at off the canvas.
  const previousNodes = useRef<{ sessionID: string; nodes: CanvasNode[] } | null>(null);
  useEffect(() => {
    if (!sessionID || loadedSessionID !== sessionID) { previousNodes.current = null; return; }
    const before = previousNodes.current?.sessionID === sessionID ? previousNodes.current.nodes : null;
    previousNodes.current = { sessionID, nodes };
    if (!before || before === nodes) return;
    const workspace = useWorkspaceStore.getState();
    const outcome = reconcileObjectWithNodes({
      previous: before, next: nodes, currentObject: workspace.currentObject,
      canvasReference: workspace.canvasReference, mergeBases: workspace.mergeBases,
    });
    if (outcome.clearReference) workspace.setCanvasReference(null);
    if (outcome.restoreObject) workspace.forceCurrentObject(outcome.restoreObject);
  }, [loadedSessionID, nodes, sessionID]);

  // The listeners below are bound once per session; they read the latest artboard, URL rewriter and
  // uploader through this ref instead of closing over stale render values.
  const pointer = useRef<{ clientX: number; clientY: number; inside: boolean } | null>(null);
  const uploadTaskId = schema.canvas.frontend.upload_task_id;
  const dropContext = useRef({ artboard, proxyAssetUrl, uploadFiles, uploadTaskId });
  dropContext.current = { artboard, proxyAssetUrl, uploadFiles, uploadTaskId };
  useEffect(() => {
    const element = boardContainer.current;
    if (!element || !sessionID || loadedSessionID !== sessionID) return;
    const onDrop = (event: DragEvent) => {
      setCardOver(false);
      if (hasCanvasDrag(event.dataTransfer?.types)) {
        event.preventDefault();
        const item = decodeCanvasDrag(event.dataTransfer?.getData(CANVAS_ITEM_MIME));
        const pending = item?.source === 'upload' ? dataURIFile(item.url, item.label || 'image') : null;
        if (pending) {
          const rectangle = (drawingArea.current ?? element).getBoundingClientRect();
          const at = dropPointToWorld({ x: event.clientX, y: event.clientY }, rectangle, useCanvasStore.getState().getViewport(sessionID));
          void dropContext.current.uploadFiles([pending], { at });
          return;
        }
        const url = item ? dropContext.current.proxyAssetUrl(item.url, item.taskId) : null;
        const scope = captureSessionScope();
        if (!item || !url) return;
        const rectangle = (drawingArea.current ?? element).getBoundingClientRect();
        const store = useCanvasStore.getState();
        const viewport = store.getViewport(sessionID);
        const world = dropPointToWorld({ x: event.clientX, y: event.clientY }, rectangle, viewport);
        void (async () => {
          const natural = item.width && item.height ? { width: item.width, height: item.height } : await loadImageSize(url);
          if (!isCurrentSessionScope(scope) || natural.width <= 0) return;
          // Centred on the pointer; one undo step. 已选用 is a server fact (session pick): neither a drop nor its undo changes it, and a failed image load (width 0) inserts nothing.
          useCanvasStore.getState().addNode(sessionID, {
            // An uploaded picture (sent in the chat, a composer attachment) has no task: it is a layer like a file drop.
            id: generateId(), type: 'image', taskId: item.taskId || dropContext.current.uploadTaskId, imageUrl: url,
            ...dropNodeRect(world, natural, dropContext.current.artboard),
          });
        })();
        return;
      }
      const files = Array.from(event.dataTransfer?.files ?? []).filter((file) => file.type.startsWith('image/'));
      if (files.length === 0) return;
      event.preventDefault();
      // Files land where they were dropped.
      const rectangle = (drawingArea.current ?? element).getBoundingClientRect();
      const at = dropPointToWorld({ x: event.clientX, y: event.clientY }, rectangle, useCanvasStore.getState().getViewport(sessionID));
      void dropContext.current.uploadFiles(files, { at });
    };
    const onDragOver = (event: DragEvent) => {
      if (hasCanvasDrag(event.dataTransfer?.types)) {
        event.preventDefault();
        setCardOver(true);
      } else if (event.dataTransfer?.types.includes('Files')) event.preventDefault();
    };
    const onDragLeave = (event: DragEvent) => {
      if (!element.contains(event.relatedTarget as Node | null)) setCardOver(false);
    };
    const onPaste = (event: ClipboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return;
      // The one paste path: copied layers or a picture from the system clipboard, whichever was copied last.
      const files = Array.from(event.clipboardData?.files ?? []).filter((file) => file.type.startsWith('image/'));
      const canvas = useCanvasStore.getState();
      const choice = chooseClipboardPaste({
        imageFiles: files.length,
        text: event.clipboardData?.getData('text/plain') ?? '',
        internalMarker: canvas._clipboardId || null,
        internalNodes: canvas._clipboard.length,
      });
      if (choice === 'none') return;
      event.preventDefault();
      const rectangle = (drawingArea.current ?? element).getBoundingClientRect();
      const anchor = pasteAnchor(pointer.current, rectangle, canvas.getViewport(sessionID));
      if (choice === 'nodes') canvas.pasteNodes(sessionID, undefined, undefined, anchor);
      else void dropContext.current.uploadFiles(files, { at: anchor });
    };
    const onPointerMove = (event: MouseEvent) => {
      pointer.current = { clientX: event.clientX, clientY: event.clientY, inside: true };
    };
    const onPointerLeave = () => {
      if (pointer.current) pointer.current = { ...pointer.current, inside: false };
    };
    element.addEventListener('mousemove', onPointerMove);
    element.addEventListener('mouseleave', onPointerLeave);
    element.addEventListener('drop', onDrop);
    element.addEventListener('dragover', onDragOver);
    element.addEventListener('dragleave', onDragLeave);
    window.addEventListener('paste', onPaste);
    return () => {
      element.removeEventListener('mousemove', onPointerMove);
      element.removeEventListener('mouseleave', onPointerLeave);
      element.removeEventListener('drop', onDrop);
      element.removeEventListener('dragover', onDragOver);
      element.removeEventListener('dragleave', onDragLeave);
      window.removeEventListener('paste', onPaste);
    };
  }, [loadedSessionID, sessionID]);

  useEffect(() => {
    useWorkspaceStore.getState().setPaintToolActive(PAINT_TOOLS.has(activeTool));
    return () => useWorkspaceStore.getState().setPaintToolActive(false);
  }, [activeTool]);

  // The current object is the picture the user is working on: select its layer and
  // bring it to the middle of the board (a version picked on the strip lands here).
  const objectNode = useMemo(
    () => currentObjectLayer(nodes, selection, sessionID, proxyAssetUrl),
    [nodes, proxyAssetUrl, selection, sessionID],
  );
  hasObjectLayer.current = objectNode !== undefined;
  const objectGeometry = useRef<FitRect | null>(null);
  objectGeometry.current = objectNode
    ? { x: objectNode.x, y: objectNode.y, width: objectNode.width, height: objectNode.height }
    : null;
  const fitState = useRef<ObjectFitState & { applied: CanvasViewport | null }>({ key: '', quiet: false, applied: null });
  const objectNodeId = objectNode?.id;
  const objectKey = `${sessionID}:${selection?.taskId}:${selection?.url}:${objectNodeId}`;
  const viewportConfig = schema.canvas.frontend.viewport;
  const railExpanded = useStudioStore((state) => state.railExpanded);
  const selectionQuiet = selection?.quiet === true;
  const selectionRestored = selection?.restored === true;
  useEffect(() => {
    if (!sessionID || loadedSessionID !== sessionID || !objectNodeId) return;
    // #262: a result picked automatically while the board already holds other work joins quietly: the
    // person's selection, view and any stroke in progress stay as they are, also when the rail or the stage
    // size changes later. Picking that object themselves fits it as usual.
    const othersOnBoard = useCanvasStore.getState().getNodes(sessionID).some((node) => node.id !== objectNodeId);
    // A session opens on its restored object with the viewport saved in this browser. That view belongs to
    // the person, so the restored picture is selected but not fitted (#312 follow-up).
    const savedViewport = viewportAtLoad.current[sessionID] === true &&
      Boolean(useCanvasStore.getState().viewportBySession[sessionID]);
    const previous = fitState.current;
    const step = objectFitStep(previous, {
      key: objectKey, quiet: selectionQuiet, othersOnBoard, restored: selectionRestored, savedViewport,
    });
    if (step === 'restored') {
      fitState.current = { key: objectKey, quiet: false, restored: true, applied: null };
      useCanvasStore.getState().selectNode(objectNodeId);
      return;
    }
    if (step === 'quiet' || step === 'hold') {
      // `hold` for a restored object must stay a restored hold on the next layout change too.
      fitState.current = {
        key: objectKey, quiet: true,
        ...(step === 'hold' && previous.restored === true ? { restored: true } : {}),
        applied: null,
      };
      return;
    }
    if (step === 'fresh') {
      fitState.current = { key: objectKey, quiet: false, applied: null };
      useCanvasStore.getState().selectNode(objectNodeId);
    }
    // Fit the picture into the visible stage. Until the user pans or zooms it
    // follows the stage size (chat column collapsing, window resize);
    // a new object always starts fitted, whatever zoom the last one had.
    const apply = () => {
      const area = drawingArea.current;
      const rect = objectGeometry.current;
      if (!area || !rect) return;
      const state = fitState.current;
      const store = useCanvasStore.getState();
      if (state.applied && !viewportUnchanged(store.viewportBySession[sessionID], state.applied)) return;
      // The fit centres in what the floating controls leave (`stageFitInsets`), so the picture clears the rail.
      const insets = stageFitInsets(railExpanded);
      const next = fitRectViewport(rect, { width: area.clientWidth, height: area.clientHeight }, viewportConfig, insets);
      if (!next) return;
      store.setViewport(sessionID, next);
      state.applied = next;
    };
    const frame = window.requestAnimationFrame(apply);
    const area = drawingArea.current;
    const observer = area ? new ResizeObserver(() => apply()) : null;
    if (area && observer) observer.observe(area);
    return () => {
      window.cancelAnimationFrame(frame);
      observer?.disconnect();
    };
  }, [loadedSessionID, objectKey, objectNodeId, railExpanded, selectionQuiet, selectionRestored, sessionID, viewportConfig]);

  // A suppressed raw inpaint result is registered in the canvas placement receipt (saved with the nodes),
  // never as a second raw layer. True means the receipt is durable: the job may then be dropped without
  // letting a reload auto-place the raw picture or the quiet selection again.
  const handledInpaint = useRef(new Set<string>());
  // Deleting a stale attempt's marker does not re-run the effect. Bump this when its own board is showing
  // again, so the job is reconsidered now instead of waiting for another switch or reload.
  const [inpaintWake, setInpaintWake] = useState(0);
  const markInpaintRawPlaced = async (
    job: { taskId: string; sessionId: string },
    task: { downloadables: Array<{ id: string; kind: string }> },
  ): Promise<boolean> => {
    // A job belongs to the session that remembered it. After a switch, the visible board's flush would
    // persist the wrong canvas, so the job stays until its own session is loaded again.
    if (flushSave.current.sessionID !== job.sessionId) return false;
    const rawAssets = task.downloadables.filter((asset) => asset.kind === 'image');
    if (rawAssets.length === 0) return true;
    const store = useCanvasStore.getState();
    const current = store.placedResultsBySession[job.sessionId];
    if (current === undefined) return false;
    const base = current === null ? planAutoJoin(sessionVersions.tiles, null).placed : current;
    if (!base) return false;
    const next = withPlaced(base, rawAssets.map((asset) => resultKey(job.taskId, asset.id)));
    if (next) store.setPlacedResults(job.sessionId, next);
    return flushSave.current.flush();
  };
  // The job is a retry receipt for the patch, not the placement truth: only a durable receipt lets it go.
  const finishInpaintJob = async (
    job: { taskId: string; sessionId: string },
    task: { downloadables: Array<{ id: string; kind: string }> },
  ): Promise<boolean> => {
    // A failed or stale flush keeps the job; forgetting the attempt lets a later effect run retry it.
    if (!await markInpaintRawPlaced(job, task)) {
      handledInpaint.current.delete(job.taskId);
      return false;
    }
    removeInpaintJob(job.taskId);
    return true;
  };

  // X8: a finished inpaint result is also put on the board as a layer at the dashed frame, everything outside
  // the selection transparent. The job (mask, frame, size) was remembered when the task was submitted; the
  // task is followed through the tracked tasks, so a result that arrives after a reload still lands.
  const trackedTasks = useWorkspaceStore((state) => state.trackedTasks);
  useEffect(() => {
    if (!sessionID || loadedSessionID !== sessionID || placedResults === undefined) return;
    for (const job of readInpaintJobs(sessionID)) {
      if (handledInpaint.current.has(job.taskId)) continue;
      const task = trackedTasks.find((item) => item.taskId === job.taskId);
      if (!task?.terminal) continue;
      handledInpaint.current.add(job.taskId);
      // A previous mount may have landed the patch but failed to drop its job (storage refused the removal):
      // the mask asset proves the patch is already there, so it is not added a second time.
      const alreadyLanded = job.maskAssetId !== undefined && useCanvasStore.getState()
        .getNodes(job.sessionId).some((node) => node.inpaint?.maskAssetId === job.maskAssetId);
      if (alreadyLanded) {
        void finishInpaintJob(job, task);
        continue;
      }
      const image = task.failure ? undefined : task.downloadables.find((asset) => asset.kind === 'image');
      const resultUrl = image ? proxyAssetUrl(image.url, job.taskId) : '';
      if (!resultUrl) {
        void finishInpaintJob(job, task);
        continue;
      }
      const scope = captureSessionScope();
      void (async () => {
        try {
          const [result, mask] = await Promise.all([loadImageElement(resultUrl), loadImageElement(safeAssetURL(job.maskUrl))]);
          const blob = await renderInpaintLayer(result, mask, job.size);
          const url = await uploadBlob(blob, `${schema.canvas.frontend.export_filename_fallback}-inpaint-layer.png`);
          if (!isCurrentSessionScope(scope) || scope.sessionId !== job.sessionId) {
            // The board that started this attempt is hidden; its job must be retried when it returns.
            handledInpaint.current.delete(job.taskId);
            // Removing the marker schedules nothing. If the owner board is active again already, wake the
            // effect once; a retry is bounded by this wake and the attempt that follows it.
            if (useChatStore.getState().activeSessionId === job.sessionId) {
              setInpaintWake((value) => value + 1);
            }
            return;
          }
          const handle = handleOfObject(useChatStore.getState().messagesBySession[job.sessionId] ?? [], { taskId: job.taskId, kind: 'image', url: resultUrl });
          // The layer keeps the exact mask asset of this submission; rows without it stay unknown.
          const maskAssetId = job.maskAssetId ?? maskAssetIdFromURL(schema, job.maskUrl);
          const inpaint = maskAssetId
            ? { maskAssetId, width: job.size.width, height: job.size.height }
            : null;
          useCanvasStore.getState().addNode(job.sessionId, {
            id: generateId(), type: 'image', taskId: schema.canvas.frontend.upload_task_id, imageUrl: url,
            x: job.frame.x, y: job.frame.y, width: job.frame.width, height: job.frame.height,
            name: handle
              ? schemaMessage(schema, 'ui.canvas.layers.inpaint_name_handle', { handle })
              : schemaText(schema, 'ui.canvas.layers.inpaint_name'),
            ...(inpaint && isInpaintSource(inpaint) ? { inpaint } : {}),
          });
          if (await finishInpaintJob(job, task)) toast.success(schemaText(schema, 'ui.canvas.inpaint.layer_added'));
        } catch (error) {
          // The result itself is in the task tray; only the layer failed. The receipt keeps the raw picture
          // off the board even if this job is dropped.
          await finishInpaintJob(job, task);
          if (isCurrentSessionScope(scope)) {
            toast.error(error instanceof InpaintSizeMismatchError
              ? schemaMessage(schema, 'ui.canvas.inpaint.layer_size_mismatch', {
                expected: `${error.expected.width}×${error.expected.height}`,
                actual: `${error.actual.width}×${error.actual.height}`,
              })
              : schemaText(schema, 'ui.canvas.inpaint.layer_failed'));
          }
        }
      })();
    }
  }, [inpaintWake, loadedSessionID, placedResults, proxyAssetUrl, schema, sessionID, sessionVersions.tiles, trackedTasks, uploadBlob]);

  // Header actions that need the board: mask editing, background removal, export.
  const imageCommand = useStudioStore((state) => state.imageCommand);
  useEffect(() => {
    if (!imageCommand || !sessionID || loadedSessionID !== sessionID) return;
    useStudioStore.getState().clearImageCommand();
    if (imageCommand.kind === 'export-board') { void handleExport(false); return; }
    if (imageCommand.kind === 'send-board') { void handleExport(true); return; }
    if (imageCommand.kind === 'merge-version') { void handleMerge(); return; }
    // The toolbar's 去背景 acts on the picture layer selected on the board (exactly one); with nothing or
    // several layers selected it acts on the current version's picture. A board without a version has only
    // the selected picture to act on (the toolbar keeps the button off until one is selected). The floating
    // bar over a layer has no 去背景 of its own: this is the only way in.
    if (imageCommand.kind === 'remove-background') {
      const target = removeBackgroundTarget(selectedImageNode, objectNode);
      // #278: nothing was exported here; say what is missing (the version's layer is gone and none is selected).
      if (!target) { toast.error(ui.removeBackgroundNoLayer); return; }
      if (target.selectLayer) useCanvasStore.getState().selectNode(target.layer.id);
      setBgTarget(target.layer.imageUrl);
      return;
    }
    // #278: inpainting works on the artboard's content, not on the version's own layer, which the person may
    // have deleted while other pictures remain (see inpaintEntry).
    const draft = useMaskStore.getState().draft;
    const entry = inpaintEntry({
      boardHasContent: artboard !== null && artboardContent(nodes, artboard).length > 0,
      maskSupported,
      hasSelection: Boolean(draft && draft.sessionId === sessionID && draft.strokes.length > 0),
    });
    if (entry === 'board_empty') { toast.error(ui.inpaintBoardEmpty); return; }
    if (objectNode) useCanvasStore.getState().selectNode(objectNode.id);
    if (entry === 'switch_model') { setMaskSwitchOpen(true); return; }
    // A selection already made (marquee, lasso or mask) is submitted as it is, without going through the mask tool.
    if (entry === 'submit_selection') { useMaskStore.getState().requestSubmit(); return; }
    setActiveTool('mask');
  }, [artboard, handleExport, handleMerge, imageCommand, loadedSessionID, maskSupported, nodes, objectNode, selectedImageNode, sessionID, setActiveTool, ui.inpaintBoardEmpty, ui.removeBackgroundNoLayer]);

  if (!sessionID || loadedSessionID !== sessionID) {
    return <div className="flex min-h-0 flex-1 items-center justify-center text-sm text-muted-foreground">{ui.loading}</div>;
  }

  return (
    <div ref={boardContainer} className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
      {cardOver ? (
        <div aria-hidden data-testid="canvas-drop-hint" className="pointer-events-none absolute inset-2 z-float flex items-end justify-center rounded-xl border-2 border-dashed border-primary bg-primary/5 pb-4">
          <span className="rounded-full bg-panel-raised px-3 py-1 text-xs text-primary shadow">{ui.dropHint}</span>
        </div>
      ) : null}
      <div className="pointer-events-none absolute inset-x-0 top-14 z-float flex justify-center px-4">
        <CanvasSizeNotice sessionId={sessionID} delivery={canvasDelivery} className="pointer-events-auto max-w-[36rem] bg-panel-raised/95 shadow" />
      </div>
      <CanvasBoard
        sessionId={sessionID}
        spaceTapTaken={spaceTapTaken}
        activeTool={activeTool}
        artboard={artboard}
        deliveryPixels={deliveryPixels}
        drawingAreaRef={drawingArea}
        onToolChange={setActiveTool}
        onUploadImage={() => uploadInput.current?.click()}
        onImageGenerator={() => {
          useStudioStore.getState().setChatCollapsed(false);
          requestAnimationFrame(() => focusChatComposer());
        }}
        onMergeVisible={() => void handleMerge()}
        onDownloadImage={(node) => downloadImage(node.imageUrl)}
        onUploadBlob={uploadBlob}
        onInpaintRequest={(request) => setInpaintRequest(request)}
        maskSupported={maskSupported}
        onMaskUnsupported={() => setMaskSwitchOpen(true)}
        onImageDoubleClick={(node) => {
          // The stage's own picture opens with its sibling versions; any other picture opens alone.
          if (selection?.kind === 'image' && selection.url === node.imageUrl) { openStageLightbox(); return; }
          const url = proxyAssetUrl(node.imageUrl, node.taskId);
          if (url) openStageLightbox({ key: node.nodeId, url, label: schemaText(schema, 'ui.studio.lightbox.title'), downloadUrl: url });
        }}
        className="h-full w-full"
      />
      {selectedImageNode && layerBarShown(activeTool, selectedImageNode) ? (
        <CanvasImageActionBar
          layer={selectedImageNode}
          onDuplicate={() => useCanvasStore.getState().duplicateSelected(sessionID)}
          onDownload={() => downloadImage(selectedImageNode.imageUrl)}
          onDelete={() => removeNode(sessionID, selectedImageNode.id)}
        />
      ) : null}
      {maskSwitchOpen ? (
        <Dialog
          open
          onOpenChange={(nextOpen) => {
            if (!nextOpen) setMaskSwitchOpen(false);
          }}
        >
          <DialogContent closeLabel={schemaText(schema, 'ui.dialog.close')} className="flex flex-col gap-0 p-5">
            <DialogTitle className="mb-3 text-sm font-semibold">
              {schemaText(schema, 'ui.canvas.mask.switch_title')}
            </DialogTitle>
            {maskSwitchTarget ? (
              <>
                <p className="text-xs leading-5 text-muted-foreground">
                  {schemaMessage(schema, 'ui.canvas.mask.switch_confirm', {
                    model: currentModelName,
                    target: maskSwitchTarget.model_name,
                  })}
                </p>
                <div className="mt-4 flex justify-end gap-2">
                  <Action size="sm"
                    type="button"
                    onClick={() => setMaskSwitchOpen(false)}
                    variant="ghost"
                  >
                    {schemaText(schema, 'ui.canvas.mask.cancel')}
                  </Action>
                  <Action size="sm"
                    type="button"
                    onClick={() => {
                      setModel('image', maskSwitchTarget.model_id);
                      setMaskSwitchOpen(false);
                      setActiveTool('mask');
                    }}
                  >
                    {schemaText(schema, 'ui.canvas.mask.switch_accept')}
                  </Action>
                </div>
              </>
            ) : (
              <p className="text-xs leading-5 text-muted-foreground">
                {schemaText(schema, 'ui.canvas.inpaint.missing_semantics')}
              </p>
            )}
          </DialogContent>
        </Dialog>
      ) : null}
      {inpaintRequest ? (
        <InpaintDialog
          request={inpaintRequest}
          offerings={offeringsByCapability?.['image-to-image']}
          modelId={modelByDomain.image ?? null}
          uploadBlob={uploadBlob}
          onClose={() => setInpaintRequest(null)}
        />
      ) : null}
      {bgTarget ? (
        <BackgroundRemovalDialog
          imageUrl={bgTarget}
          uploadBlob={uploadBlob}
          onClose={() => setBgTarget(null)}
        />
      ) : null}
      <input ref={uploadInput} type="file" multiple accept={imageUploadAccept(schema.chat.frontend.image_file_accept, schema.runtime.frontend.image_upload)} className="hidden" onChange={(event) => {
        const files = Array.from(event.target.files ?? []);
        void uploadFiles(files, { useFrame: true });
        event.target.value = '';
      }} />
    </div>
  );
}
