'use client';

import { useEffect, useId, useState } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Button } from '@/components/ui/button';
import { ChoiceChips } from '@/components/ui/choice-chips';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { Field } from '@/components/ui/field';
import { Textarea } from '@/components/ui/textarea';
import type { ChatAttachmentClaim } from '@/lib/chat/attachments';
import { preferenceReasonLabel } from '@/lib/chat/preferences';
import { schemaText } from '@/lib/product-schema';

export interface RejectCandidateDialogProps {
  /** The candidate the person said "not this one" about; null closes the dialog. */
  claim: ChatAttachmentClaim | null;
  /** The declared reason presets from the public Schema. */
  reasonPresets: readonly string[];
  busy?: boolean;
  onClose: () => void;
  /** Resolves true when the server stored the rejection. */
  onConfirm: (reasonPreset: string, reasonText: string) => Promise<boolean> | boolean | void;
}

/**
 * #241: the reason dialog behind a candidate's "不要这个". Both the preset and the free text are optional; the
 * text is bounded by the Schema, and the server refuses an over-long or undeclared reason instead of cutting it.
 */
export function RejectCandidateDialog({ claim, reasonPresets, busy = false, onClose, onConfirm }: RejectCandidateDialogProps) {
  const { schema } = useProductSchema();
  const [preset, setPreset] = useState('');
  const [reason, setReason] = useState('');
  const [error, setError] = useState('');
  const fieldId = useId();
  const maximum = schema.chat.preferences?.maximum_reason_characters ?? 0;

  // A different candidate starts a fresh reason.
  useEffect(() => {
    setPreset('');
    setReason('');
    setError('');
  }, [claim]);

  const options = reasonPresets.map((id) => ({ value: id, label: preferenceReasonLabel(schema, id) }));
  const close = () => { if (!busy) onClose(); };
  const confirm = async () => {
    const text = reason.trim();
    if (maximum > 0 && [...text].length > maximum) {
      setError(schemaText(schema, 'ui.workspace.preferences.too_long').replace('{maximum}', String(maximum)));
      return;
    }
    setError('');
    const ok = await onConfirm(preset, text);
    if (ok === false) setError(schemaText(schema, 'ui.chat.image.reject_failed'));
  };

  return (
    <Dialog open={claim !== null} onOpenChange={(open) => { if (!open) close(); }}>
      <DialogContent showCloseButton closeLabel={schemaText(schema, 'ui.chat.image.reject_cancel')}>
        <DialogHeader>
          <DialogTitle>{schemaText(schema, 'ui.chat.image.reject_dialog_title')}</DialogTitle>
          <DialogDescription>{schemaText(schema, 'ui.chat.image.reject_dialog_hint')}</DialogDescription>
        </DialogHeader>
        {options.length > 0 ? (
          <Field id={`${fieldId}-reason`} label={schemaText(schema, 'ui.chat.image.reject_reason_label')} group>
            {() => (
              <ChoiceChips
                label={schemaText(schema, 'ui.chat.image.reject_reason_label')}
                options={options}
                current={preset}
                disabled={busy}
                onChange={setPreset}
              />
            )}
          </Field>
        ) : null}
        <Field id={`${fieldId}-text`} label={schemaText(schema, 'ui.chat.image.reject_text_label')}>
          {(control) => (
            <Textarea
              {...control}
              value={reason}
              disabled={busy}
              rows={2}
              maxLength={maximum > 0 ? maximum : undefined}
              onChange={(event) => setReason(event.target.value)}
            />
          )}
        </Field>
        {error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
        <DialogFooter>
          <Button type="button" variant="outline" disabled={busy} onClick={close}>
            {schemaText(schema, 'ui.chat.image.reject_cancel')}
          </Button>
          <Button type="button" variant="destructive" disabled={busy} onClick={() => void confirm()} data-testid="reject-candidate-confirm">
            {schemaText(schema, 'ui.chat.image.reject_confirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}