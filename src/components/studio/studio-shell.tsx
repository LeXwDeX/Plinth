'use client';

import { RegionErrorBoundary } from '@/components/common/region-error-boundary';
import { ConnectivityBanner } from '@/components/layout/connectivity-banner';
import { ChatColumn } from '@/components/studio/chat-column';
import { Lightbox } from '@/components/studio/lightbox';
import { MaskDiscardDialog } from '@/components/studio/mask-discard-dialog';
import { RightPanel } from '@/components/studio/right-panel';
import { ShortcutPanel } from '@/components/studio/shortcut-panel';
import { Stage } from '@/components/studio/stage';
import { StudioTopbar } from '@/components/studio/studio-topbar';
import { ResultDetailHost } from '@/components/result/result-detail';
import { TaskDrawer } from '@/components/studio/task-center';
import { VersionStrip } from '@/components/studio/version-strip';
import { Surface } from '@/components/ui/surface';
import { useStudioShortcuts } from '@/hooks/use-stage-viewer';
import { useTaskCompletionNotice } from '@/hooks/use-task-completion-notice';
import { useTaskTracker } from '@/hooks/use-task-tracker';

/**
 * 「对话 + 舞台」工作台：顶栏 48px；左边是对话列；中间是舞台和右侧栏，
 * 舞台和右侧栏下面是 112px 的版本条；任务中心从右侧滑出，不遮挡对话。
 */
export function StudioShell() {
  return (
    <Surface variant="base" radius="none" className="flex h-dvh min-h-0 flex-col overflow-hidden text-foreground" data-testid="studio-shell">
      <StudioEffects />
      <ConnectivityBanner />
      <StudioTopbar />
      <main className="relative flex min-h-0 flex-1">
        <div className="flex h-full min-h-0" data-testid="studio-chat-slot"><RegionErrorBoundary region="chat"><ChatColumn /></RegionErrorBoundary></div>
        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex min-h-0 flex-1">
            <Stage />
            <div className="flex min-h-0" data-testid="studio-right-panel-slot"><RightPanel /></div>
          </div>
          <VersionStrip />
        </div>
        <TaskDrawer />
        <Lightbox />
        <ResultDetailHost />
        <ShortcutPanel />
        <MaskDiscardDialog />
      </main>
    </Surface>
  );
}

/**
 * The studio-wide task tracker, completion notices and shortcuts. They read the task list and the session's
 * versions, which change with every task update; kept in their own component so those updates redraw nothing
 * else (in the shell they redrew the whole studio, #291).
 */
function StudioEffects() {
  useTaskTracker();
  useTaskCompletionNotice();
  useStudioShortcuts();
  return null;
}
