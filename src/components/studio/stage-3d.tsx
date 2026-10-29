'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';

import { Stage3DViews } from '@/components/studio/stage-3d-views';
import { useProductSchema } from '@/components/product-schema-provider';
import { Surface } from '@/components/ui/surface';
import { ModelViewer3D, type ModelViewerControl } from '@/components/viewer/model-viewer-3d';
import { useModelCapture } from '@/hooks/use-model-capture';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { descriptorCaps } from '@/lib/workspace/key-dispatch';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { captureStepOf } from '@/lib/workspace/capture-request';
import { saveArchive } from '@/lib/workspace/batch-export';
import { partFilename, partsKeyOf } from '@/lib/workspace/model-parts';
import { activeLocale } from '@/lib/i18n/locale';
import { groupedCount, type ModelStats, type TextureSize } from '@/lib/workspace/model-stats';
import type { ViewMode } from '@/lib/workspace/view-mode';
import { useStudioStore, type StageModelState } from '@/store/studio-store';
import type { CurrentObject } from '@/store/workspace-store';

/** 3D 舞台：模型铺满中间区域，右下角叠着面数和贴图尺寸（读不到的写“—”，不猜）。 */
export function Stage3D({ object }: { object: CurrentObject }) {
  const { schema } = useProductSchema();
  const captureTo2D = useModelCapture();
  const [held, setHeld] = useState<{ key: string; control: ModelViewerControl } | null>(null);
  const [stats, setStats] = useState<ModelStats | null>(null);
  const [capturing, setCapturing] = useState(false);
  const [view, setView] = useState<{ key: string; mode: ViewMode } | null>(null);
  const captureRequest = useStudioStore((state) => state.captureRequest);
  const partExportRequest = useStudioStore((state) => state.partExportRequest);
  // Parts are picked by a click on the model only while the 部件 tab is the one being looked at.
  const partsActive = useStudioStore((state) => state.panelTab3d === 'parts');
  const partsKey = partsKeyOf(object);
  const selectedPart = useStudioStore((state) => (state.selectedPart?.key === partsKey ? state.selectedPart.id : null));
  const { proxyAssetUrl } = useAssetURLRewriter();
  const [texture, setTexture] = useState<{ key: string; size: TextureSize | null } | null>(null);
  const modelUrl = proxyAssetUrl(object.url, object.taskId);
  const measuredKey = `${object.taskId}:${object.assetId ?? ''}:${modelUrl}`;
  // The control of the model that is on the stage; the one of the model just switched away from does not count.
  const viewerKey = `${object.taskId}:${object.assetId}`;
  const control = held?.key === viewerKey ? held.control : null;
  const setControl = useCallback((next: ModelViewerControl | null) => setHeld(next ? { key: viewerKey, control: next } : null), [viewerKey]);
  const modelState: StageModelState = control?.failed ? 'failed' : control?.ready ? (control.incompleteTextures > 0 ? 'incomplete' : 'ready') : 'loading';
  const modelTask = object.taskId;

  // Entries that capture this model (result card, 舞台头, version bar) read its state from the store.
  useEffect(() => {
    useStudioStore.getState().setStageModelState({ taskId: modelTask, state: modelState });
  }, [modelState, modelTask]);
  useEffect(() => () => {
    const store = useStudioStore.getState();
    if (store.stageModelState?.taskId === modelTask) store.setStageModelState(null);
    // A request this stage never got to run must not wait for a later load of the model (#126).
    if (store.captureRequest?.taskId === modelTask) store.clearCaptureRequest();
  }, [modelTask]);

  // Texture sizes come from the model the viewer loaded; the file is not downloaded a second time.
  // Each model opens with its textures; the choice belongs to the model that is on the stage.
  const viewMode: ViewMode = view?.key === measuredKey ? view.mode : 'texture';
  const textureSize = texture?.key === measuredKey ? texture.size : null;

  // Ctrl/Cmd+0 (the key dispatcher) and the 复位视角 button both put the camera back.
  const resetRequest = useStudioStore((state) => state.viewResetRequest);
  const handledReset = useRef(resetRequest?.seq ?? 0);
  useEffect(() => {
    if (!resetRequest || resetRequest.seq === handledReset.current) return;
    handledReset.current = resetRequest.seq;
    control?.resetView();
  }, [control, resetRequest]);

  // 「截图到 2D」从结果卡、舞台头或版本条发来：只在模型加载好、贴图完整时截。加载失败或贴图不完整时请求直接丢弃，
  // 不排队：之后重试成功也不会补发（#126）。模型还在首次加载时请求保留到加载结束。
  useEffect(() => {
    const step = captureStepOf(captureRequest, object.taskId, modelState, capturing);
    if (step === 'drop') { useStudioStore.getState().clearCaptureRequest(); return; }
    if (step !== 'run' || !control) return;
    useStudioStore.getState().clearCaptureRequest();
    setCapturing(true);
    void control.capture().finally(() => setCapturing(false));
  }, [capturing, captureRequest, control, modelState, object.taskId]);

  // 「导出 GLB」从部件标签发来：模型加载好之后写出这一个部件。请求留到写完，面板据此显示进度。
  const handledPartExport = useRef(0);
  useEffect(() => {
    if (!partExportRequest || partExportRequest.seq === handledPartExport.current) return;
    const { key, id, seq } = partExportRequest;
    const clear = () => { if (useStudioStore.getState().partExportRequest?.seq === seq) useStudioStore.getState().clearPartExportRequest(); };
    if (key !== partsKey) { clear(); return; }
    if (modelState === 'failed') { clear(); return; }
    if (modelState === 'incomplete') {
      toast.error(schemaText(schema, 'ui.studio.stage.action.capture_textures_incomplete'));
      handledPartExport.current = seq;
      clear();
      return;
    }
    if (!control?.ready) return;
    handledPartExport.current = seq;
    const parts = useStudioStore.getState().stageParts;
    const index = parts?.key === key ? parts.parts.findIndex((part) => part.id === id) : -1;
    const part = index >= 0 ? parts?.parts[index] : undefined;
    if (!part) { clear(); return; }
    const fallbackName = formatSchemaText(schemaText(schema, 'ui.studio.panel.parts.name'), { index: index + 1 });
    const base = (object.handle ?? '').replace(/^@/, '') || object.taskId.slice(0, 8);
    control.exportPart(id)
      .then((blob) => saveArchive(blob, partFilename(base, part, fallbackName)))
      .catch(() => toast.error(schemaText(schema, 'ui.studio.panel.parts.export_failed')))
      .finally(clear);
  }, [control, modelState, object.handle, object.taskId, partExportRequest, partsKey, schema]);

  const unknown = schemaText(schema, 'ui.studio.stage.stats.unknown');
  const rows: Array<[string, string]> = [
    [schemaText(schema, 'ui.studio.stage.stats.triangles'), stats ? groupedCount(stats.triangles, activeLocale(schema)) : unknown],
    [schemaText(schema, 'ui.studio.stage.stats.textures'), textureSize ? `${textureSize.width}x${textureSize.height}` : unknown],
  ];

  return (
    <div className="relative min-h-0 min-w-0 flex-1 p-3" data-testid="stage-3d">
      <ModelViewer3D
        key={viewerKey}
        src={object.url}
        alt={schemaText(schema, 'ui.workspace.canvas.model_alt')}
        taskId={object.taskId}
        autoRotate
        className="aspect-auto h-full max-w-none"
        captureLongEdgePixels={schema.canvas.frontend.model_capture_long_edge_pixels}
        onCapture={object.assetId ? captureTo2D : undefined}
        onCaptureError={() => toast.error(schemaText(schema, 'ui.workspace.canvas.capture_failed'))}
        resetLabel={schemaText(schema, 'ui.viewer.reset_view')}
        resetShortcut={descriptorCaps(`mod+${schema.canvas.frontend.keyboard.recenter_key}`, (key) => schemaText(schema, key))}
        resetPlacement="bottom-left"
        viewMode={viewMode}
        onControl={setControl}
        onStats={setStats}
        onTextureSize={(size) => setTexture({ key: measuredKey, size })}
        onParts={(parts) => useStudioStore.getState().setStageParts(parts ? { key: partsKey, parts } : null)}
        selectedPart={partsActive ? selectedPart : null}
        onSelectPart={partsActive ? (id) => useStudioStore.getState().setSelectedPart(id ? { key: partsKey, id } : null) : undefined}
      />
      <Stage3DViews mode={viewMode} onChange={(mode) => setView({ key: measuredKey, mode })} loaded={control?.ready ?? false} supported={control?.viewModes ?? false} />
      <Surface variant="frosted" radius="lg" padding="sm" elevation="sm" className="absolute bottom-6 right-6 z-float" data-testid="stage-model-stats">
        <dl className="flex gap-4 text-xs">
          {rows.map(([label, value]) => (
            <div key={label} className="flex items-baseline gap-1.5">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="font-semibold tabular-nums">{value}</dd>
            </div>
          ))}
        </dl>
      </Surface>
      {capturing ? (
        <p role="status" className="absolute bottom-6 left-1/2 z-float -translate-x-1/2 rounded-full bg-panel-raised px-3 py-1 text-xs shadow-lg">
          {schemaText(schema, 'ui.workspace.canvas.capturing')}
        </p>
      ) : null}
    </div>
  );
}
