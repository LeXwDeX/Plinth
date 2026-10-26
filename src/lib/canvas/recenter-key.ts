/**
 * Space on the canvas: held with a drag it pans; a short press with no pointer drag
 * recenters the artboard. Keys typed into a field are never taken.
 */

/** Same rule as the stage shortcuts: a caret in any field keeps Space. */
export { isEditableTarget } from '@/lib/workspace/stage-keys';

export function createRecenterGesture(maxTapMilliseconds: number) {
  let pressedAt: number | null = null;
  let dragged = false;
  return {
    press(now: number): void {
      if (pressedAt === null) {
        pressedAt = now;
        dragged = false;
      }
    },
    pointerDown(): void {
      if (pressedAt !== null) dragged = true;
    },
    /** True when the release ends a short press that never saw a pointer: recenter. */
    release(now: number): boolean {
      const start = pressedAt;
      pressedAt = null;
      const tapped = start !== null && !dragged && now - start <= maxTapMilliseconds;
      dragged = false;
      return tapped;
    },
    cancel(): void {
      pressedAt = null;
      dragged = false;
    },
  };
}
