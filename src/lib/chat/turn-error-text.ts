import { ChatBrowserError } from './browser-client';
import { withErrorReference } from '@/lib/problem-reference';

/**
 * #665: the text a failed turn shows. The server's own refusal (with its error reference) and the Schema's stream
 * interruption text are shown as they are; a browser error ("Failed to fetch", "NetworkError when attempting to
 * fetch resource") is not product text and reads as the Schema's send failure instead.
 */
export function turnFailureText(error: unknown, schemaTexts: readonly string[], fallback: string): string {
  if (error instanceof ChatBrowserError && error.message !== '') return withErrorReference(error.message, error);
  if (error instanceof Error && schemaTexts.includes(error.message)) return error.message;
  return fallback;
}

/**
 * #665: the server refused a queued send because a turn of the session already runs (another tab sent first). That
 * refusal is a 409 without a problem code; the other codeless 409 of a send answers a choice card, which a queued send
 * never carries.
 */
export function turnActiveRefusal(error: unknown): boolean {
  return error instanceof ChatBrowserError && error.status === 409 && error.code === '';
}
