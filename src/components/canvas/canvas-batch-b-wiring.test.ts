import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { CANVAS_TOOL_IDS } from '@/lib/workspace/tools';

const read = (relative: string) => readFileSync(new URL(relative, import.meta.url), 'utf8');

/** The source without comments and string-free of nothing else: user text must not be written in components. */
function withoutComments(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
}

const BATCH_B_COMPONENTS = [
  'canvas-board.tsx',
  'canvas-tool-options.tsx',
  'canvas-tool-rail.tsx',
  'canvas-zoom-controls.tsx',
  'history-panel.tsx',
  'layers-panel.tsx',
  '../studio/attachment-thumb.tsx',
  '../studio/object-details.tsx',
  '../studio/right-panel.tsx',
  '../studio/tool-rail.tsx',
  '../studio/stage-3d-views.tsx',
  '../workspace/workspace-canvas-board.tsx',
];

describe('batch B wiring', () => {
  it.each(BATCH_B_COMPONENTS)('%s carries no user text of its own (the Product Schema supplies it)', (file) => {
    expect(withoutComments(read(file))).not.toMatch(/[一-鿿]/);
  });

  describe('the left tool rail', () => {
    const adapter = read('canvas-tool-rail.tsx');
    // The adapter proper: everything after the exported RailButton (which the zoom bar and option bar still use).
    const rail = adapter.slice(adapter.indexOf('export function CanvasToolRail('));

    it('is the thin 2D adapter of the shared ToolRail: it binds handlers and keeps no button list of its own', () => {
      expect(rail).toContain('<ToolRail');
      expect(rail).toContain('rail="canvas"');
      expect(rail).toContain('testId="canvas-tool-rail"');
      expect(rail).toContain('const handlers: Record<CanvasToolId, ToolHandler> = {');
      // Names, icons, hints and shortcuts come from canvas.frontend.tools, so none is written here.
      expect(rail).not.toMatch(/title=|aria-label=|withShortcut|keyboard\./);
      expect(rail).not.toMatch(/ui\.canvas\.toolbar\.(select|hand|marquee|lasso|mask|eyedropper|zoom_tool|upload|text|pencil|brush|eraser|crop|snap|undo|redo)\b/);
      expect(adapter).not.toMatch(/MousePointer2|Pipette|Wand2|Magnet|Undo2|Redo2|Lasso|Scissors/);
    });

    it('binds a handler to every tool id the code knows, in an object typed by the id list', () => {
      for (const id of CANVAS_TOOL_IDS) {
        expect(rail, id).toMatch(new RegExp(`\\b${id}: `));
      }
    });

    it('keeps the shape flyout, the snap toggle, the undo / redo reasons and the mask-unsupported click', () => {
      expect(rail).toContain('icon: ActiveShape');
      expect(rail).toContain('slotRef: shapesRef');
      expect(rail).toContain('flyout: shapesOpen');
      expect(rail).toContain('snap: { run: () => setSnapEnabled(!snapEnabled), pressed: snapEnabled }');
      expect(rail).toContain('disabled: !canUndo');
      expect(rail).toContain('disabled: !canRedo');
      expect(rail).toContain("text('ui.canvas.toolbar.undo_empty')");
      expect(rail).toContain("text('ui.canvas.toolbar.redo_empty')");
      // The mask tool stays clickable on a model without masks; the hint leads with the reason.
      expect(rail).toContain('onMaskUnsupported?.();');
      expect(rail).toContain("reason: maskSupported ? undefined : text('ui.canvas.mask.unsupported_tooltip')");
      expect(rail).toContain('disabled: !maskSupported && !onMaskUnsupported');
    });

    it('shares the expanded setting with the 3D view bar and is placed at the stage\'s top-left', () => {
      expect(rail).toContain('useRailExpanded()');
      expect(read('../studio/stage-3d-views.tsx')).toContain('useRailExpanded()');
      expect(rail).toContain("cn('left-3 top-3', className)");
    });

    it('keeps RailButton and SHAPE_ICONS exported for the zoom bar and the option bar', () => {
      expect(adapter).toContain('export function RailButton(');
      expect(adapter).toContain('export const SHAPE_ICONS');
      expect(read('canvas-zoom-controls.tsx')).toContain("import { RailButton } from '@/components/canvas/canvas-tool-rail';");
      expect(read('canvas-tool-options.tsx')).toContain("import { SHAPE_ICONS, RailButton } from '@/components/canvas/canvas-tool-rail';");
    });

    it('lets the option bar and the stage fit clear the rail whether it is collapsed or expanded', () => {
      expect(read('canvas-tool-options.tsx')).toContain('railClearance(useStudioStore((state) => state.railExpanded))');
      expect(read('canvas-tool-options.tsx')).toContain('style={{ left: barLeft');
      const workspace = read('../workspace/workspace-canvas-board.tsx');
      expect(workspace).toContain('const insets = stageFitInsets(railExpanded);');
      expect(workspace).toContain('railExpanded, selectionQuiet, selectionRestored, sessionID, viewportConfig]');
      // #246: fitting the dashed frame (opening, 回到画布, 适应屏幕, Ctrl+0, zoom to selection) clears the rail too.
      const board = read('canvas-board.tsx');
      expect(board).toContain('const fitInsets = useMemo(() => stageFitInsets(railExpanded), [railExpanded]);');
      expect(board).toContain('initialArtboardViewport(artboard, size, canvasSchema.frontend.viewport, fitInsets)');
      expect(board).toContain('canvasSchema.frontend.viewport, fitInsets,\n    );');
      expect(board).toContain('fitViewportToRect(rect, size, viewportConfig, fitInsets)');
    });
  });

  it('reads every tool key from the Schema through resolveToolKey, with no letters of its own', () => {
    const board = withoutComments(read('canvas-board.tsx'));
    expect(board).toContain('resolveToolKey(event, keyboard');
    expect(board).not.toMatch(/rectangle_tool_key|pencil_tool_key|line_tool_key|ellipse_tool_key|keyboard\.generate_key/);
  });

  it('routes 回到画布 through the visible content, while 适应屏幕 / Ctrl+0 still fit the frame', () => {
    const board = read('canvas-board.tsx');
    expect(board).toContain('visibleContentBounds(nodes, canvasSchema)');
    expect(board).toContain('fitContentOrArtboardViewport(');
    expect(board).toContain('onRecenter={artboard ? recenter : undefined}');
    // Space and the menu call the same recenter, so the Space hint stays truthful.
    expect(board).toContain('recenterGesture.current.release(performance.now())) recenterRef.current()');
    // Ctrl+0 and the fit button keep the artboard rule.
    expect(board).toContain('fitArtboardViewport(artboard, size, viewportConfig, fitInsets)');
  });

  it('fits the frame with Ctrl+0 through the same function as the fit button', () => {
    expect(read('canvas-board.tsx')).toContain('fitArtboardViewport(artboard, size, viewportConfig, fitInsets)');
    expect(read('canvas-zoom-controls.tsx')).toContain('fitArtboardViewport(artboard, drawingSize(), viewportSchema, stageFitInsets(railExpanded))');
  });

  it('shows the shortcut caps in the hover hints of the zoom, fit and recenter buttons', () => {
    const controls = read('canvas-zoom-controls.tsx');
    expect(controls).toContain('descriptorCaps(`mod+${keyboard.recenter_key}`, text)');
    // A Space tap only recenters while the stage shows no picture, so the Space caps go while that tap is taken.
    expect(controls).toContain("spaceTapTaken?.() ? undefined : shortcutCaps(schema, 'space_code')");
    expect(controls).toContain("shortcutCaps(schema, 'zoom_in_shortcut')");
    expect(controls).toContain("shortcutCaps(schema, 'zoom_out_shortcut')");
    expect(controls).toContain("shortcutCaps(schema, 'zoom_actual_shortcut')");
    expect(controls).toContain('<HintTooltip name={text(\'ui.canvas.toolbar.reset\')}');
    expect(controls).not.toMatch(/\btitle=|withShortcut|recenterTooltip/);
  });

  it('opens the hint of a RailButton below it, and to the right only from the shape flyout column', () => {
    const rail = read('canvas-tool-rail.tsx');
    expect(rail).toContain('<HintTooltip name={label} side={hintSide} {...hint}>');
    expect(rail).not.toMatch(/\btitle=/);
    expect(rail.match(/hintSide="right"/g)).toHaveLength(1);
    expect(rail.indexOf('hintSide="right"')).toBeGreaterThan(rail.indexOf('flyout: shapesOpen'));
    for (const file of ['canvas-zoom-controls.tsx', 'canvas-tool-options.tsx']) expect(read(file)).not.toContain('hintSide');
  });

  it('lets Esc drop a painted / marquee / lasso selection before it leaves the tool, behind the mask tool exit', () => {
    const board = read('canvas-board.tsx');
    const mask = board.indexOf('event.key === keyboard.mask_exit_key');
    const selection = board.indexOf("key === 'escape' && canvasActive && !event.defaultPrevented && activeTool !== 'mask'");
    const tool = board.indexOf("if (activeTool !== 'select') onToolChange('select');");
    expect(mask).toBeGreaterThan(-1);
    expect(selection).toBeGreaterThan(mask);
    expect(tool).toBeGreaterThan(selection);
  });

  it('keeps the dashed frame, the dimmed outside and the marching ants out of the pointer\'s way', () => {
    const board = read('canvas-board.tsx');
    expect(board).toContain('outsideDimRects(visibleWorld(viewport, size');
    expect(board).toMatch(/<Layer listening=\{false\}>\s*\{artboard\s*\?\s*outsideDimRects/);
    expect(board).toContain('dash={selectionSchema.ants_dash}');
  });

  it('offers the align commands as one submenu, and no send-to-chat or send-to-3D item in the right-click menu', () => {
    const board = read('canvas-board.tsx');
    expect(board).toContain('<ContextMenuSub>');
    expect(board).not.toMatch(/onSendTo3D|onSendToChat|hasSendTo3DHandler|send_to_3d|send_to_chat/);
  });

  it('keeps layers and history in the right panel: the stage mounts no panel of its own and F7 / Alt+F9 only switch tabs', () => {
    const workspace = read('../workspace/workspace-canvas-board.tsx');
    expect(workspace).not.toContain('CanvasSidePanel');
    expect(workspace).not.toContain('stageWidth');
    const panel = read('../studio/right-panel.tsx');
    expect(panel).toContain('<CanvasLayersPanel sessionId={sessionId} />');
    expect(panel).toContain('<CanvasHistoryPanel sessionId={sessionId} />');
    const board = withoutComments(read('canvas-board.tsx'));
    expect(board).toContain('panelShortcutTabs(schema)');
    expect(board).toContain("useStudioStore.getState().setPanelTab('image', panelShortcut.id)");
    // The old open / close / strip behaviour is gone.
    expect(board).not.toContain('setCanvasPanel');
    expect(board).not.toContain('afterLayersKey');
    expect(board).not.toContain('afterHistoryKey');
  });

  it('leaves 发送到对话 and 转 3D to the stage toolbar: the board hands the canvas no such handler', () => {
    const workspace = read('../workspace/workspace-canvas-board.tsx');
    expect(workspace).not.toMatch(/onSendTo3D|onSendToChat|conversionAllowed|sendToThreeD|canSendTo3D/);
  });

  it('lets Space + drag pan the view even when the drag starts on a picture or the crop frame', () => {
    const board = withoutComments(read('canvas-board.tsx'));
    expect(board).toContain('draggable={pansView(activeTool, spaceDown)}');
    expect(board).toContain('draggable: layerDraggable(activeTool, spaceDown, node.locked)');
    expect(board).not.toMatch(/draggable: activeTool === 'select'/);
    // The crop rectangle is a draggable shape too: while Space is held it must not take the drag.
    expect(board).toMatch(/id="crop-rect"[\s\S]*?draggable=\{!spaceDown\}/);
  });
});

describe('the mask on the board (#310)', () => {
  const board = withoutComments(read('canvas-board.tsx'));

  it('draws the whole selection, including the stroke being painted, as one overlay at the Schema tint', () => {
    expect(board).toContain('const maskCurrent = maskState?.current;');
    expect(board).toContain('maskOverlaySteps(maskStrokes, maskCurrent)');
    expect(board).toMatch(/name="mask-overlay"[\s\S]*?drawMaskOverlay\(/);
    // The mask has its own Schema tint (the pre-#310 single-stroke look), not the lighter selection tint.
    expect(board).toMatch(/readCanvasToken\(maskSchema\.tint_color_token, palette\.handle\)/);
    expect(board).toContain('tintOpacity: maskSchema.tint_opacity');
    // A pure marquee / lasso selection keeps the selection tint.
    expect(board).toMatch(/overlayTintKind\(maskOverlay\) === 'mask'/);
    expect(board).toContain('tintOpacity: selectionSchema.tint_opacity');
    // No translucent line per stroke: those stack and darken where strokes overlap.
    expect(board).not.toMatch(/maskState\.(strokes\.map|current \?)[\s\S]{0,200}<Line/);
  });

  it('shows the mask brush footprint before and while painting, sized by the brush and the zoom', () => {
    expect(board).toMatch(/if \(activeTool === 'mask'\) setMaskHover\(\{ x: world\.x, y: world\.y \}\)/);
    expect(board).toMatch(/name="mask-brush-cursor"[\s\S]*?radiusX=\{brushCursorRadius\(brushSize, viewport\.zoom\)\}/);
  });
});
