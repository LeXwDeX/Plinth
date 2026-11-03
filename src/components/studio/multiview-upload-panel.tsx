'use client';

import { useEffect, useState } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import { defaultViewChoices, validViewChoices, type MultiviewOffering } from '@/lib/workspace/multiview-upload';

export interface MultiviewUploadPanelProps {
  images: readonly string[];
  target: MultiviewOffering;
  disabled?: boolean;
  /** The view of each picture, aligned with `images`; an empty view leaves that picture out. */
  onConfirm: (choices: string[]) => void;
}

/** Asks whether 2-4 attached pictures are views of one object and which view each is; hidden until the pictures change once dismissed. */
export function MultiviewUploadPanel({ images, target, disabled = false, onConfirm }: MultiviewUploadPanelProps) {
  const { schema } = useProductSchema();
  const signature = images.join('\n');
  const [choices, setChoices] = useState<string[]>(() => defaultViewChoices(images.length, target.views));
  const [dismissedFor, setDismissedFor] = useState<string | null>(null);
  useEffect(() => { setChoices(defaultViewChoices(images.length, target.views)); }, [signature, target.views, images.length]);
  if (dismissedFor === signature) return null;
  const valid = validViewChoices(choices, target.minPictures, target.maxPictures);
  const none = schemaText(schema, 'ui.studio.multiview_upload.view_none');
  return (
    <section
      className="mx-2 mb-1 flex flex-col gap-1.5 rounded-lg border border-border bg-panel-raised p-2 text-xs"
      data-testid="studio-multiview-upload" aria-label={schemaText(schema, 'ui.studio.multiview_upload.question')}
    >
      <p className="font-medium">{schemaText(schema, 'ui.studio.multiview_upload.question')}</p>
      <p className="text-muted-foreground">{formatSchemaText(schemaText(schema, 'ui.studio.multiview_upload.hint'), { model: target.offering.model_name })}</p>
      <ul className="flex flex-wrap gap-2">
        {images.map((url, index) => (
          <li key={`${index}-${url.slice(-24)}`} className="flex items-center gap-1.5">
            <img src={url} alt={String(index + 1)} className="size-9 rounded-sm border border-border object-cover" />
            <select
              value={choices[index] ?? ''} disabled={disabled} data-testid="studio-multiview-upload-view"
              aria-label={String(index + 1)}
              onChange={(event) => setChoices((current) => current.map((value, at) => (at === index ? event.target.value : value)))}
              className="h-7 rounded-md border border-border bg-surface px-1"
            >
              <option value="">{none}</option>
              {target.views.map((view) => <option key={view.value} value={view.value}>{view.label}</option>)}
            </select>
          </li>
        ))}
      </ul>
      {!valid ? <p role="status" className="text-warning" data-testid="studio-multiview-upload-invalid">{schemaText(schema, 'ui.studio.multiview_upload.invalid')}</p> : null}
      <div className="flex items-center gap-1.5">
        <Action type="button" size="sm" disabled={disabled || !valid} onClick={() => onConfirm(choices)} data-testid="studio-multiview-upload-confirm">
          {schemaText(schema, 'ui.studio.multiview_upload.confirm')}
        </Action>
        <Action type="button" size="sm" variant="ghost" onClick={() => setDismissedFor(signature)} data-testid="studio-multiview-upload-dismiss">
          {schemaText(schema, 'ui.studio.multiview_upload.dismiss')}
        </Action>
      </div>
    </section>
  );
}
