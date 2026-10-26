/** The part of a <model-viewer> element that frames the camera. */
export interface CameraFramingElement {
  cameraOrbit: string;
  cameraTarget: string;
  fieldOfView: string;
  jumpCameraToGoal?: () => void;
  resetTurntableRotation?: (theta?: number) => void;
}

/**
 * Puts the camera back to the viewer's own default framing of the model (what it shows on load):
 * orbit, target and field of view go back to "auto" and the camera jumps there at once.
 */
export function resetModelView(element: CameraFramingElement | null | undefined): boolean {
  if (!element) return false;
  element.cameraOrbit = 'auto auto auto';
  element.cameraTarget = 'auto auto auto';
  element.fieldOfView = 'auto';
  element.resetTurntableRotation?.();
  element.jumpCameraToGoal?.();
  return true;
}

/** Load progress (0 to 1) from a <model-viewer> "progress" event; null when the event carries none. */
export function loadProgressOf(event: Event): number | null {
  const total = (event as CustomEvent<{ totalProgress?: unknown }>).detail?.totalProgress;
  return typeof total === 'number' && Number.isFinite(total) ? Math.min(1, Math.max(0, total)) : null;
}

/**
 * Whether a <model-viewer> "load" event belongs to the source the element shows now. model-viewer waits two
 * frames before it announces a load, so a load of the previous source can arrive after the source changed.
 * An event without a URL is taken as current.
 */
export function isCurrentLoad(event: Event, element: { src?: string | null }): boolean {
  const url = (event as CustomEvent<{ url?: unknown }>).detail?.url;
  return typeof url !== 'string' || url === (element.src ?? '');
}
