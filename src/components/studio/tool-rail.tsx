'use client';

import { ChevronsLeft, ChevronsRight, type LucideIcon } from 'lucide-react';
import { Fragment, type ReactNode, type Ref } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { iconByName } from '@/components/ui/icon-map';
import { Surface } from '@/components/ui/surface';
import { schemaText, type ToolRailName } from '@/lib/product-schema';
import { RAIL_COLLAPSED_WIDTH, RAIL_EXPANDED_MAX_WIDTH, RAIL_EXPANDED_MIN_WIDTH } from '@/lib/workspace/tool-rail-layout';
import { toolHintContent, toolSections, type ToolView } from '@/lib/workspace/tools';
import { cn } from '@/lib/utils';

/** What one rail button does and what state it is in. The Schema says what it is called, looks like and which key it has. */
export interface ToolHandler {
  run: () => void;
  /** The current tool or view (`aria-pressed`, primary colour). Leave it out for a plain action such as undo. */
  pressed?: boolean;
  disabled?: boolean;
  /** Why the button cannot be used right now; written first in the hover hint. */
  reason?: string;
  /** Looks unavailable but stays clickable (the click explains or offers a fix). */
  dimmed?: boolean;
  /** A glyph that replaces the Schema icon (the shape button shows the shape in use). */
  icon?: LucideIcon;
  /** Opens beside the button (the shape list); it is a child of the button's slot, which is its positioning parent. */
  flyout?: ReactNode;
  /** The slot around the button and its flyout, for callers that close the flyout on an outside click. */
  slotRef?: Ref<HTMLDivElement>;
}

export interface ToolRailProps {
  /** Which Schema rail to draw: the 2D board's tools or the 3D viewer's views. */
  rail: ToolRailName;
  /** The accessible name of the whole rail. */
  label: string;
  /** One handler per tool id. A Schema entry without a handler is not drawn. */
  handlers: Readonly<Record<string, ToolHandler | undefined>>;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  testId: string;
  itemTestId: (id: string) => string;
  /** Where the rail floats on the stage. */
  className?: string;
}

const ITEM_PRESSED = 'bg-primary/15 text-primary';
const ITEM_IDLE = 'text-muted-foreground hover:bg-muted hover:text-foreground';
/** Collapsed: icon-only, 30px wide and 28px high. Expanded: icon and name across the rail. */
const ITEM_COLLAPSED = 'w-[30px] px-0';
const ITEM_EXPANDED = 'w-full justify-start gap-2 px-2';

function RailFace({ Icon, label, expanded }: { Icon: LucideIcon; label: string; expanded: boolean }) {
  return (
    <>
      <Icon aria-hidden className="size-4" />
      {expanded ? <span className="truncate" data-testid="tool-rail-label">{label}</span> : null}
    </>
  );
}

function RailItem({ view, handler, expanded, testId }: { view: ToolView; handler: ToolHandler; expanded: boolean; testId: string }) {
  const { schema } = useProductSchema();
  const Icon = handler.icon ?? iconByName(view.icon);
  return (
    <div ref={handler.slotRef} className="relative flex" data-tool-slot={view.id}>
      <HintTooltip {...toolHintContent(schema, view, { reason: handler.reason })} side="right" className={expanded ? 'w-full' : undefined}>
        <Action
          type="button"
          variant="ghost"
          size="sm"
          aria-label={view.label}
          aria-pressed={handler.pressed}
          data-testid={testId}
          data-tool={view.id}
          disabled={handler.disabled === true}
          onClick={handler.run}
          className={cn(
            'transition-colors',
            expanded ? ITEM_EXPANDED : ITEM_COLLAPSED,
            handler.pressed ? ITEM_PRESSED : ITEM_IDLE,
            handler.dimmed && 'opacity-50',
          )}
        >
          <RailFace Icon={Icon} label={view.label} expanded={expanded} />
        </Action>
      </HintTooltip>
      {handler.flyout}
    </div>
  );
}

/** The last button of every rail: » while collapsed, « and the word 收起 while expanded. */
function RailToggle({ expanded, onExpandedChange }: { expanded: boolean; onExpandedChange: (expanded: boolean) => void }) {
  const { schema } = useProductSchema();
  const label = schemaText(schema, expanded ? 'ui.studio.rail.collapse' : 'ui.studio.rail.expand');
  const hint = schemaText(schema, expanded ? 'ui.studio.rail.collapse_hint' : 'ui.studio.rail.expand_hint');
  return (
    <HintTooltip name={label} hint={hint} side="right" className={expanded ? 'w-full' : undefined}>
      <Action
        type="button"
        variant="ghost"
        size="sm"
        aria-label={label}
        aria-expanded={expanded}
        data-testid="tool-rail-toggle"
        onClick={() => onExpandedChange(!expanded)}
        className={cn('transition-colors', expanded ? ITEM_EXPANDED : ITEM_COLLAPSED, ITEM_IDLE)}
      >
        <RailFace Icon={expanded ? ChevronsLeft : ChevronsRight} label={label} expanded={expanded} />
      </Action>
    </HintTooltip>
  );
}

function Separator() {
  return <div role="separator" aria-orientation="horizontal" className="mx-1 my-0.5 h-px shrink-0 bg-border" data-testid="tool-rail-separator" />;
}

/**
 * A rail of stage buttons drawn from `canvas.frontend.tools` (UI spec §5): sections split by a thin line,
 * each button with its hover hint (name and shortcut, what it does, and why it is unavailable when it is),
 * the current tool or view in the primary colour, and the expand toggle last. Collapsed it is icon-only
 * and 38px wide; expanded it shows the names, as wide as the longest one (96 to 112px).
 * What each button does is the caller's handler, keyed by tool id; the 2D tools and the 3D views use this
 * same component and the same expanded setting.
 */
export function ToolRail({ rail, label, handlers, expanded, onExpandedChange, testId, itemTestId, className }: ToolRailProps) {
  const { schema } = useProductSchema();
  const sections = toolSections(schema, rail)
    .map((group) => ({ section: group.section, tools: group.tools.filter((view) => handlers[view.id] !== undefined) }))
    .filter((group) => group.tools.length > 0);
  return (
    <Surface
      variant="toolbar"
      radius="xl"
      elevation="lg"
      role="toolbar"
      aria-orientation="vertical"
      aria-label={label}
      data-testid={testId}
      data-rail={rail}
      data-expanded={expanded ? 'true' : 'false'}
      style={expanded ? { width: 'max-content', minWidth: RAIL_EXPANDED_MIN_WIDTH, maxWidth: RAIL_EXPANDED_MAX_WIDTH } : { width: RAIL_COLLAPSED_WIDTH }}
      className={cn('absolute z-float flex flex-col items-stretch gap-0.5 p-[3px]', className)}
    >
      {sections.map((group, index) => (
        <Fragment key={group.section}>
          {index > 0 ? <Separator /> : null}
          <div className="flex flex-col gap-0.5" data-section={group.section}>
            {group.tools.map((view) => (
              <RailItem key={view.id} view={view} handler={handlers[view.id] as ToolHandler} expanded={expanded} testId={itemTestId(view.id)} />
            ))}
          </div>
        </Fragment>
      ))}
      {sections.length > 0 ? <Separator /> : null}
      <RailToggle expanded={expanded} onExpandedChange={onExpandedChange} />
    </Surface>
  );
}
