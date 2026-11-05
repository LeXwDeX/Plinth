'use client';

import { useState } from 'react';
import { Popover } from '@base-ui/react/popover';
import { Wallet } from 'lucide-react';
import { useShallow } from 'zustand/react/shallow';

import { useProductSchema } from '@/components/product-schema-provider';
import { PlanCard } from '@/components/studio/plan-card';
import { budgetIsShown } from '@/lib/chat/budget';
import { anchoredPositionMethod, useAnchoredPositioner } from '@/components/ui/overlay';
import { schemaCountMessage, schemaText } from '@/lib/product-schema';
import { cn } from '@/lib/utils';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore } from '@/store/workspace-store';

/**
 * #103: the session's production budget in the top bar. It only exists once the
 * session has a plan; the badge shows spent and limit, and opening it shows the
 * plan summary with each step's Task state.
 */
export function BudgetBadge() {
  const { schema } = useProductSchema();
  const activeSessionId = useChatStore((state) => state.activeSessionId);
  const budget = useChatStore((state) => state.sessions.find((session) => session.id === state.activeSessionId)?.budget ?? null);
  const tasks = useWorkspaceStore(useShallow((state) => state.trackedTasks.filter((task) => task.sessionId === activeSessionId)));
  const [open, setOpen] = useState(false);
  const layer = useAnchoredPositioner();
  // #120: a refused plan is hidden by its Schema role, not by a state id.
  if (!activeSessionId || !budgetIsShown(schema, budget)) return null;
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger
        data-testid="studio-budget-badge"
        aria-label={schemaText(schema, 'ui.studio.budget.title')}
        className={cn(
          'flex h-8 shrink-0 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground transition-colors',
          'hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring data-popup-open:bg-primary/15 data-popup-open:text-primary',
        )}
      >
        <Wallet aria-hidden className="size-3.5" />
        <span className="tabular-nums">
          {schemaCountMessage(schema, 'ui.studio.budget.badge', budget.spentCredits, {
            spent: budget.spentCredits,
            limit: budget.limitCredits,
          })}
        </span>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner positionMethod={anchoredPositionMethod} side="bottom" align="end" sideOffset={6} collisionPadding={12} className={layer}>
          <Popover.Popup className="flex w-80 max-w-(--available-width) flex-col gap-2 rounded-lg bg-popover p-2 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10">
            <Popover.Title className="px-1 text-xs font-semibold">{schemaText(schema, 'ui.studio.budget.title')}</Popover.Title>
            <PlanCard plan={budget.plan} budgetCredits={budget.limitCredits} tasks={tasks} progress={budget} />
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}
