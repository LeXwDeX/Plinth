import type { ProductSchema } from '@/lib/product-schema';

export type ImageResultSlot = { variant: string; label: string };

/** UI semantics come from the public capability declaration, not Provider wire fields. */
export function imageResultSlots(schema: ProductSchema | undefined, capability: string): ImageResultSlot[] {
  return schema?.capabilities?.find((entry) => entry.id === capability)?.image_result_group?.slots ?? [];
}

/** Preserve the declared view identity and order, including partial edit results. */
export function orderedImageResults<T extends { kind: string; variant: string; ordinal: number }>(
  assets: readonly T[], slots: readonly ImageResultSlot[] = [],
): T[] {
  const rank = new Map(slots.map((slot, index) => [slot.variant, index]));
  return assets.filter((asset) => asset.kind === 'image').sort((a, b) =>
    (rank.get(a.variant) ?? Number.MAX_SAFE_INTEGER) - (rank.get(b.variant) ?? Number.MAX_SAFE_INTEGER)
      || a.ordinal - b.ordinal);
}

/** Where a chat picture came from: its Task and, when the message attachment says, its asset. */
export interface GroupPicture { taskId: string; assetId?: string; url: string }

export interface ImageGroupView<T> { item: T; variant: string; label: string }

/**
 * One declared image group shown in place of plain candidates: its public name and ordered views. partialMessage is
 * the Schema message key shown when views are missing (an edit of some views); empty for a complete group.
 */
export interface ImageGroupPresentation<T> { capability: string; title: string; views: ImageGroupView<T>[]; partialMessage: string }

type GroupTask = { taskId: string; capability: string; assets: ReadonlyArray<{ id: string; kind: string; variant: string; url: string }> };

/**
 * The pictures of one message as a declared image group (e.g. 正面/左侧/背面/右侧), or null for plain candidates.
 * Every picture must be an image asset of the same tracked Task whose capability declares the group, each on a
 * distinct declared slot. Only a group that declares partial_message may miss views (an edit of some views), and the
 * missing views are never filled in; any other incomplete group is shown as plain candidates.
 */
export function imageGroupOfPictures<T>(
  schema: ProductSchema | undefined, items: readonly T[], identify: (item: T) => GroupPicture, tasks: ReadonlyArray<GroupTask>,
): ImageGroupPresentation<T> | null {
  if (items.length === 0) return null;
  const taskId = identify(items[0]).taskId;
  if (!taskId || items.some((item) => identify(item).taskId !== taskId)) return null;
  const task = tasks.find((entry) => entry.taskId === taskId);
  const capability = task ? schema?.capabilities?.find((entry) => entry.id === task.capability) : undefined;
  const slots = capability?.image_result_group?.slots ?? [];
  if (!task || slots.length === 0) return null;
  const views: ImageGroupView<T>[] = [];
  for (const item of items) {
    const picture = identify(item);
    const asset = task.assets.find((entry) => entry.kind === 'image' && (picture.assetId ? entry.id === picture.assetId : entry.url === picture.url));
    const slot = asset ? slots.find((entry) => entry.variant === asset.variant) : undefined;
    if (!slot || views.some((view) => view.variant === slot.variant)) return null;
    views.push({ item, variant: slot.variant, label: slot.label });
  }
  const rank = new Map(slots.map((slot, index) => [slot.variant, index]));
  views.sort((left, right) => (rank.get(left.variant) ?? 0) - (rank.get(right.variant) ?? 0));
  const partial = views.length < slots.length;
  const partialMessage = capability?.image_result_group?.partial_message ?? '';
  if (partial && !partialMessage) return null;
  return { capability: task.capability, title: capability?.label_cn ?? '', views, partialMessage: partial ? partialMessage : '' };
}
