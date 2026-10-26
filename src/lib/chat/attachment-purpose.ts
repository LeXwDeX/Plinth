/**
 * What an attached picture is for: its style, its composition, or the picture to change.
 * The backend has no field for it, so the choice is written into the message text as one
 * short line the Agent reads (the composer says so next to the choice).
 */
export type AttachmentPurpose = 'style' | 'layout' | 'input';

export const ATTACHMENT_PURPOSES: readonly AttachmentPurpose[] = ['style', 'layout', 'input'];

/** The text sent to the Agent: what was written, then the purpose line when one was chosen. */
export function withPurposeNote(text: string, note: string): string {
  const body = text.trim();
  if (note === '') return body;
  return body === '' ? note : `${body}\n${note}`;
}
