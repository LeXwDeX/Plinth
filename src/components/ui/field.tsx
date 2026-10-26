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
  error?: ReactNode
  group?: boolean
  required?: boolean
  labelHidden?: boolean
  layout?: "stacked" | "inline"
  className?: string
}

function Field({ id, label, children, help, error, group = false, required = false, labelHidden = false, layout = "stacked", className }: FieldProps) {
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
  const labelNode = group
      ? <legend className={cn("text-sm font-medium", labelHidden && "sr-only")}>{labelContent}</legend>
      : <label htmlFor={id} className={cn("text-sm font-medium", labelHidden && "sr-only")}>{labelContent}</label>
  const controlNode = children(controlProps)
  const content = <>
    {layout === "inline" && !group ? controlNode : labelNode}
    {layout === "inline" && !group ? labelNode : controlNode}
    {help && <div id={helpId} className="space-y-1 text-xs text-muted-foreground">{help}</div>}
    {error && <p id={errorId} role="alert" className="text-xs text-destructive">{error}</p>}
  </>

  // A stacked field is a flex column: the gap separates label, control, help and error
  // even though the label is an inline element, and out-of-flow nodes a control renders
  // beside itself (Base UI Select's hidden input) add no space. `space-y-*` failed on both,
  // so a Select field came out taller than its neighbours (#268).
  return group
    ? <fieldset data-slot="field" aria-describedby={describedBy} aria-invalid={controlProps["aria-invalid"]} aria-required={controlProps["aria-required"]} className={cn("space-y-2", className)}>{content}</fieldset>
    : <div data-slot="field" className={cn(layout === "inline" ? "flex items-center gap-2" : "flex flex-col gap-2", className)}>{content}</div>
}

export { Field }
