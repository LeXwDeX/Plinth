'use client';

import { useCallback, useEffect, useMemo, useRef } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useObjectActions } from '@/hooks/use-object-actions';
import { tileOfObject, useSessionVersions } from '@/hooks/use-session-versions';
import { slotKey, type CompareSlot } from '@/lib/workspace/compare';
import type { LightboxItem } from '@/lib/workspace/lightbox';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { keyBindingsOf, resolveKey, type DispatchContext } from '@/lib/workspace/key-dispatch';
import { createSpaceGesture } from '@/lib/workspace/stage-keys';
import { neighbourTile } from '@/lib/workspace/stage-viewer';
import { selectionForSession } from '@/lib/workspace/session-scope';
import { objectOfTile } from '@/lib/workspace/version-object';
import { versionLabel } from '@/lib/workspace/version-label';
import type { VersionTile } from '@/lib/workspace/versions';
import { useCanvasStore } from '@/store/canvas-store';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';

/**
 * Large view, compare and version stepping for the stage. The picture shown for a
 * version is the one on the stage when it is the current version, otherwise the
 * version's own (first or front) picture, the same one a click on the strip opens.
 */
export function useStageViewer() {
  const { schema } = useProductSchema();
  const { proxyAssetUrl } = useAssetURLRewriter();
  const { openOnStage } = useObjectActions();
  const sessionId = useChatStore((state) => state.activeSessionId);
  const current = selectionForSession(useWorkspaceStore((state) => state.currentObject), sessionId);
  const versions = useSessionVersions();
  const currentTile = tileOfObject(versions, current);

  /** The object a version shows on the stage (its current picture when it is the current version). */
  const objectOf = useCallback((tile: VersionTile): CurrentObject | null => {
    if (!sessionId) return null;
    return currentTile?.key === tile.key && current ? current : objectOfTile(tile, sessionId);
  }, [current, currentTile?.key, sessionId]);

  const itemOf = useCallback((tile: VersionTile): LightboxItem | null => {
    const object = objectOf(tile);
    if (!object || object.kind !== 'image') return null;
    const url = proxyAssetUrl(object.url, object.taskId);
    return url ? { key: `${object.taskId}:${object.assetId ?? object.url}`, url, label: versionLabel(schema, tile, 'image'), downloadUrl: url } : null;
  }, [objectOf, proxyAssetUrl, schema]);

  const slotOf = useCallback((tile: VersionTile, candidate?: { taskId: string; assetId: string; url: string; label?: string }): CompareSlot | null => {
    if (candidate) {
      const url = proxyAssetUrl(candidate.url, candidate.taskId);
      return url ? { key: slotKey(candidate.taskId, candidate.assetId), taskId: candidate.taskId, assetId: candidate.assetId, url, label: [versionLabel(schema, tile, 'image'), candidate.label].filter(Boolean).join(' · ') } : null;
    }
    const object = objectOf(tile);
    if (!object) return null;
    const url = proxyAssetUrl(object.url, object.taskId);
    if (!url) return null;
    // A model goes in as itself: its pane turns it, in step with the other panes.
    return { key: slotKey(object.taskId, object.assetId ?? object.url), taskId: object.taskId, assetId: object.assetId ?? '', url, label: versionLabel(schema, tile, object.kind), kind: object.kind };
  }, [objectOf, proxyAssetUrl, schema]);

  const stageItems = useMemo(
    () => versions.tiles.filter((tile) => tile.status === 'ready' && tile.domain === 'image').flatMap((tile) => {
      const item = itemOf(tile);
      return item ? [{ tile, item }] : [];
    }),
    [itemOf, versions.tiles],
  );

  /** The candidates of a version that came out as a group (several pictures of one request). */
  const candidateItemsOf = useCallback((tile: VersionTile): LightboxItem[] => {
    const images = tile.assets.filter((asset) => asset.kind === 'image');
    if (tile.slots.length > 0 || images.length < 2) return [];
    return images.flatMap((asset, index): LightboxItem[] => {
      const url = proxyAssetUrl(asset.url, tile.taskId);
      if (!url) return [];
      const label = [versionLabel(schema, tile, 'image'), formatSchemaText(schemaText(schema, 'ui.studio.strip.candidate_n'), { index: index + 1 })].join(' · ');
      return [{ key: slotKey(tile.taskId, asset.id), url, label, downloadUrl: url, pick: { taskId: tile.taskId, assetId: asset.id } }];
    });
  }, [proxyAssetUrl, schema]);

  /**
   * Large view of the picture on the stage. A version with several candidates opens on
   * its candidates, ←/→ paging through them; any other opens on the image versions.
   */
  const openStageLightbox = useCallback((single?: LightboxItem) => {
    if (single) { useStudioStore.getState().openLightbox([single]); return; }
    if (current?.kind !== 'image') return;
    const candidates = currentTile ? candidateItemsOf(currentTile) : [];
    if (candidates.length > 0) {
      const shown = candidates.findIndex((item) => item.pick?.assetId === current.assetId);
      useStudioStore.getState().openLightbox(candidates, Math.max(0, shown));
      return;
    }
    const index = stageItems.findIndex((entry) => entry.tile.key === currentTile?.key);
    if (index >= 0) { useStudioStore.getState().openLightbox(stageItems.map((entry) => entry.item), index); return; }
    const url = proxyAssetUrl(current.url, current.taskId);
    if (url) useStudioStore.getState().openLightbox([{ key: current.url, url, label: versionLabel(schema, currentTile, 'image'), downloadUrl: url }]);
  }, [candidateItemsOf, current, currentTile, proxyAssetUrl, schema, stageItems]);

  /** Show the previous or next finished version on the stage. */
  const stepStage = useCallback((delta: number) => {
    if (!sessionId) return;
    const next = neighbourTile(versions.tiles, currentTile?.key ?? null, delta);
    const object = next ? objectOf(next) : null;
    if (object) openOnStage(object);
  }, [currentTile?.key, objectOf, openOnStage, sessionId, versions.tiles]);

  /** A strip tile joins or leaves the comparison (a picture or a model). Returns false when it has no file to show. */
  const toggleTileInCompare = useCallback((tile: VersionTile): boolean => {
    const slot = slotOf(tile);
    if (!slot) {
      // Not taken: the comparison says why, so the click is never silent.
      useStudioStore.getState().setCompareNotice('unavailable');
      return false;
    }
    useStudioStore.getState().setCompareNotice(null);
    useStudioStore.getState().toggleCompareSlot(slot);
    return true;
  }, [slotOf]);

  const startCompare = useCallback(() => {
    const seed = currentTile && current ? slotOf(currentTile) : null;
    useStudioStore.getState().startCompare({ mode: 'two', slots: seed ? [seed] : [] });
  }, [current?.kind, currentTile, slotOf]);

  return { openStageLightbox, stepStage, toggleTileInCompare, startCompare, slotOf, itemOf, current, currentTile };
}

const OPEN_POPUP = '[data-base-ui-portal] [role=dialog][data-open], [data-base-ui-portal] [role=menu][data-open], [data-base-ui-portal] [role=listbox][data-open]';

/** True while an anchored popup (popover, menu, list) is open; it owns Esc. */
function openPopupExists(): boolean {
  return typeof document !== 'undefined' && document.querySelector(OPEN_POPUP) !== null;
}

/**
 * Window-level shortcuts, mounted once in the shell and decided by the key dispatcher (lib/workspace/key-dispatch): Space (a tap) opens and
 * closes the large view, ←/→ step through versions, Esc leaves the large view, then
 * compare. Nothing fires while the focus is in a text field.
 */
export function useStudioShortcuts() {
  const { schema } = useProductSchema();
  const viewer = useStageViewer();
  const viewerRef = useRef(viewer);
  viewerRef.current = viewer;
  const sessionId = useChatStore((state) => state.activeSessionId);

  // What belongs to one session's stage does not carry over to another.
  useEffect(() => {
    useStudioStore.getState().closeLightbox();
    useStudioStore.getState().endCompare();
  }, [sessionId]);

  useEffect(() => {
    const bindings = keyBindingsOf(schema);
    const gesture = createSpaceGesture();
    const toggleLightbox = () => {
      const studio = useStudioStore.getState();
      if (studio.lightbox) studio.closeLightbox();
      else if (viewerRef.current.current?.kind === 'image') viewerRef.current.openStageLightbox();
    };
    const contextNow = (): DispatchContext => {
      const studio = useStudioStore.getState();
      const canvas = useCanvasStore.getState();
      return {
        resultDetailOpen: studio.resultDetail !== null,
        shortcutsOpen: studio.shortcutsOpen,
        lightboxOpen: studio.lightbox !== null,
        compareOpen: studio.compare !== null,
        popupOpen: openPopupExists(),
        surface: viewerRef.current.current?.kind === 'model' ? 'model' : 'canvas',
        canvasTool: canvas.activeTool,
        canvasSelection: canvas.selectedNodeIds.length,
      };
    };
    const onKeyDown = (event: KeyboardEvent) => {
      const resolved = resolveKey(event, contextNow(), bindings);
      if (resolved === null) return;
      const studio = useStudioStore.getState();
      const { command } = resolved;
      switch (command.type) {
        case 'lightbox-gesture':
          // Taps only: a press that turns into a canvas pan must not open anything.
          if (!event.repeat) gesture.press();
          break;
        case 'lightbox-step': studio.stepLightbox(command.delta); break;
        case 'lightbox-close': studio.closeLightbox(); break;
        case 'detail-step': studio.stepResultDetail(command.delta); break;
        case 'stage-step': viewerRef.current.stepStage(command.delta); break;
        case 'compare-end': studio.endCompare(); break;
        case 'view-reset': studio.requestViewReset(); break;
        case 'shortcuts-toggle': studio.setShortcutsOpen(!studio.shortcutsOpen); break;
      }
      if (resolved.preventDefault) event.preventDefault();
      if (resolved.stopPropagation) event.stopPropagation();
    };
    const onKeyUp = (event: KeyboardEvent) => {
      if (event.code !== bindings.spaceCode) return;
      if (gesture.release() && resolveKey(event, contextNow(), bindings)?.command.type === 'lightbox-gesture') {
        event.preventDefault();
        toggleLightbox();
      }
    };
    const onPointerDown = () => gesture.pointerDown();
    const onBlur = () => gesture.cancel();
    // Capture phase, so the large view can take Esc before the dialog primitive sees it.
    window.addEventListener('keydown', onKeyDown, true);
    window.addEventListener('keyup', onKeyUp);
    window.addEventListener('pointerdown', onPointerDown, true);
    window.addEventListener('blur', onBlur);
    return () => {
      window.removeEventListener('keydown', onKeyDown, true);
      window.removeEventListener('keyup', onKeyUp);
      window.removeEventListener('pointerdown', onPointerDown, true);
      window.removeEventListener('blur', onBlur);
    };
  }, [schema]);
}
