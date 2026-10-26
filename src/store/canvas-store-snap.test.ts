import { beforeEach, describe, expect, it } from 'vitest';

import { useCanvasStore } from '@/store/canvas-store';

describe('canvas snap toggle', () => {
  beforeEach(() => {
    useCanvasStore.setState({ snapEnabledOverride: null });
  });

  it('defaults to the Schema value (no override)', () => {
    expect(useCanvasStore.getState().snapEnabledOverride).toBeNull();
  });

  it('records the viewer choice (persisted via the store partialize list)', () => {
    useCanvasStore.getState().setSnapEnabled(false);
    expect(useCanvasStore.getState().snapEnabledOverride).toBe(false);
  });
});
