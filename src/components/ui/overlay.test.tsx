// @vitest-environment jsdom
import React from 'react';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';

import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from './dropdown-menu';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './select';
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip';

// An absolutely positioned popup that is briefly placed past the viewport edge extends the document; with
// classic scrollbars Base UI's scroll lock can then catch <html> with a scrollbar and reserve an empty
// `scrollbar-gutter` strip while the popup is open, shifting the workbench left. Every anchored positioner is
// therefore fixed (overlay.ts). jsdom has no layout, so the widths themselves are measured by
// scripts/smoke/overlay-scroll-lock-geometry.mjs in a real browser; here the cause is pinned.
afterEach(cleanup);

const positionerOf = (element: HTMLElement) => {
  let node: HTMLElement | null = element;
  while (node && node !== document.body && !node.style.position) node = node.parentElement;
  return node;
};

const settle = () => act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); });

it('positions an open select popup with fixed positioning, also when it does not overlay the trigger', async () => {
  render(
    <Select defaultOpen defaultValue="b">
      <SelectTrigger><SelectValue /></SelectTrigger>
      <SelectContent alignItemWithTrigger={false}>
        <SelectItem value="a">A</SelectItem>
        <SelectItem value="b">B</SelectItem>
      </SelectContent>
    </Select>,
  );
  await settle();
  expect(positionerOf(screen.getByRole('listbox'))?.style.position).toBe('fixed');
});

it('positions an open dropdown menu with fixed positioning', async () => {
  render(
    <DropdownMenu defaultOpen>
      <DropdownMenuTrigger>open</DropdownMenuTrigger>
      <DropdownMenuContent><DropdownMenuItem>item</DropdownMenuItem></DropdownMenuContent>
    </DropdownMenu>,
  );
  await settle();
  expect(positionerOf(screen.getByRole('menu'))?.style.position).toBe('fixed');
});

it('positions an open tooltip with fixed positioning', async () => {
  render(
    <Tooltip defaultOpen>
      <TooltipTrigger>hover</TooltipTrigger>
      <TooltipContent>tip</TooltipContent>
    </Tooltip>,
  );
  await settle();
  expect(positionerOf(screen.getByText('tip'))?.style.position).toBe('fixed');
});

const sourceFiles = (dir: string): string[] => readdirSync(dir).flatMap((name) => {
  const file = path.join(dir, name);
  if (statSync(file).isDirectory()) return sourceFiles(file);
  return /\.tsx$/.test(name) && !/\.test\.tsx$/.test(name) ? [file] : [];
});

it('gives every anchored positioner in the app the shared fixed position method', () => {
  const root = path.resolve(__dirname, '../..');
  const missing: string[] = [];
  let seen = 0;
  for (const file of sourceFiles(root)) {
    const source = readFileSync(file, 'utf8');
    // Base UI always positions a context menu as fixed.
    for (const match of source.matchAll(/<(\w+)\.Positioner\b[^>]*>/g)) {
      if (match[1] === 'ContextMenuPrimitive') continue;
      seen += 1;
      if (!match[0].includes('positionMethod={anchoredPositionMethod}')) missing.push(`${path.relative(root, file)}: ${match[0].slice(0, 60)}`);
    }
  }
  expect(seen).toBeGreaterThan(5);
  expect(missing).toEqual([]);
});
