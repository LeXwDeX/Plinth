/**
 * The name a layer goes by on screen: the layers panel row and the floating bar over the selected layer
 * read this one rule. A layer that was named (a 局部重绘 result) shows its name; a text layer shows its
 * first characters; anything else is called by its type. The words come from the Product Schema
 * (`ui.canvas.layers.type_<type>`), passed in as `text`.
 *
 * A saved name that is still an untouched default (the first canvas layer is saved as "画布图层" or
 * "Canvas layer", whichever language created it) shows in the reader's language instead: `persisted`
 * is the public Schema's `localization.persisted_default_texts`, every locale's text of such a default. The saved
 * name itself never changes.
 */
export interface NamedLayer {
  type: string;
  name?: string;
  text?: string;
}

/** How many characters of a text layer's content stand in for its name. */
export const TEXT_LAYER_NAME_CHARACTERS = 12;

export function layerName(
  layer: NamedLayer,
  text: (key: string) => string,
  persisted: Readonly<Record<string, readonly string[]>> = {},
): string {
  if (layer.name) {
    const key = Object.keys(persisted).find((candidate) => persisted[candidate].includes(layer.name!));
    return key ? text(key) : layer.name;
  }
  const typeName = text(`ui.canvas.layers.type_${layer.type}`);
  if (layer.type === 'text') return (layer.text ?? '').slice(0, TEXT_LAYER_NAME_CHARACTERS) || typeName;
  return typeName;
}
