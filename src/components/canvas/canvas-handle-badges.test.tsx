import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { CanvasHandleBadges } from '@/components/canvas/canvas-handle-badges';
import type { CanvasNode } from '@/store/canvas-store';

const picture = (id: string, patch: Partial<CanvasNode> = {}): CanvasNode => ({
  id, type: 'image', imageUrl: `/api/v1/tasks/t/assets/${id}`, taskId: 't', x: 100, y: 50, width: 400, height: 300, zIndex: 1, ...patch,
});
const viewport = { zoom: 0.5, panX: 10, panY: 20 };
const render = (nodes: CanvasNode[], handles: Record<string, string>, zoom = viewport.zoom) => renderToStaticMarkup(
  <CanvasHandleBadges nodes={nodes} handles={new Map(Object.entries(handles))} viewport={{ ...viewport, zoom }} minimumScreenPixels={48} />,
);

describe('chat handle badges on the canvas (#87)', () => {
  it('puts the handle without its marker at the layer\'s top-right corner, out of the pointer\'s way', () => {
    const html = render([picture('a'), picture('b')], { a: '@img25' });
    expect(html).toContain('pointer-events-none');
    expect(html).toContain('aria-hidden="true"');
    expect(html.match(/data-testid="canvas-handle-badge"/g)).toHaveLength(1);
    expect(html).toMatch(/data-node="a"[^>]*>img25</);
    // panX + (x + width) * zoom = 10 + 500 * 0.5; panY + y * zoom = 20 + 25.
    expect(html).toContain('left:260px;top:45px');
  });

  it('turns with the layer about its top-left point', () => {
    const html = render([picture('a', { rotation: 90 })], { a: '@img25' });
    // The top-right corner of a layer turned 90° lies straight below its top-left point.
    expect(html).toMatch(/left:60(\.0+\d*)?px;top:245px;transform:rotate\(90deg\)/);
  });

  it('shows nothing for hidden layers or layers too small on screen', () => {
    expect(render([picture('a', { visible: false })], { a: '@img25' })).toBe('');
    expect(render([picture('a', { width: 80, height: 80 })], { a: '@img25' })).toBe('');
    expect(render([picture('a', { width: 80, height: 80 })], { a: '@img25' }, 1)).toContain('img25');
    expect(render([picture('a')], {})).toBe('');
  });
});
