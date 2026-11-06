type SkillStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

function storageKey(ownerId: string, sessionId: string): string {
  return `plinth:session-skill:${JSON.stringify([ownerId, sessionId])}`;
}

export function readSessionSkill(storage: SkillStorage, ownerId: string, sessionId: string): string | null {
  try {
    const value: unknown = JSON.parse(storage.getItem(storageKey(ownerId, sessionId)) ?? 'null');
    return typeof value === 'string' && value.trim() ? value : null;
  } catch {
    return null;
  }
}

export function writeSessionSkill(storage: SkillStorage, ownerId: string, sessionId: string, id: string | null): void {
  try {
    const key = storageKey(ownerId, sessionId);
    if (id) storage.setItem(key, JSON.stringify(id));
    else storage.removeItem(key);
  } catch {
    // Browser storage can be disabled or full; selection still works in memory.
  }
}
