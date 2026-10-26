"use client"

import { useId, useState, type FocusEvent, type HTMLAttributes, type ReactNode } from "react"

import { cn } from "@/lib/utils"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"

/**
 * What a hover hint says, top to bottom: why the control cannot be used right now (only when it is
 * disabled and the reason is known), its name with the shortcut, what it does, what it acts on, and
 * the cost line. Every text arrives already worded by the caller (from the Product Schema); this
 * module owns only the shape. A name may be a whole value (a long file name, an id, a parameter dump): it wraps
 * anywhere and keeps its line breaks, so a truncated cell can show its full text on hover.
 */
export interface HintContent {
  name: string
  /** Key caps printed beside the name, e.g. ["Ctrl", "Z"]. */
  shortcut?: readonly string[]
  /** Why the control is disabled; written first so a greyed button explains itself. */
  reason?: string
  /** What it does. */
  hint?: string
  /** What it acts on, already worded ("作用于：…"). */
  target?: string
  /** The cost line; `ai` marks a control that calls a model (drawn in the AI colour). */
  cost?: { text: string; ai: boolean }
}

/** The bubble's content on its own, so it can be rendered and tested without the floating layer. */
export function HintBubble({ name, shortcut, reason, hint, target, cost }: HintContent) {
  return (
    <div className="flex flex-col gap-0.5 text-left text-xs leading-relaxed [overflow-wrap:anywhere]" data-testid="hint-bubble">
      {reason ? <p className="font-medium text-warning" data-testid="hint-reason">{reason}</p> : null}
      <div className="flex items-center justify-between gap-3" data-testid="hint-name">
        <span className="whitespace-pre-wrap text-[12.5px] font-semibold">{name}</span>
        {shortcut && shortcut.length > 0 ? (
          <kbd className="shrink-0 rounded-[min(var(--radius-sm),4px)] border border-border px-1.5 font-mono text-[10.5px] text-muted-foreground" data-testid="hint-shortcut">
            {shortcut.join("+")}
          </kbd>
        ) : null}
      </div>
      {hint ? <p data-testid="hint-text">{hint}</p> : null}
      {target ? <p className="text-muted-foreground" data-testid="hint-target">{target}</p> : null}
      {cost ? (
        <p className={cn("text-[11.5px]", cost.ai ? "text-ai-mark" : "text-muted-foreground")} data-testid="hint-cost" data-ai={cost.ai ? "true" : "false"}>
          {cost.text}
        </p>
      ) : null}
    </div>
  )
}

/**
 * The hover hint of a control (R16): opens after the pointer rests 350ms and at once when the keyboard
 * focuses the control; closes on leave, click and Esc. Below the control with an 8px gap, flipping above
 * when there is no room and staying inside the window; at most 260px wide. A vertical rail passes
 * `side="right"` so the hint does not cover the buttons under the one it describes.
 *
 * The control is wrapped in a span that carries the hover and focus listeners, so a disabled button
 * (which receives no pointer events) still shows its hint; the first line then says why it is disabled.
 * The span is `inline-flex`: pass `className` where the control needs its layout role on the wrapper instead
 * (`w-full`, `min-w-0`, `shrink-0`, or the `absolute ...` placement of a corner button). A hint with no name has
 * nothing to say and never opens (a cell whose full value is empty).
 */
export function HintTooltip({
  children,
  className,
  side,
  triggerProps,
  reopenOnKeyboardFocus = false,
  suppressed = false,
  passive = false,
  ...content
}: HintContent & {
  children: ReactNode; className?: string; side?: 'bottom' | 'right'; triggerProps?: HTMLAttributes<HTMLSpanElement>; reopenOnKeyboardFocus?: boolean
  /** While true the hint stays closed and does not open (a picker whose own panel just closed and handed focus back, #324). */
  suppressed?: boolean
  /** The bubble never takes the pointer: it does not stay open when hovered and clicks pass through it to the controls below (#324). */
  passive?: boolean
}) {
  // Base UI's own state is kept as it is; `suppressed` only hides the bubble, so once it lifts a hover that is
  // already under way shows as usual.
  const [rawOpen, setOpen] = useState(false)
  const open = rawOpen && !suppressed
  const tooltipId = useId()
  const describedBy = [triggerProps?.['aria-describedby'], open ? tooltipId : undefined].filter(Boolean).join(' ') || undefined
  // Escape may suppress Base UI's next focus after the pointer entered the bubble.
  // Only explicit keyboard re-entry opts out; no effect watches focus or reopens after Escape.
  const onFocus = reopenOnKeyboardFocus ? (event: FocusEvent<HTMLSpanElement>) => {
    triggerProps?.onFocus?.(event)
    if (!event.defaultPrevented && content.name.trim() !== '' && event.currentTarget.matches(':focus-visible')) {
      setOpen(true)
    }
  } : triggerProps?.onFocus
  return (
    <TooltipProvider>
      <Tooltip open={open} onOpenChange={setOpen} disabled={content.name.trim() === ''} disableHoverablePopup={passive}>
        <TooltipTrigger render={<span className={cn("inline-flex", className)} {...triggerProps} onFocus={onFocus} aria-describedby={describedBy} />}>{children}</TooltipTrigger>
        <TooltipContent
          id={tooltipId}
          role="tooltip"
          side={side}
          arrow={false}
          className={cn("block bg-popover px-2.5 py-2 text-popover-foreground shadow-md ring-1 ring-foreground/10 rounded-lg", passive && "pointer-events-none")}
          data-testid="hint-tooltip"
        >
          <HintBubble {...content} />
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
