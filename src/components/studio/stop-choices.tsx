'use client';

import { ChevronDown, Square } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { buttonVariants } from '@/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { schemaText } from '@/lib/product-schema';

/**
 * The composer's stop while the server says the waiting turn may continue in the background (#137).
 * "Stop and cancel the task" is the usual stop; "stop waiting, keep the task running" ends the turn and
 * leaves its Task to the task tray and the task center. Both labels come from the Schema.
 */
export function StopChoices({ label, onCancel, onBackground }: {
  label: string;
  onCancel: () => void;
  onBackground: () => void;
}) {
  const { schema } = useProductSchema();
  return (
    <DropdownMenu>
      <HintTooltip name={label}>
        <DropdownMenuTrigger
          aria-label={label}
          data-testid="composer-stop-choices"
          className={buttonVariants({ variant: 'secondary', size: 'sm' })}
        >
          <Square className="size-3.5" />
          {label}
          <ChevronDown aria-hidden className="size-3" />
        </DropdownMenuTrigger>
      </HintTooltip>
      <DropdownMenuContent align="end" side="top" className="w-64">
        <DropdownMenuItem onClick={onCancel} data-testid="composer-stop-cancel">
          <Square aria-hidden />
          {schemaText(schema, 'ui.studio.composer.stop_cancel_task')}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={onBackground} data-testid="composer-stop-background" className="flex-col items-start gap-0.5">
          <span>{schemaText(schema, 'ui.studio.composer.stop_background')}</span>
          <span className="text-xs text-muted-foreground">{schemaText(schema, 'ui.studio.composer.stop_background_hint')}</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
