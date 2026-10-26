'use client';

import { useMemo } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { shortcutSections } from '@/lib/workspace/key-dispatch';
import { useStudioStore } from '@/store/studio-store';

/**
 * The shortcut panel ("?"): every shortcut by focus area, built from the same Schema key settings the
 * dispatcher and the canvas read, so a changed key shows up here without a code change.
 */
export function ShortcutPanel() {
  const open = useStudioStore((state) => state.shortcutsOpen);
  const setOpen = useStudioStore((state) => state.setShortcutsOpen);
  if (!open) return null;
  return <ShortcutPanelView onClose={() => setOpen(false)} />;
}

function ShortcutPanelView({ onClose }: { onClose: () => void }) {
  const { schema, text } = useProductSchema();
  const sections = useMemo(() => shortcutSections(schema), [schema]);
  return (
    <Dialog open onOpenChange={(next) => { if (!next) onClose(); }}>
      <DialogContent closeLabel={text('ui.account.close')} className="max-h-[88dvh] overflow-y-auto sm:max-w-2xl" data-testid="shortcut-panel">
        <DialogHeader>
          <DialogTitle>{text('ui.shortcuts.title')}</DialogTitle>
          <DialogDescription>{text('ui.shortcuts.description')}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 sm:grid-cols-2">
          {sections.map((section) => (
            <section key={section.area} aria-label={section.title} data-testid="shortcut-section" data-area={section.area} className="flex flex-col gap-1.5">
              <h3 className="text-xs font-semibold text-muted-foreground">{section.title}</h3>
              <dl className="flex flex-col gap-1">
                {section.items.map((item) => (
                  <div key={item.id} className="flex items-start justify-between gap-3 text-xs" data-testid="shortcut-item" data-id={item.id}>
                    <dt className="min-w-0">{item.label}</dt>
                    <dd className="flex shrink-0 flex-wrap justify-end gap-1">
                      {item.keys.map((caps) => (
                        <span key={caps.join('+')} className="flex items-center gap-0.5">
                          {caps.map((cap, index) => (
                            <kbd key={`${cap}-${index}`} className="rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-[11px] leading-none">{cap}</kbd>
                          ))}
                        </span>
                      ))}
                    </dd>
                  </div>
                ))}
              </dl>
            </section>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
