import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

/**
 * #280: the canvas right-click menu flashed and closed. It was a plain DropdownMenu opened by hand at a hidden
 * 1px trigger; Base UI's plain Menu dismisses on hover-out (`trigger-hover` on the popup's `mouseleave`, which
 * the open animation produced), and a hand-set `open` is not a click-like open that would suppress it. Base UI's
 * ContextMenu turns that hover dismissal off for its popup and anchors it at the pointer, so the board uses it.
 * The real open / stay / close behaviour is checked in Chromium and WebKit by scripts/test-canvas-menu-inpaint.mjs.
 */
const board = readFileSync(new URL('./canvas-board.tsx', import.meta.url), 'utf8');
const wrapper = readFileSync(new URL('../ui/context-menu.tsx', import.meta.url), 'utf8');

describe('canvas right-click menu (#280)', () => {
  it('uses the ContextMenu primitive, not a DropdownMenu opened by hand at a hidden trigger', () => {
    expect(board).toContain("from '@/components/ui/context-menu'");
    expect(board).not.toContain("from '@/components/ui/dropdown-menu'");
    expect(board).not.toMatch(/<DropdownMenu[\s>]/);
    expect(board).not.toMatch(/pointer-events-none fixed size-px opacity-0/);
    expect(board).toContain('<ContextMenuContent data-testid="canvas-context-menu"');
  });

  it('makes the stage itself the trigger, so the menu opens at the pointer and only for the stage', () => {
    const root = board.indexOf('<ContextMenu\n');
    const trigger = board.indexOf('<ContextMenuTrigger render={<div className="contents"');
    const stage = board.indexOf('<Stage\n');
    const stageEnd = board.indexOf('</Stage>');
    const triggerEnd = board.indexOf('</ContextMenuTrigger>');
    const content = board.indexOf('<ContextMenuContent');
    const rootEnd = board.indexOf('</ContextMenu>');
    expect([root, trigger, stage, stageEnd, triggerEnd, content, rootEnd].every((index) => index > -1)).toBe(true);
    expect(root).toBeLessThan(trigger);
    expect(trigger).toBeLessThan(stage);
    expect(stageEnd).toBeLessThan(triggerEnd);
    expect(triggerEnd).toBeLessThan(content);
    expect(content).toBeLessThan(rootEnd);
    // The tool rail, option bar and zoom controls (with menus of their own) stay outside the context-menu root.
    for (const outside of ['<CanvasToolRail', '<CanvasToolOptions', '<CanvasZoomControls']) {
      expect(board.indexOf(outside)).toBeGreaterThan(rootEnd);
    }
  });

  it('opens only for the select tool and reads the layer the stage hit when it opens', () => {
    expect(board).toMatch(/<ContextMenu\s+open=\{contextMenu !== null\}\s+disabled=\{activeTool !== 'select'\}/);
    expect(board).toContain('onOpenChange={(open) => setContextMenu(open ? (contextMenuTarget.current ?? { onNode: false }) : null)}');
    expect(board).toContain('contextMenuTarget.current = { onNode: Boolean(hit) };');
    // The stage still prevents the browser menu whatever the tool, and no longer positions the menu itself.
    expect(board).toMatch(/onContextMenu=\{\(event\) => \{\s*event\.evt\.preventDefault\(\);\s*if \(activeTool !== 'select'\) return;/);
    expect(board).not.toContain('x: event.evt.clientX, y: event.evt.clientY');
  });

  it('keeps the align submenu and the destructive delete item inside the context menu', () => {
    expect(board).toContain('<ContextMenuSubTrigger disabled={!item.enabled} data-testid="canvas-menu-align">');
    expect(board).toContain("variant={item.id === 'delete' ? 'destructive' : 'default'}");
    expect(wrapper).toContain('variant?: "default" | "destructive"');
    expect(wrapper).toMatch(/DropdownMenuSub as ContextMenuSub,/);
    expect(wrapper).toContain('ContextMenuPrimitive.Trigger');
  });
});
