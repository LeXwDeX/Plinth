'use client';

import { AppTopbar } from '@/components/layout/app-topbar';
import { ApprovalMenu } from '@/components/studio/approval-menu';
import { BudgetBadge } from '@/components/studio/budget-badge';
import { SessionMenu } from '@/components/studio/session-menu';
import { TaskCenterButton } from '@/components/studio/task-center';

/** The workspace fills the shared top bar with the session menu, budget, approvals and the task center. */
export function StudioTopbar() {
  return (
    <AppTopbar
      testId="studio-topbar"
      leading={<SessionMenu />}
      actions={<><BudgetBadge /><ApprovalMenu /><TaskCenterButton /></>}
    />
  );
}
