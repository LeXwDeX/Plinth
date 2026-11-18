'use client';

import { AppTopbar } from '@/components/layout/app-topbar';
import { ApprovalMenu } from '@/components/studio/approval-menu';
import { AssetListProgressChip } from '@/components/studio/asset-list-progress-chip';
import { BudgetBadge } from '@/components/studio/budget-badge';
import { ProjectSwitcher } from '@/components/studio/project-switcher';
import { SessionMenu } from '@/components/studio/session-menu';
import { StepPolicyControl } from '@/components/studio/step-policy-control';
import { TaskCenterButton } from '@/components/studio/task-center';

/** The workspace fills the shared top bar with the session menu, budget, step mode, approvals and the task center. */
export function StudioTopbar() {
  return (
    <AppTopbar
      testId="studio-topbar"
      leading={<><ProjectSwitcher /><SessionMenu /></>}
      actions={<><AssetListProgressChip /><BudgetBadge /><StepPolicyControl /><ApprovalMenu /><TaskCenterButton /></>}
    />
  );
}
