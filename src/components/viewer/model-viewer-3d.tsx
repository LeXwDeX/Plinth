'use client';

// 需要安装: pnpm add @google/model-viewer
import { useEffect, useRef, useState } from 'react';
import type { ModelViewerElement } from '@google/model-viewer';
import { RotateCcw, RotateCw } from 'lucide-react';
import { useProductSchema } from '@/components/product-schema-provider';
import { formatSchemaText, schemaCountMessage } from '@/lib/product-schema';
import { isCurrentLoad, loadProgressOf, resetModelView } from '@/lib/workspace/model-view';
import { incompleteTextureCountOf, modelLoadFailureOf, modelLoadFailureText, recordModelLoadErrors, takeModelLoadError, type ModelLoadFailure } from '@/lib/workspace/model-load-failure';
import { reportClientError } from '@/lib/client-error-report';
import { cn } from '@/lib/utils';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Surface } from '@/components/ui/surface';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { captureLoadedModelHighResolution, type ModelCaptureView } from '@/lib/workspace/model-capture';
import { modelStatsFromGltf, type ModelStats, type TextureSize } from '@/lib/workspace/model-stats';
import { exportPart, listModelParts, partFromHit, setPartHighlight, type ModelPart, type PartExporter } from '@/lib/workspace/model-parts';
import { applyViewMode, hasMaterials, largestTextureSize, type MaterialMemory, type SceneNodeLike, type ViewMode } from '@/lib/workspace/view-mode';

// 声明 model-viewer 自定义元素类型
type ModelViewerAttributes = React.DetailedHTMLProps<
  React.HTMLAttributes<HTMLElement>,
  HTMLElement
> & {
  src?: string;
  alt?: string;
  'auto-rotate'?: boolean | string;
  'camera-controls'?: boolean | string;
  'shadow-intensity'?: string;
  'environment-image'?: string;
  'interaction-prompt'?: string;
  exposure?: string;
  'animation-name'?: string;
  autoplay?: boolean | string;
  style?: React.CSSProperties;
};

declare module 'react' {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace JSX {
    interface IntrinsicElements {
      'model-viewer': ModelViewerAttributes;
    }
  }
}

declare global {
  interface Window {
    ModelViewerElement?: {
      dracoDecoderLocation?: string;
      ktx2TranscoderLocation?: string;
      meshoptDecoderLocation?: string;
    };
  }
}

/** A click this far from where the pointer went down is a camera drag, not a pick. */
const PICK_MOVE_LIMIT_PX = 4;

/** The viewer's three.js scene, as far as picking needs it. */
interface ViewerScene {
  model?: SceneNodeLike | null;
  getNDC(clientX: number, clientY: number): unknown;
  hitFromPoint(ndc: unknown): { object: SceneNodeLike } | null | undefined;
}

/** The viewer keeps its scene behind private symbols, so it is looked up through the module that declares them. */
async function viewerSceneOf(element: ModelViewerElement): Promise<{ scene: ViewerScene; root: SceneNodeLike | null; render: () => void } | null> {
  const base = await import('@google/model-viewer/lib/model-viewer-base.js');
  const holder = element as unknown as Record<symbol, unknown>;
  const scene = holder[base.$scene] as ViewerScene | undefined;
  if (!scene) return null;
  return { scene, root: scene.model ?? null, render: () => (holder[base.$needsRender] as (() => void) | undefined)?.call(element) };
}

/**
 * Drops a failed model from model-viewer's glTF cache. The cache keeps an empty result for a URL whose load
 * failed and hands it to every later load of that URL, so without this 重试 never downloads again (#264).
 * Removing the entry rejects inside model-viewer (it disposes the empty result); that is expected and caught.
 */
function forgetFailedModel(url: string): void {
  if (!url) return;
  void import('@google/model-viewer/lib/three-components/CachingGLTFLoader.js')
    .then(({ CachingGLTFLoader }) => CachingGLTFLoader.delete(url))
    .catch(() => undefined);
}

/**
 * Keeps the original error of every failed model load (see `recordModelLoadErrors`). model-viewer shares one
 * renderer, and so one glTF loader, across all viewers; it is wrapped once, before the first viewer loads.
 * A failed URL is dropped from the cache after model-viewer has finished with it, even when no viewer is
 * listening any more (the viewer was closed or switched to another model while loading).
 */
async function recordViewerLoadErrors(): Promise<void> {
  const [{ Renderer }, { $loader }] = await Promise.all([
    import('@google/model-viewer/lib/three-components/Renderer.js'),
    import('@google/model-viewer/lib/three-components/CachingGLTFLoader.js'),
  ]);
  const caching = Renderer.singleton.loader as unknown as Record<symbol, unknown>;
  if (!recordModelLoadErrors(caching[$loader], (url) => { setTimeout(() => forgetFailedModel(url), 0); })) {
    throw new Error('Model loader validation unavailable');
  }
}

/** What the stage needs from a loaded viewer: take a picture, and know when it can. */
export interface ModelViewerControl {
  ready: boolean;
  /** The model failed to load (the viewer shows the failure and 重试). */
  failed: boolean;
  /** How many textures of the loaded model could not be loaded; the model shows with default materials in their place. */
  incompleteTextures: number;
  /** The loaded model's materials can be changed, so 白模 and 线框 can be shown. */
  viewModes: boolean;
  capture: () => Promise<void>;
  /** Puts the camera back to the default framing of the model. */
  resetView: () => void;
  /** Writes one part of the loaded model (by its id from `onParts`) as a GLB. Rejects when it cannot. */
  exportPart: (id: string) => Promise<Blob>;
}

interface ModelViewer3DProps {
  src: string;
  alt?: string;
  autoRotate?: boolean;
  autoplay?: boolean;
  animationName?: string;
  taskId?: string;
  className?: string;
  captureLabel?: string;
  capturingLabel?: string;
  captureLongEdgePixels?: number;
  /** Shows a 复位视角 button on the viewer with this name (and tooltip). */
  resetLabel?: string;
  /** Key caps shown beside the reset button's name in its hover hint. */
  resetShortcut?: readonly string[];
  /** Where that button sits: the top-left corner (default) or the bottom-left, clear of a tool rail floating over the top-left. */
  resetPlacement?: 'top-left' | 'bottom-left';
  onCapture?: (blob: Blob, view: ModelCaptureView) => Promise<void>;
  /** The capture (or its hand-off to `onCapture`) failed; `error` is what was thrown. */
  onCaptureError?: (error: unknown) => void;
  /** Refresh an authenticated preview URL before retrying its load. */
  onRetry?: () => void;
  /** Public recovery copy for a caller that must not display loader diagnostics. */
  failureMessage?: string;
  /** Texture (as made), clay (grey, no pictures) or wires. Applied once the model has loaded. */
  viewMode?: ViewMode;
  /** Receives the viewer's controls (and their readiness) so the stage can drive capture. */
  onControl?: (control: ModelViewerControl | null) => void;
  /** Receives the numbers read from the loaded model; null when the file carries none. */
  onStats?: (stats: ModelStats | null) => void;
  /** Receives the largest texture size of the loaded model; null when it has none or failed to load. */
  onTextureSize?: (size: TextureSize | null) => void;
  /** Receives the model's parts once loaded; null while it is not. */
  onParts?: (parts: ModelPart[] | null) => void;
  /** The part to light up on the model; null for none. */
  selectedPart?: string | null;
  /** When given, a click on the model picks the part under the pointer (null on empty space). */
  onSelectPart?: (id: string | null) => void;
  /** Receives the viewer element once mounted (null when it goes), for callers that follow its camera. */
  onElement?: (element: ModelViewerElement | null) => void;
}

export function ModelViewer3D({
  src,
  alt = '3D Model',
  autoRotate = true,
  autoplay = false,
  animationName,
  taskId,
  className,
  captureLabel,
  capturingLabel,
  captureLongEdgePixels,
  resetLabel,
  resetShortcut,
  resetPlacement = 'top-left',
  onCapture,
  onCaptureError,
  onRetry,
  failureMessage,
  viewMode = 'texture',
  onControl,
  onStats,
  onTextureSize,
  onParts,
  selectedPart = null,
  onSelectPart,
  onElement,
}: ModelViewer3DProps) {
  const { proxyAssetUrl } = useAssetURLRewriter();
  const { text, schema } = useProductSchema();
  const [attempt, setAttempt] = useState(0);
  const [progress, setProgress] = useState(0);
  const [failure, setFailure] = useState<ModelLoadFailure | null>(null);
  const elementRef = useRef<ModelViewerElement | null>(null);
  const captureInProgress = useRef(false);
  const [moduleReady, setModuleReady] = useState(false);
  const [ready, setReady] = useState(false);
  const [incompleteTextures, setIncompleteTextures] = useState(0);
  const [capturing, setCapturing] = useState(false);
  const [viewModes, setViewModes] = useState(false);
  const materialMemory = useRef(new WeakMap() as MaterialMemory);
  const onStatsRef = useRef(onStats);
  onStatsRef.current = onStats;
  const onTextureSizeRef = useRef(onTextureSize);
  onTextureSizeRef.current = onTextureSize;
  const onControlRef = useRef(onControl);
  onControlRef.current = onControl;
  // Read through a ref so an inline callback from the caller cannot re-publish the control every render.
  const onCaptureRef = useRef(onCapture);
  onCaptureRef.current = onCapture;
  const hasCapture = Boolean(onCapture);
  const onElementRef = useRef(onElement);
  onElementRef.current = onElement;
  const onPartsRef = useRef(onParts);
  onPartsRef.current = onParts;
  const onSelectPartRef = useRef(onSelectPart);
  onSelectPartRef.current = onSelectPart;
  const selectedPartRef = useRef(selectedPart);
  selectedPartRef.current = selectedPart;
  const viewModeRef = useRef(viewMode);
  viewModeRef.current = viewMode;
  const incompleteTexturesRef = useRef(incompleteTextures);
  incompleteTexturesRef.current = incompleteTextures;
  const picking = onSelectPart !== undefined;

  useEffect(() => {
    if (moduleReady) return;
    let active = true;
    // Each custom element constructor reads the global loader configuration.
    // Set it before registration/mount so new elements cannot restore CDN defaults.
    window.ModelViewerElement = {
      ...window.ModelViewerElement,
      dracoDecoderLocation: '/model-viewer/draco/',
      ktx2TranscoderLocation: '/model-viewer/basis/',
      meshoptDecoderLocation: '/meshopt-decoder-bootstrap.js',
    };
    import('@google/model-viewer').then(async ({ ModelViewerElement }) => {
      // This version bundles MeshoptDecoder but only registers it with the
      // glTF loader after a same-origin decoder-location script has loaded.
      ModelViewerElement.meshoptDecoderLocation = '/meshopt-decoder-bootstrap.js';
      ModelViewerElement.dracoDecoderLocation = '/model-viewer/draco/';
      ModelViewerElement.ktx2TranscoderLocation = '/model-viewer/basis/';
      await recordViewerLoadErrors();
      if (active) setModuleReady(true);
    }).catch(() => {
      if (active) {
        reportClientError('model-viewer-load:unknown', 'Model viewer initialization failed');
        setReady(false);
        setFailure({ kind: 'unknown', detail: 'Model viewer initialization failed' });
      }
    });
    return () => { active = false; };
  }, [attempt, moduleReady]);

  useEffect(() => {
    const element = elementRef.current;
    if (!element) return;
    // Listeners go when the source changes or the viewer unmounts; `active` also stops a handler that is
    // already running from writing state for a source that is no longer shown.
    let active = true;
    setProgress(0);
    setFailure(null);
    setIncompleteTextures(0);
    const onProgress = (event: Event) => {
      if (!active) return;
      const value = loadProgressOf(event);
      if (value !== null) setProgress(value);
    };
    const onLoad = (event: Event) => {
      if (!active || !isCurrentLoad(event, element)) return;
      setReady(true);
      setFailure(null);
      // Textures that could not be loaded leave the geometry showing with default materials: a notice, not a failure.
      const incomplete = incompleteTextureCountOf((element as unknown as { originalGltfJson?: unknown }).originalGltfJson);
      setIncompleteTextures(incomplete);
      if (incomplete > 0) reportClientError('model-viewer-load:textures-incomplete', `${incomplete} texture(s) could not be loaded`);
      onStatsRef.current?.(modelStatsFromGltf((element as unknown as { originalGltfJson?: unknown }).originalGltfJson));
    };
    const onError = (event: Event) => {
      if (!active) return;
      const url = element.src ?? '';
      const found = modelLoadFailureOf(event, takeModelLoadError(url), navigator.onLine !== false);
      forgetFailedModel(url);
      // #233: the real cause goes to the server log; the viewer shows only the Schema sentence for its kind.
      reportClientError(`model-viewer-load:${found.kind}${found.status ? `:${found.status}` : ''}`, found.detail || found.kind, { requestId: found.requestId });
      setReady(false);
      setIncompleteTextures(0);
      setFailure(found);
      onStatsRef.current?.(null);
      onTextureSizeRef.current?.(null);
    };
    element.addEventListener('progress', onProgress);
    element.addEventListener('load', onLoad);
    element.addEventListener('error', onError);
    setReady(element.loaded);
    return () => {
      active = false;
      element.removeEventListener('progress', onProgress);
      element.removeEventListener('load', onLoad);
      element.removeEventListener('error', onError);
    };
  }, [moduleReady, src, attempt]);

  const capture = async () => {
    const element = elementRef.current;
    const onCaptureNow = onCaptureRef.current;
    if (!element || !onCaptureNow || !captureLongEdgePixels || captureInProgress.current || incompleteTexturesRef.current > 0) return;
    captureInProgress.current = true;
    setCapturing(true);
    // The part highlight is a stage affordance, not the model: it stays out of the picture.
    const found = await viewerSceneOf(element).catch(() => null);
    try {
      const { ModelViewerElement } = await import('@google/model-viewer');
      if (found?.root) setPartHighlight(found.root, null);
      // Stop auto-rotation before readback so the recorded camera describes
      // the same pixels that are uploaded as the 2D asset.
      element.removeAttribute('auto-rotate');
      await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      const result = await captureLoadedModelHighResolution(
        element, getComputedStyle(element).backgroundColor, captureLongEdgePixels,
        window.devicePixelRatio || 1,
        ModelViewerElement,
      );
      await onCaptureNow(result.blob, result.view);
    } catch (error) {
      onCaptureError?.(error);
    } finally {
      if (found?.root) { setPartHighlight(found.root, selectedPartRef.current); found.render(); }
      if (autoRotate) element.setAttribute('auto-rotate', '');
      setCapturing(false);
      captureInProgress.current = false;
    }
  };

  // 白模 / 线框: the viewer keeps its three.js scene behind a private key, so it is looked up once the model is in.
  useEffect(() => {
    const element = elementRef.current;
    if (!element || !ready) { setViewModes(false); return; }
    let active = true;
    void import('@google/model-viewer/lib/model-viewer-base.js').then((base) => {
      if (!active) return;
      const holder = element as unknown as Record<symbol, unknown>;
      // Only the loaded model: the viewer's floor shadow and skybox sit next to it and are not part of it.
      const scene = holder[base.$scene] as { model?: SceneNodeLike | null } | undefined;
      const root = scene?.model ?? null;
      onTextureSizeRef.current?.(root ? largestTextureSize(root, materialMemory.current) : null);
      if (!root || !hasMaterials(root)) { setViewModes(false); return; }
      applyViewMode(root, viewMode, materialMemory.current);
      // The highlight goes on after the mode, so it is never taken for part of the model.
      setPartHighlight(root, selectedPartRef.current);
      (holder[base.$needsRender] as (() => void) | undefined)?.call(element);
      setViewModes(true);
    }).catch(() => { if (active) setViewModes(false); });
    return () => { active = false; };
  }, [ready, src, viewMode]);

  // The parts of the loaded model, for a list outside the viewer.
  useEffect(() => {
    const element = elementRef.current;
    if (!element || !ready) return;
    let active = true;
    void viewerSceneOf(element).then((found) => {
      if (active) onPartsRef.current?.(found?.root ? listModelParts(found.root) : null);
    }).catch(() => { if (active) onPartsRef.current?.(null); });
    return () => { active = false; onPartsRef.current?.(null); };
  }, [ready, src]);

  // The chosen part is tinted on the model.
  useEffect(() => {
    const element = elementRef.current;
    if (!element || !ready) return;
    let active = true;
    void viewerSceneOf(element).then((found) => {
      if (!active || !found?.root) return;
      setPartHighlight(found.root, selectedPart);
      found.render();
    }).catch(() => undefined);
    return () => { active = false; };
  }, [ready, src, selectedPart]);

  // A click (not a drag of the camera) picks the part under the pointer.
  useEffect(() => {
    const element = elementRef.current;
    if (!element || !ready || !picking) return;
    let down: { x: number; y: number } | null = null;
    const onDown = (event: PointerEvent) => { down = event.button === 0 ? { x: event.clientX, y: event.clientY } : null; };
    const onUp = (event: PointerEvent) => {
      const start = down;
      down = null;
      if (!start || Math.hypot(event.clientX - start.x, event.clientY - start.y) >= PICK_MOVE_LIMIT_PX) return;
      const { clientX, clientY } = event;
      void viewerSceneOf(element).then((found) => {
        if (!found?.root) return;
        const hit = found.scene.hitFromPoint(found.scene.getNDC(clientX, clientY));
        const id = hit ? partFromHit(hit.object) : null;
        onSelectPartRef.current?.(id !== null && listModelParts(found.root).some((part) => part.id === id) ? id : null);
      }).catch(() => undefined);
    };
    element.addEventListener('pointerdown', onDown);
    element.addEventListener('pointerup', onUp);
    return () => {
      element.removeEventListener('pointerdown', onDown);
      element.removeEventListener('pointerup', onUp);
    };
  }, [ready, src, picking]);

  const exportPartOf = async (id: string): Promise<Blob> => {
    const element = elementRef.current;
    const found = element ? await viewerSceneOf(element) : null;
    if (!element || !found?.root || incompleteTexturesRef.current > 0) throw new Error('model is not loaded');
    try {
      return await exportPart(element as unknown as PartExporter, found.root, id, materialMemory.current, viewModeRef.current);
    } finally {
      found.render();
    }
  };
  const exportPartRef = useRef(exportPartOf);
  exportPartRef.current = exportPartOf;

  const captureRef = useRef(capture);
  captureRef.current = capture;
  const resetView = () => { resetModelView(elementRef.current as unknown as Parameters<typeof resetModelView>[0]); };
  const resetViewRef = useRef(resetView);
  resetViewRef.current = resetView;
  useEffect(() => {
    onControlRef.current?.({ ready, failed: failure !== null, incompleteTextures, viewModes, capture: () => captureRef.current(), resetView: () => resetViewRef.current(), exportPart: (id) => exportPartRef.current(id) });
  }, [ready, failure, incompleteTextures, viewModes, hasCapture, captureLongEdgePixels]);
  useEffect(() => () => onControlRef.current?.(null), []);
  useEffect(() => {
    if (!moduleReady) return;
    onElementRef.current?.(elementRef.current);
    return () => onElementRef.current?.(null);
  }, [moduleReady, attempt]);

  return (
    <Surface
      variant="canvas"
      radius="lg"
      className={cn(
        'relative w-full aspect-square overflow-hidden',
        className
      )}
    >
        {moduleReady ? (
          <model-viewer
            key={attempt}
            ref={elementRef}
            src={proxyAssetUrl(src, taskId) || undefined}
            alt={alt}
            auto-rotate={autoRotate && !capturing ? '' : undefined}
            camera-controls=""
            interaction-prompt="none"
            shadow-intensity="1.5"
            environment-image="neutral"
            animation-name={animationName}
            autoplay={(autoplay || animationName) ? '' : undefined}
            style={{ width: '100%', height: '100%', backgroundColor: 'var(--canvas)' }}
          />
        ) : null}
      {/* A neutral floor under the model; it sits over the viewer so the capture stays on the canvas colour. */}
      <div aria-hidden className="pointer-events-none absolute inset-x-0 bottom-0 h-1/3 bg-gradient-to-t from-panel/60 to-transparent" />
      {!ready && failure === null ? (
        <div role="status" className="absolute inset-0 flex flex-col items-center justify-center gap-2 text-xs text-muted-foreground" data-testid="model-viewer-loading">
          <span>{progress > 0 ? formatSchemaText(text('ui.viewer.loading_progress'), { percent: Math.round(progress * 100) }) : text('ui.viewer.loading')}</span>
          <span className="h-1 w-32 overflow-hidden rounded-full bg-muted" aria-hidden>
            <span className="block h-full rounded-full bg-primary transition-[width]" style={{ width: `${Math.round(progress * 100)}%` }} />
          </span>
        </div>
      ) : null}
      {ready && incompleteTextures > 0 ? (
        <p role="status" className="pointer-events-none absolute inset-x-3 top-3 mx-auto w-fit max-w-full rounded-full bg-panel-raised px-3 py-1 text-center text-xs text-muted-foreground shadow-lg" data-testid="model-viewer-textures-incomplete">
          {schemaCountMessage(schema, 'ui.viewer.textures_incomplete', incompleteTextures, { count: incompleteTextures })}
        </p>
      ) : null}
      {failure !== null ? (
        <div role="alert" className="absolute inset-0 flex flex-col items-center justify-center gap-2 bg-canvas/90 p-4 text-center text-xs" data-testid="model-viewer-error">
          <p className="font-medium text-destructive">{text('ui.viewer.load_failed')}</p>
          <p className="max-w-full text-muted-foreground [overflow-wrap:anywhere]">{failureMessage ?? modelLoadFailureText(failure, text)}</p>
          <Action type="button" variant="outline" size="sm" onClick={() => { onRetry?.(); setFailure(null); setProgress(0); setAttempt((value) => value + 1); }} data-testid="model-viewer-retry">
            <RotateCw />{text('ui.viewer.retry')}
          </Action>
        </div>
      ) : null}
      {resetLabel && ready ? (
        <HintTooltip name={resetLabel} shortcut={resetShortcut} className={cn('absolute left-3', resetPlacement === 'bottom-left' ? 'bottom-3' : 'top-3')}>
          <Action type="button" variant="media" size="icon-xs" onClick={resetView} aria-label={resetLabel} data-testid="model-viewer-reset">
            <RotateCcw />
          </Action>
        </HintTooltip>
      ) : null}
      {onCapture && captureLabel && (
        <Action type="button" variant="raised" size="lg" disabled={!ready || incompleteTextures > 0} loading={capturing} onClick={() => void capture()}
          className="absolute right-3 bottom-3">
          {capturing ? capturingLabel || captureLabel : captureLabel}
        </Action>
      )}
    </Surface>
  );
}
