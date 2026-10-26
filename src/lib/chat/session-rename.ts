export type RenameIntent = 'save' | 'cancel' | null;

/** What a key press in the rename field means; composition (IME) key presses mean nothing. */
export function renameKeyIntent(event: { key: string; isComposing: boolean; keyCode: number }): RenameIntent {
  if (event.isComposing || event.keyCode === 229) return null;
  if (event.key === 'Enter') return 'save';
  if (event.key === 'Escape') return 'cancel';
  return null;
}

/** An empty or unchanged name is a cancel; anything else saves the trimmed name. */
export function resolveRename(draft: string, current: string): { kind: 'cancel' } | { kind: 'save'; title: string } {
  const title = draft.trim();
  return !title || title === current ? { kind: 'cancel' } : { kind: 'save', title };
}
