/**
 * Which picture the stage toolbar's 去背景 works on. The one picture layer selected on the board wins; with
 * nothing selected, several layers, or a non-picture layer selected, it is the current version's picture
 * (its layer is then selected, so the person sees what is being cut out). On a board without a version there is
 * no current picture, so only a selected picture layer gives a target (the toolbar keeps the button off until
 * one is selected). The floating bar over a selected layer has no 去背景 button: the toolbar is the only way in.
 */
export interface BackgroundTargetLayer {
  id: string;
  imageUrl: string;
}

export interface BackgroundTarget<T extends BackgroundTargetLayer> {
  layer: T;
  /** The layer was not chosen by the person: select it on the board before the dialog opens. */
  selectLayer: boolean;
}

export function removeBackgroundTarget<T extends BackgroundTargetLayer>(
  selectedPicture: T | null | undefined,
  currentObjectLayer: T | null | undefined,
): BackgroundTarget<T> | null {
  if (selectedPicture) return { layer: selectedPicture, selectLayer: false };
  if (currentObjectLayer) return { layer: currentObjectLayer, selectLayer: true };
  return null;
}
