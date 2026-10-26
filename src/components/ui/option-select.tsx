"use client"

import type { ReactNode } from "react"

import { cn } from "@/lib/utils"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
} from "@/components/ui/select"

export interface OptionSelectOption {
  value: string
  label: ReactNode
  disabled?: boolean
  /** Options with the same group are listed under that heading. */
  group?: string
}

export interface OptionSelectProps {
  value: string
  options: readonly OptionSelectOption[]
  onValueChange: (value: string) => void
  /** Shown on the trigger when the value matches no option. */
  placeholder?: ReactNode
  disabled?: boolean
  required?: boolean
  size?: "sm" | "default"
  className?: string
  id?: string
  "aria-label"?: string
  "aria-describedby"?: string
  "aria-invalid"?: true
  "aria-required"?: true
}

/**
 * A list of plain options in the project's own dropdown, in place of a native select.
 * The trigger shows the chosen option's label, so a label can say more than its value.
 */
function OptionSelect({
  value,
  options,
  onValueChange,
  placeholder,
  disabled,
  required,
  size = "default",
  className,
  ...controlProps
}: OptionSelectProps) {
  const selected = options.find((option) => option.value === value)
  const groups = new Map<string, OptionSelectOption[]>()
  for (const option of options) groups.set(option.group ?? "", [...(groups.get(option.group ?? "") ?? []), option])
  const items = (list: readonly OptionSelectOption[]) => list.map((option) => (
    <SelectItem key={option.value} value={option.value} disabled={option.disabled}>{option.label}</SelectItem>
  ))

  // One wrapper keeps the select's hidden input out of the Field's spacing.
  return (
    <div>
      <Select value={value} onValueChange={(next) => onValueChange(String(next ?? ""))} disabled={disabled} required={required}>
        <SelectTrigger {...controlProps} size={size} className={cn("w-full", className)}>
          <span className={cn("min-w-0 flex-1 truncate text-left", !selected && "text-muted-foreground")}>{selected?.label ?? placeholder}</span>
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          {[...groups].map(([group, list]) => group
            ? <SelectGroup key={group}><SelectLabel>{group}</SelectLabel>{items(list)}</SelectGroup>
            : items(list))}
        </SelectContent>
      </Select>
    </div>
  )
}

export { OptionSelect }
