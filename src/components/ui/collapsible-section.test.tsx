import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';

import { CollapsibleSection } from './collapsible-section';

vi.stubGlobal('React', React);

it('points its chevron row at the body and hides a closed body', () => {
  const open = renderToStaticMarkup(
    <CollapsibleSection id="body" title="功能" summary="重拓扑" open onOpenChange={() => {}}><p>内容</p></CollapsibleSection>,
  );
  expect(open).toContain('aria-expanded="true"');
  expect(open).toContain('aria-controls="body"');
  expect(open).toContain('重拓扑');
  expect(open).not.toContain('hidden=""');
  expect(open).toContain('<div id="body" class="border-t border-border"><p>内容</p></div>');

  const closed = renderToStaticMarkup(
    <CollapsibleSection id="body" title="功能" open={false} onOpenChange={() => {}}><p>内容</p></CollapsibleSection>,
  );
  expect(closed).toContain('aria-expanded="false"');
  expect(closed).toContain('hidden=""');
});