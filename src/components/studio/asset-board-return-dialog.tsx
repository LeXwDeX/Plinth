'use client';

import { useState } from 'react';

import { Action } from '@/components/ui/button';
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import type { BoardReturnReason } from '@/lib/asset-lists/board';
import type { AssetListBoardItem } from '@/lib/asset-lists/client';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';

export interface AssetBoardReturnDialogProps {
  item: AssetListBoardItem;
  schema: ProductSchema;
  /** The reasons the Schema shows for this item's kind. */
  reasons: BoardReturnReason[];
  /** The submission is in flight; every control stays visible but disabled. */
  busy: boolean;
  /** The server's refusal, shown in place until the next submission. */
  error: string;
  onSubmit: (reasonIDs: string[], note: string) => void;
  onClose: () => void;
}

/**
 * #329d: the return picker. At least one declared reason is required; the
 * note is optional. The confirm button stays disabled until a reason is
 * picked, so a return can never be sent without its reason.
 */
export function AssetBoardReturnDialog({
  item, schema, reasons, busy, error, onSubmit, onClose,
}: AssetBoardReturnDialogProps) {
  const text = (key: string) => schemaText(schema, key);
  const [reasonIDs, setReasonIDs] = useState<string[]>([]);
  const [note, setNote] = useState('');
  const toggle = (id: string) => {
    setReasonIDs((previous) => previous.includes(id)
      ? previous.filter((candidate) => candidate !== id)
      : [...previous, id]);
  };
  const ready = reasonIDs.length > 0 && !busy;

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <DialogContent
        closeLabel={text('ui.common.close')}
        className="flex flex-col gap-4 p-5"
        data-testid="asset-board-return-dialog"
      >
        <DialogTitle className="text-sm font-semibold">
          {formatSchemaText(text('ui.asset_list.board.return_title'), { name: item.name })}
        </DialogTitle>

        <fieldset className="flex flex-col gap-1">
          <legend className="text-xs font-medium">{text('ui.asset_list.board.return_reason')}</legend>
          <p className="text-[11px] leading-4 text-muted-foreground">
            {text('ui.asset_list.board.return_reason_hint')}
          </p>
          {reasons.map((reason) => (
            <label
              key={reason.id}
              className="flex min-h-11 cursor-pointer items-center gap-2 rounded-md px-2 text-sm hover:bg-muted"
            >
              <input
                type="checkbox"
                className="size-4 shrink-0"
                checked={reasonIDs.includes(reason.id)}
                disabled={busy}
                onChange={() => toggle(reason.id)}
                data-testid={`asset-board-return-reason-${reason.id}`}
              />
              <span>{reason.label}</span>
            </label>
          ))}
        </fieldset>

        <label className="flex flex-col gap-1 text-xs font-medium">
          {text('ui.asset_list.board.return_note')}
          <textarea
            value={note}
            disabled={busy}
            rows={3}
            placeholder={text('ui.asset_list.board.return_note_placeholder')}
            onChange={(event) => setNote(event.target.value)}
            className="min-h-20 rounded-md border border-input bg-background p-2 text-base leading-6"
            data-testid="asset-board-return-note"
          />
        </label>

        {error ? (
          <p role="alert" className="text-xs text-destructive [overflow-wrap:anywhere]" data-testid="asset-board-return-error">
            {error}
          </p>
        ) : null}

        <div className="flex justify-end gap-2">
          <Action
            type="button"
            variant="ghost"
            size="sm"
            className="min-h-11"
            disabled={busy}
            onClick={onClose}
            data-testid="asset-board-return-cancel"
          >
            {text('ui.common.cancel')}
          </Action>
          <Action
            type="button"
            variant="destructive"
            size="sm"
            className="min-h-11 px-4"
            disabled={!ready}
            loading={busy}
            onClick={() => onSubmit(reasonIDs, note.trim())}
            data-testid="asset-board-return-submit"
          >
            {busy ? text('ui.asset_list.board.returning') : text('ui.asset_list.board.return_confirm')}
          </Action>
        </div>
      </DialogContent>
    </Dialog>
  );
}