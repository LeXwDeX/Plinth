'use client';

import { safeAssetURL } from '@/lib/core/tasks/asset-url-rewriter';
import { lazy, Suspense, useEffect, useMemo, useRef } from 'react';
import { X } from 'lucide-react';

import { RegionErrorBoundary } from '@/components/common/region-error-boundary';
import { useProductSchema } from '@/components/product-schema-provider';
import { AllOutputs } from '@/components/studio/all-outputs';
import { CompareView } from '@/components/studio/compare-view';
import { Stage3D } from '@/components/studio/stage-3d';
import { StageHeader } from '@/components/studio/stage-header';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { useSessionPick } from '@/hooks/use-session-pick';
import { useSessionVersions } from '@/hooks/use-session-versions';
import { createChatBrowserClient } from '@/lib/chat/browser-client';
import { readLastObject, restoreTarget, saveLastObject } from '@/lib/workspace/last-object';
import { schemaText } from '@/lib/product-schema';
import { isModelSlot, type CompareSlot } from '@/lib/workspace/compare';
import { selectionForSession } from '@/lib/workspace/session-scope';
import { objectOfTile } from '@/lib/workspace/version-object';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore } from '@/store/workspace-store';

const WorkspaceCanvasBoard = lazy(() => import('@/components/workspace/workspace-canvas-board').then((module) => ({ default: module.WorkspaceCanvasBoard })));

/**
 * 中间的舞台：显示当前对象，按它的种类选 2D 画板还是 3D 查看器，没有任何版本时
 * 是带起步卡片的空画板。不再有 2D / 3D 标签：谁在舞台上由当前对象决定。
 */
export function Stage() {
  const { schema } = useProductSchema();
  const sessionId = useChatStore((state) => state.activeSessionId);
  const object = selectionForSession(useWorkspaceStore((state) => state.currentObject), sessionId);
  const pending = useWorkspaceStore((state) => state.pendingResult);
  const openPending = useWorkspaceStore((state) => state.openPendingResult);
  const dismissPending = useWorkspaceStore((state) => state.dismissPendingResult);
  const allOutputsOpen = useStudioStore((state) => state.allOutputsOpen);
  const compare = useStudioStore((state) => state.compare);
  const openLightbox = useStudioStore((state) => state.openLightbox);
  const setCurrentObject = useWorkspaceStore((state) => state.setCurrentObject);
  const versions = useSessionVersions();
  const chat = useMemo(() => createChatBrowserClient(schema), [schema]);
  const sessionPick = useSessionPick(chat, sessionId);

  // The large view shows pictures; a model pane has no enlarge, so compared models are left out here.
  const enlargeCompare = (slots: readonly CompareSlot[], index: number) => {
    const pictures = slots.filter((slot) => !isModelSlot(slot));
    const shown = pictures.findIndex((slot) => slot.key === slots[index]?.key);
    if (shown < 0) return;
    openLightbox(pictures.map((slot) => ({ key: slot.key, url: safeAssetURL(slot.url), label: slot.label, downloadUrl: safeAssetURL(slot.url), ...(slot.assetId ? { pick: { taskId: slot.taskId, assetId: slot.assetId } } : {}) })), shown);
  };

  // What is on the stage is remembered per session in this browser, so coming back later lands on it.
  useEffect(() => {
    if (object && sessionId && object.sessionId === sessionId) saveLastObject(sessionId, object);
  }, [object, sessionId]);

  // A session opens on the object it was last on; failing that on the version picked on the server,
  // then on its newest finished version, instead of an empty stage.
  // The first attempt per session is the opening; a later pick is a result that arrived while the
  // person was working, which must not take the selection or move the view (#262).
  const openedSession = useRef<string | null>(null);
  useEffect(() => {
    if (object || !sessionId || versions.isLoading || !sessionPick.loaded) return;
    const opening = openedSession.current !== sessionId;
    openedSession.current = sessionId;
    const target = restoreTarget(versions.tiles, {
      saved: readLastObject(sessionId),
      picked: sessionPick.pick ? { taskId: sessionPick.pick.taskId, assetId: sessionPick.pick.assetId } : null,
    });
    const next = target ? objectOfTile(target.tile, sessionId, target.pick) : null;
    if (next) setCurrentObject({ ...next, restored: true, ...(opening ? {} : { quiet: true }) });
  }, [object, sessionId, sessionPick.loaded, sessionPick.pick, versions.isLoading, versions.tiles, setCurrentObject]);

  return (
    <section aria-label={schemaText(schema, 'ui.studio.stage.title')} data-testid="studio-stage" className="@container relative flex min-h-0 min-w-0 flex-1 flex-col bg-canvas">
      <StageHeader object={object} />
      <div className="relative flex min-h-0 flex-1">
        {object?.kind === 'model' ? (
          <RegionErrorBoundary region="viewer_3d"><Stage3D object={object} /></RegionErrorBoundary>
        ) : (
          <RegionErrorBoundary region="canvas">
          <Suspense fallback={<div role="status" className="flex flex-1 items-center justify-center text-sm text-muted-foreground">{schemaText(schema, 'ui.workspace.harness.loading')}</div>}>
            <WorkspaceCanvasBoard />
          </Suspense>
          </RegionErrorBoundary>
        )}
        {pending && !allOutputsOpen ? (
          <div className="absolute bottom-4 left-1/2 z-float flex -translate-x-1/2 items-center gap-1 rounded-full border border-primary/40 bg-panel-raised py-1 pl-1 pr-1 shadow-lg" data-testid="stage-pending-pill">
            <Action type="button" size="sm" onClick={openPending} className="rounded-full">
              {schemaText(schema, 'ui.studio.stage.pending_result')}
            </Action>
            <HintTooltip name={schemaText(schema, 'ui.studio.stage.pending_dismiss')}>
              <Action type="button" variant="ghost" size="icon-xs" onClick={dismissPending} aria-label={schemaText(schema, 'ui.studio.stage.pending_dismiss')} className="rounded-full text-muted-foreground">
                <X />
              </Action>
            </HintTooltip>
          </div>
        ) : null}
        {compare ? <CompareView onEnlarge={enlargeCompare} /> : null}
        {allOutputsOpen ? <AllOutputs /> : null}
      </div>
    </section>
  );
}
