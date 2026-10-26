/**
 * A key held while the window loses focus (Cmd+Tab, Alt+Tab) or the page is hidden never gets its keyup here,
 * so a "held" flag would stay set (#263: a stuck Space keeps layers undraggable). Call `release` then.
 * Returns the function that stops listening.
 */
export function onHeldKeysLost(
  win: Pick<EventTarget, 'addEventListener' | 'removeEventListener'>,
  doc: Pick<EventTarget, 'addEventListener' | 'removeEventListener'> & { visibilityState: DocumentVisibilityState },
  release: () => void,
): () => void {
  const onBlur = () => release();
  const onVisibility = () => {
    if (doc.visibilityState === 'hidden') release();
  };
  win.addEventListener('blur', onBlur);
  doc.addEventListener('visibilitychange', onVisibility);
  return () => {
    win.removeEventListener('blur', onBlur);
    doc.removeEventListener('visibilitychange', onVisibility);
  };
}
