'use client';

import { useCallback } from 'react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { boardDeliveryOf, rasteriseSessionBoard, boardPixelsFor3D } from '@/lib/canvas/board-delivery';
import { artboardDiffersFromObject, currentObjectSource } from '@/lib/canvas/current-object';
import { artboardContent } from '@/lib/canvas/export-canvas';
import { flushPendingEdits } from '@/lib/canvas/pending-edits';
import { exportAndUploadSnapshot, reportSnapshotFailure, saveBoardEdits, snapshotFilename } from '@/lib/canvas/send-snapshot';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { uploadWorkspaceInputAsset } from '@/lib/workspace/input-asset-upload';
import { captureSessionScope, isCurrentSessionScope } from '@/lib/workspace/session-scope';
import { useCanvasStore } from '@/store/canvas-store';
import { useChatStore } from '@/store/chat-store';
import { sameObject } from '@/store/mask-store';
import { captureBlockKeyOf } from '@/hooks/use-model-capture-block';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';
import { gaussianSplatTool } from '@/lib/workspace/gaussian-splat-action';

/**
 * 对一个对象（图或模型）的动作：在舞台打开、转 3D、截图到 2D、发到对话。结果卡、
 * 舞台头和版本条共用，所以"转 3D"在哪里点都是同一件事。
 */
export function useObjectActions() {
  const { schema } = useProductSchema();
  const { proxyAssetUrl } = useAssetURLRewriter();

  /**
   * Only objects of the session in view may take the stage. An unsent mask does not stop it: the
   * mask belongs to the canvas and stays (mask-store).
   */
  const openOnStage = useCallback((object: CurrentObject): boolean => {
    if (!object.sessionId || useChatStore.getState().activeSessionId !== object.sessionId) return false;
    useWorkspaceStore.getState().setCurrentObject(object);
    return sameObject(useWorkspaceStore.getState().currentObject, object);
  }, []);

  /** The turn that converts the object's own picture, addressed by its @img handle (no upload). */
  const requestObjectTurn = useCallback((object: CurrentObject) => {
    const key = object.handle ? 'ui.studio.action.to3d_turn' : 'ui.studio.action.to3d_turn_current';
    useStudioStore.getState().requestTurn(formatSchemaText(schemaText(schema, key), { handle: object.handle ?? '' }));
  }, [schema]);

  /**
   * 发送到 3D from the board. The Agent runs the usual image-to-3D flow, so a manual-approval session shows its
   * approval card with the cost. Same rule as the current object everywhere (currentObjectSource): the dashed
   * frame is rasterised like every other delivery (export, send to chat, inpaint), uploaded as an input asset
   * and handed to the Agent as the turn's picture, even when it holds just the current untouched picture.
   */
  const boardToThreeD = useCallback(async (sessionId: string): Promise<boolean> => {
    if (!useWorkspaceStore.getState().modelByDomain['model-3d']) {
      toast.error(schemaText(schema, 'ui.studio.action.choose_3d_model'));
      return false;
    }
    const delivery = boardDeliveryOf(sessionId);
    if (!delivery) return false;
    if (artboardContent(useCanvasStore.getState().getNodes(sessionId), delivery.artboard).length === 0) {
      toast.error(schemaText(schema, 'ui.studio.action.board_empty'));
      return false;
    }
    const scope = captureSessionScope();
    if (scope.sessionId !== sessionId) return false;
    // Strokes still on their way to the server belong in the picture (rasterising flushes again, at no cost).
    // #286: each step that fails says so, and is reported with its step.
    try {
      await saveBoardEdits(() => flushPendingEdits(sessionId));
    } catch (error) {
      const text = reportSnapshotFailure(schema, 'board-to-3d', error);
      if (isCurrentSessionScope(scope)) toast.error(text);
      return false;
    }
    if (!isCurrentSessionScope(scope)) return false;
    let url: string;
    try {
      ({ url } = await exportAndUploadSnapshot({
        exportPicture: () => rasteriseSessionBoard(schema, sessionId, { ...delivery, pixels: boardPixelsFor3D(schema, delivery) }),
        upload: (picture) => uploadWorkspaceInputAsset(schema, picture, snapshotFilename(schema)),
        maximumBytes: schema.runtime.input_assets.upload_max_bytes,
      }));
    } catch (error) {
      const text = reportSnapshotFailure(schema, 'board-to-3d', error);
      if (isCurrentSessionScope(scope)) toast.error(text);
      return false;
    }
    if (!isCurrentSessionScope(scope)) return false;
    useStudioStore.getState().requestTurn(schemaText(schema, 'ui.studio.action.to3d_turn_board'), [{ kind: 'image', url }]);
    return true;
  }, [schema]);

  /**
   * The Agent does the conversion, so a manual-approval session shows its usual
   * approval card with the cost before anything is spent. When the board holds anything, the board is what
   * gets converted, never the object's handle.
   */
  const toThreeD = useCallback((object: CurrentObject): boolean => {
    if (object.kind !== 'image') return false;
    if (!useWorkspaceStore.getState().modelByDomain['model-3d']) {
      toast.error(schemaText(schema, 'ui.studio.action.choose_3d_model'));
      return false;
    }
    if (!openOnStage(object)) return false;
    const delivery = object.sessionId ? boardDeliveryOf(object.sessionId) : null;
    if (delivery && object.sessionId
      && currentObjectSource(useCanvasStore.getState().getNodes(object.sessionId), delivery.artboard) === 'canvas') {
      void boardToThreeD(object.sessionId);
      return true;
    }
    requestObjectTurn(object);
    return true;
  }, [boardToThreeD, openOnStage, requestObjectTurn, schema]);

  /** An explicit image-to-Splat Agent turn. It names exactly the selected owned image and skips board rasterization. */
  const toGaussianSplat = useCallback((object: CurrentObject): boolean => {
    if (object.kind !== 'image' || !object.sessionId || !object.taskId || !object.assetId) return false;
    if (!openOnStage(object)) return false;
    const hasTool = Boolean(gaussianSplatTool(schema));
    const turn = !hasTool ? '' : object.handle
      ? formatSchemaText(schemaText(schema, 'ui.studio.action.splat_turn'), { handle: object.handle })
      : schemaText(schema, 'ui.studio.action.to_splat');
    if (!turn) return false;
    useStudioStore.getState().requestTurn(turn, [{ kind: 'image', taskId: object.taskId, assetId: object.assetId }]);
    return true;
  }, [openOnStage, schema]);

  const captureToTwoD = useCallback((object: CurrentObject): boolean => {
    if (object.kind !== 'model' || !object.assetId) return false;
    // A model that is on the stage and not ready cannot be captured; the request is refused, never queued (#126).
    if (captureBlockKeyOf(object.taskId, useStudioStore.getState().stageModelState)) return false;
    if (!openOnStage(object)) return false;
    useStudioStore.getState().requestCapture(object.taskId);
    return true;
  }, [openOnStage]);

  /**
   * 用图片重贴图: the picture is a texture reference for one of the session's 3D models. While the dashed frame
   * holds just the object's own untouched picture, that picture is named by its handle and nothing else rides
   * along; once the frame differs from it (painted, cropped, more layers) or it has no handle, the frame is
   * rasterised at the delivery size, uploaded and sent as the turn's picture, so the edits are what the model
   * is retextured from.
   */
  const retextureWithImage = useCallback(async (object: CurrentObject, modelHandle: string, imageHandle: string): Promise<boolean> => {
    if (object.kind !== 'image') return false;
    const sessionId = object.sessionId;
    const delivery = sessionId ? boardDeliveryOf(sessionId) : null;
    const handleTurn = formatSchemaText(schemaText(schema, 'ui.studio.retexture.turn'), { image: imageHandle, model: modelHandle });
    const currentTurn = formatSchemaText(schemaText(schema, 'ui.studio.retexture.turn_current'), { model: modelHandle });
    if (!sessionId || !delivery || artboardContent(useCanvasStore.getState().getNodes(sessionId), delivery.artboard).length === 0) {
      // No frame content: the turn goes out as before, the stage object naming the picture.
      useStudioStore.getState().requestTurn(imageHandle ? handleTurn : currentTurn);
      return true;
    }
    const scope = captureSessionScope();
    if (scope.sessionId !== sessionId) return false;
    // Strokes still on their way to the server decide the rule too: once they land, the picture differs.
    try {
      await saveBoardEdits(() => flushPendingEdits(sessionId));
    } catch (error) {
      const text = reportSnapshotFailure(schema, 'retexture', error);
      if (isCurrentSessionScope(scope)) toast.error(text);
      return false;
    }
    if (!isCurrentSessionScope(scope)) return false;
    if (imageHandle && !artboardDiffersFromObject(
      useCanvasStore.getState().getNodes(sessionId), delivery.artboard, object, (url) => proxyAssetUrl(url, object.taskId) ?? url,
    )) {
      // The frame is just this untouched picture: its handle names it, so no canvas rides along.
      useStudioStore.getState().requestTurn(handleTurn, []);
      return true;
    }
    let url: string;
    try {
      ({ url } = await exportAndUploadSnapshot({
        exportPicture: () => rasteriseSessionBoard(schema, sessionId, delivery),
        upload: (picture) => uploadWorkspaceInputAsset(schema, picture, snapshotFilename(schema)),
        maximumBytes: schema.runtime.input_assets.upload_max_bytes,
      }));
    } catch (error) {
      const text = reportSnapshotFailure(schema, 'retexture', error);
      if (isCurrentSessionScope(scope)) toast.error(text);
      return false;
    }
    if (!isCurrentSessionScope(scope)) return false;
    useStudioStore.getState().requestTurn(currentTurn, [{ kind: 'image', url }]);
    return true;
  }, [proxyAssetUrl, schema]);

  const sendToChat = useCallback((object: CurrentObject): boolean => {
    if (object.kind !== 'image') return false;
    useWorkspaceStore.getState().queueChatAttachments([{
      url: object.url,
      ...(object.assetId ? { assetId: object.assetId } : {}),
      ...(object.taskId && !object.taskId.startsWith('canvas:') ? { taskId: object.taskId } : {}),
      ...(object.captureId ? { captureId: object.captureId } : {}),
    }]);
    toast.success(schemaText(schema, 'ui.workspace.lineage.sent_chat'));
    return true;
  }, [schema]);

  return { openOnStage, toThreeD, toGaussianSplat, boardToThreeD, retextureWithImage, captureToTwoD, sendToChat };
}
