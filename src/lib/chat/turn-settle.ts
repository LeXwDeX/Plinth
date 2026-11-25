/**
 * #597: the end of a turn as the page sees it.
 *
 * The server appends a turn's `done` event before it marks the turn ended, so the newest page read right after
 * `done` can still call the reply pending. That reply then stayed "running" for the page: its branch stayed
 * disabled, and while the next turn streamed it was hidden with its pictures (the list hid every pending message
 * behind the live buffer). Only a reload put it right.
 */

/** The stored messages shown next to a streaming turn: only that turn's own pending reply is left to the buffer. */
export function messagesBesideStream<Message extends { id: string; isPending?: boolean }>(
  messages: Message[],
  stream: { assistantDbId: string | null } | undefined,
): Message[] {
  if (!stream) return messages;
  // Before the stream names its reply, any pending message may be it; once named, an earlier reply that is
  // still marked pending is not this turn's and stays in the list.
  return messages.filter((message) => !(message.isPending && (stream.assistantDbId === null || message.id === stream.assistantDbId)));
}

export type SettleOutcome = 'settled' | 'running' | 'left';

/**
 * After `done`, reads the newest page again at the given delays until no reply in it is pending. 'running' when the
 * server still calls a reply pending after the last read (a turn really runs: the session recovery takes it over);
 * 'left' when the session is no longer the one shown, or a new turn of it started, so nothing is applied.
 */
export async function readUntilSettled<Page extends { messages: { isPending?: boolean }[] }>(options: {
  read: () => Promise<Page>;
  apply: (page: Page) => void;
  isCurrent: () => boolean;
  delays: readonly number[];
  wait?: (milliseconds: number) => Promise<void>;
}): Promise<SettleOutcome> {
  const wait = options.wait ?? ((milliseconds: number) => new Promise<void>((resolve) => { setTimeout(resolve, milliseconds); }));
  for (const delay of options.delays) {
    await wait(delay);
    if (!options.isCurrent()) return 'left';
    let page: Page;
    try {
      page = await options.read();
    } catch {
      continue;
    }
    if (!options.isCurrent()) return 'left';
    options.apply(page);
    if (!page.messages.some((message) => message.isPending)) return 'settled';
  }
  return 'running';
}
