import { readFileSync } from 'node:fs';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { renameKeyIntent, resolveRename } from '@/lib/chat/session-rename';

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application }),
}));

import { SessionTitle } from './session-menu';

const session = { id: 's1', title: '银色机械猫', createdAt: '', updatedAt: '' };

describe('session title', () => {
  it('shows the name as plain text with a separate pencil button', () => {
    const html = renderToStaticMarkup(<SessionTitle session={session} />);
    const name = html.match(/<span[^>]*data-testid="studio-session-name"[^>]*>/)?.[0] ?? '';
    expect(name).not.toBe('');
    expect(html).not.toMatch(/<button[^>]*data-testid="studio-session-name"/);
    expect(html).toMatch(/<button[^>]*aria-label="重命名主题：银色机械猫"[^>]*data-testid="studio-session-rename"/);
    expect(html).not.toContain('<input');
  });

  it('wires only the pencil to the rename draft', () => {
    const source = readFileSync(new URL('./session-menu.tsx', import.meta.url), 'utf8');
    expect(source.match(/setDraft\(session\.title\)/g)).toHaveLength(1);
    const name = source.slice(source.indexOf('data-testid="studio-session-name"') - 200, source.indexOf('data-testid="studio-session-name"'));
    expect(name).not.toContain('onClick');
  });
});

describe('rename keys', () => {
  const key = (value: string, extra = {}) => ({ key: value, isComposing: false, keyCode: 0, ...extra });

  it('saves on Enter and cancels on Escape', () => {
    expect(renameKeyIntent(key('Enter'))).toBe('save');
    expect(renameKeyIntent(key('Escape'))).toBe('cancel');
    expect(renameKeyIntent(key('a'))).toBeNull();
  });

  it('ignores keys that belong to an input method composition', () => {
    expect(renameKeyIntent(key('Enter', { isComposing: true }))).toBeNull();
    expect(renameKeyIntent(key('Enter', { keyCode: 229 }))).toBeNull();
  });

  it('saves a changed trimmed name and cancels an empty or unchanged one', () => {
    expect(resolveRename('  新名字 ', '旧名字')).toEqual({ kind: 'save', title: '新名字' });
    expect(resolveRename('旧名字', '旧名字')).toEqual({ kind: 'cancel' });
    expect(resolveRename('   ', '旧名字')).toEqual({ kind: 'cancel' });
  });
});
