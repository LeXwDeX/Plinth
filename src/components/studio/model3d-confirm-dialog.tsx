'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { openComposerSettings } from '@/store/composer-settings-store';
import { useModel3DConfirmStore } from '@/store/model3d-confirm-store';

/**
 * #678: 「使用 {模型} 转 3D？」, asked when 转 3D is pressed without a 3D model picked. 确认 runs this one conversion with
 * that model; 换一个… opens the composer settings on the models tab and runs nothing. Mounted once in the studio.
 */
export function Model3DConfirmHost() {
  const { schema } = useProductSchema();
  const request = useModel3DConfirmStore((state) => state.request);
  const close = useModel3DConfirmStore((state) => state.close);
  const text = (key: string) => schemaText(schema, `ui.studio.model3d_confirm.${key}`);
  return (
    <Dialog open={request !== null} onOpenChange={(open) => { if (!open) close(); }}>
      <DialogContent showCloseButton closeLabel={text('cancel')} data-testid="model3d-confirm">
        <DialogHeader>
          <DialogTitle>{request ? formatSchemaText(text('title'), { model: request.modelName }) : ''}</DialogTitle>
          <DialogDescription>
            {request?.cost ? <span className="block font-medium text-foreground" data-testid="model3d-confirm-cost">{request.cost}</span> : null}
            {text('description')}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" data-testid="model3d-confirm-change" onClick={() => { close(); openComposerSettings('models'); }}>
            {text('change')}
          </Button>
          <Button type="button" data-testid="model3d-confirm-ok" onClick={() => {
            const current = request;
            close();
            current?.run(current.modelId);
          }}>
            {text('confirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
