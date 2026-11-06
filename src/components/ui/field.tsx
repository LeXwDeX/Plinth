"use client"

import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

export interface FieldControlProps {
  id: string
  "aria-describedby"?: string
  "aria-invalid"?: true
  "aria-required"?: true
}

interface FieldProps {
  id: string
  label: ReactNode
  children: (controlProps: FieldControlProps) => ReactNode
  help?: ReactNode
  /** Shown between the label and the control (stacked), or on its own line under the label (inline). */
  meta?: ReactNode
  error?: ReactNode
  group?: boolean
  required?: boolean
  labelHidden?: boolean
  layout?: "stacked" | "inline"
  className?: string
}

function Field({ id, label, children, help, meta, error, group = false, required = false, labelHidden = false, layout = "stacked", className }: FieldProps) {
  const helpId = help ? `${id}-help` : undefined
  const errorId = error ? `${id}-error` : undefined
  const describedBy = [helpId, errorId].filter(Boolean).join(" ") || undefined
  const controlProps: FieldControlProps = {
    id,
    "aria-describedby": describedBy,
    "aria-invalid": error ? true : undefined,
    "aria-required": required ? true : undefined,
  }
  const labelContent = <>{label}{required ? <span aria-hidden="true" className="ml-1 text-destructive">*</span> : null}</>
  const inline = layout === "inline" && !group
  const labelNode = group
      ? <legend className={cn("text-sm font-medium", labelHidden && "sr-only")}>{labelContent}</legend>
      : <label htmlFor={id} className={cn("text-sm font-medium", inline && "whitespace-nowrap", labelHidden && "sr-only")}>{labelContent}</label>
  const controlNode = children(controlProps)
  const helpNode = help && <div id={helpId} className="space-y-1 text-xs text-muted-foreground">{help}</div>
  const errorNode = error && <p id={errorId} role="alert" className="text-xs text-destructive">{error}</p>

  // #112: an inline field (a plain checkbox) keeps the control and its label on one row; what
  // follows sits on its own lines, indented to the label. Squeezed into the row before, the
  // parameter source came out one character per line. #147: the control sits in a 16px slot, so
  // the label starts at 16px + 8px gap = pl-6 whatever size the browser draws a checkbox (13px in
  // Chromium left the indented lines 3px right of the label).
  if (inline) {
    return <div data-slot="field" className={cn("flex flex-col gap-1", className)}>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1"><span className="flex w-4 shrink-0">{controlNode}</span>{labelNode}</div>
      {meta ? <div className="pl-6">{meta}</div> : null}
      {helpNode || errorNode ? <div className="space-y-1 pl-6">{helpNode}{errorNode}</div> : null}
    </div>
  }
  const content = <>
    {labelNode}
    {meta}
    {controlNode}
    {helpNode}
    {errorNode}
  </>

  // A stacked field is a flex column: the gap separates label, control, help and error
  // even though the label is an inline element, and out-of-flow nodes a control renders
  // beside itself (Base UI Select's hidden input) add no space. `space-y-*` failed on both,
  // so a Select field came out taller than its neighbours (#268).
  return group
    ? <fieldset data-slot="field" aria-describedby={describedBy} aria-invalid={controlProps["aria-invalid"]} aria-required={controlProps["aria-required"]} className={cn("space-y-2", className)}>{content}</fieldset>
    : <div data-slot="field" className={cn("flex flex-col gap-2", className)}>{content}</div>
}

export { Field }
