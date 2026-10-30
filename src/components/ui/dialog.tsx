"use client"

import * as React from "react"
import { Dialog as DialogPrimitive } from "@base-ui/react/dialog"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { XIcon } from "lucide-react"
import { OverlayLayerContext, type OverlayLayer } from "@/components/ui/overlay"
import { releaseOutsideFocus, takeFocusHandoff } from "@/components/ui/focus-handoff"

function Dialog({ ...props }: DialogPrimitive.Root.Props) {
  return <DialogPrimitive.Root data-slot="dialog" {...props} />
}

function DialogTrigger({ ...props }: DialogPrimitive.Trigger.Props) {
  return <DialogPrimitive.Trigger data-slot="dialog-trigger" {...props} />
}

function DialogPortal({ ...props }: DialogPrimitive.Portal.Props) {
  return <DialogPrimitive.Portal data-slot="dialog-portal" {...props} />
}

function DialogClose({ ...props }: DialogPrimitive.Close.Props) {
  return <DialogPrimitive.Close data-slot="dialog-close" {...props} />
}

function DialogOverlay({
  className,
  layer = 'modal',
  ...props
}: DialogPrimitive.Backdrop.Props & { layer?: Exclude<OverlayLayer, 'base'> }) {
  return (
    <DialogPrimitive.Backdrop
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 isolate bg-black/10 duration-100 supports-backdrop-filter:backdrop-blur-xs data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0",
        layer === 'blocking' ? 'z-blocking' : 'z-scrim',
        className
      )}
      {...props}
    />
  )
}

type DialogContentProps = DialogPrimitive.Popup.Props & {
  overlayClassName?: string
  layer?: Exclude<OverlayLayer, 'base'>
} & (
  | { showCloseButton?: true; closeLabel: string }
  | { showCloseButton: false; closeLabel?: never }
)

// 子组件的 layout effect 先于 Base UI 焦点管理器的 effect 执行：在页面其余区域被标成
// aria-hidden 之前取下外面的焦点，并记住关闭时要还原的元素（内部存档 #164）。
// 标记元素的 ref 先于本组件的 layout effect 挂上，用它找到所在的对话框。
// 调用方自己声明 finalFocus 时不会取走登记的交接目标；弹层内容卸载（关闭）时丢弃，
// 避免留给之后的对话框。该组件随弹层挂载与卸载，不随外层 DialogContent。
function OutsideFocusRelease({ returnTarget, discardHandoff }: { returnTarget: React.RefObject<HTMLElement | null>; discardHandoff: boolean }) {
  const marker = React.useRef<HTMLSpanElement | null>(null)
  React.useLayoutEffect(() => {
    const popup = marker.current?.closest<HTMLElement>('[data-slot="dialog-content"]') ?? null
    const released = releaseOutsideFocus(popup)
    if (released) returnTarget.current = released
  }, [returnTarget])
  React.useEffect(() => () => {
    if (discardHandoff) takeFocusHandoff()
  }, [discardHandoff])
  return <span ref={marker} hidden />
}

function DialogContent({
  className,
  overlayClassName,
  children,
  showCloseButton = true,
  closeLabel,
  layer = 'modal',
  finalFocus,
  ...props
}: DialogContentProps) {
  const returnTarget = React.useRef<HTMLElement | null>(null)
  // 调用方声明了 finalFocus 时照用；否则先交给关闭后登记的目标，再还原打开前的元素。
  const resolveFinalFocus = React.useCallback(() => {
    const handoff = takeFocusHandoff()
    if (handoff) return handoff
    const previous = returnTarget.current
    returnTarget.current = null
    return previous?.isConnected ? previous : true
  }, [])
  return (
    <DialogPortal>
      <DialogOverlay className={overlayClassName} layer={layer} />
      <DialogPrimitive.Popup
        finalFocus={finalFocus ?? resolveFinalFocus}
        data-slot="dialog-content"
        className={cn(
          "fixed top-1/2 left-1/2 grid w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-4 rounded-xl bg-background p-4 text-sm ring-1 ring-foreground/10 duration-100 outline-none sm:max-w-sm data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          layer === 'blocking' ? 'z-blocking' : 'z-modal',
          className
        )}
        {...props}
      >
        <OutsideFocusRelease returnTarget={returnTarget} discardHandoff={finalFocus !== undefined} />
        <OverlayLayerContext.Provider value={layer}>
          {children}
        </OverlayLayerContext.Provider>
        {showCloseButton && (
          <DialogPrimitive.Close
            data-slot="dialog-close"
            render={
              <Button
                variant="ghost"
                className="absolute top-2 right-2"
                size="icon-sm"
              />
            }
          >
            <XIcon
            />
            <span className="sr-only">{closeLabel}</span>
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Popup>
    </DialogPortal>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex flex-col gap-2", className)}
      {...props}
    />
  )
}

function DialogFooter({
  className,
  showCloseButton = false,
  closeLabel,
  children,
  ...props
}: React.ComponentProps<"div"> & (
  | { showCloseButton?: false; closeLabel?: never }
  | { showCloseButton: true; closeLabel: string }
)) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "-mx-4 -mb-4 flex flex-col-reverse gap-2 rounded-b-xl border-t bg-muted/50 p-4 sm:flex-row sm:justify-end",
        className
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close render={<Button variant="outline" />}>
          {closeLabel}
        </DialogPrimitive.Close>
      )}
    </div>
  )
}

function DialogTitle({ className, ...props }: DialogPrimitive.Title.Props) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn(
        "font-heading text-base leading-none font-medium",
        className
      )}
      {...props}
    />
  )
}

function DialogDescription({
  className,
  ...props
}: DialogPrimitive.Description.Props) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={cn(
        "text-sm text-muted-foreground *:[a]:underline *:[a]:underline-offset-3 *:[a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
}
