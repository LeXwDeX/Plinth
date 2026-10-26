"use client"

import * as React from "react"
import { ContextMenu as ContextMenuPrimitive } from "@base-ui/react/context-menu"

import { cn } from "@/lib/utils"
import { anchoredPopup, useAnchoredPositioner } from "@/components/ui/overlay"

/**
 * Right-click menu anchored at the pointer. Same look as DropdownMenu; opens on the trigger's contextmenu event.
 * Use this, not a DropdownMenu opened by hand, for anything a right-click opens: Base UI gives a context menu
 * pointer anchoring and context-menu dismissal, while a plain Menu also closes when the pointer leaves its
 * popup (reason `trigger-hover`), which made the canvas menu vanish right after it opened (#280).
 */
function ContextMenu({ ...props }: ContextMenuPrimitive.Root.Props) {
  return <ContextMenuPrimitive.Root data-slot="context-menu" {...props} />
}

function ContextMenuTrigger({ ...props }: ContextMenuPrimitive.Trigger.Props) {
  return <ContextMenuPrimitive.Trigger data-slot="context-menu-trigger" {...props} />
}

function ContextMenuContent({ className, ...props }: ContextMenuPrimitive.Popup.Props) {
  const anchoredPositioner = useAnchoredPositioner()
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.Positioner className={cn(anchoredPositioner, "outline-none")}>
        <ContextMenuPrimitive.Popup
          data-slot="context-menu-content"
          className={cn(anchoredPopup, "min-w-36 p-1", className)}
          {...props}
        />
      </ContextMenuPrimitive.Positioner>
    </ContextMenuPrimitive.Portal>
  )
}

function ContextMenuItem({
  className,
  variant = "default",
  ...props
}: ContextMenuPrimitive.Item.Props & { variant?: "default" | "destructive" }) {
  return (
    <ContextMenuPrimitive.Item
      data-slot="context-menu-item"
      data-variant={variant}
      className={cn(
        "relative flex cursor-default items-center gap-1.5 rounded-md px-1.5 py-1 text-sm outline-hidden select-none focus:bg-accent focus:text-accent-foreground data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        "data-[variant=destructive]:text-destructive data-[variant=destructive]:focus:bg-destructive/10 data-[variant=destructive]:focus:text-destructive dark:data-[variant=destructive]:focus:bg-destructive/20",
        className
      )}
      {...props}
    />
  )
}

// A ContextMenu's separators and submenus are Base UI's Menu parts, so the DropdownMenu wrappers render them
// with the same look; these names keep a right-click menu's markup in one vocabulary.
export {
  DropdownMenuSeparator as ContextMenuSeparator,
  DropdownMenuShortcut as ContextMenuShortcut,
  DropdownMenuSub as ContextMenuSub,
  DropdownMenuSubTrigger as ContextMenuSubTrigger,
  DropdownMenuSubContent as ContextMenuSubContent,
} from "@/components/ui/dropdown-menu"

export { ContextMenu, ContextMenuTrigger, ContextMenuContent, ContextMenuItem }
