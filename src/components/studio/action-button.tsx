'use client';

import type { ReactNode } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { iconByName } from '@/components/ui/icon-map';
import { schemaText } from '@/lib/product-schema';
import { hintContentOf, labelBreakpoint, type ActionView, type LabelBreakpoint } from '@/lib/workspace/stage-actions';
import { cn } from '@/lib/utils';

/** The label shows from this stage width up (container query on the stage header); the ✦ mark follows it. */
const LABEL_SHOWN: Record<LabelBreakpoint, string> = {
  '5xl': 'hidden @5xl:inline',
  '6xl': 'hidden @6xl:inline',
  '7xl': 'hidden @7xl:inline',
};
/** The dot takes the ✦'s place while the label is hidden. */
const DOT_HIDDEN: Record<LabelBreakpoint, string> = {
  '5xl': '@5xl:hidden',
  '6xl': '@6xl:hidden',
  '7xl': '@7xl:hidden',
};

/** Extra classes a toolbar button needs on top of the Button variants (the ✦ dot is positioned against it). */
export function actionButtonClass(pressed = false): string {
  return cn('relative', pressed && 'bg-primary/15 text-primary');
}

/**
 * What a toolbar button shows inside: the icon and, unless `iconOnly`, the label (from the stage width its
 * length allows). A button that calls a model carries the ✦ mark (UI spec §3): after the label text, and as a
 * small dot at the icon's top-right where only the icon shows.
 */
export function ActionFace({ view, iconOnly = false }: { view: ActionView; iconOnly?: boolean }) {
  const { schema } = useProductSchema();
  const Icon = iconByName(view.icon);
  const mark = schemaText(schema, 'ui.studio.hint.ai_mark');
  const dot = <span aria-hidden className="absolute right-0.5 top-0.5 size-1.5 rounded-full bg-ai-mark" data-testid="ai-dot" />;
  if (iconOnly) {
    return (
      <>
        <Icon aria-hidden />
        {view.callsModel ? dot : null}
      </>
    );
  }
  const breakpoint = labelBreakpoint(view.label);
  return (
    <>
      <Icon aria-hidden />
      <span className={LABEL_SHOWN[breakpoint]} data-testid="action-label">{view.label}</span>
      {view.callsModel ? (
        <>
          <span aria-hidden className={cn('-ml-0.5 text-[10px] leading-none text-ai-mark', LABEL_SHOWN[breakpoint])} data-testid="ai-mark">{mark}</span>
          <span aria-hidden className={cn('absolute right-0.5 top-0.5 size-1.5 rounded-full bg-ai-mark', DOT_HIDDEN[breakpoint])} data-testid="ai-dot" />
        </>
      ) : null}
    </>
  );
}

/** The hover hint around a toolbar control; `reason` (why it is disabled) is written first. */
export function ActionHint({ view, reason, children }: { view: ActionView; reason?: string; children: ReactNode }) {
  const { schema } = useProductSchema();
  return <HintTooltip {...hintContentOf(schema, view, reason ? { reason } : {})}>{children}</HintTooltip>;
}

export interface ActionButtonProps {
  view: ActionView;
  onAct: () => void;
  /** Icon only, even on a wide stage (the output and view groups). */
  iconOnly?: boolean;
  variant?: 'ghost' | 'outline';
  disabled?: boolean;
  /** Why it is disabled, written first in the hover hint. */
  disabledReason?: string;
  pressed?: boolean;
}

/**
 * One stage-toolbar button, drawn from a `canvas.frontend.stage_actions` entry (or a processing capability):
 * icon, optional label, the ✦ mark when it calls a model, the hover hint and the accessible name. What it
 * does is the `onAct` the caller binds to its id.
 */
export function ActionButton({ view, onAct, iconOnly = false, variant = 'ghost', disabled = false, disabledReason, pressed }: ActionButtonProps) {
  return (
    <ActionHint view={view} reason={disabled ? disabledReason : undefined}>
      <Action
        type="button"
        variant={variant}
        size={iconOnly ? 'icon-sm' : 'sm'}
        className={actionButtonClass(pressed === true)}
        aria-label={view.label}
        aria-pressed={pressed}
        data-testid={view.testId}
        data-action={view.id}
        data-ai={view.callsModel ? 'true' : 'false'}
        disabled={disabled}
        onClick={onAct}
      >
        <ActionFace view={view} iconOnly={iconOnly} />
      </Action>
    </ActionHint>
  );
}
