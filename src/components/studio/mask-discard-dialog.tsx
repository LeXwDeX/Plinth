'use client';

import { useEffect, useRef } from 'react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { tileOfObject, useSessionVersions } from '@/hooks/use-session-versions';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { versionLabel } from '@/lib/workspace/version-label';
import { useCanvasStore } from '@/store/canvas-store';
import { useMaskStore } from '@/store/mask-store';

/**
 * 涂好的蒙版不会被悄悄丢掉：退出蒙版之前先问；换到别的版本时不问，蒙版先停放在原来的版本上，
 * 提示一句"蒙版会保留在 v2"，回到那个版本时自动还原（放弃它只需点提示上的按钮）。
 */
export function MaskDiscardDialog() {
  const question = useMaskStore((state) => state.question);
  return (
    <>
      {question ? <MaskDiscardView /> : null}
    </>
  );
}

function MaskDiscardView() {
  const { schema } = useProductSchema();
  const dismiss = useMaskStore((state) => state.dismissQuestion);

  const discard = () => {
    useMaskStore.getState().clearDraft();
    useCanvasStore.getState().setActiveTool('select');
  };

  return (
    <Dialog open onOpenChange={(open) => { if (!open) dismiss(); }}>
      <DialogContent closeLabel={schemaText(schema, 'ui.dialog.close')} className="flex flex-col gap-0 p-5" data-testid="mask-discard">
        <DialogTitle className="mb-3 text-sm font-semibold">{schemaText(schema, 'ui.studio.mask.discard_title')}</DialogTitle>
        <p className="text-xs leading-5 text-muted-foreground">{schemaText(schema, 'ui.studio.mask.discard_exit')}</p>
        <div className="mt-4 flex justify-end gap-2">
          <Action type="button" size="sm" variant="ghost" data-testid="mask-discard-keep" onClick={dismiss}>
            {schemaText(schema, 'ui.studio.mask.discard_keep')}
          </Action>
          <Action type="button" size="sm" variant="destructive" data-testid="mask-discard-confirm" onClick={discard}>
            {schemaText(schema, 'ui.studio.mask.discard_confirm')}
          </Action>
        </div>
      </DialogContent>
    </Dialog>
  );
}

