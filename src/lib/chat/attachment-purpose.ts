/**
 * What an attached picture is for: its style, its composition, or the picture to change.
 * The structured choice travels with the turn; the composer also writes a short line the
 * Agent can read, so the old text-only path keeps working.
 */
export type AttachmentPurpose = 'style' | 'layout' | 'input';

export const ATTACHMENT_PURPOSES: readonly AttachmentPurpose[] = ['style', 'layout', 'input'];

/** The structured per-picture purposes for one turn; only pictures with a choice are listed. */
export function attachmentPurposesForTurn(
  purposes: readonly (AttachmentPurpose | null)[],
): { imageSlot: number; purpose: AttachmentPurpose }[] {
  return purposes.flatMap((purpose, index) => (purpose ? [{ imageSlot: index + 1, purpose }] : []));
}

/** A turn can name at most one picture as the one to edit. */
export function hasMultipleInputPurposes(purposes: readonly { purpose: AttachmentPurpose }[]): boolean {
  return purposes.filter((item) => item.purpose === 'input').length > 1;
}

/** The text sent to the Agent: what was written, then the purpose line when one was chosen. */
export function withPurposeNote(text: string, note: string): string {
  const body = text.trim();
  if (note === '') return body;
  return body === '' ? note : `${body}\n${note}`;
}

/**
 * The purpose line written into the message, built from every picture's own purpose. When all
 * pictures share one purpose it is the single sentence it always was; when they differ, or only
 * some have one, each chosen picture is named by its position so the line never claims the last
 * choice for all of them. `text` reads a Schema key.
 */
export function purposeNote(
  purposes: readonly (AttachmentPurpose | null)[],
  text: (key: string) => string,
): string {
  const chosen = purposes.flatMap((purpose, index) => (purpose ? [{ index: index + 1, purpose }] : []));
  if (chosen.length === 0) return '';
  const first = chosen[0].purpose;
  if (chosen.length === purposes.length && chosen.every((item) => item.purpose === first)) {
    return text(`ui.studio.composer.attachment.note_${first}`);
  }
  const items = chosen
    .map((item) => text('ui.studio.composer.attachment.note_item')
      .replace('{index}', String(item.index))
      .replace('{purpose}', text(`ui.studio.composer.attachment.purpose_${item.purpose}`)))
    .join(text('ui.studio.composer.attachment.note_separator'));
  return text('ui.studio.composer.attachment.note_mixed').replace('{items}', items);
}
