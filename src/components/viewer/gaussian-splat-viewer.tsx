'use client';

import { useEffect, useRef, useState } from 'react';
import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { SparkRenderer, SplatFileType, SplatMesh } from '@sparkjsdev/spark';

import { parseGaussianSplat, readGaussianSplatResponse } from '@/lib/workspace/gaussian-splat';
import { GaussianSplatReadyGate } from '@/lib/workspace/gaussian-splat-ready';
import { cn } from '@/lib/utils';

const READY_DEADLINE_MS = 30_000;
const TEARDOWN_DEADLINE_MS = 2_000;
const BACKGROUND = [21, 23, 28];

export interface GaussianSplatViewerProps {
  src: string;
  alt: string;
  loadingText: string;
  failedText: string;
  retryText: string;
  resetText: string;
  /** Overrides the default aspect-video frame when the host already owns a fixed box. */
  className?: string;
}

type ViewerState = 'loading' | 'ready' | 'failed';

/** A bounded, local-only viewer for the declared Tripo .splat representation. */
export function GaussianSplatViewer({ src, alt, loadingText, failedText, retryText, resetText, className }: GaussianSplatViewerProps) {
  const hostRef = useRef<HTMLDivElement>(null);
  const resetViewRef = useRef<() => void>(() => undefined);
  const [state, setState] = useState<ViewerState>('loading');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const host = hostRef.current;
    if (!host || !src) return;
    let disposed = false;
    let timeout: ReturnType<typeof setTimeout> | undefined;
    let renderer: THREE.WebGLRenderer | undefined;
    let controls: OrbitControls | undefined;
    let splat: SplatMesh | undefined;
    let spark: SparkRenderer | undefined;
    let resizeObserver: ResizeObserver | undefined;
    let framePending = false;
    let frameId: number | undefined;
    let renderTask: Promise<void> | undefined;
    let renderAgain = false;
    let updatingSpark = false;
    let teardownTask: Promise<void> | undefined;
    let initializationTask: Promise<SplatMesh> | undefined;
    let lastPixelSample = -Infinity;
    let pixelSampleTimer: number | undefined;
    const startedAt = performance.now();
    const readyGate = new GaussianSplatReadyGate(startedAt, READY_DEADLINE_MS);
    const controller = new AbortController();
    const canvas = document.createElement('canvas');
    canvas.width = 40;
    canvas.height = 40;
    const sampleContext = canvas.getContext('2d', { willReadFrequently: true });
    const scene = new THREE.Scene();
    scene.background = new THREE.Color('#1c1d1f');
    const camera = new THREE.PerspectiveCamera(50, 1, 0.01, 100);
    const initialCamera = new THREE.Vector3();
    const initialTarget = new THREE.Vector3();

    setState('loading');
    const fail = () => {
      if (disposed || !readyGate.fail()) return;
      setState('failed');
      void teardown();
    };
    const renderOnce = () => {
      framePending = false;
      frameId = undefined;
      if (disposed || !readyGate.canContinue || !renderer || !controls || renderTask) return;
      const task = (async () => {
        if (readyGate.state === 'pending' && performance.now() - startedAt >= READY_DEADLINE_MS) { fail(); return; }
        controls.update();
        // Spark's automatic update starts an unhandled Promise from onBeforeRender.
        // Drive the public update API ourselves so sorting failures reach fail().
        if (spark && splat?.isInitialized && splat.parent === scene) {
          updatingSpark = true;
          try { await spark.update({ scene, camera }); }
          finally { updatingSpark = false; }
        }
        if (disposed || !readyGate.canContinue) return;
        renderer.render(scene, camera);
        if (readyGate.state === 'pending' && performance.now() - startedAt >= READY_DEADLINE_MS) { fail(); return; }
        if (readyGate.state === 'ready' || !sampleContext || !spark || !splat || !splat.isInitialized
          || splat.numSplats === 0 || spark.activeSplats === 0) return;
        const elapsedSinceSample = performance.now() - lastPixelSample;
        if (elapsedSinceSample < 500) {
          if (pixelSampleTimer === undefined) {
            pixelSampleTimer = window.setTimeout(() => {
              pixelSampleTimer = undefined;
              scheduleRender();
            }, 500 - elapsedSinceSample);
          }
          return;
        }
        lastPixelSample = performance.now();
        sampleContext.drawImage(renderer.domElement, 0, 0, canvas.width, canvas.height);
        if (performance.now() - startedAt >= READY_DEADLINE_MS) { fail(); return; }
        const pixels = sampleContext.getImageData(0, 0, canvas.width, canvas.height).data;
        if (performance.now() - startedAt >= READY_DEADLINE_MS) { fail(); return; }
        for (let i = 0; i < pixels.length; i += 4) {
          const distanceFromBackground = Math.abs(pixels[i] - BACKGROUND[0])
            + Math.abs(pixels[i + 1] - BACKGROUND[1]) + Math.abs(pixels[i + 2] - BACKGROUND[2]);
          if (pixels[i + 3] > 0 && distanceFromBackground > 24) {
            if (!readyGate.markReady(performance.now())) { fail(); return; }
            if (timeout) clearTimeout(timeout);
            setState('ready');
            return;
          }
        }
        pixelSampleTimer = window.setTimeout(() => {
          pixelSampleTimer = undefined;
          scheduleRender();
        }, 500);
      })();
      renderTask = task;
      void task.catch(fail).finally(() => {
        if (renderTask === task) renderTask = undefined;
        if (disposed) {
          // A readback that outlives bounded teardown can still finish sorting
          // and recreate Spark's worker/ordering texture. Release those late
          // allocations after the first cleanup, even when update succeeds.
          void teardown().then(() => {
            try { spark?.dispose(); } catch { /* continue releasing graphics */ }
            try { renderer?.dispose(); } catch { /* already released */ }
          });
          return;
        }
        if (renderAgain) {
          renderAgain = false;
          scheduleRender();
        }
      });
    };
    const scheduleRender = () => {
      if (disposed || !readyGate.canContinue) return;
      if (renderTask) { renderAgain = true; return; }
      if (!framePending) {
        framePending = true;
        frameId = requestAnimationFrame(renderOnce);
      }
    };
    const resize = () => {
      if (!renderer || !host.clientWidth || !host.clientHeight) return;
      camera.aspect = host.clientWidth / host.clientHeight;
      camera.updateProjectionMatrix();
      renderer.setSize(host.clientWidth, host.clientHeight, false);
      scheduleRender();
    };
    const onContextLost = (event: Event) => { event.preventDefault(); fail(); };

    const teardown = (): Promise<void> => {
      if (teardownTask) return teardownTask;
      const teardownStartedAt = performance.now();
      disposed = true;
      resetViewRef.current = () => undefined;
      controller.abort();
      if (timeout) clearTimeout(timeout);
      if (pixelSampleTimer !== undefined) window.clearTimeout(pixelSampleTimer);
      pixelSampleTimer = undefined;
      if (frameId !== undefined) cancelAnimationFrame(frameId);
      frameId = undefined;
      framePending = false;
      renderAgain = false;
      resizeObserver?.disconnect();
      renderer?.domElement.removeEventListener('webglcontextlost', onContextLost);
      controls?.removeEventListener('change', scheduleRender);
      try { controls?.dispose(); } catch { /* continue releasing graphics */ }
      try { renderer?.setAnimationLoop(null); } catch { /* continue releasing graphics */ }
      if (spark) spark.onDirty = undefined;
      const pendingTasks: Promise<unknown>[] = [];
      if (renderTask) pendingTasks.push(renderTask);
      if (initializationTask) pendingTasks.push(initializationTask);
      teardownTask = (async () => {
        if (pendingTasks.length > 0) {
          let deadline: number | undefined;
          try {
            await Promise.race([
              Promise.allSettled(pendingTasks),
              new Promise<void>((resolve) => {
                deadline = window.setTimeout(resolve, Math.max(0, TEARDOWN_DEADLINE_MS - (performance.now() - teardownStartedAt)));
              }),
            ]);
          } finally {
            if (deadline !== undefined) window.clearTimeout(deadline);
          }
        }
        // Each release is independent: one faulty GPU object must not retain the rest.
        try { if (splat) scene.remove(splat); } catch { /* best-effort teardown */ }
        try { splat?.dispose(); } catch { /* best-effort teardown */ }
        try { if (spark) scene.remove(spark); } catch { /* best-effort teardown */ }
        try { spark?.dispose(); } catch { /* best-effort teardown */ }
        try { renderer?.dispose(); } catch { /* best-effort teardown */ }
        try { renderer?.domElement.remove(); } catch { /* continue teardown */ }
      })();
      return teardownTask;
    };

    timeout = setTimeout(fail, READY_DEADLINE_MS);
    void (async () => {
      try {
        const response = await fetch(src, { credentials: 'same-origin', cache: 'no-store', signal: controller.signal });
        const bytes = await readGaussianSplatResponse(response);
        controller.signal.throwIfAborted();
        const parsed = parseGaussianSplat(bytes);
        controller.signal.throwIfAborted();
        if (disposed) return;

        renderer = new THREE.WebGLRenderer({ antialias: false, alpha: false, preserveDrawingBuffer: false });
        renderer.setPixelRatio(1);
        host.appendChild(renderer.domElement);
        renderer.domElement.setAttribute('aria-label', alt);
        renderer.domElement.addEventListener('webglcontextlost', onContextLost);
        controls = new OrbitControls(camera, renderer.domElement);
        controls.enableDamping = false;
        controls.screenSpacePanning = true;
        controls.addEventListener('change', scheduleRender);
        spark = new SparkRenderer({
          renderer,
          autoUpdate: false,
          enableLod: false,
          enableDriveLod: false,
          onDirty: () => { if (!updatingSpark) scheduleRender(); },
        });
        scene.add(spark);
        resize();
        resizeObserver = new ResizeObserver(resize);
        resizeObserver.observe(host);

        splat = new SplatMesh({ fileBytes: bytes, fileType: SplatFileType.SPLAT, lod: false, raycastable: false });
        const loadingSplat = splat;
        initializationTask = loadingSplat.initialized;
        // Disposing an in-progress decode can race its final allocation. Release
        // again when it settles, even if the bounded teardown has already run.
        void initializationTask.finally(() => {
          if (disposed) {
            try { loadingSplat.dispose(); } catch { /* already released */ }
          }
        }).catch(() => undefined);
        // The tested image-to-splat sample is upside down/back-facing at identity. Zπ is only an
        // initial camera/view transform for a useful upright preview; it does not alter source bytes.
        splat.quaternion.setFromAxisAngle(new THREE.Vector3(0, 0, 1), Math.PI);
        await initializationTask;
        if (disposed || !readyGate.canContinue) return;
        if (!splat.isInitialized || splat.numSplats !== parsed.pointCount) throw new Error('Gaussian Splat count mismatch');
        scene.add(splat);
        splat.updateMatrixWorld(true);
        const bounds = new THREE.Box3(
          new THREE.Vector3(...parsed.bounds.min),
          new THREE.Vector3(...parsed.bounds.max),
        ).applyMatrix4(splat.matrixWorld);
        const center = bounds.getCenter(new THREE.Vector3());
        const size = bounds.getSize(new THREE.Vector3());
        const extent = Math.max(size.x, size.y, size.z);
        if (!Number.isFinite(extent) || extent <= 0) throw new Error('Invalid Gaussian Splat bounds');
        const distance = Math.max(2.2, extent * 2.25);
        controls.target.copy(center);
        camera.up.set(0, 1, 0);
        camera.position.set(center.x, center.y, center.z + distance);
        camera.near = Math.max(0.001, distance / 1000);
        camera.far = distance * 100;
        camera.lookAt(center);
        camera.updateProjectionMatrix();
        controls.update();
        initialCamera.copy(camera.position);
        initialTarget.copy(controls.target);
        resetViewRef.current = () => {
          camera.position.copy(initialCamera);
          controls?.target.copy(initialTarget);
          camera.up.set(0, 1, 0);
          camera.lookAt(initialTarget);
          camera.updateProjectionMatrix();
          controls?.update();
          scheduleRender();
        };
        scheduleRender();
      } catch {
        if (!controller.signal.aborted) fail();
      }
    })();

    return () => { void teardown(); };
  }, [src, attempt, alt]);

  return (
    <div ref={hostRef} className={cn('relative aspect-video min-h-48 w-full overflow-hidden bg-[#1c1d1f]', className)} data-testid="gaussian-splat-viewer" data-state={state}>
      {state === 'loading' ? <p className="absolute inset-0 z-10 flex items-center justify-center bg-[#1c1d1f]/85 p-4 text-sm text-white" role="status">{loadingText}</p> : null}
      {state === 'failed' ? (
        <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-[#1c1d1f]/90 p-4 text-center text-sm text-white" role="alert">
          <p>{failedText}</p>
          <button type="button" className="rounded border border-white/40 px-3 py-1.5 focus-visible:outline-2 focus-visible:outline-white" onClick={() => setAttempt((value) => value + 1)}>{retryText}</button>
        </div>
      ) : null}
      {state === 'ready' ? (
          <button type="button" className="absolute right-2 top-2 z-10 rounded border border-white/30 bg-black/60 px-2 py-1 text-xs text-white focus-visible:outline-2 focus-visible:outline-white" onClick={() => resetViewRef.current()}>{resetText}</button>
      ) : null}
    </div>
  );
}
