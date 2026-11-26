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
