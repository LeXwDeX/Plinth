/**
 * The one picture layer selected on the board: exactly one layer is selected and it is a picture. The
 * floating bar over the layer, the option bar's room for it and the stage toolbar's 去背景 on a board
 * without a version all ask this one question, so they agree on when it holds.
 */
export function selectedImageLayer<T extends { id: string; type: string; blank?: boolean }>(
  nodes: readonly T[] | undefined,
  selectedIds: readonly string[],
): T | null {
  if (!nodes || selectedIds.length !== 1) return null;
  const node = nodes.find((item) => item.id === selectedIds[0]);
  // An initial layer not painted yet (#246) has no picture to download, duplicate or remove the background of.
  return node && node.type === 'image' && node.blank !== true ? node : null;
}

/** The floating bar shows for that layer while the select tool is the current tool. */
export function layerBarShown(activeTool: string, layer: unknown): boolean {
  return layer !== null && layer !== undefined && activeTool === 'select';
}
