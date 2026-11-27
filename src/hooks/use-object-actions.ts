'use client';

import { useCallback } from 'react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useWorkspaceOfferings } from '@/hooks/use-workspace-offerings';
import type { OfferingWire } from '@/lib/agent-offerings';
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
import { openComposerSettings } from '@/store/composer-settings-store';
import { useModel3DConfirmStore } from '@/store/model3d-confirm-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';
import { defaultImageTo3DOffering, model3DCostText } from '@/lib/workspace/model3d-default';
import { gaussianSplatTool } from '@/lib/workspace/gaussian-splat-action';
import { groupMultiview, type MultiviewModel, type ThreeViewGroup } from '@/lib/workspace/three-view';

/**
 * 对一个对象（图或模型）的动作：在舞台打开、转 3D、截图到 2D、发到对话。结果卡、
 * 舞台头和版本条共用，所以"转 3D"在哪里点都是同一件事。
 */
export function useObjectActions() {
  const { schema } = useProductSchema();
  const { proxyAssetUrl } = useAssetURLRewriter();
  const { offeringsByCapability: model3dOfferings } = useWorkspaceOfferings(schema, 'model-3d');

  /**
   * #678: 转 3D needs a 3D model. With one picked, `run` goes ahead under the person's pick. Without one, the person is
   * asked once: 「使用 {模型} 转 3D？」 with the recommended available model (or `offering`); 确认 runs this one
   * conversion with that model as the turn's 3D model (no session lock, no stored preference), 换一个… opens the
   * settings. Returns what `run` returned, or false when the person is being asked (nothing was requested yet).
   */
  const withModel3D = useCallback((
    run: (models?: Record<string, string>) => boolean,
    offering?: Pick<OfferingWire, 'model_id' | 'model_name' | 'price_mode' | 'default_native_quote' | 'credits_metered' | 'estimated_credits'> | null,
  ): boolean => {
    if (useWorkspaceStore.getState().modelByDomain['model-3d']) return run();
    const choice = offering ?? defaultImageTo3DOffering(model3dOfferings);
    if (!choice) {
      // The models are still loading or none is available: the settings say which.
      toast.info(schemaText(schema, 'ui.studio.action.choose_3d_model'));
      openComposerSettings('models');
      return false;
    }
    useModel3DConfirmStore.getState().ask({
      modelId: choice.model_id,
      modelName: choice.model_name || choice.model_id,
      cost: model3DCostText(schema, choice),
      run: (modelId) => { run({ 'model-3d': modelId }); },
    });
    return false;
  }, [model3dOfferings, schema]);

  /**
   * Only objects of the session in view may take the stage. An unsent mask does not stop it: the
   * mask belongs to the canvas and stays (mask-store).
   */
  const putOnStage = useCallback((object: CurrentObject): boolean => {
    if (!object.sessionId || useChatStore.getState().activeSessionId !== object.sessionId) return false;
    useWorkspaceStore.getState().setCurrentObject(object);
    return sameObject(useWorkspaceStore.getState().currentObject, object);
  }, []);

  /**
   * #681: every caller is a person's click (a result card picture, 选用, a plan candidate, the lightbox), and a
   * click never changes the board: a picture without an explicit placement only takes the stage (`view`), the way
   * a strip tile does. Only a double click, a 「放入画板」/「插入到画布」 button or a drag puts it on the board
   * (insertOnCanvas). The automatic join of a new Agent result does not come through here (#239/#262).
   */
  const openOnStage = useCallback((object: CurrentObject): boolean =>
    putOnStage(object.kind === 'image' && !object.placement ? { ...object, placement: 'view' } : object), [putOnStage]);

  /**
   * #559: looking at a version (a strip tile, a source jump, stepping through versions) only puts it on the
   * stage. A picture already on the board is selected there; one the person removed stays removed.
   */
  const viewOnStage = useCallback((object: CurrentObject): boolean =>
    putOnStage(object.kind === 'image' ? { ...object, placement: 'view' } : object), [putOnStage]);

  /**
   * #559: 「插入到画布」 puts exactly this picture on the board and records it as placed. A model has no
   * 2D board: it opens on the 3D stage as before.
   */
  const insertOnCanvas = useCallback((object: CurrentObject): boolean =>
    putOnStage(object.kind === 'image' ? { ...object, placement: 'insert' } : object), [putOnStage]);

  /** The turn that converts the object's own picture, addressed by its @img handle (no upload). */
  const requestObjectTurn = useCallback((object: CurrentObject, models?: Record<string, string>) => {
    const key = object.handle ? 'ui.studio.action.to3d_turn' : 'ui.studio.action.to3d_turn_current';
    useStudioStore.getState().requestTurn(formatSchemaText(schemaText(schema, key), { handle: object.handle ?? '' }), undefined, undefined, models);
  }, [schema]);

  /**
   * 发送到 3D from the board. The Agent runs the usual image-to-3D flow, so a manual-approval session shows its
   * approval card with the cost. Same rule as the current object everywhere (currentObjectSource): the dashed
   * frame is rasterised like every other delivery (export, send to chat, inpaint), uploaded as an input asset
   * and handed to the Agent as the turn's picture, even when it holds just the current untouched picture.
   */
  const convertBoard = useCallback(async (sessionId: string, models?: Record<string, string>): Promise<boolean> => {
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
    useStudioStore.getState().requestTurn(schemaText(schema, 'ui.studio.action.to3d_turn_board'), [{ kind: 'image', url }], undefined, models);
    return true;
  }, [schema]);

  const boardToThreeD = useCallback(async (sessionId: string): Promise<boolean> => {
    if (useWorkspaceStore.getState().modelByDomain['model-3d']) return convertBoard(sessionId);
    withModel3D((models) => { void convertBoard(sessionId, models); return true; });
    return false;
  }, [convertBoard, withModel3D]);

  /**
   * The Agent does the conversion, so a manual-approval session shows its usual
   * approval card with the cost before anything is spent. When the board holds anything, the board is what
   * gets converted, never the object's handle. #691: `pictureOnly` (the three-view card's 只用正面转 3D) converts the
   * picture itself whatever the board holds.
   */
  const toThreeD = useCallback((object: CurrentObject, options: { pictureOnly?: boolean } = {}): boolean => {
    if (object.kind !== 'image') return false;
    return withModel3D((models) => {
      if (!openOnStage(object)) return false;
      const delivery = object.sessionId && !options.pictureOnly ? boardDeliveryOf(object.sessionId) : null;
      // #610: the board is converted only while it holds this picture's layer; otherwise the picture itself.
      if (delivery && object.sessionId
        && currentObjectSource(useCanvasStore.getState().getNodes(object.sessionId), delivery.artboard, object, proxyAssetUrl) === 'canvas') {
        void convertBoard(object.sessionId, models);
        return true;
      }
      requestObjectTurn(object, models);
      return true;
    });
  }, [convertBoard, openOnStage, proxyAssetUrl, requestObjectTurn, withModel3D]);

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

  /**
   * #65: 只用正面转 3D when the person replaced the front view with an upload. The picture is their input asset with
   * no Task, so it travels as the turn's picture (its asset address, verified by the server), like the board's.
   */
  const inputPictureToThreeD = useCallback((url: string): boolean => {
    if (!url) return false;
    return withModel3D((models) => {
      useStudioStore.getState().requestTurn(schemaText(schema, 'ui.studio.action.to3d_turn_current'), [{ kind: 'image', url }], undefined, models);
      return true;
    });
  }, [schema, withModel3D]);

  /**
   * #65: 用三视图生成 3D. The turn carries the structured request (the group and the exact picture of each finished
   * view the card shows); the server binds the pictures and their views, the Agent only names the tool. The
   * approval card then shows each picture with its view and the cost. No stage selection rides along.
   */
  const multiviewToThreeD = useCallback((group: ThreeViewGroup, model: MultiviewModel): boolean => {
    const { slots, ready, request } = groupMultiview(group, model);
    if (!ready) return false;
    const views = slots.map((slot) => slot.label).join(schemaText(schema, 'ui.studio.three_view.view_separator'));
    // #678/#691: without a 3D model picked, the person confirms this multiview model first (it is never chosen silently).
    const offering = Object.values(model3dOfferings ?? {}).flat().find((item) => item.available && item.model_id === model.modelId) ?? null;
    const run = (models?: Record<string, string>) => {
      useStudioStore.getState().requestTurn(formatSchemaText(schemaText(schema, 'ui.studio.three_view.multiview_turn'), { views }), [], request, models);
      return true;
    };
    return withModel3D(run, offering ?? { model_id: model.modelId, model_name: model.modelName });
  }, [model3dOfferings, schema, withModel3D]);

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

  return { openOnStage, viewOnStage, insertOnCanvas, toThreeD, inputPictureToThreeD, multiviewToThreeD, toGaussianSplat, boardToThreeD, retextureWithImage, captureToTwoD, sendToChat };
}
