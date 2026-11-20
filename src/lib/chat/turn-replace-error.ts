import { ChatBrowserError } from '@/lib/chat/browser-client';

/**
 * The toast for a failed replace of the last turn (regenerate, edit and resend). A 409 is the server's own
 * refusal, for example because the turn still runs, and it carries the reason (#403); every other failure
 * keeps the general text, since nothing else is known about it.
 */
export function replaceFailureMessage(error: unknown, fallback: string): string {
  return error instanceof ChatBrowserError && error.status === 409 && error.message !== '' ? error.message : fallback;
}
