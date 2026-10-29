'use client';

import { useCallback, useEffect, useMemo, useRef } from 'react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAuth } from '@/hooks/use-auth';
import { useCanvasArtboard } from '@/hooks/use-canvas-artboard';
import { BITMAP_LIMIT_TOAST_ID, bitmapCapacity, bitmapLimitMessage, CanvasBitmapLimitError } from '@/lib/canvas/bitmap-limit';
import { createCanvasBrowserClient } from '@/lib/canvas/browser-client';
import { persistCapturedImageToCanvas } from '@/lib/canvas/capture-persistence';
import { createSettledValue } from '@/lib/canvas/settled-value';
import { uploadWorkspaceInputAssetResult } from '@/lib/workspace/input-asset-upload';
import type { ModelCaptureView } from '@/lib/workspace/model-capture';
import { isCurrentSessionScope, selectionForSession } from '@/lib/workspace/session-scope';
import { useChatStore } from '@/store/chat-store';
import { useCanvasStore } from '@/store/canvas-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';

/**
 * 把 3D 舞台当前视角的截图送到 2D：上传、让服务端核实并登记截图记录、落到画板，
 * 然后让这张图成为舞台上的当前对象，并作为下一轮对话的附件（带着截图记录和来源模型）。
 * 任何一步之后用户已经切走了模型、会话或账号，就静默放弃，不把结果写到别处。
 */
export function useModelCapture() {
  const { schema } = useProductSchema();
  const rawSelection = useWorkspaceStore((state) => state.currentObject);
  const setCurrentObject = useWorkspaceStore((state) => state.setCurrentObject);
  const sessionID = useChatStore((state) => state.activeSessionId);
  const sessionRevision = useChatStore((state) => state.activeSessionRevision);
  const ownerID = useAuth().user?.id ?? null;
  const currentOwner = useRef(ownerID);
  currentOwner.current = ownerID;
  // #246: a canvas without a saved size starts at what the chosen 2D model produces, as on the board. Until
  // that model's settings have loaded, `initial` is only the Schema default, so a capture waits for them.
  const { initial, settled } = useCanvasArtboard(sessionID);
  const initialSize = useRef(createSettledValue(initial, settled));
  useEffect(() => { initialSize.current.update(initial, settled); }, [initial, settled]);
  const canvasClient = useMemo(() => createCanvasBrowserClient({
    schema: schema.canvas,
    routes: {
      projects: schema.runtime.http.legacy_routes.canvas_projects,
      project: schema.runtime.http.legacy_routes.canvas_project,
      nodes: schema.runtime.http.legacy_routes.canvas_nodes,
      merge: schema.runtime.http.legacy_routes.canvas_merge,
    },
  }), [schema]);
  const selection = selectionForSession(rawSelection, sessionID);

  return useCallback(async (blob: Blob, view: ModelCaptureView) => {
    if (!ownerID || !selection || selection.kind !== 'model' || !selection.assetId || !sessionID ||
      selection.sessionId !== sessionID) {
      throw new Error('model capture source unavailable');
    }
    const stillCurrent = () => {
      const workspace = useWorkspaceStore.getState();
      return currentOwner.current === ownerID &&
        isCurrentSessionScope({ sessionId: sessionID, revision: sessionRevision }) &&
        workspace.currentObject?.kind === 'model' &&
        workspace.currentObject.sessionId === sessionID &&
        workspace.currentObject.assetId === selection.assetId &&
        workspace.currentObject.taskId === selection.taskId;
    };
    if (!stillCurrent()) return;
    // #149: a canvas already at its picture limit takes no screenshot; say so before anything is uploaded.
    const before = bitmapCapacity(schema, useCanvasStore.getState().getNodes(sessionID));
    if (before.full) throw new CanvasBitmapLimitError(before.used, before.limit);
    if (blob.size > schema.runtime.input_assets.upload_max_bytes) {
      throw new Error('model capture exceeds upload limit');
    }
    const route = schema.runtime.http.routes.model_captures;
    if (!route) throw new Error('model capture route unavailable');
    const screenshot = await uploadWorkspaceInputAssetResult(schema, blob, 'model-capture.png');
    if (!stillCurrent()) return;
    const response = await fetch(route, { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ session_id: sessionID, source_task_id: selection.taskId,
        model_asset_id: selection.assetId, screenshot_asset_id: screenshot.id,
        idempotency_key: `capture:${screenshot.id}`, view }) });
    if (!response.ok) throw new Error(`model capture ${response.status}`);
    const record = await response.json() as {
      id?: string; session_id?: string; source_task_id?: string; model_asset_id?: string;
      screenshot_asset_id?: string; pixel_width?: number; pixel_height?: number;
    };
    const pixelWidth = record.pixel_width;
    const pixelHeight = record.pixel_height;
    if (typeof record.id !== 'string' || !record.id || record.session_id !== sessionID || record.source_task_id !== selection.taskId ||
        record.model_asset_id !== selection.assetId || record.screenshot_asset_id !== screenshot.id ||
        typeof pixelWidth !== 'number' || typeof pixelHeight !== 'number' ||
        !Number.isSafeInteger(pixelWidth) || !Number.isSafeInteger(pixelHeight) ||
        pixelWidth <= 0 || pixelHeight <= 0) {
      throw new Error('invalid capture result');
    }
    // The screenshot is a version of its own: the strip reads the chain again so it is there at once,
    // without waiting for the board to save it.
    useStudioStore.getState().bumpVersions();
    if (!stillCurrent()) return;
    // Persist the accepted capture before mounting a view that can be closed.
    // Dimensions come from the server-verified PNG, so attachment does not wait
    // for the canvas image request or depend on the canvas component's lifetime.
    const canvasInitial = await initialSize.current.read();
    if (!stillCurrent()) return;
    await persistCapturedImageToCanvas({
      client: canvasClient, schema: schema.canvas, ownerID, sessionID,
      taskID: selection.taskId, captureID: record.id, imageURL: screenshot.url,
      naturalSize: { width: pixelWidth, height: pixelHeight },
      initialSize: canvasInitial, isCurrent: stillCurrent,
    });
    if (!stillCurrent()) return;
    setCurrentObject({ taskId: selection.taskId, kind: 'image', url: screenshot.url,
      assetId: screenshot.id, captureId: record.id, sessionId: sessionID });
    // The capture also joins the chat context: the next message carries the
    // picture with its capture record and the model Task it was taken from.
    useWorkspaceStore.getState().queueChatAttachments([{
      url: screenshot.url, assetId: screenshot.id, taskId: selection.taskId, captureId: record.id,
    }]);
    // The screenshot is in; if it used up the room, the next one will not fit.
    const after = bitmapCapacity(schema, useCanvasStore.getState().getNodes(sessionID));
    if (after.full || after.near) {
      toast.warning(bitmapLimitMessage(schema, after.full ? 'reached' : 'near', after), { id: BITMAP_LIMIT_TOAST_ID });
    }
  }, [canvasClient, ownerID, schema, selection, sessionID, sessionRevision, setCurrentObject]);
}
