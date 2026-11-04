'use client';

import { memo } from 'react';

import { handleLabel } from '@/lib/workspace/object-handle';
import type { CanvasNode } from '@/store/canvas-store';

/**
 * #87: the chat handle ("img25") of every picture layer that shows a picture of the chat, in the layer's
 * top-right corner, so the pictures on the board can be matched with the handles the Agent writes. The
 * badges are DOM over the stage, never Konva nodes: an export or a snapshot rasterises the layers
 * themselves and cannot pick them up. They take no pointer events, keep their screen size at any zoom,
 * turn with the layer and are left out for layers too small on screen (`minimumScreenPixels`).
 */
export const CanvasHandleBadges = memo(function CanvasHandleBadges({
  nodes,
  handles,
  viewport,
  minimumScreenPixels,
}: {
  nodes: readonly CanvasNode[];
  handles: ReadonlyMap<string, string>;
  viewport: { zoom: number; panX: number; panY: number };
  minimumScreenPixels: number;
}) {
  if (handles.size === 0) return null;
  const shown = nodes.filter((node) => handles.has(node.id)
    && node.visible !== false
    && Math.min(node.width, node.height) * viewport.zoom >= minimumScreenPixels);
  if (shown.length === 0) return null;
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0 overflow-hidden" data-testid="canvas-handle-badges">
      {shown.map((node) => {
        const angle = ((node.rotation ?? 0) * Math.PI) / 180;
        // The layer's own top-right corner (Konva turns a node about its top-left point).
        const left = viewport.panX + (node.x + node.width * Math.cos(angle)) * viewport.zoom;
        const top = viewport.panY + (node.y + node.width * Math.sin(angle)) * viewport.zoom;
        return (
          <div
            key={node.id}
            className="absolute size-0"
            style={{ left, top, transform: node.rotation ? `rotate(${node.rotation}deg)` : undefined, transformOrigin: '0 0' }}
          >
            <span
              data-testid="canvas-handle-badge"
              data-node={node.id}
              className="absolute right-1 top-1 whitespace-nowrap rounded-sm bg-black/60 px-1 py-0.5 font-mono text-[10px] leading-none text-white"
            >
              {handleLabel(handles.get(node.id) ?? '')}
            </span>
          </div>
        );
      })}
    </div>
  );
});
