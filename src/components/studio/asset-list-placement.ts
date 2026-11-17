import type { AssetListSummary } from '@/lib/asset-lists/client';

/**
 * #327: where each production list goes in the conversation. A list whose
 * `origin_message_id` is on the page follows that message, like a choice card
 * (#321); every other list — an older page, a session switch, a list made
 * without a message of its own — goes to the conversation's end, whatever its
 * status. Lists keep the server's creation order inside a message.
 */
export function assetListPlacement(
  lists: readonly AssetListSummary[],
  shownMessageIds: readonly string[],
) {
  const shown = new Set(shownMessageIds);
  const byMessage = new Map<string, AssetListSummary[]>();
  const loose: AssetListSummary[] = [];
  const ordered = [...lists].sort((left, right) => left.created_at.localeCompare(right.created_at));
  for (const list of ordered) {
    const origin = list.origin_message_id;
    if (origin && shown.has(origin)) {
      byMessage.set(origin, [...(byMessage.get(origin) ?? []), list]);
    } else {
      loose.push(list);
    }
  }
  return { byMessage, loose };
}