import { describe, expect, it } from 'vitest';

import { flushPendingEdits, registerPendingEdits } from './pending-edits';

describe('flushPendingEdits (#286)', () => {
  it('is saved when nothing is pending', async () => {
    await expect(flushPendingEdits('session-none')).resolves.toBe(true);
  });

  it('says whether every pending edit reached the server', async () => {
    const saved = registerPendingEdits('session-a', async () => true);
    await expect(flushPendingEdits('session-a')).resolves.toBe(true);
    // The brush's upload failed: its strokes are still only on screen.
    const failed = registerPendingEdits('session-a', async () => false);
    await expect(flushPendingEdits('session-a')).resolves.toBe(false);
    failed();
    saved();
  });

  it('treats a flusher that reports nothing as done', async () => {
    const done = registerPendingEdits('session-b', async () => undefined);
    await expect(flushPendingEdits('session-b')).resolves.toBe(true);
    done();
  });
});
