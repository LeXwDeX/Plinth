// @vitest-environment jsdom

import React from 'react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { Model3DConfirmHost } from '@/components/studio/model3d-confirm-dialog';
import { useComposerSettingsStore } from '@/store/composer-settings-store';
import { useModel3DConfirmStore } from '@/store/model3d-confirm-store';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));

afterEach(cleanup);

describe('#678: 使用 {模型} 转 3D？', () => {
  const run = vi.fn();
  beforeEach(() => {
    run.mockReset();
    useComposerSettingsStore.setState({ open: false, tab: 'params' });
    render(<Model3DConfirmHost />);
    act(() => useModel3DConfirmStore.getState().ask({ modelId: 'm1', modelName: 'Model One', cost: '约 30 积分', run }));
  });

  it('names the model and the cost', () => {
    expect(screen.getByText('使用 Model One 转 3D？')).toBeTruthy();
    expect(screen.getByTestId('model3d-confirm-cost').textContent).toBe('约 30 积分');
  });

  it('确认 runs the one conversion with that model and closes', () => {
    fireEvent.click(screen.getByTestId('model3d-confirm-ok'));
    expect(run).toHaveBeenCalledWith('m1');
    expect(useModel3DConfirmStore.getState().request).toBeNull();
  });

  it('换一个… opens the models settings and runs nothing', () => {
    fireEvent.click(screen.getByTestId('model3d-confirm-change'));
    expect(run).not.toHaveBeenCalled();
    expect(useModel3DConfirmStore.getState().request).toBeNull();
    expect(useComposerSettingsStore.getState()).toMatchObject({ open: true, tab: 'models' });
  });
});
