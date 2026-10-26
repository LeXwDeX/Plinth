import { afterEach, describe, expect, it, vi } from 'vitest';
import { clearPendingDirectTask, readPendingDirectTask, savePendingDirectTask, type PendingDirectTask } from './canvas-image-actions';

/**
 * #313: an accepted Task that settles after its dialog closed must not erase a newer prepared request
 * stored under the same picture identity. The settlement only clears the identity it started with.
 */

function storage(): Map<string, string> {
  const values = new Map<string, string>();
  vi.stubGlobal('sessionStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
  return values;
}

function pending(key: string): PendingDirectTask {
  return { preparedInputId: `prepared-${key}`, requestId: `request-${key}`, key,
    sessionId: 'session-a', offeringId: 'model-a', prompt: 'blue robot' };
}

afterEach(() => vi.unstubAllGlobals());

describe('pending direct task settlement', () => {
  it('clears the request a settlement started with', () => {
    storage();
    savePendingDirectTask('session-a', 'inpaint:source-a', pending('key-old'));
    clearPendingDirectTask('session-a', 'inpaint:source-a', pending('key-old'));
    expect(readPendingDirectTask('session-a', 'inpaint:source-a')).toBeNull();
  });

  it('keeps a newer request stored under the same identity', () => {
    storage();
    savePendingDirectTask('session-a', 'inpaint:source-a', pending('key-new'));
    clearPendingDirectTask('session-a', 'inpaint:source-a', pending('key-old'));
    expect(readPendingDirectTask('session-a', 'inpaint:source-a')?.key).toBe('key-new');
  });

  it('still clears unconditionally when the prompt or offering changed', () => {
    storage();
    savePendingDirectTask('session-a', 'inpaint:source-a', pending('key-new'));
    clearPendingDirectTask('session-a', 'inpaint:source-a');
    expect(readPendingDirectTask('session-a', 'inpaint:source-a')).toBeNull();
  });
});
