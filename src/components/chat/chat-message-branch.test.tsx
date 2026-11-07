// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: defaultCatalog.application }) }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }) }));

import { ChatMessage } from './chat-message';

afterEach(cleanup);

it.each(['user', 'assistant'] as const)('offers a branch from a settled %s message', async (role) => {
  const branch = vi.fn();
  const message = { id: 'm', role, content: 'hello', createdAt: 1 };
  const view = render(<ChatMessage message={message} onBranch={branch} />);
  fireEvent.click(view.getByTestId('chat-more'));
  const item = await view.findByTestId('chat-branch');
  expect(item.textContent).toContain('分支到新对话');
  expect(item.textContent).toContain('创建分支时的当前状态');
  fireEvent.click(item);
  expect(branch).toHaveBeenCalledWith(message);
});

it('shows the reason and refuses the action while a turn is active', async () => {
  const branch = vi.fn();
  const view = render(<ChatMessage message={{ id: 'm', role: 'user', content: 'hello', createdAt: 1 }} onBranch={branch} branchDisabledReason="回复或任务尚未结束" />);
  fireEvent.click(view.getByTestId('chat-more'));
  const item = await view.findByTestId('chat-branch');
  expect(item.getAttribute('aria-disabled')).toBe('true');
  expect(item.textContent).toContain('回复或任务尚未结束');
  fireEvent.click(item);
  expect(branch).not.toHaveBeenCalled();
});
