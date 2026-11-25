// @vitest-environment jsdom
import React from 'react';
import { readFileSync } from 'node:fs';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import { ObjectThumbImage } from '@/components/studio/object-thumb';

const source = (relative: string) => readFileSync(new URL(relative, import.meta.url), 'utf8');

afterEach(cleanup);

// #624: a 3D version with no preview, or a preview that does not load, shows a clear placeholder, not a black square.
describe('object thumbnail', () => {
  it('shows the picture when there is one', () => {
    render(<ObjectThumbImage kind="model" src="/api/v1/tasks/t1/assets/thumb" />);
    expect(screen.getByTestId('object-thumb').getAttribute('data-state')).toBe('image');
    expect(screen.getByTestId('object-thumb').querySelector('img')?.getAttribute('src')).toBe('/api/v1/tasks/t1/assets/thumb');
  });

  it('shows the 3D placeholder on a muted ground when a model has no preview', () => {
    render(<ObjectThumbImage kind="model" src="" />);
    const thumb = screen.getByTestId('object-thumb');
    expect(thumb.getAttribute('data-state')).toBe('placeholder');
    expect(thumb.getAttribute('data-kind')).toBe('model');
    expect(thumb.className).toMatch(/\bbg-muted\b/);
    expect(thumb.querySelector('img')).toBeNull();
    expect(thumb.querySelector('svg.lucide-box')).not.toBeNull();
  });

  it('falls back to the placeholder when the preview does not load', () => {
    render(<ObjectThumbImage kind="model" src="/broken.png" />);
    const picture = screen.getByTestId('object-thumb').querySelector('img');
    expect(picture).not.toBeNull();
    fireEvent.error(picture!);
    expect(screen.getByTestId('object-thumb').getAttribute('data-state')).toBe('placeholder');
    expect(screen.getByTestId('object-thumb').querySelector('svg.lucide-box')).not.toBeNull();
  });

  it('tries again when another preview is given', () => {
    const { rerender } = render(<ObjectThumbImage kind="model" src="/broken.png" />);
    fireEvent.error(screen.getByTestId('object-thumb').querySelector('img')!);
    rerender(<ObjectThumbImage kind="model" src="/fine.png" />);
    expect(screen.getByTestId('object-thumb').getAttribute('data-state')).toBe('image');
  });

  it('uses a picture placeholder for a 2D object', () => {
    render(<ObjectThumbImage kind="image" src="" />);
    expect(screen.getByTestId('object-thumb').querySelector('svg.lucide-image')).not.toBeNull();
  });

  it('is what the composer object chip and the version tile show', () => {
    const composer = source('./composer.tsx');
    const strip = source('./version-strip.tsx');
    expect(composer).toContain("from '@/components/studio/object-thumb'");
    expect(composer).toMatch(/<ObjectThumbImage kind=\{chip\.kind\} src=\{chip\.thumbnail\}/);
    expect(composer).not.toContain('{chip.thumbnail ? <img');
    expect(strip).toContain("from '@/components/studio/object-thumb'");
    expect(strip).toMatch(/<ObjectThumbImage kind=\{tile\.domain\}/);
  });
});
