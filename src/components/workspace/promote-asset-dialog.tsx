'use client';

import { useEffect, useState } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { Dialog, DialogContent } from '@/components/ui/dialog';
import type { ProjectAsset, ProjectReference } from '@/lib/projects/browser-client';
import { projectContract, schemaText } from '@/lib/product-schema';

export interface PromoteAssetDialogProps {
  open: boolean;
  source: ProjectReference | null;
  previewURL?: string;
  busy?: boolean;
  error?: string;
  onCancel: () => void;
  onConfirm: (input: { kind: string; name: string; description: string }) => void;
  /** The asset the server created, so a test or parent can show it. */
  promoted?: ProjectAsset | null;
}

/**
 * #240: the confirmation dialog for promoting one finished picture into the
 * project. It shows the source thumbnail and requires a kind and a name; the
 * request is sent only by the explicit confirm button. A failure keeps the
 * dialog and its input so the person can retry.
 */
export function PromoteAssetDialog({ open, source, previewURL, busy = false, error = '', onCancel, onConfirm, promoted = null }: PromoteAssetDialogProps) {
  const { schema } = useProductSchema();
  const projectSchema = projectContract(schema);
  const kinds = projectSchema?.assets.kinds ?? [];
  const [kind, setKind] = useState(kinds[0]?.id ?? '');
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const text = (key: string) => schemaText(schema, `ui.project.${key}`);

  useEffect(() => {
    if (!open) return;
    setKind(kinds[0]?.id ?? '');
    setName('');
    setDescription('');
  }, [open, kinds]);

  if (!open) return null;
  const maximumName = projectSchema?.limits.maximum_asset_name_characters ?? 120;
  const maximumDescription = projectSchema?.limits.maximum_asset_description_characters ?? 1000;
  const valid = Boolean(source) && kind !== '' && name.trim() !== '';
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onCancel(); }}>
      <DialogContent showCloseButton={false} aria-label={text('promote_title')} className="max-w-lg" data-testid="promote-asset-dialog">
        <h2 className="text-sm font-semibold">{text('promote_title')}</h2>
        <p className="mt-1 text-xs leading-5 text-muted-foreground">{text('promote_confirm')}</p>
        <div className="mt-3 flex gap-3">
          <div className="size-24 shrink-0 overflow-hidden rounded-md border border-border bg-muted" data-testid="promote-asset-preview">
            {previewURL ? <img src={previewURL} alt={text('promotion_source')} className="size-full object-cover" /> : null}
          </div>
          <div className="flex min-w-0 flex-1 flex-col gap-2">
            <label className="flex flex-col gap-1 text-xs font-medium">
              {text('promote_kind')}
              <select value={kind} disabled={busy} onChange={(event) => setKind(event.target.value)} className="rounded-md border border-input bg-background px-2 py-1 text-xs" data-testid="promote-asset-kind">
                {kinds.map((candidate) => <option key={candidate.id} value={candidate.id}>{schemaText(schema, candidate.label_key)}</option>)}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs font-medium">
              {text('promote_name')}
              <input value={name} maxLength={maximumName} disabled={busy} onChange={(event) => setName(event.target.value)} className="rounded-md border border-input bg-background px-2 py-1 text-xs" data-testid="promote-asset-name" />
            </label>
            <label className="flex flex-col gap-1 text-xs font-medium">
              {text('asset_description')}
              <textarea value={description} maxLength={maximumDescription} rows={2} disabled={busy} onChange={(event) => setDescription(event.target.value)} className="resize-y rounded-md border border-input bg-background px-2 py-1 text-xs" data-testid="promote-asset-description" />
            </label>
          </div>
        </div>
        {promoted ? <p role="status" className="mt-2 text-xs text-muted-foreground">{text('promotion_saved')}</p> : null}
        {error ? <p role="alert" className="mt-2 text-xs text-destructive">{error}</p> : null}
        <div className="mt-3 flex justify-end gap-1.5">
          <Action type="button" variant="ghost" size="sm" disabled={busy} onClick={onCancel}>{text('cancel')}</Action>
          <Action type="button" size="sm" disabled={busy || !valid} data-testid="promote-asset-confirm" onClick={() => onConfirm({ kind, name: name.trim(), description: description.trim() })}>{text('confirm')}</Action>
        </div>
      </DialogContent>
    </Dialog>
  );
}