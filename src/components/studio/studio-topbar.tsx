'use client';

import { AppTopbar } from '@/components/layout/app-topbar';
import { ApprovalMenu } from '@/components/studio/approval-menu';
import { SessionMenu } from '@/components/studio/session-menu';
import { TaskCenterButton } from '@/components/studio/task-center';

/** The workspace fills the shared top bar with the session menu, approvals and the task center. */
export function StudioTopbar() {
  return (
    <AppTopbar
      testId="studio-topbar"
      leading={<SessionMenu />}
      actions={<><ApprovalMenu /><TaskCenterButton /></>}
    />
  );
}
