'use client';

import { useState } from 'react';

import { Action } from '@/components/ui/button';
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import {
  boardSteps,
  canExtendSteps,
  stepExtraConfig,
  stepLimit,
} from '@/lib/asset-lists/board';
import { AssetListError, type AssetListBoardItem, type AssetListClient, type AssetListItem } from '@/lib/asset-lists/client';
import { formatSchemaText, schemaText, type ProductSchema } from '@/lib/product-schema';
import { cn } from '@/lib/utils';

export interface AssetStepsExtendButtonProps {
  item: AssetListBoardItem;
  schema: ProductSchema;
  client: AssetListClient;
  projectID: string;
  listID: string;
  /** The refreshed item from the server; the board re-reads its aggregate. */
  onExtended: (item: AssetListItem) => void;
  /** The button always asks for confirmation; the dialog holds the amount. */
  className?: string;
  testId?: string;
}

/**
 * #330 step 5C: "give it a few more steps" for one capped item. The control is
 * offered only when the item's asset used its limit in the cap mode; the
 * dialog sends the asset's whole grant (current extra + the amount), so a
 * retried request cannot add twice. Every string and bound comes from the
 * Schema.
 */
export function AssetStepsExtendButton({
  item, schema, client, projectID, listID, onExtended, className, testId = 'asset-steps-extend',
}: AssetStepsExtendButtonProps) {
  const [open, setOpen] = useState(false);
  if (!canExtendSteps(schema, item)) return null;
  return (
    <>
      <Action
        type="button"
        variant="outline"
        size="sm"
        className={cn('min-h-11 px-4', className)}
        onClick={() => setOpen(true)}
        aria-label={formatSchemaText(schemaText(schema, 'ui.asset_list.steps.extend_title'), { name: item.name })}
        data-testid={testId}
      >
        {schemaText(schema, 'ui.asset_list.steps.extend')}
      </Action>
      {open ? (
        <AssetStepsExtendDialog
          item={item}
          schema={schema}
          client={client}
          projectID={projectID}
          listID={listID}
          onExtended={(updated) => {
            onExtended(updated);
            setOpen(false);
          }}
          onClose={() => setOpen(false)}
        />
      ) : null}
    </>
  );
}

export interface AssetStepsExtendDialogProps {
  item: AssetListBoardItem;
  schema: ProductSchema;
  client: AssetListClient;
  projectID: string;
  listID: string;
  onExtended: (item: AssetListItem) => void;
  onClose: () => void;
}

/** The confirm dialog of one extra-step grant. */
export function AssetStepsExtendDialog({
  item, schema, client, projectID, listID, onExtended, onClose,
}: AssetStepsExtendDialogProps) {
  const text = (key: string) => schemaText(schema, key);
  const bounds = stepExtraConfig(schema);
  const steps = boardSteps(item);
  const limit = stepLimit(steps);
  const currentExtra = steps?.extra ?? 0;
  const maximum = bounds?.maximum ?? 0;
  const [amount, setAmount] = useState(() => Math.min(Math.max(bounds?.default ?? 1, 1), Math.max(maximum, 1)));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const valid = Boolean(bounds) && Number.isInteger(amount) && amount >= 1 && amount <= maximum && !busy;
  const submit = async () => {
    if (!valid || !bounds) return;
    setBusy(true);
    setError('');
    try {
      const updated = await client.addItemSteps(projectID, listID, item.id, currentExtra + amount, item.version);
      onExtended(updated);
    } catch (failure) {
      const conflict = failure instanceof AssetListError && failure.status === 409;
      setError(conflict ? text('ui.asset_list.steps.extend_conflict') : text('ui.asset_list.steps.extend_failed'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <DialogContent
        closeLabel={text('ui.common.close')}
        className="flex flex-col gap-4 p-5"
        data-testid="asset-steps-extend-dialog"
      >
        <DialogTitle className="text-sm font-semibold">
          {formatSchemaText(text('ui.asset_list.steps.extend_title'), { name: item.name })}
        </DialogTitle>
        <p className="text-xs leading-5 text-muted-foreground">
          {formatSchemaText(text('ui.asset_list.steps.extend_description'), {
            used: steps?.used ?? 0,
            limit,
          })}
        </p>
        <label className="flex flex-col gap-1 text-xs font-medium">
          {text('ui.asset_list.steps.extend_amount')}
          <input
            type="number"
            min={1}
            max={maximum}
            step={1}
            inputMode="numeric"
            value={amount}
            disabled={busy}
            onChange={(event) => setAmount(Number(event.target.value))}
            className="min-h-11 w-28 rounded-md border border-input bg-background px-3 text-base tabular-nums"
            data-testid="asset-steps-extend-amount"
          />
          <span className="text-[11px] font-normal leading-4 text-muted-foreground">
            {formatSchemaText(text('ui.asset_list.steps.extend_hint'), {
              maximum,
              total_maximum: bounds?.totalMaximum ?? 0,
            })}
          </span>
        </label>
        {error ? (
          <p role="alert" className="text-xs text-destructive [overflow-wrap:anywhere]" data-testid="asset-steps-extend-error">
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
            data-testid="asset-steps-extend-cancel"
          >
            {text('ui.asset_list.steps.extend_cancel')}
          </Action>
          <Action
            type="button"
            size="sm"
            className="min-h-11 px-4"
            disabled={!valid}
            loading={busy}
            onClick={() => void submit()}
            data-testid="asset-steps-extend-confirm"
          >
            {text('ui.asset_list.steps.extend_confirm')}
          </Action>
        </div>
      </DialogContent>
    </Dialog>
  );
}