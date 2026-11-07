// @vitest-environment jsdom
// #180: the composer's style-lock chip asks the right panel to open 功能 and the section there.
import React from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { RightPanel } from '@/components/studio/right-panel';
import { useStudioStore } from '@/store/studio-store';

const picture = { kind: 'image' as const, taskId: 't-img', assetId: 'a-img', url: '/i.png', sessionId: 's1' };

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application }),
}));
vi.mock('@/store/chat-store', () => ({
  useChatStore: (selector: (state: object) => unknown) => selector({ activeSessionId: 's1' }),
}));
vi.mock('@/store/workspace-store', () => ({
  useWorkspaceStore: (selector: (state: object) => unknown) => selector({ currentObject: picture }),
}));
vi.mock('@/store/canvas-store', () => ({
  useCanvasStore: (selector: (state: object) => unknown) => selector({ nodesBySession: { s1: [] } }),
}));
vi.mock('@/hooks/use-model-processing-capabilities', () => ({
  useModelProcessingCapabilities: () => ({ available: [], offerings: {}, error: undefined }),
}));
vi.mock('@/components/workspace/harness-panel', () => ({
  HarnessPanel: () => <div data-testid="stub-operations" />,
}));
vi.mock('@/components/workspace/style-lock-panel', () => ({
  StyleLockSection: () => <div data-testid="stub-style-lock" />,
}));
vi.mock('@/components/canvas/layers-panel', () => ({ CanvasLayersPanel: () => <div data-testid="stub-layers" /> }));
vi.mock('@/components/canvas/history-panel', () => ({ CanvasHistoryPanel: () => <div data-testid="stub-history" /> }));
vi.mock('@/components/studio/brief-panel', () => ({ BriefPanel: () => <div data-testid="stub-brief" /> }));
vi.mock('@/components/studio/object-details', () => ({ ObjectDetails: () => <div data-testid="stub-details" /> }));
vi.mock('@/components/studio/model-parts-panel', () => ({ ModelPartsPanel: () => <div data-testid="stub-parts" /> }));
vi.mock('@/components/workspace/model-processing-panel', () => ({ ModelProcessingPanel: () => <div data-testid="stub-process" /> }));
vi.mock('@/components/workspace/model-formats', () => ({ ModelArchiveLink: () => <a data-testid="stub-archive" />, ModelFormatsPanel: () => <div data-testid="stub-formats" /> }));

describe('the style-lock chip request', () => {
  beforeEach(() => {
    useStudioStore.setState({ panelTab2d: 'layers', panelTab3d: null, styleLockRequest: null, styleLockOpen: false });
  });

  it('switches the right panel to 功能, opens the section and clears the request', async () => {
    render(<RightPanel />);
    expect(screen.getByTestId('right-panel-tab-layers').getAttribute('aria-selected')).toBe('true');
    act(() => { useStudioStore.getState().requestStyleLock(); });
    await waitFor(() => expect(screen.getByTestId('right-panel-tab-operations').getAttribute('aria-selected')).toBe('true'));
    expect(useStudioStore.getState().styleLockOpen).toBe(true);
    expect(useStudioStore.getState().styleLockRequest).toBeNull();
    expect(screen.getByTestId('stub-style-lock')).toBeTruthy();
  });
});