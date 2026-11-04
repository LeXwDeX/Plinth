"use client"

import { TriangleAlert } from "lucide-react"

import { cn } from "@/lib/utils"
import { HintTooltip } from "@/components/ui/hint-tooltip"

/** What the notice says, already worded from the Product Schema (see `unavailableLabel` in `@/lib/unavailable`). */
export interface UnavailableNoticeContent {
  /** The one inline line: "无法使用，<short reason>". */
  label: string
  /** The full explanation (the limit against the actual value, what upstream requires, what to do next); hover hint only. */
  detail?: string
}

/**
 * The shared "unavailable" notice (#93; the app-wide migration is #94): a warning icon and "无法使用，<short reason>"
 * inline, and the full explanation only in the hover hint, so no long paragraph spreads across the page. The hint
 * opens on hover and on keyboard focus; the explanation is also in the accessible description of the line, so a
 * screen reader hears it without hovering. The component owns the shape only; every text comes from the Schema.
 */
export function UnavailableNotice({ label, detail, className, testId }: UnavailableNoticeContent & { className?: string; testId?: string }) {
  const line = (
    <span role="status" tabIndex={detail ? 0 : undefined} data-testid={testId ?? "unavailable-notice"}
      className={cn("inline-flex min-w-0 items-center gap-1.5 text-warning", detail && "cursor-help", className)}>
      <TriangleAlert aria-hidden="true" className="size-3.5 shrink-0" data-testid="unavailable-icon" />
      <span className="min-w-0" data-testid="unavailable-label">{label}</span>
      {detail ? <span className="sr-only" data-testid="unavailable-detail">{detail}</span> : null}
    </span>
  )
  if (!detail) return line
  return <HintTooltip name={label} hint={detail} className="min-w-0" reopenOnKeyboardFocus>{line}</HintTooltip>
}
