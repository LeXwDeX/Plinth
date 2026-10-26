import { beforeEach, describe, expect, it } from 'vitest';

import { useInlineModelPreviewStore } from '@/store/inline-model-preview-store';

describe('inline model preview store', () => {
  beforeEach(() => useInlineModelPreviewStore.setState({ taskId: null }));

  it('holds one live viewer: opening another card hands it over', () => {
    const { open } = useInlineModelPreviewStore.getState();
    open('a');
    expect(useInlineModelPreviewStore.getState().taskId).toBe('a');
    open('b');
    expect(useInlineModelPreviewStore.getState().taskId).toBe('b');
  });

  it('lets only the holder give the viewer up', () => {
    const { open, close } = useInlineModelPreviewStore.getState();
    open('a');
    open('b');
    // The card that was replaced unmounts later and must not close the viewer of the card that replaced it.
    close('a');
    expect(useInlineModelPreviewStore.getState().taskId).toBe('b');
    close('b');
    expect(useInlineModelPreviewStore.getState().taskId).toBeNull();
  });
});
