/**
 * Structural sharing for data read again from the server or derived again on every update (#291): a value
 * equal to the one already shown keeps the old object, so memoized components that receive it do not
 * re-render. Only plain data is compared (objects, arrays, Maps, Sets and primitives); functions and
 * other class instances count as equal only when they are the same object.
 */
export function sameData(left: unknown, right: unknown): boolean {
  if (Object.is(left, right)) return true;
  if (typeof left !== 'object' || typeof right !== 'object' || left === null || right === null) return false;
  if (Array.isArray(left)) {
    if (!Array.isArray(right) || left.length !== right.length) return false;
    for (let index = 0; index < left.length; index += 1) if (!sameData(left[index], right[index])) return false;
    return true;
  }
  if (Array.isArray(right)) return false;
  if (left instanceof Map) {
    if (!(right instanceof Map) || left.size !== right.size) return false;
    for (const [key, value] of left) if (!right.has(key) || !sameData(value, right.get(key))) return false;
    return true;
  }
  if (left instanceof Set) {
    if (!(right instanceof Set) || left.size !== right.size) return false;
    for (const value of left) if (!right.has(value)) return false;
    return true;
  }
  const leftPrototype = Object.getPrototypeOf(left);
  if (leftPrototype !== Object.getPrototypeOf(right) || (leftPrototype !== Object.prototype && leftPrototype !== null)) return false;
  const leftKeys = Object.keys(left);
  if (leftKeys.length !== Object.keys(right).length) return false;
  for (const key of leftKeys) {
    if (!Object.prototype.hasOwnProperty.call(right, key)) return false;
    if (!sameData((left as Record<string, unknown>)[key], (right as Record<string, unknown>)[key])) return false;
  }
  return true;
}

/**
 * `next` with every item that equals the previous item of the same key replaced by that previous object.
 * When nothing changed (same keys in the same order, all equal) the previous array itself is returned.
 */
export function keepUnchanged<T>(previous: readonly T[] | undefined, next: T[], keyOf: (item: T) => string): T[] {
  if (!previous || previous.length === 0) return next;
  const byKey = new Map<string, T>();
  for (const item of previous) byKey.set(keyOf(item), item);
  let allSame = previous.length === next.length;
  const result = next.map((item, index) => {
    const old = byKey.get(keyOf(item));
    const kept = old !== undefined && sameData(old, item) ? old : item;
    if (kept !== previous[index]) allSame = false;
    return kept;
  });
  return allSame ? (previous as T[]) : result;
}
