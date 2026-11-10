// @vitest-environment jsdom

import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { PromoteAssetDialog } from './promote-asset-dialog';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));

afterEach(cleanup);

describe('promote asset dialog', () => {
  it('requires a name and sends nothing before the explicit confirm', () => {
    const onConfirm = vi.fn();
    render(
      <PromoteAssetDialog open source={{ task_id: 't1', asset_id: 'a1' }} previewURL="/pic.png" onCancel={() => {}} onConfirm={onConfirm} />,
    );
    expect(screen.getByTestId('promote-asset-preview').querySelector('img')?.getAttribute('src')).toBe('/pic.png');
    expect((screen.getByTestId('promote-asset-confirm') as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByTestId('promote-asset-name'), { target: { value: '角色 A' } });
    fireEvent.change(screen.getByTestId('promote-asset-description'), { target: { value: '标准形象' } });
    expect(onConfirm).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('promote-asset-confirm'));
    expect(onConfirm).toHaveBeenCalledWith({ kind: 'character', name: '角色 A', description: '标准形象' });
  });

  it('keeps the dialog and input when the server refuses, and renders the refusal', () => {
    const onCancel = vi.fn();
    render(
      <PromoteAssetDialog open source={{ input_asset_id: 'u1' }} busy={false} error="只能提升自己的已完成任务产物或上传的图片。"
        onCancel={onCancel} onConfirm={() => {}} />,
    );
    fireEvent.change(screen.getByTestId('promote-asset-name'), { target: { value: 'Role A' } });
    expect(screen.getByRole('alert').textContent).toContain('只能提升自己的已完成任务产物或上传的图片。');
    expect((screen.getByTestId('promote-asset-name') as HTMLInputElement).value).toBe('Role A');
    fireEvent.click(screen.getByRole('button', { name: messages['ui.project.cancel'] }));
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it('draws nothing while closed', () => {
    render(<PromoteAssetDialog open={false} source={null} onCancel={() => {}} onConfirm={() => {}} />);
    expect(screen.queryByTestId('promote-asset-dialog')).toBeNull();
  });
});