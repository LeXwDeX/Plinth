'use client';

import { useState } from 'react';

import { Action } from '@/components/ui/button';
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import {
  boardSteps,
  canExtendSteps,
  stepExtraBounds,
  stepLimit,
} from '@/lib/asset-lists/board';
import {
  AssetListError,
  type AssetListBoardItem,
  type AssetListBoardSteps,
  type AssetListClient,
  type AssetListItem,
} from '@/lib/asset-lists/client';
import {
  canExtendSessionSteps,
  sessionStepsExhausted,
  sessionStepsLimit,
  type SessionSteps,
} from '@/lib/chat/session-steps';
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

/** The confirm dialog of one list item's extra-step grant. */
export function AssetStepsExtendDialog(props: AssetStepsExtendDialogProps) {
  const { item, schema, client, projectID, listID, onExtended, onClose } = props;
  return (
    <StepsExtendDialog
      name={item.name}
      steps={boardSteps(item)}
      schema={schema}
      onSubmit={async (target) => {
        let updated: AssetListItem;
        try {
          updated = await client.addItemSteps(projectID, listID, item.id, target, item.version);
        } catch (failure) {
          if (failure instanceof AssetListError && failure.status === 409) {
            throw new ConflictRefusal(schemaText(schema, 'ui.asset_list.steps.extend_conflict'));
          }
          throw failure;
        }
        onExtended(updated);
      }}
      onClose={onClose}
    />
  );
}

export interface SessionStepsExtendButtonProps {
  sessionID: string;
  name: string;
  steps: SessionSteps | null;
  schema: ProductSchema;
  /** Sends the asset's whole grant; the hook re-reads the standing. */
  onSubmit: (target: number) => Promise<void>;
  className?: string;
  testId?: string;
}

/**
 * #330 §6: "give it a few more steps" for one asset session (an external MCP
 * asset, #332). It reuses the same dialog and Schema copy as the list board;
 * the only difference is the route: the write is keyed by the chat session.
 */
export function SessionStepsExtendButton({
  sessionID, name, steps, schema, onSubmit, className, testId = 'session-steps-extend',
}: SessionStepsExtendButtonProps) {
  const [open, setOpen] = useState(false);
  const extendsSteps = canExtendSessionSteps(schema) && steps?.mode === 'cap' &&
    sessionStepsExhausted(steps);
  if (!extendsSteps) return null;
  return (
    <>
      <Action
        type="button"
        variant="outline"
        size="sm"
        className={cn('min-h-9 px-3', className)}
        onClick={() => setOpen(true)}
        aria-label={formatSchemaText(schemaText(schema, 'ui.asset_list.steps.extend_title'), { name })}
        data-testid={testId}
      >
        {schemaText(schema, 'ui.asset_list.steps.extend')}
      </Action>
      {open ? (
        <StepsExtendDialog
          name={name}
          steps={{
            mode: steps?.mode,
            used: steps?.used ?? 0,
            model3d: 0,
            cap: steps?.cap,
            extra: steps?.extra,
          }}
          schema={schema}
          onSubmit={onSubmit}
          onClose={() => setOpen(false)}
          testId="session-steps-extend-dialog"
          ariaSession={sessionID}
        />
      ) : null}
    </>
  );
}

/** A refusal whose copy is already the Schema's; the dialog shows it verbatim. */
class ConflictRefusal extends Error {}

export interface StepsExtendDialogProps {
  name: string;
  steps: AssetListBoardSteps | null;
  schema: ProductSchema;
  onSubmit: (target: number) => Promise<void>;
  onClose: () => void;
  testId?: string;
  /** Test/accessibility hook: the asset session the grant belongs to. */
  ariaSession?: string;
}

/**
 * The confirm dialog shared by the list item and the asset session. The amount
 * is added on top of the grant the caller read; a refusal keeps the dialog
 * open so the person can retry after a refresh.
 */
export function StepsExtendDialog({
  name, steps, schema, onSubmit, onClose, testId = 'asset-steps-extend-dialog', ariaSession,
}: StepsExtendDialogProps) {
  const text = (key: string) => schemaText(schema, key);
  const bounds = stepExtraBounds(schema);
  const limit = stepLimit(steps ?? null);
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
      await onSubmit(currentExtra + amount);
      onClose();
    } catch (failure) {
      if (failure instanceof ConflictRefusal) {
        setError(failure.message);
      } else {
        const conflict = failure instanceof AssetListError && failure.status === 409;
        setError(conflict ? text('ui.asset_list.steps.extend_conflict') : text('ui.asset_list.steps.extend_failed'));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <DialogContent
        closeLabel={text('ui.common.close')}
        className="flex flex-col gap-4 p-5"
        data-testid={testId}
        data-session={ariaSession}
      >
        <DialogTitle className="text-sm font-semibold">
          {formatSchemaText(text('ui.asset_list.steps.extend_title'), { name })}
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