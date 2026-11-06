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
