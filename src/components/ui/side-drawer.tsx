'use client';

import { useEffect, useId, useRef, type ReactNode } from 'react';
import { X } from 'lucide-react';

import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { OverlayLayerContext } from '@/components/ui/overlay';
import { cn } from '@/lib/utils';

/**
 * A non-modal panel docked to the right edge of its positioned parent. It has
 * no scrim, so the chat, stage and strip stay usable while it is open; Escape
 * and the close button dismiss it. Controls opened inside it float above it.
 */
export function SideDrawer({
  title,
  closeLabel,
  onClose,
  children,
  className,
  testId,
}: {
  title: string;
  closeLabel: string;
  onClose: () => void;
  children: ReactNode;
  className?: string;
  testId?: string;
}) {
  const titleId = useId();
  const panel = useRef<HTMLElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    panel.current?.querySelector<HTMLElement>('button:not([disabled])')?.focus();
    const element = panel.current;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.key !== 'Escape') return;
      event.preventDefault();
      onCloseRef.current();
    };
    element?.addEventListener('keydown', onKeyDown);
    return () => {
      element?.removeEventListener('keydown', onKeyDown);
      if (element?.contains(document.activeElement) && previous?.isConnected) previous.focus();
    };
  }, []);

  return (
    <section
      ref={panel}
      role="complementary"
      aria-labelledby={titleId}
      data-testid={testId}
      className={cn('absolute inset-y-0 right-0 z-modal flex w-[min(100%,380px)] flex-col border-l border-border bg-panel text-foreground shadow-xl', className)}
    >
      <OverlayLayerContext.Provider value="modal">
        <header className="flex h-12 shrink-0 items-center justify-between gap-2 border-b border-border px-3">
          <h2 id={titleId} className="text-sm font-semibold">{title}</h2>
          <HintTooltip name={closeLabel}>
            <Action type="button" variant="ghost" size="icon-sm" onClick={onClose} aria-label={closeLabel}>
              <X className="size-4" />
            </Action>
          </HintTooltip>
        </header>
        {children}
      </OverlayLayerContext.Provider>
    </section>
  );
}
